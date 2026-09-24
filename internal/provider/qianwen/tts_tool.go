package qianwen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// TTSTool 千问非流式语音合成（qwen3-tts-flash / qwen3-tts-instruct-flash）。
type TTSTool struct {
	client *TTSClient
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSTool(apiKey, outDir string) *TTSTool {
	return &TTSTool{client: NewTTSClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *TTSTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "qianwen",
		Name:        "tts",
		Title:       "语音合成（千问）",
		Description: "qwen3-tts 非流式合成，48 官方音色，instruct 模型支持自然语言风格指令",
		Group:       "语音",
	}
}

// qwenTTSMaxChars qwen3-tts 系单次合成的字符上限（官方 api-reference：Qwen-TTS 512 tokens，
// 其他模型 600 字符）。
const qwenTTSMaxChars = 600

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本（最多 600 字符）", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: "qwen3-tts-flash", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "qwen3-tts-flash", Label: "qwen3-tts-flash"},
				{Value: "qwen3-tts-instruct-flash", Label: "qwen3-tts-instruct-flash（支持风格指令）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options: VoiceOptions()},
		{Key: "language_type", Label: "语言", Type: provider.ParamString, Group: "参数",
			Placeholder: "Chinese / English / Japanese…（首字母大写）；留空自动"},
		{Key: "instructions", Label: "风格指令", Type: provider.ParamText, Group: "参数",
			Placeholder: "仅 instruct 模型生效：用自然语言描述语速/情感/风格"},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if n := utf8.RuneCountInString(text); n > qwenTTSMaxChars {
		return provider.TaskOutput{}, fmt.Errorf("千问单次合成最多 %d 字符（当前 %d）：长文本请使用火山引擎同步通道（自动分段），或缩短文本", qwenTTSMaxChars, n)
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key", ErrNoCred)
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = "qwen3-tts-flash"
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	if !VoiceSupportsModel(voice, model) {
		return provider.TaskOutput{}, fmt.Errorf("音色 %s 不支持模型 %s：请在音色列表选择支持该模型的音色，或切换模型", voice, model)
	}
	// instructions 仅 instruct 模型支持，flash 不识别该字段
	instructions := ""
	if strings.Contains(model, "instruct") {
		instructions = paramString(in.Params, "instructions")
	}

	report(20, "正在合成", nil)
	res, err := t.client.Synthesize(ctx, TTSReq{
		Model: model, Text: text, Voice: voice,
		LanguageType: paramString(in.Params, "language_type"),
		Instructions: instructions,
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	// 产物格式以上游实际容器为准（无 format 请求参数，由音频 URL 扩展名/Content-Type 推断）
	format := res.Format
	if format == "" {
		format = "wav"
	}
	report(80, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+format)
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	// 绝对 _out 落在 outDir 内归一为相对展示路径，outDir 外保持绝对路径
	// （防 ../ 逃逸，语义与 volcengine tts 及 asr 的 resolveOut 一致）。
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, res.Audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: format,
			Size: int64(len(res.Audio)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"model":      model,
			"voice":      voice,
		},
	}, nil
}

// paramString 与 volcengine 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}
