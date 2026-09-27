package server

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/localmodel"
)

// 本地语音模型管理端点：目录+状态、开始/暂停下载、删除、打开目录。
// 全部本地操作无凭证,不触碰 ErrNoCred 映射;写入操作沿用 requireAuth(单用户工具,不做 admin 门)。

func (s *Server) listModels(c *gin.Context) {
	all := s.svc.LocalModels().List()
	items := make([]localmodel.ModelView, 0, len(all))
	for _, v := range all {
		// 平台过滤(spec §3.1):无当前平台资产的引擎条目在该平台不展示,
		// 否则用户会看到条目但下载必败(如 linux/arm64 无 audiocpp 资产)。
		// 只过滤 HTTP 视图;Manager.List() 保持完整目录供内部消费者使用。
		if v.Entry.Kind == "engine" {
			if _, ok := v.Entry.ArchiveFor(runtime.GOOS, runtime.GOARCH); !ok {
				continue
			}
		}
		items = append(items, v)
	}
	ok(c, gin.H{"items": items})
}

// modelFail 统一错误映射:未知模型 → NotFound,其余 → BadRequest(文案已可定位)。
func modelFail(c *gin.Context, err error) {
	if errors.Is(err, localmodel.ErrUnknownModel) {
		fail(c, CodeNotFound, err.Error())
		return
	}
	fail(c, CodeBadRequest, err.Error())
}

func (s *Server) startModelDownload(c *gin.Context) {
	if err := s.svc.LocalModels().Start(c.Param("id")); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// stopModelDownload 先验 id 存在性:Manager.Stop 只比对活动下载 id,未知 id 会被误报
// 「未在下载」(BadRequest),这里用 View 前置拦截,保证未知 id 统一 NotFound 语义。
func (s *Server) stopModelDownload(c *gin.Context) {
	id := c.Param("id")
	if _, found := s.svc.LocalModels().View(id); !found {
		fail(c, CodeNotFound, fmt.Sprintf("%s: %s", localmodel.ErrUnknownModel, id))
		return
	}
	if err := s.svc.LocalModels().Stop(id); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (s *Server) deleteModel(c *gin.Context) {
	if err := s.svc.LocalModels().Delete(c.Param("id")); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// openModelsDir 打开 <dataDir>/models(桌面形态前端显隐;web 形态调用也不越权,仅弹本机文件管理器)。
func (s *Server) openModelsDir(c *gin.Context) {
	if err := localmodel.OpenDir(s.svc.LocalModels().Dir()); err != nil {
		fail(c, CodeTaskFailed, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}
