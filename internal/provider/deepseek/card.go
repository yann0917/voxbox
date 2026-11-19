package deepseek

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard 设置页「DeepSeek」凭证卡声明。语音合成/识别任务工具与 AI 助手共用
// 同一个 API Key，保存后热重载覆盖重注册。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "deepseek",
		Title: "DeepSeek · 大模型 / 语音 / 联网搜索",
		Description: "DeepSeek 开放平台（deepseek.com）：deepseek-flash 与 deepseek-v4-pro 文本大模型（AI 助手可用），" +
			"网页搜索与抓取的联网能力（按成功调用计费，约 ¥5/千次），" +
			"以及语音合成（stella/tide 双音色，限时 ¥1.5/万字符）与语音识别（21 语种，限时 ¥0.39/小时音频）。" +
			"API Key 在 platform.deepseek.com 创建。",
		Kind:  provider.KindCloud,
		Order: 19, // 千问(20)之上：用户指定的设置页排列位置
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "deepseek.api_key", Placeholder: "platform.deepseek.com 创建",
				Hint: "大模型对话、联网搜索与语音合成/识别共用，Bearer 鉴权"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"api_key": cfg.Deepseek.APIKey}
		},
		// secret 留空=不修改。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["api_key"]; ok && v != "" {
				nc.Deepseek.APIKey = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Deepseek = src.Deepseek },
		Test:       ProbeDeepseek,
	}
}

// ProbeDeepseek DeepSeek 连通性探测：/models 只读接口零消耗，即可验证鉴权有效性。
func ProbeDeepseek(cfg *config.Config) (string, bool) {
	key := cfg.Deepseek.APIKey
	if key == "" {
		return "未配置 DeepSeek API Key：请执行 voxbox config set deepseek.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := NewClient(key, BaseURL).ProbeModels(ctx); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
