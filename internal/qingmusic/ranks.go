package qingmusic

// 排行榜模块（自 qingmusic-go 移植）：四平台榜单全部明文 HTTP，无加密无签名。
//
//	kg  GET  mobilecdnbj.kugou.com/api/v3/rank/song?…&rankid=<id>&page=<p>
//	kw  GET  wapi.kuwo.cn/api/www/bang/bang/musicList?bangId=<id>&pn=<p>&rn=30
//	wy  POST interface3.music.163.com/api/v6/playlist/detail  （榜单=官方歌单，无翻页 n=300）
//	tx  POST u.y.qq.com/cgi-bin/musicu.fcg（musicToplist.ToplistInfoServer/GetDetail，无翻页）
//
// 各家的坑：
//  1. kw 的 data.num 是字符串 "300"，必须宽松解析。
//  2. kw 时长字段 songTimeMinutes 是格式化串 "03:19"；wy 是 "4分13秒"；
//     tx 的 interval 是秒数塞 Duration。同名不同义。
//  3. tx 歌名取 title 不是 name；歌手用 " & " 拼接（kw/wy 是 "、"）。
//  4. kg 失败时不抛异常而是 {code:500,error}，Go 侧统一返回 error。
//  5. wy 在 App 里带的硬编码 MUSIC_U cookie 属于真实账号，故意不内置（公开榜单不需要）。
//  6. 榜单 id 列表四家都是 App 内置的（硬编码表见 ranksBoardTables）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 榜单端点与常量。
const (
	// KgRankURL 酷狗榜单（明文 http，App 原样如此），pagesize=100 写死在 URL 里。
	KgRankURL = "http://mobilecdnbj.kugou.com/api/v3/rank/song" +
		"?version=9108&ranktype=1&plat=0&pagesize=100&area_code=1&page="

	KwRankURL = "https://wapi.kuwo.cn/api/www/bang/bang/musicList?bangId=%s&pn=%d&rn=%d"

	// KwRankPageSize 酷我每页 30。
	KwRankPageSize = 30

	// KwRankUA dex 里写死的 Edge UA。
	KwRankUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/113.0.0.0 Safari/537.36 Edg/113.0.1774.50"

	// WyRankURL 网易云「榜单」—— 实际是歌单详情接口。
	WyRankURL = "https://interface3.music.163.com/api/v6/playlist/detail"

	// WyRankBodyFormat 请求体：id 是榜单对应的官方歌单 id。
	WyRankBodyFormat = "id=%s&n=%d&newDetailPage=1&newStyle=true&s=5"

	// WyRankMaxTracks n=300 一次拿（wy 没有翻页参数）。
	WyRankMaxTracks = 300

	// WyRankUA dex 里用的 Chrome UA。
	WyRankUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

	// TxRankURL QQ 音乐统一网关。
	TxRankURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"

	// TxRankUA App 写的是一个 IE9 UA。
	TxRankUA = "Mozilla/5.0 (compatible; MSIE 9.0; Windows NT 6.1; WOW64; Trident/5.0)"

	TxRankModule = "musicToplist.ToplistInfoServer"
	TxRankMethod = "GetDetail"

	// TxRankDefaultNum 默认条数（30 即榜单前 30）。
	TxRankDefaultNum = 30
)

// RankQuality 一个可用档位（仅 kg）。
type RankQuality struct {
	Type string `json:"type"` // "128k" / "320k" / "flac"
	Size string `json:"size"`
	Hash string `json:"hash"`
}

// RankSong 榜单里的一首歌。
// Duration 单位随平台变：kg/tx 是秒；kw/wy 恒 0（时长在 Interval 格式串里）。
type RankSong struct {
	Rid      string                 `json:"rid"` // kg 里就是 item.hash
	Name     string                 `json:"name"`
	Artist   string                 `json:"artist"`
	Pic      string                 `json:"pic"`
	Album    string                 `json:"album"`
	Duration int64                  `json:"duration"`
	Source   string                 `json:"source"` // kg / kw / wy / tx
	Hash     string                 `json:"hash"`
	Types    []RankQuality          `json:"types"`
	TypesMap map[string]RankQuality `json:"_types"`
	Sort     int64                  `json:"sort,omitempty"`
	Vid      string                 `json:"vid,omitempty"` // 无 MV 时塞字面量「没有vid」（App 行为）
	Interval string                 `json:"interval,omitempty"`
	Time     string                 `json:"time,omitempty"`
}

