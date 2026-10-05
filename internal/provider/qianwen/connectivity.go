package qianwen

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeQianwen 千问连通性探测：极短文本合成（消耗少量额度，同小米/智谱模式）。
func ProbeQianwen(cfg *config.Config) (string, bool) {
	key := cfg.Qianwen.APIKey
	if key == "" {
		return "未配置千问 API Key：请执行 voxbox config set qianwen.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := NewTTSClient(key, BaseURL)
	if _, err := client.Synthesize(ctx, TTSReq{
		Model: "qwen3-tts-flash", Text: "测", Voice: DefaultVoice,
	}); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
