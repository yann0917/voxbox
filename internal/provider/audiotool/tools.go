// audiotool 工具集：切割、合并、变调变速三件「结构性剪辑」工具与共享基建。
// 参数构建（纯函数）与进程执行分离，构建层全部可单测。
package audiotool

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// outDir 由 RegisterAll 注入（= dataDir），产物写到 <outDir>/editor/<reqID>/。
type baseTool struct {
	outDir string
}

// ---------- 共享参数 ----------

// formatOpts 输出格式枚举（auto=跟随源容器）。转码类操作的通用输出段。
var formatOpts = []provider.ParamOption{
	{Value: "auto", Label: "跟随源格式"},
	{Value: "mp3", Label: "MP3"},
	{Value: "m4a", Label: "M4A (AAC)"},
	{Value: "wav", Label: "WAV (无损)"},
	{Value: "flac", Label: "FLAC (无损)"},
	{Value: "ogg", Label: "OGG (Vorbis)"},
}

var bitrateOpts = []provider.ParamOption{
	{Value: "128k", Label: "128 kbps（标准）"},
	{Value: "192k", Label: "192 kbps（高品质）"},
	{Value: "256k", Label: "256 kbps"},
	{Value: "320k", Label: "320 kbps（最高）"},
}

// outputSpec 输出格式/码率参数（各工具共用）。
type outputSpec struct {
	Format  string `json:"format"`
	Bitrate string `json:"bitrate"`
}

func outputSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "format", Label: "输出格式", Type: provider.ParamEnum, Default: "auto", Group: "输出", Options: formatOpts},
		{Key: "bitrate", Label: "码率", Type: provider.ParamEnum, Default: "192k", Group: "输出", Options: bitrateOpts},
	}
}

func (s outputSpec) resolve(srcPath string) (ext, codec, bitrate string) {
	ext = resolveOutputExt(s.Format, srcPath)
	codec, err := audioCodecFor(ext)
	if err != nil {
		codec, ext = "libmp3lame", "mp3"
	}
	return ext, codec, normalizeBitrateStr(s.Bitrate)
}

func normalizeExt(ext string) string {
	return strings.ToLower(strings.TrimLeft(strings.TrimSpace(ext), "."))
}

// audioCodecFor 扩展名 → 编码器。无损编码不接受 -b:a。
func audioCodecFor(ext string) (string, error) {
	switch normalizeExt(ext) {
	case "mp3":
		return "libmp3lame", nil
	case "m4a", "aac", "mp4", "mov":
		return "aac", nil
	case "wav":
		return "pcm_s16le", nil
	case "flac":
		return "flac", nil
	case "ogg", "oga":
		return "libvorbis", nil
	case "opus":
		return "libopus", nil
	default:
		return "libmp3lame", nil // 未知扩展名回落 MP3
	}
}

func isLossless(codec string) bool {
	switch codec {
	case "pcm_s16le", "pcm_s24le", "pcm_f32le", "flac", "alac":
		return true
	}
	return false
}

func normalizeBitrateStr(b string) string {
	b = strings.TrimSpace(strings.ToLower(b))
	if b == "" {
		return "192k"
	}
	if _, err := strconv.Atoi(strings.TrimSuffix(b, "k")); err != nil {
		return "192k"
	}
	return b
}

// encodeTail 输出侧编码参数（-vn 丢封面轨；无损不给 -b:a）。
func encodeTail(ext, codec, bitrate string) []string {
	args := []string{"-c:a", codec}
	if bitrate != "" && !isLossless(codec) {
		args = append(args, "-b:a", bitrate)
	}
	return append(args, "-vn")
}

// resolveOutputExt 确定产物扩展名：显式 format 优先，auto/未知回落源扩展名，
// 源扩展名也不认识时落 mp3（容器与码流必须对得上，见 ExtOr 契约）。
func resolveOutputExt(format, srcPath string) string {
	if e := normalizeExt(format); e != "" && e != "auto" {
		return e
	}
	src := normalizeExt(strings.TrimPrefix(filepath.Ext(srcPath), "."))
	switch src {
	case "mp3", "m4a", "aac", "wav", "flac", "ogg", "oga", "opus":
		if src == "aac" {
			return "m4a" // 裸 AAC 码流统一落 m4a 容器，避免 adts 兼容性问题
		}
		return src
	default:
		return "mp3"
	}
}

func srcBaseName(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." {
		base = "audio"
	}
	return base
}

