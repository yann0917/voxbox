package qingmusic

// MV 模块（自 qingmusic-go 移植）：走第三方解析服务 music.haitangw.cc，
// 纯 HTTP + JSON，不需要任何加密/签名。
//
// 实测确认的关键事实（别想当然改）：
//
//  1. 播放地址响应是「画质名 → URL」的扁平对象（{"标清画质":"http://…"}），
//     画质名由服务端返回无法枚举，必须遍历 map。
//  2. 热门 MV 用 message 报错，歌单用 msg，两者不一致。
//  3. 热门 MV 与 MV 搜索是两套响应形状 + 两套字段 + 两套归一化：
//     热门是 {code,data:{list}} 且 duration 用秒；搜索是裸数组且 duration 用毫秒。
//  4. 线路（line）是另一套音源命名（小绿线路=tx QQ），接口按线路名 switch，
//     认不出的线路落 default（=小绿/QQ），与 App 一致。

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

// 线路别名：App 里这套名字与音源 id 并行存在。
const (
	LineKw = "小窝线路" // kw 酷我
	LineKg = "小狗线路" // kg 酷狗
	LineWy = "小云线路" // wy 网易云
	LineTx = "小绿线路" // tx QQ 音乐（注意是「小绿」）
	LineMg = "小粉线路" // mg 咪咕（命名类推，接口无独立分支，请求会落 QQ 线）
)

// MV 相关端点。
const (
	// MVServiceBase MV/歌单/榜单共用的第三方服务基址。
	MVServiceBase = "https://music.haitangw.cc"

	// HotMVPath 热门 MV 端点，响应 {code,message,data:{list[...]}}。
	HotMVPath = "/MV/qqmv_new_hot.php"

	// HotMVDefaultSize 热门 MV 的默认每页条数。
	HotMVDefaultSize = 15

	// HomeRequestTimeoutMS 首页请求超时（App 的 HOME_REQUEST_TIMEOUT_MS）。
	HomeRequestTimeoutMS = 5000

	// MVLookupSize Lookup 单次请求条数，实测服务端上限就是 100。
	MVLookupSize = 100

	// MVLookupPages Lookup 默认扫几页。
	MVLookupPages = 1
)

// LineNameOf 音源 id → 线路名；未知音源返回空串。
func LineNameOf(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SourceKw:
		return LineKw
	case SourceKg:
		return LineKg
	case SourceWy:
		return LineWy
	case SourceTx:
		return LineTx
	case SourceMg:
		return LineMg
	default:
		return ""
	}
}

// SourceOfLine 线路名 → 音源 id（LineNameOf 的反查）；认不出返回空串，不猜。
func SourceOfLine(line string) string {
	switch strings.TrimSpace(line) {
	case LineKw:
		return SourceKw
	case LineKg:
		return SourceKg
	case LineWy:
		return SourceWy
	case LineTx:
		return SourceTx
	case LineMg:
		return SourceMg
	default:
		return ""
	}
}

// ---------------------------------------------------------------- URL 构造

// mvURLByLine 按线路拼 MV 端点（#16091/#16092 两个 switch 的合并形态）。
// value 转义后接在 path 后，suffix 追加在末位；default 分支 = 小绿(QQ)。
func mvURLByLine(line, kgPath, kwPath, wyPath, qqPath, value, suffix string) string {
	var base string
	switch line {
	case LineKg:
		base = kgPath
	case LineWy:
		base = wyPath
	case LineKw:
		base = kwPath
	default:
		base = qqPath
	}
	return base + url.QueryEscape(value) + suffix
}

// MVInfoURL MV 清晰度信息 URL，响应形如 {"标清画质":{"size":"10.44MB"},…}。
func MVInfoURL(line, id string) string {
	return mvURLByLine(line,
		MVServiceBase+"/MV/KGMV.php?id=",
		MVServiceBase+"/MV/KWMV.php?id=",
		MVServiceBase+"/MV/WYMV.php?id=",
		MVServiceBase+"/qq/QQMV.php?id=",
		strings.TrimSpace(id), "&action=info")
}

