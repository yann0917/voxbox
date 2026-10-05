// Package voicefavorites 收藏音色功能模块：弹框式音色选择器的收藏数据源。
// 按平台分类、按人隔离；收藏幂等（重复收藏返回既有行），移除不存在回 404 语义。
// 业务校验在 service，本包只承担 HTTP 面（与 glossary 同款骨架）。
package voicefavorites

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// favVoiceMaxSize 星标态整表拉取的单页上限（单平台音色数 ≤ 几百，一次拉全够用）。
const favVoiceMaxSize = 500

// Module 收藏音色功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "voice-favorites" }

func (mod) Register(m *module.Mount) {
	h := handlers{svc: m.Svc}
	g := m.API.Group("/voice-favorites")
	g.GET("", h.list)
	g.GET("/ids", h.ids)
	g.POST("", h.create)
	g.DELETE("/:id", h.delete)
}

type handlers struct{ svc *service.Service }

// list 收藏列表：?provider= 可选平台筛选，分页参数同全局口径；size 上限 500
// （弹框星标态一次拉全单平台收藏）。
func (h handlers) list(c *gin.Context) {
	page, size := httpx.PageParams(c)
	if size > favVoiceMaxSize {
		size = favVoiceMaxSize
	}
	items, total, err := h.svc.ListFavoriteVoices(
		httpx.PrincipalFrom(c).ID, c.Query("provider"), size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total})
}

// ids 某平台已收藏音色的引用对（轻量：星标态渲染与取消收藏定位行，无分页语义；
// 收藏量上限为单平台音色数，一次拉全）。
func (h handlers) ids(c *gin.Context) {
	items, _, err := h.svc.ListFavoriteVoices(
		httpx.PrincipalFrom(c).ID, c.Query("provider"), favVoiceMaxSize, 0)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	refs := make([]gin.H, 0, len(items))
	for _, it := range items {
		refs = append(refs, gin.H{"id": it.ID, "voice_id": it.VoiceID})
	}
	httpx.OK(c, gin.H{"refs": refs})
}

type favVoiceReq struct {
	Provider string `json:"provider"`
	VoiceID  string `json:"voice_id"`
	Name     string `json:"name"`
	Label    string `json:"label"`
	Lang     string `json:"lang"`
}

func (h handlers) create(c *gin.Context) {
	var req favVoiceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	row, isNew, err := h.svc.AddFavoriteVoice(httpx.PrincipalFrom(c).ID, service.FavoriteVoiceInput{
		Provider: req.Provider, VoiceID: req.VoiceID,
		Name: req.Name, Label: req.Label, Lang: req.Lang,
	})
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}
	httpx.OK(c, gin.H{"item": row, "is_new": isNew})
}

func (h handlers) delete(c *gin.Context) {
	v, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || v == 0 {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误")
		return
	}
	if err := h.svc.RemoveFavoriteVoice(httpx.PrincipalFrom(c).ID, uint(v)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Fail(c, httpx.CodeNotFound, "收藏不存在或已移除")
			return
		}
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, nil)
}