// RankBoard 一份榜单。
type RankBoard struct {
	Code       int        `json:"code"`
	Pic        string     `json:"pic"`
	Name       string     `json:"name"`
	MusicList  []RankSong `json:"musicList"`
	Total      int64      `json:"total"`
	UpdateTime string     `json:"updateTime"`
	Error      string     `json:"error,omitempty"`
}

// RankBoardInfo 一份内置榜单的描述符。
type RankBoardInfo struct {
	Source string `json:"source"` // kw / wy / tx / kg
	ID     string `json:"id"`     // App 里的 id（kg 带 kg__ 前缀，tx 就是 topid）
	Name   string `json:"name"`   // 中文榜名
	RankID string `json:"rankid"` // 真正传给接口的 id：kw=bangId，wy=歌单id，tx=topid，kg=rankid
}

// ---------------------------------------------------------------- 内置榜单清单（App 硬编码表）

// kwRankBoardTable dex 里 getKwRankingData 的 mapOf（顺序按 dex 原样）。
var kwRankBoardTable = []RankBoardInfo{
	{Source: SourceKw, ID: "93", Name: "飙升榜", RankID: "93"},
	{Source: SourceKw, ID: "17", Name: "新歌榜", RankID: "17"},
	{Source: SourceKw, ID: "16", Name: "热歌榜", RankID: "16"},
	{Source: SourceKw, ID: "158", Name: "抖音榜", RankID: "158"},
	{Source: SourceKw, ID: "176", Name: "万物榜", RankID: "176"},
	{Source: SourceKw, ID: "145", Name: "畅听榜", RankID: "145"},
}

// wyRankBoardTable dex 里 getWyRankingData 的 mapOf。
var wyRankBoardTable = []RankBoardInfo{
	{Source: SourceWy, ID: "19723756", Name: "飙升榜", RankID: "19723756"},
	{Source: SourceWy, ID: "3778678", Name: "热歌榜", RankID: "3778678"},
	{Source: SourceWy, ID: "2884035", Name: "原创榜", RankID: "2884035"},
	{Source: SourceWy, ID: "7785066739", Name: "黑胶vip热歌榜", RankID: "7785066739"},
	{Source: SourceWy, ID: "3779629", Name: "新歌榜", RankID: "3779629"},
	{Source: SourceWy, ID: "991319590", Name: "说唱榜", RankID: "991319590"},
	{Source: SourceWy, ID: "71384707", Name: "古典榜", RankID: "71384707"},
	{Source: SourceWy, ID: "745956260", Name: "韩语榜", RankID: "745956260"},
	{Source: SourceWy, ID: "5059644681", Name: "日语榜", RankID: "5059644681"},
	{Source: SourceWy, ID: "5059642708", Name: "国风榜", RankID: "5059642708"},
	{Source: SourceWy, ID: "6886768100", Name: "中文DJ榜", RankID: "6886768100"},
	{Source: SourceWy, ID: "6723173524", Name: "网络热歌榜", RankID: "6723173524"},
	{Source: SourceWy, ID: "8532443277", Name: "蛋仔派对听歌榜", RankID: "8532443277"},
}

// txRankBoardTable JS 常量（id == topid）。
var txRankBoardTable = []RankBoardInfo{
	{Source: SourceTx, ID: "4", Name: "流行指数榜", RankID: "4"},
	{Source: SourceTx, ID: "26", Name: "热歌榜", RankID: "26"},
	{Source: SourceTx, ID: "27", Name: "新歌榜", RankID: "27"},
	{Source: SourceTx, ID: "62", Name: "飙升榜", RankID: "62"},
	{Source: SourceTx, ID: "58", Name: "说唱榜", RankID: "58"},
	{Source: SourceTx, ID: "57", Name: "喜力电音榜", RankID: "57"},
	{Source: SourceTx, ID: "28", Name: "网络歌曲榜", RankID: "28"},
	{Source: SourceTx, ID: "5", Name: "内地榜", RankID: "5"},
	{Source: SourceTx, ID: "3", Name: "欧美榜", RankID: "3"},
	{Source: SourceTx, ID: "59", Name: "香港地区榜", RankID: "59"},
	{Source: SourceTx, ID: "16", Name: "韩国榜", RankID: "16"},
	{Source: SourceTx, ID: "60", Name: "抖快榜", RankID: "60"},
	{Source: SourceTx, ID: "29", Name: "影视金曲榜", RankID: "29"},
	{Source: SourceTx, ID: "17", Name: "日本榜", RankID: "17"},
	{Source: SourceTx, ID: "52", Name: "腾讯音乐人原创榜", RankID: "52"},
	{Source: SourceTx, ID: "36", Name: "K歌金曲榜", RankID: "36"},
	{Source: SourceTx, ID: "61", Name: "台湾地区榜", RankID: "61"},
	{Source: SourceTx, ID: "63", Name: "DJ舞曲榜", RankID: "63"},
	{Source: SourceTx, ID: "64", Name: "综艺新歌榜", RankID: "64"},
	{Source: SourceTx, ID: "65", Name: "国风热歌榜", RankID: "65"},
	{Source: SourceTx, ID: "67", Name: "听歌识曲榜", RankID: "67"},
	{Source: SourceTx, ID: "72", Name: "动漫音乐榜", RankID: "72"},
}

