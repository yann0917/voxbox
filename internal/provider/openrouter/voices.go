package openrouter

import "github.com/yann0917/voxbox/internal/provider"

// Voice 预置音色条目（OpenRouter Gemini TTS voice 枚举，2026-09-29 校准，全量 30 个）。
type Voice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// presetVoices 预置音色。OpenRouter 接口无音色列表端点，枚举取自模型卡文档；
// 官方未附风格注解，不做意译，Label 即音色名。
var presetVoices = []Voice{
	{ID: "Zephyr", Label: "Zephyr"},
	{ID: "Puck", Label: "Puck"},
	{ID: "Charon", Label: "Charon"},
	{ID: "Kore", Label: "Kore"},
	{ID: "Fenrir", Label: "Fenrir"},
	{ID: "Leda", Label: "Leda"},
	{ID: "Orus", Label: "Orus"},
	{ID: "Aoede", Label: "Aoede"},
	{ID: "Callirrhoe", Label: "Callirrhoe"},
	{ID: "Autonoe", Label: "Autonoe"},
	{ID: "Enceladus", Label: "Enceladus"},
	{ID: "Iapetus", Label: "Iapetus"},
	{ID: "Umbriel", Label: "Umbriel"},
	{ID: "Algieba", Label: "Algieba"},
	{ID: "Despina", Label: "Despina"},
	{ID: "Erinome", Label: "Erinome"},
	{ID: "Algenib", Label: "Algenib"},
	{ID: "Rasalgethi", Label: "Rasalgethi"},
	{ID: "Laomedeia", Label: "Laomedeia"},
	{ID: "Achernar", Label: "Achernar"},
	{ID: "Alnilam", Label: "Alnilam"},
	{ID: "Schedar", Label: "Schedar"},
	{ID: "Gacrux", Label: "Gacrux"},
	{ID: "Pulcherrima", Label: "Pulcherrima"},
	{ID: "Achird", Label: "Achird"},
	{ID: "Zubenelgenubi", Label: "Zubenelgenubi"},
	{ID: "Vindemiatrix", Label: "Vindemiatrix"},
	{ID: "Sadachbia", Label: "Sadachbia"},
	{ID: "Sadaltager", Label: "Sadaltager"},
	{ID: "Sulafat", Label: "Sulafat"},
}

// DefaultVoice 默认音色（模型卡示例同款，列表首项）。
const DefaultVoice = "Zephyr"

// 合成模型（OpenRouter 模型卡 ID）：flash 标准版 / lite 轻量版（输出价更低）。
const (
	ModelTTS     = "google/gemini-3.8-flash-tts"      // 标准版（既有默认，行为不变）
	ModelTTSLite = "google/gemini-3.8-flash-lite-tts" // 轻量版（输出价更低）
)

// Voices 返回预置音色列表（静态表：接口无音色列表端点）。
func Voices() []Voice { return presetVoices }

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 用）。
func VoiceOptions() []provider.ParamOption {
	opts := make([]provider.ParamOption, 0, len(presetVoices))
	for _, v := range presetVoices {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.Label})
	}
	return opts
}
