package qingmusic

// 歌单广场模块（自 qingmusic-go 移植）。端点全部在 music.haitangw.cc，纯 HTTP+JSON。
//
//	热门歌单   /music/gedan/kw.php?name=精选&page=1&limit=16   → 数组在 data.musicList
//	酷我标签   /music/gedan/kw.php?type=getTagLists&id=<tagID>&page&limit=15&order=hot
//	网易云分类 /music/gedan/wy.php?type=getCategoryList&cat=<cat>&page&limit=15&order=hot
//	歌单详情   /music/gedan/kwlist.php?id=<id>&page=<p>&limit=500&type=list
//
// 实测确认的坑：
//  1. 同一个 kw.php 端点，带 name= 时数组在 data.musicList，带 type=getTagLists 时在 data.data。
//  2. 错误字段是 msg，不是 message（MV 那边用 message）。
//  3. 字段类型跨平台不一致：酷我的 id/total/listencnt 是字符串，网易云是数字，一律宽松转换。
//  4. 列表与详情字段名不同（img/uname vs img/userName）；kw 列表项与热门项又一套
//     （id/img/listencnt vs rid/pic/PlayCnt），都要走别名链。
//  5. 歌单详情单页封顶 100 首（与请求的 limit 无关），&page= 翻页可取全量 —— 全量用 DetailAll。
//  6. kwlist.php 只认酷我歌单 id；wy/kg 的歌单在 App 里详情链路本就为空（无详情可拉），
//     能拉详情的只有 kw 与 tx（后者见 playlists_kg_tx.go）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// 歌单端点与常量。
const (
	GedanKwListPath = "/music/gedan/kw.php"
	GedanWyListPath = "/music/gedan/wy.php"
	GedanDetailPath = "/music/gedan/kwlist.php"

	// HotPlaylistKeyword 热门歌单固定用「精选」这个词（URL 编码前）。
	HotPlaylistKeyword = "精选"

	// HotPlaylistDefaultLimit 热门歌单固定 limit。
	HotPlaylistDefaultLimit = 16

	// GedanPageLimit 标签/分类列表的每页条数。
	GedanPageLimit = 15

	// GedanDetailLimit 详情接口请求的 limit（服务端单页仍封顶 GedanDetailCap）。
	GedanDetailLimit = 500

	// GedanDetailCap 单页实际返回的封顶条数（实测 100）。
	GedanDetailCap = 100

	// GedanDetailMaxPages DetailAll 翻页上限（防御服务端不返回空页导致死循环）。
	GedanDetailMaxPages = 100
)

// Playlist 歌单（列表项/标签项/分类项共用）。字段名按列表接口对齐。
type Playlist struct {
	ID         string `json:"id"` // 酷我是字符串，网易云是数字 —— 统一成字符串
	Name       string `json:"name"`
	Img        string `json:"img"`
	Pic        string `json:"pic,omitempty"`
	User       string `json:"uname"`
	UserID     string `json:"uid,omitempty"`
	Total      int64  `json:"total"` // 歌曲数
	ListenCnt  int64  `json:"listencnt"`
	FavorCnt   int64  `json:"favorcnt"`
	CommentCnt int64  `json:"commentcnt"`
	Desc       string `json:"desc,omitempty"`
	Tag        string `json:"tag,omitempty"` // 详情才有，逗号分隔
	Source     string `json:"source"`        // kw / wy / kg / tx
}

// Cover 取封面，兼容 img / pic 两种字段名。
func (p Playlist) Cover() string { return firstNonEmpty(p.Img, p.Pic) }

// PlaylistDetail 歌单详情（kwlist.php 的 data，或 tx 歌单详情归一化后的同形结构）。
type PlaylistDetail struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Img       string         `json:"img"`
	UserName  string         `json:"userName"`
	UserPic   string         `json:"uPic"`
	Total     int64          `json:"total"`
	Tag       string         `json:"tag"` // "华语,伤感,情歌"
	MusicList []PlaylistSong `json:"musicList"`

	// Source 是这个歌单（以及它里面的歌）属于哪个音源：决定 MusicList 里的
	// rid 能不能喂给 ResolveURL —— kw 的 rid 是酷我数字串，tx 的是 QQ song mid。
	Source string `json:"source,omitempty"`
}

// Tracks 把歌单曲目转成下载/解析用的 []Track。Source 为空时按酷我语义填 kw
// （唯一会走到这里的场景就是 kwlist.php）。
func (d PlaylistDetail) Tracks() []Track {
	src := d.Source
	if src == "" {
		src = SourceKw
	}
	return PlaylistSongsToTracks(d.MusicList, src)
}