// kgRankBoardTable JS 常量 kgBoardList（id 形如 "kg__8888"，RankID 是纯数字）。
var kgRankBoardTable = []RankBoardInfo{
	{Source: SourceKg, ID: "kg__8888", Name: "TOP500", RankID: "8888"},
	{Source: SourceKg, ID: "kg__6666", Name: "飙升榜", RankID: "6666"},
	{Source: SourceKg, ID: "kg__59703", Name: "蜂鸟流行音乐榜", RankID: "59703"},
	{Source: SourceKg, ID: "kg__52144", Name: "抖音热歌榜", RankID: "52144"},
	{Source: SourceKg, ID: "kg__52767", Name: "快手热歌榜", RankID: "52767"},
	{Source: SourceKg, ID: "kg__24971", Name: "DJ热歌榜", RankID: "24971"},
	{Source: SourceKg, ID: "kg__23784", Name: "网络红歌榜", RankID: "23784"},
	{Source: SourceKg, ID: "kg__44412", Name: "说唱先锋榜", RankID: "44412"},
	{Source: SourceKg, ID: "kg__31308", Name: "内地榜", RankID: "31308"},
	{Source: SourceKg, ID: "kg__33160", Name: "电音榜", RankID: "33160"},
	{Source: SourceKg, ID: "kg__31313", Name: "香港地区榜", RankID: "31313"},
	{Source: SourceKg, ID: "kg__51341", Name: "民谣榜", RankID: "51341"},
	{Source: SourceKg, ID: "kg__54848", Name: "台湾地区榜", RankID: "54848"},
	{Source: SourceKg, ID: "kg__31310", Name: "欧美榜", RankID: "31310"},
	{Source: SourceKg, ID: "kg__33162", Name: "ACG新歌榜", RankID: "33162"},
	{Source: SourceKg, ID: "kg__31311", Name: "韩国榜", RankID: "31311"},
	{Source: SourceKg, ID: "kg__31312", Name: "日本榜", RankID: "31312"},
	{Source: SourceKg, ID: "kg__49225", Name: "80后热歌榜", RankID: "49225"},
	{Source: SourceKg, ID: "kg__49223", Name: "90后热歌榜", RankID: "49223"},
	{Source: SourceKg, ID: "kg__49224", Name: "00后热歌榜", RankID: "49224"},
	{Source: SourceKg, ID: "kg__33165", Name: "粤语金曲榜", RankID: "33165"},
	{Source: SourceKg, ID: "kg__33166", Name: "欧美金曲榜", RankID: "33166"},
	{Source: SourceKg, ID: "kg__33163", Name: "影视金曲榜", RankID: "33163"},
	{Source: SourceKg, ID: "kg__51340", Name: "伤感榜", RankID: "51340"},
	{Source: SourceKg, ID: "kg__35811", Name: "会员专享榜", RankID: "35811"},
	{Source: SourceKg, ID: "kg__37361", Name: "雷达榜", RankID: "37361"},
	{Source: SourceKg, ID: "kg__21101", Name: "分享榜", RankID: "21101"},
	{Source: SourceKg, ID: "kg__46910", Name: "综艺新歌榜", RankID: "46910"},
	{Source: SourceKg, ID: "kg__30972", Name: "酷狗音乐人原创榜", RankID: "30972"},
	{Source: SourceKg, ID: "kg__60170", Name: "闽南语榜", RankID: "60170"},
	{Source: SourceKg, ID: "kg__65234", Name: "儿歌榜", RankID: "65234"},
	{Source: SourceKg, ID: "kg__4681", Name: "美国BillBoard榜", RankID: "4681"},
	{Source: SourceKg, ID: "kg__25028", Name: "Beatport电子舞曲榜", RankID: "25028"},
	{Source: SourceKg, ID: "kg__4680", Name: "英国单曲榜", RankID: "4680"},
	{Source: SourceKg, ID: "kg__38623", Name: "韩国Melon音乐榜", RankID: "38623"},
	{Source: SourceKg, ID: "kg__42807", Name: "joox本地热歌榜", RankID: "42807"},
	{Source: SourceKg, ID: "kg__36107", Name: "小语种热歌榜", RankID: "36107"},
	{Source: SourceKg, ID: "kg__4673", Name: "日本公信榜", RankID: "4673"},
	{Source: SourceKg, ID: "kg__46868", Name: "日本SPACE SHOWER榜", RankID: "46868"},
	{Source: SourceKg, ID: "kg__42808", Name: "KKBOX风云榜", RankID: "42808"},
	{Source: SourceKg, ID: "kg__60171", Name: "越南语榜", RankID: "60171"},
	{Source: SourceKg, ID: "kg__60172", Name: "泰语榜", RankID: "60172"},
	{Source: SourceKg, ID: "kg__59895", Name: "R&B榜", RankID: "59895"},
	{Source: SourceKg, ID: "kg__59896", Name: "摇滚榜", RankID: "59896"},
	{Source: SourceKg, ID: "kg__59897", Name: "爵士榜", RankID: "59897"},
	{Source: SourceKg, ID: "kg__59898", Name: "乡村音乐榜", RankID: "59898"},
	{Source: SourceKg, ID: "kg__59900", Name: "纯音乐榜", RankID: "59900"},
	{Source: SourceKg, ID: "kg__59899", Name: "古典榜", RankID: "59899"},
	{Source: SourceKg, ID: "kg__22603", Name: "5sing音乐榜", RankID: "22603"},
	{Source: SourceKg, ID: "kg__21335", Name: "繁星音乐榜", RankID: "21335"},
	{Source: SourceKg, ID: "kg__33161", Name: "古风新歌榜", RankID: "33161"},
}