// MVPlaybackURL MV 播放地址 URL。小绿(QQ) 线不带 quality 参数（App 实际行为）。
func MVPlaybackURL(line, id, quality string) string {
	suffix := ""
	if q := strings.TrimSpace(quality); q != "" && line != LineTx {
		suffix = "&quality=" + url.QueryEscape(q)
	}
	return mvURLByLine(line,
		MVServiceBase+"/MV/KGMV.php?id=",
		MVServiceBase+"/MV/KWMV.php?id=",
		MVServiceBase+"/MV/WYMV.php?id=",
		MVServiceBase+"/qq/QQMV.php?id=",
		strings.TrimSpace(id), suffix)
}

// MVSearchURL 按名搜索 MV。分页参数 page 是真的：漏了翻页会永远拿第一页。
func MVSearchURL(line, name string, page int) string {
	if page < 1 {
		page = 1
	}
	return mvURLByLine(line,
		MVServiceBase+"/MV/KGMV.php?name=",
		MVServiceBase+"/MV/KWMV.php?name=",
		MVServiceBase+"/MV/WYMV.php?name=",
		MVServiceBase+"/qq/QQMV.php?name=",
		strings.TrimSpace(name), fmt.Sprintf("&page=%d", page))
}

// HotMVURL 热门 MV URL，page/size 都做 App 同款归一化。
func HotMVURL(page, size int) string {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = HotMVDefaultSize
	}
	return fmt.Sprintf("%s%s?type=hot&page=%d&size=%d", MVServiceBase, HotMVPath, page, size)
}

// ---------------------------------------------------------------- 模型

// MVItem 一条 MV。Duration 是已格式化的 mm:ss 串（展示用）。
type MVItem struct {
	ID        string `json:"id"`        // vid
	Name      string `json:"name"`      // title || subtitle || "未知 MV"
	Artist    string `json:"artist"`    // singer || "未知歌手"
	Pic       string `json:"pic"`       // cover，http 强制升 https
	Duration  string `json:"duration"`  // duration_text，缺则用 duration 秒数格式化
	PlayCount int64  `json:"playcount"` // play_count，非数则 0
	Source    string `json:"source"`
	Line      string `json:"line"`
}

// MVQuality 一个可用画质。Name 是服务端返回的画质名（中文），Size 是展示用大小。
type MVQuality struct {
	Name string `json:"name"`
	Size string `json:"size"`
	URL  string `json:"url,omitempty"`
}

// ---------------------------------------------------------------- 归一化

// NormalizeHotMVItem 热门 MV 项归一化；ok=false 表示丢弃（仅 vid 为空才丢）。
func NormalizeHotMVItem(item map[string]any) (MVItem, bool) {
	vid := strings.TrimSpace(anyString(item["vid"]))
	if vid == "" {
		return MVItem{}, false
	}
	name := strings.TrimSpace(firstNonEmpty(anyString(item["title"]), anyString(item["subtitle"])))
	if name == "" {
		name = "未知 MV"
	}
	artist := strings.TrimSpace(anyString(item["singer"]))
	if artist == "" {
		artist = "未知歌手"
	}
	pic := strings.TrimSpace(anyString(item["cover"]))
	if strings.HasPrefix(strings.ToLower(pic), "http://") {
		pic = "https://" + pic[len("http://"):]
	}
	duration := strings.TrimSpace(anyString(item["duration_text"]))
	if duration == "" {
		duration = FormatDuration(item["duration"])
	}
	return MVItem{
		ID: vid, Name: name, Artist: artist, Pic: pic, Duration: duration,
		PlayCount: anyInt64(item["play_count"]),
		Source:    SourceTx, // App 写死 'qq'
		Line:      LineTx,
	}, true
}

// NormalizeMVSearchItem MV 搜索结果项归一化。
//
// ⚠️ 与 NormalizeHotMVItem 是两套规则：字段名不同（id/name/artist/cover/playcount），
// duration 是毫秒，封面不做 http→https 升级，id 不判空 —— App 没做的这里也不做。
func NormalizeMVSearchItem(item map[string]any, line string) MVItem {
	artist := anyString(item["artist"])
	if artist == "" {
		artist = "未知歌手"
	}
	return MVItem{
		ID:        anyString(item["id"]),
		Name:      anyString(item["name"]),
		Artist:    artist,
		Pic:       anyString(item["cover"]),
		Duration:  FormatDurationMS(item["duration"]),
		PlayCount: anyInt64(item["playcount"]),
		Source:    SourceOfLine(line),
		Line:      strings.TrimSpace(line),
	}
}

