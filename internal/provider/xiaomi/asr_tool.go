package xiaomi

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

// asrMaxB64 官方载荷上限：input_audio.data 的 base64 字符串 ≤10MB；
// asrMaxRaw 换算回原始字节上限（base64 膨胀 4/3）。超限音频由服务端自动切分
// 逐段转写后拼接（wav 纯 Go 按帧切分、边界吸附静音区；mp3 走 ffmpeg 流拷贝）。
// asrSegBytes 为单段预算（var 便于测试注入）；asrInputCap 为整文件上限，
// 超出后分段请求数与内存都不划算，引导改用火山引擎录音文件通道。
const (
	asrMaxB64   = 10 << 20
	asrMaxRaw   = asrMaxB64 / 4 * 3 // 7.5MB
	asrInputCap = 200 << 20
)

var asrSegBytes = 7 << 20

// ASRTool 小米 MiMo 语音识别（mimo-v2.5-asr，同步转写）。
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
		Provider:    "xiaomi",
		Name:        "asr",
		Title:       "语音识别（小米 MiMo）",
		Description: "mimo-v2.5-asr 同步转写（mp3/wav，超 7.5MB 自动分段转写），中英自动识别；无时间戳，输出纯文本",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL（mp3/wav）；留空可配合本地文件直读（无需对象存储）"},
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "", Label: "自动识别"},
				{Value: "zh", Label: "中文"},
				{Value: "en", Label: "英语"},
			}},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audio, source, err := t.resolveInput(ctx, in)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置小米 API Key，或 voxbox config set xiaomi.api_key", ErrNoCred)
	}
	if len(audio) > asrInputCap {
		return provider.TaskOutput{}, fmt.Errorf("音频过大（%.0fMB）：小米同步转写支持 200MB 内自动分段，更长录音请改用火山引擎识别（最长 5 小时）",
			float64(len(audio))/1024/1024)
	}
	mime := sniffAudioMIME(audio)
	if mime == "" {
		return provider.TaskOutput{}, fmt.Errorf("无法识别音频格式：小米 ASR 仅支持 mp3 / wav（请转换格式后重试）")
	}
	lang := paramString(in.Params, "language")

	report(15, "检查音频规格", nil)
	text, totalMS, segs, err := t.transcribeAuto(ctx, audio, mime, lang, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(80, "保存转写文本", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("asr", reqID+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, []byte(text), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}
	summary := map[string]any{
		"char_count":  utf8.RuneCountInString(text),
		"model":       asrModel,
		"source":      source,
		"segment_num": segs,
	}
	if lang != "" {
		summary["language"] = lang
	}
	if totalMS > 0 {
		summary["duration_ms"] = totalMS
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: relPath, Format: "txt",
			Size:       int64(len(text)),
			DurationMS: totalMS,
		}},
		Summary: summary,
	}, nil
}

// transcribeAuto 按官方单次载荷限制（原始音频 ≤7.5MB）自动决定「直传」或
// 「切分逐段转写」：wav 纯 Go 按帧切分（边界吸附静音区），mp3 写临时文件交
// ffmpeg 流拷贝分段（按探测码率折算单段秒数）。返回（拼接文本, 总时长毫秒, 分段数）。
func (t *ASRTool) transcribeAuto(ctx context.Context, audio []byte, mime, lang string, report provider.ProgressReporter) (string, int64, int, error) {
	if len(audio) <= asrSegBytes {
		text, seconds, err := t.client.Transcribe(ctx, ASRInput{Audio: audio, MIME: mime, Language: lang})
		return text, int64(seconds) * 1000, 1, err
	}
	if mime == "audio/wav" {
		if w, perr := provider.ParseWAV(audio); perr == nil {
			return t.transcribeWAVSegments(ctx, w, mime, lang, report)
		}
	}
	return t.transcribeSplitFFmpeg(ctx, audio, mime, lang, report)
}

