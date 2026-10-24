package qingmusic

import (
	"fmt"
	"strings"
)

// 歌单广场的「一级线路 → 二级分类」表。
//
// ---------------------------------------------------------------- 逆向来源
//
// ⚠️ 整张表**不是接口返回的，而是硬编码在 App 的 JS 里** —— 这是最容易找错的一环：
// 四个平台的「分类清单」在网络上根本抓不到，`kw.php?type=getTagLists&id=`
// 传空 id 回的是**歌单**（total 9080），不是标签表。表就在字节码里。
//
// 位置：hermes **#15890**（`PlaylistSquare`，歌单广场页）里建了四个数组，
// 分别存进闭包环境槽 14/15/16/17，再由 **#15902**（`switchSource`）按来源取用：
//
//	switchSource(src):
//	  'kuwo'    → env14   （11 项，id 是数字串）
//	  'netease' → env15   （10 项，id 就是中文名本身）
//	  'tx'      → env17   （ 5 项，id 是 3/5/6/7/8）
//	  'kg'      → env16   （10 项，id 是 59/62/71/…）
//	  其它       → env15
//
// 归属**不是猜的**，两条独立证据：
//
//  1. #15890 渲染的四 个 tab 就是这四条线路，每个 tab 的 onPress 都是一个写死 source 的闭包：
//     小窝(#15915→'kuwo')、小云(#15916→'netease')、小绿(#15917→'tx')、小狗(#15918→'kg')；
//     进页面时 useEffect(#15913) 调 `switchSource('kuwo')` —— 默认落在小窝。
//  2. kg 表首项 id=10000000 与本库既有常量 `KgCategoryDefaultTagID` 完全一致
//     （#15894 kg 分支里写死的 `param.id`），交叉验证通过。
//
// ⚠️ **广场里 tx 的别名是「小绿」，不是「小Q」**：'小Q' 是配置体系
// （music.json 的 `小Q源`）与导航标题（`小Q歌单`）里的叫法，两套名字都真实存在。
// 做分派时别名必须对上，否则会落到别的线路。
//
// ---------------------------------------------------------------- 实测状态（2026-09-22 实跑，非推断）
//
//	线路      分类 id 是否真的分流
//	小窝 kw  ✅  11/11 生效，且内容与名字吻合（1877「游戏」→《狂野飙车8》原声带）
//	小云 wy  ✅  10/10 生效，且内容与名字吻合（「儿童」→ 哄娃儿歌大全）
//	小绿 tx  ⚠️  字节码 5 项的「推荐/最热/…」标签与服务端分类树对不上，
//	           以 get_category_basic 的服务端命名为准（见 playlistLineCategories）
//	小狗 kg  ❌  10/10 返回**同一批歌单**：任取 3 个 id，返回的 specialid 列表
//	           完全一致（逐项比对，非只看首条）；换 App 同形的 `t=<裸id>&c=&p=1`
//	           也一样。端点本身是活的（第 2 页与第 1 页不同），
//	           **只有「分类」这一维被服务端忽略**。
//
// 所以 kg 的分类在上游已无可用数据源（接口废 + 官网换 gcid 新体系），
// 本库 kg 线路不设分类，只出最新歌单流（见 playlists_kg_tx.go）。
// ---------------------------------------------------------------- 与旧结论的关系
//
// 旧版 `TxCategoryIDs()`（1..20 全有效）没有错：1..20 服务端都容忍且各有内容。
// 参考项目字节码表把它收窄成 5 项「推荐/最热/…」，但那批标签与服务端
// get_category_basic 的树名对不上（5 在服务端是「日语」不是「推荐」）。
// 本库以服务端树为准：tx 用 20 项命名分类，默认第一项「国语」(1)。

// 歌单广场四条线路的别名（取自 #15890 的 tab 标签，顺序即 App 的 tab 顺序）。
const (
	PlaylistLineKwAlias = "小窝"
	PlaylistLineWyAlias = "小云"
	PlaylistLineTxAlias = "小绿"
	PlaylistLineKgAlias = "小狗"
)