// FormatDuration 秒 → "mm:ss"（热门 MV/首页用）。
func FormatDuration(v any) string {
	sec := anyFloat(v)
	if sec < 0 || sec != sec { // NaN 归零
		sec = 0
	}
	t := int64(sec)
	return fmt.Sprintf("%02d:%02d", t/60, t%60)
}

// FormatDurationMS 毫秒 → "mm:ss"（MV 搜索页专用，别与 FormatDuration 混用）。
func FormatDurationMS(v any) string {
	ms := anyFloat(v)
	if ms == 0 { // 0/NaN/缺值 都落这里
		return "00:00"
	}
	t := int64(ms / 1000)
	return fmt.Sprintf("%02d:%02d", t/60, t%60)
}

// NormalizeMVVideoURL 播放地址严格校验：拒绝 null/HTML 错误页/控制字符，
// scheme 白名单 http/https/rtsp/file/content。解析服务出错时会把 HTML 错误页
// 或字符串 "null" 直接返回，不校验就会把 HTML 写进播放器。
func NormalizeMVVideoURL(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false
	}
	switch strings.ToLower(trimmed) {
	case "null", "undefined", "false":
		return "", false
	}
	low := strings.ToLower(trimmed)
	if strings.HasPrefix(low, "<") || strings.HasPrefix(low, "[") ||
		strings.HasPrefix(low, "<!doctype") {
		return "", false
	}
	for _, r := range trimmed {
		if r < 0x20 {
			return "", false
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "rtsp", "file", "content":
		return trimmed, true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------- 客户端

// MVClient MV/歌单/榜单共用的 music.haitangw.cc 客户端：
// 默认 5 秒超时，统一带移动端 UA。Base 可指向 httptest 服务器（测试）。
type MVClient struct {
	HTTP  *http.Client
	Base  string
	Agent string
}

// DefaultMobileUA iPhone UA，这类接口最不容易被拦的一个。
const DefaultMobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) " +
	"AppleWebKit/605.1.15 (KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1"

// NewMVClient 默认 5 秒超时（对齐 HOME_REQUEST_TIMEOUT_MS）。
func NewMVClient() *MVClient {
	return &MVClient{
		HTTP:  NewHTTPClient(HomeRequestTimeoutMS * time.Millisecond),
		Base:  MVServiceBase,
		Agent: DefaultMobileUA,
	}
}

// base 当前生效的服务基址（去尾斜杠；空 → MVServiceBase）。
func (c *MVClient) base() string {
	b := strings.TrimSpace(c.Base)
	if b == "" {
		return MVServiceBase
	}
	return strings.TrimSuffix(b, "/")
}

// abs 把端点构造函数产出的 URL 里的 MVServiceBase 前缀换成当前基址（仅测试时生效）。
func (c *MVClient) abs(rawURL string) string {
	b := c.base()
	if b == MVServiceBase {
		return rawURL
	}
	if rest, ok := strings.CutPrefix(rawURL, MVServiceBase); ok {
		return b + rest
	}
	return rawURL
}

// HotMVs 取热门 MV。响应必须是 {code:200, data:{list:[…]}}，否则报错。
func (c *MVClient) HotMVs(ctx context.Context, page, size int) ([]MVItem, error) {
	raw, err := c.getJSON(ctx, c.abs(HotMVURL(page, size)))
	if err != nil {
		return nil, err
	}
	code, _ := raw["code"].(float64)
	data, _ := raw["data"].(map[string]any)
	list, _ := data["list"].([]any)
	if int(code) != 200 || list == nil {
		msg := anyString(raw["message"])
		if msg == "" {
			msg = "热门 MV 数据格式异常"
		}
		return nil, fmt.Errorf("qingmusic: %s", msg)
	}
	out := make([]MVItem, 0, len(list))
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if it, keep := NormalizeHotMVItem(m); keep {
			out = append(out, it)
		}
	}
	return out, nil
}

