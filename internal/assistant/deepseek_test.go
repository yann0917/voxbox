package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

// 离线校准 /responses 协议与联网工具回路：deepseekBaseURL 指向 httptest 假上游，
// 事件形态按 2026-10-17 api-docs 文档口径构造；真机冒烟由用户配置 key 后补。

// writeSSE 按 SSE 帧格式写出事件（data: JSON + 空行）。
func writeSSE(t *testing.T, w http.ResponseWriter, events ...any) {
	t.Helper()
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}
}

func TestScanResponsesStreamDeltas(t *testing.T) {
	// 思考通道增量不回调；正文增量按序回调；completed 终止
	body := "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"思考\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"你\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"好\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	var got []string
	err := scanResponsesStream(context.Background(), strings.NewReader(body), func(d string) {
		got = append(got, d)
	}, func(responsesOutputItem) {})
	if err != nil {
		t.Fatalf("scanResponsesStream: %v", err)
	}
	if strings.Join(got, "") != "你好" {
		t.Fatalf("deltas = %q, want 你好", got)
	}
}

func TestScanResponsesStreamFailedEvent(t *testing.T) {
	body := "data: {\"type\":\"response.failed\",\"error\":{\"code\":402,\"message\":\"余额不足\"}}\n\n"
	err := scanResponsesStream(context.Background(), strings.NewReader(body), func(string) {}, func(responsesOutputItem) {})
	if err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("err = %v, want 含余额不足", err)
	}
}

func TestScanResponsesStreamRawJSONError(t *testing.T) {
	// 流式请求直接回 JSON 错误体、无任何 data 行
	body := "{\"error\":{\"message\":\"鉴权失败\"}}"
	err := scanResponsesStream(context.Background(), strings.NewReader(body), func(string) {}, func(responsesOutputItem) {})
	if err == nil || !strings.Contains(err.Error(), "鉴权失败") {
		t.Fatalf("err = %v, want 含鉴权失败", err)
	}
}

