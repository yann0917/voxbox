package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/server"
	"github.com/yann0917/voxbox/internal/service"
)

//go:embed all:webdist
var webDist embed.FS

func newServeCommand() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "启动 Web 控制台",
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if port >= 0 {
				cfg.Server.Port = port
			}
			svc, err := service.New(cfg)
			if err != nil {
				return err
			}
			defer svc.Close()
			srv := server.New(svc)
			svc.StartEngine(srv.Hub().Notify, 2)

			// 首次启动引导：users 表为空时创建 admin。随机密码只在本次控制台打印，
			// 首登强制改密；VOXBOX_ADMIN_PASSWORD 供部署自动化注入（不回显）。
			initialPassword, err := srv.EnsureBootstrapAdmin()
			if err != nil {
				return fmt.Errorf("初始化管理员账号失败: %w", err)
			}
			if initialPassword != "" && os.Getenv("VOXBOX_DESKTOP") != "1" {
				username := os.Getenv("VOXBOX_ADMIN_USERNAME")
				if username == "" {
					username = "admin"
				}
				fmt.Fprintf(os.Stderr, "\n=== 首次启动已创建管理员账号 ===\n")
				fmt.Fprintf(os.Stderr, "用户名: %s\n", username)
				fmt.Fprintf(os.Stderr, "初始密码: %s （仅本次显示，请立即登录修改）\n\n", initialPassword)
			}

			// MCP Streamable HTTP 端点：与 Web 控制台同进程同端口（/api/mcp），
			// 工具调用与 Web 任务共享同一引擎；鉴权与 /api 其余端点同层（Bearer token）。
			mcpSrv := newMCPServer(svc)
			srv.MountMCP(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
				return mcpSrv
			}, nil))

			// 配置文件监听：服务运行中 CLI config set / 手工编辑 config.yaml 的凭证
			// 变更热生效。失败仅降级告警，不阻断启动（Web 保存路径不依赖此监听）。
			stopWatch, err := config.Watch(func(c *config.Config) {
				svc.ReloadDiskConfig(c)
				fmt.Fprintf(os.Stderr, "配置文件变更已热加载: %s\n", config.Path())
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "配置文件监听不可用（Web 保存仍即时生效）: %v\n", err)
			} else {
				defer stopWatch()
			}

			handler := server.WithStatic(srv.Handler(), webDist)
			host := cfg.Server.Host
			if host == "" {
				host = "127.0.0.1"
			}
			// 统一绑定监听（显式端口与 0 端口同路径）：必须先 bind 后发就绪行，
			// 否则消费方（桌面壳/e2e）读到 VOXBOX_READY 时 socket 可能尚未可连。
			// ln 不 Close、直接交给 http.Serve，同时消除 listen-close-rebind 竞态。
			ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.Server.Port))
			if err != nil {
				if cfg.Server.Port == 0 {
					return fmt.Errorf("分配空闲端口失败: %w", err)
				}
				return fmt.Errorf("监听 %s:%d 失败: %w", host, cfg.Server.Port, err)
			}
			cfg.Server.Port = ln.Addr().(*net.TCPAddr).Port
			addr := ln.Addr().String()
			fmt.Printf("VOXBOX_READY addr=%s\n", addr) // stdout 机器可读就绪行（桌面壳契约，绑定后发出）
			fmt.Fprintf(os.Stderr, "voxbox Web 已启动: http://%s\n", addr)
			return http.Serve(ln, handler)
		},
	}
	cmd.Flags().IntVar(&port, "port", -1, "端口（默认取配置，0 为系统分配空闲端口）")
	return cmd
}
