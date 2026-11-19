package deepseek

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// ttsMaxBillingChars 官方单次上限：10,000 计费字符（汉字按 2 计，其余按 1）。
const ttsMaxBillingChars = 10000

// TTSTool DeepSeek 语音合成（/v1/tools/tts 整段通道）：stella/tide 双官方音色，
// 十语种按文本自动识别，返回 24kHz WAV。
type TTSTool struct {
	client *Client
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSTool(apiKey, outDir string) *TTSTool {
	return &TTSTool{client: NewClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *TTSTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "deepseek",
		Name:        "tts",
		Title:       "语音合成（DeepSeek）",
		Description: "stella / tide 双官方音色，十语种按文本自动识别（含粤语）；单次 ≤10,000 计费字符（汉字按 2 计），返回 wav",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options: VoiceOptions()},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	text = pronunciation.Apply(text, "") // 发音词典：合成前文本预处理（无语言参数，仅全语言词条生效）
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 DeepSeek API Key，或 voxbox config set deepseek.api_key", ErrNoCred)
	}
	if n := billingChars(text); n > ttsMaxBillingChars {
		return provider.TaskOutput{}, fmt.Errorf("文本超出单次合成上限（%d 计费字符，当前约 %d，汉字按 2 计）：请拆分后分段合成", ttsMaxBillingChars, n)
	}
	voice := paramString(in.Params, "voice")

	report(20, "正在合成", nil)
	audio, err := t.client.Synthesize(ctx, text, voice)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(80, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+".wav")
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
			Kind: "audio", Path: relPath, Format: "wav",
			Size: int64(len(audio)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"voice":      voiceOr(voice),
		},
	}, nil
}

// voiceOr 供 summary 呈现：未显式选音色时按服务端实际生效的默认音色回填。
func voiceOr(v string) string {
	if v == "" {
		return DefaultVoice
	}
	return v
}

// billingChars 按官方计费口径估算字符数：汉字等非 ASCII rune 按 2 计，其余按 1
// （官方口径为「汉字算 2 个字符」，以非 ASCII 近似——预检只用于给出明确的上限错误，
// 宁多拦不漏拦，真实计费以官方为准）。
func billingChars(text string) int {
	n := 0
	for _, r := range text {
		if r > 0x7F {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// resolveOut 相对路径锚定 outDir 并回算相对形态；绝对 _out 仅当落在 outDir 内时
// 归一为相对展示路径，outDir 外保持绝对（与 qianwen/xiaomi/volcengine tts 同款）。
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

// paramString 与 qianwen/xiaomi 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}
