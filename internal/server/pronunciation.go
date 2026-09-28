package server

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/pronunciation"
)

// 发音词典（TTS 合成前文本预处理）：读与试听登录即可，增删改沿设置页口径仅 admin。
// 存储为 <dataDir>/pronunciation.json（internal/pronunciation 磁盘即真相），无 DB 表。

func (s *Server) listPronunciation(c *gin.Context) {
	ok(c, gin.H{"entries": pronunciation.Default().List()})
}

type pronunciationReq struct {
	Term        string `json:"term"`
	Replacement string `json:"replacement"`
	Language    string `json:"language"` // "*" 或二字母码；空串归一为 "*"
	Enabled     *bool  `json:"enabled"`  // 缺省 true
}

func (s *Server) addPronunciation(c *gin.Context) {
	var req pronunciationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	e, err := pronunciation.Default().Add(req.Term, req.Replacement, req.Language, enabled)
	if err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, e)
}

func (s *Server) updatePronunciation(c *gin.Context) {
	var req pronunciationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	e, err := pronunciation.Default().Update(c.Param("id"), req.Term, req.Replacement, req.Language, enabled)
	if err != nil {
		if errors.Is(err, pronunciation.ErrNotFound) {
			fail(c, CodeNotFound, err.Error())
		} else {
			fail(c, CodeBadRequest, err.Error())
		}
		return
	}
	ok(c, e)
}

func (s *Server) deletePronunciation(c *gin.Context) {
	if err := pronunciation.Default().Delete(c.Param("id")); err != nil {
		fail(c, CodeNotFound, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}

// testPronunciation 试听干跑：按请求语言应用词典，返回替换后文本与命中统计。
// 只做文本替换，不发起合成。
func (s *Server) testPronunciation(c *gin.Context) {
	var req struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Text == "" {
		fail(c, CodeBadRequest, "参数错误：text 必填")
		return
	}
	before := req.Text
	after := pronunciation.Apply(before, req.Language)
	ok(c, gin.H{
		"text":     after,
		"replaced": after != before,
	})
}
