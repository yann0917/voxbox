package translate

import (
	"context"
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
