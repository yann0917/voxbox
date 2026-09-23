package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yann0917/voxbox/internal/task"
)

// WS 心跳（gorilla 官方 chat 示例模式）：服务端周期 ping，浏览器协议层自动 pong，
// 客户端无需配合；pong 刷新读超时，两个周期无响应即判死连接并回收。
const writeWait = 10 * time.Second // 单次写超时

// up 的 CheckOrigin 做同源校验：WS 会话可读取任务文本，不能对任意网页放行。
// 顺序：无 Origin（非浏览器客户端/CLI/测试）放行 → 本机 Origin（localhost/127.0.0.1，
// 覆盖 vite 代理等开发场景端口不一致）放行 → 严格同源（u.Host == r.Host）放行 → 拒绝。
var up = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
			return true
		}
		return u.Host == r.Host
	},
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub 维护 WS 客户端并广播任务事件；Notify 作为 task.Engine 的回调。
// 心跳参数在实例字段而非包级变量：随 Hub 生命周期配置，测试缩短周期不与
// 其他 Hub 的在途连接 goroutine 共享可变量。
type Hub struct {
	mu        sync.Mutex
	clients   map[*client]struct{}
	pingEvery time.Duration // 服务端 ping 周期
	pongWait  time.Duration // 读超时，须 > pingEvery
}

func NewHub() *Hub {
	return &Hub{
		clients:   map[*client]struct{}{},
		pingEvery: 30 * time.Second,
		pongWait:  70 * time.Second,
	}
}

// SetHeartbeat 自定义心跳参数（测试缩短周期用）；须在首个连接建立前调用。
func (h *Hub) SetHeartbeat(pingEvery, pongWait time.Duration) {
	h.pingEvery, h.pongWait = pingEvery, pongWait
}

func (h *Hub) Notify(ev task.Event) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.broadcast(raw)
}

func (h *Hub) broadcast(raw []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- raw:
		default: // 慢客户端直接丢弃，避免阻塞
		}
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// clientCount 当前连接数（测试观察僵死连接回收用）。
func (h *Hub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(h.pingEvery)
	defer ticker.Stop()
	for {
		select {
		case raw, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			_ = c.conn.WriteMessage(websocket.TextMessage, raw)
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = c.conn.Close() // 唤醒读泵走 unregister，避免死连接悬挂到读超时
				return
			}
		}
	}
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request, snapshotJSON func() []byte) {
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 64)}
	h.register(c)
	go h.writePump(c)
	// 连接建立即补发非终态任务快照，防漏消息
	if snapshotJSON != nil {
		if snap := snapshotJSON(); snap != nil {
			c.send <- snap
		}
	}
	// 读泵：仅处理关闭。读超时靠对端 pong 刷新（浏览器协议层自动应答），
	// 半开/僵死连接在 pongWait 内被清理，不再无限悬挂。
	go func() {
		defer func() {
			h.unregister(c)
			_ = conn.Close()
		}()
		_ = conn.SetReadDeadline(time.Now().Add(h.pongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(h.pongWait))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}