// RankBoards 返回某音源的内置榜单清单；source 空或未知返回 nil。
func RankBoards(source string) []RankBoardInfo {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SourceKw:
		return append([]RankBoardInfo(nil), kwRankBoardTable...)
	case SourceWy:
		return append([]RankBoardInfo(nil), wyRankBoardTable...)
	case SourceTx:
		return append([]RankBoardInfo(nil), txRankBoardTable...)
	case SourceKg:
		return append([]RankBoardInfo(nil), kgRankBoardTable...)
	default:
		return nil
	}
}

// RankSources 有榜单的音源 id 列表（固定顺序 kw/wy/tx/kg）。
func RankSources() []string { return []string{SourceKw, SourceWy, SourceTx, SourceKg} }

// ResolveRankBoard 用「中文榜名」或「平台 id / rankid」在表里查一条榜单。
// 匹配顺序：Name → ID → RankID。查不到返回 false。
func ResolveRankBoard(source, nameOrID string) (RankBoardInfo, bool) {
	key := strings.TrimSpace(nameOrID)
	if key == "" {
		return RankBoardInfo{}, false
	}
	for _, b := range RankBoards(source) {
		if b.Name == key {
			return b, true
		}
	}
	for _, b := range RankBoards(source) {
		if b.ID == key || b.RankID == key {
			return b, true
		}
	}
	return RankBoardInfo{}, false
}

// RankingUnavailableError 「该音源没有排行榜」的错误。
func RankingUnavailableError(source string) error {
	return fmt.Errorf("qingmusic: %s 没有排行榜 —— 支持 kw/wy/tx/kg 四家", source)
}

// FetchRankSongs 按音源分发取榜单 —— 四平台统一入口。
// rankID 语义随音源变（kg=rankid、kw=bangId、wy=歌单id、tx=topid），均可传中文榜名。
// page 只对 kg/kw 有效；wy/tx 接口没有翻页参数，传了也会被忽略。
func FetchRankSongs(ctx context.Context, source, rankID string, page int) (*RankBoard, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SourceKg:
		if b, ok := ResolveRankBoard(SourceKg, rankID); ok {
			rankID = b.RankID
		}
		return KgRankSongs(ctx, rankID, page)
	case SourceKw:
		return KwRankSongs(ctx, rankID, page)
	case SourceWy:
		return WyRankSongs(ctx, rankID, nil)
	case SourceTx:
		return TxRankSongs(ctx, rankID, nil)
	default:
		return nil, RankingUnavailableError(source)
	}
}

// ---------------------------------------------------------------- 酷狗

