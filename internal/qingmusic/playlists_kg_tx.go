package qingmusic

// 歌单广场的 kg / tx 两个分类分支 + tx 歌单详情（自 qingmusic-go 移植）。
//
//	kg 分类  GET www2.kugou.kugou.com/yueku/v9/special/getSpecial?is_ajax=1&cdn=cdn&t=<encodeURIComponent(JSON)>
//	tx 分类  GET u.y.qq.com/cgi-bin/musicu.fcg?…&data=<encodeURIComponent(JSON)>
//	tx 详情  GET c.y.qq.com/v8/fcg-bin/fcg_v8_playlist_cp.fcg?id=<disstid>&format=json&newsong=1&platform=jqspaframe.json
//
// 实测确认的坑：
//  1. kg 响应字段与 kw/wy 都不同（specialid/specialname/song_count…），单独解码。
//  2. tx 分类的 titleid 服务端不校验，真正起作用的是 category_id（实测 1..20 全部有效）。
//     但 category_id ↔ 中文名对照无证据，不编名字。
//  3. tx 歌单分类列表的歌单本体嵌在 v_item[].basic 里，不是平铺在元素上。
//  4. tx 详情一次返回全部歌曲（501 首实测一次拿完），分页参数是 song_begin/song_num（0 起偏移）。
//  5. cdlist 里 dissid（短 id）与 disstid（长 id）不是一个东西：只有 disstid/basic.tid 能拉详情。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------- kg 歌单列表（最新歌单流）
//
// ⚠️ 参考项目走的 get_playlist_by_tag（www2.kugou.kugou.com getSpecial）已废：
// 2026-09-22 实测分类 id 与 cur_page 都被服务端忽略 —— 每页永远返回同一批 20 条。
// 酷狗官网歌单广场也已换成 gcid 新体系（服务端渲染，无公开 JSON）。
// 本库改用酷狗移动版仍在服役的 plist/index：真实翻页（total=600、30 条/页），
// 字段齐全（specialid 可直接走 v5/special/song 拉歌单详情）。

const (
	// KgPlistIndexURL 酷狗最新歌单流端点（30 条/页，page 从 1 开始，total=600）。
	KgPlistIndexURL = "http://m.kugou.com/plist/index&json=true?page=%d"
)

