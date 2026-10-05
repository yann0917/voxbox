package volcengine

import (
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard 火山语音凭证卡（文案沿用现设置页：播客必须 APP ID+Token，TTS/ASR 双轨凭证）。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "volcengine",
		Title: "火山引擎 · 语音合成 / 识别 / 播客",
		Description: "播客生成必须 APP ID + Access Token（播客仅支持这对凭证）；TTS / 语音识别两者皆可——" +
			"APP ID + Access Token，或新版 API Key 单键。凭证在语音技术控制台（console.volcengine.com/speech）创建。",
		Kind:  provider.KindCloud,
		Order: 10,
		Fields: []provider.CredentialField{
			{Key: "app_id", Label: "APP ID", Kind: provider.FieldText, ConfigKey: "volc.speech.app_id",
				Placeholder: "火山控制台获取"},
			{Key: "access_token", Label: "Access Token", Kind: provider.FieldSecret, ConfigKey: "volc.speech.access_token",
				Placeholder: "留空表示不修改", Hint: "与 APP ID 配套"},
			{Key: "api_key", Label: "新版 API Key", Kind: provider.FieldSecret, ConfigKey: "volc.speech.api_key",
				Placeholder: "留空表示不修改", Hint: "仅 TTS / 语音识别可用，播客不支持"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{
				"app_id":       cfg.Volc.Speech.AppID,
				"access_token": cfg.Volc.Speech.AccessToken,
				"api_key":      cfg.Volc.Speech.APIKey,
			}
		},
		// 播客必须凭证对（APP ID+Token），TTS/ASR 双轨：凭证对或单 API Key 任一可用即已配置。
		Configured: func(vals map[string]string) bool {
			return (vals["app_id"] != "" && vals["access_token"] != "") || vals["api_key"] != ""
		},
		// app_id 是 text 字段：提交值即生效，空串=清空（播客凭证对可整体撤销）；
		// secret 字段保留「空串=不修改」守卫。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["app_id"]; ok {
				nc.Volc.Speech.AppID = v
			}
			if v, ok := fields["access_token"]; ok && v != "" {
				nc.Volc.Speech.AccessToken = v
			}
			if v, ok := fields["api_key"]; ok && v != "" {
				nc.Volc.Speech.APIKey = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Volc = src.Volc },
		Test:       ProbeSpeech,
	}
}

// MediaKitCard AI MediaKit 人声分离凭证卡（独立于火山语音凭证体系，落盘仍 volc.mediakit.*）。
func MediaKitCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "mediakit",
		Title: "AI MediaKit · 人声分离",
		Description: "仅用于人声背景音分离，与火山语音是两套独立凭证。" +
			"API Key 在火山引擎控制台（console.volcengine.com）的 AI MediaKit 服务创建。",
		Kind:  provider.KindCloud,
		Order: 15,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "MediaKit API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "volc.mediakit.api_key", Placeholder: "在 AI MediaKit 控制台创建"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"api_key": cfg.Volc.MediaKit.APIKey}
		},
		// 分离工具在 volcengine 包（与语音卡共用工具集与 volc 配置段）：同重注册、同段同步。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["api_key"]; ok && v != "" {
				nc.Volc.MediaKit.APIKey = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Volc = src.Volc },
		Test:       ProbeMediaKit,
	}
}
