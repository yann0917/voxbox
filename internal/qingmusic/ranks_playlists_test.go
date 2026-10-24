package qingmusic

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------- 排行榜

// kw 归一化：rid 抠不出数字整条丢弃；时长是格式化串；http 封面升 https。
func TestNormalizeKwRankSong(t *testing.T) {
	if _, ok := NormalizeKwRankSong(map[string]any{"rid": "abc", "name": "x"}); ok {
		t.Fatal("rid 无数字应整条丢弃")
	}
	s, ok := NormalizeKwRankSong(map[string]any{
		"rid": "ku_123456", "name": "歌", "artist": "人", "album": "专辑",
		"pic": "http://img.kuwo.cn/a.jpg", "songTimeMinutes": "03:19", "releaseDate": "2024-01-01",
	})
	if !ok || s.Rid != "123456" || s.Interval != "03:19" {
		t.Fatalf("kw 归一化错误: %+v keep=%v", s, ok)
	}
	if s.Pic != "https://img.kuwo.cn/a.jpg" {
		t.Fatalf("封面应升 https: %s", s.Pic)
	}
	if s.Vid != "没有vid" {
		t.Fatalf("无 MV 应塞「没有vid」: %s", s.Vid)
	}
}

// wy 归一化：id 转不动丢；歌手用 "、" 拼接；时长 "4分13秒"。
func TestNormalizeWyRankSong(t *testing.T) {
	s, ok := NormalizeWyRankSong(map[string]any{
		"id": "186016", "name": "歌", "dt": float64(253000), "publishTime": float64(1700000000000),
		"ar": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}},
		"al": map[string]any{"name": "专辑", "picUrl": "https://p/1.jpg"},
	})
	if !ok || s.Rid != "186016" || s.Artist != "A、B" || s.Album != "专辑" {
		t.Fatalf("wy 归一化错误: %+v keep=%v", s, ok)
	}
	if s.Interval != "4分13秒" {
		t.Fatalf("wy 时长应为中文格式: %s", s.Interval)
	}
}

// tx 归一化：歌名取 title；歌手 " & "；rid 取 mid；封面由 album.mid 现拼。
func TestNormalizeTxRankSong(t *testing.T) {
	s, ok := NormalizeTxRankSong(map[string]any{
		"mid": "003aAY4m3YyTas", "id": "bad_id_should_not_win", "title": "茶汤",
		"interval": float64(238),
		"singer":   []any{map[string]any{"name": "郁可唯"}, map[string]any{"name": "时代少年团"}},
		"album":    map[string]any{"mid": "000GGPbd0g_2Vz", "name": "专辑"},
	})
	if !ok || s.Rid != "003aAY4m3YyTas" || s.Name != "茶汤" || s.Artist != "郁可唯 & 时代少年团" {
		t.Fatalf("tx 归一化错误: %+v keep=%v", s, ok)
	}
	if s.Duration != 238 {
		t.Fatalf("tx Duration 应为秒数: %d", s.Duration)
	}
	if s.Pic != "https://y.gtimg.cn/music/photo_new/T002R300x300M000000GGPbd0g_2Vz.jpg" {
		t.Fatalf("封面拼接错误: %s", s.Pic)
	}
}

// kg 归一化：rid=hash；歌手 filename 拆分兜底；音质三档按大小非 0 塞。
func TestNormalizeKgRankSong(t *testing.T) {
	s := NormalizeKgRankSong(map[string]any{
		"hash": "abc123", "filename": "歌手A - 歌名X - 副标", "duration": float64(200),
		"filesize":   float64(1024 * 1024 * 4),
		"sqfilesize": float64(0), // 0 不塞档位
		"imgurl":     "http://imge.kugou.com/{size}_1.jpg",
	})
	if s.Rid != "abc123" || s.Name != "歌名X - 副标" || s.Artist != "歌手A" {
		t.Fatalf("kg 归一化错误: %+v", s)
	}
	if s.Duration != 200 {
		t.Fatalf("kg Duration 应为秒数: %d", s.Duration)
	}
	if len(s.Types) != 1 || s.Types[0].Type != "128k" {
		t.Fatalf("0 大小的档位不应塞入: %+v", s.Types)
	}
	if s.Pic != "https://imge.kugou.com/400_1.jpg" {
		t.Fatalf("封面 {size} 替换 + https 升级错误: %s", s.Pic)
	}
}

