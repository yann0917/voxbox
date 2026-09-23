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

// newMTMockServer 机器翻译 mock：按 handler 决策响应，记录每次请求的路径/头/body 供断言。
// 复用 ttsLongMockServer 的 mockReq（同构）。
func newMTMockServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any, n int)) *ttsLongMockServer {
	m := &ttsLongMockServer{t: t}
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
		w.Header().Set("X-Tt-Logid", "logid-mt")
		if handler != nil {
			handler(w, r, body, n)
			return
		}
		writeJSON(w, map[string]any{"code": 20000000, "message": "ok"})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func TestMTTranslateSuccess(t *testing.T) {
	m := newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		writeJSON(w, map[string]any{
			"code": 20000000, "message": "ok",
			"data": map[string]any{
				"translation_list": []map[string]any{
					{
						"translation":              "字节跳动正在打造一个全球性的知识与娱乐平台。",
						"detected_source_language": "en",
						"usage": map[string]any{
							"prompt_tokens": 30, "completion_tokens": 20, "total_tokens": 50,
						},
					},
					{
						"translation": "第二句译文",
						"usage":       map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
					},
				},
			},
		})
	})

	c := NewMTClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	out, err := c.Translate(context.Background(), MTTranslateReq{
		TargetLanguage: "zh",
		TextList:       []string{"ByteDance is building a global platform", "second line"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := m.request(0)
	if req.path != "/api/v3/machine_translation/matx_translate" {
		t.Errorf("path = %q", req.path)
	}
	// 老版控制台鉴权 + 固定资源 ID
	if req.headers["X-Api-App-Key"] != "app-1" || req.headers["X-Api-Access-Key"] != "tok-1" {
		t.Errorf("auth headers = %v", req.headers)
	}
	if req.headers["X-Api-Resource-Id"] != "volc.speech.mt" {
		t.Errorf("X-Api-Resource-Id = %q", req.headers["X-Api-Resource-Id"])
	}
	if req.headers["X-Api-Request-Id"] == "" {
		t.Errorf("X-Api-Request-Id 缺失")
	}
	// body：source_language 未指定不下发，corpus 未配置不下发
	if _, ok := req.body["source_language"]; ok {
		t.Errorf("source_language 不应下发: %v", req.body)
	}
	if _, ok := req.body["corpus"]; ok {
		t.Errorf("corpus 不应下发: %v", req.body)
	}
	if req.body["target_language"] != "zh" {
		t.Errorf("target_language = %v", req.body["target_language"])
	}
	list, _ := req.body["text_list"].([]any)
	if len(list) != 2 || list[0] != "ByteDance is building a global platform" {
		t.Errorf("text_list = %v", req.body["text_list"])
	}

	// 译文与请求一一对应；usage/detected_source_language 归位
	if len(out) != 2 {
		t.Fatalf("len(out) = %d", len(out))
	}
	if out[0].Translation != "字节跳动正在打造一个全球性的知识与娱乐平台。" || out[0].DetectedSourceLanguage != "en" {
		t.Errorf("out[0] = %+v", out[0])
	}
	if out[0].Usage.PromptTokens != 30 || out[0].Usage.CompletionTokens != 20 || out[0].Usage.TotalTokens != 50 {
		t.Errorf("usage[0] = %+v", out[0].Usage)
	}
	if out[1].Translation != "第二句译文" || out[1].DetectedSourceLanguage != "" || out[1].Usage.TotalTokens != 15 {
		t.Errorf("out[1] = %+v", out[1])
	}
}

func TestMTTranslateNewAuthAndGlossary(t *testing.T) {
	m := newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		writeJSON(w, map[string]any{
			"code": 20000000, "message": "ok",
			"data": map[string]any{"translation_list": []map[string]any{
				{"translation": "火山引擎提供全套云服务", "usage": map[string]any{"total_tokens": 21}},
			}},
		})
	})

	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{
		SourceLanguage: "en", TargetLanguage: "zh",
		TextList:          []string{"Volcengine provides cloud services"},
		GlossaryList:      map[string]string{"Volcengine": "火山引擎"},
		GlossaryTableID:   "tbl-1",
		GlossaryTableName: "产品术语",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := m.request(0)
	if req.headers["X-Api-Key"] != "key-1" {
		t.Errorf("X-Api-Key = %q", req.headers["X-Api-Key"])
	}
	if v := req.headers["X-Api-App-Key"]; v != "" {
		t.Errorf("新版鉴权不应携带 X-Api-App-Key: %q", v)
	}
	if req.body["source_language"] != "en" {
		t.Errorf("source_language = %v", req.body["source_language"])
	}
	corpus, ok := req.body["corpus"].(map[string]any)
	if !ok {
		t.Fatalf("corpus 缺失: %v", req.body)
	}
	glossary, _ := corpus["glossary_list"].(map[string]any)
	if glossary["Volcengine"] != "火山引擎" {
		t.Errorf("glossary_list = %v", corpus["glossary_list"])
	}
	if corpus["glossary_table_id"] != "tbl-1" || corpus["glossary_table_name"] != "产品术语" {
		t.Errorf("corpus = %v", corpus)
	}
}

func TestMTTranslateErrorCodes(t *testing.T) {
	cases := []struct {
		code    int
		message string
		want    []string // 错误消息必须包含的片段
	}{
		{45000001, "target_language is required", []string{"45000001", "target_language is required"}},
		{45000130, "overflow text_list(17>16)", []string{"45000130", "1024 Tokens", "17>16"}},
		{55000001, "pipeline failed: x", []string{"55000001", "请重试", "pipeline failed: x"}},
		{59999999, "unknown", []string{"59999999", "unknown"}},
	}
	for _, tc := range cases {
		m := newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": tc.code, "message": tc.message})
		})
		c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
		_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
		if err == nil {
			t.Fatalf("code %d: 应返回错误", tc.code)
		}
		for _, frag := range tc.want {
			if !strings.Contains(err.Error(), frag) {
				t.Errorf("code %d: err = %q, 应含 %q", tc.code, err.Error(), frag)
			}
		}
	}
}

func TestMTTranslateHTTPAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "bad"}, srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, 应包装 ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, 应含 HTTP 401", err)
	}
}

func TestMTTranslateHTTPOtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	t.Cleanup(srv.Close)
	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("err = %v, 应含 HTTP 502 与响应体摘录", err)
	}
}

// 实测上游在资源未开通时返回 HTTP 500 + 结构化包络（code 55000000 / requested resource not granted）：
// 必须解析出业务码并识别为未开通，而不是退化成 HTTP 500 泛化错误。
func TestMTTranslateResourceNotGranted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tt-Logid", "logid-grant")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":55000000,"message":"[resource_id=volc.speech.mt] requested resource not granted"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !errors.Is(err, ErrNotGranted) {
		t.Errorf("err = %v, 应包装 ErrNotGranted", err)
	}
	for _, frag := range []string{"volc.speech.mt", "55000000", "开通", "logid-grant"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("err = %q, 应含 %q", err.Error(), frag)
		}
	}
	// 不可重试语义：不能退化成泛化的 HTTP 500 错误
	if strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("err = %q, 不应退化为 HTTP 状态码错误", err.Error())
	}
}

func TestMTIsNotGrantedVariants(t *testing.T) {
	yes := []string{
		"[resource_id=volc.speech.mt] requested resource not granted",
		"resource not granted",
		"Resource Not Granted",
		"the resource is not authorized for this app",
		"该资源未开通",
	}
	for _, m := range yes {
		if !mtIsNotGranted(m) {
			t.Errorf("mtIsNotGranted(%q) = false, want true", m)
		}
	}
	no := []string{"pipeline failed: timeout", "overflow text_list(17>16)", ""}
	for _, m := range no {
		if mtIsNotGranted(m) {
			t.Errorf("mtIsNotGranted(%q) = true, want false", m)
		}
	}
}

// 服务内部错误（55000001）仍应是可重试的任务失败，不能被误判为未开通。
func TestMTTranslateServerErrorNotGrantedMismatch(t *testing.T) {
	m := newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		writeJSON(w, map[string]any{"code": 55000001, "message": "pipeline failed: transient"})
	})
	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
	if err == nil || errors.Is(err, ErrNotGranted) {
		t.Fatalf("err = %v, 应保留为可重试的服务内部错误", err)
	}
	if !strings.Contains(err.Error(), "请重试") {
		t.Errorf("err = %q, 应提示重试", err.Error())
	}
}

func TestMTTranslateEmptyResult(t *testing.T) {
	m := newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		writeJSON(w, map[string]any{"code": 20000000, "message": "ok", "data": map[string]any{"translation_list": []any{}}})
	})
	c := NewMTClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, err := c.Translate(context.Background(), MTTranslateReq{TargetLanguage: "en", TextList: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "未返回翻译结果") {
		t.Fatalf("err = %v, 应报未返回翻译结果", err)
	}
}
