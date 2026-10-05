// Package module 功能模块骨架：功能域（路由面/数据/交互）自描述挂载。
// 新增功能模块 = 新建 internal/modules/<x> 包实现 Module + internal/modules 清单加一行，
// 不再触碰 server/routes.go。模块间不互相 import，跨域交互走 service 或事件。
package module

import (
	"github.com/gin-gonic/gin"

	"github.com/yann0917/voxbox/internal/service"
)

// Mount 装配点：server 把已挂好鉴权的路由组与服务实例交给模块，模块在 Register 内
// 挂自己的路由。Root 供健康检查级公开端点；API 为 requireAuth 组；Admin 为
// requireAuth+requireAdmin 组（未登录 401、非管理员 403，与存量 admin 端点同语义）。
// 后续有模块需要更多运行期依赖（如 Hub、数据目录）时按需加字段，不预造。
type Mount struct {
	Root  gin.IRouter
	API   gin.IRouter
	Admin gin.IRouter
	Svc   *service.Service
}

// Module 功能模块。
type Module interface {
	ID() string
	Register(m *Mount)
}
