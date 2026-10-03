package server

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// 提示词库：内置条目与用户自定义条目的列表、自定义条目 CRUD、AI 写作流式端点。
// 条目按登录用户隔离（admin 也不例外）；apply 的 SSE 协议与助手 chat 完全一致——
// 预检错误走 JSON 包络，流开始后错误走 data: {"error":...} 事件，终止 {"done":true}。

func (s *Server) listPrompts(c *gin.Context) {
	items, err := s.svc.ListPrompts(principalFrom(c).ID)
	if err != nil {
		fail(c, CodeTaskFailed, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

type promptReq struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Kind        string `json:"kind"`
}

func (s *Server) createPrompt(c *gin.Context) {
	var req promptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	p, err := s.svc.CreatePrompt(principalFrom(c).ID, service.PromptInput{
		Name: req.Name, Category: req.Category,
		Description: req.Description, Content: req.Content, Kind: req.Kind,
	})
	if err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, p)
}

// promptID 路径参数里的条目 id。
func promptID(c *gin.Context) (uint, bool) {
	v, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

func (s *Server) updatePrompt(c *gin.Context) {
	id, okID := promptID(c)
	if !okID {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	var req promptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	p, err := s.svc.UpdatePrompt(principalFrom(c).ID, id, service.PromptInput{
		Name: req.Name, Category: req.Category,
		Description: req.Description, Content: req.Content, Kind: req.Kind,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, CodeNotFound, "提示词不存在")
		} else {
			fail(c, CodeBadRequest, err.Error())
		}
		return
	}
	ok(c, p)
}

func (s *Server) deletePrompt(c *gin.Context) {
	id, okID := promptID(c)
	if !okID {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.DeletePrompt(principalFrom(c).ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, CodeNotFound, "提示词不存在")
		} else {
			fail(c, CodeTaskFailed, err.Error())
		}
		return
	}
	ok(c, gin.H{"ok": true})
}

// applyPrompt AI 写作（生成/润色）：按条目注入系统提示，流式回传正文。
func (s *Server) applyPrompt(c *gin.Context) {
	var req service.ApplyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	resolved, err := s.svc.ResolveApply(principalFrom(c).ID, req)
	if err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}

	writeEvent := sseWriter(c)
	err = s.svc.StreamApply(c.Request.Context(), resolved, func(delta string) {
		// 客户端断开后写事件失败不中断循环：请求上下文取消会让上游读取尽快退出
		_ = writeEvent(gin.H{"delta": delta})
	})
	if err != nil {
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": assistantErrCode(err), "message": err.Error()}})
		}
		return
	}
	writeEvent(gin.H{"done": true})
}
