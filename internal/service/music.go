// 音乐与链接下载的服务层：qingmusic 搜索/音源/歌词/直链解析与 yt-dlp 链接预览。
// HTTP 面在 internal/modules/music 与 internal/modules/downloader，此处只收业务。
package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/provider/downloader"
	"github.com/yann0917/voxbox/internal/provider/music"
	"github.com/yann0917/voxbox/internal/qingmusic"
	"github.com/yann0917/voxbox/internal/store"
)

// musicSearchTimeout 单次搜索的整体时限（各源内部另有独立重试与超时）。
const musicSearchTimeout = 20 * time.Second

// MusicSources 当前音源配置的只读视图（线路 / 启用态 / 支持档位 / 搜索实现名）。
// 未导入音源配置时返回空切片（前端展示「去设置页同步或导入」引导）。
func (s *Service) MusicSources() []qingmusic.SourceLine {
	cfg, err := music.LoadSources(s.dataDir)
	if err != nil {
		return nil
	}
	return cfg.Lines
}

// MusicConfigured 音源配置是否已导入。
func (s *Service) MusicConfigured() bool {
	_, err := os.Stat(music.SourcesFile(s.dataDir))
	return err == nil
}

// MusicSearch 单音源搜索。page 从 1 起。
func (s *Service) MusicSearch(ctx context.Context, source, keyword string, page int) ([]qingmusic.Track, error) {
	if keyword == "" {
		return nil, fmt.Errorf("搜索词不能为空")
	}
	if page < 1 {
		page = 1
	}
	if _, err := music.LoadSources(s.dataDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.NewSearcher().Search(ctx, source, keyword, page)
}

// sanitizeSourceConfig 剥掉音源配置头的 JS 注释 banner（社区配置普遍带
// `/** @name … */` 头与 // 行注释，非合法 JSON）：_PARSE 前净化，落盘文件即纯 JSON。
func sanitizeSourceConfig(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	for strings.HasPrefix(s, "/*") {
		if i := strings.Index(s, "*/"); i >= 0 {
			s = strings.TrimSpace(s[i+2:])
		} else {
			break
		}
	}
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(strings.Join(kept, "\n"))
}

// MusicSyncSources 从配置的同步地址拉取音源配置落盘（设置页「同步音源」）。
// 返回导入的音源数。
func (s *Service) MusicSyncSources(ctx context.Context) (int, error) {
	url := s.Config().Music.SourcesURL
	if url == "" {
		return 0, fmt.Errorf("未配置音源同步地址：设置页「音乐音源」填写 music.json 的拉取 URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("同步地址不合法: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("拉取音源配置失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("拉取音源配置失败：HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, fmt.Errorf("读取音源配置失败: %w", err)
	}
	return s.MusicImportSources(raw)
}

// MusicImportSources 校验并导入一份音源配置（music.json），成功即落盘生效。
func (s *Service) MusicImportSources(raw []byte) (int, error) {
	clean := sanitizeSourceConfig(raw)
	parsed, err := qingmusic.ParseSourceConfig(clean)
	if err != nil {
		return 0, fmt.Errorf("音源配置解析失败: %w", err)
	}
	if len(parsed.Lines) == 0 {
		return 0, fmt.Errorf("音源配置里没有任何线路")
	}
	path := music.SourcesFile(s.dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, clean, 0o600); err != nil {
		return 0, fmt.Errorf("音源配置写入失败: %w", err)
	}
	return len(parsed.Lines), nil
}

// MusicLyric 取歌词（lrc 文本）。不支持或拿不到时返回错误（调用方按可选处理）。
func (s *Service) MusicLyric(ctx context.Context, source, rid, name, artist string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return qingmusic.NewSearcher().Lyric(ctx, source, rid, name, artist, 0)
}

// MusicResolve 解析试听直链：按音源配置定档（默认 standard），返回带请求头的直链，
// 由模块端点代理转发（直链有时效与防盗链头，浏览器直连不可靠）。
func (s *Service) MusicResolve(ctx context.Context, source, rid, level string) (*qingmusic.Resolved, error) {
	sources, err := music.LoadSources(s.dataDir)
	if err != nil {
		return nil, err
	}
	if lv, ok := sources.ResolveLevel(source, level); ok {
		level = lv
	} else if level == "" {
		level = qingmusic.LevelStandard
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return qingmusic.NewGateway().ResolveURL(ctx, source, rid, level)
}

// DownloaderProbe 链接元数据预览（yt-dlp -J；抖音链接标记交前端按抖音流程走）。
func (s *Service) DownloaderProbe(ctx context.Context, raw string) (map[string]any, error) {
	if raw == "" {
		return nil, fmt.Errorf("链接不能为空")
	}
	return downloader.Probe(ctx, s.Config(), raw)
}

// ---------------------------------------------------------------- 聚合并发搜索

// MusicSearchAll 全音源并发搜索：对全部启用且实现了搜索的音源 fan-out，
// 按音源配置顺序聚合（并发完成顺序不定，直接 append 会把顺序变成竞速排名）。
// 单音源失败不拖垮整体，错误按音源归档给前端标注。
func (s *Service) MusicSearchAll(ctx context.Context, keyword string, page int) ([]qingmusic.Track, map[string]string, error) {
	if page < 1 {
		page = 1
	}
	sc, err := music.LoadSources(s.dataDir)
	if err != nil {
		return nil, nil, err
	}
	searcher := qingmusic.NewSearcher()
	lines := sc.SearchableLines(searcher)
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("没有可用的音源：请检查音源配置的启用状态")
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()

	type result struct {
		idx    int
		tracks []qingmusic.Track
		err    error
	}
	ch := make(chan result, len(lines))
	for i, l := range lines {
		go func(i int, l qingmusic.SourceLine) {
			tracks, err := searcher.SearchByLine(ctx, l, keyword, page)
			ch <- result{idx: i, tracks: tracks, err: err}
		}(i, l)
	}
	perSource := make([][]qingmusic.Track, len(lines))
	errs := map[string]string{}
	for range lines {
		r := <-ch
		if r.err != nil {
			errs[lines[r.idx].ID] = r.err.Error()
			continue
		}
		perSource[r.idx] = r.tracks
	}
	items := make([]qingmusic.Track, 0, len(lines)*10)
	for _, tracks := range perSource {
		items = append(items, tracks...)
	}
	return items, errs, nil
}

// ---------------------------------------------------------------- 排行榜

// MusicRankSources 支持榜单的音源（kw/wy/tx/kg）。
func (s *Service) MusicRankSources() []string { return qingmusic.RankSources() }

// MusicRankBoards 某音源的内置榜单清单（App 硬编码表）。
func (s *Service) MusicRankBoards(source string) []qingmusic.RankBoardInfo {
	return qingmusic.RankBoards(source)
}

// MusicRankSongs 拉某榜单歌曲页。
func (s *Service) MusicRankSongs(ctx context.Context, source, boardID string, page int) (*qingmusic.RankBoard, error) {
	if page < 1 {
		page = 1
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.FetchRankSongs(ctx, source, boardID, page)
}

// ---------------------------------------------------------------- 歌单广场

// MusicPlaylistPage 歌单列表的一页（总数供前端分页；kg 类接口总数=本页条数）。
type MusicPlaylistPage struct {
	Source string               `json:"source"`
	Tag    string               `json:"tag,omitempty"`
	Page   int                  `json:"page"`
	Total  int64                `json:"total"`
	Items  []qingmusic.Playlist `json:"items"`
}

// MusicPlaylistLines 歌单广场线路 + 二级分类表。
func (s *Service) MusicPlaylistLines() []qingmusic.PlaylistLine {
	return qingmusic.PlaylistLines()
}

// MusicPlaylists 按线路拉歌单页：kw（tag 空=分类总览）、wy/tx（分类解析）、kg（最新流无分类）。
func (s *Service) MusicPlaylists(ctx context.Context, source, tag string, page int) (*MusicPlaylistPage, error) {
	if page < 1 {
		page = 1
	}
	source = strings.ToLower(strings.TrimSpace(source))
	tag = strings.TrimSpace(tag)
	c := qingmusic.NewPlaylistClient()
	switch source {
	case qingmusic.SourceKw:
		id := tag
		if id != "" {
			var err error
			if id, err = qingmusic.ResolvePlaylistCategory(source, id); err != nil {
				return nil, err
			}
		}
		items, total, err := c.KwTagPage(ctx, id, page)
		if err != nil {
			return nil, err
		}
		return &MusicPlaylistPage{Source: source, Tag: tag, Page: page, Total: total, Items: items}, nil
	case qingmusic.SourceWy:
		cat, err := qingmusic.ResolvePlaylistCategory(source, tag)
		if err != nil {
			return nil, err
		}
		items, total, err := c.WyCategoryPage(ctx, cat, page)
		if err != nil {
			return nil, err
		}
		return &MusicPlaylistPage{Source: source, Tag: cat, Page: page, Total: total, Items: items}, nil
	case qingmusic.SourceTx:
		cat, err := qingmusic.ResolvePlaylistCategory(source, tag)
		if err != nil {
			return nil, err
		}
		items, total, err := c.TxCategoryPlaylists(ctx, "", cat, page)
		if err != nil {
			return nil, err
		}
		return &MusicPlaylistPage{Source: source, Tag: cat, Page: page, Total: total, Items: items}, nil
	case qingmusic.SourceKg:
		items, total, err := c.KgLatestPlaylists(ctx, page)
		if err != nil {
			return nil, err
		}
		return &MusicPlaylistPage{Source: source, Page: page, Total: total, Items: items}, nil
	case "":
		return nil, fmt.Errorf("参数错误：source 必填（kw | wy | tx | kg）")
	default:
		return nil, fmt.Errorf("未知歌单线路 %q（可选 kw/wy/tx/kg）", source)
	}
}

// MusicPlaylistDetailDTO 歌单详情（曲目已转 Track，可直接复用试听/下载/分离动作）。
type MusicPlaylistDetailDTO struct {
	Source   string            `json:"source"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Img      string            `json:"img,omitempty"`
	UserName string            `json:"user_name,omitempty"`
	Total    int64             `json:"total"`
	Tracks   []qingmusic.Track `json:"tracks"`
}

// MusicPlaylistDetail 拉歌单详情（kw 翻页取全量，tx 一次全量，wy 单次至多 300 首）。
func (s *Service) MusicPlaylistDetail(ctx context.Context, source, id string) (*MusicPlaylistDetailDTO, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("参数错误：id 必填")
	}
	c := qingmusic.NewPlaylistClient()
	var d *qingmusic.PlaylistDetail
	var err error
	switch strings.ToLower(strings.TrimSpace(source)) {
	case qingmusic.SourceKw:
		d, err = c.DetailAll(ctx, id)
	case qingmusic.SourceTx:
		d, err = c.TxPlaylistDetail(ctx, id)
	case qingmusic.SourceWy:
		d, err = c.WyPlaylistDetail(ctx, id)
	case qingmusic.SourceKg:
		d, err = c.KgPlaylistDetail(ctx, id)
	case "":
		return nil, fmt.Errorf("参数错误：source 必填（kw | tx | wy | kg）")
	default:
		return nil, fmt.Errorf("未知歌单线路 %q（可选 kw/tx/wy/kg）", source)
	}
	if err != nil {
		return nil, err
	}
	return &MusicPlaylistDetailDTO{
		Source: d.Source, ID: d.ID, Name: d.Name, Img: d.Img,
		UserName: d.UserName, Total: d.Total, Tracks: d.Tracks(),
	}, nil
}

// ---------------------------------------------------------------- MV

// mvLineOf 音源 id → MV 线路名；空/未知回落 QQ（与 App 的 default 分支一致）。
func mvLineOf(source string) string {
	if line := qingmusic.LineNameOf(source); line != "" {
		return line
	}
	return qingmusic.LineTx
}

// MusicMvHot 热门 MV（QQ 源）。
func (s *Service) MusicMvHot(ctx context.Context, page, size int) ([]qingmusic.MVItem, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 60 {
		size = 30
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.NewMVClient().HotMVs(ctx, page, size)
}

// MusicMvSearch 按线路搜索 MV。
func (s *Service) MusicMvSearch(ctx context.Context, source, keyword string, page int) ([]qingmusic.MVItem, error) {
	if page < 1 {
		page = 1
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.NewMVClient().SearchMVs(ctx, mvLineOf(source), keyword, page)
}

// MusicMvPlayback 某 MV 的可播画质地址列表。
func (s *Service) MusicMvPlayback(ctx context.Context, source, vid string) ([]qingmusic.MVQuality, error) {
	if vid == "" {
		return nil, fmt.Errorf("参数错误：id 必填")
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.NewMVClient().PlaybackURLs(ctx, mvLineOf(source), vid)
}

// MusicMvStream MV 流代理响应：直链多为 http（https 部署会被拦混合内容），
// 统一同源代理；Range 头透传，<video> 可拖动进度。调用方负责关闭 Body。
func (s *Service) MusicMvStream(ctx context.Context, source, vid, quality, rangeHeader string) (*http.Response, error) {
	qs, err := s.MusicMvPlayback(ctx, source, vid)
	if err != nil {
		return nil, err
	}
	picked, err := qingmusic.PickQuality(qs, quality)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, picked.URL, nil)
	if err != nil {
		return nil, err
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取 MV 流失败: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("拉取 MV 流失败: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// ---------------------------------------------------------------- 收听历史

// AddMusicHistory 记一次收听（best-effort 由调用方保证：失败不影响播放）。
func (s *Service) AddMusicHistory(userID string, it store.MusicPlayHistory) error {
	return s.db.AddMusicPlayHistory(userID, it)
}

// ListMusicHistory 收听历史分页（最近收听倒序）。
func (s *Service) ListMusicHistory(userID string, page, size int) ([]store.MusicPlayHistory, int64, error) {
	return s.db.ListMusicPlayHistory(userID, page, size)
}

// DeleteMusicHistory 删除一条收听历史。
func (s *Service) DeleteMusicHistory(userID, source, rid string) error {
	return s.db.DeleteMusicPlayHistory(userID, source, rid)
}
