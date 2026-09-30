package prompts

import (
	"strings"
	"testing"
)

func TestBuiltinCatalogIntegrity(t *testing.T) {
	list := Builtin()
	if len(list) != 21 {
		t.Fatalf("内置条目数 = %d, want 21（生成 8 + 润色 4 + 方言 9）", len(list))
	}
	seen := map[string]bool{}
	gen, polish, dialect := 0, 0, 0
	for _, e := range list {
		if e.Key == "" || e.Name == "" || e.Category == "" || e.Content == "" {
			t.Fatalf("条目字段残缺: %+v", e)
		}
		if seen[e.Key] {
			t.Fatalf("条目 Key 重复: %s（Key 是对外引用的稳定标识，不可复用）", e.Key)
		}
		seen[e.Key] = true
		if !e.Kind.Valid() {
			t.Fatalf("%s 用途非法: %s", e.Key, e.Kind)
		}
		if !e.Builtin {
			t.Fatalf("%s 应标记 Builtin", e.Key)
		}
		switch e.Kind {
		case KindGenerate:
			gen++
		case KindPolish:
			polish++
		case KindDialect:
			dialect++
			// 方言条目必须自带改写指令骨架(改写动词+只输出约束);
			// 朗读约束由 SystemPrompt 统一追加,不在此重复
			if !strings.Contains(e.Content, "改写") || !strings.Contains(e.Content, "只输出") {
				t.Fatalf("方言条目 %s 正文缺少改写/输出约束: %q", e.Key, e.Content)
			}
		}
	}
	if gen != 8 || polish != 4 || dialect != 9 {
		t.Fatalf("生成/润色/方言 = %d/%d/%d, want 8/4/9", gen, polish, dialect)
	}
}

// TestDialectKeysStable 方言条目 Key 是前端选择器的稳定引用,清单锁定防误改。
func TestDialectKeysStable(t *testing.T) {
	want := []string{
		"dialect-yue", "dialect-sichuan", "dialect-henan", "dialect-dongbei",
		"dialect-shaanxi", "dialect-shandong", "dialect-tianjin", "dialect-wu", "dialect-minnan",
	}
	for _, key := range want {
		if _, ok := Get(key); !ok {
			t.Fatalf("内置方言条目缺失: %s", key)
		}
	}
	if _, ok := Get("dialect-cantonese"); ok {
		t.Fatal("方言 Key 命名不应混用英文别名")
	}
}

func TestGet(t *testing.T) {
	e, ok := Get("idiom-story")
	if !ok || e.Name != "成语故事" || e.Kind != KindGenerate {
		t.Fatalf("Get(idiom-story) = %+v, %v", e, ok)
	}
	if _, ok := Get("no-such"); ok {
		t.Fatal("未知 key 应返回 false")
	}
}

func TestSystemPrompt(t *testing.T) {
	base := "创作一个故事。"
	sys, err := SystemPrompt(base, KindGenerate, LengthLong)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{base, "朗读约束", "800 字"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("系统提示缺 %q: %s", want, sys)
		}
	}
	// 润色不接篇幅指令
	sys, _ = SystemPrompt(base, KindPolish, LengthLong)
	if strings.Contains(sys, "800 字") {
		t.Fatalf("润色类不应有篇幅指令: %s", sys)
	}
	// 方言同样不接篇幅指令(以原文篇幅为准),朗读约束照常追加
	sys, err = SystemPrompt(base, KindDialect, LengthLong)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sys, "800 字") {
		t.Fatalf("方言类不应有篇幅指令: %s", sys)
	}
	if !strings.Contains(sys, "朗读约束") {
		t.Fatal("方言类应追加朗读约束")
	}
	// 篇幅缺省不追加指令
	sys, _ = SystemPrompt(base, KindGenerate, "")
	if strings.Contains(sys, "【篇幅】") {
		t.Fatalf("未指定篇幅不应追加指令: %s", sys)
	}
	if _, err := SystemPrompt(base, "other", ""); err == nil {
		t.Fatal("非法用途应报错")
	}
}
