package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// WithStatic 在 API handler 外挂 SPA 静态资源（embed 的 webdist/ 产物）。
// /api 前缀交给 API handler；其余路径交给文件服务器，
// 文件不存在时改写为 / 回退 index.html（前端 BrowserRouter 路由需要）。
// index.html（含路由回退）必须 Cache-Control: no-cache——哈希命名的资产可长缓存，
// 但入口 HTML 一旦被浏览器启发式缓存，发新版后用户会停在旧页面（资源哈希都不变地命中旧缓存）。
func WithStatic(api http.Handler, dist embed.FS) http.Handler {
	sub, err := fs.Sub(dist, "webdist")
	if err != nil {
		return api
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			api.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		if r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
