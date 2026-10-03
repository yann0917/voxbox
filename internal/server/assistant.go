package server

import (
	"errors"
	"fmt"
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
	// assistantMaxContextRunes context（如转写全文）rune 上限：与前端问答上下文的
	// 转写截断值一致（web/src/pages/quicknote/model.ts CONTEXT_TRANSCRIPT_MAX）。
	assistantMaxContextRunes = 24000
)

// assistantModels 模型目录（后端单一事实来源）：前端按 Enabled 禁用未配置平台的模型组。
func (s *Server) assistantModels(c *gin.Context) {
	ok(c, assistant.CatalogWith(s.svc.Config()))
}

// assistantChatStream 流式底层测试缝：生产即 assistant.StreamCompose（system 参数化
// 导出入口，system 由 assistant.ChatSystem 组装——不带 context 时即默认助手提示）；
// server 包测试替换为假实现离线断言下发内容（refineStream 同款）。
var assistantChatStream = assistant.StreamCompose

type assistantChatReq struct {
	Provider string              `json:"provider"`
	Model    string              `json:"model"`
	Messages []assistant.Message `json:"messages"`
	// Context 额外上下文（如单条录音的转写全文）：非空时以空行追加在默认系统提示之后。
	Context string `json:"context,omitempty"`
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
	// context 服务端封顶：原样透传给 ChatSystem 拼接，不设限则绕开单条消息的
	// rune 上限（任意登录用户可塞数 MB），与前端截断值一致。
	if len([]rune(req.Context)) > assistantMaxContextRunes {
		fail(c, CodeBadRequest, "上下文过长")
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

	writeEvent := sseWriter(c)
	err := assistantChatStream(c.Request.Context(), cfg, p, req.Model, assistant.ChatSystem(req.Context), msgs, func(delta string) {
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
