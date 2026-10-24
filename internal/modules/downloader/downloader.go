// Package downloader 链接下载的预览模块：probe 解析链接元数据（标题/时长/合集数/字幕语言），
// 供素材页链接输入的确认 UI。下载本身是 provider/downloader 的 download 任务。
package downloader

import (
	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
)

// Module 链接下载预览功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "downloader" }

func (mod) Register(m *module.Mount) {
	h := handlers{svc: m.Svc}
	g := m.API.Group("/downloader")
	g.POST("/probe", h.probe)
}

type handlers struct{ svc *service.Service }

type probeReq struct {
	URL string `json:"url"`
}

// probe 解析链接元数据。抖音链接回 backend=douyin（无元数据预览，前端直接进入选项）。
func (h handlers) probe(c *gin.Context) {
	var req probeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.URL == "" {
		httpx.Fail(c, httpx.CodeTaskFailed, "请携带要解析的链接")
		return
	}
	meta, err := h.svc.DownloaderProbe(c.Request.Context(), req.URL)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, meta)
}
