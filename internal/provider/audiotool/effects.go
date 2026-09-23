// audiotool 效果器四件：均衡器、音量与响度、淡入淡出、倒放。
// 均为一进一出的 filter 链操作，走 runToArtifact 统一收口。
package audiotool

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// ============================================================
// 均衡器 audio.equalizer
// ============================================================

// EqBandFreqs 10 段图示均衡的标准中心频率（Hz）。
var EqBandFreqs = []float64{31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

// EqPresets 预设的 10 段增益（dB），按通用听感惯例取值。
var EqPresets = map[string][]float64{
	"flat":      {0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	"pop":       {-1, -1, -1, -1, 0, 0, 2, 2, 2, 2},
	"rock":      {5, 5, 2, 2, -1, -1, 2, 2, 4, 4},
	"jazz":      {4, 4, 2, 2, -1, -1, 2, 2, 3, 3},
	"classical": {4, 4, 2, 2, -1, -1, 2, 2, 3, 3},
	"vocal":     {-2, -2, -2, -2, 2, 2, 4, 4, 3, 3},
	"bass":      {8, 8, 5, 5, 1, 1, 0, 0, 0, 0},
	"treble":    {0, 0, 0, 0, 0, 0, 3, 3, 6, 6},
	"loudness":  {6, 6, 3, 3, 0, 0, 2, 2, 5, 5},
}

// eqTool 10 段图示均衡器（preset 或自定义增益 CSV）。
type eqTool struct{ baseTool }

func (t *eqTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "equalizer", Title: "均衡器",
		Description: "10 段图示均衡（31Hz~16kHz）：9 个预设（流行/摇滚/人声/低音增强…）或自定义各段增益",
		Group:       "音频",
	}
}

func (t *eqTool) ParamSpecs() []provider.ParamSpec {
	presetOpts := make([]provider.ParamOption, 0, len(EqPresets))
	for _, name := range []string{"flat", "pop", "rock", "jazz", "classical", "vocal", "bass", "treble", "loudness"} {
		presetOpts = append(presetOpts, provider.ParamOption{Value: name, Label: name})
	}
	specs := []provider.ParamSpec{
		{Key: "preset", Label: "预设", Type: provider.ParamEnum, Default: "flat", Group: "曲线", Options: presetOpts},
		{Key: "gains", Label: "自定义增益（dB）", Type: provider.ParamString, Group: "曲线",
			Placeholder: "10 段逗号分隔，非空时覆盖预设，如 3,0,0,-2,0,1,2,3,4,4"},
	}
	return append(specs, outputSpecs()...)
}

type eqParams struct {
	Preset string
	Gains  string
	spec   outputSpec
}

// resolveEqGains 解析最终生效的 10 段增益：gains CSV 优先，否则按 preset。
func resolveEqGains(p eqParams) ([]float64, error) {
	if csv := strings.TrimSpace(p.Gains); csv != "" {
		parts := strings.Split(csv, ",")
		if len(parts) != len(EqBandFreqs) {
			return nil, fmt.Errorf("自定义增益需为 %d 段逗号分隔数值，收到 %d 段", len(EqBandFreqs), len(parts))
		}
		gains := make([]float64, len(parts))
		for i, s := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, fmt.Errorf("第 %d 段增益不是数字: %q", i+1, s)
			}
			if v < -12 || v > 12 {
				return nil, fmt.Errorf("第 %d 段增益需在 ±12dB 内，收到 %g", i+1, v)
			}
			gains[i] = v
		}
		return gains, nil
	}
	name := strings.ToLower(strings.TrimSpace(p.Preset))
	if name == "" {
		name = "flat"
	}
	gains, ok := EqPresets[name]
	if !ok {
		return nil, fmt.Errorf("未知均衡预设 %q", name)
	}
	return append([]float64(nil), gains...), nil
}

// buildEqGains 把增益编译成 equalizer filter 链；全 0 退化 anull（空链会报错）。
func buildEqGains(gains []float64) string {
	var parts []string
	for i, g := range gains {
		if g == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("equalizer=f=%s:t=q:w=1:g=%s", trimFloat(EqBandFreqs[i]), trimFloat(g)))
	}
	if len(parts) == 0 {
		return "anull"
	}
	return strings.Join(parts, ",")
}

