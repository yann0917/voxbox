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

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: "qwen3-tts-flash", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "qwen3-tts-flash", Label: "qwen3-tts-flash"},
				{Value: "qwen3-tts-instruct-flash", Label: "qwen3-tts-instruct-flash（支持风格指令）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options: VoiceOptions()},
		{Key: "language_type", Label: "语言", Type: provider.ParamString, Group: "参数",
			Placeholder: "如 Chinese / English；留空不指定"},
		{Key: "instructions", Label: "风格指令", Type: provider.ParamText, Group: "参数",
			Placeholder: "仅 instruct 模型生效：用自然语言描述语速/情感/风格"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum, Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{{Value: "mp3", Label: "MP3"}, {Value: "wav", Label: "WAV"}}},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("未配置千问 API Key：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key")
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = "qwen3-tts-flash"
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}

	report(20, "正在合成", nil)
	res, err := t.client.Synthesize(ctx, TTSReq{
		Model: model, Text: text, Voice: voice,
		LanguageType: paramString(in.Params, "language_type"),
		Instructions: paramString(in.Params, "instructions"),
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	// 上游格式与请求不一致时以实际容器为准
	if res.Format != "" {
		format = res.Format
	}
	report(80, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+format)
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath := relPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.outDir, relPath)
	}
	relPath, _ = filepath.Rel(t.outDir, absPath)
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
