package zhipu

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeZhipu 智谱连通性探测：极短文本合成（消耗少量额度，同千问/小米模式）。
func ProbeZhipu(cfg *config.Config) (string, bool) {
	key := cfg.Zhipu.APIKey
	if key == "" {
		return "未配置智谱 API Key：请执行 voxbox config set zhipu.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := NewTTSClient(key, BaseURL).Synthesize(ctx, TTSSynthesizeReq{
		Text: "测", Voice: DefaultVoice,
	}); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