func (t *eqTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := eqParams{
		Preset: paramString(in.Params, "preset"),
		Gains:  paramString(in.Params, "gains"),
		spec:   outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
	}
	gains, err := resolveEqGains(p)
	if err != nil {
		return provider.TaskOutput{}, err
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
	args := append([]string{"-af", buildEqGains(gains)}, encodeTail(ext, codec, br)...)
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_eq."+ext)
	report(8, "开始均衡处理…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), info.Duration, report, "均衡处理中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "均衡完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "equalizer", "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": "equalizer", "gains": gains},
	}, nil
}

// ============================================================
// 音量与响度 audio.volume
// ============================================================

// volumeTool 增益微调或 EBU R128 响度归一化（loudnorm 二选一）。
type volumeTool struct{ baseTool }

func (t *volumeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "volume", Title: "音量与响度",
		Description: "增益微调（dB）或响度归一化到目标 LUFS（流媒体 -14 / 播客 -16 / 广播 -23），带真峰值保护",
		Group:       "音频",
	}
}

func (t *volumeTool) ParamSpecs() []provider.ParamSpec {
	lufsOpts := []provider.ParamOption{
		{Value: "-14", Label: "-14 LUFS（流媒体）"},
		{Value: "-16", Label: "-16 LUFS（播客/语音）"},
		{Value: "-23", Label: "-23 LUFS（广播 EBU R128）"},
	}
	specs := []provider.ParamSpec{
		{Key: "gain_db", Label: "增益（dB）", Type: provider.ParamFloat, Default: 0, Group: "调整",
			Placeholder: "-30 ~ +12，0 = 不调整"},
		{Key: "normalize", Label: "响度归一化（覆盖增益）", Type: provider.ParamBool, Group: "调整"},
		{Key: "lufs", Label: "目标响度", Type: provider.ParamEnum, Default: "-14", Group: "调整", Options: lufsOpts},
	}
	return append(specs, outputSpecs()...)
}

type volumeParams struct {
	GainDB    float64
	Normalize bool
	LUFS      float64
	spec      outputSpec
}

// buildVolumeArgs 编译音量/响度参数。归一化优先；增益为正且不归一时加 alimiter
// 防削顶（level=disabled 保留余量，默认的 level=true 会把输出顶回 1.0）。
func buildVolumeArgs(p volumeParams, sampleRate int, ext, codec, bitrate string) ([]string, error) {
	if p.GainDB != 0 && (p.GainDB < -30 || p.GainDB > 12) {
		return nil, fmt.Errorf("增益需在 -30 ~ +12 dB，收到 %g", p.GainDB)
	}
	if !p.Normalize && p.GainDB == 0 {
		return nil, fmt.Errorf("增益与归一化均未设置，无需处理")
	}
	if p.Normalize && (p.LUFS < -40 || p.LUFS > -5) {
		return nil, fmt.Errorf("目标响度需在 -40 ~ -5 LUFS，收到 %g", p.LUFS)
	}

	var af string
	switch {
	case p.Normalize:
		// TP=-1.5 自带真峰值保护；单遍动态模式会把采样率抬高，必须钉回源采样率
		af = fmt.Sprintf("loudnorm=I=%s:TP=-1.5:LRA=11", trimFloat(p.LUFS))
	case p.GainDB > 0:
		// 正增益有削顶风险：链尾 alimiter 保险（level=disabled 保留余量，
		// 默认的 level=true 会把输出顶回 1.0）
		af = fmt.Sprintf("volume=%sdB,alimiter=limit=0.98:level=disabled", trimFloat(p.GainDB))
	default:
		af = fmt.Sprintf("volume=%sdB", trimFloat(p.GainDB))
	}
	args := []string{"-af", af}
	if sampleRate > 0 {
		args = append(args, "-ar", fmt.Sprint(sampleRate))
	}
	return append(args, encodeTail(ext, codec, bitrate)...), nil
}

func (t *volumeTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := volumeParams{
		GainDB:    paramFloat(in.Params, "gain_db", 0),
		Normalize: paramBool(in.Params, "normalize"),
		LUFS:      paramFloat(in.Params, "lufs", -14),
		spec:      outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
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
	args, err := buildVolumeArgs(p, info.SampleRate, ext, codec, br)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_volume."+ext)
	report(8, "开始处理…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), info.Duration, report, "音量处理中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "处理完成", nil)
	op := "volume"
	if p.Normalize {
		op = "normalize"
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": op, "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": op, "gain_db": p.GainDB, "normalize": p.Normalize, "lufs": p.LUFS},
	}, nil
}

// ============================================================
// 淡入淡出 audio.fade
// ============================================================

// fadeTool 片头淡入 / 片尾淡出。
type fadeTool struct{ baseTool }

func (t *fadeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "fade", Title: "淡入淡出",
		Description: "片头淡入与片尾淡出（afade），支持线性/对数/指数曲线",
		Group:       "音频",
	}
}