// KgRankSongs 取酷狗榜单，page 从 1 开始（每页固定 100，写在 URL 里）。
func KgRankSongs(ctx context.Context, rankid string, page int) (*RankBoard, error) {
	if page < 1 {
		page = 1
	}
	rankid = strings.TrimSpace(rankid)
	if rankid == "" {
		return nil, fmt.Errorf("qingmusic: 酷狗榜单 rankid 不能为空")
	}

	client := NewMVClient()
	rawURL := fmt.Sprintf("%s%d&rankid=%s&with_res_tag=0&show_portrait_mv=1",
		KgRankURL, page, rankid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultMobileUA)
	resp, err := client.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 酷狗榜单请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}

	var parsed kgRankResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: 酷狗榜单响应解析失败: %w", err)
	}
	if parsed.ErrCode != 0 {
		return nil, fmt.Errorf("qingmusic: Kg API Error: %d", parsed.ErrCode)
	}

	list := make([]RankSong, 0, len(parsed.Data.Info))
	for _, it := range parsed.Data.Info {
		list = append(list, NormalizeKgRankSong(it))
	}
	board := &RankBoard{
		Code: 200, Name: "酷狗榜单", MusicList: list,
		Total: parsed.Data.Total, UpdateTime: formatUnixTime(parsed.Data.Timestamp),
	}
	if len(list) > 0 {
		board.Pic = list[0].Pic // 取第一首的封面当榜单封面
	}
	return board, nil
}

// kgRankResponse 酷狗榜单的原始响应（顶层字段：data/errcode/status/error）。
type kgRankResponse struct {
	ErrCode int    `json:"errcode"`
	Status  int    `json:"status"`
	Error   string `json:"error"`
	Data    struct {
		Timestamp int64            `json:"timestamp"`
		Total     int64            `json:"total"`
		Info      []map[string]any `json:"info"`
	} `json:"data"`
}

// NormalizeKgRankSong 榜单歌曲归一化。
// 音质三档按「字段 > 0」逐个塞（128k/320k/flac）；rid 就是 hash。
// 歌手走 singername → authors[0] → filename 拆分 三级兜底（实测响应里没有 singername，
// 实际永远走 filename 拆分，与 App 一致）。
func NormalizeKgRankSong(item map[string]any) RankSong {
	types := make([]RankQuality, 0, 3)
	typesMap := make(map[string]RankQuality, 3)
	addQuality := func(name, sizeKey, hashKey string) {
		size, ok := anyInt64OK(item[sizeKey])
		if !ok || size <= 0 {
			return
		}
		q := RankQuality{Type: name, Size: FormatFileSize(size), Hash: anyString(item[hashKey])}
		types = append(types, q)
		typesMap[name] = q
	}
	addQuality("128k", "filesize", "hash")
	addQuality("320k", "320filesize", "320hash")
	addQuality("flac", "sqfilesize", "sqhash")

	artist := strings.TrimSpace(anyString(item["singername"]))
	if artist == "" {
		artist = firstAuthorName(item["authors"])
	}
	name := strings.TrimSpace(anyString(item["songname"]))
	if artist == "" {
		// filename 形如「歌手 - 歌名」，歌名里再有 ' - ' 则拼回。
		if fn := anyString(item["filename"]); fn != "" {
			if parts := strings.Split(fn, " - "); len(parts) >= 2 {
				artist = strings.TrimSpace(parts[0])
				if name == "" {
					name = strings.TrimSpace(strings.Join(parts[1:], " - "))
				}
			}
		}
	}

	pic := firstNonEmpty(
		anyString(item["album_sizable_cover"]),
		anyString(item["album_img"]),
		anyString(item["imgurl"]),
	)
	if pic != "" {
		pic = strings.ReplaceAll(pic, "{size}", "400")
		if strings.HasPrefix(pic, "http:") {
			pic = "https:" + pic[len("http:"):]
		}
	}

	hash := anyString(item["hash"])
	return RankSong{
		Rid: hash, Name: name, Artist: artist, Pic: pic,
		Album:    firstNonEmpty(anyString(item["remark"]), anyString(item["album_name"])),
		Duration: anyInt64(item["duration"]), // 秒
		Source:   SourceKg, Hash: hash, Types: types, TypesMap: typesMap,
		Sort: anyInt64(item["sort"]),
	}
}

// firstAuthorName 从酷狗的 authors[] 里取第一个歌手名。
func firstAuthorName(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	if m, ok := arr[0].(map[string]any); ok {
		return strings.TrimSpace(anyString(m["author_name"]))
	}
	return ""
}

// FormatFileSize 字节 → 人类可读。
func FormatFileSize(n int64) string {
	const unit = 1024
	switch {
	case n < unit:
		return strconv.FormatInt(n, 10) + "B"
	case n < unit*unit:
		return strconv.FormatFloat(float64(n)/unit, 'f', -1, 64) + "KB"
	case n < unit*unit*unit:
		return strconv.FormatFloat(float64(n)/(unit*unit), 'f', 2, 64) + "MB"
	default:
		return strconv.FormatFloat(float64(n)/(unit*unit*unit), 'f', 2, 64) + "GB"
	}
}

