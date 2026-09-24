package zhipu

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 启动路径注册（同 key 重复注册报错）。凭证缺失仍注册，Run 时再报凭证错误。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(cfg, dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 凭证热加载覆盖重注册（与 qianwen/xiaomi 同款契约）：工具集与启动时一致，
// Replace 不会失败；进行中任务持有旧实例不受影响。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(cfg, dataDir) {
		reg.Replace(t)
	}
}

func allTools(cfg config.Config, dataDir string) []provider.Tool {
	return []provider.Tool{
		NewTTSTool(cfg.Zhipu.APIKey, dataDir),
		NewASRTool(cfg.Zhipu.APIKey, dataDir),
		NewVoiceCloneTool(cfg.Zhipu.APIKey), // 仅接口对接：CLI/任务通道可用，前端暂无页面
		NewVoiceDeleteTool(cfg.Zhipu.APIKey),
	}
}
