package qingmusic

// 歌词抓取：按音源直连官方歌词端点，返回 LRC 纯文本。
//
// 端点可用性为 2026-10 实测（直连生产环境）：
//   - wy：music.163.com/api/song/lyric            ✅ 明文 JSON（lrc.lyric）
//   - tx：i.y.qq.com/lyric/fcgi-bin/…fcg          ✅ JSON，lyric 字段为 base64
//   - kg：krcs.kugou.com 检索 → lyrics.kugou.com 下载（fmt=lrc）✅ content 为 base64
//   - kw：m.kuwo.cn/newh5/singles/songinfoandlrc  ❌ 恒返回「音乐查询失败」，
//       nmobi / www api（csrf）均不可用——酷我接口近年频繁变动，社区已放弃该端点
//   - mg：music.migu.cn/v3/api/music/audioLyric   ❌ 301 到 HTML；MIGUM2.0
//       get_lyric.do 返回「路由请求不支持」
//
// kw/mg 的歌词由上层 service 做跨音源回退（拿 name+artist 到 wy/tx/kg 搜同名曲
// 取词），本层只负责「给定 source+rid 直连取词」与「候选列表择优匹配」。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrLyricUnsupported 该音源没有可用的直连歌词端点（上层据此走跨源回退）。
var ErrLyricUnsupported = fmt.Errorf("该音源暂不支持歌词")

// 歌词端点默认地址（变量而非常量：测试用 httptest 顶替）。
var (
	lyricWyBase       = "https://music.163.com/api/song/lyric"
	lyricTxBase       = "https://i.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg"
	lyricKgSearchBase = "https://krcs.kugou.com/search"
	lyricKgDlBase     = "https://lyrics.kugou.com/download"
)

// Lyric 按 source 直连取词。kg 需要 name/artist（候选检索关键词）与
// durationMs（时长毫秒，参与精确匹配，0 表示未知）；其余音源忽略这两个参数。
func (s *Searcher) Lyric(ctx context.Context, source, rid, name, artist string, durationMs int64) (string, error) {
	rid = strings.TrimSpace(rid)
	if rid == "" {
		return "", fmt.Errorf("rid 必填")
	}
	switch source {
	case SourceWy:
		return s.lyricWy(ctx, lyricWyBase, rid)
	case SourceTx:
		return s.lyricTx(ctx, lyricTxBase, rid)
	case SourceKg:
		return s.lyricKg(ctx, lyricKgSearchBase, lyricKgDlBase, rid, strings.TrimSpace(name+" "+artist), durationMs)
	default:
		return "", ErrLyricUnsupported
	}
}

// lyricWy GET {base}?id={rid}&lv=1&kv=1&tv=-1 → {"lrc":{"lyric":"[00:00.000] …"}}
func (s *Searcher) lyricWy(ctx context.Context, base, rid string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"?id="+url.QueryEscape(rid)+"&lv=1&kv=1&tv=-1", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", uaWindows)
	req.Header.Set("Referer", "https://music.163.com")

	body, err := s.do(ctx, req)
	if err != nil {
		return "", err
	}
	var raw struct {
		Code int `json:"code"`
		Lrc  struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("qingmusic: wy 歌词响应解析失败: %w", err)
	}
	if raw.Code != 0 && raw.Code != 200 {
		return "", fmt.Errorf("qingmusic: wy 歌词接口返回 code=%d", raw.Code)
	}
	return strings.TrimSpace(raw.Lrc.Lyric), nil
}

