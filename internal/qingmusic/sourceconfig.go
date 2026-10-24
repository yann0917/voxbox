package qingmusic

// 音源配置（App 里的 storage key = 'apiConfig'）。
//
// ------------------------------------------------------------------ 反编译事实
//
// 这份配置**不在 APK 里** —— assets/ 下只有 index.android.bundle，没有 music.json。
// 它在运行时由用户在「我的 → 导入音源配置」里粘贴一个 URL 导入，然后落到
// storage（AsyncStorage/MMKV）的 'apiConfig' 键。UI 文案可佐证：
//
//	'请输入音源配置URL地址' / '请粘贴音源网址' / '未找到音源配置'
//	'请先在"我的"->"导入音源配置"中导入音源配置文件'
//
// 读取入口有五个函数，读的都是同一份数据（字节码编号为 Hermes function id）：
//
//	#8529  readSourceLines       → 返回归一化后的 lines[]，异常时 [] + '读取音源音质配置失败:'
//	#9129  getImportedConfig     → 返回 {config} 解包后的对象，异常/空 → null
//	#10772 getConfigObject       → 同 #9129
//	#12874 loadConfig            → 归一化后回调出去，异常时 '加载音源配置失败:'
//	#16510 readImportedConfig    → 原样返回 JSON.parse 的结果（不解包）
//
// 解包规则（#9129 / #12874 一致）：`parsed.config || parsed`，
// 所以下面三种 JSON 形态 App 都认：
//
//	{"config": {"lines": [...]}}     官方推荐
//	{"lines": [...]}                 直接给（config 缺失 → 退回 parsed 自身）
//	[{...}, {...}]                   裸数组经 config 分支也能走到
//
// 归一化规则（#8531 / #10775，逐字段）：
//
//	id        = String(x.id).trim().toLowerCase()
//	name      = String(x.name || x.id)          // 缺 name 用 id 顶
//	enabled   = x.enabled !== false             // 缺省 true
//	levels    = Array.isArray(x.levels) ? x.levels.map(String).filter(Boolean) : []
//	searchApi = 查表(REGISTRY, String(x.searchApi).trim())   // 名字 → **函数引用**
//	detailApi = 查表(REGISTRY, String(x.detailApi).trim())
//
// searchApi / detailApi 是关键设计：配置里写的是**实现函数名**，App 在归一化时
// 把它查表解析成真正的函数，解析不到就置 undefined（#10775 的 TypeOf===‘function’ 分支）。
// 之后 #11849 ensureSearchApi 在真正要搜的时候检查，解析不到就抛
// `{音源名}搜索接口不可用`。
//
// 真实配置里的对应关系：
//
//	kw   → fetchSearchMusic  / fetchMusicDetail    ← 通用名，不是 kwSearchMusic
//	kg   → kgSearchMusic     / kgMusicDetail
//	wy   → wySearchMusic     / wyMusicDetail
//	tx   → txSearchMusic     / txMusicDetail
//	mg   → mgSearchMusic     / mgMusicDetail
//	bili → biliSearchMusic   / biliMusicDetail
//
// kw 用通用名这点有旁证：#6896 fetchDetailBySource 是硬编码的 source→实现分发，
// 它的 switch 把 kw 的实现放在 default 分支，其余五个各占一个 case。
//
// 音质决策（#8534 getStoredQualityForSource）：
//
//	首选档位在该音源 levels 里 → 用它
//	否则                     → 退到该音源支持的**最高档**（clampLevel）
//	levels 为空 / 没这条音源  → 保留首选档位不动
//
// 首选档位的来源：用户在该音源上显式选过 → 用设置值；没选过 → DefaultPreferredLevel
// （standard，128kbps；只有该音源没有 standard 时才退到它的最高档）。
//
// #8538 是同一套逻辑的「批量对账」版：遍历所有音源，把不再被支持的
// 已存档位改写成该音源支持的最高档 —— 对应本文件的 Reconcile。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 模型

