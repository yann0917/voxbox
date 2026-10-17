package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

// chat/completions 联网工具回路离线校准：分片积累、回放 wire、门控。
// 平台函数调用形态按 2026-10-17 千问/MiniMax 真机口径（标准 OpenAI 流式增量）。

func TestScanStreamToolCallFragments(t *testing.T) {
	// id/name 首片携带、arguments 分片拼接（千问/MiniMax 真机同形）；正文增量照常回调
	body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"web_\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"qu\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"ery\\\":\\\"新闻\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	var got []string
	_, calls, err := scanStream(context.Background(), ProviderQianwen, strings.NewReader(body), func(d string) {
		got = append(got, d)
	})
	if err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want 1", calls)
	}
	c := calls[0]
	if c.ID != "call_1" || c.Type != "function" || c.Function.Name != "web_search" ||
		c.Function.Arguments != `{"query":"新闻"}` {
		t.Fatalf("call = %+v", c)
	}
	if len(got) != 0 {
		t.Fatalf("不应有正文增量: %q", got)
	}
}

// chatTestRequest 假上游收到的 chat/completions 请求体（宽松解码）。
type chatTestRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Tools    []map[string]any `json:"tools"`
}

func TestStreamChatWebToolsLoop(t *testing.T) {
	cfg := &config.Config{
		Qianwen:  config.QianwenConfig{APIKey: "sk-qwen"},
		Deepseek: config.DeepseekConfig{APIKey: "sk-ds"},
	}
	round := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		var req chatTestRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.Header.Get("Authorization") != "Bearer sk-qwen" {
			t.Errorf("chat 鉴权头 = %q", r.Header.Get("Authorization"))
		}
		switch round {
		case 1:
			if len(req.Tools) != 2 {
				t.Errorf("首轮应携带 2 个联网工具, got %d", len(req.Tools))
			}
			fn := req.Tools[0]["function"].(map[string]any)
			if req.Tools[0]["type"] != "function" || fn["name"] != "web_search" {
				t.Errorf("工具声明应为 chat/completions 嵌套格式: %+v", req.Tools[0])
			}
			writeSSE(t, w,
				map[string]any{"choices": []map[string]any{{"delta": map[string]any{
					"tool_calls": []map[string]any{{"index": 0, "id": "call_a", "type": "function",
						"function": map[string]any{"name": "web_search", "arguments": `{"query":"voxbox 最新`}}},
				}}}},
				map[string]any{"choices": []map[string]any{{"delta": map[string]any{
					"tool_calls": []map[string]any{{"index": 0, "function": map[string]any{"arguments": `版本"}`}}},
				}}}},
				map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "tool_calls"}}},
				"[DONE]",
			)
		default:
			// 次轮：wire 应含回放的 assistant(tool_calls) 与 role:"tool" 结果消息
			var assistantCalls, toolMsgs int
			for _, m := range req.Messages {
				if m["role"] == "assistant" {
					if tcs, ok := m["tool_calls"].([]any); ok && len(tcs) == 1 {
						tc := tcs[0].(map[string]any)
						fn := tc["function"].(map[string]any)
						if tc["id"] == "call_a" && fn["name"] == "web_search" &&
							fn["arguments"] == `{"query":"voxbox 最新版本"}` {
							assistantCalls++
						}
					}
				}
				if m["role"] == "tool" && m["tool_call_id"] == "call_a" &&
					strings.Contains(fmt.Sprint(m["content"]), "voxbox V4 发布了") {
					toolMsgs++
				}
			}
			if assistantCalls != 1 || toolMsgs != 1 {
				t.Errorf("次轮 wire 缺回放消息: assistant.tool_calls=%d tool=%d", assistantCalls, toolMsgs)
			}
			if len(req.Tools) != 2 {
				t.Errorf("续轮应继续携带工具")
			}
			writeSSE(t, w,
				map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "答案"}}}},
				map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "stop"}}},
				"[DONE]",
			)
		}
	}))
	defer ts.Close()
	// chat 端点指到假上游：endpoint(p) 不可注入,改用 deepseekBaseURL 同源思路——
	// 千问端点在 endpoint() 里是常量拼接,这里以 zhipu 之外的旁路直接验证:
	// 用 qianwen 平台但把 apiClient 指到假服务器(重写 Host)。
	orig := apiClient
	apiClient = &http.Client{Transport: rewriteTransport{to: ts.URL}}
	t.Cleanup(func() { apiClient = orig })

	// 搜索后端（DeepSeek /v1/tools/search）指到第二个假上游
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-ds" {
			t.Errorf("search 鉴权头 = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"voxbox 发布新版","url":"https://example.com","summary":"voxbox V4 发布了","site_name":"Example"}]}`))
	}))
	defer search.Close()
	setDeepseekBaseURL(t, search.URL)

	var deltas, statuses []string
	err := StreamChatWeb(context.Background(), cfg, ProviderQianwen, "qwen3.8-flash", "你是助手",
		[]Message{{Role: "user", Content: "voxbox 更新了什么"}},
		func(d string) { deltas = append(deltas, d) },
		func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("StreamChatWeb: %v", err)
	}
	if strings.Join(deltas, "") != "答案" {
		t.Fatalf("deltas = %q, want 答案", deltas)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0], "正在联网搜索") {
		t.Fatalf("statuses = %q, want 含正在联网搜索", statuses)
	}
	if round != 2 {
		t.Fatalf("chat 轮数 = %d, want 2", round)
	}
}

