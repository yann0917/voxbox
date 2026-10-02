// Package server 提供 gin HTTP 服务与 WebSocket hub。
package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

type Server struct {
	svc        *service.Service
	hub        *Hub
	desktop    bool         // 桌面形态（VOXBOX_DESKTOP=1）：未登录请求注入库内 admin 免登录直达
	mcpHandler http.Handler // MCP Streamable HTTP 端点（serve 装配时可选挂载，须在 Handler() 之前）

	// newLiveEngine 实时字幕引擎构造缝（live.go：生产按 engine 参数分派，单测注入 mock）。
	newLiveEngine newLiveEngineFn
}

func New(svc *service.Service) *Server {
	s := &Server{svc: svc, hub: NewHub(), desktop: os.Getenv("VOXBOX_DESKTOP") == "1"}
	// 模型状态机 → WS:下载进度/状态迁移经 Hub 推送,前端 /api/models 免轮询
	svc.LocalModels().SetNotifier(s.hub.NotifyModel)
	s.newLiveEngine = s.defaultNewLiveEngine
	return s
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

// modelsSnapshotJSON 返回在途模型快照消息(downloading/verifying;空返回 nil 不补发)。
// 与任务快照同理:连接晚于下载开始或 WS 断连重连后,前端以此对齐缓存。
func (s *Server) modelsSnapshotJSON() []byte {
	var evs []localmodel.Event
	for _, v := range s.svc.LocalModels().List() {
		if v.Status == localmodel.StatusDownloading || v.Status == localmodel.StatusVerifying {
			evs = append(evs, localmodel.Event{
				ID: v.Entry.ID, Status: v.Status,
				DownloadedBytes: v.DownloadedBytes, TotalBytes: v.TotalBytes, Error: v.Error,
			})
		}
	}
	if len(evs) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"type": "model.snapshot", "models": evs})
	return raw
}
