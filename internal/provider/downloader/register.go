package downloader

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 将下载工具注册进 registry（启动路径）。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(cfg, dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 以最新配置覆盖重注册（路径类配置变更后调用）。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(cfg, dataDir) {
		reg.Replace(t)
	}
}

func allTools(cfg config.Config, dataDir string) []provider.Tool {
	return []provider.Tool{
		NewTool(cfg.Downloader.YtdlpPath, cfg.Downloader.DouyinCommand, cfg.Downloader.DouyinConfig, dataDir),
	}
}
