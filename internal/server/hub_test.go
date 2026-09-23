package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yann0917/voxbox/internal/task"
)

func TestHubBroadcast(t *testing.T) {
	ts, s, ac := newTestServer(t)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": []string{sessionCookieHeader(t, ac, ts)}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	s.Hub().Notify(task.Event{Type: "progress", TaskID: "x", Progress: 42, Note: "测试"})

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	// 注：Progress 为 JSON 数字，序列化后不含引号，故断言 "42" 调整为 42。
	if !strings.Contains(string(msg), `"progress"`) || !strings.Contains(string(msg), "42") {
		t.Errorf("msg = %s", msg)
	}
}

// TestHubPingPongKeepalive 心跳保活：缩短 ping 周期与读超时，客户端应答 pong 时
// 连接跨多个读超时窗口存活且能继续收广播；服务端 ping 按期到达。
func TestHubPingPongKeepalive(t *testing.T) {
	ts, s, ac := newTestServer(t)
	s.Hub().SetHeartbeat(50*time.Millisecond, 300*time.Millisecond)

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": []string{sessionCookieHeader(t, ac, ts)}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	pings := make(chan struct{}, 64)
	ws.SetPingHandler(func(appData string) error {
		pings <- struct{}{}
		return ws.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(2*time.Second))
	})
	// gorilla 契约：任一次读错误后连接即失效，不得再读。整个测试只用这一个读泵，
	// 存活/广播信号都经通道回传。
	readErr := make(chan error, 1)
	gotMsg := make(chan []byte, 8)
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			gotMsg <- msg
		}
	}()

	// 熬过 3 个读超时窗口（300ms×3）：pong 未刷新服务端读超时的话，连接早已被关闭
	select {
	case err := <-readErr:
		t.Fatalf("连接在保活窗口内被服务端关闭: %v", err)
	case <-time.After(900 * time.Millisecond):
	}
	if len(pings) == 0 {
		t.Fatal("未收到服务端 ping")
	}

	// 连接仍存活：广播可达
	s.Hub().Notify(task.Event{Type: "progress", TaskID: "keepalive", Progress: 1})
	select {
	case msg := <-gotMsg:
		if !strings.Contains(string(msg), "keepalive") {
			t.Errorf("msg = %s", msg)
		}
	case err := <-readErr:
		t.Fatalf("pong 正常时连接应存活: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("超时未收到广播")
	}
}

// TestHubDeadClientReaped 不应答 pong 的僵死连接在读超时后被服务端清理。
func TestHubDeadClientReaped(t *testing.T) {
	ts, s, ac := newTestServer(t)
	s.Hub().SetHeartbeat(50*time.Millisecond, 300*time.Millisecond)

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": []string{sessionCookieHeader(t, ac, ts)}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	// 不读消息 → 不应答 ping（默认 ping handler 也不会被触发）

	// 广播进 send 通道，证明已注册
	s.Hub().Notify(task.Event{Type: "progress", TaskID: "x", Progress: 1})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.Hub().Notify(task.Event{Type: "progress", TaskID: "probe", Progress: 1})
		time.Sleep(100 * time.Millisecond)
		if s.Hub().clientCount() == 0 {
			return // 读超时触发 unregister，僵死连接已被回收
		}
	}
	t.Fatal("僵死连接未被清理")
}