// PlaylistCategory 歌单广场某条线路下的**二级分类**（App 里就是那排分类 chip）。
type PlaylistCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Hot App 在字节码里给这一项打了 `hot: true`。
	//
	// 点位就是「热门/默认」的前几个（kw 的 抖音/经典，wy 的 华语/流行）。
	// 保留它是因为它是**字节码里的原始信息**，不是本库的加工。
	Hot bool `json:"hot,omitempty"`
}

// PlaylistLine 歌单广场的一条**一级线路**（App 里就是那 4 个 tab）。
type PlaylistLine struct {
	Source     string             `json:"source"` // kw / wy / tx / kg
	Alias      string             `json:"alias"`  // 小窝 / 小云 / 小绿 / 小狗
	Title      string             `json:"title"`  // 导航标题：小窝歌单 / 小云歌单 / 小Q歌单 / 小狗歌单
	Categories []PlaylistCategory `json:"categories"`
}

// playlistLineOrder App 里 tab 的真实顺序（#15890 渲染顺序）。
//
// ⚠️ 不是拼音序也不是 kw/kg/wy/tx 那种习惯序 —— 用户看到的就是这个顺序。
var playlistLineOrder = []string{SourceKw, SourceWy, SourceTx, SourceKg}

// playlistLineMeta 线路的别名与导航标题。
//
// 别名来自 #15915-#15918 四个 onPress 闭包配的 tab 文案；
// 导航标题来自 AppNavigator（#5441）里的 `options.title`。
var playlistLineMeta = map[string]struct{ Alias, Title string }{
	SourceKw: {PlaylistLineKwAlias, "小窝歌单"},
	SourceWy: {PlaylistLineWyAlias, "小云歌单"},
	SourceTx: {PlaylistLineTxAlias, "小Q歌单"},
	SourceKg: {PlaylistLineKgAlias, "小狗歌单"},
}

// playlistLineCategories 各线路的二级分类表。
//
// ⚠️ 与参考项目字节码表的两处**刻意偏离**（2026-09-22，均有实测依据）：
//
//  1. kg：字节码里确有 10 项分类（推荐/流行/网络/…），但实测服务端**忽略分类 id**
//     ——任取几个 id 返回的 specialid 列表逐项一致，换 App 同形的请求也一样。
//     展示一组点了没反应的假分类比不放更糟，故 kg 置空（广场只出推荐流）。
//  2. tx：字节码 5 项（5 推荐/6 最热/7 最新/3 热藏/8 飙升）与服务端自己的
//     分类树对不上 —— playlist.PlayListCategoryServer.get_category_basic 对同一批
//     id 给的名字是 国语/粤语/英语/韩语/日语/小语种/治愈/…（2026-09-22 逐项实测，
//     并与 get_category_content 的 total_cnt 交叉验证：精选=4、国语/伤感/宣泄=996）。
//     以服务端树为准，采用 20 项命名分类。
//
// kw/wy 两张表照抄字节码，实测内容与名字吻合。
var playlistLineCategories = map[string][]PlaylistCategory{
	// env14：酷我标签 id（数字串）
	SourceKw: {
		{ID: "2189", Name: "抖音", Hot: true},
		{ID: "1265", Name: "经典", Hot: true},
		{ID: "2200", Name: "情歌"},
		{ID: "2199", Name: "BGM"},
		{ID: "2212", Name: "演唱会"},
		{ID: "1877", Name: "游戏"},
		{ID: "155", Name: "怀旧"},
		{ID: "621", Name: "网络"},
		{ID: "2201", Name: "合唱"},
		{ID: "181", Name: "ACG"},
		{ID: "171", Name: "儿童"},
	},

	// env15：网易云分类名（id 与名字相同，直接进 `cat=`）
	SourceWy: {
		{ID: "华语", Name: "华语", Hot: true},
		{ID: "流行", Name: "流行", Hot: true},
		{ID: "摇滚", Name: "摇滚"},
		{ID: "民谣", Name: "民谣"},
		{ID: "电子", Name: "电子"},
		{ID: "说唱", Name: "说唱"},
		{ID: "轻音乐", Name: "轻音乐"},
		{ID: "影视原声", Name: "影视原声"},
		{ID: "ACG", Name: "ACG"},
		{ID: "儿童", Name: "儿童"},
	},

	// 服务端分类树（get_category_basic），非字节码表 —— 见上方偏离说明。
	SourceTx: {
		{ID: "1", Name: "国语"},
		{ID: "2", Name: "粤语"},
		{ID: "3", Name: "英语"},
		{ID: "4", Name: "韩语"},
		{ID: "5", Name: "日语"},
		{ID: "6", Name: "小语种"},
		{ID: "7", Name: "治愈"},
		{ID: "8", Name: "快乐"},
		{ID: "9", Name: "寂寞"},
		{ID: "10", Name: "思念"},
		{ID: "11", Name: "活力塑形"},
		{ID: "12", Name: "伤感"},
		{ID: "13", Name: "安静"},
		{ID: "14", Name: "精选"},
		{ID: "15", Name: "超级猩猩"},
		{ID: "16", Name: "励志"},
		{ID: "17", Name: "宣泄"},
		{ID: "18", Name: "获奖原声"},
		{ID: "19", Name: "有氧燃脂"},
		{ID: "20", Name: "甜蜜"},
	},

	// env16：酷狗 —— 参考字节码里确有 10 项标签表，但其依托的 getSpecial 接口
	// 2026-09-22 实测已废（分类 id 与翻页均被服务端忽略，永远返回同一批 20 条）。
	// 故 kg 不设分类，广场走 plist/index 最新歌单流（600 条、真实翻页，
	// 见 playlists_kg_tx.go 的 KgLatestPlaylists）。
	SourceKg: nil,
}

