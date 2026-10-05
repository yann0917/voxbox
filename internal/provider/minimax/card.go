package minimax

import (
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard 设置页「MiniMax」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "minimax",
		Title: "MiniMax · 语音合成 / 语音识别 / 大模型",
		Description: "MiniMax 开放平台（minimax.cn）：speech-2.8 同步/长文本异步合成（327 系统音色）、" +
			"asr-1.0 识别（说话人分离/句级时间戳/SRT）、MiniMax-M3 与 M2.7 文本大模型（AI 助手可用）。" +
			"API Key 在 platform.minimax.cn「接口密钥」创建。",
		Kind:  provider.KindCloud,
		Order: 24,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "minimax.api_key", Placeholder: "platform.minimax.cn 创建",
				Hint: "语音合成/识别/大模型共用，Bearer 鉴权"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"api_key": cfg.Minimax.APIKey}
		},
		// secret 留空=不修改。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["api_key"]; ok && v != "" {
				nc.Minimax.APIKey = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Minimax = src.Minimax },
		Test:       ProbeMinimax,
	}
}