// finalizeArtifact 把 ffmpeg 已写好的 .part 产物改名落定并探测大小与时长
// （CLI _out 与数据目录两种落点共用）。
func finalizeArtifact(ctx context.Context, part, final string) (size, durMS int64, err error) {
	fi, err := os.Stat(part)
	if err != nil {
		return 0, 0, fmt.Errorf("读取产物失败: %w", err)
	}
	size = fi.Size()
	if err := os.Rename(part, final); err != nil {
		return 0, 0, fmt.Errorf("写入产物失败: %w", err)
	}
	if ai, err := probeAudio(ctx, final); err == nil {
		durMS = int64(ai.Duration * 1000)
	}
	return size, durMS, nil
}

// runToArtifact 剪辑类工具的统一收口：建目录 → 拼装 ffmpeg 命令（多输入 + 参数 +
// .part 输出）→ 执行（进度映射到 [8,95]）→ 改名落定并探测时长。失败清理 .part。
//
// out 为空时产物落数据目录（返回相对路径）；非空时走 CLI 的 _out 约定——
// 产物写该绝对路径并原样回传（引擎允许产物 Path 为绝对路径）。
func (t *baseTool) runToArtifact(ctx context.Context, inputs []string, args []string, artPath, out string, total float64, report provider.ProgressReporter, verb string) (string, int64, int64, error) {
	final := filepath.Join(t.outDir, artPath)
	if out != "" {
		if !filepath.IsAbs(out) {
			return "", 0, 0, fmt.Errorf("输出路径需为绝对路径，收到 %q", out)
		}
		final = out
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", 0, 0, fmt.Errorf("创建产物目录失败: %w", err)
	}
	// .part 插在扩展名之前（x.part.mp3 而非 x.mp3.part）：ffmpeg 靠扩展名推断
	// 封装器，裸 ".part" 后缀会直接 "Error opening output files: Invalid argument"。
	part := strings.TrimSuffix(final, filepath.Ext(final)) + ".part" + filepath.Ext(final)
	ffArgs := make([]string, 0, 2*len(inputs)+len(args)+2)
	for _, in := range inputs {
		ffArgs = append(ffArgs, "-i", in)
	}
	ffArgs = append(ffArgs, args...)
	ffArgs = append(ffArgs, part)
	if err := runFFmpeg(ctx, ffArgs, total, progressBridge(report, 8, 95, func(p int) string {
		if p < 0 {
			return verb + "…"
		}
		return fmt.Sprintf("%s %d%%…", verb, p)
	})); err != nil {
		_ = os.Remove(part)
		return "", 0, 0, err
	}
	size, durMS, err := finalizeArtifact(ctx, part, final)
	if err != nil {
		return "", 0, 0, err
	}
	return final, size, durMS, nil
}

// resolveOutCodec 决定输出格式三件套：显式 _out（CLI）时格式跟随该路径扩展名，
// 否则跟随源文件；再叠加输出格式/码率参数。
func resolveOutCodec(format, refPath, bitrate string) (ext, codec, br string) {
	ext = resolveOutputExt(format, refPath)
	codec, _ = audioCodecFor(ext)
	return ext, codec, normalizeBitrateStr(bitrate)
}

// ---------- 参数提取 ----------

func paramString(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := params[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func paramFloat(params map[string]any, key string, def float64) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	if params[key] == nil {
		return def
	}
	if f, err := strconv.ParseFloat(fmt.Sprint(params[key]), 64); err == nil {
		return f
	}
	return def
}

func paramBool(params map[string]any, key string) bool {
	v, ok := params[key].(bool)
	return ok && v
}

// orderedInputs 汇总按上传顺序排列的输入文件：键为 audio, audio2, audio3…（createTask 约定）。
func orderedInputs(files map[string]string) []string {
	type kv struct {
		idx int
		p   string
	}
	var items []kv
	for k, v := range files {
		if v == "" {
			continue
		}
		if k == "audio" {
			items = append(items, kv{idx: 1, p: v})
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(k, "audio")); err == nil && n > 0 {
			items = append(items, kv{idx: n, p: v})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].idx < items[j].idx })
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.p
	}
	return out
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// newReqID 产物子目录 id（与 mvsep 的每任务独立子目录防碰撞同一手法）。
func newReqID() string { return uuid.NewString() }

