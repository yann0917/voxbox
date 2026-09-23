// Package server 提供 gin HTTP 服务与 WebSocket hub。
package server

import (
	"encoding/json"
	"net/http"

	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

type Server struct {
	svc        *service.Service
	hub        *Hub
	mcpHandler http.Handler // MCP Streamable HTTP 端点（serve 装配时可选挂载，须在 Handler() 之前）
}

func New(svc *service.Service) *Server {
	return &Server{svc: svc, hub: NewHub()}
}

func (s *Server) Hub() *Hub { return s.hub }

// MountMCP 挂载 MCP Streamable HTTP 端点（路由 /api/mcp，All-methods）。
// 必须在 Handler() 构建路由之前调用。
func (s *Server) MountMCP(h http.Handler) { s.mcpHandler = h }

// snapshotJSONFor 返回非终态任务快照消息（按用户收窄；空=全量，admin 用）。
func (s *Server) snapshotJSONFor(userID string) []byte {
	items, _, err := s.svc.DB().ListTasks("", []store.TaskStatus{store.StatusPending, store.StatusRunning}, 100, 0, userID)
	if err != nil || len(items) == 0 {
		return nil
	}
	dtos := make([]taskDTO, 0, len(items))
	for _, t := range items {
		dtos = append(dtos, toTaskDTO(t))
	}
	raw, _ := json.Marshal(map[string]any{"type": "task.snapshot", "tasks": dtos})
	return raw
}
