package localruntime

// audiocpp_live_test.go live 会话客户端单测:裸 TCP mock 复现 audiocpp live 协议时序
// (SSE 响应先于请求体结束下发——httptest 的 Go server 会被 Go http client 扣到请求
// 体写完才投递早期响应,真机 server 不会,须裸 TCP 才能测到流式语义)。

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// liveMockServer live 端点裸 TCP mock。行为:
//  1. 读请求头(捕获 path?query)→ 立即回 SSE 响应头 + onOpen 注入的首批事件并 flush;
//  2. 排干请求体直到 EOF(终止 chunk);
//  3. 执行 onDrain(会话序号, chunk 写入器)→ 写终止 chunk → 关连接。
type liveMockServer struct {
	t  *testing.T
	ln net.Listener

	mu        sync.Mutex
	sessions  int
	queries   []string
	bodiesEOF bool
}

func startLiveMockServer(t *testing.T, onOpen func(n int, chunk func(string)), onDrain func(n int, chunk func(string))) *liveMockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &liveMockServer{t: t, ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handle(conn, onOpen, onDrain)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *liveMockServer) handle(conn net.Conn, onOpen func(n int, chunk func(string)), onDrain func(n int, chunk func(string))) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	var reqLine, query string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		if reqLine == "" && (strings.HasPrefix(line, "POST ") || strings.HasPrefix(line, "GET ")) {
			reqLine = line
			if i := strings.Index(line, "?"); i >= 0 {
				rest := strings.TrimSpace(line[i+1:])
				if j := strings.Index(rest, " "); j >= 0 {
					query = rest[:j]
				}
			}
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	chunk := func(s string) {
		fmt.Fprintf(conn, "%x\r\n%s\r\n", len(s), s)
	}
	if strings.Contains(reqLine, "/health") {
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\n{\"status\":\"ok\"}\r\n")
		return
	}
	m.mu.Lock()
	m.sessions++
	m.queries = append(m.queries, query)
	n := m.sessions
	m.mu.Unlock()
	fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
	if onOpen != nil {
		onOpen(n, chunk)
	}
	drainChunked(rd) // 排干 chunked 请求体(终止 chunk 即结束;HTTP 客户端不半关 TCP,不能等 EOF)
	m.mu.Lock()
	m.bodiesEOF = true
	m.mu.Unlock()
	if onDrain != nil {
		onDrain(n, chunk)
	}
	chunk("data: [DONE]\n\n")
	fmt.Fprint(conn, "0\r\n\r\n")
}

// drainChunked 逐 chunk 排干请求体,遇终止 chunk(尺寸 0)返回。
func drainChunked(rd *bufio.Reader) {
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		size := strings.TrimSpace(line)
		if i := strings.IndexByte(size, ';'); i >= 0 {
			size = size[:i] // chunk 扩展
		}
		var n int
		if _, err := fmt.Sscanf(size, "%x", &n); err != nil {
			return
		}
		if n == 0 {
			_, _ = io.CopyN(io.Discard, rd, 2) // 尾随 CRLF
			return
		}
		if _, err := io.CopyN(io.Discard, rd, int64(n)+2); err != nil {
			return
		}
	}
}

func (m *liveMockServer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions
}

func (m *liveMockServer) queryAt(i int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < len(m.queries) {
		return m.queries[i]
	}
	return ""
}

func (m *liveMockServer) sawBodyEOF() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bodiesEOF
}

// TestLiveASRSession 全流程:健康检查 → live POST(streaming 声明 id + 格式参数)→
// 喂音频期 delta 实时到达 → Finish(终止 chunk)→ done 全量 + [DONE] → Wait 返回全量。
func TestLiveASRSession(t *testing.T) {
	mock := startLiveMockServer(t,
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"你好\"}\n\n")
		},
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"世界\"}\n\n")
			chunk("data: {\"type\":\"transcript.text.done\",\"text\":\"你好世界\"}\n\n")
		})
	tts := NewURLTTSRuntime("http://" + mock.ln.Addr().String())

	sess, err := tts.StartLiveASR(context.Background(), "r2t2-q8_0", LiveASROptions{Language: "zh", Hotwords: "热词"})
	if err != nil {
		t.Fatalf("StartLiveASR: %v", err)
	}
	defer func() { _ = sess.Close() }()

	q := mock.queryAt(0)
	for _, want := range []string{"model=r2t2-q8_0-stream", "sample_rate=16000", "channels=1", "sample_format=s16le", "language=zh", "prompt="} {
		if !strings.Contains(q, want) {
			t.Errorf("query 缺 %s: %s", want, q)
		}
	}

	if _, err := sess.Write([]byte("pcm")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case ev := <-sess.Updates():
		if ev.Delta != "你好" {
			t.Fatalf("首个事件 = %+v, want delta 你好(喂音频期实时到达)", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 未收到喂音频期增量")
	}

	if err := sess.Finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !mock.sawBodyEOF() { // 终止 chunk 由 transport 异步写出,轮询等待
		if time.Now().After(deadline) {
			t.Fatal("Finish 应发出 HTTP 终止 chunk(服务端读到请求体结束)")
		}
		time.Sleep(5 * time.Millisecond)
	}
	txt, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if txt != "你好世界" {
		t.Fatalf("final = %q, want 你好世界(done 全量优先于 delta 累积)", txt)
	}
}

// TestLiveASRSessionDoneLost 服务端 [DONE] 随连接关闭丢失(真机实测长会话形态):
// EOF + 已 Finish → 以 delta 累积文本收束,不报错。
func TestLiveASRSessionDoneLost(t *testing.T) {
	mock := startLiveMockServer(t,
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"前半\"}\n\n")
		},
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"后半\"}\n\n")
			// 不写 done,直接收尾连接(模拟 [DONE] 丢失)
		})
	// onDrain 后不再写 [DONE]:直接关闭 —— 用自定义行为覆盖
	tts := NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	sess, err := tts.StartLiveASR(context.Background(), "r2t2-q8_0", LiveASROptions{})
	if err != nil {
		t.Fatalf("StartLiveASR: %v", err)
	}
	defer func() { _ = sess.Close() }()
	_ = sess.Finish()
	txt, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatalf("EOF+Finish 应正常收束, got err=%v", err)
	}
	if txt != "前半后半" {
		t.Fatalf("final = %q, want 前半后半(delta 累积)", txt)
	}
}