// lyricTx GET {base}?songmid={rid}&g_tk=5381&format=json → {"retcode":0,"lyric":"<base64 LRC>"}
// 纯文本返回时校验必须带 Referer（y.qq.com 防盗链）。
func (s *Searcher) lyricTx(ctx context.Context, base, rid string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"?songmid="+url.QueryEscape(rid)+"&g_tk=5381&format=json", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", uaWindows)
	req.Header.Set("Referer", "https://y.qq.com/")

	body, err := s.do(ctx, req)
	if err != nil {
		return "", err
	}
	var raw struct {
		RetCode int    `json:"retcode"`
		Lyric   string `json:"lyric"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("qingmusic: tx 歌词响应解析失败: %w", err)
	}
	if raw.RetCode != 0 || raw.Lyric == "" {
		return "", fmt.Errorf("qingmusic: tx 歌词接口返回 retcode=%d", raw.RetCode)
	}
	dec, err := base64.StdEncoding.DecodeString(raw.Lyric)
	if err != nil {
		return "", fmt.Errorf("qingmusic: tx 歌词 base64 解码失败: %w", err)
	}
	return strings.TrimSpace(string(dec)), nil
}

// lyricKg 两步：krcs 检索候选（hash=rid + 关键词 + 时长）→ 下载 fmt=lrc（content 为 base64）。
// KRC 加密格式只在 fmt 缺省时出现；显式 fmt=lrc 拿到的就是明文 LRC 的 base64。
func (s *Searcher) lyricKg(ctx context.Context, searchBase, dlBase, rid, keyword string, durationMs int64) (string, error) {
	if strings.TrimSpace(keyword) == "" {
		return "", fmt.Errorf("kg 歌词需要 name/artist 作检索关键词")
	}
	q := searchBase + "?ver=1&man=yes&client=mobi&hash=" + url.QueryEscape(rid) +
		"&duration=" + strconv.FormatInt(durationMs, 10) +
		"&keyword=" + url.QueryEscape(keyword)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", uaWindows)

	body, err := s.do(ctx, req)
	if err != nil {
		return "", err
	}
	var found struct {
		Status     int `json:"status"`
		Candidates []struct {
			ID        string `json:"id"`
			AccessKey string `json:"accesskey"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &found); err != nil {
		return "", fmt.Errorf("qingmusic: kg 歌词候选解析失败: %w", err)
	}
	if found.Status != 200 || len(found.Candidates) == 0 {
		return "", fmt.Errorf("qingmusic: kg 歌词候选为空（status=%d）", found.Status)
	}
	cand := found.Candidates[0]

	dl := dlBase + "?ver=1&client=pc&id=" + url.QueryEscape(cand.ID) +
		"&accesskey=" + url.QueryEscape(cand.AccessKey) + "&fmt=lrc&charset=utf8"
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, dl, nil)
	if err != nil {
		return "", err
	}
	req2.Header.Set("User-Agent", uaWindows)

	body2, err := s.do(ctx, req2)
	if err != nil {
		return "", err
	}
	var dlRes struct {
		Status  int    `json:"status"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body2, &dlRes); err != nil {
		return "", fmt.Errorf("qingmusic: kg 歌词下载解析失败: %w", err)
	}
	if dlRes.Status != 200 || dlRes.Content == "" {
		return "", fmt.Errorf("qingmusic: kg 歌词下载失败（status=%d）", dlRes.Status)
	}
	dec, err := base64.StdEncoding.DecodeString(dlRes.Content)
	if err != nil {
		return "", fmt.Errorf("qingmusic: kg 歌词 base64 解码失败: %w", err)
	}
	return strings.TrimSpace(string(dec)), nil
}

// ---- 跨源回退的候选匹配 ----

// BestTrackMatch 在搜索结果里挑与 name/artist 最匹配的曲目；没有足够把握的
// 匹配返回 nil（宁可没有歌词，也不要给用户一首同名的别的歌的词）。
//
// 打分：歌名完全相等（归一化后）3 分；互相包含 1 分；歌手有交集 1 分。
// 接受线 = 歌名完全相等（3），即「歌名一样才认，歌手分越高排越前」。
func BestTrackMatch(tracks []Track, name, artist string) *Track {
	wantName, wantArtist := normMatch(name), normMatch(artist)
	var best *Track
	bestScore := 0
	for i := range tracks {
		t := &tracks[i]
		score := 0
		gotName, gotArtist := normMatch(t.Name), normMatch(t.Artist)
		switch {
		case gotName != "" && gotName == wantName:
			score = 3
		case wantName != "" && (strings.Contains(gotName, wantName) || strings.Contains(wantName, gotName)):
			score = 1
		}
		if wantArtist != "" && gotArtist != "" && strings.Contains(gotArtist, wantArtist) {
			score++
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	if best == nil || bestScore < 3 {
		return nil
	}
	return best
}

// normMatch 匹配归一化：去空白、小写、去装饰性括号段（「稻香 (Live)」→「稻香」）。
func normMatch(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "（("); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.ReplaceAll(s, " ", "")
}