// Tags 把逗号分隔的 tag 拆开，去掉空项。
func (d PlaylistDetail) Tags() []string {
	out := make([]string, 0, 4)
	for _, t := range strings.Split(d.Tag, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// PlaylistSong 歌单里的一首歌。
// kw 歌单详情只有 rid/name/artist/pic 四个字段（没有 album、没有时长，恒 0）；
// tx 详情有 album 与 interval，故 TxPlaylistDetail 会填上。
type PlaylistSong struct {
	Rid      string `json:"rid"`
	Name     string `json:"name"`
	Artist   string `json:"artist"`
	Pic      string `json:"pic"`
	Album    string `json:"album,omitempty"`
	Duration int64  `json:"duration,omitempty"` // 毫秒
}

// PlaylistSongsToTracks 把歌单曲目批量转成 Track（歌单 → 批量下载的桥）。
// rid 与 name 都为空的行不产出（喂给 ResolveURL 只会白跑一趟）。
func PlaylistSongsToTracks(songs []PlaylistSong, source string) []Track {
	out := make([]Track, 0, len(songs))
	for _, s := range songs {
		if s.Rid == "" && s.Name == "" {
			continue
		}
		out = append(out, Track{
			Source: source, Rid: s.Rid, Name: s.Name, Artist: s.Artist,
			Album: s.Album, Duration: s.Duration, Pic: s.Pic,
		})
	}
	return out
}

// ---------------------------------------------------------------- URL 构造

// HotPlaylistURL 热门歌单 URL。
func HotPlaylistURL() string {
	return fmt.Sprintf("%s%s?name=%s&page=1&limit=%d",
		MVServiceBase, GedanKwListPath,
		url.QueryEscape(HotPlaylistKeyword), HotPlaylistDefaultLimit)
}

// KwTagListURL 酷我标签/歌单列表 URL。
// ⚠️ limit=15 与 order=hot 这两个尾巴参数必须带，少了分页失效（App 写死）。
// tagID 为空时返回「分类总览」形态（实测 data.data 是热门歌单列表）。
func KwTagListURL(tagID string, page int) string {
	if page < 1 {
		page = 1
	}
	return fmt.Sprintf("%s%s?type=getTagLists&id=%s&page=%d&limit=%d&order=hot",
		MVServiceBase, GedanKwListPath, url.QueryEscape(strings.TrimSpace(tagID)),
		page, GedanPageLimit)
}

// WyCategoryListURL 网易云分类列表 URL。
func WyCategoryListURL(cat string, page int) string {
	if page < 1 {
		page = 1
	}
	return fmt.Sprintf("%s%s?type=getCategoryList&cat=%s&page=%d&limit=%d&order=hot",
		MVServiceBase, GedanWyListPath, url.QueryEscape(strings.TrimSpace(cat)),
		page, GedanPageLimit)
}

// PlaylistDetailURL 歌单详情 URL（只认酷我歌单 id）。
// 尾巴必须带：只发 ?id= 时服务端只回 30 首，带上实测回 100 首（单页封顶）。
func PlaylistDetailURL(id string, page int) string {
	if page < 1 {
		page = 1
	}
	return fmt.Sprintf("%s%s?id=%s&page=%d&limit=%d&type=list",
		MVServiceBase, GedanDetailPath, url.QueryEscape(strings.TrimSpace(id)),
		page, GedanDetailLimit)
}

// ---------------------------------------------------------------- 客户端

// PlaylistClient 歌单接口客户端（复用 MVClient 的超时与 UA）。
type PlaylistClient struct {
	MV *MVClient

	// detailPageFn 仅供测试注入（模拟翻页服务端）；为 nil 时走真实 Detail。
	detailPageFn func(ctx context.Context, id string, page int) (*PlaylistDetail, error)
}

// NewPlaylistClient 默认 5 秒超时，与首页其它请求一致。
func NewPlaylistClient() *PlaylistClient { return &PlaylistClient{MV: NewMVClient()} }

// detailPage 取一页详情（真实请求或测试注入）。
func (c *PlaylistClient) detailPage(ctx context.Context, id string, page int) (*PlaylistDetail, error) {
	if c.detailPageFn != nil {
		return c.detailPageFn(ctx, id, page)
	}
	return c.Detail(ctx, id, page)
}

// HotPlaylists 取热门歌单。⚠️ 错误字段是 msg。
func (c *PlaylistClient) HotPlaylists(ctx context.Context) ([]Playlist, error) {
	raw, err := c.MV.getJSON(ctx, HotPlaylistURL())
	if err != nil {
		return nil, err
	}
	if codeOf(raw) != 200 {
		msg := firstNonEmpty(anyString(raw["msg"]), anyString(raw["message"]))
		if msg == "" {
			msg = "热门歌单接口返回异常"
		}
		return nil, fmt.Errorf("qingmusic: %s", msg)
	}
	// 带 name= 时数组在 data.musicList
	return decodePlaylistArray(nestedData(raw)["musicList"], SourceKw), nil
}

// KwTagPage 取酷我标签下的歌单，带回总数（data.total）。
// ⚠️ 带 type=getTagLists 时数组在 data.data，不是 data.musicList。
func (c *PlaylistClient) KwTagPage(ctx context.Context, tagID string, page int) ([]Playlist, int64, error) {
	raw, err := c.MV.getJSON(ctx, KwTagListURL(tagID, page))
	if err != nil {
		return nil, 0, err
	}
	if codeOf(raw) != 200 {
		return nil, 0, fmt.Errorf("qingmusic: %s", firstNonEmpty(anyString(raw["msg"]), "歌单分类接口返回异常"))
	}
	data := nestedData(raw)
	return decodePlaylistArray(data["data"], SourceKw), anyInt64(data["total"]), nil
}

// WyCategoryPage 取网易云分类下的歌单，带回总数。
func (c *PlaylistClient) WyCategoryPage(ctx context.Context, cat string, page int) ([]Playlist, int64, error) {
	raw, err := c.MV.getJSON(ctx, WyCategoryListURL(cat, page))
	if err != nil {
		return nil, 0, err
	}
	if codeOf(raw) != 200 {
		return nil, 0, fmt.Errorf("qingmusic: %s", firstNonEmpty(anyString(raw["msg"]), "歌单分类接口返回异常"))
	}
	data := nestedData(raw)
	return decodePlaylistArray(data["data"], SourceWy), anyInt64(data["total"]), nil
}

// Detail 取歌单详情的**单页**（封顶 100 首，与请求的 limit 无关）。
// 返回的 Total 是歌单真实歌曲数（可能 205），len(MusicList) 是本页条数，两者分开看。
func (c *PlaylistClient) Detail(ctx context.Context, id string, page int) (*PlaylistDetail, error) {
	raw, err := c.MV.getJSON(ctx, PlaylistDetailURL(id, page))
	if err != nil {
		return nil, err
	}
	if codeOf(raw) != 200 {
		return nil, fmt.Errorf("qingmusic: %s", firstNonEmpty(anyString(raw["msg"]), "获取歌单失败"))
	}
	data := nestedData(raw)
	d := &PlaylistDetail{
		ID:       anyString(data["id"]),
		Name:     anyString(data["name"]),
		Img:      anyString(data["img"]),
		UserName: anyString(data["userName"]),
		UserPic:  anyString(data["uPic"]),
		Total:    anyInt64(data["total"]),
		Tag:      anyString(data["tag"]),
		// kwlist.php 是酷我侧端点，只认酷我 id，source 恒为 kw。
		Source: SourceKw,
	}
	if arr, ok := data["musicList"].([]any); ok {
		d.MusicList = make([]PlaylistSong, 0, len(arr))
		for _, e := range arr {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			s := PlaylistSong{
				Rid:    anyString(m["rid"]),
				Name:   anyString(m["name"]),
				Artist: anyString(m["artist"]),
				Pic:    anyString(m["pic"]),
				Album:  anyString(m["album"]),
			}
			if s.Rid == "" && s.Name == "" {
				continue
			}
			d.MusicList = append(d.MusicList, s)
		}
	}
	return d, nil
}

// DetailAll 翻页把歌单全部歌曲拉回来。
// 终止条件（任一命中）：本页为空 / 累计 >= Total（Total<=0 时不用）/ 页数达上限。
// 第一页失败整体失败；后续页失败返回已拿到的部分（别因第 3 页超时丢掉前 200 首）。
// 合并结果按页顺序拼接并按 Rid 去掉跨页重复项。
func (c *PlaylistClient) DetailAll(ctx context.Context, id string) (*PlaylistDetail, error) {
	var (
		out  *PlaylistDetail
		all  []PlaylistSong
		seen = map[string]struct{}{}
	)
	for page := 1; page <= GedanDetailMaxPages; page++ {
		cur, err := c.detailPage(ctx, id, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if out == nil {
			out = cur
		}
		if len(cur.MusicList) == 0 {
			break
		}
		for _, s := range cur.MusicList {
			if s.Rid != "" {
				if _, dup := seen[s.Rid]; dup {
					continue
				}
				seen[s.Rid] = struct{}{}
			}
			all = append(all, s)
		}
		if out.Total > 0 && int64(len(all)) >= out.Total {
			break
		}
	}
	if out == nil { // 理论上不可达：page=1 失败已提前 return
		out = &PlaylistDetail{ID: id}
	}
	out.MusicList = all
	return out, nil
}

// ---------------------------------------------------------------- 网易云歌单详情
//
// App 的 wy 详情链路是断的（详情页 apiUrl 空串），但网易云「榜单」与歌单详情
// 本就是同一个接口（interface3.music.163.com/api/v6/playlist/detail，wy 排行榜
// 就是官方歌单）—— 它对任意网易云歌单 id 都有效（2026-09-22 实测：华语分类
// 歌单 6957226907 可拉回 100 首曲目）。参考项目没接这条线，这里补上。
//
// ⚠️ 单次 n=WYRankMaxTracks(300)：更长的歌单只拿到前 300 首（trackCount 仍是真实总数）。

// WyPlaylistDetail 拉网易云歌单详情（含曲目，转成与 kw/tx 同形的 PlaylistDetail）。
func (c *PlaylistClient) WyPlaylistDetail(ctx context.Context, id string) (*PlaylistDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("qingmusic: 网易云歌单 id 不能为空")
	}
	client := NewMVClient()
	client.HTTP.Timeout = 20e9
	raw, err := client.postBody(ctx, WyRankURL, "application/x-www-form-urlencoded",
		WyRankBodyFor(id, WyRankMaxTracks), WyRankUA, nil)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 网易云歌单详情请求失败: %w", err)
	}

	var parsed struct {
		Code     int `json:"code"`
		Playlist struct {
			Name        string `json:"name"`
			CoverImgURL string `json:"coverImgUrl"`
			TrackCount  int64  `json:"trackCount"`
			UpdateTime  int64  `json:"trackUpdateTime"`
			Creator     struct {
				Nickname string `json:"nickname"`
			} `json:"creator"`
			Tags   []string         `json:"tags"`
			Tracks []map[string]any `json:"tracks"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: 网易云歌单详情响应解析失败: %w", err)
	}
	if parsed.Code != 0 && parsed.Code != 200 {
		return nil, fmt.Errorf("qingmusic: 网易云歌单详情返回 code=%d（歌单可能不存在）", parsed.Code)
	}

	d := &PlaylistDetail{
		ID:       id,
		Name:     parsed.Playlist.Name,
		Img:      parsed.Playlist.CoverImgURL,
		UserName: parsed.Playlist.Creator.Nickname,
		Total:    parsed.Playlist.TrackCount,
		Tag:      strings.Join(parsed.Playlist.Tags, ","),
		Source:   SourceWy,
	}
	if len(parsed.Playlist.Tracks) > 0 {
		d.MusicList = make([]PlaylistSong, 0, len(parsed.Playlist.Tracks))
		for _, it := range parsed.Playlist.Tracks {
			if s, keep := wyPlaylistSong(it); keep {
				d.MusicList = append(d.MusicList, s)
			}
		}
	}
	if d.Total <= 0 {
		d.Total = int64(len(d.MusicList))
	}
	return d, nil
}

// wyPlaylistSong 网易云歌单曲目归一化：rid=歌曲 id；dt 已是毫秒；歌手「、」拼接。
func wyPlaylistSong(track map[string]any) (PlaylistSong, bool) {
	rid := digitsOnly(anyString(track["id"]))
	if rid == "" {
		return PlaylistSong{}, false
	}
	var album, pic string
	if al, ok := track["al"].(map[string]any); ok {
		album = anyString(al["name"])
		pic = anyString(al["picUrl"])
	}
	return PlaylistSong{
		Rid:      rid,
		Name:     anyString(track["name"]),
		Artist:   joinNamesSep(track["ar"], "、"),
		Album:    album,
		Pic:      pic,
		Duration: anyInt64(track["dt"]),
	}, true
}

// ---------------------------------------------------------------- 解析辅助

// decodePlaylistArray 把 data 下的数组解成 []Playlist。
// 字段一律宽松转换；id/img/listencnt 都要走别名链（kw 列表与热门分支字段名不同）。
func decodePlaylistArray(v any, source string) []Playlist {
	arr, ok := v.([]any)
	if !ok {
		return []Playlist{}
	}
	out := make([]Playlist, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		p := Playlist{
			// 别名链：酷我列表用 id，热门用 rid
			ID:   firstNonEmpty(anyString(m["id"]), anyString(m["rid"])),
			Name: strings.TrimSpace(anyString(m["name"])),
			// 别名链：列表用 img，热门用 pic
			Img:    firstNonEmpty(anyString(m["img"]), anyString(m["pic"])),
			Pic:    anyString(m["pic"]),
			User:   firstNonEmpty(anyString(m["uname"]), anyString(m["author"])),
			UserID: anyString(m["uid"]),
			Total:  anyInt64(m["total"]),
			// 别名链：热门分支是 PlayCnt（大写 P、大写 C）
			ListenCnt:  firstAnyInt64(m, "listencnt", "PlayCnt", "playcnt"),
			FavorCnt:   anyInt64(m["favorcnt"]),
			CommentCnt: anyInt64(m["commentcnt"]),
			Desc:       anyString(m["desc"]),
			Source:     source,
		}
		if p.ID == "" && p.Name == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