// KgLatestPlaylists 取酷狗最新歌单流（一页 30 条，可翻页；total=600）。
func (c *PlaylistClient) KgLatestPlaylists(ctx context.Context, page int) ([]Playlist, int64, error) {
	if page < 1 {
		page = 1
	}
	body, err := c.MV.getBodyUA(ctx, fmt.Sprintf(KgPlistIndexURL, page), DefaultMobileUA)
	if err != nil {
		return nil, 0, fmt.Errorf("qingmusic: 酷狗歌单请求失败: %w", err)
	}
	var parsed struct {
		Plist struct {
			List struct {
				Total int64            `json:"total"`
				Info  []map[string]any `json:"info"`
			} `json:"list"`
		} `json:"plist"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("qingmusic: 酷狗歌单响应解析失败: %w", err)
	}
	out := make([]Playlist, 0, len(parsed.Plist.List.Info))
	for _, m := range parsed.Plist.List.Info {
		p := Playlist{
			ID:        firstNonEmpty(anyString(m["specialid"]), anyString(m["global_specialid"])),
			Name:      anyString(m["specialname"]),
			Img:       anyString(m["imgurl"]),
			User:      anyString(m["username"]),
			UserID:    anyString(m["suid"]),
			Total:     firstAnyInt64(m, "songcount"),
			ListenCnt: firstAnyInt64(m, "playcount"),
			Desc:      anyString(m["intro"]),
			Source:    SourceKg,
		}
		if p.ID == "" || p.Name == "" {
			continue
		}
		out = append(out, p)
	}
	return out, parsed.Plist.List.Total, nil
}

// ---------------------------------------------------------------- tx 歌单分类

const (
	// TxCategoryBaseURL QQ 音乐歌单分类端点前缀。
	TxCategoryBaseURL = "https://u.y.qq.com/cgi-bin/musicu.fcg" +
		"?loginUin=0&hostUin=0&format=json&inCharset=utf-8&outCharset=utf-8" +
		"&notice=0&platform=wk_v15.json&needNewCode=0&data="

	TxCategoryModule = "playlist.PlayListCategoryServer"
	TxCategoryMethod = "get_category_content"

	TxCategoryPageSize = 15

	// TxCategoryDefaultTitleID 实测可用的默认 titleid（服务端不校验）。
	TxCategoryDefaultTitleID = 1000000000

	// TxCategoryDefaultID 实测可用的默认 category_id。
	TxCategoryDefaultID = 1

	// TxCategoryIDMin/Max 实测 category_id 的有效区间（1..20 每个都返回真实歌单）。
	TxCategoryIDMin = 1
	TxCategoryIDMax = 20
)

// TxCategoryIDs 实测有效的 category_id 列表（1..20 服务端都容忍）。
//
// ⚠️ 旧结论「1..20 全有效」没有错但会误导：其中只有 3/5/6/7/8 是 App 歌单广场
// 真正的分类（推荐/热藏/最热/最新/飙升，见 playlists_cats.go 的内置表）。
// 广场 UI 请用 PlaylistCategoriesOf(source) 的表，默认分类 = 表首项（推荐=5）。
func TxCategoryIDs() []int {
	out := make([]int, 0, TxCategoryIDMax-TxCategoryIDMin+1)
	for i := TxCategoryIDMin; i <= TxCategoryIDMax; i++ {
		out = append(out, i)
	}
	return out
}

// TxCategoryURLFor 构造 QQ 音乐歌单分类 URL；两个 id 留空即用默认值。
// ⚠️ 给错 category_id（如 10000000、0）服务端回空结果 —— HTTP 200、不是报错。
func TxCategoryURLFor(titleID, categoryID string, page int) (string, error) {
	if page < 1 {
		page = 1
	}
	tid := int64(TxCategoryDefaultTitleID)
	if s := strings.TrimSpace(titleID); s != "" {
		n, ok := anyInt64OK(s)
		if !ok {
			return "", fmt.Errorf("qingmusic: QQ音乐 titleid 必须是数字，得到 %q", titleID)
		}
		tid = n
	}
	cid := int64(TxCategoryDefaultID)
	if s := strings.TrimSpace(categoryID); s != "" {
		n, ok := anyInt64OK(s)
		if !ok {
			return "", fmt.Errorf("qingmusic: QQ音乐 category_id 必须是数字，得到 %q", categoryID)
		}
		cid = n
	}
	body := map[string]any{
		"comm": map[string]any{"cv": 1602, "ct": 20},
		"playlist": map[string]any{
			"module": TxCategoryModule,
			"method": TxCategoryMethod,
			"param": map[string]any{
				"titleid": tid, "caller": "0", "category_id": cid,
				"size": TxCategoryPageSize, "page": page, "use_page": 1,
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return TxCategoryBaseURL + url.QueryEscape(string(raw)), nil
}

// TxCategoryPlaylists 取 QQ 音乐歌单分类列表。
// 返回的 Playlist.ID 取自 v_item[].basic.tid —— 就是歌单详情的 id，可直接拉详情。
func (c *PlaylistClient) TxCategoryPlaylists(ctx context.Context, titleID, categoryID string, page int) ([]Playlist, int64, error) {
	rawURL, err := TxCategoryURLFor(titleID, categoryID, page)
	if err != nil {
		return nil, 0, err
	}
	body, err := c.MV.getBodyUA(ctx, rawURL, DefaultMobileUA)
	if err != nil {
		return nil, 0, fmt.Errorf("qingmusic: QQ音乐歌单分类请求失败: %w", err)
	}
	raw := map[string]any{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("qingmusic: QQ音乐歌单分类响应解析失败: %w", err)
	}
	top, _ := raw["playlist"].(map[string]any)
	if code := anyInt64(top["code"]); code != 0 {
		return nil, 0, fmt.Errorf("qingmusic: QQ音乐歌单分类返回 code=%d", code)
	}
	data, _ := top["data"].(map[string]any)
	content, _ := data["content"].(map[string]any)
	total := anyInt64(content["total_cnt"])
	list := decodeTxVItem(content["v_item"], SourceTx)
	return list, total, nil
}

// decodeTxVItem 解 v_item 数组：歌单本体嵌在 basic 里（为容错也接受平铺老形态）。
func decodeTxVItem(v any, source string) []Playlist {
	arr, _ := v.([]any)
	out := make([]Playlist, 0, len(arr))
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		m, _ := em["basic"].(map[string]any)
		if m == nil {
			m = em
		}
		p := txPlaylistOf(m, source)
		if p.ID == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// txPlaylistOf 把一条 tx 歌单对象解成 Playlist。
// ⚠️ ID 取值顺序有讲究：dissid 是短 id，拿它拉详情回 code=10（不存在）；
// 能请求详情的只有 disstid（长 id）与分类列表里的 tid，所以 dissid 排最后。
func txPlaylistOf(m map[string]any, source string) Playlist {
	p := Playlist{
		ID: firstNonEmpty(
			anyString(m["tid"]), anyString(m["disstid"]),
			anyString(m["id"]), anyString(m["dissid"]),
		),
		Name: firstNonEmpty(anyString(m["title"]), anyString(m["dissname"]), anyString(m["name"])),
		User: firstNonEmpty(anyString(m["nickname"]), anyString(m["nick"])),
		Desc: anyString(m["desc"]),
		Tag:  txTagNames(m["v_tag"]),
		// 别名链：v_tag 之外的 tags 数组（歌单详情里是这种）
		Total:      firstAnyInt64(m, "song_cnt", "songnum", "total_song_num", "song_count"),
		FavorCnt:   firstAnyInt64(m, "fav_cnt", "favorcnt"),
		ListenCnt:  firstAnyInt64(m, "play_cnt", "visitnum", "listencnt"),
		CommentCnt: firstAnyInt64(m, "comment_cnt", "cmtnum"),
		Source:     source,
	}
	if p.Tag == "" {
		p.Tag = txTagNames(m["tags"])
	}
	// 封面：歌单广场是 basic.cover.{big_url,…}，歌单详情是 logo
	if cov, ok := m["cover"].(map[string]any); ok {
		p.Img = firstNonEmpty(
			anyString(cov["big_url"]), anyString(cov["medium_url"]),
			anyString(cov["default_url"]), anyString(cov["small_url"]),
		)
	}
	p.Img = firstNonEmpty(p.Img, anyString(m["logo"]), anyString(m["img"]), anyString(m["imgurl"]))
	if cr, ok := m["creator"].(map[string]any); ok {
		p.User = firstNonEmpty(p.User, anyString(cr["nick"]))
	}
	return p
}

// txTagNames 把 [{id,name},…] 的标签数组拼成逗号分隔串。
func txTagNames(v any) string {
	arr, _ := v.([]any)
	names := make([]string, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if n := strings.TrimSpace(anyString(m["name"])); n != "" {
			names = append(names, n)
		}
	}
	return strings.Join(names, ",")
}

// ---------------------------------------------------------------- tx 歌单详情

const (
	// TxPlaylistDetailBaseURL QQ 音乐歌单详情端点。
	TxPlaylistDetailBaseURL = "https://c.y.qq.com/v8/fcg-bin/fcg_v8_playlist_cp.fcg"

	// TxPlaylistDetailTail 实测可用的固定尾巴（裸请求也能过，无需 Referer/UA）。
	TxPlaylistDetailTail = "&format=json&newsong=1&platform=jqspaframe.json"

	// TxPlaylistDetailCodeOK 成功码；歌单不存在时是 10。
	TxPlaylistDetailCodeOK = 0
)

// TxPlaylistDetailURL 构造详情 URL。begin 是歌曲偏移量（0 起）；num<=0 不带分页参数
// —— 实测不带就是一次返回全部歌曲。
func TxPlaylistDetailURL(id string, begin, num int) string {
	u := TxPlaylistDetailBaseURL + "?id=" + url.QueryEscape(strings.TrimSpace(id)) +
		TxPlaylistDetailTail
	if num > 0 {
		if begin < 0 {
			begin = 0
		}
		u += fmt.Sprintf("&song_begin=%d&song_num=%d", begin, num)
	}
	return u
}

// TxPlaylistDetail 拉 QQ 音乐歌单详情（含全部歌曲，与 kw 侧 PlaylistDetail 同形）。
func (c *PlaylistClient) TxPlaylistDetail(ctx context.Context, id string) (*PlaylistDetail, error) {
	return c.txPlaylistDetail(ctx, id, 0, 0)
}

func (c *PlaylistClient) txPlaylistDetail(ctx context.Context, id string, begin, num int) (*PlaylistDetail, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单 id 不能为空")
	}
	body, err := c.MV.getBodyUA(ctx, TxPlaylistDetailURL(id, begin, num), DefaultMobileUA)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单详情请求失败: %w", err)
	}
	raw := map[string]any{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单详情响应解析失败: %w", err)
	}
	if code := anyInt64(raw["code"]); code != TxPlaylistDetailCodeOK {
		// code=10 = 歌单不存在（错误的 id），别当网络问题。
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单详情返回 code=%d（10 表示歌单不存在）", code)
	}
	data, _ := raw["data"].(map[string]any)
	cdlist, _ := data["cdlist"].([]any)
	if len(cdlist) == 0 {
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单详情无数据（id=%s）", id)
	}
	first, _ := cdlist[0].(map[string]any)
	if first == nil {
		return nil, fmt.Errorf("qingmusic: QQ音乐歌单详情 cdlist[0] 结构异常（id=%s）", id)
	}

	// 歌单本体字段与分类列表项不是同一套：详情是扁平 logo/nickname/tags。
	p := txPlaylistOf(first, SourceTx)
	d := &PlaylistDetail{
		ID:       firstNonEmpty(anyString(first["disstid"]), p.ID, id),
		Name:     firstNonEmpty(anyString(first["dissname"]), p.Name),
		Img:      firstNonEmpty(p.Img, anyString(first["logo"])),
		UserName: firstNonEmpty(anyString(first["nickname"]), anyString(first["nick"])),
		UserPic:  anyString(first["headurl"]),
		Total:    firstAnyInt64(first, "total_song_num", "songnum", "cur_song_num"),
		Tag:      p.Tag,
		Source:   SourceTx,
	}
	if d.Total <= 0 {
		d.Total = int64(len(mustArray(first["songlist"])))
	}
	if arr := mustArray(first["songlist"]); len(arr) > 0 {
		d.MusicList = make([]PlaylistSong, 0, len(arr))
		for _, e := range arr {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if s, keep := NormalizeTxPlaylistSong(m); keep {
				d.MusicList = append(d.MusicList, s)
			}
		}
	}
	return d, nil
}

// NormalizeTxPlaylistSong QQ 音乐歌单详情里的单曲归一化。
// rid 取 mid（不是 id）—— resolve-url 要的是 song mid；interval 是秒，换算毫秒。
func NormalizeTxPlaylistSong(item map[string]any) (PlaylistSong, bool) {
	if item == nil {
		return PlaylistSong{}, false
	}
	rid := firstNonEmpty(anyString(item["mid"]), anyString(item["id"]))
	if rid == "" {
		return PlaylistSong{}, false
	}
	var albumMid, albumName string
	if al, ok := item["album"].(map[string]any); ok {
		albumMid = anyString(al["mid"])
		albumName = anyString(al["name"])
	}
	pic := ""
	if albumMid != "" {
		pic = "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + albumMid + ".jpg"
	}
	return PlaylistSong{
		Rid:      rid,
		Name:     firstNonEmpty(anyString(item["title"]), anyString(item["name"])),
		Artist:   joinNamesSep(item["singer"], " & "),
		Album:    albumName,
		Pic:      pic,
		Duration: anyInt64(item["interval"]) * 1000, // interval 是秒
	}, true
}

// ---------------------------------------------------------------- 酷狗歌单详情
//
// App 的 kg 详情链路是断的（详情页 apiUrl 空串），但酷狗自己的曲目列表接口是活的：
// mobilecdnbj.kugou.com/api/v5/special/song?specialid=<id>&page=&pagesize=
//（2026-09-22 实测：返回 data.total + data.info[]，hash 为大写 FileHash —— 恰是
// resolve-url 要的 rid，duration 为秒，filename 形如「歌手 - 歌名」）。
// 参考项目没接这条线，这里补上，kg 歌单从此可打开/试听/批量下载。

// KgSpecialSongURL 酷狗歌单曲目列表端点（pagesize 一次最多给几百，单页拿全）。
const KgSpecialSongURL = "http://mobilecdnbj.kugou.com/api/v5/special/song" +
	"?specialid=%s&page=%d&pagesize=%d&version=9108"

// KgPlaylistDetailPageSize 单次拉取条数（大于绝大多数歌单；Data.Total 是真实总数）。
const KgPlaylistDetailPageSize = 300

// KgPlaylistDetail 拉酷狗歌单详情（含曲目，转成与 kw/tx 同形的 PlaylistDetail）。
// 歌单元信息（名称/封面）不在该接口里，由调用方从广场卡片补充。
func (c *PlaylistClient) KgPlaylistDetail(ctx context.Context, specialID string) (*PlaylistDetail, error) {
	specialID = strings.TrimSpace(specialID)
	if specialID == "" {
		return nil, fmt.Errorf("qingmusic: 酷狗歌单 specialid 不能为空")
	}
	client := NewMVClient()
	client.HTTP.Timeout = 20e9
	rawURL := fmt.Sprintf(KgSpecialSongURL, url.QueryEscape(specialID), 1, KgPlaylistDetailPageSize)
	body, err := client.getBodyUA(ctx, rawURL, DefaultMobileUA)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 酷狗歌单详情请求失败: %w", err)
	}
	var parsed struct {
		Status int `json:"status"`
		Data   struct {
			Total int              `json:"total"`
			Info  []map[string]any `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: 酷狗歌单详情响应解析失败: %w", err)
	}
	if parsed.Status != 0 && parsed.Status != 1 {
		// 实测成功响应 status=1；其他值按错误处理
		return nil, fmt.Errorf("qingmusic: 酷狗歌单详情返回 status=%d（歌单可能不存在）", parsed.Status)
	}
	d := &PlaylistDetail{
		ID:     specialID,
		Total:  int64(parsed.Data.Total),
		Source: SourceKg,
	}
	if len(parsed.Data.Info) > 0 {
		d.MusicList = make([]PlaylistSong, 0, len(parsed.Data.Info))
		for _, it := range parsed.Data.Info {
			if s, keep := kgPlaylistSong(it); keep {
				d.MusicList = append(d.MusicList, s)
			}
		}
	}
	if d.Total <= 0 {
		d.Total = int64(len(d.MusicList))
	}
	return d, nil
}

// kgPlaylistSong 酷狗歌单曲目归一化：rid=hash（大写 FileHash）；duration 秒 → 毫秒；
// filename 形如「歌手 - 歌名」，歌手缺省时走 authors 兜底。
func kgPlaylistSong(item map[string]any) (PlaylistSong, bool) {
	hash := strings.ToUpper(anyString(item["hash"]))
	if hash == "" {
		return PlaylistSong{}, false
	}
	// filename「歌手 - 歌名」：歌名里再有 ' - ' 则拼回（与 kg 排行榜同款拆分）
	artist, name := "", strings.TrimSpace(anyString(item["filename"]))
	if parts := strings.SplitN(name, " - ", 2); len(parts) == 2 {
		artist, name = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	pic := ""
	if tp, ok := item["trans_param"].(map[string]any); ok {
		if cov := anyString(tp["union_cover"]); cov != "" {
			pic = strings.ReplaceAll(cov, "{size}", "240")
		}
	}
	return PlaylistSong{
		Rid:      hash,
		Name:     name,
		Artist:   artist,
		Pic:      pic,
		Duration: anyInt64(anyString(item["duration"])) * 1000, // 秒 → 毫秒
	}, true
}
