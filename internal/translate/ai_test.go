package translate

import (
	"context"
	"fmt"
	"strings"
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

// setAICapture 在 setAIStream 基础上额外记录每次调用的 system/user 提示,供记忆注入断言。
func setAICapture(t *testing.T, responses ...string) (*int, *[][2]string) {
	t.Helper()
	orig := aiStreamCompose
	t.Cleanup(func() { aiStreamCompose = orig })
	calls := 0
	var prompts [][2]string
	aiStreamCompose = func(ctx context.Context, cfg *config.Config, provider, model, system string,
		messages []assistantMessage, onDelta func(string)) error {
		if calls >= len(responses) {
			t.Fatalf("意外的第 %d 次 AI 调用", calls+1)
		}
		calls++
		prompts = append(prompts, [2]string{system, messages[0].Content})
		onDelta(responses[calls-1])
		return nil
	}
	return &calls, &prompts
}

func TestAITranslateAnchored(t *testing.T) {
	calls := setAIStream(t,
		`{"1":{"src":"你好","tr":"Hello"},"2":{"src":"世界","tr":"World"}}`,
	)
	lines := []string{"你好", "世界"}
	got, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "ai-model", lines, "", "en", nil)
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
	got, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"你好", "世界"}, "", "en", nil)
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
	_, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"OK"}, "", "en", nil)
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
		p, err := parseAIResponse(tc.raw)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if p.Entries["1"].Tr != tc.want {
			t.Fatalf("case %d: %+v", i, p)
		}
	}
	if _, err := parseAIResponse("完全不是 JSON"); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestParseAIResponseSimple(t *testing.T) {
	p, err := parseAIResponse(`{"1":"Hello","2":"World"}`)
	if err != nil || !p.Simple || p.Entries["1"].Tr != "Hello" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestParseAIResponseWrapper(t *testing.T) {
	p, err := parseAIResponse(`{"translations":{"1":{"src":"你好","tr":"Hello"}},
		"summary":"开场问候","glossary":{"张三":"Zhang San"},}`)
	if err != nil || p.Simple || p.Entries["1"].Tr != "Hello" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if p.Summary != "开场问候" || p.Glossary["张三"] != "Zhang San" {
		t.Fatalf("记忆字段丢失: %+v", p)
	}
	// 裸锚定(补翻轮形态):不误判成 wrapper,记忆字段为空
	p2, err := parseAIResponse(`{"1":{"src":"你好","tr":"Hello"}}`)
	if err != nil || p2.Simple || p2.Summary != "" || len(p2.Glossary) != 0 {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
	// wrapper 但 translations 全空(退化形态)→ 落到后续形态,此处最终报错
	if _, err := parseAIResponse(`{"translations":{"1":{}},"summary":"x"}`); err == nil {
		t.Fatal("全空 translations 不应被采信")
	}
}

func TestBuildAnchoredPrompt(t *testing.T) {
	system, user := buildAnchoredPrompt([]string{"你好", "世界"}, "", "zh-Hant", nil, false)
	if !contains(system, "繁体中文") {
		t.Fatal("zh-Hant 目标须在提示词中消歧为繁体中文(issue #332 教训)")
	}
	if !contains(user, "1. 你好") || !contains(user, "2. 世界") {
		t.Fatalf("user=%q", user)
	}
}

func TestBuildAnchoredPromptMemory(t *testing.T) {
	mem := &TranslationMemory{
		Summary: "码头夜谈",
		Glossary: []GlossaryTerm{
			{Src: "张三", Dst: "Zhang San"},
			{Src: "云帆号", Dst: "Yunfan"},
		},
	}
	// 首轮:注入记忆 + wrapper 协议
	system, user := buildAnchoredPrompt([]string{"你好"}, "", "en", mem, true)
	if !contains(system, `"translations"`) || !contains(system, `"summary"`) || !contains(system, `"glossary"`) {
		t.Fatalf("首轮 system 须要求 wrapper 协议: %q", system)
	}
	if !contains(user, "码头夜谈") || !contains(user, "张三 → Zhang San") || !contains(user, "云帆号 → Yunfan") {
		t.Fatalf("首轮 user 须注入记忆: %q", user)
	}
	// 补翻轮:带记忆上下文但保持裸锚定协议
	system2, user2 := buildAnchoredPrompt([]string{"你好"}, "", "en", mem, false)
	if contains(system2, `"translations"`) {
		t.Fatalf("补翻轮不应请求 wrapper: %q", system2)
	}
	if !contains(user2, "张三 → Zhang San") {
		t.Fatalf("补翻轮 user 仍须带记忆: %q", user2)
	}
}

// fakeGlossaryStore 内存术语表桩:可注入读写错误。
type fakeGlossaryStore struct {
	terms   map[string]map[string]string // lang → src → dst
	loadErr error
	upErr   error
	upserts int
}

func newFakeGlossaryStore() *fakeGlossaryStore {
	return &fakeGlossaryStore{terms: map[string]map[string]string{}}
}

func (f *fakeGlossaryStore) LoadGlossary(targetLang string, limit int) ([]GlossaryTerm, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	m := f.terms[targetLang]
	out := make([]GlossaryTerm, 0, len(m))
	for k, v := range m {
		out = append(out, GlossaryTerm{Src: k, Dst: v})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeGlossaryStore) UpsertGlossary(targetLang string, terms []GlossaryTerm) error {
	f.upserts++
	if f.upErr != nil {
		return f.upErr
	}
	if f.terms[targetLang] == nil {
		f.terms[targetLang] = map[string]string{}
	}
	for _, t := range terms {
		f.terms[targetLang][t.Src] = t.Dst
	}
	return nil
}

func TestAITranslateMemoryFlow(t *testing.T) {
	store := newFakeGlossaryStore()
	mem := newTranslationMemory(store, "en")
	calls, prompts := setAICapture(t,
		`{"translations":{"1":{"src":"张三来了","tr":"Zhang San arrives"}},"summary":"开场问候","glossary":{"张三":"Zhang San"}}`,
		`{"translations":{"1":{"src":"张三走了","tr":"Zhang San leaves"}},"summary":"张三登场","glossary":{}}`,
	)
	// 两批同走一个 mem:首批学词,次批 prompt 注入
	if _, _, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"张三来了"}, "", "en", mem); err != nil {
		t.Fatal(err)
	}
	if _, _, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"张三走了"}, "", "en", mem); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("calls=%d", *calls)
	}
	firstUser, secondUser := (*prompts)[0][1], (*prompts)[1][1]
	if strings.Contains(firstUser, "Zhang San") {
		t.Fatalf("首批 user 不应含术语表: %q", firstUser)
	}
	if !strings.Contains(secondUser, "前文场景摘要(仅供衔接上下文,不要翻译):开场问候") ||
		!strings.Contains(secondUser, "张三 → Zhang San") {
		t.Fatalf("次批 user 须注入首批所学: %q", secondUser)
	}
	if store.upserts != 1 || store.terms["en"]["张三"] != "Zhang San" {
		t.Fatalf("首批术语应落库: upserts=%d terms=%v", store.upserts, store.terms)
	}
	// 次批摘要应刷新会话记忆
	if mem.Summary != "张三登场" {
		t.Fatalf("摘要应被次批刷新: %q", mem.Summary)
	}
}