// progressBridge 把 ffmpeg 百分比映射为任务进度 [from,to]，-1 时报 from。
func progressBridge(report provider.ProgressReporter, from, to int, noteFn func(int) string) func(int) {
	return func(p int) {
		if p < 0 {
			report(from, noteFn(-1), nil)
			return
		}
		v := from + (to-from)*p/100
		if v > to {
			v = to
		}
		report(v, noteFn(p), nil)
	}
}

// ============================================================
// 音频切割 audio.trim
// ============================================================

// trimTool 选区切割：保留选区（trim）或挖除选区（cutout，去广告/去口误）。
type trimTool struct{ baseTool }

func (t *trimTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "trim", Title: "音频切割",
		Description: "按时间区间切割音频：保留选区或挖除选区（去广告/去口误），本地 ffmpeg 精确定位",
		Group:       "音频",
	}
}

func (t *trimTool) ParamSpecs() []provider.ParamSpec {
	specs := []provider.ParamSpec{
		{Key: "start", Label: "起点（秒）", Type: provider.ParamFloat, Required: true, Group: "选区", Placeholder: "如 12.5"},
		{Key: "end", Label: "终点（秒）", Type: provider.ParamFloat, Group: "选区", Placeholder: "0 表示到结尾"},
		{Key: "cutout", Label: "挖除选区（保留其余部分）", Type: provider.ParamBool, Group: "选区"},
	}
	return append(specs, outputSpecs()...)
}

type trimParams struct {
	Start  float64
	End    float64
	Cutout bool
	spec   outputSpec
}

// buildTrimArgs 编译切割参数（输出侧 -ss/-t 定位：重编码下是精确 seek）。
func buildTrimArgs(p trimParams, srcDur float64, ext, codec, bitrate string) ([]string, error) {
	if p.Start < 0 {
		return nil, fmt.Errorf("起点不能为负，收到 %g", p.Start)
	}
	if srcDur > 0 && p.Start >= srcDur {
		return nil, fmt.Errorf("起点（%gs）超出音频时长（%gs）", p.Start, srcDur)
	}
	if p.End > 0 && p.End <= p.Start {
		return nil, fmt.Errorf("终点（%gs）需大于起点（%gs）", p.End, p.Start)
	}

	var args []string
	if p.Cutout {
		cutEnd := p.End
		if cutEnd <= 0 {
			return nil, fmt.Errorf("挖除选区需要终点大于起点")
		}
		if srcDur > 0 && cutEnd > srcDur {
			cutEnd = srcDur
		}
		// 头尾各 atrim 后 asetpts 归零再 concat；asetpts 不做第二段时间戳会错位
		fc := fmt.Sprintf(
			"[0:a]atrim=start=0:end=%s,asetpts=N/SR/TB[a0];"+
				"[0:a]atrim=start=%s,asetpts=N/SR/TB[a1];"+
				"[a0][a1]concat=n=2:v=0:a=1[out]",
			trimFloat(p.Start), trimFloat(cutEnd))
		args = append(args, "-filter_complex", fc, "-map", "[out]")
	} else {
		if p.Start > 0 {
			args = append(args, "-ss", trimFloat(p.Start))
		}
		if d := p.trimDuration(srcDur); d > 0 {
			args = append(args, "-t", trimFloat(d))
		}
	}
	return append(args, encodeTail(ext, codec, bitrate)...), nil
}

func (p trimParams) trimDuration(srcDur float64) float64 {
	if p.End > 0 {
		d := p.End - p.Start
		if srcDur > 0 && p.End > srcDur {
			d = srcDur - p.Start
		}
		return d
	}
	return 0
}

func (t *trimTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := trimParams{
		Start:  paramFloat(in.Params, "start", 0),
		End:    paramFloat(in.Params, "end", 0),
		Cutout: paramBool(in.Params, "cutout"),
		spec:   outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
	}
	report(5, "正在探测音频信息…", nil)
	info, err := probeAudio(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	out := paramString(in.Params, "_out")
	extRef := out
	if extRef == "" {
		extRef = src
	}
	ext, codec, br := resolveOutCodec(p.spec.Format, extRef, p.spec.Bitrate)
	args, err := buildTrimArgs(p, info.Duration, ext, codec, br)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	op := "trim"
	if p.Cutout {
		op = "cutout"
	}
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_"+op+"."+ext)
	total := p.trimDuration(info.Duration)
	if total <= 0 {
		total = info.Duration - p.Start
	}
	report(8, "开始切割…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), total, report, "切割中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "切割完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": op, "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": op, "start": p.Start, "end": p.End, "duration_sec": round2(info.Duration)},
	}, nil
}

