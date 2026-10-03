package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/subtitle"
	"github.com/yann0917/voxbox/internal/translate"
)

func newTranslateCommand() *cobra.Command {
	var (
		textFile  string
		source    string
		target    string
		terms     string
		termsFile string
		tableID   string
		tableName string
		outPath   string
		jsonOut   bool

		srtFile   string
		mtSource  string
		bilingual bool
	)
	cmd := &cobra.Command{
		Use:   "translate <text>",
		Short: "机器翻译：大模型文本翻译（32 语种互译、术语定制）",
		Long: `机器翻译：火山机器翻译大模型（matx_translate），同步返回译文。
支持 32 语种互译，源语言缺省自动检测；术语经 --terms 直传（原词=译词，
逗号或换行分隔），也可指定术语管理平台的术语表。

翻译 SRT 字幕文件用 --srt：整份逐条翻译后写出译文字幕，--bilingual 可
同时保留原文；翻译源可选大模型 / 火山机器翻译 / 免费接口（--source，
缺省自动选择）。`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if srtFile != "" {
				return runSRTTranslate(c, srtFile, mtSource, target, outPath, bilingual, jsonOut)
			}
			text := ""
			if len(args) == 1 {
				text = args[0]
			}
			if textFile != "" {
				raw, err := os.ReadFile(textFile)
				if err != nil {
					return fmt.Errorf("读取文本文件失败: %w", err)
				}
				text = string(raw)
			}
			params := map[string]any{"text": text, "target_language": target}
			if source != "" {
				params["source_language"] = source
			}
			if termsFile != "" {
				raw, err := os.ReadFile(termsFile)
				if err != nil {
					return fmt.Errorf("读取术语文件失败: %w", err)
				}
				terms = strings.TrimSpace(terms) + "\n" + string(raw)
			}
			if strings.TrimSpace(terms) != "" {
				params["terms"] = terms
			}
			if tableID != "" {
				params["glossary_table_id"] = tableID
			}
			if tableName != "" {
				params["glossary_table_name"] = tableName
			}
			return runToolSync(c, "volcengine", "translate", params, nil, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&textFile, "file", "", "从文件读取待翻译文本")
	f.StringVar(&source, "from", "", "源语言代码（缺省自动检测，如 zh/en/ja/zh-Hant）")
	f.StringVar(&target, "to", "en", "目标语言代码（32 语种，见官方语言支持）")
	f.StringVar(&terms, "terms", "", "直传术语：原词=译词，逗号或换行分隔")
	f.StringVar(&termsFile, "terms-file", "", "从文件读取术语（每行一条 原词=译词）")
	f.StringVar(&tableID, "table-id", "", "术语表 ID（术语管理平台）")
	f.StringVar(&tableName, "table-name", "", "术语表名称（与 table-id 二选一或同传）")
	f.StringVar(&outPath, "out", "", "译文输出路径（默认数据目录自动命名）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	f.StringVar(&srtFile, "srt", "", "翻译 SRT 字幕文件，与待翻译文本参数互斥")
	f.StringVar(&mtSource, "source", "", "字幕翻译源：ai | volcengine | free（缺省自动选择，仅 --srt 生效）")
	f.BoolVar(&bilingual, "bilingual", false, "输出双语字幕（原文在上译文在下，仅 --srt 生效）")
	return cmd
}

// translatedSegments 非双语译出稿的正文替换：有译文的条目以译文为字幕文本
// （无译文退化原文），时间轴与序号语义不变。
func translatedSegments(segs []subtitle.Segment) []subtitle.Segment {
	out := make([]subtitle.Segment, len(segs))
	for i, sg := range segs {
		out[i] = sg
		if tr := strings.TrimSpace(sg.Translation); tr != "" {
			out[i].Text = tr
		}
	}
	return out
}

// runSRTTranslate 字幕文件翻译：读 SRT → 引擎翻译 → 写 SRT（可选双语）。
// 进度走 stderr，不污染 --json 的 stdout。
func runSRTTranslate(c *cobra.Command, srtFile, source, target, outPath string, bilingual, jsonOut bool) error {
	raw, err := os.ReadFile(srtFile)
	if err != nil {
		return fmt.Errorf("读取字幕文件失败: %w", err)
	}
	segs, err := subtitle.ParseSRT(string(raw))
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	last := 0
	res, err := translate.Run(c.Context(), cfg, segs, translate.Options{
		Source: source, TargetLang: target,
	}, func(done, total int) {
		if done/100 > last/100 || done == total { // 每 100 条或收尾打点
			last = done
			fmt.Fprintf(os.Stderr, "\r翻译进度 %d/%d", done, total)
		}
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	out := outPath
	if out == "" {
		ext := ".srt"
		if bilingual {
			ext = ".bilingual.srt"
		}
		out = strings.TrimSuffix(srtFile, filepath.Ext(srtFile)) + "." + target + ext
	}
	data := subtitle.BuildSRT(translatedSegments(res.Segments))
	if bilingual {
		data = subtitle.BuildSRTBilingual(res.Segments)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("写入字幕失败: %w", err)
	}
	if jsonOut {
		type cliStats struct {
			Total        int    `json:"total"`
			Translated   int    `json:"translated"`
			Repaired     int    `json:"repaired"`
			Untranslated int    `json:"untranslated"`
			Source       string `json:"source"`
			Output       string `json:"output"`
		}
		return json.NewEncoder(os.Stdout).Encode(cliStats{
			Total: res.Stats.Total, Translated: res.Stats.Translated,
			Repaired: res.Stats.Repaired, Untranslated: res.Stats.Untranslated,
			Source: res.Stats.Source, Output: out,
		})
	}
	fmt.Printf("已翻译 %d/%d 条（补翻 %d，疑似未翻译 %d，源 %s）→ %s\n",
		res.Stats.Translated, res.Stats.Total, res.Stats.Repaired, res.Stats.Untranslated, res.Stats.Source, out)
	return nil
}