func TestFilterGlossaryTerms(t *testing.T) {
	lines := []string{"Zhang San boarded the ship.", "云帆号起航了"}
	kept := filterGlossaryTerms([]GlossaryTerm{
		{Src: "Zhang San", Dst: "张三"},  // 原文里有(大小写归一) → 保留
		{Src: "云帆号", Dst: "Yunfan"},    // 原文里有 → 保留
		{Src: "lighthouse", Dst: "灯塔"}, // 本批原文没有 → 滤掉(幻觉/凭空)
		{Src: "", Dst: "x"},            // 空 → 滤掉
	}, lines)
	if len(kept) != 2 || kept[0].Src != "Zhang San" || kept[1].Src != "云帆号" {
		t.Fatalf("存在性校验不符: %+v", kept)
	}
	if got := filterGlossaryTerms([]GlossaryTerm{{Src: "x", Dst: "y"}}, nil); got != nil {
		t.Fatalf("空原文批应全滤: %v", got)
	}
}

func TestAITranslateMemoryRepairNoAsk(t *testing.T) {
	store := newFakeGlossaryStore()
	mem := newTranslationMemory(store, "en")
	mem.Learn("", []GlossaryTerm{{Src: "张三", Dst: "Zhang San"}})
	calls, prompts := setAICapture(t,
		`{"translations":{"1":{"src":"你好","tr":"Hi"},"2":{"src":"错位","tr":"World"}},"summary":"s","glossary":{}}`,
		`{"1":{"src":"世界","tr":"World"}}`,
	)
	got, diag, err := aiTranslateLines(context.Background(), &config.Config{}, "", "m", []string{"你好", "世界"}, "", "en", mem)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || got[1] != "World" || diag.Repaired != 1 {
		t.Fatalf("got=%v diag=%+v calls=%d", got, diag, *calls)
	}
	repairSystem, repairUser := (*prompts)[1][0], (*prompts)[1][1]
	if strings.Contains(repairSystem, `"translations"`) {
		t.Fatalf("补翻轮不应请求 wrapper 协议: %q", repairSystem)
	}
	if !strings.Contains(repairUser, "张三 → Zhang San") {
		t.Fatalf("补翻轮 user 仍须带记忆: %q", repairUser)
	}
}

