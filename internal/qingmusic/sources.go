package qingmusic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Searcher 直连各平台官方接口取 rid。
//
// 这一层对应 App 里的原生 Kotlin 模块（SecureKwApiModule），
// 因为搜索接口本身不做“加密”，只是带平台特定的 UA / 参数 / 签名。
//
// **实现按 searchApi 名字注册**，而不是写死 switch —— 对应 App 的
// 「配置里写函数名 → 查表拿到函数引用」（#10775）：
//
//	kw   → fetchSearchMusic   kg → kgSearchMusic   wy → wySearchMusic
//	tx   → txSearchMusic      mg → mgSearchMusic   bili → biliSearchMusic
//
// 好处是配置换了实现名就能换实现，也能让「配置启用了但没实现」的音源
// 有一套明确的报错语义（对应 App 的 #11849「搜索接口不可用」），
// 而不是静默漏掉。
type Searcher struct {
	HTTP *http.Client

	// Endpoints 覆盖分类搜索（专辑/歌手/歌单）的端点，key 见 search.go 的 ep* 常量。
	// 空 = 用内置默认；仅测试（httptest 顶替真实服务）会用到。
	Endpoints map[string]string

	// registry: searchApi 名字 → 实现。
	registry map[string]SearchFunc
}

// SearchFunc 一个音源搜索实现，与配置里的 searchApi 一一对应。page 从 1 开始。
type SearchFunc func(ctx context.Context, keyword string, page int) ([]Track, error)

// 内置的 searchApi 名字。与真实配置里写的完全一致，供注册与文档共用。
const (
	SearchAPIFetch = "fetchSearchMusic" // kw（通用名）
	SearchAPIKg    = "kgSearchMusic"
	SearchAPIWy    = "wySearchMusic"
	SearchAPITx    = "txSearchMusic"
	SearchAPIMg    = "mgSearchMusic"
	SearchAPIBili  = "biliSearchMusic" // 尚未实现
)

// 内置的 detailApi 名字（歌词 / 封面来源）。App 侧对应 #6896 fetchDetailBySource。
//
// Go 侧**只登记名字、不提供实现** —— 保持「配置能原样读进来」，
// 但真要用的时候会明确告诉你没实现，而不是返回空数据。
const (
	DetailAPIFetch = "fetchMusicDetail" // kw（通用名，也是 #6896 的 default 分支）
	DetailAPIKg    = "kgMusicDetail"
	DetailAPIWy    = "wyMusicDetail"
	DetailAPITx    = "txMusicDetail"
	DetailAPIMg    = "mgMusicDetail"
	DetailAPIBili  = "biliMusicDetail"
)

func NewSearcher() *Searcher {
	s := &Searcher{HTTP: NewHTTPClient(10 * time.Second)}
	s.registerBuiltins()
	return s
}

// registerBuiltins 注册已实现的音源。
func (s *Searcher) registerBuiltins() {
	s.Register(SearchAPIFetch, s.searchKw)
	s.Register(SearchAPIKg, s.searchKg)
	s.Register(SearchAPIWy, s.searchWy)
	s.Register(SearchAPITx, s.searchTx)
	s.Register(SearchAPIMg, s.searchMg)
}

// Register 挂一个 searchApi 实现。传 nil 表示注销（用于「知道这个 API 名但没实现」）。
func (s *Searcher) Register(name string, fn SearchFunc) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if s.registry == nil {
		s.registry = map[string]SearchFunc{}
	}
	if fn == nil {
		delete(s.registry, name)
		return
	}
	s.registry[name] = fn
}

// HasAPI 该 searchApi 名字是否已有实现。对应 App 的 ensureSearchApi 检查项。
func (s *Searcher) HasAPI(name string) bool {
	_, ok := s.registry[strings.TrimSpace(name)]
	return ok
}

