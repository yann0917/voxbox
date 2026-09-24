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
)

// asrMaxB64 官方载荷上限：input_audio.data 的 base64 字符串 ≤10MB；
// asrMaxRaw 换算回原始字节上限（base64 膨胀 4/3）。
const (
	asrMaxB64 = 10 << 20
	asrMaxRaw = asrMaxB64 / 4 * 3 // 7.5MB
)

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
		Description: "mimo-v2.5-asr 同步转写（mp3/wav，音频 ≤7.5MB），中英自动识别；无时间戳，输出纯文本",
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
	if len(audio) > asrMaxRaw {
		return provider.TaskOutput{}, fmt.Errorf("音频载荷超限：base64 编码后须 ≤10MB（原始音频 ≤7.5MB，当前 %.1fMB）",
			float64(len(audio))/1024/1024)
	}
	mime := sniffAudioMIME(audio)
	if mime == "" {
		return provider.TaskOutput{}, fmt.Errorf("无法识别音频格式：小米 ASR 仅支持 mp3 / wav（请转换格式后重试）")
	}
	lang := paramString(in.Params, "language")

	report(20, "正在转写", nil)
	text, seconds, err := t.client.Transcribe(ctx, ASRInput{Audio: audio, MIME: mime, Language: lang})
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
		"char_count": utf8.RuneCountInString(text),
		"model":      asrModel,
		"source":     source,
	}
	if lang != "" {
		summary["language"] = lang
	}
	if seconds > 0 {
		summary["duration_ms"] = int64(seconds) * 1000
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: relPath, Format: "txt",
			Size:       int64(len(text)),
			DurationMS: int64(seconds) * 1000,
		}},
		Summary: summary,
	}, nil
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
