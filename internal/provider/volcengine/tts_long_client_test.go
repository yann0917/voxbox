package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ttsLongMockServer 长文本合成 mock：submit/query 走 handler 决策，其余路径（audio_url 下载）
// 返回 raw 音频字节。记录每次请求的路径/头/body 供断言（并发安全）。
type ttsLongMockServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []mockReq
	submit func(w http.ResponseWriter, r *http.Request, body map[string]any, n int)
	query  func(w http.ResponseWriter, r *http.Request, body map[string]any, n int)
}

type mockReq struct {
	path    string
	headers map[string]string
	body    map[string]any
}

func newTTSLongMockServer(t *testing.T, submit, query func(w http.ResponseWriter, r *http.Request, body map[string]any, n int)) *ttsLongMockServer {
	m := &ttsLongMockServer{t: t, submit: submit, query: query}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		headers := map[string]string{}
		for _, k := range []string{"X-Api-Key", "X-Api-App-Key", "X-Api-Access-Key", "X-Api-Resource-Id", "X-Api-Request-Id", "X-Tt-Logid"} {
			headers[k] = r.Header.Get(k)
		}
		m.mu.Lock()
		n := len(m.reqs)
		m.reqs = append(m.reqs, mockReq{path: r.URL.Path, headers: headers, body: body})
		m.mu.Unlock()
		w.Header().Set("X-Tt-Logid", "logid-123")
		if r.URL.Path == ttsLongSubmitPath && m.submit != nil {
			m.submit(w, r, body, n)
			return
		}
		if r.URL.Path == ttsLongQueryPath && m.query != nil {
			m.query(w, r, body, n)
			return
		}
		// 其余路径视为音频下载
		_, _ = w.Write([]byte("FAKE_MP3_BYTES"))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *ttsLongMockServer) request(n int) mockReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n >= len(m.reqs) {
		m.t.Fatalf("请求序号 %d 超出实际请求数 %d", n, len(m.reqs))
	}
	return m.reqs[n]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestTTSLongSubmitSuccess(t *testing.T) {
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{
				"code": 20000000,
				"data": map[string]any{"task_id": body["user"].(map[string]any)["unique_id"], "req_text_length": 6},
			})
		}, nil)

	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	taskID, err := c.Submit(context.Background(), TTSLongSubmitReq{
		Text: "你好世界", Speaker: "zh_female_vv_uranus_bigtts", Resource: ttsLongResourceSeed,
		Format: "mp3", SampleRate: 24000, SpeechRate: 20, LoudnessRate: -10,
		EnableTimestamp: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := m.request(0)
	if req.path != "/api/v3/tts/submit" {
		t.Errorf("path = %q", req.path)
	}
	if req.headers["X-Api-Key"] != "key-1" {
		t.Errorf("X-Api-Key = %q", req.headers["X-Api-Key"])
	}
	if req.headers["X-Api-Resource-Id"] != "seed-tts-2.0" {
		t.Errorf("X-Api-Resource-Id = %q", req.headers["X-Api-Resource-Id"])
	}
	if taskID == "" || taskID != req.headers["X-Api-Request-Id"] {
		t.Errorf("taskID = %q, 应等于 unique_id(%q)", taskID, req.headers["X-Api-Request-Id"])
	}

	// body 结构：user.unique_id + req_params{speaker,text,audio_params}
	user := req.body["user"].(map[string]any)
	if user["uid"] != "voxbox" || user["unique_id"] == "" {
		t.Errorf("user = %v", user)
	}
	params := req.body["req_params"].(map[string]any)
	if params["speaker"] != "zh_female_vv_uranus_bigtts" || params["text"] != "你好世界" {
		t.Errorf("req_params = %v", params)
	}
	audio := params["audio_params"].(map[string]any)
	if audio["format"] != "mp3" || audio["sample_rate"] != float64(24000) ||
		audio["speech_rate"] != float64(20) || audio["loudness_rate"] != float64(-10) ||
		audio["enable_timestamp"] != true {
		t.Errorf("audio_params = %v", audio)
	}
	// 未指定的可选段不应出现：model/post_process/explicit_language
	if _, ok := params["model"]; ok {
		t.Errorf("model 不应下发: %v", params)
	}
	if _, ok := params["post_process"]; ok {
		t.Errorf("post_process 不应下发: %v", params)
	}
	if _, ok := params["explicit_language"]; ok {
		t.Errorf("explicit_language 不应下发: %v", params)
	}
}

func TestTTSLongSubmitLegacyAuthAndOptionals(t *testing.T) {
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "srv-task-1"}})
		}, nil)

	c := NewTTSLongClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	taskID, err := c.Submit(context.Background(), TTSLongSubmitReq{
		Text: "复刻", Speaker: "icl-voice", Resource: ttsLongResourceICL,
		Model: "seed-icl-2.0-x", Format: "ogg_opus", SampleRate: 48000,
		ExplicitLanguage: "zh-cn", Pitch: 5, AIGCWatermark: true, BitRate: 160000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "srv-task-1" {
		t.Errorf("taskID = %q, want 服务端返回值", taskID)
	}
	req := m.request(0)
	if req.headers["X-Api-App-Key"] != "app-1" || req.headers["X-Api-Access-Key"] != "tok-1" {
		t.Errorf("老版鉴权头 = %v", req.headers)
	}
	if req.headers["X-Api-Resource-Id"] != "seed-icl-2.0" {
		t.Errorf("X-Api-Resource-Id = %q", req.headers["X-Api-Resource-Id"])
	}
	params := req.body["req_params"].(map[string]any)
	if params["model"] != "seed-icl-2.0-x" || params["explicit_language"] != "zh-cn" {
		t.Errorf("req_params = %v", params)
	}
	pp := params["post_process"].(map[string]any)
	if pp["pitch"] != float64(5) {
		t.Errorf("post_process = %v", pp)
	}
	audio := params["audio_params"].(map[string]any)
	if audio["bit_rate"] != float64(160000) {
		t.Errorf("audio_params = %v", audio)
	}
}

func TestTTSLongSubmitErrorWithLogid(t *testing.T) {
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 45000101, "message": "invalid api key"})
		}, nil)

	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "bad"}, m.srv.URL)
	_, err := c.Submit(context.Background(), TTSLongSubmitReq{Text: "x", Resource: ttsLongResourceSeed})
	if err == nil || !strings.Contains(err.Error(), "45000101") || !strings.Contains(err.Error(), "logid-123") {
		t.Fatalf("err = %v, 应含状态码与 logid", err)
	}
}

