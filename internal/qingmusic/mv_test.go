package qingmusic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------- URL 构造（移植自 qingmusic-go mv_test.go 的关键回归点）

// 三条具名线路 + default(小绿)：认不出的线路落 QQ。
func TestMVInfoURLByLine(t *testing.T) {
	const id = "001UZToL4FDJ9b"
	cases := []struct {
		line string
		want string
	}{
		{LineKg, "https://music.haitangw.cc/MV/KGMV.php?id=" + id + "&action=info"},
		{LineWy, "https://music.haitangw.cc/MV/WYMV.php?id=" + id + "&action=info"},
		{LineKw, "https://music.haitangw.cc/MV/KWMV.php?id=" + id + "&action=info"},
		{LineTx, "https://music.haitangw.cc/qq/QQMV.php?id=" + id + "&action=info"},
		{"没听过的线路", "https://music.haitangw.cc/qq/QQMV.php?id=" + id + "&action=info"},
	}
	for _, c := range cases {
		if got := MVInfoURL(c.line, id); got != c.want {
			t.Errorf("MVInfoURL(%q)\n got %s\nwant %s", c.line, got, c.want)
		}
	}
}

// 小绿(QQ) 分支不带 quality 参数（App 实际行为）。
func TestMVPlaybackURLByLine(t *testing.T) {
	const id = "001UZToL4FDJ9b"
	if got, want := MVPlaybackURL(LineKw, id, "1080"),
		"https://music.haitangw.cc/MV/KWMV.php?id="+id+"&quality=1080"; got != want {
		t.Errorf("kw 应带 quality\n got %s\nwant %s", got, want)
	}
	if got, want := MVPlaybackURL(LineTx, id, "1080"),
		"https://music.haitangw.cc/qq/QQMV.php?id="+id; got != want {
		t.Errorf("小绿不该带 quality\n got %s\nwant %s", got, want)
	}
	if got, want := MVPlaybackURL(LineKg, id, ""),
		"https://music.haitangw.cc/MV/KGMV.php?id="+id; got != want {
		t.Errorf("空 quality 不该拼参数\n got %s\nwant %s", got, want)
	}
}

// 搜索 URL 必须带 &page=（回归：早期实现漏了它，翻页永远拿第一页）。
func TestMVSearchURLPage(t *testing.T) {
	if got := MVSearchURL(LineTx, "雨滴", 3); !strings.HasSuffix(got, "&page=3") {
		t.Fatalf("页码未进入 URL: %s", got)
	}
	if got := MVSearchURL(LineTx, "x", 0); !strings.HasSuffix(got, "&page=1") {
		t.Fatalf("page<1 未归一到 1: %s", got)
	}
	paths := map[string]string{
		LineKg: "/MV/KGMV.php?name=",
		LineKw: "/MV/KWMV.php?name=",
		LineWy: "/MV/WYMV.php?name=",
		LineTx: "/qq/QQMV.php?name=",
	}
	for line, p := range paths {
		if got := MVSearchURL(line, "x", 2); !strings.Contains(got, p) {
			t.Fatalf("线路 %s 端点错: %s", line, got)
		}
	}
}

func TestHotMVURL(t *testing.T) {
	if got, want := HotMVURL(1, 15),
		"https://music.haitangw.cc/MV/qqmv_new_hot.php?type=hot&page=1&size=15"; got != want {
		t.Fatalf(" got %s\nwant %s", got, want)
	}
	if got := HotMVURL(0, 0); !strings.Contains(got, "page=1&size=15") {
		t.Fatalf("page/size 未归一到默认值: %s", got)
	}
}

// ---------------------------------------------------------------- 归一化

// 只有 vid 是必需的，缺了才丢弃；http 封面强制升 https。
func TestNormalizeHotMVItem(t *testing.T) {
	if _, ok := NormalizeHotMVItem(map[string]any{"title": "没有 vid"}); ok {
		t.Fatal("缺 vid 应被丢弃")
	}
	it, ok := NormalizeHotMVItem(map[string]any{
		"vid": "v1", "title": "歌", "singer": "人",
		"cover": "http://img.example/x.jpg", "duration": 215, "play_count": "12345",
	})
	if !ok {
		t.Fatal("完整项不应被丢弃")
	}
	if it.ID != "v1" || it.Name != "歌" || it.Artist != "人" {
		t.Fatalf("字段归一化错误: %+v", it)
	}
	if it.Pic != "https://img.example/x.jpg" {
		t.Fatalf("http 封面应升级 https: %s", it.Pic)
	}
	if it.Duration != "03:35" {
		t.Fatalf("秒制时长格式化错误: %s", it.Duration)
	}
	if it.PlayCount != 12345 {
		t.Fatalf("字符串播放量应宽松转换: %d", it.PlayCount)
	}
	if it.Source != SourceTx || it.Line != LineTx {
		t.Fatalf("热门 MV 固定 QQ 源: %+v", it)
	}
}