// transcribeWAVSegments 纯 Go 切分已解析的 WAV 并逐段转写。
func (t *ASRTool) transcribeWAVSegments(ctx context.Context, w *provider.WAV, mime, lang string, report provider.ProgressReporter) (string, int64, int, error) {
	parts := w.Split(0, asrSegBytes, true)
	var acc strings.Builder
	for i, part := range parts {
		report(20+60*i/len(parts), fmt.Sprintf("正在转写第 %d/%d 段", i+1, len(parts)), nil)
		text, _, err := t.client.Transcribe(ctx, ASRInput{Audio: part, MIME: mime, Language: lang})
		if err != nil {
			return "", 0, 0, err
		}
		acc.WriteString(text)
	}
	return acc.String(), int64(w.Duration() * 1000), len(parts), nil
}

// transcribeSplitFFmpeg mp3 等非线性格式的切分通道：临时落盘 → ffprobe 探测
// 码率/时长 → ffmpeg 流拷贝分段 → 逐段读取转写拼接。无 ffmpeg 报安装指引。
func (t *ASRTool) transcribeSplitFFmpeg(ctx context.Context, audio []byte, mime, lang string, report provider.ProgressReporter) (string, int64, int, error) {
	if err := audiotool.EnsureFFmpeg(); err != nil {
		return "", 0, 0, fmt.Errorf("音频超过单次转写限制（7.5MB），自动分段需要 ffmpeg：%w", err)
	}
	ext := ".mp3"
	if mime != "audio/mpeg" {
		ext = ".wav"
	}
	tmp, err := os.CreateTemp("", "xiaomi-asr-*"+ext)
	if err != nil {
		return "", 0, 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(audio); err != nil {
		tmp.Close()
		return "", 0, 0, fmt.Errorf("写入临时音频失败: %w", err)
	}
	tmp.Close()
	info, err := audiotool.ProbeAudio(ctx, tmp.Name())
	if err != nil {
		return "", 0, 0, fmt.Errorf("音频超过单次转写限制（7.5MB），自动分段需要先探测音频信息: %w", err)
	}
	segSec := 0.0
	if info.BitRate > 0 {
		segSec = float64(asrSegBytes) / float64(info.BitRate)
	}
	if segSec <= 0 && info.Duration > 0 {
		// 码率未知：按段数均分总时长
		segSec = info.Duration / float64((len(audio)+asrSegBytes-1)/asrSegBytes)
	}
	if segSec <= 0 {
		return "", 0, 0, fmt.Errorf("音频超过单次转写限制（7.5MB），但无法确定切分参数（码率/时长探测为 0）")
	}
	dir, parts, err := audiotool.SegmentAudioFile(ctx, tmp.Name(), segSec, nil)
	if err != nil {
		return "", 0, 0, err
	}
	defer os.RemoveAll(dir)
	var acc strings.Builder
	for i, part := range parts {
		report(20+60*i/len(parts), fmt.Sprintf("正在转写第 %d/%d 段", i+1, len(parts)), nil)
		raw, err := os.ReadFile(part)
		if err != nil {
			return "", 0, 0, fmt.Errorf("读取分段音频失败: %w", err)
		}
		text, _, err := t.client.Transcribe(ctx, ASRInput{Audio: raw, MIME: mime, Language: lang})
		if err != nil {
			return "", 0, 0, err
		}
		acc.WriteString(text)
	}
	return acc.String(), int64(info.Duration * 1000), len(parts), nil
}

// resolveInput 解析音频字节输入：Files["audio"] 本地文件直读（同步 base64 上传无需对象
// 存储中转，与 URL-only 工具不同）；否则取 params.url 公网地址下载。返回字节与来源标记。
func (t *ASRTool) resolveInput(ctx context.Context, in provider.TaskInput) ([]byte, string, error) {
	if src := in.Files["audio"]; src != "" {
		raw, err := os.ReadFile(src)
		if err != nil {
			return nil, "", fmt.Errorf("读取音频文件失败: %w", err)
		}
		return raw, "file", nil
	}
	u := strings.TrimSpace(paramString(in.Params, "url"))
	if u == "" {
		return nil, "", fmt.Errorf("缺少必填参数: 音频（小米识别需要音频 URL 或本地文件）")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return nil, "", fmt.Errorf("参数错误：音频 URL 仅支持 http(s):// 公网地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	// 不附带 Authorization：目标是用户自己的公网地址，带自有凭证既泄露 api_key 又可能与
	// 签名参数冲突（与 qianwen FetchTranscription 同理）。
	resp, err := asrHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载音频失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("下载音频失败: %w", err)
	}
	return raw, "url", nil
}