// SourceLine 一个音源（音源配置里 lines[] 的一项）。
type SourceLine struct {
	ID      string   `json:"id"`      // 归一化后：trim + 小写，如 "kw"
	Name    string   `json:"name"`    // 展示名，缺省等于 ID
	Enabled bool     `json:"enabled"` // 缺省 true；false 的音源不参与搜索
	Levels  []string `json:"levels"`  // 该音源允许的音质档，空数组 = 不限制

	// SearchAPI / DetailAPI 是**实现函数名**，App 侧查表把它解析成函数引用。
	//
	// 真实配置长这样（#10775 归一化时做的正是「名字 → 函数」的查表）：
	//
	//	kw   → fetchSearchMusic  / fetchMusicDetail   ← 注意这两个是通用名
	//	kg   → kgSearchMusic     / kgMusicDetail
	//	wy   → wySearchMusic     / wyMusicDetail
	//	tx   → txSearchMusic     / txMusicDetail
	//	mg   → mgSearchMusic     / mgMusicDetail
	//	bili → biliSearchMusic   / biliMusicDetail
	//
	// kw 用的是通用名（不是 kwSearchMusic），这一点在字节码里有旁证：
	// #6896 fetchDetailBySource 的 switch 以 kw 的实现作 default 分支。
	// Go 侧不做函数引用，保留名字本身，由 Searcher 的注册表按名解析。
	SearchAPI string `json:"searchApi,omitempty"`
	DetailAPI string `json:"detailApi,omitempty"`
}

// Searchable 该音源是否有可用的搜索实现名。
// 对应 App 的 #11849 ensureSearchApi：解析不到就抛「{音源名}搜索接口不可用」。
func (l SourceLine) Searchable() bool { return strings.TrimSpace(l.SearchAPI) != "" }

// DetailCapable 该音源是否声明了详情实现名（歌词 / 封面来源）。
func (l SourceLine) DetailCapable() bool { return strings.TrimSpace(l.DetailAPI) != "" }

// SourceConfig 一整份音源配置（内存模型）。
type SourceConfig struct {
	Lines []SourceLine `json:"lines"`

	// Quality 每个音源当前选中的音质（source → level）。
	// 对应 App 里另一处存储的音质偏好，也是 #8534 里被 clamp 的对象。
	Quality map[string]string `json:"quality,omitempty"`

	// Origin 配置来源：文件路径、"example" 或导入 URL。仅用于排查。
	// 注意没有 "builtin" —— App 没有内置音源，缺配置就是 ErrNoSourceConfig。
	Origin string `json:"origin,omitempty"`
}

// ---------------------------------------------------------------- 解析

// rawLine 是解析中途的形态。
//
// Levels 就是 []string —— 真实配置里它永远是字符串数组（`"levels": ["standard",…]`），
// 声明成 any 会把类型信息丢掉，得不偿失。健壮性不靠这里兜，靠下面的**逐行解析**：
// 某一行写坏了只丢那一行，而不是整份配置解析失败。
type rawLine struct {
	ID        any      `json:"id"`
	Name      any      `json:"name"`
	Enabled   *bool    `json:"enabled"`
	Levels    []string `json:"levels"`
	SearchAPI string   `json:"searchApi"`
	DetailAPI string   `json:"detailApi"`
}

// lenientLine 只在严格解析失败时兜底，用来复刻 App 的
// `Array.isArray(x.levels) ? x.levels.map(String).filter(Boolean) : []`：
// levels 收 RawMessage，于是「不是数组」不会让这一行作废，元素是数字也能 String() 出来。
type lenientLine struct {
	ID        any             `json:"id"`
	Name      any             `json:"name"`
	Enabled   *bool           `json:"enabled"`
	Levels    json.RawMessage `json:"levels"`
	SearchAPI string          `json:"searchApi"`
	DetailAPI string          `json:"detailApi"`
}

