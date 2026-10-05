// Package httpx HTTP 表面的公共设施：统一 JSON 包络、业务码（与 CLI 退出码同语义）、
// 请求身份、分页参数与 SSE 帧协议。server 保留小写薄别名承接存量 handler，
// 功能模块（internal/modules/<x>）直接消费本包。
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/provider/openrouter"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
	"github.com/yann0917/voxbox/internal/store"
)

// 业务码与 CLI 退出码共用同一套语义。
const (
	CodeOK            = 0
	CodeBadRequest    = 2 // 参数错误、未知工具
	CodeTaskFailed    = 3 // 任务/上游失败
	CodeBadCredential = 4 // 凭证缺失或无效
	CodeInternal      = 5 // 内部错误
	CodeNotFound      = 6 // 资源不存在
	CodeUnauthorized  = 7 // 未登录 / 会话或 token 失效
	CodeForbidden     = 8 // 已登录但权限不足（非 admin 触碰 admin 端点）
)

// Envelope 统一 JSON 包络。
type Envelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

// OK 成功响应（HTTP 恒 200，业务码区分成败）。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Code: CodeOK, Data: data, Message: "ok"})
}

// Fail 失败响应。
func Fail(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, Envelope{Code: code, Data: nil, Message: message})
}

// FailErr 按 error 类型映射业务码：NotFound->6、参数->2、凭证->4、其余->3。
func FailErr(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, store.ErrNotFound):
		Fail(c, CodeNotFound, msg)
	case errors.Is(err, volcengine.ErrNoCred), errors.Is(err, volcengine.ErrAuth),
		errors.Is(err, openrouter.ErrNoCred), errors.Is(err, qianwen.ErrNoCred),
		errors.Is(err, xiaomi.ErrNoCred), errors.Is(err, zhipu.ErrNoCred):
		Fail(c, CodeBadCredential, msg)
	case strings.Contains(msg, "缺少必填参数"), strings.Contains(msg, "未知工具"), strings.Contains(msg, "参数错误"):
		Fail(c, CodeBadRequest, msg)
	default:
		Fail(c, CodeTaskFailed, msg)
	}
}

// PrincipalKey gin context 中请求身份的存放键（requireAuth 中间件写入）。
const PrincipalKey = "voxbox.principal"

// Principal 当前请求身份：requireAuth 从 Cookie 会话或 Bearer token 解析后注入 gin context。
type Principal struct {
	ID                 string
	Username           string
	Role               string
	MustChangePassword bool
}

// IsAdmin admin 角色判定。
func (p *Principal) IsAdmin() bool { return p.Role == "admin" }

// PrincipalFrom 取当前请求身份（未登录路径返回 nil）。
func PrincipalFrom(c *gin.Context) *Principal {
	p, _ := c.Get(PrincipalKey)
	pp, _ := p.(*Principal)
	return pp
}

// PageParams 列表端点通用分页参数（page 1 起始，size 缺省 20），与 /api/tasks 同口径。
func PageParams(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	return page, size
}

// SSEWriter 设置 SSE 响应头并返回写帧函数：v JSON 序列化后写一帧并 Flush；
// 写失败（客户端断开）返回 false——调用方按既有约定继续喂（断连由请求上下文
// 取消让上游尽快退出），是否停止发事件由各端点的 err 分支决定。
func SSEWriter(c *gin.Context) func(v any) bool {
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	w := c.Writer
	return func(v any) bool {
		raw, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: " + string(raw) + "\n\n")); err != nil {
			return false
		}
		w.Flush()
		return true
	}
}

// StreamErrCode 流内错误 → 业务码（与 FailErr 同语义：凭证 4，其余任务失败 3）。
func StreamErrCode(err error) int {
	switch {
	case errors.Is(err, zhipu.ErrNoCred), errors.Is(err, qianwen.ErrNoCred), errors.Is(err, xiaomi.ErrNoCred):
		return CodeBadCredential
	}
	return CodeTaskFailed
}
