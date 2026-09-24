// Package zhipu 智谱开放平台（open.bigmodel.cn）语音 provider：
// glm-tts 非流式合成（响应为音频二进制）、glm-asr-2512 短音频转写（multipart 直传，≤25MB/30s）、
// 音色列表/复刻/删除（复刻与删除仅对接接口，前端暂不展示页面），Bearer API Key 鉴权，BaseURL 固定官方。
package zhipu

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 设置页「智谱开放平台」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "zhipu",
		Title: "智谱开放平台 · 语音合成 / 语音识别",
		Description: "智谱 AI 开放平台（bigmodel.cn）：glm-tts 语音合成（官方音色 + 复刻音色）与 glm-asr-2512 短音频转写" +
			"（≤25MB / 30 秒）。API Key 在平台「API Keys」页创建。",
		Kind:  provider.KindCloud,
		Order: 22,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "zhipu.api_key", Placeholder: "bigmodel.cn 用户中心创建",
				Hint: "语音合成/识别/音色管理共用，Bearer 鉴权"},
		},
	}
}
