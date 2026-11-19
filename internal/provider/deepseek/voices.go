package deepseek

import "github.com/yann0917/voxbox/internal/provider"

// Voice 预置音色条目（官方 guides/tts 音色表，2026-11-19 校准：仅两枚官方音色）。
// 语种不区分音色（zh/yue/en/ja/ko/ms/ar/id/th/vi 十语种按文本自动识别，无语种参数）。
type Voice struct {
	ID     string `json:"id"`
	Desc   string `json:"desc"`
	Gender string `json:"gender"`
}

// presetVoices 官方音色（stella 为服务端默认）。
var presetVoices = []Voice{
	{ID: "stella", Desc: "海星 · 女声（默认）", Gender: "女"},
	{ID: "tide", Desc: "暗潮 · 男声", Gender: "男"},
}

// DefaultVoice 官方默认音色。
const DefaultVoice = "stella"

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 与音色路由共用，{value,label} 形态）。
func VoiceOptions() []provider.ParamOption {
	opts := make([]provider.ParamOption, 0, len(presetVoices))
	for _, v := range presetVoices {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.ID + " · " + v.Desc})
	}
	return opts
}