func (t *fadeTool) ParamSpecs() []provider.ParamSpec {
	curveOpts := []provider.ParamOption{
		{Value: "linear", Label: "线性"},
		{Value: "log", Label: "对数（先快后慢）"},
		{Value: "exp", Label: "指数（先慢后快）"},
	}
	specs := []provider.ParamSpec{
		{Key: "fade_in", Label: "淡入时长（秒）", Type: provider.ParamFloat, Group: "曲线", Placeholder: "0 = 不淡入"},
		{Key: "fade_out", Label: "淡出时长（秒）", Type: provider.ParamFloat, Group: "曲线", Placeholder: "0 = 不淡出"},
		{Key: "curve", Label: "曲线", Type: provider.ParamEnum, Default: "linear", Group: "曲线", Options: curveOpts},
	}
	return append(specs, outputSpecs()...)
}

type fadeParams struct {
	FadeIn  float64
	FadeOut float64
	Curve   string
	spec    outputSpec
}

// afadeCurve 把曲线名映射到 afade 的 curve 值（linear→tri）。
func afadeCurve(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "log":
		return "log"
	case "exp":
		return "exp"
	default:
		return "tri"
	}
}

// buildFadeArgs 编译淡入淡出参数；淡出起点按源时长回推，超出时长时钳半。
func buildFadeArgs(p fadeParams, srcDur float64, ext, codec, bitrate string) ([]string, error) {
	if p.FadeIn < 0 || p.FadeOut < 0 {
		return nil, fmt.Errorf("淡入/淡出时长不能为负")
	}
	if p.FadeIn == 0 && p.FadeOut == 0 {
		return nil, fmt.Errorf("淡入与淡出时长均为 0，无需处理")
	}
	if srcDur <= 0 {
		return nil, fmt.Errorf("探测音频时长失败，无法定位淡出起点")
	}
	curve := afadeCurve(p.Curve)

	var chains []string
	if p.FadeIn > 0 {
		d := clampF(p.FadeIn, 0.01, srcDur/2)
		chains = append(chains, fmt.Sprintf("afade=t=in:st=0:d=%s:curve=%s", trimFloat(d), curve))
	}
	if p.FadeOut > 0 {
		d := clampF(p.FadeOut, 0.01, srcDur/2)
		st := srcDur - d
		chains = append(chains, fmt.Sprintf("afade=t=out:st=%s:d=%s:curve=%s", trimFloat(st), trimFloat(d), curve))
	}
	args := append([]string{"-af", strings.Join(chains, ",")}, encodeTail(ext, codec, bitrate)...)
	return args, nil
}

func (t *fadeTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := fadeParams{
		FadeIn:  paramFloat(in.Params, "fade_in", 0),
		FadeOut: paramFloat(in.Params, "fade_out", 0),
		Curve:   paramString(in.Params, "curve"),
		spec:    outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")},
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
	args, err := buildFadeArgs(p, info.Duration, ext, codec, br)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_fade."+ext)
	report(8, "开始处理…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), info.Duration, report, "淡入淡出处理中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "处理完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "fade", "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": "fade", "fade_in": p.FadeIn, "fade_out": p.FadeOut, "curve": afadeCurve(p.Curve)},
	}, nil
}

// ============================================================
// 倒放 audio.reverse
// ============================================================

// reverseTool 音频倒放（areverse）。
type reverseTool struct{ baseTool }

func (t *reverseTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "reverse", Title: "倒放",
		Description: "整段音频反向播放（areverse）",
		Group:       "音频",
	}
}

func (t *reverseTool) ParamSpecs() []provider.ParamSpec {
	return append([]provider.ParamSpec{}, outputSpecs()...)
}

func (t *reverseTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	spec := outputSpec{Format: paramString(in.Params, "format"), Bitrate: paramString(in.Params, "bitrate")}
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
	ext, codec, br := resolveOutCodec(spec.Format, extRef, spec.Bitrate)
	args := append([]string{"-af", "areverse"}, encodeTail(ext, codec, br)...)
	artPath := filepath.Join("editor", newReqID(), srcBaseName(src)+"_reversed."+ext)
	report(8, "开始倒放处理…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), info.Duration, report, "倒放处理中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "倒放完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "reverse", "source": filepath.Base(src)},
		}},
		Summary: map[string]any{"op": "reverse", "duration_sec": round2(info.Duration)},
	}, nil
}
