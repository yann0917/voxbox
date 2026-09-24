package qianwen

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TTS 全链路（真机校准后的 wire 结构）：Bearer 鉴权头、语义字段平铺在 input.*
// 下（text/voice/language_type，无 messages 包装）、响应 output.audio.url 被下载为字节。
func TestTTSClientSynthesize(t *testing.T) {
	var auth, body string
	audioBytes := []byte("fake-wav-bytes")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 音频下载请求与合成请求共用本 handler，先分流，避免覆盖合成请求的抓包值
		if r.URL.Path == "/audio.wav" {
			_, _ = w.Write(audioBytes)
			return
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"request_id":"req-1","code":"","message":"",
			"output":{"finish_reason":"stop","audio":{"url":"` + srv.URL + `/audio.wav","data":"","id":"audio_1"}}}`))
	}))
	defer srv.Close()

	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{
		Model: "qwen3-tts-flash", Text: "你好", Voice: "Cherry",
		LanguageType: "Chinese",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	for _, want := range []string{`"model":"qwen3-tts-flash"`, `"text":"你好"`, `"voice":"Cherry"`, `"language_type":"Chinese"`} {
		if !contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	// voice 等语义字段必须在 input 下，不得回退到 messages/content 包装
	if contains(body, `"messages"`) || contains(body, `"parameters"`) {
		t.Errorf("请求体不应含 messages/parameters 包装:\n%s", body)
	}
	if string(got.Audio) != string(audioBytes) {
		t.Errorf("音频字节不符: %q", got.Audio)
	}
	if got.Format != "wav" {
		t.Errorf("Format = %q", got.Format)
	}
}

// instructions 仅随模型语义由工具层传入；client 对 instruct 请求原样携带。
func TestTTSClientSynthesizeInstruct(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"output":{"audio":{"data":"QUJD"}}}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{
		Model: "qwen3-tts-instruct-flash", Text: "hi", Voice: "Cherry",
		Instructions: "用轻快的语速",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(body, `"instructions":"用轻快的语速"`) {
		t.Errorf("请求体缺少 instructions:\n%s", body)
	}
	if string(got.Audio) != "ABC" || got.Format != "wav" {
		t.Errorf("data 解码不符: %q format=%q", got.Audio, got.Format)
	}
}

// 流式形态的 data（base64，无 data: 前缀）直接解码，不发起第二次请求。
func TestTTSClientSynthesizeDataURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"output":{"audio":{"url":"","data":"QUJD"}}}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{Model: "qwen3-tts-flash", Text: "hi", Voice: "Cherry"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Audio) != "ABC" {
		t.Errorf("base64 解码不符: %q", got.Audio)
	}
	if got.Format != "wav" {
		t.Errorf("Format = %q", got.Format)
	}
	if hits.Load() != 1 {
		t.Errorf("data 不应发起下载请求，hits=%d", hits.Load())
	}
}

// 上游错误（code/message 非空）转为中文错误；缺 voice 在客户端前置拦截。
func TestTTSClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"The voice property is required."}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-bad", srv.URL)
	_, err := c.Synthesize(context.Background(), TTSReq{Model: "qwen3-tts-flash", Text: "hi"})
	if err == nil || !contains(err.Error(), "voice") {
		t.Fatalf("缺 voice 应前置报错, got %v", err)
	}
	_, err = c.Synthesize(context.Background(), TTSReq{Model: "qwen3-tts-flash", Text: "hi", Voice: "Cherry"})
	if err == nil || !contains(err.Error(), "InvalidParameter") {
		t.Fatalf("上游 InvalidParameter 应透出, got %v", err)
	}
}

// contains 子串判断辅助（body 形态断言用）。
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