// SearchMVs 按名搜索 MV。响应是裸数组（无外壳）；结果按 vid 去重（翻页收敛用）。
func (c *MVClient) SearchMVs(ctx context.Context, line, keyword string, page int) ([]MVItem, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("qingmusic: MV 搜索关键词不能为空")
	}
	arr, err := c.getJSONArray(ctx, c.abs(MVSearchURL(line, keyword, page)))
	if err != nil {
		return nil, err
	}
	out := make([]MVItem, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		it := NormalizeMVSearchItem(m, line)
		key := "mv_" + it.ID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, it)
	}
	return out, nil
}

// PlaybackURLs 取某条 MV 的全部画质地址（画质名 → URL），按画质从低到高排序。
func (c *MVClient) PlaybackURLs(ctx context.Context, line, id string) ([]MVQuality, error) {
	raw, err := c.getJSON(ctx, c.abs(MVPlaybackURL(line, id, "")))
	if err != nil {
		return nil, err
	}
	out := make([]MVQuality, 0, len(raw))
	for name, v := range raw {
		switch val := v.(type) {
		case string:
			if u, ok := NormalizeMVVideoURL(val); ok {
				out = append(out, MVQuality{Name: name, URL: u})
			}
		case map[string]any:
			// 有些线路把画质包成 {url: "..."} 这类对象。
			if u, ok := NormalizeMVVideoURL(firstNonEmpty(anyString(val["url"]), anyString(val["link"]))); ok {
				out = append(out, MVQuality{Name: name, URL: u})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("qingmusic: 无法获取视频地址（线路 %s，id %s）", line, id)
	}
	sortQualities(out)
	return out, nil
}

// Qualities 取清晰度列表（&action=info），响应 {"标清画质":{"size":"10.44MB"},…}。
func (c *MVClient) Qualities(ctx context.Context, line, id string) ([]MVQuality, error) {
	raw, err := c.getJSON(ctx, c.abs(MVInfoURL(line, id)))
	if err != nil {
		return nil, err
	}
	out := make([]MVQuality, 0, len(raw))
	for name, v := range raw {
		q := MVQuality{Name: name}
		if m, ok := v.(map[string]any); ok {
			q.Size = anyString(m["size"])
		} else if s, ok := v.(string); ok {
			q.Size = s
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("qingmusic: 无法获取视频清晰度（线路 %s，id %s）", line, id)
	}
	sortQualities(out)
	return out, nil
}

// Lookup 按 vid 在热门 MV 榜里回查一条记录（best-effort）。
// MV 接口没有「按 id 取详情」的端点，想拿歌名/歌手只能回查列表页。
// ok=false 且 err=nil 表示榜里确实没有；err != nil 表示回查本身失败（勿混淆）。
func (c *MVClient) Lookup(ctx context.Context, id string, pages int) (MVItem, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MVItem{}, false, nil
	}
	if pages < 1 {
		pages = MVLookupPages
	}
	for p := 1; p <= pages; p++ {
		list, err := c.HotMVs(ctx, p, MVLookupSize)
		if err != nil {
			return MVItem{}, false, err
		}
		for _, it := range list {
			if it.ID == id {
				return it, true, nil
			}
		}
	}
	return MVItem{}, false, nil
}

// sortQualities 按画质权重升序排，权重相同再按名字排（次级键保证输出确定，
// 否则 map 遍历的随机顺序会让同名次画质顺序每次乱跳）。
func sortQualities(qs []MVQuality) {
	sort.SliceStable(qs, func(i, j int) bool {
		ri, rj := qualityRank(qs[i].Name), qualityRank(qs[j].Name)
		if ri != rj {
			return ri < rj
		}
		return qs[i].Name < qs[j].Name
	})
}

// qualityRank 画质名 → 排序权重。画质名是服务端返回的动态中文串，按关键词猜权重，
// 认不出的排最后（99），不会盖过蓝光/原画。
func qualityRank(name string) int {
	switch {
	case strings.Contains(name, "流畅"):
		return 5
	case strings.Contains(name, "标清"):
		return 10
	case strings.Contains(name, "高清"):
		return 20
	case strings.Contains(name, "超清"):
		return 30
	case strings.Contains(name, "蓝光"):
		return 40
	case strings.Contains(name, "原画"):
		return 50
	default:
		return 99
	}
}

// PickQuality 从可用画质里挑一个：want 精确→大小写不敏感匹配，都不中报错并列可用项；
// want 为空取权重最高的（同级比名字，结果不依赖输入顺序）。
func PickQuality(qs []MVQuality, want string) (MVQuality, error) {
	if len(qs) == 0 {
		return MVQuality{}, fmt.Errorf("qingmusic: 该 MV 没有可用的画质地址")
	}
	if want = strings.TrimSpace(want); want != "" {
		for _, q := range qs {
			if q.Name == want {
				return q, nil
			}
		}
		for _, q := range qs {
			if strings.EqualFold(q.Name, want) {
				return q, nil
			}
		}
		return MVQuality{}, fmt.Errorf("qingmusic: 该 MV 没有画质 %q（可用: %s）", want, strings.Join(QualityNames(qs), ", "))
	}
	best := qs[0]
	for _, q := range qs[1:] {
		rq, rb := qualityRank(q.Name), qualityRank(best.Name)
		if rq > rb || (rq == rb && q.Name > best.Name) {
			best = q
		}
	}
	return best, nil
}

// QualityNames 取画质名列表（报错/展示用）。
func QualityNames(qs []MVQuality) []string {
	out := make([]string, 0, len(qs))
	for _, q := range qs {
		out = append(out, q.Name)
	}
	return out
}

// getBody 发 GET 并返回响应体。
func (c *MVClient) getBody(ctx context.Context, rawURL string) ([]byte, error) {
	return c.getBodyUA(ctx, rawURL, c.Agent)
}

// getBodyUA 发 GET 并返回响应体，UA 由调用方指定（各平台 UA 不同）。
func (c *MVClient) getBodyUA(ctx context.Context, rawURL, ua string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 请求失败（%w）", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qingmusic: 请求失败（HTTP %d）", resp.StatusCode)
	}
	return body, nil
}

