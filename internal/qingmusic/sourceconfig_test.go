package qingmusic

// 音源配置的语义测试。
//
// 这里断言的都是**从 APK 字节码里读出来的行为**，不是拍脑袋定的规矩，
// 所以每条都用注释标出对应的函数号，方便日后拿新版本 APK 复核：
//
//	#8529 readSourceLines          #9129 getImportedConfig
//	#8531 归一化                    #8534 getStoredQualityForSource
//	#8538 对账                      #8554 设置音质
//	#12879 档位→标签/码率

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// #9129/#12874：解包规则是 `parsed.config || parsed`，所以这些形态都要能读。
func TestParseSourceConfigShapes(t *testing.T) {
	const body = `"lines":[{"id":"kw","levels":["standard"]}]`

	cases := []struct {
		name string
		in   string
	}{
		{"config 包一层（官方形态）", `{"config":{` + body + `}}`},
		{"直接给 lines", `{` + body + `}`},
		{"config 下直接是数组", `{"config":[{"id":"kw","levels":["standard"]}]}`},
		{"裸数组", `[{"id":"kw","levels":["standard"]}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseSourceConfig([]byte(c.in))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(cfg.Lines) != 1 || cfg.Lines[0].ID != "kw" {
				t.Fatalf("期望 1 个 kw 音源，得到 %+v", cfg.Lines)
			}
			if !cfg.Lines[0].Enabled {
				t.Fatal("enabled 缺省应为 true")
			}
			if got := cfg.Lines[0].Levels; !reflect.DeepEqual(got, []string{"standard"}) {
				t.Fatalf("levels = %v", got)
			}
		})
	}
}

// #10775 逐字段：id 去空格转小写、name 缺省用 id、enabled 缺省 true、
// levels 走 String() 再 filter(Boolean)、searchApi/detailApi 只 trim 不小写。
func TestNormalizeSourceLine(t *testing.T) {
	in := `{"lines":[
		{"id":" KW ","name":"","levels":["standard","exhigh"],"searchApi":" fetchSearchMusic ","detailApi":"fetchMusicDetail"},
		{"id":"KG","enabled":false,"levels":"not-an-array"},
		{"id":"wy","enabled":true},
		{"id":""}
	]}`
	cfg, err := ParseSourceConfig([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	// 第 4 条 id 为空 —— App 靠 filter(x => x) 之后的 find 永不命中，
	// 这里等价地直接丢弃，避免出现永远匹配不上的幽灵音源。
	if len(cfg.Lines) != 3 {
		t.Fatalf("期望 3 条（空 id 被丢弃），得到 %d: %+v", len(cfg.Lines), cfg.Lines)
	}

	kw := cfg.Lines[0]
	if kw.ID != "kw" {
		t.Fatalf(`id 应 trim+小写，得到 %q`, kw.ID)
	}
	if kw.Name != "kw" {
		t.Fatalf(`name 为空时应回退到 id，得到 %q`, kw.Name)
	}
	want := []string{"standard", "exhigh"}
	if !reflect.DeepEqual(kw.Levels, want) {
		t.Fatalf("levels = %v，期望 %v", kw.Levels, want)
	}
	// searchApi 是函数名，大小写敏感 —— 只能 trim，绝不能像 id 那样小写化。
	if kw.SearchAPI != "fetchSearchMusic" {
		t.Fatalf("searchApi = %q，应 trim 但保持大小写", kw.SearchAPI)
	}
	if kw.DetailAPI != "fetchMusicDetail" {
		t.Fatalf("detailApi = %q", kw.DetailAPI)
	}
	if !kw.Searchable() || !kw.DetailCapable() {
		t.Fatal("写了 searchApi/detailApi 应判定为可用")
	}

	kg := cfg.Lines[1]
	if kg.Enabled {
		t.Fatal("显式 enabled:false 应生效")
	}
	if len(kg.Levels) != 0 {
		t.Fatalf("levels 不是数组时应为 []，得到 %v", kg.Levels)
	}
	if kg.Searchable() {
		t.Fatal("没写 searchApi 不该判定为可搜")
	}

	wy := cfg.Lines[2]
	if !wy.Enabled {
		t.Fatal("显式 enabled:true 应生效")
	}
	if len(wy.Levels) != 0 {
		t.Fatalf("缺 levels 时应为 []，得到 %v", wy.Levels)
	}
}

// 一行写坏了只丢那一行，不该让整份配置解析失败（App 的 filter(x => x) 也是逐项语义）。
func TestDecodeSourceLinesIsolatesBadLines(t *testing.T) {
	in := `{"lines":[
		{"id":"kw","levels":["standard"]},
		{"id":"kg","levels":[320,"standard"]},
		{"id":"bad","levels":"我写错了"},
		"我甚至不是个对象",
		{"id":"mg","levels":["standard"]}
	]}`
	cfg, err := ParseSourceConfig([]byte(in))
	if err != nil {
		t.Fatalf("坏行不该让整份配置失败: %v", err)
	}
	got := make([]string, 0, len(cfg.Lines))
	for _, l := range cfg.Lines {
		got = append(got, l.ID)
	}
	want := []string{"kw", "kg", "bad", "mg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("存活行 = %v，期望 %v", got, want)
	}
	// 数字 320 走 map(String) → "320"（App 的 Array.isArray 分支就是这么做的）。
	if k := cfg.Lines[1]; !reflect.DeepEqual(k.Levels, []string{"320", "standard"}) {
		t.Fatalf("kg levels = %v，期望 [320 standard]", k.Levels)
	}
	// levels 不是数组 → 当空，但这一行本身照常保留。
	if b := cfg.Lines[2]; len(b.Levels) != 0 {
		t.Fatalf("非数组 levels 应为空，得到 %v", b.Levels)
	}
}

// 真实配置（用户的 music.json）要能原样读进来，字段一个不丢。
func TestParseRealWorldConfig(t *testing.T) {
	const real = `{
	  "lines": [
	    {"id":"kw","name":"小窝源","enabled":true,"searchApi":"fetchSearchMusic","detailApi":"fetchMusicDetail",
	     "levels":["standard","exhigh","lossless","atmos","atmos_plus","master"]},
	    {"id":"kg","name":"小狗源","enabled":true,"searchApi":"kgSearchMusic","detailApi":"kgMusicDetail",
	     "levels":["standard","exhigh","lossless","hires","atmos","clear"]},
	    {"id":"wy","name":"小云源","enabled":true,"searchApi":"wySearchMusic","detailApi":"wyMusicDetail",
	     "levels":["standard","exhigh","lossless","hires","sky","jyeffect","jymaster"]},
	    {"id":"tx","name":"小Q源","enabled":true,"searchApi":"txSearchMusic","detailApi":"txMusicDetail",
	     "levels":["standard","exhigh","lossless"]},
	    {"id":"bili","name":"哔哩哔哩","enabled":true,"searchApi":"biliSearchMusic","detailApi":"biliMusicDetail",
	     "levels":["bili192"]},
	    {"id":"mg","name":"小粉源","enabled":true,"searchApi":"mgSearchMusic","detailApi":"mgMusicDetail",
	     "levels":["standard"]}
	  ]
	}`
	cfg, err := ParseSourceConfig([]byte(real))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Lines) != 6 {
		t.Fatalf("应有 6 个音源，得到 %d", len(cfg.Lines))
	}

	// kw 的 searchApi 是通用名（不是 kwSearchMusic）—— 这条容易写反。
	if l, _ := cfg.Line("kw"); l.SearchAPI != SearchAPIFetch || l.DetailAPI != DetailAPIFetch {
		t.Fatalf("kw 的实现名不对: %s / %s", l.SearchAPI, l.DetailAPI)
	}
	// tx 只到 lossless，mg 只有 standard。
	if l, _ := cfg.Line("tx"); !reflect.DeepEqual(cfg.AllowedLevels(l.ID), []string{"lossless", "exhigh", "standard"}) {
		t.Fatalf("tx 档位不对: %v", cfg.AllowedLevels(l.ID))
	}
	if got := cfg.AllowedLevels("mg"); !reflect.DeepEqual(got, []string{"standard"}) {
		t.Fatalf("mg 档位不对: %v", got)
	}
	// mg 只有 standard → 默认档落到 standard。
	if got := cfg.PreferredLevel("mg"); got != LevelStandard {
		t.Fatalf("mg 默认档 = %q，期望 standard", got)
	}
	// kw 支持 standard → 默认就是 standard（128kbps；不越级去试 lossless/atmos/master）。
	if got := cfg.PreferredLevel("kw"); got != LevelStandard {
		t.Fatalf("kw 默认档 = %q，期望 standard", got)
	}

	for _, l := range cfg.Lines {
		if !l.Searchable() {
			t.Fatalf("%s 缺 searchApi", l.ID)
		}
	}
}

// 注册表：配置里的 searchApi 名要能查到实现，查不到要有说得出原因的报错
// （复刻 App 的 #11849「{音源名}搜索接口不可用」）。
func TestSearcherRegistry(t *testing.T) {
	s := NewSearcher()
	for _, name := range []string{SearchAPIFetch, SearchAPIKg, SearchAPIWy, SearchAPITx, SearchAPIMg} {
		if !s.HasAPI(name) {
			t.Errorf("%s 应该已注册", name)
		}
	}
	// bili 我们知道名字但没实现 —— 不能谎报有实现。
	if s.HasAPI(SearchAPIBili) {
		t.Error("biliSearchMusic 尚未实现，不该出现在注册表里")
	}
	if got, want := len(s.APIs()), 5; got != want {
		t.Fatalf("已注册 %d 个，期望 %d: %v", got, want, s.APIs())
	}

	cfg, _ := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","enabled":true,"searchApi":"fetchSearchMusic"},
		{"id":"bili","enabled":true,"searchApi":"biliSearchMusic"},
		{"id":"nope","enabled":true},
		{"id":"off","enabled":false,"searchApi":"fetchSearchMusic"}
	]}`))

	if got := len(cfg.SearchableLines(s)); got != 1 {
		t.Fatalf("可搜音源应有 1 个（kw），得到 %d", got)
	}
	if got := len(cfg.UnsearchableLines(s)); got != 2 {
		t.Fatalf("不可搜的启用音源应有 2 个（bili/nope），得到 %d", got)
	}

	// bili：名字知道、实现没有 → 必须报「未实现」，而不是空结果或「未配置」。
	bili := cfg.Lines[1]
	if _, err := s.SearchByLine(context.Background(), bili, "x", 1); err == nil {
		t.Fatal("bili 搜索应报错")
	} else if !strings.Contains(err.Error(), SearchAPIBili) {
		t.Fatalf("错误信息该点名缺哪个实现，得到: %v", err)
	}

	// 没写 searchApi：跟「写了没实现」是两回事，提示也要不一样。
	if _, err := s.SearchByLine(context.Background(), cfg.Lines[2], "x", 1); err == nil {
		t.Fatal("没写 searchApi 应报错")
	} else if !strings.Contains(err.Error(), "没写") {
		t.Fatalf("该提示配置缺失，得到: %v", err)
	}

	// 关闭的音源直接拒绝，连注册表都不用查。
	if _, err := s.SearchByLine(context.Background(), cfg.Lines[3], "x", 1); err == nil {
		t.Fatal("已关闭音源应报错")
	} else if !strings.Contains(err.Error(), "已关闭") {
		t.Fatalf("该提示已关闭，得到: %v", err)
	}
}

