package server

// /api/refine 加工端点测试：底层流式调用经 refineStream 测试缝替换为假实现
// （离线跑协议，不外联），预检校验链与 Summary 落盘走真实 DB（speakers_test 同款
// fixture 直落库）。SSE 协议与助手 chat 一致：预检错误 JSON 包络，流内 delta→done。

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/store"
)

// setRefineStream 替换流式底层缝并在测试结束还原；fn 收到定形的 system/user 消息，
// calls 返回调用次数供断言（未配置凭证时预检拦截，不应被调用）。
func setRefineStream(t *testing.T, fn func(system, user string, onDelta func(string)) error) *int {
	t.Helper()
	orig := refineStream
	t.Cleanup(func() { refineStream = orig })
	calls := 0
	refineStream = func(ctx context.Context, cfg *config.Config, p assistant.Provider, model, system string,
		messages []assistant.Message, onDelta func(string)) error {
		calls++
		return fn(system, messages[len(messages)-1].Content, onDelta)
	}
	return &calls
}

// refineFixture 直落库造任务：rf-asr 成功任务（segments 含编号说话人与妙记形态的
// 自带前缀 text，speaker_names 预置改名）、rf-empty 成功但无转写、rf-failed 失败、
// rf-tts 非加工工具，均归 alice-id（越权用例借 bob 客户端）。
func refineFixture(t *testing.T, s *Server) {
	t.Helper()
	if err := s.svc.DB().CreateTask(&store.Task{
		ID: "rf-asr", UserID: "alice-id", Provider: "volcengine", Tool: "asr",
		Status: store.StatusSucceeded, Params: `{}`,
		Summary: `{"segments":[{"text":"大家好","start_ms":0,"end_ms":900,"speaker":"1"},` +
			`{"text":"说话人2：好的","start_ms":5000,"end_ms":8000},` +
			`{"text":"散会","start_ms":10000,"end_ms":11000,"speaker_id":"2"}],"duration_ms":11000,` +
			`"speakers_count":2,"speaker_names":{"1":"张三"}}`,
		CreatedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, tool, summary string }{
		{"rf-empty", "asr", `{}`},
		{"rf-failed", "asr", `{"segments":[]}`},
		{"rf-tts", "tts", `{"text":"合成结果"}`},
	} {
		status := store.StatusSucceeded
		if tc.id == "rf-failed" {
			status = store.StatusFailed
		}
		if err := s.svc.DB().CreateTask(&store.Task{
			ID: tc.id, UserID: "alice-id", Provider: "volcengine", Tool: tc.tool,
			Status: status, Params: `{}`, Summary: tc.summary,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// postRefine POST /api/refine 并返回（SSE 响应体, Content-Type）。
func postRefine(t *testing.T, ac *http.Client, url, body string) (string, string) {
	t.Helper()
	resp, err := ac.Post(url+"/api/refine", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw), resp.Header.Get("Content-Type")
}

// TestRefineSummaryFlow summary 模式全链路：SSE 事件顺序 delta→done（无 error），
// system 为内置纪要提示词，user 转写行渲染生效（speaker_names 改名 + 妙记前缀原样），
// 结果落 Summary.refined.summary 且其余键保留。
func TestRefineSummaryFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, s, ac := newTestServer(t)
	refineFixture(t, s)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-refine-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}
	var gotSystem, gotUser string
	setRefineStream(t, func(system, user string, onDelta func(string)) error {
		gotSystem, gotUser = system, user
		onDelta("## 会议纪要")
		onDelta("\n\n- 张三：确认方案")
		return nil
	})

	body, ct := postRefine(t, ac, ts.URL, `{"task_id":"rf-asr","mode":"summary","provider":"qianwen","model":"qwen3.8-flash"}`)
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %s, want text/event-stream", ct)
	}
	if strings.Contains(body, `"error"`) {
		t.Fatalf("正常流不应有 error 事件: %s", body)
	}
	first, done := strings.Index(body, `"delta"`), strings.Index(body, `"done"`)
	if first < 0 || done < 0 || first > done {
		t.Fatalf("事件顺序应为 delta→done: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), `"done":true}`) {
		t.Fatalf("流应以 done 事件收尾: %s", body)
	}
	// system/user 定形检查
	if !strings.Contains(gotSystem, "纪要") {
		t.Errorf("system 应为内置纪要提示词: %s", gotSystem)
	}
	if !strings.Contains(gotUser, "00:00:00 张三：大家好") {
		t.Errorf("user 应含改名后的说话人行: %s", gotUser)
	}
	if !strings.Contains(gotUser, "00:00:05 说话人2：好的") {
		t.Errorf("妙记形态的自带前缀应原样保留: %s", gotUser)
	}
	if !strings.Contains(gotUser, "00:00:10 说话人2：散会") {
		t.Errorf("千问形态的 speaker_id 段应补说话人前缀: %s", gotUser)
	}
	// Summary 落盘 refined
	sum := taskSummary(t, ac, ts.URL, "rf-asr")
	refined, _ := sum["refined"].(map[string]any)
	if refined == nil {
		t.Fatalf("Summary 应有 refined 键: %v", sum)
	}
	if refined["summary"] != "## 会议纪要\n\n- 张三：确认方案" {
		t.Errorf("refined.summary = %v", refined["summary"])
	}
	if at, _ := refined["updated_at"].(string); at == "" {
		t.Errorf("refined.updated_at = %v", refined["updated_at"])
	}
	if sum["duration_ms"].(float64) != 11000 || sum["speaker_names"] == nil || sum["segments"] == nil {
		t.Errorf("其余 Summary 键应保留: %v", sum)
	}
}

// TestRefineTodosPersistence todos 模式：(a) 合法 JSON（容忍代码栅栏）落数组；
// (b) 非合法 JSON 原文落键 + 仍以 done 收尾（不报错给前端），同模式重跑覆盖旧值。
func TestRefineTodosPersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, s, ac := newTestServer(t)
	refineFixture(t, s)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-refine-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}

	// (a) 栅栏包裹的合法 JSON 数组
	setRefineStream(t, func(system, user string, onDelta func(string)) error {
		if !strings.Contains(system, "待办") || !strings.Contains(system, "JSON") {
			t.Errorf("todos system 提示词错误: %s", system)
		}
		onDelta("```json\n")
		onDelta(`[{"content":"交方案","owner":"张三","due":"下周三"}]`)
		onDelta("\n```")
		return nil
	})
	body, _ := postRefine(t, ac, ts.URL, `{"task_id":"rf-asr","mode":"todos","provider":"qianwen","model":"qwen3.8-flash"}`)
	if strings.Contains(body, `"error"`) || !strings.Contains(body, `"done":true}`) {
		t.Fatalf("合法 JSON 流应 delta→done: %s", body)
	}
	sum := taskSummary(t, ac, ts.URL, "rf-asr")
	refined, _ := sum["refined"].(map[string]any)
	todos, ok := refined["todos"].([]any)
	if !ok || len(todos) != 1 {
		t.Fatalf("refined.todos 应为数组: %v", refined)
	}
	td, _ := todos[0].(map[string]any)
	if td["content"] != "交方案" || td["owner"] != "张三" {
		t.Errorf("todos[0] = %v", td)
	}

	// (b) 非法 JSON：原文兜底落键，流照常 done
	setRefineStream(t, func(system, user string, onDelta func(string)) error {
		onDelta("这条转写没有待办。")
		return nil
	})
	body, _ = postRefine(t, ac, ts.URL, `{"task_id":"rf-asr","mode":"todos","provider":"qianwen","model":"qwen3.8-flash"}`)
	if strings.Contains(body, `"error"`) || !strings.Contains(body, `"done":true}`) {
		t.Fatalf("非法 JSON 不应报错给前端: %s", body)
	}
	sum = taskSummary(t, ac, ts.URL, "rf-asr")
	refined, _ = sum["refined"].(map[string]any)
	if refined["todos"] != "这条转写没有待办。" {
		t.Errorf("非法 JSON 应原文落键: %v", refined["todos"])
	}
	if at, _ := refined["updated_at"].(string); at == "" {
		t.Errorf("覆盖后 updated_at 应刷新: %v", refined)
	}
}

// TestRefineEventsRecordDate events 模式：录音日期（任务 CreatedAt 的日期）附在
// user 消息转写头部，结果落 refined.events 数组。
func TestRefineEventsRecordDate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, s, ac := newTestServer(t)
	refineFixture(t, s)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-refine-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}
	var gotUser string
	setRefineStream(t, func(system, user string, onDelta func(string)) error {
		gotUser = user
		onDelta(`[{"title":"方案评审","start":"2026-09-30 14:00","end":"","description":"下周三评审"}]`)
		return nil
	})
	body, _ := postRefine(t, ac, ts.URL, `{"task_id":"rf-asr","mode":"events","provider":"qianwen","model":"qwen3.8-flash"}`)
	if strings.Contains(body, `"error"`) || !strings.Contains(body, `"done":true}`) {
		t.Fatalf("events 流应 delta→done: %s", body)
	}
	if !strings.Contains(gotUser, "录音日期：2026-09-28") {
		t.Errorf("user 头部应附录音日期: %s", gotUser)
	}
	if !strings.Contains(gotUser, "00:00:00 张三：大家好") {
		t.Errorf("user 应含转写行: %s", gotUser)
	}
	sum := taskSummary(t, ac, ts.URL, "rf-asr")
	events, ok := sum["refined"].(map[string]any)["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("refined.events 应为数组: %v", sum["refined"])
	}
}

