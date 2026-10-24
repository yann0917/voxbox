// Package music 音乐素材模块：搜索（单源/聚合）/ 音源配置管理 / 歌词 / 试听流代理 /
// 排行榜 / 歌单广场 / MV / 收听历史的 HTTP 面。
// 长任务（歌曲/MV 下载落盘）不在本模块——走 provider/music 的任务工具。
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
	"github.com/yann0917/voxbox/internal/store"
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
	g.GET("/search/all", h.searchAll)
	g.GET("/lyric", h.lyric)
	g.GET("/stream", h.stream)
	// 排行榜
	g.GET("/ranks/boards", h.rankBoards)
	g.GET("/ranks/songs", h.rankSongs)
	// 歌单广场
	g.GET("/playlists/lines", h.playlistLines)
	g.GET("/playlists", h.playlists)
	g.GET("/playlists/detail", h.playlistDetail)
	// MV
	g.GET("/mv/hot", h.mvHot)
	g.GET("/mv/search", h.mvSearch)
	g.GET("/mv/play", h.mvPlay)
	g.GET("/mv/stream", h.mvStream)
	// 收听历史
	g.POST("/history", h.addHistory)
	g.GET("/history", h.listHistory)
	g.DELETE("/history", h.deleteHistory)
}

type handlers struct{ svc *service.Service }

func pageParam(c *gin.Context) int {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	return page
}

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
	tracks, err := h.svc.MusicSearch(c.Request.Context(), source, keyword, pageParam(c))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": tracks, "page": pageParam(c)})
}

// searchAll 全音源并发聚合搜索：结果合并、单源失败按 source 归档。
func (h handlers) searchAll(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))
	items, errs, err := h.svc.MusicSearchAll(c.Request.Context(), keyword, pageParam(c))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "page": pageParam(c), "errors": errs})
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
	proxyGet(c, resolved.URL, resolved.PlaybackHeaders, resolved.Headers)
}

// proxyGet 代理一次 GET：上游头透传（Content-Type/Length/Range 相关），Body 流式回写。
func proxyGet(c *gin.Context, url string, headerSets ...map[string]string) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, "直链不合法: "+err.Error())
		return
	}
	for _, set := range headerSets {
		for k, v := range set {
			if req.Header.Get(k) == "" {
				req.Header.Set(k, v)
			}
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, "拉取音频流失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		httpx.Fail(c, httpx.CodeTaskFailed, "拉取音频流失败：HTTP "+strconv.Itoa(resp.StatusCode))
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			c.Header(k, v)
		}
	}
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" && resp.StatusCode == http.StatusPartialContent {
		c.Status(http.StatusPartialContent)
	} else {
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusOK)
	}
	_, _ = io.Copy(c.Writer, resp.Body)
}

// ---- 排行榜 ----

// rankBoards 某音源的内置榜单清单：?source=kw。
func (h handlers) rankBoards(c *gin.Context) {
	source := c.Query("source")
	boards := h.svc.MusicRankBoards(source)
	httpx.OK(c, gin.H{"sources": h.svc.MusicRankSources(), "items": boards})
}

// rankSongs 榜单歌曲页：?source&board&page。
func (h handlers) rankSongs(c *gin.Context) {
	board, err := h.svc.MusicRankSongs(c.Request.Context(), c.Query("source"), c.Query("board"), pageParam(c))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, board)
}

// ---- 歌单广场 ----

// playlistLines 歌单广场线路与二级分类表。
func (h handlers) playlistLines(c *gin.Context) {
	httpx.OK(c, gin.H{"items": h.svc.MusicPlaylistLines()})
}

// playlists 歌单列表页：?source&tag&page。
func (h handlers) playlists(c *gin.Context) {
	page, err := h.svc.MusicPlaylists(c.Request.Context(), c.Query("source"), c.Query("tag"), pageParam(c))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, page)
}

// playlistDetail 歌单详情（曲目转 Track）：?source&id。
func (h handlers) playlistDetail(c *gin.Context) {
	d, err := h.svc.MusicPlaylistDetail(c.Request.Context(), c.Query("source"), c.Query("id"))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, d)
}

// ---- MV ----

// mvHot 热门 MV：?page&size。
func (h handlers) mvHot(c *gin.Context) {
	size, _ := strconv.Atoi(c.DefaultQuery("size", "30"))
	items, err := h.svc.MusicMvHot(c.Request.Context(), pageParam(c), size)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "page": pageParam(c)})
}

// mvSearch 搜索 MV：?source&keyword&page。
func (h handlers) mvSearch(c *gin.Context) {
	items, err := h.svc.MusicMvSearch(c.Request.Context(), c.Query("source"), strings.TrimSpace(c.Query("keyword")), pageParam(c))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "page": pageParam(c)})
}

// mvPlay 某 MV 的可播画质列表：?source&vid。
func (h handlers) mvPlay(c *gin.Context) {
	qs, err := h.svc.MusicMvPlayback(c.Request.Context(), c.Query("source"), c.Query("vid"))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": qs})
}

// mvStream MV 流代理（Range 透传，<video> 可拖进度）：?source&vid&quality。
func (h handlers) mvStream(c *gin.Context) {
	resp, err := h.svc.MusicMvStream(c.Request.Context(), c.Query("source"), c.Query("vid"),
		c.Query("quality"), c.GetHeader("Range"))
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			c.Header(k, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

// ---- 收听历史 ----

type historyReq struct {
	Source   string `json:"source"`
	Rid      string `json:"rid"`
	Name     string `json:"name"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Pic      string `json:"pic"`
	Duration int64  `json:"duration"`
}

// addHistory 记一次收听（fire-and-forget：失败不报错干扰播放）。
func (h handlers) addHistory(c *gin.Context) {
	var req historyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, "请求体不合法")
		return
	}
	err := h.svc.AddMusicHistory(httpx.PrincipalFrom(c).ID, store.MusicPlayHistory{
		Source: req.Source, Rid: req.Rid, Name: req.Name, Artist: req.Artist,
		Album: req.Album, Pic: req.Pic, Duration: req.Duration,
	})
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// listHistory 收听历史分页：?page&size（最近收听倒序）。
func (h handlers) listHistory(c *gin.Context) {
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	items, total, err := h.svc.ListMusicHistory(httpx.PrincipalFrom(c).ID, pageParam(c), size)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": pageParam(c)})
}

// deleteHistory 删除一条收听历史：?source&rid。
func (h handlers) deleteHistory(c *gin.Context) {
	if err := h.svc.DeleteMusicHistory(httpx.PrincipalFrom(c).ID, c.Query("source"), c.Query("rid")); err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}
