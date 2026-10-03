package server

// sse.go SSE 流式响应公共设施:助手 chat、提示词 apply、录音笔记加工 refine、
// 字幕翻译 subtitles/translate 四个端点共用同一帧协议——预检错误走 JSON 包络,
// 流开始后逐帧写 "data: <json>\n\n",载荷按 JSON 键区分(delta/done/error/progress/result),
// 前端 sse.ts 按 data: 行解析。

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// sseWriter 设置 SSE 响应头并返回写帧函数:v JSON 序列化后写一帧并 Flush;
// 写失败(客户端断开)返回 false——调用方按既有约定继续喂(断连由请求上下文
// 取消让上游尽快退出),是否停止发事件由各端点的 err 分支决定。
func sseWriter(c *gin.Context) func(v any) bool {
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