func TestTTSLongQueryStates(t *testing.T) {
	calls := 0
	m := newTTSLongMockServer(t, nil,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			calls++
			if calls <= 2 { // 前两次 Running，第三次 Success
				writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t1", "task_status": 1}})
				return
			}
			writeJSON(w, map[string]any{
				"code": 20000000,
				"data": map[string]any{
					"task_id": "t1", "task_status": 2,
					"audio_url":       "http://mock/audio/abc.mp3",
					"req_text_length": 100, "synthesize_text_length": 98,
					"url_expire_time": 1777777777,
					"sentences": []map[string]any{
						{"text": "第一句。", "startTime": 0.0, "endTime": 1.2345},
						{"text": "第二句。", "startTime": 1.5, "endTime": 2.5},
					},
				},
			})
		})

	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)

	// Running 态
	res, err := c.Query(context.Background(), "t1", ttsLongResourceSeed)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "Running" {
		t.Errorf("status = %q", res.Status)
	}
	// 查询请求体与资源头
	req := m.request(0)
	if req.body["task_id"] != "t1" {
		t.Errorf("body = %v", req.body)
	}

	// Success 态：audio_url + 毫秒归一分句
	for {
		res, err = c.Query(context.Background(), "t1", ttsLongResourceSeed)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status == "Success" {
			break
		}
	}
	if res.AudioURL == "" || res.URLExpireTime != 1777777777 {
		t.Errorf("result = %+v", res)
	}
	if res.ReqTextLength != 100 || res.SynthesizeTextLength != 98 {
		t.Errorf("text lengths = %d/%d", res.ReqTextLength, res.SynthesizeTextLength)
	}
	if len(res.Sentences) != 2 || res.Sentences[0].StartMS != 0 || res.Sentences[0].EndMS != 1235 ||
		res.Sentences[1].StartMS != 1500 {
		t.Errorf("sentences = %+v", res.Sentences)
	}
}

func TestTTSLongQueryFailure(t *testing.T) {
	m := newTTSLongMockServer(t, nil,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t1", "task_status": 3, "message": "text invalid"}})
		})
	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	res, err := c.Query(context.Background(), "t1", ttsLongResourceSeed)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "Failure" || res.Message != "text invalid" {
		t.Errorf("result = %+v", res)
	}
}

func TestTTSLongQueryWrongResource(t *testing.T) {
	m := newTTSLongMockServer(t, nil, nil)
	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, _ = c.Query(context.Background(), "t1", ttsLongResourceICL)
	if req := m.request(0); req.headers["X-Api-Resource-Id"] != "seed-icl-2.0" {
		t.Errorf("查询必须回传与提交一致的资源 ID: %v", req.headers)
	}
}

func TestTTSLongDownload(t *testing.T) {
	m := newTTSLongMockServer(t, nil, nil)
	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	audio, err := c.Download(context.Background(), m.srv.URL+"/audio/abc.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "FAKE_MP3_BYTES" {
		t.Errorf("audio = %q", audio)
	}
}

func TestTTSLongDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewTTSLongClientWithBaseURL(SpeechCred{APIKey: "key-1"}, srv.URL)
	_, err := c.Download(context.Background(), srv.URL+"/expired.mp3")
	if err == nil || !errors.Is(err, err) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, 应含 HTTP 403", err)
	}
}
