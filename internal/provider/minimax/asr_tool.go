package minimax

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
)

// minimaxASRLimits 官方单次请求限制：时长 ≤500 秒、大小 ≤50MB（超时上游回 400/413 不截断），
// 且不支持裸 PCM。超限音频由服务端自动切分逐段转写后归一时间轴拼接：wav 纯 Go 按帧精确
// 切分（边界吸附静音区），其余格式走 ffmpeg 流拷贝分段。asrSeg* 为切分预算（var 便于
// 测试注入）；asrInputCap 为整文件上限，超出后分段请求数与内存都不划算，引导改用
// 火山引擎录音文件通道（最长 5 小时）。
const (
	minimaxASRMaxSecs  = 500
	minimaxASRMaxBytes = 50 << 20
	asrInputCap        = 200 << 20
)

var (
	minimaxASRSegSecs  = 490.0
	minimaxASRSegBytes = 48 << 20
)

// 官方格式白名单（wav/aiff/flac/alac(m4a)/mp3/aac/opus/ogg，无裸 PCM）：
// 按扩展名拦截（魔数校验交上游，避免误杀非标头文件）。
var asrAllowedExts = map[string]bool{
	".wav": true, ".aiff": true, ".flac": true, ".m4a": true,
	".mp3": true, ".aac": true, ".opus": true, ".ogg": true,
}

