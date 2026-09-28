package pronunciation

import (
	"os"
	"path/filepath"
	"testing"
)

// seed 构造独立实例并写入词条，绕过进程单例。
func seed(t *testing.T, entries ...Entry) *Store {
	t.Helper()
	s := New(filepath.Join(t.TempDir(), "pronunciation.json"))
	for _, e := range entries {
		if _, err := s.Add(e.Term, e.Replacement, e.Language, true); err != nil {
			t.Fatalf("Add(%q): %v", e.Term, err)
		}
	}
	return s
}

// applyOn 独立实例上的 Apply（不经进程单例）。
func applyOn(s *Store, text, language string) string {
	if d := buildDictionary(s.entriesFor(langPrefix(language))); d != nil {
		text = d.replace(text)
	}
	return applyInline(text)
}

func TestApplyLatinLongestFirst(t *testing.T) {
	s := seed(t,
		Entry{Term: "Dr", Replacement: "Doctor", Language: "*"},
		Entry{Term: "Dr. Smith", Replacement: "Smith doctor", Language: "*"},
	)
	// 长词条优先；短词条标点结尾场景由 "Dr" 自身演示（首尾均词字符 → 双侧边界）
	got := applyOn(s, "Dr. Smith and Dr Wu", "")
	want := "Smith doctor and Doctor Wu"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyLatinWordBoundary(t *testing.T) {
	s := seed(t, Entry{Term: "cat", Replacement: "kat", Language: "*"})
	if got, want := applyOn(s, "the category of cat, cats", ""), "the category of kat, cats"; got != want {
		t.Fatalf("got %q, want %q（cat 不得命中 category/cats）", got, want)
	}
}

func TestApplyCaseInsensitiveAndPunctuationKept(t *testing.T) {
	s := seed(t, Entry{Term: "GIF", Replacement: "jiff", Language: "*"})
	if got, want := applyOn(s, "A Gif file, GIFs abound", ""), "A jiff file, GIFs abound"; got != want {
		t.Fatalf("got %q, want %q（GIFs 是另一个词，不命中 GIF）", got, want)
	}
}

func TestApplyCJKNoBoundary(t *testing.T) {
	s := seed(t, Entry{Term: "重庆", Replacement: "chóngqìng", Language: "zh"})
	// CJK 词条不做边界检查：更长中文词组内也要命中
	if got, want := applyOn(s, "重庆火锅很好吃", "zh"), "chóngqìng火锅很好吃"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyLatinKeyCJKNeighbor(t *testing.T) {
	s := seed(t, Entry{Term: "GIF", Replacement: "jiff", Language: "*"})
	// 邻接汉字不算词字符：GIF 图 可命中
	if got, want := applyOn(s, "发个GIF图", ""), "发个jiff图"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplySinglePassIdempotent(t *testing.T) {
	s := seed(t,
		Entry{Term: "a", Replacement: "ab", Language: "*"},
		Entry{Term: "ab", Replacement: "b", Language: "*"},
	)
	// 产物不重扫："a"→"ab" 后不会因词条 "ab" 再变 "b"
	if got, want := applyOn(s, "a", ""), "ab"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyLanguageScoping(t *testing.T) {
	s := seed(t,
		Entry{Term: "lead", Replacement: "liːd", Language: "en"},
		Entry{Term: "长安", Replacement: "cháng'ān", Language: "*"},
	)
	if got, want := applyOn(s, "read lead at 长安", "en"), "read liːd at cháng'ān"; got != want {
		t.Fatalf("en: got %q, want %q", got, want)
	}
	// en 词条不命中中文请求；全语言词条仍生效
	if got, want := applyOn(s, "lead 长安", "zh"), "lead cháng'ān"; got != want {
		t.Fatalf("zh: got %q, want %q", got, want)
	}
	// 未指定语言：仅全语言词条
	if got, want := applyOn(s, "lead 长安", ""), "lead cháng'ān"; got != want {
		t.Fatalf("no-lang: got %q, want %q", got, want)
	}
	// zh-CN 截前缀；全称 Chinese 走别名表
	for _, l := range []string{"zh-CN", "Chinese", "chinese"} {
		if got := applyOn(s, "长安", l); got != "cháng'ān" {
			t.Fatalf("lang %q: got %q", l, got)
		}
	}
}

func TestApplyDisabledSkipped(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "p.json"))
	if _, err := s.Add("foo", "bar", "*", true); err != nil {
		t.Fatal(err)
	}
	e, err := s.Add("baz", "qux", "*", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(e.ID, "baz", "qux", "*", false); err != nil {
		t.Fatal(err)
	}
	if got, want := applyOn(s, "foo baz", ""), "bar baz"; got != want {
		t.Fatalf("got %q, want %q（停用词条不得生效）", got, want)
	}
}

func TestApplyInlineOverrides(t *testing.T) {
	s := seed(t, Entry{Term: "gif", Replacement: "jiff", Language: "*"})
	if got, want := applyOn(s, "[[gif|giff]] 动图", ""), "giff 动图"; got != want {
		t.Fatalf("行内带原词: got %q, want %q", got, want)
	}
	if got, want := applyOn(s, "读作[[Nuh-VAD-uh]]的城市", ""), "读作Nuh-VAD-uh的城市"; got != want {
		t.Fatalf("行内纯读音: got %q, want %q", got, want)
	}
	// 单层括号不受影响
	if got, want := applyOn(s, "[pause] then [[gif|jiff]]", ""), "[pause] then jiff"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := applyOn(s, "empty[[]]gone", ""), "emptygone"; got != want {
		t.Fatalf("空标注: got %q, want %q", got, want)
	}
	// 未闭合的 [[ 不动原文
	if got, want := applyOn(s, "open [[ bracket", ""), "open [[ bracket"; got != want {
		t.Fatalf("未闭合: got %q, want %q", got, want)
	}
}

func TestApplyEmptyAndNoDict(t *testing.T) {
	s := New("")
	if got := applyOn(s, "", ""); got != "" {
		t.Fatalf("空文本: got %q", got)
	}
	if got, want := applyOn(s, "原样返回", ""), "原样返回"; got != want {
		t.Fatalf("空词典: got %q, want %q", got, want)
	}
}

func TestStorePersistAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pronunciation.json")
	s := New(path)
	e1, err := s.Add("GIF", "jiff", "zh-CN", true)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Language != "zh" {
		t.Fatalf("language 归一化失败: %q", e1.Language)
	}
	// 同词条同语言范围（大小写折叠）拒绝重复
	if _, err := s.Add("gif", "giff", "zh", true); err == nil {
		t.Fatal("重复词条未拒绝")
	}
	// 不同语言范围允许
	if _, err := s.Add("gif", "jiff", "en", true); err != nil {
		t.Fatalf("不同语言范围应允许: %v", err)
	}
	// 重新打开读取盘上数据
	s2 := New(path)
	if got := len(s2.List()); got != 2 {
		t.Fatalf("持久化读取词条数 = %d, want 2", got)
	}
	if err := s2.Delete(e1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Update("nope", "a", "b", "*", true); err == nil {
		t.Fatal("更新不存在词条未报错")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("词典文件未落盘: %v", err)
	}
}

func TestStoreValidation(t *testing.T) {
	s := New("")
	for _, tc := range []struct{ term, repl, lang string }{
		{"", "x", "*"}, {" ", "x", "*"}, {"x", "", "*"}, {"x", " ", "*"}, {"x", "y", "f"},
	} {
		if _, err := s.Add(tc.term, tc.repl, tc.lang, true); err == nil {
			t.Fatalf("Add(%q,%q,%q) 应报错", tc.term, tc.repl, tc.lang)
		}
	}
}
