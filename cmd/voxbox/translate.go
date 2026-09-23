package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
	)
	cmd := &cobra.Command{
		Use:   "translate <text>",
		Short: "机器翻译：大模型文本翻译（32 语种互译、术语定制）",
		Long: `机器翻译：火山机器翻译大模型（matx_translate），同步返回译文。
支持 32 语种互译，源语言缺省自动检测；术语经 --terms 直传（原词=译词，
逗号或换行分隔），也可指定术语管理平台的术语表。`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
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
	return cmd
}
