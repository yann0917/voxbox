package gsgc

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地能力卡：格式工厂在线工具（免费），只读展示。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "gsgc",
		Title:       "格式工厂 · 在线转换",
		Description: "格式工厂在线版：音视频/图片转换压缩与人声分离，云端执行、免费可用。",
		Kind:        provider.KindLocal,
		Order:       95,
	}
}
