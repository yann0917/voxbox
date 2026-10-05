// Package openrouter OpenRouter 聚合网关（openrouter.ai）语音 provider：
// Google Gemini 语音合成（google/gemini-3.8-flash-tts）经 OpenRouter 文本转语音接口，
// Bearer API Key 鉴权，BaseURL 固定官方。
package openrouter

import (
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard 设置页「OpenRouter」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "openrouter",
		Title: "OpenRouter · Gemini 语音合成",
		Description: "OpenRouter 聚合网关（openrouter.ai）：google/gemini-3.8-flash-tts 语音合成，" +
			"30 个预置英文音色。API Key 在 openrouter.ai/settings/keys 创建。",
		Kind:  provider.KindCloud,
		Order: 23,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "openrouter.api_key", Placeholder: "openrouter.ai/settings/keys 创建",
				Hint: "按用量计费，Bearer 鉴权"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"api_key": cfg.OpenRouter.APIKey}
		},
		// secret 留空=不修改。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["api_key"]; ok && v != "" {
				nc.OpenRouter.APIKey = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.OpenRouter = src.OpenRouter },
		Test:       ProbeOpenRouter,
	}
}
