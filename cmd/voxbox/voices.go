package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

// voicesMatches 筛选匹配：筛选项与任一元素全等或元素包含筛选项即命中（如 --scene 扮演）。
func voicesMatches(list []string, filter string) bool {
	for _, v := range list {
		if v == filter || strings.Contains(v, filter) {
			return true
		}
	}
	return false
}

func newVoicesCmd() *cobra.Command {
	var (
		jsonOut  bool
		sceneFlt string
		langFlt  string
	)
	cmd := &cobra.Command{Use: "voices", Short: "音色查询"}
	list := &cobra.Command{
		Use:   "list",
		Short: "列出内置音色（来源：官方在线音色列表，可按场景/语种筛选）",
		RunE: func(c *cobra.Command, args []string) error {
			voices := volcengine.Voices()
			filtered := make([]volcengine.Voice, 0, len(voices))
			for _, v := range voices {
				if sceneFlt != "" && !voicesMatches(v.Scenes, sceneFlt) {
					continue
				}
				if langFlt != "" && !voicesMatches(v.Languages, langFlt) {
					continue
				}
				filtered = append(filtered, v)
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(filtered)
			}
			for _, v := range filtered {
				fmt.Printf("%-46s %-16s %s %s\n", v.ID, v.Name, v.Gender,
					strings.Join(v.Scenes, "/")+" · "+strings.Join(v.Languages, "/"))
			}
			fmt.Fprintf(os.Stderr, "共 %d 个音色\n", len(filtered))
			return nil
		},
	}
	list.Flags().BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	list.Flags().StringVar(&sceneFlt, "scene", "", "按场景筛选（通用场景/角色扮演/视频配音/教育场景/客服场景/有声阅读/外语音色/多情感/趣味口音）")
	list.Flags().StringVar(&langFlt, "lang", "", "按语种筛选（中文/美式英语/日语等，完整词表见 --json 输出 languages 字段）")
	cmd.AddCommand(list)
	return cmd
}
