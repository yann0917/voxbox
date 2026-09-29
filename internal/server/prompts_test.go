package server

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestPromptsFlow 列表合并内置 → 创建/改/删 → 设置默认模型回读。
func TestPromptsFlow(t *testing.T) {
	ts, _, ac := newTestServer(t)

	// 列表：内置条目在前
	list := getEnvelope(t, ac, ts.URL+"/api/prompts")
	items, _ := list.Data.(map[string]any)["items"].([]any)
	if len(items) < 12 {
		t.Fatalf("内置条目应至少 12 条, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["source"] != "builtin" || first["key"] != "idiom-story" {
		t.Fatalf("list[0] = %v, want builtin idiim-story", first)
	}

	// 创建（服务端校验：缺名称拒绝）
	status, e := doJSON(t, ac, http.MethodPost, ts.URL+"/api/prompts",
		`{"category":"文案","content":"正文"}`)
	if status != 200 || e.Code != CodeBadRequest {
		t.Fatalf("缺名称应 400: code=%d (%s) status=%d", e.Code, e.Message, status)
	}
	status, e = doJSON(t, ac, http.MethodPost, ts.URL+"/api/prompts",
		`{"name":"产品介绍","category":"文案","content":"介绍这款产品"}`)
	if status != 200 || e.Code != CodeOK {
		t.Fatalf("创建 code = %d (%s) status=%d", e.Code, e.Message, status)
	}
	created, _ := e.Data.(map[string]any)
	id, _ := created["id"].(float64)
	if id == 0 {
		t.Fatalf("创建返回缺 id: %v", e.Data)
	}

	// 更新
	status, e = doJSON(t, ac, http.MethodPut, ts.URL+"/api/prompts/"+strconv.Itoa(int(id)),
		`{"name":"改名","category":"故事","content":"新正文","kind":"polish"}`)
	if status != 200 || e.Code != CodeOK {
		t.Fatalf("更新 code = %d (%s)", e.Code, e.Message)
	}
	// 删除
	status, e = doJSON(t, ac, http.MethodDelete, ts.URL+"/api/prompts/"+strconv.Itoa(int(id)), "")
	if status != 200 || e.Code != CodeOK {
		t.Fatalf("删除 code = %d (%s)", e.Code, e.Message)
	}
	// 再删 → 404 语义
	status, e = doJSON(t, ac, http.MethodDelete, ts.URL+"/api/prompts/"+strconv.Itoa(int(id)), "")
	if status != 200 || e.Code != CodeNotFound {
		t.Fatalf("重复删除应 NotFound: code=%d", e.Code)
	}
}

// TestApplyPromptProtocol apply 的 SSE 协议：无凭证时流内 error 事件（业务码 4）、
// 参数错误走 JSON 包络。
func TestApplyPromptProtocol(t *testing.T) {
	ts, _, ac := newTestServer(t)

	// 参数错误：JSON 包络
	status, e := doJSON(t, ac, http.MethodPost, ts.URL+"/api/prompts/apply", `{}`)
	if status != 200 || e.Code != CodeBadRequest {
		t.Fatalf("空请求应包络 400: code=%d (%s) status=%d", e.Code, e.Message, status)
	}

	// 无凭证：SSE 已开（200 + text/event-stream），流内下发 error 且 code=4
	resp, err := ac.Post(ts.URL+"/api/prompts/apply", "application/json",
		strings.NewReader(`{"builtin":"idiom-story"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %s, want text/event-stream", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, `"code":4`) {
		t.Fatalf("流内应下发业务码 4 的 error 事件: %s", body)
	}
	if strings.Contains(body, `"done"`) {
		t.Fatal("出错后不应有 done 事件")
	}
}

// TestAssistantDefaultSetting 默认大模型：保存（admin）、非法值拒绝、GET 回读。
func TestAssistantDefaultSetting(t *testing.T) {
	ts, _, ac := newTestServer(t)

	status, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/assistant",
		`{"default_model":"zhipu:gpt-4o"}`)
	if status != 200 || e.Code != CodeBadRequest {
		t.Fatalf("目录外模型应拒绝: code=%d (%s)", e.Code, e.Message)
	}
	status, e = doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/assistant",
		`{"default_model":"qianwen:qwen3.8-max"}`)
	if status != 200 || e.Code != CodeOK {
		t.Fatalf("保存 code = %d (%s)", e.Code, e.Message)
	}
	list := getEnvelope(t, ac, ts.URL+"/api/settings")
	assistant, _ := list.Data.(map[string]any)["assistant"].(map[string]any)
	if got, _ := assistant["default_model"].(string); got != "qianwen:qwen3.8-max" {
		t.Fatalf("GET /api/settings assistant = %v", list.Data)
	}
	// 空串恢复自动
	status, e = doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/assistant", `{"default_model":""}`)
	if status != 200 || e.Code != CodeOK {
		t.Fatalf("清空 code = %d (%s)", e.Code, e.Message)
	}
}