// detailApi：名字都认得，但一个实现都没有 —— 这正是当前的已知缺口。
// Searcher.Register 支持换实现与注销。
func TestSearcherRegisterOverride(t *testing.T) {
	s := NewSearcher()
	called := false
	s.Register("customSearch", func(ctx context.Context, kw string, page int) ([]Track, error) {
		called = true
		return []Track{{Source: "x", Rid: "1"}}, nil
	})
	line := SourceLine{ID: "x", Name: "X", Enabled: true, SearchAPI: "customSearch"}
	got, err := s.SearchByLine(context.Background(), line, "hi", 1)
	if err != nil || !called || len(got) != 1 {
		t.Fatalf("自定义实现没被调用: err=%v called=%v got=%v", err, called, got)
	}

	s.Register("customSearch", nil) // 注销
	if s.HasAPI("customSearch") {
		t.Fatal("注销后不该还在注册表里")
	}
	if _, err := s.SearchByLine(context.Background(), line, "hi", 1); err == nil {
		t.Fatal("注销后应报错")
	}
}

// Search 的老接口（按 source）要跟注册表保持一致 —— kw 走的是通用名。
func TestSearchBySourceUsesRegistry(t *testing.T) {
	s := NewSearcher()
	if _, err := s.Search(context.Background(), "不存在的音源", "x", 1); err == nil {
		t.Fatal("未知 source 应报错")
	}
	// bili 有 source 常量但没有实现 → 报错应当点名 biliSearchMusic。
	if _, err := s.Search(context.Background(), SourceBili, "x", 1); err == nil {
		t.Fatal("bili 未实现仍应报错")
	} else if !strings.Contains(err.Error(), SearchAPIBili) {
		t.Fatalf("错误信息该点名 %s，得到: %v", SearchAPIBili, err)
	}
}

