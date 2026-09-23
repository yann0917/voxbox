package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// aucRequest 记录 mock 服务收到的请求信息（路径/头/body），供断言。
type aucRequest struct {
	path    string
	headers map[string]string
	body    map[string]any
}

// newAUCMockServer 构造异步 ASR mock 服务：记录请求头与 body，按 respHeader/respBody 回复
// （respBody 为 nil 时返回空 body，对应 submit 的成功响应）。
func newAUCMockServer(t *testing.T, respHeader map[string]string, respBody any, got *aucRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.headers = map[string]string{
			"X-Api-Key":         r.Header.Get("X-Api-Key"),
			"X-Api-App-Key":     r.Header.Get("X-Api-App-Key"),
			"X-Api-Access-Key":  r.Header.Get("X-Api-Access-Key"),
			"X-Api-Resource-Id": r.Header.Get("X-Api-Resource-Id"),
			"X-Api-Request-Id":  r.Header.Get("X-Api-Request-Id"),
			"X-Api-Sequence":    r.Header.Get("X-Api-Sequence"),
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got.body = m
		for k, v := range respHeader {
			w.Header().Set(k, v)
		}
		w.WriteHeader(http.StatusOK)
		if respBody == nil {
			return
		}
		if s, ok := respBody.(string); ok {
			_, _ = w.Write([]byte(s))
			return
		}
		_ = json.NewEncoder(w).Encode(respBody)
	}))
}

func TestSubmitSuccess(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, nil, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{AppID: "app", AccessToken: "tok"}, srv.URL)
	taskID, err := c.Submit(context.Background(), "https://example.com/audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	// 官方语义：submit 的任务 ID 即客户端传入的 X-Api-Request-Id。
	if taskID == "" || taskID != got.headers["X-Api-Request-Id"] {
		t.Fatalf("taskID = %q, 请求头 X-Api-Request-Id = %q", taskID, got.headers["X-Api-Request-Id"])
	}
	if got.path != "/api/v3/auc/bigmodel/submit" {
		t.Errorf("path = %q", got.path)
	}
	if got.headers["X-Api-Resource-Id"] != "volc.seedasr.auc" {
		t.Errorf("X-Api-Resource-Id = %q", got.headers["X-Api-Resource-Id"])
	}
	if got.headers["X-Api-Sequence"] != "-1" {
		t.Errorf("X-Api-Sequence = %q", got.headers["X-Api-Sequence"])
	}
	if got.headers["X-Api-App-Key"] != "app" || got.headers["X-Api-Access-Key"] != "tok" {
		t.Errorf("老版鉴权头 = %v", got.headers)
	}
	if got.body["audio_url"] != "https://example.com/audio.mp3" {
		t.Errorf("body = %v", got.body)
	}
}

func TestSubmitAuthError(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "45000001"}, nil, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{AppID: "a", AccessToken: "bad"}, srv.URL)
	_, err := c.Submit(context.Background(), "https://example.com/audio.mp3")
	if err == nil || !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "45000001") {
		t.Errorf("err 应包含状态码 45000001: %v", err)
	}
}