// APIs 已实现的 searchApi 名字（排序后），便于打印与对拍。
func (s *Searcher) APIs() []string {
	out := make([]string, 0, len(s.registry))
	for k := range s.registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SearchByLine 按配置里那条音源声明实现搜索 —— 这是配置真正生效的入口。
//
// 与 App 一致的三件事：
//   - 音源在配置里 enabled=false → 不搜（对应「关闭的音源不会在搜索时显示」）
//   - searchApi 解析不到实现 → 返回「…搜索接口不可用」（#11849 的语义）
//   - 音源没配 searchApi → 同上
func (s *Searcher) SearchByLine(ctx context.Context, line SourceLine, keyword string, page int) ([]Track, error) {
	if !line.Enabled {
		return nil, fmt.Errorf("qingmusic: 音源 %s（%s）已关闭", line.ID, line.Name)
	}
	if !line.Searchable() {
		return nil, fmt.Errorf("qingmusic: %s搜索接口不可用（配置里没写 searchApi）", line.Name)
	}
	fn, ok := s.registry[line.SearchAPI]
	if !ok || fn == nil {
		return nil, fmt.Errorf("qingmusic: %s搜索接口不可用（%s 未实现）", line.Name, line.SearchAPI)
	}
	return fn(ctx, keyword, page)
}

// Search 按 source 分发。page 从 1 开始。
//
// 这里保留 source 入参是为了兼容旧调用方与 qm url/play 那类「已知 source + rid」
// 的场景；真正按配置走的是 SearchByLine。
func (s *Searcher) Search(ctx context.Context, source, keyword string, page int) ([]Track, error) {
	if page < 1 {
		page = 1
	}
	// source → 默认的 searchApi 名（与内置配置一致）。
	var api string
	switch source {
	case SourceKw:
		api = SearchAPIFetch
	case SourceKg:
		api = SearchAPIKg
	case SourceWy:
		api = SearchAPIWy
	case SourceTx:
		api = SearchAPITx
	case SourceMg:
		api = SearchAPIMg
	case SourceBili:
		api = SearchAPIBili
	default:
		return nil, fmt.Errorf("qingmusic: 未知音源 %q", source)
	}
	fn, ok := s.registry[api]
	if !ok || fn == nil {
		return nil, fmt.Errorf("qingmusic: 音源 %s 搜索接口不可用（%s 未实现）", source, api)
	}
	return fn(ctx, keyword, page)
}

func (s *Searcher) do(ctx context.Context, req *http.Request) ([]byte, error) {
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("qingmusic: %s HTTP %d: %s", req.URL.Host, res.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// doRetry 对同一个请求重试若干次。
//
// 网易云 eapi 存在偶发拒绝（实测短时间内连打多个平台后，
// 同一请求会返回一次 404 nginx 页，紧接着重试即 200），因此 wy 走这条路。
func (s *Searcher) doRetry(ctx context.Context, req *http.Request, attempts int) ([]byte, error) {
	if attempts < 1 {
		attempts = 1
	}
	var (
		body []byte
		err  error
	)
	for i := 0; i < attempts; i++ {
		clone := req.Clone(ctx)
		if req.GetBody != nil {
			b, berr := req.GetBody()
			if berr != nil {
				return nil, berr
			}
			clone.Body = b
		}
		body, err = s.do(ctx, clone)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(400*(i+1)) * time.Millisecond)
	}
	return nil, err
}

// ---------------------------------------------------------------- 酷我 (kw)

// kwSearchBase 与 App 一致是 http（App 内为 "http://search.kuwo.cn/r.s?client=kt&all="）。
// 这里用 https：同一个接口 https 同样可用，且规避了「纯 HTTP 连接复用被中间代理串线」的问题。
const kwSearchBase = "https://search.kuwo.cn/r.s?client=kt&all="

// kwSearchTail 原样取自 inner_searchMusic$1 的字符串常量。
const kwSearchTail = "&rn=30&uid=794762570&ver=kwplayer_ar_9.2.2.1&vipver=1&show_copyright_off=1" +
	"&newver=1&ft=music&cluster=0&strategy=2012&encoding=utf8&rformat=json&vermerge=1&mobi=1&issubtitle=1"

func (s *Searcher) searchKw(ctx context.Context, keyword string, page int) ([]Track, error) {
	u := kwSearchBase + url.QueryEscape(keyword) + "&pn=" + strconv.Itoa(page) + kwSearchTail
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uaWindows)

	body, err := s.do(ctx, req)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Show    string           `json:"SHOW"`
		Total   string           `json:"TOTAL"`
		AbsList []map[string]any `json:"abslist"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: kw 响应解析失败: %w", err)
	}

	tracks := make([]Track, 0, len(raw.AbsList))
	for _, it := range raw.AbsList {
		rid := digitsOnly(str(get(it, "MUSICRID"))) // "MUSIC_228908" → "228908"
		if rid == "" {
			continue
		}
		tracks = append(tracks, Track{
			Source:   SourceKw,
			Rid:      rid,
			Name:     str(get(it, "SONGNAME")),
			Artist:   strings.ReplaceAll(str(get(it, "ARTIST")), "&", "、"),
			Album:    str(get(it, "ALBUM")),
			Duration: secondsToMillis(str(get(it, "DURATION"))),
		})
	}
	return tracks, nil
}

// ---------------------------------------------------------------- 酷狗 (kg)

const kgSearchBase = "https://songsearch.kugou.com/song_search_v2?keyword="

// kgSearchTail 原样取自 inner_kgSearchMusic$1 的字符串常量。
const kgSearchTail = "&pagesize=30&userid=0&clientver=&platform=WebFilter&filter=2" +
	"&iscorrection=1&privilege_filter=0&area_code=1"

func (s *Searcher) searchKg(ctx context.Context, keyword string, page int) ([]Track, error) {
	u := kgSearchBase + url.QueryEscape(keyword) + "&page=" + strconv.Itoa(page) + kgSearchTail
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uaWindows)

	body, err := s.do(ctx, req)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Code int `json:"code"`
		Data struct {
			Lists []map[string]any `json:"lists"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: kg 响应解析失败: %w", err)
	}

	tracks := make([]Track, 0, len(raw.Data.Lists))
	for _, it := range raw.Data.Lists {
		// rid = FileHash 大写（对应 processKgSongDataFromApi）
		rid := strings.ToUpper(str(get(it, "FileHash")))
		if rid == "" {
			continue
		}
		tracks = append(tracks, Track{
			Source:   SourceKg,
			Rid:      rid,
			Name:     str(get(it, "SongName")),
			Artist:   str(get(it, "SingerName")),
			Album:    str(get(it, "AlbumName")),
			Duration: secondsToMillis(str(get(it, "Duration"))),
		})
	}
	return tracks, nil
}

