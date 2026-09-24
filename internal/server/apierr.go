package server

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
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
)

type envelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

func ok(c *gin.Context, data any) {
	c.JSON(200, envelope{Code: CodeOK, Data: data, Message: "ok"})
}

func fail(c *gin.Context, code int, message string) {
	c.JSON(200, envelope{Code: code, Data: nil, Message: message})
}

// failErr 按 error 类型映射业务码：NotFound->6、参数->2、凭证->4、其余->3。
func failErr(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(c, CodeNotFound, msg)
	case errors.Is(err, volcengine.ErrNoCred), errors.Is(err, volcengine.ErrAuth),
		errors.Is(err, qianwen.ErrNoCred), errors.Is(err, xiaomi.ErrNoCred):
		fail(c, CodeBadCredential, msg)
	case strings.Contains(msg, "缺少必填参数"), strings.Contains(msg, "未知工具"), strings.Contains(msg, "参数错误"):
		fail(c, CodeBadRequest, msg)
	default:
		fail(c, CodeTaskFailed, msg)
	}
}