func TestQueryCompleted(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, map[string]any{
		"id":     "task-1",
		"status": "Completed",
		"result": map[string]any{
			"text": "你好世界",
			"utterances": []any{
				map[string]any{"text": "你好", "start_time": 0, "end_time": 1000},
				map[string]any{"text": "世界", "start_time": 1000, "end_time": 2000},
			},
		},
		"audio_info": map[string]any{"duration": 2000},
	}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	resp, status, err := c.Query(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != "Completed" {
		t.Errorf("status = %q", status)
	}
	if resp.Text != "你好世界" || resp.DurationMS != 2000 {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Segments) != 2 ||
		resp.Segments[0].Text != "你好" || resp.Segments[0].StartMS != 0 || resp.Segments[0].EndMS != 1000 ||
		resp.Segments[1].Text != "世界" || resp.Segments[1].StartMS != 1000 || resp.Segments[1].EndMS != 2000 {
		t.Fatalf("segments = %+v", resp.Segments)
	}
	if got.path != "/api/v3/auc/bigmodel/query" {
		t.Errorf("path = %q", got.path)
	}
	if got.body["id"] != "task-1" {
		t.Errorf("body = %v", got.body)
	}
	// 新版鉴权：APIKey 非空时用 X-Api-Key，且 Query 不携带 X-Api-Sequence。
	if got.headers["X-Api-Key"] != "ak" {
		t.Errorf("X-Api-Key = %q", got.headers["X-Api-Key"])
	}
	if got.headers["X-Api-Sequence"] != "" {
		t.Errorf("Query 不应携带 X-Api-Sequence, got %q", got.headers["X-Api-Sequence"])
	}
}

func TestQueryStillRunning(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, map[string]any{
		"id":     "task-1",
		"status": "Running",
	}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	resp, status, err := c.Query(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != "Running" {
		t.Errorf("status = %q", status)
	}
	if resp.Text != "" || len(resp.Segments) != 0 {
		t.Errorf("Running 状态不应有识别结果: %+v", resp)
	}
}

// idleSubmitBody 构造闲时/极速版共用的提交请求体。
func idleSubmitBody() aucTaskRequest {
	return aucTaskRequest{
		Audio:   aucAudioMeta{URL: "https://example.com/audio.mp3", Format: "mp3", Language: "zh-CN"},
		Request: aucTaskOption{ModelName: "bigmodel", EnableITN: true, EnablePunc: true, ShowUtterances: true},
	}
}

func TestSubmitIdleSuccess(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"},
		map[string]any{"task_id": "idle-task-1"}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	taskID, err := c.SubmitIdle(context.Background(), idleSubmitBody())
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "idle-task-1" {
		t.Fatalf("taskID = %q, 期望响应体 task_id 优先", taskID)
	}
	if got.path != asrIdleSubmitPath {
		t.Errorf("path = %q", got.path)
	}
	if got.headers["X-Api-Resource-Id"] != asrIdleResourceID {
		t.Errorf("X-Api-Resource-Id = %q", got.headers["X-Api-Resource-Id"])
	}
	if got.headers["X-Api-Sequence"] != "-1" {
		t.Errorf("X-Api-Sequence = %q", got.headers["X-Api-Sequence"])
	}
	audio, _ := got.body["audio"].(map[string]any)
	if audio == nil || audio["url"] != "https://example.com/audio.mp3" || audio["format"] != "mp3" || audio["language"] != "zh-CN" {
		t.Errorf("body.audio = %v", got.body["audio"])
	}
	req, _ := got.body["request"].(map[string]any)
	if req == nil || req["model_name"] != "bigmodel" || req["show_utterances"] != true || req["enable_itn"] != true || req["enable_punc"] != true {
		t.Errorf("body.request = %v", got.body["request"])
	}
}

func TestSubmitIdleFallbackTaskID(t *testing.T) {
	// 响应体不含 task_id 时回退为客户端 X-Api-Request-Id（与标准版同语义）。
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, nil, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	taskID, err := c.SubmitIdle(context.Background(), idleSubmitBody())
	if err != nil {
		t.Fatal(err)
	}
	if taskID == "" || taskID != got.headers["X-Api-Request-Id"] {
		t.Fatalf("taskID = %q, 请求头 X-Api-Request-Id = %q", taskID, got.headers["X-Api-Request-Id"])
	}
}

func TestQueryIdleCompleted(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, map[string]any{
		"result": map[string]any{
			"text": "你好世界",
			"utterances": []any{
				map[string]any{"text": "你好", "start_time": 0, "end_time": 1000},
				map[string]any{"text": "世界", "start_time": 1000, "end_time": 2000},
			},
		},
		"audio_info": map[string]any{"duration": 2000},
	}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	resp, status, err := c.QueryIdle(context.Background(), "idle-task-9")
	if err != nil {
		t.Fatal(err)
	}
	if status != "Completed" {
		t.Errorf("status = %q", status)
	}
	if resp.Text != "你好世界" || resp.DurationMS != 2000 || len(resp.Segments) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if got.path != asrIdleQueryPath {
		t.Errorf("path = %q", got.path)
	}
	// 官方语义：查询的任务 ID 经 X-Api-Request-Id 头回传，请求体为空 JSON。
	if got.headers["X-Api-Request-Id"] != "idle-task-9" {
		t.Errorf("X-Api-Request-Id = %q, 期望回传任务 ID", got.headers["X-Api-Request-Id"])
	}
	if len(got.body) != 0 {
		t.Errorf("请求体应为空 JSON, got %v", got.body)
	}
}

func TestQueryIdleIntermediateStates(t *testing.T) {
	cases := []struct {
		name   string
		header string
		body   map[string]any
		status string
	}{
		{"2开头中间态码", "20000001", map[string]any{}, "Running"},
		{"5开头服务端码", "50000001", map[string]any{}, "Running"},
		{"无状态码", "", map[string]any{}, "Running"},
		{"体状态Queuing", "20000000", map[string]any{"status": "Queuing"}, "Queuing"},
		{"体状态Failed", "20000000", map[string]any{"status": "Failed"}, "Failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got aucRequest
			hdr := map[string]string{}
			if tc.header != "" {
				hdr["X-Api-Status-Code"] = tc.header
			}
			srv := newAUCMockServer(t, hdr, tc.body, &got)
			defer srv.Close()

			c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
			_, status, err := c.QueryIdle(context.Background(), "idle-task-9")
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if status != tc.status {
				t.Errorf("status = %q, 期望 %q", status, tc.status)
			}
		})
	}
}

func TestQueryIdleAuthError(t *testing.T) {
	// 4 开头状态码为终态错误（鉴权/参数类），立即失败不进入轮询。
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "45000001"}, map[string]any{}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{AppID: "a", AccessToken: "bad"}, srv.URL)
	_, _, err := c.QueryIdle(context.Background(), "idle-task-9")
	if err == nil || !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestQueryIdleCompletedWithoutResult(t *testing.T) {
	// 体状态 Completed 但无 result：病态响应，应报错而非空转轮询。
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"},
		map[string]any{"status": "Completed"}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	_, _, err := c.QueryIdle(context.Background(), "idle-task-9")
	if err == nil || !strings.Contains(err.Error(), "未返回识别结果") {
		t.Fatalf("err = %v, 期望包含「未返回识别结果」", err)
	}
}