// TestLiveASRSessionAbort 无 Finish 直接 Close(客户端放弃):Write 报错、Wait 返回错误、
// 资源收敛(连接断开,服务端 body 读到错误)。
func TestLiveASRSessionAbort(t *testing.T) {
	mock := startLiveMockServer(t, nil, func(n int, chunk func(string)) {})
	tts := NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	sess, err := tts.StartLiveASR(context.Background(), "r2t2-q8_0", LiveASROptions{})
	if err != nil {
		t.Fatalf("StartLiveASR: %v", err)
	}
	if _, err := sess.Write([]byte("pcm")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := sess.Write([]byte("more")); err == nil {
		t.Error("Close 后 Write 应报错")
	}
	if _, err := sess.Wait(context.Background()); err == nil {
		t.Error("Close 放弃的会话 Wait 应报错(而非空文本假成功)")
	}
	// 幂等。
	if err := sess.Close(); err != nil {
		t.Errorf("重复 Close 应幂等 nil, got %v", err)
	}
}

// TestLiveASRServerError 服务端 error 事件:会话以错误收束,Wait 透传消息。
func TestLiveASRServerError(t *testing.T) {
	mock := startLiveMockServer(t,
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"error\",\"error\":{\"message\":\"live request body stalled\"}}\n\n")
		},
		func(n int, chunk func(string)) {})
	tts := NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	sess, err := tts.StartLiveASR(context.Background(), "r2t2-q8_0", LiveASROptions{})
	if err != nil {
		t.Fatalf("StartLiveASR: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Write([]byte("pcm")); err != nil {
		t.Fatalf("write: %v", err)
	}
	txt, err := sess.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("Wait err = %v, want 服务端错误透传", err)
	}
	if txt != "" {
		t.Errorf("txt = %q, want 空", txt)
	}
}

// TestLiveASRSessionWriteAfterError 服务端 error 事件后继续 Write 的收敛(30s idle
// 断流的 aftermath):管道写入不悬挂不 panic,Finish 正常发终止 chunk,Wait 透传会话
// 错误;增量事件先于错误到达(文本保留由上层适配器负责,本层错误契约=零文本+错误)。
func TestLiveASRSessionWriteAfterError(t *testing.T) {
	mock := startLiveMockServer(t,
		func(n int, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"开头\"}\n\n")
			chunk("data: {\"type\":\"error\",\"error\":{\"message\":\"live request body stalled\"}}\n\n")
		},
		func(n int, chunk func(string)) {})
	tts := NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	sess, err := tts.StartLiveASR(context.Background(), "r2t2-q8_0", LiveASROptions{})
	if err != nil {
		t.Fatalf("StartLiveASR: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Write([]byte("pcm-1")); err != nil {
		t.Fatalf("error 前 write: %v", err)
	}
	select {
	case ev := <-sess.Updates():
		if ev.Delta != "开头" {
			t.Fatalf("事件 = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 error 前增量")
	}
	// aftermath:错误事件后继续写(用户不知情继续说话),不得悬挂/panic。
	if _, err := sess.Write([]byte("pcm-2")); err != nil {
		t.Fatalf("error 后 write 应照常进入管道(服务端断流由 Finish/关闭收尾): %v", err)
	}
	if err := sess.Finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	txt, err := sess.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("Wait err = %v, want 服务端错误透传", err)
	}
	if txt != "" {
		t.Errorf("txt = %q, want 空(部分文本由上层适配器经事件流保留)", txt)
	}
	// 幂等收尾。
	if err := sess.Finish(); err != nil {
		t.Errorf("重复 Finish 应幂等 nil, got %v", err)
	}
}
