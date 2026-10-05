// Package prompts 提示词库模块：内置条目与用户自定义条目的列表、自定义条目 CRUD、
// AI 写作流式端点。条目按登录用户隔离（admin 也不例外）；业务逻辑在 service
// （ListPrompts 等），本包只承担 HTTP 面。
package prompts

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// Module 提示词库功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "prompts" }

func (mod) Register(m *module.Mount) {
	h := handlers{svc: m.Svc}
	g := m.API.Group("/prompts")
	g.GET("", h.list)
	g.POST("", h.create)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/apply", h.apply)
}

type handlers struct{ svc *service.Service }

func (h handlers) list(c *gin.Context) {
	items, err := h.svc.ListPrompts(httpx.PrincipalFrom(c).ID)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type promptReq struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Kind        string `json:"kind"`
}

func (h handlers) create(c *gin.Context) {
	var req promptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	p, err := h.svc.CreatePrompt(httpx.PrincipalFrom(c).ID, service.PromptInput{
		Name: req.Name, Category: req.Category,
		Description: req.Description, Content: req.Content, Kind: req.Kind,
	})
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}
	httpx.OK(c, p)
}

// promptID 路径参数里的条目 id。
func promptID(c *gin.Context) (uint, bool) {
	v, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

func (h handlers) update(c *gin.Context) {
	id, okID := promptID(c)
	if !okID {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	var req promptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	p, err := h.svc.UpdatePrompt(httpx.PrincipalFrom(c).ID, id, service.PromptInput{
		Name: req.Name, Category: req.Category,
		Description: req.Description, Content: req.Content, Kind: req.Kind,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Fail(c, httpx.CodeNotFound, "提示词不存在")
		} else {
			httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		}
		return
	}
	httpx.OK(c, p)
}

func (h handlers) delete(c *gin.Context) {
	id, okID := promptID(c)
	if !okID {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	if err := h.svc.DeletePrompt(httpx.PrincipalFrom(c).ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Fail(c, httpx.CodeNotFound, "提示词不存在")
		} else {
			httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		}
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// apply AI 写作（生成/润色）：按条目注入系统提示，流式回传正文。
// SSE 协议与助手 chat 完全一致：预检错误走 JSON 包络，流开始后错误走
// data: {"error":...} 事件，终止 {"done":true}。
func (h handlers) apply(c *gin.Context) {
	var req service.ApplyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	resolved, err := h.svc.ResolveApply(httpx.PrincipalFrom(c).ID, req)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}

	writeEvent := httpx.SSEWriter(c)
	err = h.svc.StreamApply(c.Request.Context(), resolved, func(delta string) {
		// 客户端断开后写事件失败不中断循环：请求上下文取消会让上游读取尽快退出
		_ = writeEvent(gin.H{"delta": delta})
	})
	if err != nil {
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": httpx.StreamErrCode(err), "message": err.Error()}})
		}
		return
	}
	writeEvent(gin.H{"done": true})
}