// 四平台榜单表都在，且可按中文榜名/id/rankid 查回。
func TestRankBoards(t *testing.T) {
	counts := map[string]int{SourceKw: 6, SourceWy: 13, SourceTx: 22, SourceKg: 51}
	for src, want := range counts {
		if got := len(RankBoards(src)); got != want {
			t.Errorf("RankBoards(%s) = %d 项, want %d", src, got, want)
		}
	}
	if RankBoards("bili") != nil {
		t.Error("不支持的音源应返回 nil")
	}
	b, ok := ResolveRankBoard(SourceKw, "飙升榜")
	if !ok || b.RankID != "93" {
		t.Fatalf("中文榜名查询失败: %+v", b)
	}
	if b, ok := ResolveRankBoard(SourceKg, "kg__8888"); !ok || b.RankID != "8888" {
		t.Fatalf("kg id 查询失败: %+v", b)
	}
	if _, ok := ResolveRankBoard(SourceTx, "不存在"); ok {
		t.Error("查不到应返回 false")
	}
}

// 统一入口：不支持的音源报错；page 归一。
func TestFetchRankSongsUnsupported(t *testing.T) {
	if _, err := FetchRankSongs(context.Background(), "bili", "x", 1); err == nil {
		t.Fatal("不支持的音源应报错")
	}
	if _, err := FetchRankSongs(context.Background(), "", "", 1); err == nil {
		t.Fatal("空音源应报错")
	}
}

// ---------------------------------------------------------------- 歌单

// kw 列表与热门分支字段名不同，id/img/listencnt 都要走别名链。
func TestDecodePlaylistArrayAlias(t *testing.T) {
	list := decodePlaylistArray([]any{
		map[string]any{"id": "1", "name": "标签歌单", "img": "i1.png", "uname": "u", "total": "10", "listencnt": "100"},
		map[string]any{"rid": "2", "name": "热门歌单", "pic": "i2.png", "PlayCnt": float64(200)},
	}, SourceKw)
	if len(list) != 2 {
		t.Fatalf("应解析出 2 条: %d", len(list))
	}
	if list[0].ID != "1" || list[0].Img != "i1.png" || list[0].Total != 10 || list[0].ListenCnt != 100 {
		t.Fatalf("标签分支别名链错误: %+v", list[0])
	}
	if list[1].ID != "2" || list[1].Img != "i2.png" || list[1].ListenCnt != 200 {
		t.Fatalf("热门分支别名链错误: %+v", list[1])
	}
}

// DetailAll 翻页聚合：单页封顶 100 → &page 翻页取全量，按 Rid 去重，后续页失败保留部分。
func TestDetailAllPaging(t *testing.T) {
	c := NewPlaylistClient()
	pages := map[int][]PlaylistSong{
		1: {{Rid: "1", Name: "a"}, {Rid: "2", Name: "b"}},
		2: {{Rid: "2", Name: "b"}, {Rid: "3", Name: "c"}}, // 跨页重复项
	}
	c.detailPageFn = func(_ context.Context, _ string, page int) (*PlaylistDetail, error) {
		if page > 2 {
			return &PlaylistDetail{Total: 3, Source: SourceKw}, nil // 空页 → 终止
		}
		return &PlaylistDetail{Total: 3, Source: SourceKw, MusicList: pages[page]}, nil
	}
	d, err := c.DetailAll(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.MusicList) != 3 {
		t.Fatalf("应聚合 3 首（去重后）: %d", len(d.MusicList))
	}
	if d.MusicList[2].Rid != "3" || d.Source != SourceKw {
		t.Fatalf("聚合结果错误: %+v", d.MusicList)
	}
	// 第一页失败 → 整体失败
	c2 := NewPlaylistClient()
	c2.detailPageFn = func(_ context.Context, _ string, _ int) (*PlaylistDetail, error) {
		return nil, context.DeadlineExceeded
	}
	if _, err := c2.DetailAll(context.Background(), "42"); err == nil {
		t.Fatal("第一页失败应整体失败")
	}
}

// PlaylistSongsToTracks：rid 与 name 都空的行丢弃；source 统一注入。
func TestPlaylistSongsToTracks(t *testing.T) {
	tracks := PlaylistSongsToTracks([]PlaylistSong{
		{Rid: "1", Name: "a", Artist: "x"},
		{Rid: "", Name: "", Artist: "y"}, // 丢弃
		{Rid: "3", Name: "c", Album: "al", Duration: 210000},
	}, SourceTx)
	if len(tracks) != 2 {
		t.Fatalf("应产出 2 条: %d", len(tracks))
	}
	if tracks[0].Source != SourceTx || tracks[1].Album != "al" || tracks[1].Duration != 210000 {
		t.Fatalf("Track 转换错误: %+v", tracks)
	}
}

