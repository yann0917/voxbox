// Package modules 内置功能模块清单。新增功能模块 = 新建 internal/modules/<x> 包
// 实现 module.Module + All() 加一行。
package modules

import (
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/modules/glossary"
	"github.com/yann0917/voxbox/internal/modules/prompts"
	"github.com/yann0917/voxbox/internal/modules/voicefavorites"
)

// All 全部功能模块（挂载顺序即清单顺序）。
func All() []module.Module {
	return []module.Module{
		prompts.Module(),
		glossary.Module(),
		voicefavorites.Module(),
	}
}
