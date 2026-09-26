package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
)

// ---- AI 助手（悬浮面板）：模型目录 + 流式对话 ----
// SSE 是流端点，与 artifacts/stream 同款例外：不套 JSON 包络。协议约定——预检错误
// （参数/凭证/模型）仍走统一包络（fail/failErr，响应为 application/json，前端按
// Content-Type 区分）；流开始后错误以 data: {"error":...} 事件下发，终止事件 {"done":true}。

const (
	assistantMaxMessages = 40   // 历史超出只保留最近 40 条上下文
	assistantMaxRunes    = 8000 // 单条消息 rune 上限
)

// assistantModels 模型目录（后端单一事实来源）：前端按 Enabled 禁用未配置平台的模型组。
func (s *Server) assistantModels(c *gin.Context) {
	ok(c, assistant.CatalogWith(s.svc.Config()))
}

type assistantChatReq struct {
	Provider string              `json:"provider"`
	Model    string              `json:"model"`
	Messages []assistant.Message `json:"messages"`
}

func (s *Server) assistantChat(c *gin.Context) {
	var req assistantChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	p := assistant.Provider(req.Provider)
	if !assistant.KnownProvider(p) {
		fail(c, CodeBadRequest, "参数错误：未知平台 "+req.Provider)
		return
	}
	// 消息整备：剥离 system（系统提示服务端注入，客户端不可伪造），空内容丢弃，
	// 单条封顶，超长历史截尾。
	msgs := make([]assistant.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		m.Content = strings.TrimSpace(m.Content)
		if m.Content == "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		if len([]rune(m.Content)) > assistantMaxRunes {
			fail(c, CodeBadRequest, "单条消息过长")
			return
		}
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != "user" {
		fail(c, CodeBadRequest, "参数错误：至少需要一条用户消息")
		return
	}
	if len(msgs) > assistantMaxMessages {
		msgs = msgs[len(msgs)-assistantMaxMessages:]
	}
	if !assistant.ModelAllowed(p, req.Model) {
		fail(c, CodeBadRequest, fmt.Sprintf("平台 %s 不支持模型 %s", assistant.Label(p), req.Model))
		return
	}
	cfg := s.svc.Config()
	if err := assistant.CheckCredential(cfg, p); err != nil {
		failErr(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	w := c.Writer
	writeEvent := func(v any) bool {
		raw, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return false
		}
		w.Flush()
		return true
	}
	err := assistant.Stream(c.Request.Context(), cfg, p, req.Model, msgs, func(delta string) {
		// 客户端断开后写事件失败不中断循环：请求上下文取消会让上游读取尽快退出
		_ = writeEvent(gin.H{"delta": delta})
	})
	if err != nil {
		// 客户端主动中止生成属正常交互：不算错误、不再发事件
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": assistantErrCode(err), "message": err.Error()}})
		}
		return
	}
	writeEvent(gin.H{"done": true})
}

// assistantErrCode 流内错误 → 业务码（与 failErr 同语义：凭证 4，其余任务失败 3）。
func assistantErrCode(err error) int {
	switch {
	case errors.Is(err, zhipu.ErrNoCred), errors.Is(err, qianwen.ErrNoCred), errors.Is(err, xiaomi.ErrNoCred):
		return CodeBadCredential
	}
	return CodeTaskFailed
}
