package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newASRCommand() *cobra.Command {
	var (
		audioURL string
		outPath  string
		srt      bool
		hotwords string
		language string
		version  string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "asr <file>",
		Short: "语音识别：一句话识别（本地文件同步）与录音文件识别（URL 异步）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			file := ""
			if len(args) == 1 {
				file = args[0]
			}
			if file != "" && audioURL != "" {
				return fmt.Errorf("音频文件与 URL 只能提供其一")
			}
			if file == "" && audioURL == "" {
				return fmt.Errorf("请提供音频文件或 --url")
			}
			// 版本缺省按输入方式推断：本地文件 → 一句话识别；URL → 标准版
			if version == "" {
				if file != "" {
					version = "sentence"
				} else {
					version = "standard"
				}
			}
			switch version {
			case "sentence", "standard", "idle", "flash":
			default:
				return fmt.Errorf("识别版本 version 仅支持 sentence（一句话，本地文件）/ standard（录音文件识别）/ idle / flash")
			}
			// 文件+URL 版本的组合交给工具层裁决：配置了对象存储时自动中转走对应版本，
			// 未配置时 standard 降级一句话、idle/flash 给出明确的设置指引。
			if audioURL != "" && version == "sentence" {
				return fmt.Errorf("一句话识别仅支持本地音频文件；URL 请使用 --version standard / idle / flash")
			}

			var files map[string]string
			if file != "" {
				if _, err := os.Stat(file); err != nil {
					return fmt.Errorf("音频文件不存在: %s", file)
				}
				abs, err := filepath.Abs(file)
				if err != nil {
					return fmt.Errorf("解析音频文件路径失败: %w", err)
				}
				files = map[string]string{"audio": abs}
			}

			params := map[string]any{"hotwords": hotwords, "language": language, "srt": srt, "version": version}
			if audioURL != "" {
				params["url"] = audioURL
			}
			return runToolSync(c, "volcengine", "asr", params, files, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&audioURL, "url", "", "公网音频 URL（与位置参数二选一）")
	f.StringVar(&outPath, "out", "", "转写文本输出路径（默认数据目录自动命名）")
	f.BoolVar(&srt, "srt", true, "额外产出 SRT 字幕（--srt=false 关闭）")
	f.StringVar(&hotwords, "hotwords", "", "热词，逗号分隔（原样透传）")
	f.StringVar(&language, "language", "", "识别语言（留空自动识别中文/英文/常见方言；可选 zh-CN/en-US/ja-JP/yue-CN 等 25 种）")
	f.StringVar(&version, "version", "", "识别版本（缺省按输入推断：本地文件→sentence，URL→standard）：sentence 一句话识别（本地文件，同步秒级）/ standard 标准版（录音文件识别）/ idle 闲时版（24h 内完成）/ flash 极速版（秒级）；本地文件+standard/idle/flash 需配置对象存储中转")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}
