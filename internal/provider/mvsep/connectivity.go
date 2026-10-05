package mvsep

import (
	"context"
	"fmt"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeMVSep MVSep 连通性探测：GET /api/app/user 验证 token 并顺带取回
// 账户名；未配置时直接报未配置，不发请求。
func ProbeMVSep(cfg *config.Config) (string, bool) {
	if cfg.MVSep.APIToken == "" {
		return "未配置 MVSep API Token：请执行 voxbox config set mvsep.api_token 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL)
	u, err := c.User(ctx)
	if err != nil {
		return err.Error(), false
	}
	qs, qerr := c.Queue(ctx)
	if qerr == nil && qs.FreeMax > 0 {
		return fmt.Sprintf("连接成功（%s，今日免费分离余量 %d/%d）", u.Name, qs.FreeLeft, qs.FreeMax), true
	}
	return fmt.Sprintf("连接成功（%s）", u.Name), true
}
