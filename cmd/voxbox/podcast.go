package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newPodcastCommand() *cobra.Command {
	var (
		textFile  string
		pageURL   string
		scriptURL string
		speakers  string
		format    string
		headMusic bool
		outPath   string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "podcast [text]",
		Short: "语音播客：文本/网页/对话稿生成双人播客",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			text := ""
			if len(args) == 1 {
				text = args[0]
			}
			// 位置参数 text、--file、--url、--script 最多提供一个（互斥）。
			provided := 0
			for _, v := range []string{text, textFile, pageURL, scriptURL} {
				if v != "" {
					provided++
				}
			}
			if provided > 1 {
				return fmt.Errorf("播客输入只能提供其一")
			}

			params := map[string]any{}
			switch {
			case text != "":
				params["input_text"] = text
			case textFile != "":
				raw, err := os.ReadFile(textFile)
				if err != nil {
					return fmt.Errorf("读取文本文件失败: %w", err)
				}
				params["input_text"] = string(raw)
			case pageURL != "":
				params["url"] = pageURL
			case scriptURL != "":
				raw, err := os.ReadFile(scriptURL)
				if err != nil {
					return fmt.Errorf("读取对话稿文件失败: %w", err)
				}
				params["script"] = string(raw) // 对话稿 JSON 原文作为字符串参数传递
			}

			if speakers == "" {
				return fmt.Errorf("请通过 --speakers 指定两个音色 ID（用 voxbox voices list 查询）")
			}
			params["speakers"] = speakers
			params["format"] = format
			params["head_music"] = headMusic
			return runToolSync(c, "volcengine", "podcast", params, nil, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&textFile, "file", "", "从文件读取播客文本（与位置参数/--url/--script 互斥）")
	f.StringVar(&pageURL, "url", "", "网页链接，服务端联网总结后生成")
	f.StringVar(&scriptURL, "script", "", "对话稿 JSON 文件路径（读文件内容作为 script 参数）")
	f.StringVar(&speakers, "speakers", "", "两个音色 ID，逗号分隔，顺序为说话人 A、B（必填）")
	f.StringVar(&format, "format", "mp3", "音频格式: mp3|ogg_opus|pcm|aac")
	f.BoolVar(&headMusic, "head-music", false, "是否加开头音乐")
	f.StringVar(&outPath, "out", "", "播客音频输出路径（默认数据目录自动命名，对话稿同路径 .json）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}
