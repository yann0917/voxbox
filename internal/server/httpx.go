package server

// 响应与请求身份设施已上收 internal/httpx（功能模块直接消费）；此处保留小写薄别名
// 承接存量 handler 文件，避免一次性改写全部领域文件——存量域逐步迁移后收缩。

import (
	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
)

// 业务码（与 CLI 退出码同语义）。
const (
	CodeOK            = httpx.CodeOK
	CodeBadRequest    = httpx.CodeBadRequest
	CodeTaskFailed    = httpx.CodeTaskFailed
	CodeBadCredential = httpx.CodeBadCredential
	CodeInternal      = httpx.CodeInternal
	CodeNotFound      = httpx.CodeNotFound
	CodeUnauthorized  = httpx.CodeUnauthorized
	CodeForbidden     = httpx.CodeForbidden
)

type envelope = httpx.Envelope

func ok(c *gin.Context, data any) {
	httpx.OK(c, data)
}

func fail(c *gin.Context, code int, message string) {
	httpx.Fail(c, code, message)
}

func failErr(c *gin.Context, err error) {
	httpx.FailErr(c, err)
}

const principalKey = httpx.PrincipalKey

type Principal = httpx.Principal

func principalFrom(c *gin.Context) *Principal {
	return httpx.PrincipalFrom(c)
}

// pageParams 列表端点通用分页参数。
func pageParams(c *gin.Context) (page, size int) {
	return httpx.PageParams(c)
}

// sseWriter SSE 帧协议写手（助手 chat / 提示词 apply / refine / 字幕翻译共用）。
func sseWriter(c *gin.Context) func(v any) bool {
	return httpx.SSEWriter(c)
}
