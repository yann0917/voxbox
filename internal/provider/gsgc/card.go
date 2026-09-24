package gsgc

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地能力卡：格式工厂站点工具（匿名），只读展示。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "gsgc",
		Title:       "站点工具（格式工厂）",
		Description: "人声分离与音视频/图片转换压缩的站点协议通道，匿名可用、无凭证。",
		Kind:        provider.KindLocal,
		Order:       95,
	}
}
