package xiaomi

import "github.com/yann0917/voxbox/internal/provider"

// Voice 预置音色条目（官方文档音色表，2026-09-24 校准）。
// 仅 mimo-v2.5-tts 支持 audio.voice；voicedesign 音色由文本描述生成，无预置音色。
type Voice struct {
	ID       string `json:"id"`
	Desc     string `json:"desc"`
	Gender   string `json:"gender"`   // 男|女
	Language string `json:"language"` // 中文|英文
}

// presetVoices 预置音色（mimo-v2.5-tts 可选；mimo_default 随集群，中文集群为冰糖）。
var presetVoices = []Voice{
	{ID: "mimo_default", Desc: "默认音色（中文集群为冰糖）", Gender: "女", Language: "中文"},
	{ID: "冰糖", Desc: "中文女声", Gender: "女", Language: "中文"},
	{ID: "茉莉", Desc: "中文女声", Gender: "女", Language: "中文"},
	{ID: "苏打", Desc: "中文男声", Gender: "男", Language: "中文"},
	{ID: "白桦", Desc: "中文男声", Gender: "男", Language: "中文"},
	{ID: "Mia", Desc: "英文女声", Gender: "女", Language: "英文"},
	{ID: "Chloe", Desc: "英文女声", Gender: "女", Language: "英文"},
	{ID: "Milo", Desc: "英文男声", Gender: "男", Language: "英文"},
	{ID: "Dean", Desc: "英文男声", Gender: "男", Language: "英文"},
}

// DefaultVoice 官方默认音色。
const DefaultVoice = "mimo_default"

// Voices 返回预置音色列表（编译期常量表，无需运行时请求）。
func Voices() []Voice { return presetVoices }

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 用）。
func VoiceOptions() []provider.ParamOption {
	opts := make([]provider.ParamOption, 0, len(presetVoices))
	for _, v := range presetVoices {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.ID + " · " + v.Desc})
	}
	return opts
}
