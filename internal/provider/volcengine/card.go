package volcengine

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 火山语音凭证卡（文案沿用现设置页：播客必须 APP ID+Token，TTS/ASR 双轨凭证）。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "volcengine",
		Title: "火山引擎 · 语音合成 / 识别 / 播客",
		Description: "播客生成必须 APP ID + Access Token（播客协议只认这对凭证）；TTS / 语音识别两者皆可——" +
			"APP ID + Access Token，或新版 API Key 单键。",
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
	}
}

// MediaKitCard AI MediaKit 人声分离凭证卡（独立于火山语音凭证体系，落盘仍 volc.mediakit.*）。
func MediaKitCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "mediakit",
		Title:       "AI MediaKit · 人声分离",
		Description: "仅用于人声背景音分离，与火山语音是两套独立凭证。",
		Kind:        provider.KindCloud,
		Order:       15,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "MediaKit API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "volc.mediakit.api_key", Placeholder: "在 AI MediaKit 控制台创建"},
		},
	}
}