// formatUnixTime 秒级时间戳 → "2006-01-02 15:04:05"。
func formatUnixTime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

// ---------------------------------------------------------------- 酷我

// KwRankSongs 取酷我榜单。bangID 可传 bangId 或中文榜名；每页 30，可翻页。
func KwRankSongs(ctx context.Context, bangID string, page int) (*RankBoard, error) {
	if page < 1 {
		page = 1
	}
	id := strings.TrimSpace(bangID)
	label := id
	if b, ok := ResolveRankBoard(SourceKw, id); ok {
		id, label = b.RankID, b.Name
	}
	if id == "" {
		return nil, fmt.Errorf("qingmusic: 酷我榜单 bangId 不能为空")
	}

	client := NewMVClient()
	client.HTTP.Timeout = 15e9 // dex 里是 10s connect + 10s read，这里给 15s 总超时
	rawURL := KwRankURLFor(id, page, KwRankPageSize)
	body, err := client.getBodyUA(ctx, rawURL, KwRankUA)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 酷我排行榜调用失败: %w", err)
	}
	return ParseKwRankResponse(body, label)
}

// KwRankURLFor 构造酷我榜单 URL。
func KwRankURLFor(bangID string, page, size int) string {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = KwRankPageSize
	}
	return fmt.Sprintf(KwRankURL, url.QueryEscape(strings.TrimSpace(bangID)), page, size)
}

// ParseKwRankResponse 解析酷我榜单响应。
// ⚠️ data.num 实测是字符串 "300" 而非数字 —— data 解成 map 再宽松取，两种类型都能吃。
func ParseKwRankResponse(body []byte, label string) (*RankBoard, error) {
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data map[string]any
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: 酷我榜单响应解析失败: %w", err)
	}
	if parsed.Code != 200 {
		msg := parsed.Msg
		if msg == "" {
			msg = "未知错误"
		}
		return nil, fmt.Errorf("qingmusic: 酷我API返回错误: %s", msg)
	}
	rawList, _ := parsed.Data["musicList"].([]any)
	if _, has := parsed.Data["musicList"]; !has {
		return nil, fmt.Errorf("qingmusic: 酷我榜单响应数据结构错误（缺 data.musicList）")
	}
	list := make([]RankSong, 0, len(rawList))
	for _, e := range rawList {
		it, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if s, keep := NormalizeKwRankSong(it); keep {
			list = append(list, s)
		}
	}
	return &RankBoard{
		Code: 200, Name: firstNonEmpty(label, "酷我榜单"),
		Pic:        anyString(parsed.Data["img"]),
		MusicList:  list,
		Total:      anyInt64(parsed.Data["num"]), // "300" → 300
		UpdateTime: anyString(parsed.Data["pub"]),
	}, nil
}

// NormalizeKwRankSong 酷我榜单单曲归一化。
// rid 抠不出数字就整条丢弃（App 行为）；时长是格式化串 songTimeMinutes。
func NormalizeKwRankSong(item map[string]any) (RankSong, bool) {
	rid := digitsOnly(anyString(item["rid"]))
	if rid == "" {
		return RankSong{}, false
	}
	vid := "没有vid"
	if mvpay, ok := item["mvpayinfo"].(map[string]any); ok {
		if v := anyInt64(mvpay["vid"]); v != 0 {
			vid = strconv.FormatInt(v, 10)
		}
	}
	pic := anyString(item["pic"])
	if strings.HasPrefix(pic, "http://") {
		pic = "https://" + strings.TrimPrefix(pic, "http://")
	}
	return RankSong{
		Rid: rid, Vid: vid,
		Name:     anyString(item["name"]),
		Artist:   anyString(item["artist"]),
		Album:    anyString(item["album"]),
		Pic:      pic,
		Interval: anyString(item["songTimeMinutes"]), // "03:19" 格式串，不是秒数
		Time:     anyString(item["releaseDate"]),
		Source:   SourceKw,
	}, true
}

// ---------------------------------------------------------------- 网易云

// WyRankOption 网易云榜单可选项（App 硬编码的 MUSIC_U cookie 不内置，公开榜单不需要）。
type WyRankOption struct {
	Cookie    string
	ClientIP  string
	MaxTracks int
}

