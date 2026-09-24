package qianwen

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/yann0917/voxbox/internal/provider"
)

// voices.json 由官方音色列表（platform.qianwenai.com
// /docs/developer-guides/speech/voice-list/qwen-tts 非实时节）解析生成：
// 描述/性别/语种/模型支持矩阵/官方试听音频 URL（公开 CDN，可直接播放）。
//
//go:embed voices.json
var voicesJSON []byte

// Voice 内置音色条目（非实时 qwen3-tts 系）。
type Voice struct {
	ID        string   `json:"id"`
	Desc      string   `json:"desc"`
	Gender    string   `json:"gender"`    // 男|女
	Languages []string `json:"languages"` // 支持语种
	Models    []string `json:"models"`    // 支持的非实时模型
	Preview   string   `json:"preview,omitempty"`
}

// Voices 返回内置音色列表（编译期内嵌，无需运行时请求在线接口）。
// 数据由生成脚本保证合法；解析失败属构建期事故，直接 panic 暴露。
func Voices() []Voice {
	var vs []Voice
	if err := json.Unmarshal(voicesJSON, &vs); err != nil {
		panic(fmt.Sprintf("voices.json 损坏: %v", err))
	}
	return vs
}

// VoiceSupportsModel 音色是否支持指定模型；未知音色放行（交上游裁决）。
func VoiceSupportsModel(id, model string) bool {
	for _, v := range Voices() {
		if v.ID == id {
			return slices.Contains(v.Models, model)
		}
	}
	return true
}

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 用；试听/筛选由 /api/voices?provider=qianwen 提供）。
func VoiceOptions() []provider.ParamOption {
	opts := make([]provider.ParamOption, 0)
	for _, v := range Voices() {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.ID + " · " + v.Desc + "（" + v.Gender + "）"})
	}
	return opts
}

// DefaultVoice 默认音色。
const DefaultVoice = "Cherry"
