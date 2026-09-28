package server

// MVSep（mvsep.com 音频源分离）：算法/账户/历史查询路由。
// 任务提交复用 /api/tasks（provider=mvsep, tool=separate），不设独立提交端点。

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
)

// listMVSepAlgorithms 算法列表：进程内缓存 1 小时（上游限频 60/分钟），
// ?refresh=1 绕过缓存强拉。
func (s *Server) listMVSepAlgorithms(c *gin.Context) {
	algos, err := s.svc.MVSepAlgorithms(c.Request.Context(), c.Query("refresh") == "1")
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, algos)
}

// mvsepStatus 账户信息 + 站点队列/每日免费额度（分离页头部展示）。
func (s *Server) mvsepStatus(c *gin.Context) {
	u, qs, err := s.svc.MVSepUserQueue(c.Request.Context())
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"user": u, "queue": qs})
}

// mvsepHistory MVSep 云端分离历史（?start=&limit=，默认 0/10，上游上限 20）。
func (s *Server) mvsepHistory(c *gin.Context) {
	start, _ := strconv.Atoi(c.DefaultQuery("start", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	items, err := s.svc.MVSepHistory(c.Request.Context(), start, limit)
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

// mvsepSeparationGet 按 hash 直查云端任务状态/结果（免鉴权上游接口的只读代理）：
// 供分离页从云端历史补拉结果（本服务任务超时/清理后仍可取回产物直链）。
func (s *Server) mvsepSeparationGet(c *gin.Context) {
	hash := strings.TrimSpace(c.Query("hash"))
	if hash == "" {
		fail(c, CodeBadRequest, "参数错误：hash 必填")
		return
	}
	cfg := s.svc.Config()
	status, res, err := mvsep.New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL).Get(c.Request.Context(), hash)
	if err != nil {
		// failed/not_found 属任务终态而非传输错误：以状态+错误返回 200，前端按终态展示
		ok(c, gin.H{"status": status, "error": err.Error()})
		return
	}
	ok(c, gin.H{"status": status, "result": res})
}