// ParsePlaylistLine 把用户输入翻成本库的 source id。
//
// 接受三种写法：平台 id（kw）、官方名（kuwo）、**App 里的线路别名（小窝）**
// —— 别名这一路是给「照着 App 界面敲」的人准备的，认不出就返回 false，
// 不替调用方猜一个默认线路。
//
// ⚠️ 比对前会 ToLower，所以下面 case 里的别名一律写**小写形式**
// （「小Q」要写成 "小q"，否则永远匹配不上 —— 这个坑当场踩过一次）。
func ParsePlaylistLine(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case SourceKw, "kuwo", PlaylistLineKwAlias, "小窝线路", "小窝歌单":
		return SourceKw, true
	case SourceWy, "netease", "wangyi", PlaylistLineWyAlias, "小云线路", "小云歌单":
		return SourceWy, true
	case SourceTx, "qq", "qqmusic", PlaylistLineTxAlias, "小绿线路", "小Q", "小q", "qq歌单", "小Q歌单":
		return SourceTx, true
	case SourceKg, "kugou", PlaylistLineKgAlias, "小狗线路", "小狗歌单":
		return SourceKg, true
	default:
		return "", false
	}
}

// PlaylistLineOf 取一条线路的完整信息（含二级分类）。
func PlaylistLineOf(source string) (PlaylistLine, bool) {
	src, ok := ParsePlaylistLine(source)
	if !ok {
		return PlaylistLine{}, false
	}
	return PlaylistLine{
		Source:     src,
		Alias:      playlistLineMeta[src].Alias,
		Title:      playlistLineMeta[src].Title,
		Categories: PlaylistCategoriesOf(src),
	}, true
}

// PlaylistLines 按 App 的 tab 顺序返回四条线路。
func PlaylistLines() []PlaylistLine {
	out := make([]PlaylistLine, 0, len(playlistLineOrder))
	for _, src := range playlistLineOrder {
		// 表是写死的，PlaylistLineOf 不会失败；真失败了宁可少一条也不塞占位。
		if l, ok := PlaylistLineOf(src); ok {
			out = append(out, l)
		}
	}
	return out
}

// PlaylistCategoriesOf 返回某条线路的二级分类**副本**。
//
// 返回副本而不是内部切片：调用方（尤其是 CLI）会排序或裁剪，
// 让它改到包级变量上会污染同进程的下一次调用。
func PlaylistCategoriesOf(source string) []PlaylistCategory {
	src, ok := ParsePlaylistLine(source)
	if !ok {
		return nil
	}
	inner := playlistLineCategories[src]
	out := make([]PlaylistCategory, len(inner))
	copy(out, inner)
	return out
}