func TestRecognizeFlashSuccess(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, map[string]any{
		"task_id": "flash-1",
		"result": map[string]any{
			"text": "极速结果",
			"utterances": []any{
				map[string]any{"text": "极速", "start_time": 0, "end_time": 500},
			},
		},
		"audio_info": map[string]any{"duration": 1500},
	}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	resp, err := c.RecognizeFlash(context.Background(), idleSubmitBody())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "极速结果" || resp.DurationMS != 1500 || len(resp.Segments) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if got.path != asrFlashPath {
		t.Errorf("path = %q", got.path)
	}
	if got.headers["X-Api-Resource-Id"] != asrFlashResourceID {
		t.Errorf("X-Api-Resource-Id = %q", got.headers["X-Api-Resource-Id"])
	}
	if got.headers["X-Api-Sequence"] != "-1" {
		t.Errorf("X-Api-Sequence = %q", got.headers["X-Api-Sequence"])
	}
}

func TestRecognizeFlashNoResult(t *testing.T) {
	var got aucRequest
	srv := newAUCMockServer(t, map[string]string{"X-Api-Status-Code": "20000000"}, map[string]any{}, &got)
	defer srv.Close()

	c := NewASRAUCClientWithBaseURL(SpeechCred{APIKey: "ak"}, srv.URL)
	_, err := c.RecognizeFlash(context.Background(), idleSubmitBody())
	if err == nil || !strings.Contains(err.Error(), "未返回识别结果") {
		t.Fatalf("err = %v, 期望包含「未返回识别结果」", err)
	}
}