// 搜索与热门是两套字段规则：毫秒制时长、封面不升级、source 由线路推出。
func TestNormalizeMVSearchItem(t *testing.T) {
	it := NormalizeMVSearchItem(map[string]any{
		"id": "42", "name": "雨滴", "artist": "",
		"cover": "http://img.example/y.jpg", "duration": 215000, "playcount": 99,
	}, LineWy)
	if it.ID != "42" || it.Name != "雨滴" {
		t.Fatalf("字段错误: %+v", it)
	}
	if it.Artist != "未知歌手" {
		t.Fatalf("空歌手应兜底: %s", it.Artist)
	}
	if it.Duration != "03:35" {
		t.Fatalf("毫秒制时长格式化错误: %s", it.Duration)
	}
	if it.Pic != "http://img.example/y.jpg" {
		t.Fatalf("搜索结果封面不做 https 升级: %s", it.Pic)
	}
	if it.Source != SourceWy || it.Line != LineWy {
		t.Fatalf("source 应由线路推出: %+v", it)
	}
}

// 严校验：null 字面量、HTML 错误页、控制字符、非法 scheme 一律拒绝。
func TestNormalizeMVVideoURL(t *testing.T) {
	bad := []string{"", "null", "undefined", "false", "<html>", "[{\"a\":1}]",
		"<!doctype html>", "ht tp://x/a.mp4", "javascript:alert(1)", "http://\x01x"}
	for _, s := range bad {
		if _, ok := NormalizeMVVideoURL(s); ok {
			t.Fatalf("%q 应判无效", s)
		}
	}
	good := []string{" http://mv.qq.com/a.mp4 ", "https://x.cn/v.mp4?sign=1#f"}
	for _, s := range good {
		if _, ok := NormalizeMVVideoURL(s); !ok {
			t.Fatalf("%q 应判有效", s)
		}
	}
}

// 画质排序：按权重升序，同名次按名字（输出确定性）。
func TestSortQualitiesAndPick(t *testing.T) {
	qs := []MVQuality{
		{Name: "原画", URL: "u1"}, {Name: "标清画质", URL: "u2"}, {Name: "高清画质", URL: "u3"},
	}
	sortQualities(qs)
	if qs[0].Name != "标清画质" || qs[2].Name != "原画" {
		t.Fatalf("排序错误: %+v", qs)
	}
	// 指定画质：精确命中
	if q, err := PickQuality(qs, "高清画质"); err != nil || q.URL != "u3" {
		t.Fatalf("指定画质选择错误: %+v %v", q, err)
	}
	// 指定不存在的画质：报错并列可用项
	if _, err := PickQuality(qs, "8K"); err == nil || !strings.Contains(err.Error(), "标清画质") {
		t.Fatalf("未知画质应报错并列可用项: %v", err)
	}
	// 不指定：自动挑权重最高
	if q, err := PickQuality(qs, ""); err != nil || q.Name != "原画" {
		t.Fatalf("自动画质应取最高: %+v %v", q, err)
	}
}

// 线路名 ↔ 音源 id 双向映射。
func TestLineSourceMapping(t *testing.T) {
	for src, line := range map[string]string{
		SourceKw: LineKw, SourceKg: LineKg, SourceWy: LineWy, SourceTx: LineTx, SourceMg: LineMg,
	} {
		if got := LineNameOf(src); got != line {
			t.Errorf("LineNameOf(%s) = %s, want %s", src, got, line)
		}
		if got := SourceOfLine(line); got != src {
			t.Errorf("SourceOfLine(%s) = %s, want %s", line, got, src)
		}
	}
	if LineNameOf("bili") != "" || SourceOfLine("未知线路") != "" {
		t.Error("未知值应返回空串，不猜")
	}
}