// #8534：首选档位该音源给不了 → 退到它支持的最高档；没这条音源 → 原样返回。
func TestResolveLevel(t *testing.T) {
	cfg, err := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","levels":["standard","exhigh","lossless","hires"]},
		{"id":"noLevels"},
		{"id":"off","enabled":false,"levels":["hires"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		source, preferred, want string
		wantOK                  bool
		why                     string
	}{
		{"kw", "lossless", "lossless", true, "首选被支持 → 原样"},
		{"kw", "atmos", "hires", true, "首选不被支持 → 退到最高档"},
		{"kw", "", "standard", true, "没给首选 → 默认档 standard（支持 standard 时不越级）"},
		{"kw", "standard", "standard", true, "最低档也要保得住，不能往上跳"},
		{"noLevels", "atmos", "atmos", true, "levels 为空 = 不限制，原样返回"},
		{"off", "lossless", "lossless", false, "音源已关闭 → 不可用且不改档位"},
		{"nope", "lossless", "lossless", false, "配置里没这条音源 → 不可用"},
	}
	for _, c := range cases {
		got, ok := cfg.ResolveLevel(c.source, c.preferred)
		if got != c.want {
			t.Errorf("%s/%q: 档位 = %q，期望 %q（%s）", c.source, c.preferred, got, c.want, c.why)
		}
		if ok != c.wantOK {
			t.Errorf("%s/%q: 可用性 = %v，期望 %v（%s）", c.source, c.preferred, ok, c.wantOK, c.why)
		}
	}
}

// LevelsFor：从首选档起、按音质从高到低给出降级链。
// 这条链取代了 download.go 里写死的 lossless→exhigh→standard。
func TestLevelsFor(t *testing.T) {
	cfg, err := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","levels":["standard","exhigh","lossless","hires"]},
		{"id":"lofi","levels":["standard"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := cfg.LevelsFor("kw", ""), []string{"standard"}; !reflect.DeepEqual(got, want) {
		t.Errorf("不给首选: %v，期望 %v（默认 standard，不越级去试更高档）", got, want)
	}
	if got, want := cfg.LevelsFor("kw", "exhigh"), []string{"exhigh", "standard"}; !reflect.DeepEqual(got, want) {
		t.Errorf("给 exhigh: %v，期望 %v（只往下退，不越级）", got, want)
	}
	if got, want := cfg.LevelsFor("kw", "atmos"), []string{"hires", "lossless", "exhigh", "standard"}; !reflect.DeepEqual(got, want) {
		t.Errorf("首选不被支持: %v，期望整条链 %v", got, want)
	}
	// 关键收益：只支持一档的音源不会被白打两次请求。
	if got, want := cfg.LevelsFor("lofi", ""), []string{"standard"}; !reflect.DeepEqual(got, want) {
		t.Errorf("单档音源: %v，期望 %v", got, want)
	}
	if got := cfg.LevelsFor("nope", "lossless"); !reflect.DeepEqual(got, []string{"lossless"}) {
		t.Errorf("未知音源应原样返回首选，得到 %v", got)
	}
}

// #12789 / #12879：档位权重表。未知档位必须排在最后。
func TestLevelRank(t *testing.T) {
	if LevelRankOf(LevelStandard) >= LevelRankOf(LevelHires) {
		t.Fatal("standard 应低于 hires")
	}
	if LevelRankOf(LevelHires) >= LevelRankOf(LevelJYMaster) {
		t.Fatal("hires 应低于 jymaster")
	}
	if LevelRankOf("这不是档位") != 0 {
		t.Fatal("未知档位权重应为 0（永远最后才试）")
	}
	if _, bitrate := LevelLabel(LevelBili192); bitrate != "约192kbps · M4A" {
		t.Fatalf("bili192 的码率标签不对: %q", bitrate)
	}
}

// #8538：导入新配置后，已选但不再被支持的档位要被改写成最高档。
func TestReconcile(t *testing.T) {
	cfg, err := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","levels":["standard","exhigh","lossless"]}
	],"quality":{"kw":"hires","ghost":"hires"}}`))
	if err != nil {
		t.Fatal(err)
	}
	changed := cfg.Reconcile()
	if !reflect.DeepEqual(changed, []string{"kw"}) {
		t.Fatalf("被改写的音源 = %v，期望 [kw]（ghost 不在配置里，应跳过）", changed)
	}
	if cfg.Quality["kw"] != "lossless" {
		t.Fatalf("kw 应被改写为 lossless，得到 %q", cfg.Quality["kw"])
	}
	if cfg.Quality["ghost"] != "hires" {
		t.Fatalf("配置里没有的音源不该被动，得到 %q", cfg.Quality["ghost"])
	}
}

// 至少保留一个音源（对应 UI 文案「至少保留一个音源」）。
func TestSetEnabledKeepsAtLeastOne(t *testing.T) {
	cfg, _ := ParseSourceConfig([]byte(`{"lines":[{"id":"a"},{"id":"b"}]}`))
	if err := cfg.SetEnabled("a", false); err != nil {
		t.Fatalf("关掉 a 后还剩 b，不该报错: %v", err)
	}
	if err := cfg.SetEnabled("b", false); err == nil {
		t.Fatal("关掉最后一个音源应报错")
	}
	if err := cfg.SetEnabled("nope", false); err == nil {
		t.Fatal("操作不存在的音源应报错")
	}
}

// #8554：设置音质会校验该音源是否支持。
func TestSetQualityRejectsUnsupported(t *testing.T) {
	cfg, _ := ParseSourceConfig([]byte(`{"lines":[{"id":"kw","levels":["standard","lossless"]}]}`))
	if _, err := cfg.SetQuality("kw", "lossless"); err != nil {
		t.Fatalf("lossless 是支持的，不该报错: %v", err)
	}
	if _, err := cfg.SetQuality("kw", "atmos"); err == nil {
		t.Fatal("atmos 不被支持，应报错")
	}
	if _, err := cfg.SetQuality("ghost", "lossless"); err == nil {
		t.Fatal("音源不存在，应报错")
	}
}

// 存盘再读要能保住 quality —— 这条曾经真的丢过（第一次实现漏解析 quality 字段）。
func TestSourceConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "music.json")

	cfg, _ := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","name":"酷我","levels":["standard","lossless"]},
		{"id":"kg","enabled":false,"levels":["standard"]}
	],"quality":{"kw":"lossless"}}`))
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	// 落盘形态要能被 App 吃下（无 config 包裹时走 `parsed.config || parsed` 的后者）。
	raw, _ := os.ReadFile(path)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("落盘结果不是合法 JSON: %v", err)
	}
	if _, ok := probe["lines"]; !ok {
		t.Fatal("落盘应包含 lines 键")
	}

	back, err := LoadSourceConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Lines, cfg.Lines) {
		t.Fatalf("lines 往返不一致:\n got %+v\nwant %+v", back.Lines, cfg.Lines)
	}
	if back.Quality["kw"] != "lossless" {
		t.Fatalf("quality 往返丢失，得到 %+v", back.Quality)
	}
}

