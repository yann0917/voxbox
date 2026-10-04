package server

// 术语表(字幕 AI 翻译):AI 自动沉淀与人工维护(设置页)共用一表。人工新增/改词
// 撞唯一约束回显明确错误,不静默覆盖。

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/store"
)

func (s *Server) listGlossary(c *gin.Context) {
	items, err := s.svc.ListGlossary(c.Query("lang"))
	if err != nil {
		fail(c, CodeTaskFailed, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

type glossaryReq struct {
	TargetLanguage string `json:"target_language"`
	Src            string `json:"src"`
	Dst            string `json:"dst"`
}

func (s *Server) createGlossaryTerm(c *gin.Context) {
	var req glossaryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	row, err := s.svc.CreateGlossaryTerm(req.TargetLanguage, req.Src, req.Dst)
	if err != nil {
		failGlossaryErr(c, err)
		return
	}
	ok(c, row)
}

func glossaryID(c *gin.Context) (uint, bool) {
	v, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

func (s *Server) updateGlossaryTerm(c *gin.Context) {
	id, okID := glossaryID(c)
	if !okID {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	var req glossaryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	row, err := s.svc.UpdateGlossaryTerm(id, req.TargetLanguage, req.Src, req.Dst)
	if err != nil {
		failGlossaryErr(c, err)
		return
	}
	ok(c, row)
}

func (s *Server) deleteGlossaryTerm(c *gin.Context) {
	id, okID := glossaryID(c)
	if !okID {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.DeleteGlossaryTerm(id); err != nil {
		failGlossaryErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// failGlossaryErr 术语表写操作的错误映射:参数类回 CodeBadRequest,不存在回
// CodeNotFound,其余透出。
func failGlossaryErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(c, CodeNotFound, "词条不存在")
	case errors.Is(err, store.ErrGlossaryExists):
		fail(c, CodeBadRequest, "同语言下已有这条原文,请直接编辑那条词条")
	default:
		fail(c, CodeBadRequest, err.Error())
	}
}
