package openrouter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// TTSTool OpenRouter Gemini 语音合成（flash / flash-lite 双模型，非流式，响应为 mp3 二进制）。
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
		Provider:    "openrouter",
		Name:        "tts",
		Title:       "语音合成（OpenRouter）",
		Description: "Gemini 3.8 Flash(-Lite) TTS 经 OpenRouter 网关合成，30 个预置音色，lite 版输出价更低，响应为 mp3",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: ModelTTS, Group: "参数",
			Options: []provider.ParamOption{
				{Value: ModelTTS, Label: "gemini-3.8-flash-tts（标准）"},
				{Value: ModelTTSLite, Label: "gemini-3.8-flash-lite-tts（输出价更低）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options:     VoiceOptions(),
			Placeholder: "OpenRouter 预置音色（Zephyr 等）"},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	text = pronunciation.Apply(text, "") // 发音词典：合成前文本预处理
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 OpenRouter API Key，或 voxbox config set openrouter.api_key", ErrNoCred)
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	// 模型：空 = 标准版（既有默认）；lite 由前端/CLI 显式选择
	model := paramString(in.Params, "model")

	// 官方未给单次输入上限：整段一次请求，超限由上游错误透出（不做猜测性预切）
	report(20, "正在合成", nil)
	audio, err := t.client.Synthesize(ctx, TTSReq{Text: text, Voice: voice, Model: model})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(95, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+".mp3")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: "mp3",
			Size: int64(len(audio)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"model":      effectiveModel(model),
			"voice":      voice,
		},
	}, nil
}
