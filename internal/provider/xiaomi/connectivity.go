package xiaomi

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeXiaomi 小米 MiMo 连通性探测：极短文本合成（消耗少量额度，同千问/智谱模式）。
func ProbeXiaomi(cfg *config.Config) (string, bool) {
	key := cfg.Xiaomi.APIKey
	if key == "" {
		return "未配置小米 API Key：请执行 voxbox config set xiaomi.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := NewTTSClient(key, BaseURL).Synthesize(ctx, TTSReq{
		Model: ModelPreset, Text: "测", Voice: DefaultVoice, Format: "wav",
	}); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
