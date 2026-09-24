// Package xiaomi 小米 MiMo 开放平台（api.xiaomimimo.com）语音合成 provider：
// MiMo-V2.5-TTS 系走 OpenAI 兼容 chat/completions 协议，Bearer API Key 鉴权，BaseURL 固定官方。
package xiaomi

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 设置页「小米 MiMo」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "xiaomi",
		Title: "小米 MiMo · 语音合成 / 语音识别",
		Description: "小米 MiMo 开放平台（platform.xiaomimimo.com）：MiMo-V2.5-TTS 系列语音合成与 mimo-v2.5-asr 同步转写" +
			"（OpenAI 兼容协议）。API Key 在平台控制台「API Keys」创建。",
		Kind:  provider.KindCloud,
		Order: 21,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "xiaomi.api_key", Placeholder: "在 MiMo 开放平台控制台创建",
				Hint: "语音合成与语音识别共用，Bearer 鉴权"},
		},
	}
}