// ============================================================
// 音频合并 audio.merge
// ============================================================

// mergeTool 多文件顺序合并：硬拼（concat filter，统一采样率/声道）或交叉淡化拼接。
type mergeTool struct{ baseTool }

func (t *mergeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "merge", Title: "音频合并",
		Description: "将多个音频按顺序合并为一个文件，可选接缝处交叉淡化（acrossfade）平滑过渡",
		Group:       "音频",
	}
}

func (t *mergeTool) ParamSpecs() []provider.ParamSpec {
	specs := []provider.ParamSpec{
		{Key: "crossfade", Label: "交叉淡化（秒）", Type: provider.ParamFloat, Group: "拼接",
			Placeholder: "0 = 直接拼接；如 2 = 每个接缝 2 秒淡入淡出过渡"},
	}
	return append(specs, outputSpecs()...)
}

type mergeParams struct {
	Crossfade float64
	spec      outputSpec
}

// buildMergeArgs 编译合并参数。concat filter 要求所有输入采样率/声道一致，
// 一律先 aformat 统一到 44100Hz/stereo/fltp（异源拼接的安全默认）。
func buildMergeArgs(n int, p mergeParams, ext, codec, bitrate string) ([]string, error) {
	if n < 2 {
		return nil, fmt.Errorf("合并至少需要 2 个文件")
	}
	if p.Crossfade < 0 {
		return nil, fmt.Errorf("交叉淡化时长不能为负，收到 %g", p.Crossfade)
	}

	uniform := func(i int) string {
		return fmt.Sprintf("[%d:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a%d]", i, i)
	}
	var fc strings.Builder
	args := []string{}
	if p.Crossfade > 0 {
		// acrossfade 一次吃两路：统一段 + 串成链
		for i := 0; i < n; i++ {
			if i > 0 {
				fc.WriteString(";")
			}
			fc.WriteString(uniform(i))
		}
		d := trimFloat(clampF(p.Crossfade, 0.1, 12))
		prev := "[a0]"
		for i := 1; i < n; i++ {
			out := fmt.Sprintf("[x%d]", i)
			fc.WriteString(";")
			fmt.Fprintf(&fc, "%s[a%d]acrossfade=d=%s:c1=tri:c2=tri%s", prev, i, d, out)
			prev = out
		}
		args = append(args, "-filter_complex", fc.String(), "-map", prev)
	} else {
		for i := 0; i < n; i++ {
			if i > 0 {
				fc.WriteString(";")
			}
			fc.WriteString(uniform(i))
		}
		labels := make([]string, n)
		for i := range labels {
			labels[i] = fmt.Sprintf("[a%d]", i)
		}
		fmt.Fprintf(&fc, ";%sconcat=n=%d:v=0:a=1[out]", strings.Join(labels, ""), n)
		args = append(args, "-filter_complex", fc.String(), "-map", "[out]")
	}
	return append(args, encodeTail(ext, codec, bitrate)...), nil
}

func (t *mergeTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	inputs := orderedInputs(in.Files)
	if len(inputs) < 2 {
		return provider.TaskOutput{}, fmt.Errorf("合并至少需要 2 个音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := mergeParams{
		Crossfade: paramFloat(in.Params, "crossfade", 0),
		spec:      outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
	}
	report(5, "正在校验输入文件…", nil)
	var total float64
	for _, path := range inputs {
		info, err := probeAudio(ctx, path)
		if err != nil {
			return provider.TaskOutput{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		total += info.Duration
	}
	if p.Crossfade > 0 {
		total -= p.Crossfade * float64(len(inputs)-1)
		if total < 1 {
			total = 1
		}
	}
	out := paramString(in.Params, "_out")
	extRef := out
	if extRef == "" {
		extRef = inputs[0]
	}
	ext, codec, br := resolveOutCodec(p.spec.Format, extRef, p.spec.Bitrate)
	args, err := buildMergeArgs(len(inputs), p, ext, codec, br)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	name := srcBaseName(inputs[0])
	if len(inputs) == 2 {
		name += "+" + srcBaseName(inputs[1])
	} else {
		name += fmt.Sprintf("_等%d轨", len(inputs))
	}
	artPath := filepath.Join("editor", newReqID(), name+"_merged."+ext)

	report(8, "开始合并…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, inputs, args, artPath, paramString(in.Params, "_out"), total, report, "合并中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "合并完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "merge", "inputs": len(inputs), "crossfade": p.Crossfade},
		}},
		Summary: map[string]any{"op": "merge", "inputs": len(inputs), "crossfade_sec": p.Crossfade, "duration_sec": round2(total)},
	}, nil
}

// ============================================================
// 变调变速 audio.pitch
// ============================================================

// pitchTool 变调（半音）与变速（倍率）双轴调整，可独立或同时生效。
type pitchTool struct{ baseTool }

func (t *pitchTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "pitch", Title: "变调变速",
		Description: "乐调滑条改变音高（半音），BPM 滑条改变节奏（速度倍率）；两轴独立互不影响，asetrate+atempo 实现",
		Group:       "音频",
	}
}

