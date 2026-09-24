package xiaomi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TTS 全链路（真机 wire 结构）：Bearer 鉴权头、OpenAI 兼容 chat/completions——
// 合成文本在 assistant 消息、风格指令在 user 消息、audio.format/voice 平铺顶层；
// 响应 choices[0].message.audio.data base64 解码为字节。
func TestTTSClientSynthesize(t *testing.T) {
	var auth, body string
	audioB64 := "QUJDREVG" // "ABCDEF"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathChatCompletions {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","audio":{"data":"` + audioB64 + `"}}}]}`))
	}))
	defer srv.Close()

	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{
		Model: ModelPreset, Text: "你好", Voice: "冰糖",
		Instructions: "语速轻快", Format: "wav",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	for _, want := range []string{
		`"model":"mimo-v2.5-tts"`,
		`"role":"user","content":"语速轻快"`,
		`"role":"assistant","content":"你好"`,
		`"audio":{"format":"wav","voice":"冰糖"}`,
		`"stream":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if string(got) != "ABCDEF" {
		t.Errorf("音频字节不符: %q", got)
	}
}

// 无风格指令时不得携带 user 消息；voicedesign 无预置音色，voice 字段不随请求下发。
func TestTTSClientVoiceDesignWire(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"audio":{"data":"QUJD"}}}]}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-test", srv.URL)
	if _, err := c.Synthesize(context.Background(), TTSReq{
		Model: ModelVoiceDesign, Text: "hi", Voice: "",
		Instructions: "低沉的男声", Format: "mp3",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"role":"assistant","content":"hi"`) {
		t.Errorf("合成文本应放 assistant 消息:\n%s", body)
	}
	if !strings.Contains(body, `"role":"user","content":"低沉的男声"`) {
		t.Errorf("音色描述应放 user 消息:\n%s", body)
	}
	if strings.Contains(body, `"voice"`) {
		t.Errorf("voicedesign 不应携带 audio.voice:\n%s", body)
	}
	if !strings.Contains(body, `"format":"mp3"`) {
		t.Errorf("请求体缺少 format:\n%s", body)
	}
}

// 上游错误（OpenAI 风格 error 体）转中文错误；响应缺音频时报数据缺失。
func TestTTSClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key","type":"auth_error","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-bad", srv.URL)
	_, err := c.Synthesize(context.Background(), TTSReq{Model: ModelPreset, Text: "hi", Voice: DefaultVoice})
	if err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("上游错误应透出 message, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_api_key") {
		t.Errorf("错误应附 code, got %v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"no audio"}}]}`))
	}))
	defer empty.Close()
	_, err = NewTTSClient("sk-test", empty.URL).Synthesize(context.Background(), TTSReq{Model: ModelPreset, Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "未找到音频") {
		t.Fatalf("缺音频应报数据缺失, got %v", err)
	}
}
