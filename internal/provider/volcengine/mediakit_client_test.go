package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mediaKitRequest 记录 mock 服务收到的请求信息（方法/路径/鉴权头/body），供断言。
type mediaKitRequest struct {
	method string
	path   string
	auth   string
	body   map[string]any
}

// newMediaKitMockServer 构造 MediaKit mock 服务：记录请求信息，按 status/respBody 回复
// （respBody 支持 map/slice（JSON 编码）、string/[]byte（原样写出）、nil（空 body））。
func newMediaKitMockServer(t *testing.T, status int, respBody any, got *mediaKitRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			got.method = r.Method
			got.path = r.URL.Path
			got.auth = r.Header.Get("Authorization")
			got.body = nil
			if r.Body != nil {
				var m map[string]any
				if err := json.NewDecoder(r.Body).Decode(&m); err == nil {
					got.body = m
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		switch b := respBody.(type) {
		case nil:
		case string:
			_, _ = w.Write([]byte(b))
		case []byte:
			_, _ = w.Write(b)
		default:
			_ = json.NewEncoder(w).Encode(b)
		}
	}))
}

// TestSubmitMediaKit 覆盖简报 3 个提交用例：成功（Bearer 头/body 非空字段/任务 ID）、
// HTTP 401 → ErrAuth、HTTP 200 + success=false → 中文错误含上游 code/message。
func TestSubmitMediaKit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK, map[string]any{
			"success":   true,
			"task_id":   "task-123",
			"task_type": "separate-voice",
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		taskID, err := c.Submit(context.Background(), "audio_url", "https://example.com/a.mp3", "Audio", "mp3")
		if err != nil {
			t.Fatal(err)
		}
		if taskID != "task-123" {
			t.Fatalf("taskID = %q, want task-123", taskID)
		}
		if got.method != http.MethodPost || got.path != "/api/v1/tools/separate-voice" {
			t.Errorf("request = %s %s", got.method, got.path)
		}
		if got.auth != "Bearer key-1" {
			t.Errorf("Authorization = %q, want Bearer key-1", got.auth)
		}
		if got.body["audio_url"] != "https://example.com/a.mp3" ||
			got.body["scene"] != "Audio" || got.body["output_format"] != "mp3" {
			t.Errorf("body = %v", got.body)
		}
		// 非空字段才进 body：未指定的 video_url 不应出现。
		if _, ok := got.body["video_url"]; ok {
			t.Errorf("body 不应包含 video_url: %v", got.body)
		}
	})

	t.Run("auth_error", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusUnauthorized, map[string]any{
			"success": false,
			"error":   map[string]any{"code": "Unauthorized", "message": "invalid api key"},
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("bad-key", srv.URL)
		_, err := c.Submit(context.Background(), "audio_url", "https://example.com/a.mp3", "Audio", "mp3")
		if err == nil || !errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
	})

	t.Run("api_error", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK,
			`{"success":false,"error":{"code":"InvalidParameter","message":"bad url"}}`, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		_, err := c.Submit(context.Background(), "audio_url", "https://bad.example.com/x.mp3", "Audio", "mp3")
		if err == nil {
			t.Fatal("success=false 应报错")
		}
		if !strings.Contains(err.Error(), "InvalidParameter") || !strings.Contains(err.Error(), "bad url") {
			t.Fatalf("err 应含上游 code/message: %v", err)
		}
		if errors.Is(err, ErrAuth) {
			t.Fatalf("业务错误不应包装 ErrAuth: %v", err)
		}
	})
}