// LookupPlaylistCategory 按 id 或名字查一项分类。
//
// 只做**精确匹配**（忽略大小写与首尾空白）：分类名字都是短中文词，
// 前缀/包含匹配会把「流行」匹配到「流行摇滚」这类东西上，静默给出错数据。
// 重名（kg 的「流行」有 167/153 两项）返回 false —— 交给调用方报错。
func LookupPlaylistCategory(source, idOrName string) (PlaylistCategory, bool) {
	key := strings.ToLower(strings.TrimSpace(idOrName))
	if key == "" {
		return PlaylistCategory{}, false
	}
	for _, c := range PlaylistCategoriesOf(source) {
		if strings.ToLower(c.ID) == key || strings.ToLower(c.Name) == key {
			return c, true
		}
	}
	return PlaylistCategory{}, false
}

// PlaylistCategoryCandidates 列出同名/同 id 的全部命中（用于「重名」报错）。
func PlaylistCategoryCandidates(source, idOrName string) []PlaylistCategory {
	key := strings.ToLower(strings.TrimSpace(idOrName))
	var out []PlaylistCategory
	for _, c := range PlaylistCategoriesOf(source) {
		if strings.ToLower(c.ID) == key || strings.ToLower(c.Name) == key {
			out = append(out, c)
		}
	}
	return out
}

// ResolvePlaylistCategory 把「分类 id 或名字」解析成分类 id。
//
// 规则（顺序即优先级）：
//
//	key 为空        → 该线路表**首项**（App 切线路时就是选 cat[0]）
//	命中唯一        → 该 id
//	命中多项（重名） → 报错并列出候选 id，让你自己选
//	认不出          → 报错并提示去 `qm playlists cats <线路>` 看清单
//
// 为什么认不出要**报错**而不是「当成裸 id 发出去」—— 实测四条线路对
// 未知分类 id 的处理**各不相同，而且都不可见**：
//
//	小窝 kw  → total=0 空列表（看起来像「这个分类没歌单」）
//	小绿 tx  → total_cnt=0 空列表（同上）
//	小狗 kg  → 服务端直接忽略，回同一批推荐歌单（看起来像「分类没生效」）
//	小云 wy  → **静默回落成 500 条通用歌单**（和 cat 传空完全一样）
//
// 也就是说，id 打错一位，有的线给你空、有的线给你**别的分类的数据**，
// 没有一条会报错。查表报错是唯一能把「分类真没歌单」与「id 服务端不认识」
// 分开的手段。想绕过本表直接发裸 id，用 -raw。
func ResolvePlaylistCategory(source, idOrName string) (string, error) {
	src, ok := ParsePlaylistLine(source)
	if !ok {
		return "", fmt.Errorf("qingmusic: 未知歌单线路 %q（可选 kw/wy/tx/kg 或 小窝/小云/小绿/小狗）", source)
	}
	cats := PlaylistCategoriesOf(src)
	if len(cats) == 0 {
		return "", fmt.Errorf("qingmusic: 线路 %s 没有内置分类表", src)
	}
	if strings.TrimSpace(idOrName) == "" {
		return cats[0].ID, nil
	}
	hits := PlaylistCategoryCandidates(src, idOrName)
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("qingmusic: %s 线路没有名为 %q 的分类"+
			"（用 qm playlists cats %s 看全部 %d 项，或用 -raw 直接发裸 id）",
			src, idOrName, src, len(cats))
	case 1:
		return hits[0].ID, nil
	default:
		parts := make([]string, 0, len(hits))
		for _, h := range hits {
			parts = append(parts, fmt.Sprintf("%s(%s)", h.Name, h.ID))
		}
		return "", fmt.Errorf("qingmusic: %s 线路有 %d 个叫 %q 的分类：%s —— 请直接用 id",
			src, len(hits), idOrName, strings.Join(parts, "、"))
	}
}

// PlaylistLineLabel 输出用的「小窝(kw)」形式标签。
func PlaylistLineLabel(source string) string {
	src, ok := ParsePlaylistLine(source)
	if !ok {
		return source
	}
	return fmt.Sprintf("%s(%s)", playlistLineMeta[src].Alias, src)
}