// WyRankSongs 取网易云榜单。playlistID 可传歌单 id 或中文榜名。
// wy 没有翻页：一次请求把 n 条全拿回来（默认 n=300）。
func WyRankSongs(ctx context.Context, playlistID string, opts *WyRankOption) (*RankBoard, error) {
	id := strings.TrimSpace(playlistID)
	if b, ok := ResolveRankBoard(SourceWy, id); ok {
		id = b.RankID
	}
	if id == "" {
		return nil, fmt.Errorf("qingmusic: 网易云榜单 id 不能为空")
	}
	o := WyRankOption{}
	if opts != nil {
		o = *opts
	}
	if o.MaxTracks <= 0 {
		o.MaxTracks = WyRankMaxTracks
	}
	headers := map[string]string{}
	if o.ClientIP != "" {
		headers["X-FORWARDED-FOR"] = o.ClientIP
		headers["CLIENT-IP"] = o.ClientIP
	}
	if o.Cookie != "" {
		headers["cookie"] = o.Cookie
	}

	client := NewMVClient()
	client.HTTP.Timeout = 20e9
	raw, err := client.postBody(ctx, WyRankURL, "application/x-www-form-urlencoded",
		WyRankBodyFor(id, o.MaxTracks), WyRankUA, headers)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 网易云排行榜调用失败: %w", err)
	}

	var parsed struct {
		Code     int `json:"code"`
		Playlist struct {
			Tracks          []map[string]any `json:"tracks"`
			CoverImgURL     string           `json:"coverImgUrl"`
			TrackCount      int64            `json:"trackCount"`
			TrackUpdateTime int64            `json:"trackUpdateTime"`
			Name            string           `json:"name"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: 网易云榜单响应解析失败: %w", err)
	}
	if parsed.Playlist.Tracks == nil {
		return nil, fmt.Errorf("qingmusic: 网易云榜单响应数据结构错误（缺 playlist.tracks）")
	}
	list := make([]RankSong, 0, len(parsed.Playlist.Tracks))
	for _, it := range parsed.Playlist.Tracks {
		if s, keep := NormalizeWyRankSong(it); keep {
			list = append(list, s)
		}
	}
	return &RankBoard{
		Code: 200, Name: firstNonEmpty(parsed.Playlist.Name, "网易云榜单"),
		Pic: parsed.Playlist.CoverImgURL, MusicList: list,
		Total:      parsed.Playlist.TrackCount,
		UpdateTime: formatWyDate(parsed.Playlist.TrackUpdateTime),
	}, nil
}

// WyRankBodyFor 构造网易云榜单请求体。
func WyRankBodyFor(playlistID string, maxTracks int) string {
	if maxTracks <= 0 {
		maxTracks = WyRankMaxTracks
	}
	return fmt.Sprintf(WyRankBodyFormat, url.QueryEscape(strings.TrimSpace(playlistID)), maxTracks)
}

// NormalizeWyRankSong 网易云榜单单曲归一化。id 转不动就丢；时长存 "4分13秒" 中文格式。
func NormalizeWyRankSong(track map[string]any) (RankSong, bool) {
	rawID := anyString(track["id"])
	id := digitsOnly(rawID)
	if id == "" {
		return RankSong{}, false
	}
	vid := "没有vid"
	if mv := anyInt64(track["mv"]); mv > 0 {
		vid = strconv.FormatInt(mv, 10)
	}
	var albumName, albumPic string
	if al, ok := track["al"].(map[string]any); ok {
		albumName = anyString(al["name"])
		albumPic = anyString(al["picUrl"])
	}
	return RankSong{
		Rid: id, Vid: vid,
		Name:     anyString(track["name"]),
		Artist:   joinNamesSep(track["ar"], "、"),
		Album:    albumName,
		Pic:      albumPic,
		Interval: FormatCNDuration(anyInt64(track["dt"]) / 1000),
		Time:     formatWyDate(anyInt64(track["publishTime"])),
		Source:   SourceWy,
	}, true
}

// FormatCNDuration 秒 → "4分13秒"；<=0 返回 "0分0秒"。
func FormatCNDuration(seconds int64) string {
	if seconds <= 0 {
		return "0分0秒"
	}
	return strconv.FormatInt(seconds/60, 10) + "分" +
		strconv.FormatInt(seconds%60, 10) + "秒"
}

// formatWyDate 毫秒时间戳 → "2006-01-02"。
func formatWyDate(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

// ---------------------------------------------------------------- QQ 音乐

// TxRankOption QQ 音乐榜单可选项。Period 传空串表示「最新一期」。
type TxRankOption struct {
	Num    int
	Period string
	Comm   *TxRankComm
}

// TxRankComm #7456 里写死的 comm 块。
type TxRankComm struct {
	Uin    int    `json:"uin"`
	Format string `json:"format"`
	Ct     int    `json:"ct"`
	Cv     int    `json:"cv"`
}

// DefaultTxRankComm 返回固定 comm 值。
func DefaultTxRankComm() TxRankComm { return TxRankComm{Uin: 0, Format: "json", Ct: 20, Cv: 1859} }

// TxRankSongs 取 QQ 音乐榜单。topid 可传数字或中文榜名（无翻页，一次 num 条）。
func TxRankSongs(ctx context.Context, topid string, opts *TxRankOption) (*RankBoard, error) {
	id := strings.TrimSpace(topid)
	label := id
	if b, ok := ResolveRankBoard(SourceTx, id); ok {
		id, label = b.RankID, b.Name
	}
	if id == "" {
		return nil, fmt.Errorf("qingmusic: QQ音乐榜单 topid 不能为空")
	}
	o := TxRankOption{}
	if opts != nil {
		o = *opts
	}
	if o.Num <= 0 {
		o.Num = TxRankDefaultNum
	}
	comm := DefaultTxRankComm()
	if o.Comm != nil {
		comm = *o.Comm
	}
	rawBody, err := TxRankPayload(id, o.Num, o.Period, comm)
	if err != nil {
		return nil, err
	}

	client := NewMVClient()
	client.HTTP.Timeout = 20e9
	raw, err := client.postBody(ctx, TxRankURL, "application/json", string(rawBody), TxRankUA, nil)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: QQ排行榜调用失败: %w", err)
	}

	var parsed struct {
		Code    int `json:"code"`
		Toplist struct {
			Code int `json:"code"`
			Data struct {
				SongInfoList []map[string]any `json:"songInfoList"`
				Title        string           `json:"title"`
				UpdateTime   int64            `json:"updateTime"`
				Period       string           `json:"period"`
			} `json:"data"`
		} `json:"toplist"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("qingmusic: QQ榜单响应解析失败: %w", err)
	}
	if parsed.Toplist.Code != 0 {
		return nil, fmt.Errorf("qingmusic: QQ榜单 toplist.code=%d", parsed.Toplist.Code)
	}
	list := make([]RankSong, 0, len(parsed.Toplist.Data.SongInfoList))
	for _, it := range parsed.Toplist.Data.SongInfoList {
		if s, keep := NormalizeTxRankSong(it); keep {
			list = append(list, s)
		}
	}
	board := &RankBoard{
		Code: 200, Name: firstNonEmpty(label, "TX排行榜"), MusicList: list,
		Total: int64(len(list)), UpdateTime: parsed.Toplist.Data.Period,
	}
	if len(list) > 0 {
		board.Pic = list[0].Pic
	}
	return board, nil
}

