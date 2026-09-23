package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yann0917/voxbox/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "查看与设置凭证及配置"}
	cmd.AddCommand(newConfigSetCmd(), newConfigListCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "设置配置项（volc.speech.app_id / volc.speech.access_token / volc.speech.api_key / volc.mediakit.api_key / server.port）",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := config.Set(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "已保存 %s\n", args[0])
			return nil
		},
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "查看配置（密钥打码）",
		RunE: func(c *cobra.Command, args []string) error {
			rows, err := config.List()
			if err != nil {
				return err
			}
			for _, kv := range rows {
				fmt.Printf("%-28s %s\n", kv.Key, kv.Value)
			}
			return nil
		},
	}
}