// responsesTestRequest 假上游收到的 /responses 请求体（宽松解码）。
type responsesTestRequest struct {
	Model        string           `json:"model"`
	Instructions string           `json:"instructions"`
	Input        []map[string]any `json:"input"`
	Tools        []map[string]any `json:"tools"`
	ToolChoice   *string          `json:"tool_choice"`
	Reasoning    *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

func setDeepseekBaseURL(t *testing.T, url string) {
	t.Helper()
	orig := deepseekBaseURL
	deepseekBaseURL = url
	t.Cleanup(func() { deepseekBaseURL = orig })
}

// TestStreamResponsesToolLoop 工具回路端到端：首轮发起 web_search → 本地执行（校验
// 鉴权/搜索词）→ 次轮回传 function_call + function_call_output 并产出正文。
func TestStreamResponsesToolLoop(t *testing.T) {
	cfg := &config.Config{Deepseek: config.DeepseekConfig{APIKey: "sk-test"}}
	round := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/search":
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("search 鉴权头 = %q", r.Header.Get("Authorization"))
			}
			var body struct {
				Query       string `json:"query"`
				Count       int    `json:"count"`
				OriQuestion string `json:"ori_question"`
				NeedContent bool   `json:"need_content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Query != "voxbox 最新版本" {
				t.Errorf("search query = %q", body.Query)
			}
			if body.NeedContent {
				t.Errorf("need_content 应恒为 false")
			}
			if body.OriQuestion != "voxbox 更新了什么" {
				t.Errorf("ori_question = %q, want 最近一条用户消息", body.OriQuestion)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"title": "voxbox 发布新版", "url": "https://example.com/release",
				"snippet": "s", "summary": "voxbox V4 发布了",
				"site_name": "Example", "date": "2026-10-01T00:00:00+00:00",
				"authority": 3, "rerank_score": 2.4,
			}}})
		case "/responses":
			round++
			var req responsesTestRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if round == 1 {
				if len(req.Tools) != 2 {
					t.Errorf("首轮应携带 2 个联网工具, got %d", len(req.Tools))
				}
				if !strings.Contains(req.Instructions, "web_search") {
					t.Errorf("instructions 应含联网工具指引: %q", req.Instructions)
				}
				if req.Reasoning == nil || req.Reasoning.Effort != "low" {
					t.Errorf("flash 应携带 effort=low, got %+v", req.Reasoning)
				}
				writeSSE(t, w,
					map[string]any{"type": "response.reasoning_text.delta", "delta": "思考"},
					map[string]any{"type": "response.output_item.done", "item": map[string]any{
						"type": "function_call", "call_id": "fc1", "name": "web_search", "status": "completed",
						"arguments": `{"query":"voxbox 最新版本","count":3}`,
					}},
					map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
				)
				return
			}
			// 次轮：input 应含回传的调用项与工具结果
			var hasCall, hasOutput bool
			for _, item := range req.Input {
				if item["type"] == "function_call" && item["call_id"] == "fc1" {
					hasCall = true
				}
				if item["type"] == "function_call_output" && item["call_id"] == "fc1" &&
					strings.Contains(fmt.Sprint(item["output"]), "voxbox V4 发布了") {
					hasOutput = true
				}
			}
			if !hasCall || !hasOutput {
				t.Errorf("次轮 input 缺回传项: hasCall=%v hasOutput=%v input=%+v", hasCall, hasOutput, req.Input)
			}
			if req.ToolChoice != nil {
				t.Errorf("续轮不应强制 tool_choice")
			}
			writeSSE(t, w,
				map[string]any{"type": "response.output_text.delta", "delta": "答案"},
				map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
			)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	setDeepseekBaseURL(t, ts.URL)

	var deltas []string
	var statuses []string
	msgs := []Message{{Role: "user", Content: "voxbox 更新了什么"}}
	err := StreamChatWeb(context.Background(), cfg, ProviderDeepseek, "deepseek-flash", "你是助手", msgs,
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
		t.Fatalf("responses 轮数 = %d, want 2", round)
	}
}

// TestStreamResponsesForceTextAfterMaxRounds 轮数上限：连续 maxWebRounds 轮工具调用后，
// 收尾请求不再携带工具且 tool_choice=none，强制文本作答。
func TestStreamResponsesForceTextAfterMaxRounds(t *testing.T) {
	cfg := &config.Config{Deepseek: config.DeepseekConfig{APIKey: "sk-test"}}
	round := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req responsesTestRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/responses" {
			// 工具执行流量（/v1/tools/search）：回空结果即可
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		round++
		if round <= maxWebRounds {
			if len(req.Tools) != 2 {
				t.Errorf("第 %d 轮应携带工具", round)
			}
			writeSSE(t, w,
				map[string]any{"type": "response.output_item.done", "item": map[string]any{
					"type": "function_call", "call_id": fmt.Sprintf("fc%d", round), "name": "web_search", "status": "completed",
					"arguments": `{"query":"循环"}`,
				}},
				map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
			)
			return
		}
		if req.Tools != nil {
			t.Errorf("收尾轮不应携带 tools")
		}
		if req.ToolChoice == nil || *req.ToolChoice != "none" {
			t.Errorf("收尾轮应 tool_choice=none, got %+v", req.ToolChoice)
		}
		writeSSE(t, w,
			map[string]any{"type": "response.output_text.delta", "delta": "收尾"},
			map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
		)
	}))
	defer ts.Close()
	setDeepseekBaseURL(t, ts.URL)

	var deltas []string
	err := StreamChatWeb(context.Background(), cfg, ProviderDeepseek, "deepseek-v4-pro", "s",
		[]Message{{Role: "user", Content: "q"}}, func(d string) { deltas = append(deltas, d) }, nil)
	if err != nil {
		t.Fatalf("StreamChatWeb: %v", err)
	}
	if strings.Join(deltas, "") != "收尾" {
		t.Fatalf("deltas = %q, want 收尾", deltas)
	}
	if round != maxWebRounds+1 {
		t.Fatalf("responses 轮数 = %d, want %d", round, maxWebRounds+1)
	}
}

// TestStreamComposeDeepSeekPlain 不挂工具的纯净 /responses 调用（润色/翻译/写作等
// StreamCompose 链路的 DeepSeek 形态）：不带 tools/tool_choice、instructions 原样。
func TestStreamComposeDeepSeekPlain(t *testing.T) {
	cfg := &config.Config{Deepseek: config.DeepseekConfig{APIKey: "sk-test"}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req responsesTestRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Tools != nil || req.ToolChoice != nil {
			t.Errorf("纯净调用不应携带 tools/tool_choice: %+v %+v", req.Tools, req.ToolChoice)
		}
		if req.Instructions != "自定义系统提示" {
			t.Errorf("instructions = %q, want 原样", req.Instructions)
		}
		writeSSE(t, w,
			map[string]any{"type": "response.output_text.delta", "delta": "好"},
			map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
		)
	}))
	defer ts.Close()
	setDeepseekBaseURL(t, ts.URL)

	var got []string
	err := StreamCompose(context.Background(), cfg, ProviderDeepseek, "deepseek-flash", "自定义系统提示",
		[]Message{{Role: "user", Content: "hi"}}, func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatalf("StreamCompose: %v", err)
	}
	if strings.Join(got, "") != "好" {
		t.Fatalf("deltas = %q, want 好", got)
	}
}

func TestExecuteWebToolFailures(t *testing.T) {
	cfg := &config.Config{Deepseek: config.DeepseekConfig{APIKey: "sk-test"}}
	for _, tc := range []struct{ name, tool, args, want string }{
		{"未知工具", "apply_patch", "{}", "未知工具"},
		{"参数非 JSON", "web_search", "not-json", "参数"},
		{"搜索词为空", "web_search", `{"query":"  "}`, "参数"},
		{"抓取地址非 http", "web_fetch", `{"url":"ftp://x"}`, "抓取失败"},
	} {
		got := executeWebTool(context.Background(), cfg, tc.tool, tc.args, "", nil)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want 含 %q", tc.name, got, tc.want)
		}
	}
}

func TestLastUserQuestion(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "第一问"},
		{Role: "assistant", Content: "答"},
		{Role: "user", Content: "最新一问"},
	}
	if got := lastUserQuestion(msgs); got != "最新一问" {
		t.Fatalf("got %q", got)
	}
	if got := lastUserQuestion(nil); got != "" {
		t.Fatalf("空消息应返回空, got %q", got)
	}
}
