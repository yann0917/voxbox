package audiotool

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地能力卡：ffmpeg 音频剪辑 12 工具，无凭证、只读展示。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "audiotool",
		Title:       "本地音频剪辑",
		Description: "ffmpeg 本地处理：裁剪/合并/变调/均衡/闪避等 12 个工具，无凭证、离线可用。",
		Kind:        provider.KindLocal,
		Order:       90,
	}
}
