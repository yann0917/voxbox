package translate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/subtitle"
)

func engineCfg() *config.Config { return &config.Config{} }

func TestBatchRunFreeSource(t *testing.T) {
	orig := []subtitle.Segment{
		{Text: "第一句", StartMS: 0, EndMS: 1000},
		{Text: "第二句", StartMS: 1000, EndMS: 2000},
		{Text: "第三句", StartMS: 2000, EndMS: 3000},
	}
	var batches [][]string
	freeTranslate = func(ctx context.Context, cfg *config.Config, lines []string, srcLang, tgtLang string) ([]string, error) {
		batches = append(batches, lines)
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = "T:" + l
		}
		return out, nil
	}
	defer func() { freeTranslate = freeTranslateProd }()
	var progress []int
	res, err := Run(context.Background(), engineCfg(), orig, Options{Source: "free", TargetLang: "en"}, func(done, total int) {
		progress = append(progress, done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Source != "free" || res.Stats.Translated != 3 || res.Stats.Total != 3 {
		t.Fatalf("stats=%+v", res.Stats)
	}
	for i, s := range res.Segments {
		if s.Translation != "T:"+orig[i].Text || s.Text != orig[i].Text {
			t.Fatalf("segment[%d]=%+v", i, s)
		}
	}
	if len(progress) == 0 || progress[len(progress)-1] != 3 {
		t.Fatalf("progress=%v", progress)
	}
}

func TestRunGuards(t *testing.T) {
	if _, err := Run(context.Background(), engineCfg(), nil, Options{Source: "free", TargetLang: "en"}, nil); err == nil {
		t.Fatal("空字幕应报错")
	}
	many := make([]subtitle.Segment, 2001)
	if _, err := Run(context.Background(), engineCfg(), many, Options{Source: "free", TargetLang: "en"}, nil); err == nil {
		t.Fatal("超 2000 条应报错")
	}
	if _, err := Run(context.Background(), engineCfg(), []subtitle.Segment{{Text: "x", StartMS: 0, EndMS: 100}}, Options{Source: "free"}, nil); err == nil {
		t.Fatal("缺目标语言应报错")
	}
	if _, err := Run(context.Background(), engineCfg(), []subtitle.Segment{{Text: "x", StartMS: 0, EndMS: 100}}, Options{Source: "bad", TargetLang: "en"}, nil); err == nil {
		t.Fatal("未知 source 应报错")
	}
}

func TestResolveSourceAuto(t *testing.T) {
	// 空配置:ai(无凭证)与 volcengine(无凭证)跳过 → free
	got, err := ResolveSource(engineCfg(), "")
	if err != nil || got != "free" {
		t.Fatalf("ResolveSource auto=%q err=%v", got, err)
	}
	if _, err := ResolveSource(engineCfg(), "ai"); err == nil {
		t.Fatal("显式 ai 无凭证应报错")
	}
	if got, err := ResolveSource(engineCfg(), "free"); err != nil || got != "free" {
		t.Fatalf("free 直选: %q %v", got, err)
	}
}

func TestTargetLangValidation(t *testing.T) {
	segs := []subtitle.Segment{{Text: "x", StartMS: 0, EndMS: 100}}
	if _, err := Run(context.Background(), engineCfg(), segs, Options{Source: "free", TargetLang: "klingon"}, nil); err == nil {
		t.Fatal("不在语言清单的目标应报错")
	}
	if _, err := Run(context.Background(), engineCfg(), segs, Options{Source: "free", TargetLang: "zh-Hant"}, nil); err == nil {
		t.Fatal("免费链 zh-Hant 目标应报错(经源调用透出)")
	}
	_ = strings.TrimSpace
}

// mkWrapperResp 造一份合法 wrapper 响应:按给定行文本逐条回显吻合 + summary + glossary。
func mkWrapperResp(lines []string, summary, glossary string) string {
	var b strings.Builder
	b.WriteString(`{"translations":{`)
	for i, l := range lines {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%d":{"src":%q,"tr":"译%s"}`, i+1, l, l)
	}
	fmt.Fprintf(&b, `},"summary":%q,"glossary":%s}`, summary, glossary)
	return b.String()
}

// lineTexts 取 segments 的行文本,供 mock 回显。
func lineTexts(segs []subtitle.Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

func TestRunAIMemoryAcrossBatches(t *testing.T) {
	// 21 条 → 2 批:验证 Run 内跨批记忆(首批学词→次批 prompt 注入)与术语落库
	store := newFakeGlossaryStore()
	store.terms["en"] = map[string]string{"旧词": "Old Term"} // 预热注入首批
	lines := make([]subtitle.Segment, 21)
	for i := range lines {
		lines[i] = subtitle.Segment{Text: fmt.Sprintf("line%d", i), StartMS: int64(i) * 1000, EndMS: int64(i+1) * 1000}
	}
	calls, prompts := setAICapture(t,
		mkWrapperResp(lineTexts(lines[:20]), "上半场", `{"line0":"Line Zero"}`),
		mkWrapperResp(lineTexts(lines[20:]), "下半场", `{"line20":"Line Twenty"}`),
	)
	res, err := Run(context.Background(),
		&config.Config{Zhipu: config.ZhipuConfig{APIKey: "test-key"}}, // 过 Run 的 AI 源凭证预检;调用走 mock 不出网
		lines, Options{Source: "ai", TargetLang: "en", Model: "m", GlossaryStore: store}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || res.Stats.Source != "ai" {
		t.Fatalf("calls=%d stats=%+v", *calls, res.Stats)
	}
	firstUser, secondUser := (*prompts)[0][1], (*prompts)[1][1]
	if !strings.Contains(firstUser, "旧词 → Old Term") {
		t.Fatalf("首批 user 须注入 store 预热术语: %q", firstUser)
	}
	if !strings.Contains(secondUser, "line0 → Line Zero") || !strings.Contains(secondUser, "上半场") {
		t.Fatalf("次批 user 须注入首批所学: %q", secondUser)
	}
	if store.terms["en"]["line0"] != "Line Zero" || store.terms["en"]["line20"] != "Line Twenty" {
		t.Fatalf("两批术语应都落库: %v", store.terms["en"])
	}
	if res.Segments[20].Translation != "译line20" {
		t.Fatalf("末批译文应对齐: %+v", res.Segments[20])
	}
}