// asrLanguages 识别语种（BCP-47，官方走请求头）：空 = 混合语言识别。
var asrLanguages = []provider.ParamOption{
	{Value: "", Label: "自动识别（混合语种）"},
	{Value: "zh", Label: "中文"}, {Value: "yue", Label: "粤语"},
	{Value: "en", Label: "英语"}, {Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"},
	{Value: "th", Label: "泰语"}, {Value: "vi", Label: "越南语"}, {Value: "id", Label: "印尼语"},
	{Value: "ms", Label: "马来语"}, {Value: "fil", Label: "菲律宾语"}, {Value: "ar", Label: "阿拉伯语"},
	{Value: "tr", Label: "土耳其语"}, {Value: "fr", Label: "法语"}, {Value: "de", Label: "德语"},
	{Value: "es", Label: "西班牙语"}, {Value: "it", Label: "意大利语"}, {Value: "pt", Label: "葡萄牙语"},
	{Value: "pl", Label: "波兰语"}, {Value: "ru", Label: "俄语"}, {Value: "uk", Label: "乌克兰语"},
}

// ASRTool MiniMax 语音识别（asr-1.0）：multipart 直传，verbose_json 带说话人分离与
// 句级时间戳；超 500 秒/50MB 自动分段转写后归一时间轴，输出转写文本 + SRT 字幕。
type ASRTool struct {
	client *ASRClient
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewASRTool(apiKey, outDir string) *ASRTool {
	return &ASRTool{client: NewASRClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "minimax",
		Name:        "asr",
		Title:       "语音识别（MiniMax）",
		Description: "asr-1.0 同步转写（官方 8 种格式，超 500 秒/50MB 自动分段），自带说话人分离与句级时间戳，输出文本 + SRT 字幕",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL；留空可配合本地文件直传（无需对象存储）"},
		{Key: "language", Label: "语种", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: asrLanguages},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audioPath, source, err := t.resolveInput(ctx, in)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if source == "url" {
		defer os.Remove(audioPath) // URL 下载的临时文件用完即清；本地文件不动
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 MiniMax API Key，或 voxbox config set minimax.api_key", ErrNoCred)
	}
	if fi, statErr := os.Stat(audioPath); statErr == nil && fi.Size() > asrInputCap {
		return provider.TaskOutput{}, fmt.Errorf("音频过大（%.0fMB）：MiniMax 识别支持 200MB 内自动分段，更长录音请改用火山引擎识别（最长 5 小时）",
			float64(fi.Size())/1024/1024)
	}
	ext := strings.ToLower(filepath.Ext(audioPath))
	if !asrAllowedExts[ext] {
		return provider.TaskOutput{}, fmt.Errorf("参数错误：MiniMax 识别仅支持 wav / aiff / flac / m4a / mp3 / aac / opus / ogg 音频（当前 %s）", ext)
	}
	language := paramString(in.Params, "language")

	report(15, "检查音频规格", nil)
	segs, totalMS, parts, err := t.transcribeAuto(ctx, audioPath, ext, language, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(80, "保存转写产物", nil)
	return t.saveArtifacts(in, segs, totalMS, source, parts)
}

// asrSegment 归一后的识别分句：毫秒时间轴 + 说话人（全工具统一契约，供 SRT 与 Summary）。
type asrSegment struct {
	Text    string
	StartMS int64
	EndMS   int64
	Speaker string
}

// transcribeAuto 按官方单次限制自动决定「直传」或「切分逐段转写」：
//   - wav：纯 Go 解析头部得精确时长，超预算按帧切分（边界吸附静音区），
//     解析失败的非常规文件回落 ffmpeg 通道；
//   - 其余格式（及 wav 回落）：ffprobe 探测时长判定，超预算由 ffmpeg 流拷贝分段；
//     ffprobe 缺失时回落单次直传（交上游报时长/大小超限）；
//   - 逐段时间轴按前序段累计时长归一（各次转写的时间戳均相对本段起点）。
//
// 返回（归一分句, 总时长毫秒, 分段数）。
func (t *ASRTool) transcribeAuto(ctx context.Context, path, ext, language string, report provider.ProgressReporter) ([]asrSegment, int64, int, error) {
	if ext == ".wav" {
		if data, err := os.ReadFile(path); err == nil {
			if w, perr := provider.ParseWAV(data); perr == nil {
				if w.Duration() > minimaxASRSegSecs || len(data) > minimaxASRMaxBytes {
					return t.transcribeSegmentsWAV(ctx, w, language, report)
				}
				resp, err := t.client.Transcribe(ctx, path, language)
				return respSegments(resp, 0), secToMS(w.Duration()), 1, err
			}
		}
	}

	// 非 wav（或 wav 解析失败）：探测驱动。≤50MB 且探测不可用时回落单次直传
	//（ffprobe 缺失/非常规文件都交上游裁决）；超 50MB 必须分段。
	overSize := false
	if fi, statErr := os.Stat(path); statErr == nil {
		overSize = fi.Size() > minimaxASRMaxBytes
	}
	info, perr := audiotool.ProbeAudio(ctx, path)
	if perr != nil {
		if overSize {
			if lookErr := audiotool.EnsureFFmpeg(); lookErr != nil {
				return nil, 0, 0, fmt.Errorf("音频超过 50MB 且无法自动分段：%w", lookErr)
			}
			return nil, 0, 0, fmt.Errorf("音频超过 50MB，自动分段需要先探测音频信息: %w", perr)
		}
		resp, err := t.client.Transcribe(ctx, path, language)
		return respSegments(resp, 0), secToMS(resp.Duration), 1, err
	}
	if !overSize && info.Duration <= minimaxASRSegSecs {
		resp, err := t.client.Transcribe(ctx, path, language)
		return respSegments(resp, 0), secToMS(info.Duration), 1, err
	}
	segSec := minimaxASRSegSecs
	if info.BitRate > 0 {
		if bySec := float64(minimaxASRSegBytes) / float64(info.BitRate); bySec < segSec {
			segSec = bySec
		}
	}
	if err := audiotool.EnsureFFmpeg(); err != nil {
		return nil, 0, 0, fmt.Errorf("音频超过单次识别限制（500 秒/50MB），自动分段需要 ffmpeg：%w", err)
	}
	dir, parts, err := audiotool.SegmentAudioFile(ctx, path, segSec, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	defer os.RemoveAll(dir)
	return t.transcribeParts(ctx, parts, language, info.Duration, report)
}

// transcribeSegmentsWAV 切分已解析的 WAV 并逐段转写（纯 Go 路径，无外部依赖）。
func (t *ASRTool) transcribeSegmentsWAV(ctx context.Context, w *provider.WAV, language string, report provider.ProgressReporter) ([]asrSegment, int64, int, error) {
	segs := w.Split(minimaxASRSegSecs, minimaxASRSegBytes, true)
	dir, err := os.MkdirTemp("", "minimax-asr-seg-")
	if err != nil {
		return nil, 0, 0, fmt.Errorf("创建分段临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)
	parts := make([]string, 0, len(segs))
	for i, seg := range segs {
		p := filepath.Join(dir, fmt.Sprintf("seg-%03d.wav", i))
		if err := os.WriteFile(p, seg, 0o644); err != nil {
			return nil, 0, 0, fmt.Errorf("写入分段音频失败: %w", err)
		}
		parts = append(parts, p)
	}
	return t.transcribeParts(ctx, parts, language, w.Duration(), report)
}

// transcribeParts 逐段转写并归一：段偏移按已消费音频秒数累加（WAV 路径用解析器精确
// 时长，ffmpeg 路径段时长由探测总时长均摊误差可忽略——段预算远小于上游 500 秒上限）。
func (t *ASRTool) transcribeParts(ctx context.Context, parts []string, language string, totalSec float64, report provider.ProgressReporter) ([]asrSegment, int64, int, error) {
	var all []asrSegment
	offsetSec := 0.0
	for i, part := range parts {
		if report != nil {
			report(20+60*i/len(parts), fmt.Sprintf("正在转写第 %d/%d 段", i+1, len(parts)), nil)
		}
		resp, err := t.client.Transcribe(ctx, part, language)
		if err != nil {
			return nil, 0, 0, err
		}
		all = append(all, respSegments(resp, offsetSec)...)
		// 段时长：末段用总时长兜底（探测值与分段实际值存在亚秒级误差）
		segSec := resp.Duration
		if segSec <= 0 {
			segSec = (totalSec - offsetSec) / float64(len(parts)-i)
		}
		offsetSec += segSec
	}
	return all, secToMS(totalSec), len(parts), nil
}

// respSegments 上游响应 → 归一分句（加段偏移毫秒）；无时间戳（response_format=json
// 兜底场景）时退化为无轴文本段（时间轴全 0，SRT 不产出）。
func respSegments(resp ASRResp, offsetSec float64) []asrSegment {
	offsetMS := secToMS(offsetSec)
	if len(resp.Segments) == 0 {
		if resp.Text == "" {
			return nil
		}
		return []asrSegment{{Text: resp.Text}}
	}
	out := make([]asrSegment, 0, len(resp.Segments))
	for _, s := range resp.Segments {
		out = append(out, asrSegment{
			Text:    s.Text,
			StartMS: offsetMS + int64(s.Start*1000+0.5),
			EndMS:   offsetMS + int64(s.End*1000+0.5),
			Speaker: s.Speaker,
		})
	}
	return out
}

// saveArtifacts 落盘转写文本（asr/<uuid>.txt）与 SRT 字幕（asr/<uuid>.srt，仅当拥有
// 有效时间轴），Summary 产出 segments（含可选 speaker）/duration_ms/source 与去重
// speakers_count（契约与火山系识别产物一致，前端/字幕工坊零适配复用）。
func (t *ASRTool) saveArtifacts(in provider.TaskInput, segs []asrSegment, totalMS int64, source string, parts int) (provider.TaskOutput, error) {
	text := joinSegmentText(segs)
	hasTimeline := len(segs) > 0 && segs[len(segs)-1].EndMS > 0
	srtContent := ""
	if hasTimeline {
		srtSegs := make([]provider.SRTSegment, 0, len(segs))
		for _, s := range segs {
			srtSegs = append(srtSegs, provider.SRTSegment{StartMS: s.StartMS, EndMS: s.EndMS, Text: s.Text})
		}
		srtContent = provider.BuildSRT(srtSegs)
	}

	reqID := uuid.NewString()
	txtPath := filepath.Join("asr", reqID+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		txtPath = outParam
	}
	// srt 路径跟随 txt 路径：仅换扩展名（_out 为绝对路径时 srt 同为绝对；相对时在相对段上替换）
	srtPath := strings.TrimSuffix(txtPath, filepath.Ext(txtPath)) + ".srt"
	txtAbs, txtRel := resolveOut(t.outDir, txtPath)
	srtAbs, srtRel := resolveOut(t.outDir, srtPath)

	if err := os.MkdirAll(filepath.Dir(txtAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(txtAbs, []byte(text), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}
	arts := []provider.Artifact{{
		Kind: "transcript", Path: txtRel, Format: "txt",
		Size:       int64(len(text)),
		DurationMS: totalMS,
	}}
	if srtContent != "" {
		if err := os.WriteFile(srtAbs, []byte(srtContent), 0o644); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
		}
		arts = append(arts, provider.Artifact{
			Kind: "subtitle", Path: srtRel, Format: "srt", Size: int64(len(srtContent)),
		})
	}

	segMaps := make([]map[string]any, 0, len(segs))
	seen := map[string]int{}
	for _, s := range segs {
		seg := map[string]any{"text": s.Text, "start_ms": s.StartMS, "end_ms": s.EndMS}
		if s.Speaker != "" {
			seg["speaker"] = s.Speaker
			seen[s.Speaker]++
		}
		segMaps = append(segMaps, seg)
	}
	summary := map[string]any{
		"char_count":  utf8.RuneCountInString(text),
		"model":       ASRModel,
		"source":      source,
		"segment_num": parts,
		"duration_ms": totalMS,
	}
	// segments 仅在拥有时间轴时产出（契约与火山系一致：无轴纯文本不渲染分句列表）
	if len(segMaps) > 0 && hasTimeline {
		summary["segments"] = segMaps
	}
	if len(seen) > 0 {
		summary["speakers_count"] = len(seen)
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary:   summary,
	}, nil
}

// joinSegmentText 全文文本：有时间轴按分句拼接（与上游 text 语义一致），无时间轴直接用原文。
func joinSegmentText(segs []asrSegment) string {
	if len(segs) == 1 && segs[0].StartMS == 0 && segs[0].EndMS == 0 {
		return segs[0].Text
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// resolveInput 解析音频文件输入：Files["audio"] 本地文件直用；否则取 params.url 下载到
// 临时文件后转传（识别仅收 multipart，无对象存储中转需求）。
// 返回音频本地路径与来源标记。
func (t *ASRTool) resolveInput(ctx context.Context, in provider.TaskInput) (string, string, error) {
	if src := in.Files["audio"]; src != "" {
		if _, err := os.Stat(src); err != nil {
			return "", "", fmt.Errorf("读取音频文件失败: %w", err)
		}
		return src, "file", nil
	}
	u := strings.TrimSpace(paramString(in.Params, "url"))
	if u == "" {
		return "", "", fmt.Errorf("缺少必填参数: 音频（MiniMax 识别需要音频 URL 或本地文件）")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", fmt.Errorf("参数错误：音频 URL 仅支持 http(s):// 公网地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	// 不附带 Authorization：目标是用户自己的公网地址，不外泄凭证（与智谱/小米同理）
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("下载音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("下载音频失败: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "minimax-asr-*"+strings.ToLower(filepath.Ext(u)))
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", "", fmt.Errorf("下载音频失败: %w", err)
	}
	return tmp.Name(), "url", nil
}
