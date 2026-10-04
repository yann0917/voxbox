// Package translate 字幕翻译引擎:分批 → 源调用 → 锚定校验 → 单轮定点补翻 → 汇总。
// 源适配器为包级变量(测试缝):ai(锚定协议)/volcengine(matx_translate 批 16)/free(回退链)。
package translate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/subtitle"
)

const (
	maxSegments   = 2000
	batchSizeAI   = 20
	batchSizeVolc = 16
	batchSizeFree = 16
)

// Options 翻译选项;Provider/Model 仅 Source=ai 时生效(空=AI 默认大模型);
// GlossaryStore 仅 Source=ai 时生效(nil=纯内存,术语表不落库)。
type Options struct {
	Source        string // ai | volcengine | free;空=auto
	SourceLang    string // 空=自动检测
	TargetLang    string // 必填
	Provider      string
	Model         string
	GlossaryStore GlossaryStore
}

// Progress 每批完成回调:done 为累计已完成条数,total 为总条数。
type Progress func(done, total int)

// Stats 翻译统计(SSE 终帧/CLI JSON 原样透出,snake_case 由 handler 层组装)。
type Stats struct {
	Total        int
	Translated   int
	Repaired     int
	Untranslated int
	EchoChecked  int
	Source       string
}

// Result 翻译结果:Segments 与入参等长对齐,Translation 填充译文。
type Result struct {
	Segments []subtitle.Segment
	Stats    Stats
}

// validateTargetLang 目标语言须在火山 32 语种清单内(三源统一口径)。
func validateTargetLang(code string) error {
	for _, l := range volcengine.MTLanguages() {
		if l.Code == code {
			return nil
		}
	}
	return fmt.Errorf("目标语言 %q 不在支持清单内", code)
}

// ResolveSource 解析翻译源:显式指定时校验可用性;空=auto(ai→volcengine→free 首个可用)。
func ResolveSource(cfg *config.Config, source string) (string, error) {
	switch source {
	case "free":
		return "free", nil
	case "ai":
		if _, _, err := assistant.ResolveDefault(cfg); err != nil {
			return "", err
		}
		return "ai", nil
	case "volcengine":
		if err := volcCredOf(cfg).Validate(); err != nil {
			return "", err
		}
		return "volcengine", nil
	case "":
		if _, _, err := assistant.ResolveDefault(cfg); err == nil {
			return "ai", nil
		}
		if volcCredOf(cfg).Validate() == nil {
			return "volcengine", nil
		}
		return "free", nil
	default:
		return "", fmt.Errorf("未知翻译源 %q", source)
	}
}

// Run 翻译主流程:守卫 → 源解析 → 按源切批逐批翻译(进度回调)→ 与入参等长对齐汇总。
// AI 源逐批走 aiTranslateLines(锚定校验+定点补翻,诊断聚合进 Stats);
// 火山/免费源经包级缝函数批翻译。任一批失败即整体失败,已完成批不落盘。
func Run(ctx context.Context, cfg *config.Config, segs []subtitle.Segment, o Options, onProgress Progress) (*Result, error) {
	if len(segs) == 0 {
		return nil, errors.New("没有可翻译的字幕内容")
	}
	if len(segs) > maxSegments {
		return nil, fmt.Errorf("字幕共 %d 条,超过单次上限 %d,请拆分后翻译", len(segs), maxSegments)
	}
	if strings.TrimSpace(o.TargetLang) == "" {
		return nil, errors.New("缺少目标语言")
	}
	if err := validateTargetLang(o.TargetLang); err != nil {
		return nil, err
	}
	src, err := ResolveSource(cfg, o.Source)
	if err != nil {
		return nil, err
	}
	lines := make([]string, len(segs))
	for i, s := range segs {
		lines[i] = s.Text
	}
	size := batchSizeAI
	switch src {
	case "volcengine":
		size = batchSizeVolc
	case "free":
		size = batchSizeFree
	}
	out := make([]string, len(lines))
	var diag AIDiag
	var mem *TranslationMemory
	if src == "ai" {
		// 跨批记忆:store 预热术语表(失败降级纯内存),逐批 Learn 增量落库
		mem = newTranslationMemory(o.GlossaryStore, o.TargetLang)
		mem.load()
	}
	done := 0
	for _, batch := range subtitle.BatchIndices(len(lines), size) {
		batchLines := make([]string, len(batch))
		for k, idx := range batch {
			batchLines[k] = lines[idx]
		}
		var (
			batchOut []string
			batchErr error
		)
		switch src {
		case "ai":
			var d AIDiag
			batchOut, d, batchErr = aiTranslateLines(ctx, cfg, o.Provider, o.Model, batchLines, o.SourceLang, o.TargetLang, mem)
			diag.EchoChecked += d.EchoChecked
			diag.Misaligned += d.Misaligned
			diag.Repaired += d.Repaired
			diag.Untranslated += d.Untranslated
		case "volcengine":
			batchOut, batchErr = volcTranslate(ctx, cfg, batchLines, o.SourceLang, o.TargetLang)
		default:
			batchOut, batchErr = freeTranslate(ctx, cfg, batchLines, o.SourceLang, o.TargetLang)
		}
		if batchErr != nil {
			return nil, fmt.Errorf("第 %d-%d 条翻译失败: %w", batch[0]+1, batch[len(batch)-1]+1, batchErr)
		}
		if len(batchOut) != len(batch) {
			return nil, fmt.Errorf("第 %d-%d 条译文数 %d 与原文不符", batch[0]+1, batch[len(batch)-1]+1, len(batchOut))
		}
		for k, idx := range batch {
			out[idx] = batchOut[k]
		}
		done += len(batch)
		if onProgress != nil {
			onProgress(done, len(lines))
		}
	}
	resSegs := make([]subtitle.Segment, len(segs))
	for i, s := range segs {
		resSegs[i] = subtitle.Segment{Text: s.Text, Translation: out[i], StartMS: s.StartMS, EndMS: s.EndMS}
	}
	return &Result{Segments: resSegs, Stats: Stats{
		Total:        len(segs),
		Translated:   len(segs) - diag.Untranslated,
		Repaired:     diag.Repaired,
		Untranslated: diag.Untranslated,
		EchoChecked:  diag.EchoChecked,
		Source:       src,
	}}, nil
}
