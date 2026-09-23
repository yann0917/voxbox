package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yann0917/voxbox/internal/config"
)

// 词典资产：命名的热词表（ASR/妙记）与术语对（翻译），落 config.yaml dicts 段。
// 工具页通过 GET /api/dicts 读取并一键填入，管理（增删）走 CLI。

func newDictCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dict",
		Short: "词典资产管理：热词表与术语对（ASR/妙记/翻译共用）",
	}
	root.AddCommand(newDictListCmd(), newDictAddCmd(), newDictRmCmd())
	return root
}

func newDictListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出全部词典",
		RunE: func(c *cobra.Command, args []string) error {
			dicts, err := config.Dicts()
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(dicts)
				return nil
			}
			if len(dicts) == 0 {
				eprintf("暂无词典，用 `voxbox dict add <名称> --hotwords/--terms` 创建\n")
				return nil
			}
			for _, d := range dicts {
				eprintf("%s\n", d.Name)
				if d.Hotwords != "" {
					eprintf("  热词: %s\n", d.Hotwords)
				}
				if d.Terms != "" {
					eprintf("  术语: %s\n", d.Terms)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newDictAddCmd() *cobra.Command {
	var hotwords, terms string
	cmd := &cobra.Command{
		Use:   "add <名称>",
		Short: "创建或覆盖词典（--hotwords 热词逗号分隔 / --terms 术语 原词=译词,逗号或换行分隔）",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if hotwords == "" && terms == "" {
				return fmt.Errorf("--hotwords 与 --terms 至少提供其一")
			}
			// 术语格式轻校验：每条必须含「=」，坏条目直接拒绝，避免提交上游才报错。
			for _, pair := range strings.FieldsFunc(terms, func(r rune) bool { return r == ',' || r == '\n' }) {
				if !strings.Contains(pair, "=") {
					return fmt.Errorf("术语格式错误: %q（应为 原词=译词）", pair)
				}
			}
			if err := config.SetDict(name, config.DictEntry{Hotwords: hotwords, Terms: terms}); err != nil {
				return err
			}
			eprintf("词典 %q 已保存\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&hotwords, "hotwords", "", "热词，逗号分隔（提升 ASR/妙记专有名词识别率）")
	cmd.Flags().StringVar(&terms, "terms", "", "术语对：原词=译词，逗号或换行分隔（翻译固定译法）")
	return cmd
}

func newDictRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <名称>",
		Short: "删除词典",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := config.RemoveDict(strings.TrimSpace(args[0])); err != nil {
				return err
			}
			eprintf("词典 %q 已删除\n", args[0])
			return nil
		},
	}
}
