package translate

import (
	"context"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

// setAIStream 替换 StreamCompose 缝:按注册的响应序列逐次吐整段 JSON(模拟累积 delta)。
func setAIStream(t *testing.T, responses ...string) *int {
	t.Helper()
	orig := aiStreamCompose
	t.Cleanup(func() { aiStreamCompose = orig })
	calls := 0
	aiStreamCompose = func(ctx context.Context, cfg *config.Config, provider, model, system string,
		messages []assistantMessage, onDelta func(string)) error {
		if calls >= len(responses) {
			t.Fatalf("意外的第 %d 次 AI 调用", calls+1)
		}
		calls++
		onDelta(responses[calls-1])
		return nil
	}
	return &calls
}

func TestAITranslateAnchored(t *testing.T) {
	calls := setAIStream(t,
		`{"1":{"src":"你好","tr":"Hello"},"2":{"src":"世界","tr":"World"}}`,
	)
	lines := []string{"你好", "世界"}
	got, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "ai-model", lines, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || got[0] != "Hello" || got[1] != "World" {
		t.Fatalf("got=%v calls=%d", got, *calls)
	}
	if diag.EchoChecked != 2 || diag.Misaligned != 0 {
		t.Fatalf("diag=%+v", diag)
	}
}

func TestAITranslateMisalignedRepair(t *testing.T) {
	// 第一轮:合并翻译(第 2 条回显错位)→ 定点补翻第 2 条
	calls := setAIStream(t,
		`{"1":{"src":"你好","tr":"Hello there"},"2":{"src":"错位回显","tr":"World"}}`,
		`{"1":{"src":"世界","tr":"World"}}`,
	)
	got, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"你好", "世界"}, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || got[0] != "Hello there" || got[1] != "World" {
		t.Fatalf("got=%v calls=%d", got, *calls)
	}
	if diag.Misaligned != 1 || diag.Repaired != 1 {
		t.Fatalf("diag=%+v", diag)
	}
}

func TestAITranslateUntranslatedCounted(t *testing.T) {
	setAIStream(t, `{"1":{"src":"OK","tr":"OK"}}`)
	_, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"OK"}, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	if diag.Untranslated != 1 {
		t.Fatalf("diag=%+v(疑似复制应计数,单轮补翻后仍相同则保留并计数)", diag)
	}
}

func TestParseAnchoredLenient(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"```json\n{\"1\":{\"src\":\"a\",\"tr\":\"b\"}}\n```", "b"},
		{"前置说明 {\"1\":{\"src\":\"a\",\"tr\":\"b\"}} 后缀", "b"},
		{"{\"1\":{\"src\":\"a\",\"tr\":\"b\",}}", "b"}, // 尾逗号
		{"<think>x</think>{\"1\":{\"src\":\"a\",\"tr\":\"b\"}}", "b"},
	}
	for i, tc := range cases {
		m, _, err := parseAIResponse(tc.raw)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if m["1"].Tr != tc.want {
			t.Fatalf("case %d: %+v", i, m)
		}
	}
	if _, _, err := parseAIResponse("完全不是 JSON"); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestParseAIResponseSimple(t *testing.T) {
	m, simple, err := parseAIResponse(`{"1":"Hello","2":"World"}`)
	if err != nil || !simple || m["1"].Tr != "Hello" {
		t.Fatalf("m=%v simple=%v err=%v", m, simple, err)
	}
}

func TestBuildAnchoredPrompt(t *testing.T) {
	system, user := buildAnchoredPrompt([]string{"你好", "世界"}, "", "zh-Hant")
	if !contains(system, "繁体中文") {
		t.Fatal("zh-Hant 目标须在提示词中消歧为繁体中文(issue #332 教训)")
	}
	if !contains(user, "1. 你好") || !contains(user, "2. 世界") {
		t.Fatalf("user=%q", user)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
