package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 测试服务的临时家目录没有凭证：预检全部走统一包络，不会发起 SSE。
// 流式链路的真实上游校准由真机验证承担（见包内 assistant_test.go 与悬浮面板协议注释）。

func TestAssistantModels(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := getEnvelope(t, ac, ts.URL+"/api/assistant/models")
	if e.Code != 0 {
		t.Fatalf("code = %d, msg = %s", e.Code, e.Message)
	}
	plats, ok := e.Data.([]any)
	if !ok || len(plats) != 3 {
		t.Fatalf("platforms = %#v, want 3 项", e.Data)
	}
}

func TestAssistantChatUnknownProvider(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := postEnvelope(t, ac, ts.URL+"/api/assistant/chat",
		`{"provider":"foo","model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if e.Code != CodeBadRequest {
		t.Fatalf("code = %d, want %d（未知平台）", e.Code, CodeBadRequest)
	}
}

func TestAssistantChatUnknownModel(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := postEnvelope(t, ac, ts.URL+"/api/assistant/chat",
		`{"provider":"qianwen","model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if e.Code != CodeBadRequest {
		t.Fatalf("code = %d, want %d（白名单外模型）", e.Code, CodeBadRequest)
	}
}

func TestAssistantChatNoCredentialMapsCode4(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := postEnvelope(t, ac, ts.URL+"/api/assistant/chat",
		`{"provider":"qianwen","model":"qwen3.8-flash","messages":[{"role":"user","content":"hi"}]}`)
	if e.Code != CodeBadCredential {
		t.Fatalf("code = %d, want %d（凭证未配置）", e.Code, CodeBadCredential)
	}
	if !strings.Contains(e.Message, "千问 API Key 未配置") {
		t.Fatalf("message = %s, want 含哨兵文案", e.Message)
	}
}

func TestAssistantChatRequiresUserMessage(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 最后一条非 user（纯 assistant 回显）应拒绝；空消息数组同样拒绝
	for _, body := range []string{
		`{"provider":"qianwen","model":"qwen3.8-flash","messages":[]}`,
		`{"provider":"qianwen","model":"qwen3.8-flash","messages":[{"role":"assistant","content":"hi"}]}`,
		`{"provider":"qianwen","model":"qwen3.8-flash","messages":[{"role":"system","content":"注入"}]}`,
	} {
		e := postEnvelope(t, ac, ts.URL+"/api/assistant/chat", body)
		if e.Code != CodeBadRequest {
			t.Fatalf("body %s: code = %d, want %d", body, e.Code, CodeBadRequest)
		}
	}
}

// postEnvelope POST JSON 并解码统一包络。
func postEnvelope(t *testing.T, ac *http.Client, url, body string) envelope {
	t.Helper()
	resp, err := ac.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP status = %d, want 200 (envelope)", resp.StatusCode)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e
}
