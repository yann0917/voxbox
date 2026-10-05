// Package glossary 翻译术语表模块：字幕 AI 翻译的 AI 自动沉淀与人工维护（设置页）
// 共用一表。业务逻辑在 service（ListGlossary 等），本包只承担 HTTP 面。
// 人工新增/改词撞唯一约束回显明确错误，不静默覆盖。
package glossary

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// Module 术语表功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "glossary" }

func (mod) Register(m *module.Mount) {
	h := handlers{svc: m.Svc}
	g := m.API.Group("/glossary")
	g.GET("", h.list)
	g.POST("", h.create)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
}

type handlers struct{ svc *service.Service }

func (h handlers) list(c *gin.Context) {
	page, size := httpx.PageParams(c)
	items, total, err := h.svc.ListGlossary(c.Query("lang"), size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total})
}

type glossaryReq struct {
	TargetLanguage string `json:"target_language"`
	Src            string `json:"src"`
	Dst            string `json:"dst"`
}

func (h handlers) create(c *gin.Context) {
	var req glossaryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	row, err := h.svc.CreateGlossaryTerm(req.TargetLanguage, req.Src, req.Dst)
	if err != nil {
		h.failTermErr(c, err)
		return
	}
	httpx.OK(c, row)
}

// termID 路径参数里的词条 id。
func termID(c *gin.Context) (uint, bool) {
	v, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

func (h handlers) update(c *gin.Context) {
	id, okID := termID(c)
	if !okID {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	var req glossaryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	row, err := h.svc.UpdateGlossaryTerm(id, req.TargetLanguage, req.Src, req.Dst)
	if err != nil {
		h.failTermErr(c, err)
		return
	}
	httpx.OK(c, row)
}

func (h handlers) delete(c *gin.Context) {
	id, okID := termID(c)
	if !okID {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	if err := h.svc.DeleteGlossaryTerm(id); err != nil {
		h.failTermErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// failTermErr 术语表写操作的错误映射：不存在回 NotFound，撞唯一约束回参数错误
// 并给出明确指引，其余透出。
func (h handlers) failTermErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Fail(c, httpx.CodeNotFound, "词条不存在")
	case errors.Is(err, store.ErrGlossaryExists):
		httpx.Fail(c, httpx.CodeBadRequest, "同语言下已有这条原文,请直接编辑那条词条")
	default:
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
	}
}
