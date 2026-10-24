// Package music 音乐素材模块：搜索 / 音源配置管理 / 歌词 / 试听流代理的 HTTP 面。
// 长任务（下载落盘）不在本模块——走 provider/music 的 music_download 任务。
package music

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
)

// Module 音乐素材功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "music" }

func (mod) Register(m *module.Mount) {
	h := handlers{svc: m.Svc}
	g := m.API.Group("/music")
	g.GET("/sources", h.sources)
	g.POST("/sources/sync", h.syncSources)
	g.POST("/sources/import", h.importSources)
	g.GET("/search", h.search)
	g.GET("/lyric", h.lyric)
	g.GET("/stream", h.stream)
}

type handlers struct{ svc *service.Service }

// sources 当前音源配置视图 + 已导入标记。
func (h handlers) sources(c *gin.Context) {
	lines := h.svc.MusicSources()
	out := make([]gin.H, 0, len(lines))
	for _, l := range lines {
		out = append(out, gin.H{
			"id": l.ID, "name": l.Name, "enabled": l.Enabled,
			"levels": l.Levels, "search_api": l.SearchAPI,
		})
	}
	httpx.OK(c, gin.H{"items": out, "configured": h.svc.MusicConfigured(),
		"sources_url": h.svc.Config().Music.SourcesURL})
}

// syncSources 从配置的同步地址拉取音源配置。
func (h handlers) syncSources(c *gin.Context) {
	n, err := h.svc.MusicSyncSources(c.Request.Context())
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"lines": n})
}

// importSources 导入一份 music.json（请求体即文件原文）。
func (h handlers) importSources(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20))
	if err != nil || len(raw) == 0 {
		httpx.Fail(c, httpx.CodeTaskFailed, "请求体为空：请携带 music.json 原文")
		return
	}
	n, err := h.svc.MusicImportSources(raw)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"lines": n})
}

// search 单音源搜索：?source=kw&keyword=…&page=1。
func (h handlers) search(c *gin.Context) {
	source := c.Query("source")
	keyword := strings.TrimSpace(c.Query("keyword"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	tracks, err := h.svc.MusicSearch(c.Request.Context(), source, keyword, page)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": tracks, "page": page})
}

// lyric 歌词文本：?source&rid&name&artist（name/artist 供部分音源匹配）。
func (h handlers) lyric(c *gin.Context) {
	lrc, err := h.svc.MusicLyric(c.Request.Context(), c.Query("source"), c.Query("rid"),
		c.Query("name"), c.Query("artist"))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"lyric": lrc})
}

// stream 试听流代理：按 source/rid 解析直链后转发响应体。
// 直链带时效与防盗链请求头，浏览器直连不可靠，统一走服务端代理。
func (h handlers) stream(c *gin.Context) {
	resolved, err := h.svc.MusicResolve(c.Request.Context(),
		c.Query("source"), c.Query("rid"), c.Query("level"))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, resolved.URL, nil)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, "直链不合法: "+err.Error())
		return
	}
	for k, v := range resolved.PlaybackHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range resolved.Headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, "拉取音频流失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.Fail(c, httpx.CodeTaskFailed, "拉取音频流失败：HTTP "+strconv.Itoa(resp.StatusCode))
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		c.Header("Content-Length", cl)
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, resp.Body)
}