// tx 歌单详情单曲归一化：rid 取 mid、interval 秒 → 毫秒。
func TestNormalizeTxPlaylistSong(t *testing.T) {
	s, ok := NormalizeTxPlaylistSong(map[string]any{
		"mid": "003mid", "id": "999", "title": "歌", "interval": float64(238),
		"singer": []any{map[string]any{"name": "A"}},
		"album":  map[string]any{"mid": "alb", "name": "专辑"},
	})
	if !ok || s.Rid != "003mid" || s.Duration != 238000 || s.Album != "专辑" {
		t.Fatalf("tx 歌单曲归一化错误: %+v keep=%v", s, ok)
	}
}

// tx 分类 id 表：1..20 服务端都容忍。
func TestTxCategoryIDs(t *testing.T) {
	ids := TxCategoryIDs()
	if len(ids) != 20 || ids[0] != 1 || ids[19] != 20 {
		t.Fatalf("tx 分类表错误: %v", ids)
	}
}

// 歌单广场四条线路：顺序（kw/wy/tx/kg）、别名、分类表完整性。
func TestPlaylistLines(t *testing.T) {
	lines := PlaylistLines()
	if len(lines) != 4 {
		t.Fatalf("应 4 条线路: %d", len(lines))
	}
	wantOrder := []string{SourceKw, SourceWy, SourceTx, SourceKg}
	wantCats := map[string]int{SourceKw: 11, SourceWy: 10, SourceTx: 20, SourceKg: 0}
	for i, l := range lines {
		if l.Source != wantOrder[i] {
			t.Errorf("第 %d 条线路 = %s, want %s（App tab 顺序）", i, l.Source, wantOrder[i])
		}
		if l.Alias == "" || l.Title == "" {
			t.Errorf("%s 缺别名/标题: %+v", l.Source, l)
		}
		if len(l.Categories) != wantCats[l.Source] {
			t.Errorf("%s 分类数 = %d, want %d", l.Source, len(l.Categories), wantCats[l.Source])
		}
		if len(l.Categories) > 0 && (l.Categories[0].ID == "" || l.Categories[0].Name == "") {
			t.Errorf("%s 首个分类不完整: %+v", l.Source, l.Categories[0])
		}
	}
	// tx 用服务端分类树：首项「国语」，末项「甜蜜」
	txCats := PlaylistCategoriesOf(SourceTx)
	if txCats[0].Name != "国语" || txCats[19].Name != "甜蜜" {
		t.Fatalf("tx 分类表应与服务端树一致: %q..%q", txCats[0].Name, txCats[19].Name)
	}
}

// 分类解析：空=首项、id/名字命中、未知报错；kg 无分类表。
func TestResolvePlaylistCategory(t *testing.T) {
	if got, err := ResolvePlaylistCategory(SourceTx, ""); err != nil || got != "1" {
		t.Errorf("tx 默认分类 = %q, %v; want 1(国语)", got, err)
	}
	if got, err := ResolvePlaylistCategory(SourceKw, "游戏"); err != nil || got != "1877" {
		t.Errorf("kw 名字解析 = %q, %v; want 1877", got, err)
	}
	if got, err := ResolvePlaylistCategory(SourceWy, "民谣"); err != nil || got != "民谣" {
		t.Errorf("wy 名字解析 = %q, %v", got, err)
	}
	if got, err := ResolvePlaylistCategory(SourceTx, "伤感"); err != nil || got != "12" {
		t.Errorf("tx 名字解析 = %q, %v; want 12", got, err)
	}
	if _, err := ResolvePlaylistCategory(SourceTx, "不存在"); err == nil {
		t.Error("未知分类应报错")
	}
	if _, err := ResolvePlaylistCategory(SourceKg, "推荐"); err == nil {
		t.Error("kg 无分类表，任何分类都应报错")
	}
	if _, ok := ParsePlaylistLine("小绿"); !ok {
		t.Error("线路别名「小绿」应可解析")
	}
	if _, ok := ParsePlaylistLine("bili"); ok {
		t.Error("bili 不是歌单线路")
	}
}
