package xiaomi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ASR 全链路（真机 wire 结构）：Bearer 鉴权头、音频以 input_audio 内容部件进 user 消息
// （data 为 data URI 自描述 MIME）、asr_options.language 平铺顶层；响应 choices[0].message.content
// 为转写文本，usage.prompt_tokens_details.seconds 为音频时长。
func TestASRClientTranscribe(t *testing.T) {
	var auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathChatCompletions {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"你好世界"}}],
			"usage":{"prompt_tokens_details":{"audio_tokens":100,"seconds":3}}}`))
	}))
	defer srv.Close()

	c := NewASRClient("sk-test", srv.URL)
	text, sec, err := c.Transcribe(context.Background(), ASRInput{
		Audio: []byte("ID3fake-mp3"), MIME: "audio/mpeg", Language: "zh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	for _, want := range []string{
		`"model":"mimo-v2.5-asr"`,
		`"role":"user"`,
		`"type":"input_audio"`,
		`"data":"data:audio/mpeg;base64,SUQzZmFrZS1tcDM="`,
		`"language":"zh"`,
		`"stream":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if text != "你好世界" {
		t.Errorf("转写文本 = %q", text)
	}
	if sec != 3 {
		t.Errorf("音频时长 = %d", sec)
	}
}

// language 留空/auto 时不发送 asr_options（上游默认自动识别）。
func TestASRClientLanguageOmitted(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk-test", srv.URL)
	for _, lang := range []string{"", "auto"} {
		if _, _, err := c.Transcribe(context.Background(), ASRInput{
			Audio: []byte("RIFFxWAVEx"), MIME: "audio/wav", Language: lang,
		}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, "asr_options") {
			t.Errorf("language=%q 不应携带 asr_options:\n%s", lang, body)
		}
	}
}

// 上游错误透出 message/code；响应缺文本时报数据缺失。
func TestASRClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(413)
		_, _ = w.Write([]byte(`{"error":{"message":"Audio too large","code":"payload_too_large"}}`))
	}))
	defer srv.Close()
	_, _, err := NewASRClient("sk-bad", srv.URL).Transcribe(context.Background(), ASRInput{
		Audio: []byte("ID3x"), MIME: "audio/mpeg",
	})
	if err == nil || !strings.Contains(err.Error(), "Audio too large") {
		t.Fatalf("上游错误应透出 message, got %v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
	}))
	defer empty.Close()
	_, _, err = NewASRClient("sk-test", empty.URL).Transcribe(context.Background(), ASRInput{
		Audio: []byte("ID3x"), MIME: "audio/mpeg",
	})
	if err == nil || !strings.Contains(err.Error(), "未找到转写文本") {
		t.Fatalf("缺文本应报数据缺失, got %v", err)
	}
}

// 魔数判别：ID3/帧同步 → mp3，RIFF/WAVE → wav，其余空串。
func TestSniffAudioMIME(t *testing.T) {
	cases := []struct {
		want string
		b    []byte
	}{
		{"audio/mpeg", []byte("ID3\x04tag")},
		{"audio/mpeg", []byte{0xFF, 0xFB, 0x90, 0x00}},
		{"audio/wav", []byte("RIFF\x24\x00\x00\x00WAVEfmt ")},
		{"", []byte("fLaC\x00\x00\x00\x22")},
		{"", []byte("OggS\x00\x02")},
	}
	for _, c := range cases {
		if got := sniffAudioMIME(c.b); got != c.want {
			t.Errorf("sniffAudioMIME(% x) = %q, want %q", c.b[:4], got, c.want)
		}
	}
}