// TestRefineStreamError 流内错误：下发 error 事件（业务码 3）、无 done、不落盘。
func TestRefineStreamError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, s, ac := newTestServer(t)
	refineFixture(t, s)
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-refine-test"}}`); e.Code != CodeOK {
		t.Fatalf("配置假凭证 code = %d (%s)", e.Code, e.Message)
	}
	setRefineStream(t, func(system, user string, onDelta func(string)) error {
		onDelta("部分输出")
		return errors.New("千问 API 错误: 余额不足")
	})
	body, _ := postRefine(t, ac, ts.URL, `{"task_id":"rf-asr","mode":"summary","provider":"qianwen","model":"qwen3.8-flash"}`)
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, `"code":3`) {
		t.Fatalf("流内错误应下发 code=3 的 error 事件: %s", body)
	}
	if strings.Contains(body, `"done"`) {
		t.Fatalf("出错后不应有 done 事件: %s", body)
	}
	sum := taskSummary(t, ac, ts.URL, "rf-asr")
	if _, exists := sum["refined"]; exists {
		t.Errorf("出错不应落盘 refined: %v", sum["refined"])
	}
}

// TestRefineValidation 预检校验链全部走 JSON 包络（不换 SSE）：模式/instruction/
// 平台/模型/任务存在/属主/工具/状态/转写非空/凭证。
func TestRefineValidation(t *testing.T) {
	ts, s, ac := newTestServer(t)
	refineFixture(t, s)
	testUser(t, s, "bob", "user")
	acBob := loginAs(t, ts, "bob", "password-bob")

	valid := `"provider":"qianwen","model":"qwen3.8-flash"`
	for _, tc := range []struct {
		name string
		ac   *http.Client
		body string
		want int
	}{
		{"未知模式", ac, `{"task_id":"rf-asr","mode":"nope",` + valid + `}`, CodeBadRequest},
		{"custom 缺 instruction", ac, `{"task_id":"rf-asr","mode":"custom",` + valid + `}`, CodeBadRequest},
		{"未知平台", ac, `{"task_id":"rf-asr","mode":"summary","provider":"foo","model":"m"}`, CodeBadRequest},
		{"白名单外模型", ac, `{"task_id":"rf-asr","mode":"summary","provider":"qianwen","model":"gpt-4o"}`, CodeBadRequest},
		{"任务不存在", ac, `{"task_id":"no-task","mode":"summary",` + valid + `}`, CodeNotFound},
		{"越权同报不存在", acBob, `{"task_id":"rf-asr","mode":"summary",` + valid + `}`, CodeNotFound},
		{"非加工工具", ac, `{"task_id":"rf-tts","mode":"summary",` + valid + `}`, CodeBadRequest},
		{"任务未成功", ac, `{"task_id":"rf-failed","mode":"summary",` + valid + `}`, CodeBadRequest},
		{"无转写内容", ac, `{"task_id":"rf-empty","mode":"summary",` + valid + `}`, CodeBadRequest},
		{"凭证未配置", ac, `{"task_id":"rf-asr","mode":"summary",` + valid + `}`, CodeBadCredential},
	} {
		_, e := doJSON(t, tc.ac, http.MethodPost, ts.URL+"/api/refine", tc.body)
		if e.Code != tc.want {
			t.Errorf("%s: code = %d (%s), want %d", tc.name, e.Code, e.Message, tc.want)
		}
	}
}
