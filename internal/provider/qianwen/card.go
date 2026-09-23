// Package qianwen 千问平台（platform.qianwenai.com）语音服务 provider：
// qwen3-tts 非流式合成 + filetrans 文件转写，Bearer API Key 鉴权，BaseURL 固定官方。
package qianwen

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 设置页「千问平台」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "qianwen",
		Title: "千问平台 · 语音合成 / 语音识别",
		Description: "阿里千问 AI 平台（platform.qianwenai.com）：qwen3-tts 非流式语音合成与 qwen3-asr 文件转写。" +
			"API Key 在平台控制台「API-KEY 管理」创建。",
		Kind:  provider.KindCloud,
		Order: 20,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "qianwen.api_key", Placeholder: "sk-…（控制台创建）",
				Hint: "语音合成与语音识别共用，Bearer 鉴权"},
		},
	}
}
