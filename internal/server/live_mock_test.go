package server

// live_mock_test.go audiocpp live 端点的裸 TCP mock(与 localruntime 包测试同款):
// httptest 的 Go server 会让 Go http client 把早期 SSE 响应扣到请求体写完才投递
// (真机 server 不会),须裸 TCP 复现「响应先于请求体结束」的真实流式时序。

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type liveMockServer struct {
	t  *testing.T
	ln net.Listener

	chunkN atomic.Int64 // 收到的请求体 chunk 数(静音续命断言用)

	mu       sync.Mutex
	sessions int
	queries  []string
}

func startLiveMockServer(t *testing.T, onOpen func(n int, conn net.Conn, chunk func(string)), onDrain func(n int, conn net.Conn, chunk func(string))) *liveMockServer {
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

func (m *liveMockServer) handle(conn net.Conn, onOpen func(n int, conn net.Conn, chunk func(string)), onDrain func(n int, conn net.Conn, chunk func(string))) {
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
		onOpen(n, conn, chunk)
	}
	m.drain(rd) // 排干 chunked 请求体(终止 chunk 即结束,不能等 TCP EOF)
	if onDrain != nil {
		onDrain(n, conn, chunk)
	}
	chunk("data: [DONE]\n\n")
	fmt.Fprint(conn, "0\r\n\r\n")
}

// drain 逐 chunk 排干请求体,遇终止 chunk(尺寸 0)返回;每收到一个数据 chunk 计数一次。
func (m *liveMockServer) drain(rd *bufio.Reader) {
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
		m.chunkN.Add(1)
	}
}

// chunks 收到的请求体数据 chunk 数(含用户音频与静音续命帧)。
func (m *liveMockServer) chunks() int { return int(m.chunkN.Load()) }

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
