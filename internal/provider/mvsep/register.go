package mvsep

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 将 MVSep 工具注册进 registry（启动路径）。token 未配置时仍注册
// （Run 时再报凭证错误，与火山工具同一语义）。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(cfg, dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 以最新凭证覆盖重注册（Web 设置保存/配置文件热加载后调用，无需重启）。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(cfg, dataDir) {
		reg.Replace(t)
	}
}

func allTools(cfg config.Config, dataDir string) []provider.Tool {
	return []provider.Tool{
		NewSeparateTool(cfg.MVSep.APIToken, cfg.MVSep.BaseURL, dataDir),
	}
}