// ParseSourceConfig 解析一份音源配置 JSON，容忍 App 认的几种形态。
func ParseSourceConfig(data []byte) (*SourceConfig, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("qingmusic: 音源配置内容为空")
	}

	// 裸数组：[{...}, {...}]
	if trimmed[0] == '[' {
		lines, err := decodeSourceLines(trimmed)
		if err != nil {
			return nil, err
		}
		return &SourceConfig{Lines: lines}, nil
	}

	// 对象：先看有没有 config 包一层，再看 lines / quality。
	var env struct {
		Config  json.RawMessage   `json:"config"`
		Lines   json.RawMessage   `json:"lines"`
		Quality map[string]string `json:"quality"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("qingmusic: 音源配置不是合法 JSON: %w", err)
	}

	// config 缺省时退回对象自身（对应 `parsed.config || parsed`）。
	inner := env.Config
	if len(bytes.TrimSpace(inner)) == 0 || string(bytes.TrimSpace(inner)) == "null" {
		inner = trimmed
	}

	innerT := bytes.TrimSpace(inner)
	// {"config": [...]}
	if len(innerT) > 0 && innerT[0] == '[' {
		lines, err := decodeSourceLines(innerT)
		if err != nil {
			return nil, err
		}
		return &SourceConfig{Lines: lines, Quality: normalizeQuality(env.Quality)}, nil
	}
	// {"config": {"lines": [...]}} 或 {"lines": [...]}
	var holder struct {
		Lines   json.RawMessage   `json:"lines"`
		Quality map[string]string `json:"quality"`
	}
	if err := json.Unmarshal(inner, &holder); err != nil {
		return nil, fmt.Errorf("qingmusic: 音源配置缺少 lines 节点: %w", err)
	}

	// quality 可能在 config 里，也可能在外层，两处都收（内层优先）。
	quality := normalizeQuality(holder.Quality)
	if quality == nil {
		quality = normalizeQuality(env.Quality)
	}

	if len(bytes.TrimSpace(holder.Lines)) == 0 || string(bytes.TrimSpace(holder.Lines)) == "null" {
		// 兜底：对象自身是单条 line（有些配置只放一条）。
		if l, ok := decodeOneSourceLine(inner); ok {
			return &SourceConfig{Lines: []SourceLine{l}, Quality: quality}, nil
		}
		return nil, errors.New("qingmusic: 音源配置缺少 lines 数组")
	}
	lines, err := decodeSourceLines(holder.Lines)
	if err != nil {
		return nil, err
	}
	return &SourceConfig{Lines: lines, Quality: quality}, nil
}

// normalizeQuality 归一化 quality 表：音源 id 与档位都 trim + 小写，空项丢掉。
func normalizeQuality(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.ToLower(strings.TrimSpace(v))
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeSourceLines(raw []byte) ([]SourceLine, error) {
	// 逐行收 RawMessage 再单独解析：一行写坏只丢那一行。
	// 对应 App 的 `lines.filter(x => x)` —— 也是逐项语义，不是整体失败。
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("qingmusic: lines 不是数组: %w", err)
	}
	out := make([]SourceLine, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		r, ok := parseRawLine(item)
		if !ok {
			continue // 这一行实在读不出来，跳过
		}
		l := normalizeSourceLine(r)
		// 空 id 的项永远匹配不上任何 source（App 的 find(l => l.id === sid) 同理），
		// 丢掉避免出现幽灵音源；重复 id 保留第一条。
		if l.ID == "" || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		out = append(out, l)
	}
	return out, nil
}

// parseRawLine 解析单行。先按 schema 严格解析（Levels []string），
// 失败再走一次宽松解析 —— 典型触发场景是 levels 里混了数字、或写成了非数组。
func parseRawLine(b []byte) (rawLine, bool) {
	var r rawLine
	if err := json.Unmarshal(b, &r); err == nil {
		return r, true
	}

	var loose lenientLine
	if err := json.Unmarshal(b, &loose); err != nil {
		return rawLine{}, false
	}
	return rawLine{
		ID:        loose.ID,
		Name:      loose.Name,
		Enabled:   loose.Enabled,
		Levels:    levelsFromRaw(loose.Levels),
		SearchAPI: loose.SearchAPI,
		DetailAPI: loose.DetailAPI,
	}, true
}

// levelsFromRaw 复刻 `Array.isArray(x.levels) ? x.levels.map(String).filter(Boolean) : []`。
// 非数组（字符串 / 对象 / 缺失 / null）一律当空；数组元素一律 String() 化。
func levelsFromRaw(raw json.RawMessage) []string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '[' {
		return nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(t, &elems); err != nil {
		return nil
	}
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		var v any
		if err := json.Unmarshal(e, &v); err != nil {
			continue
		}
		switch v.(type) {
		case map[string]any, []any, nil:
			continue // String(对象) 出来是 "[object Object]"，没有意义
		}
		if s := strings.ToLower(str(v)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func decodeOneSourceLine(raw []byte) (SourceLine, bool) {
	r, ok := parseRawLine(raw)
	if !ok {
		return SourceLine{}, false
	}
	l := normalizeSourceLine(r)
	return l, l.ID != ""
}

// normalizeSourceLine 逐字段复刻 #10775（含 searchApi / detailApi 的 trim）。
func normalizeSourceLine(r rawLine) SourceLine {
	id := strings.ToLower(str(r.ID)) // String(x.id).trim().toLowerCase()

	name := str(r.Name)
	if name == "" {
		name = id // name || id
	}

	enabled := true // enabled !== false
	if r.Enabled != nil {
		enabled = *r.Enabled
	}

	// Array.isArray(levels) ? levels.map(String).filter(Boolean) : []
	// 挂上 trim+小写：App 只做 String()，但配置手写时大小写/空格很常见，
	// 归一化到小写才能和 levelRank / 网关参数对齐。
	levels := make([]string, 0, len(r.Levels))
	for _, v := range r.Levels {
		if s := strings.ToLower(str(v)); s != "" {
			levels = append(levels, s)
		}
	}

	return SourceLine{
		ID:      id,
		Name:    name,
		Enabled: enabled,
		Levels:  levels,
		// 这两个是**函数名**，只能 trim，绝不能小写 —— JS 侧大小写敏感，
		// 小写化会让 fetchSearchMusic 查不到表（#10775 也只是 String(...).trim()）。
		SearchAPI: strings.TrimSpace(r.SearchAPI),
		DetailAPI: strings.TrimSpace(r.DetailAPI),
	}
}

// ---------------------------------------------------------------- 查询

// Line 按 id 取音源（id 会自动归一化，调用方传 " KW " 也能命中）。
func (c *SourceConfig) Line(id string) (SourceLine, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, l := range c.Lines {
		if l.ID == id {
			return l, true
		}
	}
	return SourceLine{}, false
}

// Enabled 该音源是否可用（配置里有且 enabled）。
func (c *SourceConfig) Enabled(id string) bool {
	l, ok := c.Line(id)
	return ok && l.Enabled
}

// EnabledSources 返回启用的音源 id，保持配置里的顺序。
// 搜索时按这个列表走 —— 对应 App 的「关闭的音源将不会在搜索时显示」。
func (c *SourceConfig) EnabledSources() []string {
	out := make([]string, 0, len(c.Lines))
	for _, l := range c.Lines {
		if l.Enabled {
			out = append(out, l.ID)
		}
	}
	return out
}

// EnabledLines 返回启用的音源对象（保持顺序）。
func (c *SourceConfig) EnabledLines() []SourceLine {
	out := make([]SourceLine, 0, len(c.Lines))
	for _, l := range c.Lines {
		if l.Enabled {
			out = append(out, l)
		}
	}
	return out
}

// SearchableLines 启用的音源里，searchApi 能在 s 的注册表里找到实现的那批。
//
// 「启用但没实现」是真实存在的状态（配置可以启用一个我们没实现的 searchApi），
// 所以调用方通常要的是这个，而不是 EnabledLines —— 否则会白跑一趟拿报错。
// 想同时提示用户少了什么，用 UnsearchableLines。
func (c *SourceConfig) SearchableLines(s *Searcher) []SourceLine {
	if s == nil {
		s = NewSearcher()
	}
	out := make([]SourceLine, 0, len(c.Lines))
	for _, l := range c.EnabledLines() {
		if l.Searchable() && s.HasAPI(l.SearchAPI) {
			out = append(out, l)
		}
	}
	return out
}

// UnsearchableLines 启用了、但 searchApi 没有实现的音源 —— 对应 App 的
// #11849「{音源名}搜索接口不可用」会在运行时报的那批。
func (c *SourceConfig) UnsearchableLines(s *Searcher) []SourceLine {
	if s == nil {
		s = NewSearcher()
	}
	out := make([]SourceLine, 0)
	for _, l := range c.EnabledLines() {
		if !l.Searchable() || !s.HasAPI(l.SearchAPI) {
			out = append(out, l)
		}
	}
	return out
}

// AllowedLevels 该音源允许的档位（按音质从高到低排好）。
// 未知档位排在最后。没有这条音源时返回 nil。
func (c *SourceConfig) AllowedLevels(id string) []string {
	l, ok := c.Line(id)
	if !ok {
		return nil
	}
	out := append([]string(nil), l.Levels...)
	sort.SliceStable(out, func(i, j int) bool {
		return LevelRankOf(out[i]) > LevelRankOf(out[j])
	})
	return out
}

// BestLevel 该音源支持的最高档。levels 为空时返回 ""。
func (c *SourceConfig) BestLevel(id string) string {
	ls := c.AllowedLevels(id)
	if len(ls) == 0 {
		return ""
	}
	return ls[0]
}

// DefaultPreferredLevel 用户没显式选音质时的默认档：standard（标准 128kbps）。
//
// 早先的默认是 lossless（复刻 App 行为），实测代价太大：网易云会一头撞进 jymaster
// （单曲 158MB 母带 flac），酷狗给 atmos（赵雷《我记得》单曲 79MB），分离链路据此
// 提交还会撞上上游 15 分钟超时。默认档现按「够用且省流量」定为 standard —— 它同时是
// 各音源普遍支持的兜底档，降级链到这里天然到底，不会白打请求。
//
// 注意档位 ≠ 容器：standard 定义的是码率（128kbps），主流音源给 mp3（酷狗实测
// audio/mpeg），但容器以直链实际返回为准 —— 酷我 lossless 实测给 m4a/aac、
// B 站 bili192 就是 M4A。落盘扩展名按直链推断、兜底 .mp3，真实容器由 SniffContainer
// 嗅探后决定标签写法（见 tags.go）。
const DefaultPreferredLevel = LevelStandard

// PreferredLevel 该音源当前生效的档位：
//
//	显式设置过           → 用设置值
//	没设过、支持 standard → standard（默认档，128kbps）
//	没设过、不支持 standard → 该音源支持的最高档（如某些音源只有 bili192）
//
// 没有这条音源时返回 ""。
func (c *SourceConfig) PreferredLevel(id string) string {
	l, ok := c.Line(id)
	if !ok {
		return ""
	}
	if lv := c.Quality[id]; lv != "" {
		return lv
	}
	if len(l.Levels) == 0 {
		return DefaultPreferredLevel
	}
	for _, a := range l.Levels {
		if a == DefaultPreferredLevel {
			return a
		}
	}
	return c.BestLevel(id)
}

// ResolveLevel 复刻 #8534 getStoredQualityForSource。
//
// preferred 为空时取默认档（standard，128kbps）；该音源没有 standard 才退到最高档。
// 返回 (最终档位, 该音源是否可用)。音源不可用时不改档位，原样返回。
func (c *SourceConfig) ResolveLevel(source, preferred string) (string, bool) {
	l, ok := c.Line(source)
	if !ok || !l.Enabled {
		return preferred, false
	}
	if len(l.Levels) == 0 {
		return preferred, true // 没声明档位 = 不限制
	}
	if preferred == "" {
		for _, lv := range l.Levels {
			if lv == DefaultPreferredLevel {
				return lv, true
			}
		}
		return c.BestLevel(source), true
	}
	for _, lv := range l.Levels {
		if lv == preferred {
			return preferred, true
		}
	}
	// 首选档位这个音源给不了 → 退到它支持的最高档（clampLevel）。
	return c.BestLevel(source), true
}

// LevelsFor 生成下载用的降级链：从 preferred 起，按音质从高到低列出该音源支持的档位。
//
// preferred 为空时取 PreferredLevel（默认 standard，128kbps）。preferred 不被支持时，
// 链首就是该音源的最高档。音源没有声明 levels 时只返回 preferred，
// 把决定权交回调用方。
//
// 这取代了 download.go 里写死的 lossless → exhigh → standard：
// 那个链条对「只支持 standard 的音源」会白打两次请求。
func (c *SourceConfig) LevelsFor(source, preferred string) []string {
	l, ok := c.Line(source)
	if !ok || len(l.Levels) == 0 {
		if preferred == "" {
			preferred = DefaultPreferredLevel
		}
		return []string{preferred}
	}

	if preferred == "" {
		preferred = c.PreferredLevel(source)
	}
	allowed := c.AllowedLevels(source) // 已按高→低排好
	for i, lv := range allowed {
		if lv == preferred {
			// 从命中档位往下截断：既不高跳（App 不会越级取更高），也不漏更低档。
			return append([]string(nil), allowed[i:]...)
		}
	}
	// preferred 不被支持 → 整条链给全（从最高档开始）。
	return allowed
}

// Reconcile 复刻 #8538：把 Quality 里「该音源已经不支持」的档位改写成该音源的最高档。
// 返回被改写的音源列表。导入新配置后调用一次，避免留着一个永远取不到的档位。
func (c *SourceConfig) Reconcile() []string {
	if c.Quality == nil {
		return nil
	}
	var changed []string
	for id, lv := range c.Quality {
		l, ok := c.Line(id)
		if !ok || len(l.Levels) == 0 {
			continue
		}
		supported := false
		for _, a := range l.Levels {
			if a == lv {
				supported = true
				break
			}
		}
		if supported {
			continue
		}
		best := c.BestLevel(id)
		if best == "" || best == lv {
			continue
		}
		c.Quality[id] = best
		changed = append(changed, id)
	}
	sort.Strings(changed)
	return changed
}

// Clone 深拷贝一份，便于调用方在不动原配置的前提下试探（比如临时关掉某个音源）。
func (c *SourceConfig) Clone() *SourceConfig {
	out := &SourceConfig{Origin: c.Origin, Lines: append([]SourceLine(nil), c.Lines...)}
	for i := range out.Lines {
		out.Lines[i].Levels = append([]string(nil), c.Lines[i].Levels...)
	}
	if c.Quality != nil {
		out.Quality = make(map[string]string, len(c.Quality))
		for k, v := range c.Quality {
			out.Quality[k] = v
		}
	}
	return out
}

// ---------------------------------------------------------------- 示例表 / 缺配置

// ErrNoSourceConfig 没有音源配置。
//
// ⚠️ 这是 App 的真实行为：**App 没有任何内置音源**。字节码里对应文案是
//
//	'未找到音源配置'
//	'当前未导入任何音源配置'
//	'请先在"我的"->"导入音源配置"中导入音源配置文件'
//
// 所以配置缺失既不是「用一套默认值」，也不是可忽略的小事 —— 它是一个需要
// 用户去导入的前置条件。LoadSourceConfig 会把这个错误原样返回。
var ErrNoSourceConfig = errors.New("未找到音源配置")

// IsNoSourceConfig 判断错误是否为「未导入音源配置」。
func IsNoSourceConfig(err error) bool { return errors.Is(err, ErrNoSourceConfig) }

// ExampleSourceConfig 一份可直接用的示例音源表（离线 / 测试 / -no-config 用）。
//
// ⚠️ 名字里的 example 是刻意的：App 并没有这样一张出厂表，别把它当成 App 行为。
// 它只是把一份**真实可用的配置**（见仓库里的 music.example.json）内联进来，
// 方便离线跑 CLI 与写测试。档位与 searchApi/detailApi 全部照抄那份配置，不是猜的：
// kw 支持 atmos/atmos_plus/master，tx 只到 lossless，mg 只有 standard。
//
// 展示名用中性叫法（那份真实配置里是「小窝源/小狗源/小云源/小Q源/小粉源」这类
// 玩梗命名，属于分享者的口味，不适合当默认展示）。
func ExampleSourceConfig() *SourceConfig {
	return &SourceConfig{
		Origin: "example",
		Lines: []SourceLine{
			{ID: SourceKw, Name: "酷我音乐", Enabled: true,
				SearchAPI: "fetchSearchMusic", DetailAPI: "fetchMusicDetail",
				Levels: []string{LevelStandard, LevelExhigh, LevelLossless, LevelAtmos, LevelAtmosPlus, LevelMaster}},
			{ID: SourceKg, Name: "酷狗音乐", Enabled: true,
				SearchAPI: "kgSearchMusic", DetailAPI: "kgMusicDetail",
				Levels: []string{LevelStandard, LevelExhigh, LevelLossless, LevelHires, LevelAtmos, LevelClear}},
			{ID: SourceWy, Name: "网易云音乐", Enabled: true,
				SearchAPI: "wySearchMusic", DetailAPI: "wyMusicDetail",
				Levels: []string{LevelStandard, LevelExhigh, LevelLossless, LevelHires, LevelSky, LevelJYEffect, LevelJYMaster}},
			{ID: SourceTx, Name: "QQ音乐", Enabled: true,
				SearchAPI: "txSearchMusic", DetailAPI: "txMusicDetail",
				Levels: []string{LevelStandard, LevelExhigh, LevelLossless}},
			{ID: SourceBili, Name: "哔哩哔哩", Enabled: true,
				SearchAPI: "biliSearchMusic", DetailAPI: "biliMusicDetail",
				Levels: []string{LevelBili192}},
			{ID: SourceMg, Name: "咪咕音乐", Enabled: true,
				SearchAPI: "mgSearchMusic", DetailAPI: "mgMusicDetail",
				Levels: []string{LevelStandard}},
		},
		Quality: map[string]string{},
	}
}

// ---------------------------------------------------------------- 读写

// DefaultSourceConfigPath 配置文件路径，可用环境变量 QM_CONFIG 覆盖。
func DefaultSourceConfigPath() string {
	if p := os.Getenv("QM_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "music.json"
	}
	return filepath.Join(home, ".config", "qingmusic", "music.json")
}

// LoadSourceConfig 读配置。path 为空走默认路径。
//
// 文件不存在时返回 (nil, ErrNoSourceConfig) —— **对齐 App**：App 没有内置音源，
// 缺配置就是要用户去「导入音源配置」。想拿到一份能直接跑的配置请用
// LoadSourceConfigOrExample，或自己 Import 一个 URL。
func LoadSourceConfig(path string) (*SourceConfig, error) {
	if path == "" {
		path = DefaultSourceConfigPath()
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSourceConfig
	}
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 读取音源配置失败: %w", err)
	}
	cfg, err := ParseSourceConfig(data)
	if err != nil {
		return nil, fmt.Errorf("qingmusic: 解析音源配置失败: %w", err)
	}
	cfg.Origin = path
	return cfg, nil
}

// LoadSourceConfigOrExample 是 LoadSourceConfig 的「离线友好」版：文件缺失时
// 退回 ExampleSourceConfig()，方便 CLI 在没导入配置时也能跑。
// 注意这不是 App 行为，只是本库的便捷封装。
func LoadSourceConfigOrExample(path string) (*SourceConfig, error) {
	cfg, err := LoadSourceConfig(path)
	if IsNoSourceConfig(err) {
		return ExampleSourceConfig(), nil
	}
	return cfg, err
}

// Save 落盘。写成 App 能直接吃的 {"lines":[...],"quality":{...}} 形态
// （App 的解包是 `parsed.config || parsed`，所以无 config 包裹同样正确）。
func (c *SourceConfig) Save(path string) error {
	if path == "" {
		path = DefaultSourceConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("qingmusic: 创建配置目录失败: %w", err)
	}
	out := &SourceConfig{Lines: c.Lines, Quality: c.Quality}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// 先写临时文件再改名，避免写一半把原配置毁了。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.Origin = path
	return nil
}

// ---------------------------------------------------------------- 导入

// Import 从 URL 或本地文件导入一份音源配置。
//
// 与 App 的导入行为对齐：**只做 JSON 结构校验，不校验音源 id 是否认识** ——
// 配置里出现未知音源照样收下（搜索时自然用不上，因为 Searcher 不认那个 id）。
func (c *SourceConfig) Import(ctx context.Context, client *http.Client, src string) error {
	var data []byte
	var origin string

	if u := strings.TrimSpace(src); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		if client == nil {
			client = NewHTTPClient(20 * time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("qingmusic: 下载音源配置失败: %w", err)
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("qingmusic: 下载音源配置失败: HTTP %d", res.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
		if err != nil {
			return fmt.Errorf("qingmusic: 读取音源配置失败: %w", err)
		}
		origin = u
	} else {
		var err error
		data, err = os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("qingmusic: 读取音源配置文件失败: %w", err)
		}
		origin = src
	}

	parsed, err := ParseSourceConfig(data)
	if err != nil {
		return err
	}
	if len(parsed.Lines) == 0 {
		return errors.New("qingmusic: 音源配置里没有有效的 lines 项")
	}

	// 导入是「替换」语义（和 App 一致），但保留用户已选的音质。
	old := c.Quality
	c.Lines = parsed.Lines
	if parsed.Quality != nil {
		c.Quality = parsed.Quality
	} else {
		c.Quality = old
	}
	if c.Quality == nil {
		c.Quality = map[string]string{}
	}
	c.Origin = origin
	c.Reconcile() // 新配置可能不再支持某些已选档位
	return nil
}

// SetQuality 设置某个音源的音质，并做一次 clamp（对应 #8554）。
// 返回实际生效的档位。音源不存在或未启用时返回错误。
func (c *SourceConfig) SetQuality(source, level string) (string, error) {
	l, ok := c.Line(source)
	if !ok {
		return "", fmt.Errorf("qingmusic: 音源配置里没有 %q", source)
	}
	if level == "" {
		return "", errors.New("qingmusic: 音质不能为空")
	}
	if len(l.Levels) > 0 {
		supported := false
		for _, a := range l.Levels {
			if a == level {
				supported = true
				break
			}
		}
		if !supported {
			return "", fmt.Errorf("qingmusic: %s 不支持音质 %q（可用: %s）",
				source, level, strings.Join(l.Levels, ", "))
		}
	}
	if c.Quality == nil {
		c.Quality = map[string]string{}
	}
	c.Quality[source] = level
	return level, nil
}

// SetEnabled 开关某个音源。至少保留一个启用的音源（对应 UI 文案「至少保留一个音源」）。
func (c *SourceConfig) SetEnabled(source string, enabled bool) error {
	idx := -1
	for i, l := range c.Lines {
		if l.ID == strings.ToLower(strings.TrimSpace(source)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("qingmusic: 音源配置里没有 %q", source)
	}
	if !enabled {
		n := 0
		for i, l := range c.Lines {
			if i != idx && l.Enabled {
				n++
			}
		}
		if n == 0 {
			return errors.New("qingmusic: 至少保留一个音源")
		}
	}
	c.Lines[idx].Enabled = enabled
	return nil
}
