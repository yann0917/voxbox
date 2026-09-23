package volcengine

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// voices.json 由火山引擎在线音色列表（docs 6561/1257544）解析生成：
// 含豆包语音合成模型 2.0（含外语）、1.0 全量音色；S2S/SC 端到端实时模型专用音色（jupiter/saturn
// 前缀）不属于 TTS 可用列表，已排除。字段供 Web 端按场景/语种筛选。
//
//go:embed voices.json
var voicesJSON []byte

// Voice 内置音色条目。
type Voice struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Gender     string   `json:"gender"` // 男|女（由 voice_type 推断）
	Scenes     []string `json:"scenes"` // 通用场景/角色扮演/视频配音/教育场景/客服场景/有声阅读/外语音色/多情感/趣味口音
	Languages  []string `json:"languages"`
	Dialects   []string `json:"dialects,omitempty"` // 中文方言（粤语/上海/北京……）
	Tags       []string `json:"tags,omitempty"`     // 特殊标签（抖音同款/豆包同款/剪映同款……）
	Emotions   []string `json:"emotions,omitempty"` // 1.0 多情感音色支持的情感参数
	Generation string   `json:"generation"`         // 2.0|1.0
	Note       string   `json:"note,omitempty"`     // 备注（如：仅支持单向流，双向流调用会直接报错）
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
