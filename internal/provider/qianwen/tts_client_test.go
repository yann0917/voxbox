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

// TTS 全链路：Bearer 鉴权头、请求体形态（messages 内 text/voice/language_type/instructions）、
// 响应 content 里的音频 URL 被下载为字节。
func TestTTSClientSynthesize(t *testing.T) {
	var auth, body string
	audioBytes := []byte("fake-mp3-bytes")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 音频下载请求与合成请求共用本 handler，先分流，避免覆盖合成请求的抓包值
		if r.URL.Path == "/audio.mp3" {
			_, _ = w.Write(audioBytes)
			return
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"text":"你好"},{"audio":"` + srv.URL + `/audio.mp3"}]}}]}}`))
	}))
	defer srv.Close()

	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{
		Model: "qwen3-tts-flash", Text: "你好", Voice: "Cherry",
		LanguageType: "Chinese", Instructions: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	for _, want := range []string{`"model":"qwen3-tts-flash"`, `"voice":"Cherry"`, `"text":"你好"`, `"language_type":"Chinese"`} {
		if !contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if string(got.Audio) != string(audioBytes) {
		t.Errorf("音频字节不符: %q", got.Audio)
	}
	if got.Format != "mp3" {
		t.Errorf("Format = %q", got.Format)
	}
}

// data URI 音频（content 项为 data:audio/...;base64,...）直接解码，不发起第二次请求。
func TestTTSClientSynthesizeDataURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"audio":"data:audio/wav;base64,QUJD"}]}}]}}`))
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
		t.Errorf("data URI 不应发起下载请求，hits=%d", hits.Load())
	}
}

// 上游错误（code/message 非空）转为中文错误。
func TestTTSClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key"}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-bad", srv.URL)
	if _, err := c.Synthesize(context.Background(), TTSReq{Text: "hi"}); err == nil {
		t.Fatal("期望 401 报错")
	}
}

// contains 子串判断辅助（body 形态断言用）。
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