// TxRankPayload 构造 QQ 音乐榜单的 JSON 请求体。
func TxRankPayload(topid string, num int, period string, comm TxRankComm) ([]byte, error) {
	n, err := strconv.Atoi(strings.TrimSpace(topid))
	if err != nil {
		return nil, fmt.Errorf("qingmusic: QQ音乐榜单 topid 必须是数字，得到 %q", topid)
	}
	if num <= 0 {
		num = TxRankDefaultNum
	}
	return json.Marshal(map[string]any{
		"toplist": map[string]any{
			"module": TxRankModule,
			"method": TxRankMethod,
			"param":  map[string]any{"topid": n, "num": num, "period": period},
		},
		"comm": comm,
	})
}

// NormalizeTxRankSong QQ 音乐榜单单曲归一化。
// 歌名取 title；歌手用 " & " 拼接；封面由 album.mid 现拼（300 尺寸）；Duration=秒。
func NormalizeTxRankSong(item map[string]any) (RankSong, bool) {
	rid := firstNonEmpty(anyString(item["mid"]), anyString(item["id"]))
	if rid == "" {
		return RankSong{}, false
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
	return RankSong{
		Rid:      rid,
		Name:     anyString(item["title"]),
		Artist:   joinNamesSep(item["singer"], " & "),
		Album:    albumName,
		Pic:      pic,
		Duration: anyInt64(item["interval"]), // 秒
		Source:   SourceTx,
	}, true
}

// ---------------------------------------------------------------- 小工具

// joinNamesSep 把 [{name},…] 歌手数组按分隔符拼接。
// 各平台分隔符不同（字节码坐实）：wy 用 "、"，tx 用 " & "；kw 直接取服务端字符串。
func joinNamesSep(v any, sep string) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, it := range arr {
		o, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if n := strings.TrimSpace(anyString(o["name"])); n != "" {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, sep)
}