// postBody 发 POST 并返回响应体。headers 为 nil 时只带 UA。
func (c *MVClient) postBody(ctx context.Context, rawURL, contentType, body, ua string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 请求失败（%w）", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qingmusic: 请求失败（HTTP %d）", resp.StatusCode)
	}
	return raw, nil
}

// getJSON 发 GET 并解成 map。
func (c *MVClient) getJSON(ctx context.Context, rawURL string) (map[string]any, error) {
	body, err := c.getBody(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("qingmusic: 接口返回数据格式异常: %w", err)
	}
	return out, nil
}

// getJSONArray 发 GET 并解成数组（MV 搜索接口返回裸数组，没有 {code,data} 外壳）。
func (c *MVClient) getJSONArray(ctx context.Context, rawURL string) ([]any, error) {
	body, err := c.getBody(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	var out []any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("qingmusic: 接口返回数据格式异常: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------- 宽松转换小工具

// anyString JSON 值 → 字符串（酷我给字符串、网易云给数字，一律宽松转换）。
func anyString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(t)
	}
}

func anyFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func anyInt64(v any) int64 {
	f := anyFloat(v)
	if f != f { // NaN
		return 0
	}
	return int64(f)
}

// anyInt64OK 与 anyInt64 类似，但区分「字段缺失」和「字段就是 0」。
func anyInt64OK(v any) (int64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		if t != t {
			return 0, false
		}
		return int64(t), true
	case int:
		return int64(t), true
	case int64:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// nestedData 取顶层 data 对象，缺失时返回空 map。
func nestedData(raw map[string]any) map[string]any {
	if d, ok := raw["data"].(map[string]any); ok {
		return d
	}
	return map[string]any{}
}

// codeOf 取响应的 code，兼容数字与字符串两种形态。
func codeOf(raw map[string]any) int {
	if n, ok := anyInt64OK(raw["code"]); ok {
		return int(n)
	}
	if s, ok := raw["code"].(string); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n
		}
	}
	return 0
}

// firstAnyInt64 取第一个能转成整数的键（同一语义在不同分支下键名不同的别名链）。
func firstAnyInt64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if n, ok := anyInt64OK(m[k]); ok {
			return n
		}
	}
	return 0
}

// mustArray 把 nil / 非数组都当空数组。
func mustArray(v any) []any {
	arr, _ := v.([]any)
	return arr
}
