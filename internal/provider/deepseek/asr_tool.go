package deepseek

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

// asrLimits 官方单次请求限制：16bit PCM WAV / Ogg Opus，文件 ≤8MiB，音频 ≤120 秒。
// 超限音频由服务端自动分段逐段转写后拼接；asrInputCap 为整文件上限，超出后分段
// 请求数与内存都不划算，引导改用火山引擎录音文件通道。
const (
	asrMaxSecs  = 118.0 // 切分预算：官方 120 秒上限留 2 秒头部余量
	asrInputCap = 200 << 20
)

// asrSegBytes 单段字节预算：官方 8MiB 上限留 64KiB 头部余量（var 便于测试注入）。
var asrSegBytes = (8 << 20) - 64<<10

// ASRTool DeepSeek 语音识别（/v1/tools/asr，multipart 直传同步转写）。
// 无时间戳（不产 SRT）；120 秒/8MiB 单次限制定位为「短音频/一句话」级转写。
type ASRTool struct {
	client *Client
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewASRTool(apiKey, outDir string) *ASRTool {
	return &ASRTool{client: NewClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "deepseek",
		Name:        "asr",
		Title:       "语音识别（DeepSeek）",
		Description: "同步转写（16bit PCM WAV / Ogg Opus 直收，其余格式自动转码；超 120 秒/8MiB 自动分段转写），21 语种自动识别；无时间戳，输出纯文本",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL（wav/ogg 等）；留空可配合本地文件直传（无需对象存储）"},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audio, source, err := t.resolveInput(ctx, in)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 DeepSeek API Key，或 voxbox config set deepseek.api_key", ErrNoCred)
	}
	if len(audio) > asrInputCap {
		return provider.TaskOutput{}, fmt.Errorf("音频过大（%.0fMB）：DeepSeek 同步转写支持 200MB 内自动分段，更长录音请改用火山引擎识别（最长 5 小时）",
			float64(len(audio))/1024/1024)
	}

	report(15, "检查音频规格", nil)
	segs, wavMS, err := t.prepareSegments(ctx, audio, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	var acc strings.Builder
	var totalMS int64
	for i, seg := range segs {
		if len(segs) > 1 {
			report(20+60*i/len(segs), fmt.Sprintf("正在转写第 %d/%d 段", i+1, len(segs)), nil)
		}
		text, ms, err := t.client.Transcribe(ctx, seg)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		acc.WriteString(text)
		totalMS += ms
	}
	if totalMS == 0 {
		totalMS = wavMS // 上游未回时长时以 WAV 解析值兜底
	}
	report(80, "保存转写文本", nil)

	text := acc.String()
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
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: relPath, Format: "txt",
			Size:       int64(len(text)),
			DurationMS: totalMS,
		}},
		Summary: map[string]any{
			"char_count":  utf8.RuneCountInString(text),
			"source":      source,
			"segment_num": len(segs),
			"duration_ms": totalMS,
		},
	}, nil
}

// prepareSegments 把输入音频准备成 ≤官方单次限制的分段列表，返回（分段字节列表,
// WAV 解析的整文件时长毫秒）：
//   - 16bit 整数 PCM 且采样率在官方白名单内的 WAV：纯 Go 按帧切分（边界吸附静音区）；
//   - 其余输入（mp3/m4a/flac/ogg、float/多 bit WAV、白名单外采样率）：统一经 ffmpeg
//     转码为 24kHz 单声道 16bit WAV 再切——官方仅收 16bit PCM WAV / Ogg Opus，
//     统一转 WAV 让切分与预算判断落在同一容器上（Ogg 直传省转码的价值不值得多养一条路径）。
func (t *ASRTool) prepareSegments(ctx context.Context, audio []byte, report provider.ProgressReporter) ([][]byte, int64, error) {
	if w, err := provider.ParseWAV(audio); err == nil && isDirectWAV(w) {
		return w.Split(asrMaxSecs, asrSegBytes, true), int64(w.Duration() * 1000), nil
	}
	report(15, "转换音频格式", nil)
	if err := audiotool.EnsureFFmpeg(); err != nil {
		return nil, 0, fmt.Errorf("音频需转换为官方支持的 16bit PCM WAV，自动转换需要 ffmpeg：%w", err)
	}
	tmp, err := os.CreateTemp("", "deepseek-asr-in-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(audio); err != nil {
		tmp.Close()
		return nil, 0, fmt.Errorf("写入临时音频失败: %w", err)
	}
	tmp.Close()
	out, err := os.CreateTemp("", "deepseek-asr-*.wav")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(out.Name())
	if err := audiotool.TranscodeWAV(ctx, tmp.Name(), out.Name()); err != nil {
		return nil, 0, fmt.Errorf("音频转换失败（官方仅收 16bit PCM WAV / Ogg Opus）: %w", err)
	}
	raw, err := os.ReadFile(out.Name())
	if err != nil {
		return nil, 0, fmt.Errorf("读取转换结果失败: %w", err)
	}
	w, err := provider.ParseWAV(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("转换后的 WAV 解析失败: %w", err)
	}
	return w.Split(asrMaxSecs, asrSegBytes, true), int64(w.Duration() * 1000), nil
}

// isDirectWAV 官方直传条件：16bit 整数 PCM（FormatTag=1）+ 采样率白名单
// （8000/16000/22050/24000/32000/44100/48000 Hz）。
func isDirectWAV(w *provider.WAV) bool {
	if w.FormatTag != 1 || w.BitsPerSample != 16 {
		return false
	}
	switch w.SampleRate {
	case 8000, 16000, 22050, 24000, 32000, 44100, 48000:
		return true
	}
	return false
}

// resolveInput 解析音频字节输入：Files["audio"] 本地文件直读（同步 multipart 上传无需
// 对象存储中转）；否则取 params.url 公网地址下载。返回字节与来源标记。
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
		return nil, "", fmt.Errorf("缺少必填参数: 音频（DeepSeek 识别需要音频 URL 或本地文件）")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return nil, "", fmt.Errorf("参数错误：音频 URL 仅支持 http(s):// 公网地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	// 不附带 Authorization：目标是用户自己的公网地址，带自有凭证既泄露 api_key 又可能与
	// 签名参数冲突（与 xiaomi/zhipu 同理）。
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
