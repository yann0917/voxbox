package minimax

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeMinimax MiniMax 连通性探测：查询可用音色（只读接口，零合成消耗，异于
// 千问/小米/智谱的 1 字合成探活——MiniMax 音色接口即可验证鉴权有效性）。
func ProbeMinimax(cfg *config.Config) (string, bool) {
	key := cfg.Minimax.APIKey
	if key == "" {
		return "未配置 MiniMax API Key：请执行 voxbox config set minimax.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := NewVoiceClient(key, BaseURL).List(ctx); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
