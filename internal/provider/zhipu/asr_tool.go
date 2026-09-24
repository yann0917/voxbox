package zhipu

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

// zhipuASRLimits 官方限制：wav/mp3，文件 ≤25MB，音频 ≤30 秒（时长无法本地精确判定，
// 文件体积先行拦截，超时长交上游报错）。
const (
	zhipuASRMaxBytes = 25 << 20
	zhipuASRMaxSecs  = 30
)

// ASRTool 智谱语音识别（glm-asr-2512）：multipart 直传，同步返回转写文本。
// 无时间戳（不产 SRT）；30 秒上限定位为「短音频/一句话」级转写。
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
		Provider:    "zhipu",
		Name:        "asr",
		Title:       "语音识别（智谱）",
		Description: "glm-asr-2512 短音频转写（wav/mp3 ≤25MB、≤30 秒），多语言，支持热词与上下文；输出纯文本",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL（wav/mp3）；留空可配合本地文件直传（无需对象存储）"},
		{Key: "prompt", Label: "上下文", Type: provider.ParamText, Group: "参数",
			Placeholder: "长文本场景提供之前的转录结果作为上下文（建议 <8000 字）"},
		{Key: "hotwords", Label: "热词", Type: provider.ParamString, Group: "参数",
			Placeholder: "逗号分隔，提升领域词汇识别率（≤100 个）"},
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
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置智谱 API Key，或 voxbox config set zhipu.api_key", ErrNoCred)
	}
	if fi, statErr := os.Stat(audioPath); statErr == nil && fi.Size() > zhipuASRMaxBytes {
		return provider.TaskOutput{}, fmt.Errorf("音频载荷超限：智谱识别仅支持 25MB 内文件（当前 %.1fMB）",
			float64(fi.Size())/1024/1024)
	}
	// 官方仅收 wav/mp3：按扩展名白名单拦截（魔数校验交上游，避免误杀非标头文件）
	ext := strings.ToLower(filepath.Ext(audioPath))
	if ext != ".wav" && ext != ".mp3" {
		return provider.TaskOutput{}, fmt.Errorf("参数错误：智谱识别仅支持 wav / mp3 音频（当前 %s）", ext)
	}

	report(20, "正在转写", nil)
	text, err := t.client.Transcribe(ctx, audioPath,
		paramString(in.Params, "prompt"), splitHotwords(paramString(in.Params, "hotwords")))
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
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: relPath, Format: "txt",
			Size: int64(len(text)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"model":      "glm-asr-2512",
			"source":     source,
		},
	}, nil
}

// resolveInput 解析音频文件输入：Files["audio"] 本地文件直用；否则取 params.url 下载到
// 临时文件后转传（transcriptions 仅收 multipart，无对象存储中转需求）。
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
		return "", "", fmt.Errorf("缺少必填参数: 音频（智谱识别需要音频 URL 或本地文件）")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", fmt.Errorf("参数错误：音频 URL 仅支持 http(s):// 公网地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	// 不附带 Authorization：目标是用户自己的公网地址，不外泄凭证（与 xiaomi 同理）
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("下载音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("下载音频失败: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "zhipu-asr-*"+strings.ToLower(filepath.Ext(u)))
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

// splitHotwords 逗号分隔热词 → 去空切片。
func splitHotwords(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