// TestQueryMediaKit 覆盖简报 4 个查询用例：completed 双轨（Audio 场景）、completed 三轨（Drama 场景）、
// failed（任务终态非传输错误，errMsg 交调用方）、running（res 为 nil）。
func TestQueryMediaKit(t *testing.T) {
	t.Run("completed_audio", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK, map[string]any{
			"success": true,
			"task_id": "task-1",
			"status":  "completed",
			"result": map[string]any{
				"voice_audio_url":      "https://cdn.example.com/voice.aac",
				"background_audio_url": "https://cdn.example.com/background.aac",
				"duration":             12.5,
			},
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		status, res, errMsg, err := c.Query(context.Background(), "task-1")
		if err != nil {
			t.Fatal(err)
		}
		if status != "completed" || errMsg != "" {
			t.Fatalf("status = %q, errMsg = %q", status, errMsg)
		}
		if got.method != http.MethodGet || got.path != "/api/v1/tasks/task-1" {
			t.Errorf("request = %s %s", got.method, got.path)
		}
		if got.auth != "Bearer key-1" {
			t.Errorf("Authorization = %q, want Bearer key-1", got.auth)
		}
		// 顺序固定 voice→background，且仅收非空 URL 字段。
		if res == nil || len(res.Tracks) != 2 ||
			res.Tracks[0].Kind != "voice" || res.Tracks[0].URL != "https://cdn.example.com/voice.aac" ||
			res.Tracks[1].Kind != "background" || res.Tracks[1].URL != "https://cdn.example.com/background.aac" {
			t.Fatalf("tracks = %+v", res)
		}
		if res.DurationS != 12.5 {
			t.Errorf("DurationS = %v, want 12.5", res.DurationS)
		}
	})

	t.Run("completed_drama", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK, map[string]any{
			"success": true,
			"task_id": "task-2",
			"status":  "completed",
			"result": map[string]any{
				"voice_audio_url": "https://cdn.example.com/voice.aac",
				"music_audio_url": "https://cdn.example.com/music.aac",
				"sfx_audio_url":   "https://cdn.example.com/sfx.aac",
				"duration":        30,
			},
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		status, res, errMsg, err := c.Query(context.Background(), "task-2")
		if err != nil {
			t.Fatal(err)
		}
		if status != "completed" || errMsg != "" {
			t.Fatalf("status = %q, errMsg = %q", status, errMsg)
		}
		// Drama 场景无 background 轨：顺序 voice→music→sfx。
		if res == nil || len(res.Tracks) != 3 ||
			res.Tracks[0].Kind != "voice" || res.Tracks[1].Kind != "music" || res.Tracks[2].Kind != "sfx" {
			t.Fatalf("tracks = %+v", res)
		}
		if res.DurationS != 30 {
			t.Errorf("DurationS = %v, want 30", res.DurationS)
		}
	})

	t.Run("failed", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK, map[string]any{
			"success": true,
			"task_id": "task-3",
			"status":  "failed",
			"error": map[string]any{
				"code":    "InternalError",
				"message": "处理失败",
				"param":   "",
				"type":    "system",
			},
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		status, res, errMsg, err := c.Query(context.Background(), "task-3")
		if err != nil {
			t.Fatalf("failed 是任务终态而非传输错误, err = %v", err)
		}
		if status != "failed" {
			t.Errorf("status = %q, want failed", status)
		}
		if res != nil {
			t.Errorf("failed 不应有结果: %+v", res)
		}
		if !strings.Contains(errMsg, "InternalError") || !strings.Contains(errMsg, "处理失败") {
			t.Fatalf("errMsg 应含上游 code/message: %q", errMsg)
		}
	})

	t.Run("running", func(t *testing.T) {
		var got mediaKitRequest
		srv := newMediaKitMockServer(t, http.StatusOK, map[string]any{
			"success": true,
			"task_id": "task-4",
			"status":  "running",
		}, &got)
		defer srv.Close()

		c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
		status, res, errMsg, err := c.Query(context.Background(), "task-4")
		if err != nil {
			t.Fatal(err)
		}
		if status != "running" {
			t.Errorf("status = %q, want running", status)
		}
		if res != nil {
			t.Errorf("running 状态 res 应为 nil, got %+v", res)
		}
		if errMsg != "" {
			t.Errorf("errMsg = %q, want 空", errMsg)
		}
	})
}

// TestDownloadMediaKit 覆盖简报下载用例：200 字节流逐字节一致；404 报错。
func TestDownloadMediaKit(t *testing.T) {
	payload := []byte("fake-audio-bytes-\x00\x01\x02\xff")
	var got mediaKitRequest
	srv := newMediaKitMockServer(t, http.StatusOK, payload, &got)
	defer srv.Close()

	c := NewMediaKitClientWithBaseURL("key-1", srv.URL)
	// 产物为 24h 临时直链：绝对 URL 直连，不走 baseURL。
	data, err := c.Download(context.Background(), srv.URL+"/separate/task-1_voice.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("data = %q, want %q", data, payload)
	}
	if got.path != "/separate/task-1_voice.mp3" {
		t.Errorf("path = %q", got.path)
	}

	notFound := newMediaKitMockServer(t, http.StatusNotFound, map[string]any{"success": false}, nil)
	defer notFound.Close()
	if _, err := c.Download(context.Background(), notFound.URL+"/missing.mp3"); err == nil {
		t.Fatal("404 应报错")
	}
}
