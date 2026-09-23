// 格式工厂站点可达性探测代理：/api/gsgc/health。同源代理而非前端直连，
// 前端无需知道站点地址，也绕开跨域。探测动作即协议第一步（申请预签名上传
// 地址，匿名 GET，无副作用），成功即代表站点接口可用。
package server

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
)

// gsgcHealth GET /api/gsgc/health：探测格式工厂站点（4s 超时）。
// 不可达时 ok:true + reachable:false（探测动作本身成功，业务语义由字段表达）。
func (s *Server) gsgcHealth(c *gin.Context) {
	client := gsgc.New()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	_, _, err := client.FetchUploadURL(ctx, "probe.mp3")
	if err != nil {
		ok(c, gin.H{
			"reachable": false,
			"base_url":  client.BaseURL(),
			"error":     err.Error(),
		})
		return
	}
	ok(c, gin.H{
		"reachable": true,
		"base_url":  client.BaseURL(),
	})
}
