package music

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 将音乐工具注册进 registry（启动路径）。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 以最新配置覆盖重注册（Web 设置保存/配置文件热加载后调用）。
// 工具只依赖 dataDir（音源配置每次 Run 现读），重注册仅保持装配惯例一致。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(dataDir) {
		reg.Replace(t)
	}
}

func allTools(dataDir string) []provider.Tool {
	return []provider.Tool{
		NewTool(dataDir),
	}
}