// 文件不存在时要返回 ErrNoSourceConfig —— 对齐 App：App 没有内置音源，
// 缺配置的文案是「未找到音源配置 / 请先在"我的"->"导入音源配置"中导入音源配置文件」。
func TestLoadMissingReturnsErrNoSourceConfig(t *testing.T) {
	cfg, err := LoadSourceConfig(filepath.Join(t.TempDir(), "nope.json"))
	if !IsNoSourceConfig(err) {
		t.Fatalf("文件不存在应返回 ErrNoSourceConfig，得到 cfg=%+v err=%v", cfg, err)
	}
	if cfg != nil {
		t.Fatalf("配置缺失时不该返回一份配置，得到 %+v", cfg)
	}

	// 便捷版才退回示例表（这是本库行为，不是 App 行为）。
	ex, err := LoadSourceConfigOrExample(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("LoadSourceConfigOrExample 不该报错: %v", err)
	}
	if len(ex.Lines) != 6 {
		t.Fatalf("示例表应有 6 个平台，得到 %d", len(ex.Lines))
	}
	for _, id := range []string{SourceKw, SourceKg, SourceWy, SourceTx, SourceBili, SourceMg} {
		if !ex.Enabled(id) {
			t.Fatalf("%s 在示例表里应为启用", id)
		}
		if best := ex.BestLevel(id); best == "" {
			t.Fatalf("%s 示例档位不该为空", id)
		}
	}
	// 示例表按真实配置照抄：kw 用通用函数名，mg 只有 standard。
	if l, _ := ex.Line(SourceKw); l.SearchAPI != "fetchSearchMusic" || l.DetailAPI != "fetchMusicDetail" {
		t.Fatalf("kw 应使用通用实现名，得到 %q/%q", l.SearchAPI, l.DetailAPI)
	}
	if l, _ := ex.Line(SourceMg); len(l.Levels) != 1 || l.Levels[0] != LevelStandard {
		t.Fatalf("mg 示例应只有 standard，得到 %v", l.Levels)
	}
}