func TestTranslationMemoryLearnAndCaps(t *testing.T) {
	store := newFakeGlossaryStore()
	mem := newTranslationMemory(store, "en")
	mem.load() // 空库无影响

	// 非法项过滤:空/src=dst/超长
	mem.Learn("", []GlossaryTerm{
		{Src: "", Dst: "x"},
		{Src: "AI", Dst: "AI"},
		{Src: strings.Repeat("长", termSrcMaxRunes+1), Dst: "x"},
		{Src: "张三", Dst: "Zhang San"},
	})
	if len(mem.Glossary) != 1 || mem.Glossary[0].Dst != "Zhang San" {
		t.Fatalf("非法项应被过滤: %+v", mem.Glossary)
	}
	// 同词修订:覆盖译法并移到最新位
	mem.Learn("", []GlossaryTerm{{Src: "李四", Dst: "Li Si"}})
	mem.Learn("", []GlossaryTerm{{Src: "张三", Dst: "Zhang San II"}})
	if len(mem.Glossary) != 2 || mem.Glossary[0].Src != "李四" || mem.Glossary[1].Dst != "Zhang San II" {
		t.Fatalf("修订应覆盖并置尾: %+v", mem.Glossary)
	}
	if store.terms["en"]["张三"] != "Zhang San II" {
		t.Fatalf("修订应落库: %v", store.terms)
	}
	// 摘要截断
	mem.Learn(strings.Repeat("景", summaryMaxRunes+10), nil)
	if got := len([]rune(mem.Summary)); got != summaryMaxRunes {
		t.Fatalf("摘要应截断到 %d, got %d", summaryMaxRunes, got)
	}
	// 落库失败不放大
	store.upErr = fmt.Errorf("db down")
	mem.Learn("", []GlossaryTerm{{Src: "王五", Dst: "Wang Wu"}})
	if mem.Glossary[len(mem.Glossary)-1].Src != "王五" {
		t.Fatal("落库失败不应影响会话内记忆")
	}
	// load 失败降级纯内存
	bad := newFakeGlossaryStore()
	bad.loadErr = fmt.Errorf("boom")
	m2 := newTranslationMemory(bad, "en")
	m2.load()
	if len(m2.Glossary) != 0 {
		t.Fatal("load 失败应降级为空记忆")
	}
}

func TestInjectGlossaryBudget(t *testing.T) {
	all := make([]GlossaryTerm, 0, glossaryInjectMax+50)
	for i := 0; i < glossaryInjectMax+50; i++ {
		all = append(all, GlossaryTerm{Src: fmt.Sprintf("词%d", i), Dst: fmt.Sprintf("t%d", i)})
	}
	got := injectGlossary(all)
	if len(got) != glossaryInjectMax {
		t.Fatalf("条数预算: got %d", len(got))
	}
	if got[0].Src != "词50" || got[len(got)-1].Src != fmt.Sprintf("词%d", glossaryInjectMax+49) {
		t.Fatalf("应取最新区段且保持旧→新: first=%s last=%s", got[0].Src, got[len(got)-1].Src)
	}
	// 字节预算:单条超长直接截停
	huge := []GlossaryTerm{{Src: strings.Repeat("长", glossaryMaxBytes), Dst: "x"}, {Src: "a", Dst: "b"}}
	if got := injectGlossary(huge); len(got) != 1 || got[0].Src != "a" {
		t.Fatalf("超预算条目应被跳过: %+v", got)
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
