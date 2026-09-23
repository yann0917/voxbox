package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yann0917/voxbox/internal/service"
)

// TestMCPHTTPEndpoint 验证 serve 内嵌的 MCP Streamable HTTP 端点：
// 无 token 401（JSON-RPC 错误体）；轮换出的 Bearer token 可完成 initialize 握手。
func TestMCPHTTPEndpoint(t *testing.T) {
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	s := New(svc)
	svc.StartEngine(s.Hub().Notify, 2)
	// 挂载一个无工具的测试 Server：握手与路由验证不依赖具体工具
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "voxbox-test", Version: "0"}, nil)
	s.MountMCP(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))

	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	ac := loginTestClient(t, s, ts)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"it","version":"0"}}}`
	post := func(token string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("POST", ts.URL+"/api/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// 无 token：401 + JSON-RPC 错误体（不套业务包络）
	resp := post("")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token HTTP status = %d, want 401", resp.StatusCode)
	}
	var rpcErr struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcErr); err != nil || rpcErr.Error.Code != -32001 {
		t.Fatalf("401 响应体应为 JSON-RPC 错误: %v", rpcErr)
	}

	// 轮换出 token（走真实 /api/auth/token/rotate）后握手成功
	tr, err := ac.Post(ts.URL+"/api/auth/token/rotate", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var tokenEnv envelope
	_ = json.NewDecoder(tr.Body).Decode(&tokenEnv)
	tr.Body.Close()
	if tokenEnv.Code != 0 {
		t.Fatalf("rotate code = %d (%s)", tokenEnv.Code, tokenEnv.Message)
	}
	tokData, _ := tokenEnv.Data.(map[string]any)
	token, _ := tokData["api_token"].(string)
	if !strings.HasPrefix(token, "tbx_") {
		t.Fatalf("api_token = %q, want tbx_ 前缀", token)
	}

	resp = post(token)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("带 token HTTP status = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	raw := string(buf[:n])
	// Streamable HTTP 可能回 application/json 或 SSE 帧，两种都应携带 initialize 结果
	if !strings.Contains(raw, `"serverInfo"`) || !strings.Contains(raw, "voxbox-test") {
		t.Fatalf("initialize 响应缺少 serverInfo: %s", raw)
	}
}
