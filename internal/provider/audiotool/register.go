// RegisterAll 将音频剪辑工具集注册进 registry。本地能力无凭证，注册一次即可，
// 无需参与设置热更新重注册（volcengine/mvsep 才需要）。
package audiotool

import (
	"errors"

	"github.com/yann0917/voxbox/internal/provider"
)

// AllTools 返回全部工具实例（供注册与测试清点）。
func AllTools(dataDir string) []provider.Tool {
	base := baseTool{outDir: dataDir}
	return []provider.Tool{
		&trimTool{base},
		&mergeTool{base},
		&pitchTool{base},
		&analyzeTool{base},
		&eqTool{base},
		&volumeTool{base},
		&fadeTool{base},
		&reverseTool{base},
		&mixTool{base},
		&hookTool{base},
		&clipTool{base},
		&duckTool{base},
	}
}

// RegisterAll 启动路径注册；同一工具重复注册报错（Registry 防呆契约）。
func RegisterAll(reg *provider.Registry, dataDir string) error {
	var errs []error
	for _, t := range AllTools(dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