// PreferredLevel：显式设置优先；没设过时退到 standard（128kbps 默认档），
// 不支持 standard 的音源退到它自己的最高档。
func TestPreferredLevel(t *testing.T) {
	cfg, err := ParseSourceConfig([]byte(`{"lines":[
		{"id":"kw","levels":["standard","exhigh","lossless","hires","jymaster"]},
		{"id":"lofi","levels":["lossless","hires"]},
		{"id":"bili","levels":["bili192"]}
	],"quality":{"kw":"hires"}}`))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct{ source, want string }{
		{"kw", "hires"},     // 显式设过 → 用设置值（哪怕不是最高档）
		{"lofi", "hires"},   // 不支持 standard → 退到自己的最高档（hires > lossless）
		{"bili", "bili192"}, // 同上
		{"nope", ""},        // 没这条音源
	}
	for _, c := range cases {
		if got := cfg.PreferredLevel(c.source); got != c.want {
			t.Errorf("PreferredLevel(%q) = %q，期望 %q", c.source, got, c.want)
		}
	}

	// 没设过 quality 且支持 standard → 默认 standard（128kbps），不越级去试 jymaster（158MB 母带）。
	cfg2, _ := ParseSourceConfig([]byte(`{"lines":[{"id":"wy","levels":["standard","lossless","jymaster"]}]}`))
	if got := cfg2.PreferredLevel("wy"); got != LevelStandard {
		t.Fatalf("默认档应为 standard，得到 %q", got)
	}
	if got, want := cfg2.LevelsFor("wy", ""), []string{"standard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("默认降级链 = %v，期望 %v", got, want)
	}
}
