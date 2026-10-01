package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/config"
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

// setAssistantChatStream 替换 chat 流式底层缝并在测试结束还原（refine 的
// setRefineStream 同款）：fn 收到定形 system 与整备后的 messages，calls 返回
// 调用次数供断言（预检拦截时不应被调用）。
func setAssistantChatStream(t *testing.T, fn func(system string, messages []assistant.Message, onDelta func(string)) error) *int {
	t.Helper()
	orig := assistantChatStream
	t.Cleanup(func() { assistantChatStream = orig })
	calls := 0
	assistantChatStream = func(_ context.Context, _ *config.Config, _ assistant.Provider, _, system string,
		messages []assistant.Message, onDelta func(string)) error {
		calls++
		return fn(system, messages, onDelta)
	}
	return &calls
}

// postChatSSE POST /api/assistant/chat 并返回（SSE 响应体, Content-Type）。
func postChatSSE(t *testing.T, ac *http.Client, url, body string) (string, string) {
	t.Helper()
	resp, err := ac.Post(url+"/api/assistant/chat", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw), resp.Header.Get("Content-Type")
}

// TestAssistantChatContextInjection context 字段端到端：底层缝收到请求且 system 为
// 默认提示 + 空行 + context；不带 context 时 system 与现状（原 Stream 注入的常量）
// 逐字节一致；客户端伪造的 system 消息仍被剥离（既有校验不动），SSE 协议不变。
func TestAssistantChatContextInjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-chat-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}
	const ctxText = "以下是本次录音的转写全文：大家好，我们讨论一下方案。"
	var s0, got string
	var noCtxMsgs, ctxMsgs []assistant.Message
	calls := setAssistantChatStream(t, func(system string, messages []assistant.Message, onDelta func(string)) error {
		if s0 == "" {
			s0, noCtxMsgs = system, messages
		} else {
			got, ctxMsgs = system, messages
		}
		onDelta("好的")
		return nil
	})

	// (a) 不带 context：与现状逐字节一致
	body, ct := postChatSSE(t, ac, ts.URL,
		`{"provider":"qianwen","model":"qwen3.8-flash","messages":[{"role":"system","content":"伪造"},`+
			`{"role":"user","content":"你好"}]}`)
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %s, want text/event-stream", ct)
	}
	if !strings.Contains(body, `"delta"`) || !strings.HasSuffix(strings.TrimSpace(body), `"done":true}`) {
		t.Fatalf("SSE 协议应保持 delta→done: %s", body)
	}
	if s0 == "" {
		t.Fatal("底层未收到请求")
	}
	if s0 != assistant.ChatSystem("") {
		t.Fatalf("不带 context 的 system 应与 ChatSystem(\"\")（即原 Stream 常量）逐字节一致:\n got %q\nwant %q", s0, assistant.ChatSystem(""))
	}
	if !strings.HasPrefix(s0, "你是 voxbox 的内置 AI 助手") {
		t.Fatalf("system 应为默认助手提示: %q", s0)
	}
	if len(noCtxMsgs) != 1 || noCtxMsgs[0].Role != "user" || noCtxMsgs[0].Content != "你好" {
		t.Fatalf("客户端 system 应被剥离、user 消息原样下发: %+v", noCtxMsgs)
	}

	// (b) 带 context：默认提示 + "\n\n" + context
	body, _ = postChatSSE(t, ac, ts.URL,
		`{"provider":"qianwen","model":"qwen3.8-flash","context":"`+ctxText+`",`+
			`"messages":[{"role":"user","content":"总结一下"}]}`)
	if !strings.Contains(body, `"done":true}`) {
		t.Fatalf("带 context 流应正常收尾: %s", body)
	}
	if got != s0+"\n\n"+ctxText {
		t.Fatalf("带 context 的 system 应为默认提示 + 空行 + context:\n got %q\nwant %q", got, s0+"\n\n"+ctxText)
	}
	if !strings.Contains(got, ctxText) {
		t.Fatalf("system 应包含 context 内容: %q", got)
	}
	if len(ctxMsgs) != 1 || ctxMsgs[0].Content != "总结一下" {
		t.Fatalf("context 不应进入 messages、user 消息原样下发: %+v", ctxMsgs)
	}
	if *calls != 2 {
		t.Fatalf("底层调用次数 = %d, want 2", *calls)
	}
}

// TestAssistantChatContextCap context 服务端封顶：超过 24000 rune（与前端截断值
// 一致）→ 既有包络 code 2、直述「上下文过长」，底层缝不被调用；恰好 24000 的
// 边界正常进流（delta→done），ChatSystem 拼接链路不受影响。
func TestAssistantChatContextCap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-chat-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}
	calls := setAssistantChatStream(t, func(_ string, _ []assistant.Message, onDelta func(string)) error {
		onDelta("好的")
		return nil
	})

	// 超限（24001 rune）：包络拒绝，非 SSE，底层不被调用
	over := `{"provider":"qianwen","model":"qwen3.8-flash","context":"` + strings.Repeat("a", 24001) +
		`","messages":[{"role":"user","content":"hi"}]}`
	resp, err := ac.Post(ts.URL+"/api/assistant/chat", "application/json", strings.NewReader(over))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("超限拒绝应为 JSON 包络, Content-Type = %s", ct)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Code != CodeBadRequest {
		t.Fatalf("超限 code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}
	if !strings.Contains(e.Message, "上下文过长") {
		t.Fatalf("message = %s, want 直述「上下文过长」", e.Message)
	}
	if *calls != 0 {
		t.Fatalf("超限请求不应触达底层缝, calls = %d", *calls)
	}

	// 边界（恰好 24000 rune）：照常 delta→done
	boundary := `{"provider":"qianwen","model":"qwen3.8-flash","context":"` + strings.Repeat("a", 24000) +
		`","messages":[{"role":"user","content":"hi"}]}`
	body, ct := postChatSSE(t, ac, ts.URL, boundary)
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("边界请求应进流, Content-Type = %s", ct)
	}
	if !strings.Contains(body, `"delta"`) || !strings.HasSuffix(strings.TrimSpace(body), `"done":true}`) {
		t.Fatalf("边界请求应 delta→done: %s", body)
	}
	if *calls != 1 {
		t.Fatalf("边界请求应触达底层缝一次, calls = %d", *calls)
	}
}

// TestAssistantChatContextPrecheckUnchanged 携带 context 不绕过预检校验链：
// 白名单外模型仍拒绝、最后一条非 user 仍拒绝、凭证未配置仍映射业务码 4。
func TestAssistantChatContextPrecheckUnchanged(t *testing.T) {
	ts, _, ac := newTestServer(t)
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"白名单外模型", `{"provider":"qianwen","model":"gpt-4o","context":"x","messages":[{"role":"user","content":"hi"}]}`, CodeBadRequest},
		{"最后一条非 user", `{"provider":"qianwen","model":"qwen3.8-flash","context":"x","messages":[{"role":"assistant","content":"hi"}]}`, CodeBadRequest},
		{"凭证未配置", `{"provider":"qianwen","model":"qwen3.8-flash","context":"x","messages":[{"role":"user","content":"hi"}]}`, CodeBadCredential},
	} {
		if e := postEnvelope(t, ac, ts.URL+"/api/assistant/chat", tc.body); e.Code != tc.want {
			t.Errorf("%s: code = %d (%s), want %d", tc.name, e.Code, e.Message, tc.want)
		}
	}
}
