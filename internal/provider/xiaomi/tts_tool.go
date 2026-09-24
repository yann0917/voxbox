package xiaomi

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

const (
	ModelPreset      = "mimo-v2.5-tts"
	ModelVoiceDesign = "mimo-v2.5-tts-voicedesign"
)

// TTSTool 小米 MiMo 语音合成（MiMo-V2.5-TTS 系列，OpenAI 兼容协议）。
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
		Provider:    "xiaomi",
		Name:        "tts",
		Title:       "语音合成（小米 MiMo）",
		Description: "MiMo-V2.5-TTS 系列合成，9 官方预置音色，支持自然语言风格指令与文本描述定制音色",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: ModelPreset, Group: "参数",
			Options: []provider.ParamOption{
				{Value: ModelPreset, Label: "mimo-v2.5-tts（预置音色）"},
				{Value: ModelVoiceDesign, Label: "mimo-v2.5-tts-voicedesign（音色由文本描述定制）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options: VoiceOptions()},
		{Key: "instructions", Label: "风格指令", Type: provider.ParamText, Group: "参数",
			Placeholder: "自然语言描述语速/情感/风格；voicedesign 模型下为音色描述（必填）"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum, Default: "wav", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "wav", Label: "wav"},
				{Value: "mp3", Label: "mp3"},
			}},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置小米 API Key，或 voxbox config set xiaomi.api_key", ErrNoCred)
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = ModelPreset
	}
	format := paramString(in.Params, "format")
	if format == "" {
		format = "wav"
	}
	// 预置音色仅 mimo-v2.5-tts 生效：voicedesign 音色由描述生成，audio.voice 上游不支持
	voice := DefaultVoice
	instructions := paramString(in.Params, "instructions")
	if model == ModelVoiceDesign {
		if instructions == "" {
			return provider.TaskOutput{}, fmt.Errorf("voicedesign 模型需要填写音色描述（用自然语言描述想要的音色）")
		}
		voice = ""
	} else if v := paramString(in.Params, "voice"); v != "" {
		voice = v
	}

	report(20, "正在合成", nil)
	audio, err := t.client.Synthesize(ctx, TTSReq{
		Model: model, Text: text, Voice: voice,
		Instructions: instructions, Format: format,
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(80, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+format)
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	// 绝对 _out 落在 outDir 内归一为相对展示路径，outDir 外保持绝对路径
	// （防 ../ 逃逸，语义与 qianwen/volcengine tts 的 resolveOut 一致）。
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: format,
			Size: int64(len(audio)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"model":      model,
			"voice":      voice,
		},
	}, nil
}

// resolveOut 相对路径锚定 outDir 并回算相对形态；绝对 _out 仅当落在 outDir 内时
// 归一为相对展示路径，outDir 外保持绝对——相对化会回算出 ../ 逃逸路径，
// 破坏产物越界防护与 CLI --json 对绝对 path 的消费约定（与 qianwen 同款实现）。
func resolveOut(outDir, p string) (abs, rel string) {
	outDir = filepath.Clean(outDir)
	abs = p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(outDir, p)
	}
	if !strings.HasPrefix(abs, outDir+string(filepath.Separator)) {
		return abs, abs
	}
	rel, _ = filepath.Rel(outDir, abs)
	return abs, rel
}

// paramString 与 qianwen 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}