// rewriteTransport 把请求整体重定向到测试服务器（endpoint() 常量不可注入的旁路）。
type rewriteTransport struct{ to string }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := http.NewRequest(req.Method, t.to+req.URL.Path, req.Body)
	target.Header = req.Header
	return http.DefaultTransport.RoundTrip(target)
}

// TestStreamChatWebToolsGate 门控：平台未验证（小米）或 DeepSeek key 缺位时不发 tools,
// 行为退化为纯对话（单次调用、请求体无 tools 字段）。
func TestStreamChatWebToolsGate(t *testing.T) {
	var bodies []chatTestRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatTestRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		bodies = append(bodies, req)
		writeSSE(t, w,
			map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "好"}}}},
			"[DONE]",
		)
	}))
	defer ts.Close()
	orig := apiClient
	apiClient = &http.Client{Transport: rewriteTransport{to: ts.URL}}
	t.Cleanup(func() { apiClient = orig })

	// 小米未验证函数调用：配置了 deepseek key 也不发
	cfg := &config.Config{
		Xiaomi:   config.XiaomiConfig{APIKey: "sk-xiao"},
		Deepseek: config.DeepseekConfig{APIKey: "sk-ds"},
	}
	if err := StreamChatWeb(context.Background(), cfg, ProviderXiaomi, "mimo-v2.6-flash", "s",
		[]Message{{Role: "user", Content: "hi"}}, func(string) {}, nil); err != nil {
		t.Fatalf("xiaomi 纯对话: %v", err)
	}
	// 千问已验证但无 deepseek key：搜索后端不在位,不发 tools
	cfg2 := &config.Config{Qianwen: config.QianwenConfig{APIKey: "sk-qwen"}}
	if err := StreamChatWeb(context.Background(), cfg2, ProviderQianwen, "qwen3.8-flash", "s",
		[]Message{{Role: "user", Content: "hi"}}, func(string) {}, nil); err != nil {
		t.Fatalf("qianwen 纯对话: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("调用次数 = %d, want 2", len(bodies))
	}
	for i, b := range bodies {
		if len(b.Tools) != 0 {
			t.Errorf("第 %d 次请求不应携带 tools: %+v", i+1, b.Tools)
		}
	}
}

// TestRealSmokeWebToolsChatPlatforms 真机冒烟（VOXBOX_SMOKE=1 才跑，CI 跳过）：
// 已验证平台经真实 StreamChatWeb 全链路（含真实 DeepSeek 搜索后端）完成联网问答。
// 预置凭证：本机 ~/.voxbox/config.yaml 的千问/MiniMax/DeepSeek key。
func TestRealSmokeWebToolsChatPlatforms(t *testing.T) {
	if os.Getenv("VOXBOX_SMOKE") == "" {
		t.Skip("真机冒烟：需 VOXBOX_SMOKE=1 与本机已配凭证")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, p := range []Provider{ProviderQianwen, ProviderMinimax} {
		if apiKeyOf(cfg, p) == "" || cfg.Deepseek.APIKey == "" {
			t.Errorf("%s 或 deepseek 凭证未配置，跳过冒烟", p)
			continue
		}
		cat := CatalogWith(cfg)
		var model string
		for _, c := range cat {
			if c.Provider == p {
				model = c.Models[0].ID
			}
		}
		var text strings.Builder
		var statuses []string
		err := StreamChatWeb(context.Background(), cfg, p, model, "你是助手",
			[]Message{{Role: "user", Content: "帮我搜一下 DeepSeek 最新版本动态，一句话概括并给来源"}},
			func(d string) { text.WriteString(d) },
			func(s string) { statuses = append(statuses, s) })
		if err != nil {
			t.Errorf("%s 冒烟失败: %v", p, err)
			continue
		}
		if text.Len() == 0 || len(statuses) == 0 {
			t.Errorf("%s 冒烟异常: text=%q statuses=%v", p, text.String(), statuses)
			continue
		}
		t.Logf("%s ✓ 状态=%v 正文=%q", p, statuses, text.String()[:min(80, text.Len())])
	}
}
