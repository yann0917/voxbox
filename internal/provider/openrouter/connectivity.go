package openrouter

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeOpenRouter OpenRouter 连通性探测：极短文本合成（消耗少量额度，同千问/小米/智谱模式）。
func ProbeOpenRouter(cfg *config.Config) (string, bool) {
	key := cfg.OpenRouter.APIKey
	if key == "" {
		return "未配置 OpenRouter API Key：请执行 voxbox config set openrouter.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := NewTTSClient(key, BaseURL).Synthesize(ctx, TTSReq{
		Text: "测", Voice: DefaultVoice,
	}); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
