package local

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地推理卡:已安装引擎+模型驱动的 TTS/ASR 工具,无凭证。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "local",
		Title:       "本地推理",
		Description: "本地引擎(audio.cpp / sherpa-onnx)驱动已下载模型的合成与识别,离线可用;引擎与模型在设置页「本地环境」下载。",
		Kind:        provider.KindLocal,
		Order:       85,
	}
}