// ---------------------------------------------------------------- 网易云 (wy)

// wyEapiEndpoint 取自 inner_wySearchMusic$1：App 固定打 /eapi/batch，
// 而签名里的逻辑路径是 /api/cloudsearch/pc。
//
// 实测 /eapi/batch、/eapi/cloudsearch/pc，以及 interface3 / music.163.com 各主机均可用。
// 这里用 https：同接口 https 同样可用，且规避「纯 HTTP 连接复用被中间代理串线」的问题。
const wyEapiEndpoint = "https://interface.music.163.com/eapi/batch"

// wySearchPayload 构造 eapi 的明文请求体。searchWy 与 RawSearch 共用。
//
// offset 的差异：App 里写的是 offset = page * 30（page 为 1 基），
// 也就是第 1 页会跳过前 30 条 —— 这是 App 的 off-by-one。
// 这里按正确分页实现 (page-1)*30；若要严格复刻 App，把下面改成 page*30 即可。
func wySearchPayload(keyword string, page int) string {
	b, _ := json.Marshal(map[string]any{
		"s":      keyword,
		"type":   1,
		"limit":  30,
		"total":  page <= 1,
		"offset": (page - 1) * 30,
	})
	return string(b)
}

func (s *Searcher) searchWy(ctx context.Context, keyword string, page int) ([]Track, error) {
	// 请求体字段与 App 一致：s / type / limit / total / offset。
	payload := wySearchPayload(keyword, page)

	form := url.Values{}
	form.Set("params", EapiEncrypt(eapiURL, payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wyEapiEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uaChromeLinux)
	req.Header.Set("Origin", "https://music.163.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := s.doRetry(ctx, req, 3)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Code   int `json:"code"`
		Result struct {
			Songs []map[string]any `json:"songs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: wy 响应解析失败: %w", err)
	}

	tracks := make([]Track, 0, len(raw.Result.Songs))
	for _, it := range raw.Result.Songs {
		// rid = 歌曲 id（App 里是 String.valueOf(optLong("id"))）
		rid := str(get(it, "id"))
		if rid == "" || rid == "0" {
			continue
		}
		tr := Track{
			Source: SourceWy,
			Rid:    rid,
			Name:   str(get(it, "name")),
			Artist: joinNames(getArr(it, "ar")),
			Album:  nestedStr(it, "al", "name"),
			Pic:    nestedStr(it, "al", "picUrl"),
		}
		if d, ok := num(get(it, "dt")); ok {
			tr.Duration = int64(d)
		}
		tracks = append(tracks, tr)
	}
	return tracks, nil
}

// ---------------------------------------------------------------- QQ 音乐 (tx)

const txSearchEndpoint = "https://u.y.qq.com/cgi-bin/musics.fcg?sign="

// BuildTxSearchRequestBody 复刻 buildTxSearchRequestBody(keyword, page, numPerPage)。
// page 传入的是 1 基页码。
func BuildTxSearchRequestBody(keyword string, page, numPerPage int) map[string]any {
	if page < 1 {
		page = 1
	}
	return map[string]any{
		"comm": map[string]any{
			"_channelid":                  "0",
			"_os_version":                 "6.2.9200-2",
			"ct":                          "19",
			"cv":                          "2151",
			"guid":                        "1F70E520B2EAA7D25E11760783C53CA9",
			"patch":                       "118",
			"psrf_access_token_expiresAt": 0,
			"psrf_qqaccess_token":         "",
			"psrf_qqopenid":               "",
			"psrf_qqunionid":              "",
			"tmeAppID":                    "qqmusic",
			"tmeLoginType":                0,
			"uin":                         "0",
			"wid":                         "7223299733393904640",
		},
		"music.search.SearchCgiService": map[string]any{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicDesktop",
			"param": map[string]any{
				"grp":          1,
				"num_per_page": numPerPage,
				"page_num":     page,
				"query":        keyword,
				"remoteplace":  "txt.newclient.top",
				"search_type":  0,
				"searchid":     TxSearchID(),
			},
		},
	}
}

func (s *Searcher) searchTx(ctx context.Context, keyword string, page int) ([]Track, error) {
	bodyObj := BuildTxSearchRequestBody(keyword, page, 30)
	payload, err := json.Marshal(bodyObj)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, txSearchEndpoint+TxSign(string(payload)), strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uaQQMusic)
	req.Header.Set("Content-Type", "application/json")

	body, err := s.do(ctx, req)
	if err != nil {
		return nil, err
	}

	// 响应结构（实测 2026-09）：
	//
	//	{ "code":0, "ts":..., "music.search.SearchCgiService": {
	//	    "code":0, "data": { "body": { "song": { "list": [ {...} ] } } } } }
	//
	// 顶层 key 就是 module 名，所以这里用 map 动态取，避免写死结构体。
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: tx 响应解析失败: %w", err)
	}

	svc, _ := raw["music.search.SearchCgiService"].(map[string]any)
	if svc == nil {
		return nil, fmt.Errorf("qingmusic: tx 响应缺少 music.search.SearchCgiService 节点（原始: %s）", truncate(string(body), 200))
	}
	data, _ := svc["data"].(map[string]any)
	innerBody, _ := data["body"].(map[string]any)
	songObj, _ := innerBody["song"].(map[string]any)
	list := toMapSlice(songObj["list"])

	tracks := make([]Track, 0, len(list))
	for _, it := range list {
		// rid = 歌曲顶层 mid（实测 "001Bbywq2gicae"）。
		// 注意：file 节点下只有 media_mid，没有 mid；顶层 mid 才是 file.mid 的等价物。
		rid := str(get(it, "mid"))
		if rid == "" {
			rid = nestedStr(it, "file", "media_mid")
		}
		if rid == "" {
			continue
		}
		tr := Track{
			Source: SourceTx,
			Rid:    rid,
			Name:   str(get(it, "name")),
			Artist: joinNames(getArr(it, "singer")),
			Album:  nestedStr(it, "album", "name"),
		}
		if d, ok := num(get(it, "interval")); ok {
			tr.Duration = int64(d) * 1000
		}
		tracks = append(tracks, tr)
	}
	return tracks, nil
}

// ---------------------------------------------------------------- 咪咕 (mg)

// 咪咕搜索有两个等价的官方网关，schema 完全一致：
//
//	mgSearchBase（App v1.3.9 使用的 jadeite 域名）——实测 2026-09 起服务端收紧，
//	   带 sid=USS 必返 {"code":"411","info":"参数异常"}，不带 sid 也拿不到数据，
//	   即 App 原样请求已失效（非本地实现问题）。
//	mgSearchBaseAlt（MIGUM2.0 老网关）——同一套 search_all.do 参数，实测正常返回
//	   code=000000 与完整 songResultData，因此作为实际使用的端点。
const (
	mgSearchBase    = "https://jadeite.migu.cn/music_search/v3/search/searchAll"
	mgSearchBaseAlt = "https://app.c.nf.migu.cn/MIGUM2.0/v1.0/content/search_all.do"
)

// mgSearchSwitch 原样取自 inner_mgSearchMusic$1。
const mgSearchSwitch = `{"song":1,"album":0,"singer":0,"tagSong":1,"mvSong":0,"bestShow":1,"songlist":0,"lyricSong":0}`

// mgSearchURL 拼搜索 URL。searchMg 与 RawSearch 共用，避免两份实现走偏。
func mgSearchURL(keyword string, page int) string {
	return mgSearchBaseAlt +
		"?isCorrect=0&isCopyright=1&searchSwitch=" + url.QueryEscape(mgSearchSwitch) +
		"&pageSize=30&text=" + url.QueryEscape(keyword) +
		"&pageNo=" + strconv.Itoa(page) +
		"&sort=0&sid=USS"
}

// mgSearchHeaders 返回咪咕搜索需要的请求头（含 createMgSignature 的产物）。
func mgSearchHeaders(keyword string) (map[string]string, string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	return map[string]string{
		"User-Agent": mgUA,
		"uiVersion":  mgUIVer,
		"timestamp":  ts,
		"channel":    mgChannel,
		"deviceId":   mgDeviceID,
		"sign":       MgSign(ts, keyword),
	}, ts
}

func (s *Searcher) searchMg(ctx context.Context, keyword string, page int) ([]Track, error) {
	headers, _ := mgSearchHeaders(keyword)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mgSearchURL(keyword, page), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	body, err := s.do(ctx, req)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Code           string `json:"code"`
		Info           string `json:"info"`
		SongResultData struct {
			ResultList []any `json:"resultList"`
		} `json:"songResultData"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("qingmusic: mg 响应解析失败: %w", err)
	}
	if raw.Code != "" && raw.Code != "000000" {
		return nil, fmt.Errorf("qingmusic: mg 接口返回 %s %s", raw.Code, raw.Info)
	}

	// resultList 是「数组的数组」：外层是按资源类型分组的桶，元素才是歌曲对象。
	items := flattenMapList(raw.SongResultData.ResultList)

	tracks := make([]Track, 0, len(items))
	for _, it := range items {
		// rid：App 的 jadeite 版本字段名是 songId，MIGUM2.0 版本是 id，两者都取。
		rid := str(get(it, "songId"))
		if rid == "" || rid == "0" {
			rid = str(get(it, "id"))
		}
		if rid == "" || rid == "0" {
			continue
		}
		tr := Track{
			Source: SourceMg,
			Rid:    rid,
			Name:   str(get(it, "name")),
		}
		// 歌手：jadeite 版是 singerList，MIGUM2.0 版是 singers。
		tr.Artist = joinNames(getArr(it, "singerList"))
		if tr.Artist == "" {
			tr.Artist = joinNames(getArr(it, "singers"))
		}
		// 专辑：MIGUM2.0 用 albums[0].name。
		if arr := getArr(it, "albums"); len(arr) > 0 {
			if o, ok := arr[0].(map[string]any); ok {
				tr.Album = str(o["name"])
			}
		}
		if tr.Album == "" {
			tr.Album = str(get(it, "album"))
		}
		if d, ok := num(get(it, "duration")); ok {
			tr.Duration = int64(d)
		}
		tracks = append(tracks, tr)
	}
	return tracks, nil
}

// flattenMapList 展开「数组的数组」结构：元素本身是对象就直接收，
// 元素是数组就逐项收（咪咕 resultList 属于后者）。
func flattenMapList(v []any) []map[string]any {
	out := make([]map[string]any, 0, len(v))
	for _, it := range v {
		switch t := it.(type) {
		case map[string]any:
			out = append(out, t)
		case []any:
			for _, sub := range t {
				if o, ok := sub.(map[string]any); ok {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- 解析辅助

func get(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func getArr(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

// toMapSlice 把 []any 收敛成 []map[string]any，跳过非对象元素。
func toMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if o, ok := it.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

// joinNames 把 [{"name":"A"},{"name":"B"}] 拼成 "A、B"。
func joinNames(arr []any) string {
	parts := make([]string, 0, len(arr))
	for _, it := range arr {
		if o, ok := it.(map[string]any); ok {
			if n := str(o["name"]); n != "" {
				parts = append(parts, n)
			}
		}
	}
	return strings.Join(parts, "、")
}

func nestedStr(m map[string]any, key, sub string) string {
	if o, ok := m[key].(map[string]any); ok {
		return str(o[sub])
	}
	return ""
}

// digitsOnly 保留数字字符（"MUSIC_228908" → "228908"）。
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// secondsToMillis 把 "245" 这类秒数字符串转毫秒。
func secondsToMillis(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f * 1000)
	}
	return 0
}