func (t *pitchTool) ParamSpecs() []provider.ParamSpec {
	specs := []provider.ParamSpec{
		{Key: "semitones", Label: "变调（半音）", Type: provider.ParamFloat, Default: 0, Group: "调整",
			Placeholder: "-12 ~ +12，正=升调；1 半音 = 1 个乐调"},
		{Key: "tempo", Label: "变速（倍率）", Type: provider.ParamFloat, Default: 1, Group: "调整",
			Placeholder: "0.5 ~ 2.0，1 = 不变速；1.25 = 快 25%（约 +4 BPM/100）"},
	}
	return append(specs, outputSpecs()...)
}

type pitchParams struct {
	Semitones float64
	Tempo     float64
	spec      outputSpec
}

// buildPitchArgs 编译变调变速参数。
//
// 原理（asetrate 方案）：asetrate=SR*r 谎报采样率 → 播放变快 r 倍且音调升 r 倍；
// aresample=SR 拉回真实采样率把音调变化固化；atempo 补偿把速度还原或改为目标倍率。
// asetrate 只接受整数，实际倍率用取整后的采样率反算，保证时长不漂。
func buildPitchArgs(p pitchParams, sampleRate int, ext, codec, bitrate string) ([]string, error) {
	if p.Semitones != 0 && (p.Semitones < -12 || p.Semitones > 12) {
		return nil, fmt.Errorf("变调需在 ±12 半音内，收到 %g", p.Semitones)
	}
	if p.Tempo != 1 && (p.Tempo < 0.25 || p.Tempo > 4) {
		return nil, fmt.Errorf("变速倍率需在 0.25 ~ 4.0，收到 %g", p.Tempo)
	}
	if p.Semitones == 0 && p.Tempo == 1 {
		return nil, fmt.Errorf("变调与变速均为默认值，无需处理")
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("探测源采样率失败，无法变调")
	}

	var af string
	if p.Semitones != 0 {
		ratio := math.Pow(2, p.Semitones/12)
		newRate := int(math.Round(float64(sampleRate) * ratio))
		actual := float64(newRate) / float64(sampleRate)
		// 总 tempo = 目标速度 ÷ 变调引入的速度；asetrate 链后再补一层变速
		tempo, err := atempoChain(p.Tempo / actual)
		if err != nil {
			return nil, err
		}
		af = fmt.Sprintf("asetrate=%d,aresample=%d,%s", newRate, sampleRate, tempo)
	} else {
		tempo, err := atempoChain(p.Tempo)
		if err != nil {
			return nil, err
		}
		af = tempo
	}
	args := append([]string{"-af", af}, encodeTail(ext, codec, bitrate)...)
	return args, nil
}

func (t *pitchTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := pitchParams{
		Semitones: paramFloat(in.Params, "semitones", 0),
		Tempo:     paramFloat(in.Params, "tempo", 1),
		spec:      outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
	}
	if p.Tempo == 0 {
		p.Tempo = 1 // 显式传 0 视作缺省
	}
	report(5, "正在探测音频信息…", nil)
	info, err := probeAudio(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	out := paramString(in.Params, "_out")
	extRef := out
	if extRef == "" {
		extRef = src
	}
	ext, codec, br := resolveOutCodec(p.spec.Format, extRef, p.spec.Bitrate)
	args, err := buildPitchArgs(p, info.SampleRate, ext, codec, br)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_pitch."+ext)
	total := info.Duration / p.Tempo
	report(8, "开始处理…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), total, report, "处理中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "处理完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "pitch", "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": "pitch", "semitones": p.Semitones, "tempo": p.Tempo, "duration_sec": round2(total)},
	}, nil
}

// ---------- 小工具 ----------

func round2(v float64) float64 { return math.Round(v*100) / 100 }
