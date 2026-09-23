package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ttsStreamMockServer 流式合成 mock：handler 输出按行 JSON 流（模拟 HTTP Chunked），
// 记录请求头与 body 供断言。
type ttsStreamMockServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	header map[string]string
	body   map[string]any
}

func newTTSStreamMockServer(t *testing.T, lines func() []string) *ttsStreamMockServer {
	m := &ttsStreamMockServer{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.header = map[string]string{
			"X-Api-Key":                             r.Header.Get("X-Api-Key"),
			"X-Api-App-Key":                         r.Header.Get("X-Api-App-Key"),
			"X-Api-Access-Key":                      r.Header.Get("X-Api-Access-Key"),
			"X-Api-Resource-Id":                     r.Header.Get("X-Api-Resource-Id"),
			"X-Api-Request-Id":                      r.Header.Get("X-Api-Request-Id"),
			"X-Control-Require-Usage-Tokens-Return": r.Header.Get("X-Control-Require-Usage-Tokens-Return"),
			"Connection":                            r.Header.Get("Connection"),
		}
		m.body = body
		m.mu.Unlock()
		w.Header().Set("X-Tt-Logid", "logid-stream-1")
		w.WriteHeader(http.StatusOK)
		for _, line := range lines() {
			_, _ = w.Write([]byte(line + "\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *ttsStreamMockServer) snapshot() (map[string]string, map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.header, m.body
}

func TestTTSStreamSynthesizeSuccess(t *testing.T) {
	chunk1 := base64.StdEncoding.EncodeToString([]byte("CHUNK_ONE_"))
	chunk2 := base64.StdEncoding.EncodeToString([]byte("CHUNK_TWO_"))
	lines := []string{
		`{"code":0,"message":"OK","data":"` + chunk1 + `"}`,
		`{"code":0,"message":"OK","data":"` + chunk2 + `","sentence":{"words":[{"word":"你","startTime":0,"endTime":0.2}]}}`,
		`{"code":20000000,"message":"success","sentence":{"text":"你好。","words":[{"word":"好。","startTime":0.2,"endTime":0.5}]},"usage":{"text_words":3}}`,
	}
	m := newTTSStreamMockServer(t, func() []string { return lines })

	var chunkCalls int
	c := NewTTSStreamClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	res, err := c.SynthesizeStream(context.Background(), TTSStreamSubmitReq{
		Text: "你好。", Speaker: "zh_female_vv_uranus_bigtts", Resource: ttsLongResourceSeed,
		Format: "mp3", SampleRate: 24000, EnableSubtitle: true,
	}, func(chunks, bytes int) { chunkCalls++ })
	if err != nil {
		t.Fatal(err)
	}

	// 音频分片按序拼接；终态不计入分片
	if string(res.Audio) != "CHUNK_ONE_CHUNK_TWO_" {
		t.Errorf("audio = %q", res.Audio)
	}
	if res.Chunks != 2 || chunkCalls != 2 {
		t.Errorf("chunks = %d, onChunk 调用 %d 次", res.Chunks, chunkCalls)
	}
	// 字级时间戳跨行累积 + 毫秒归一
	if len(res.Words) != 2 || res.Words[0].StartMS != 0 || res.Words[0].EndMS != 200 ||
		res.Words[1].Word != "好。" || res.Words[1].EndMS != 500 {
		t.Errorf("words = %+v", res.Words)
	}
	if res.Sentence != "你好。" || res.BilledWords != 3 {
		t.Errorf("sentence = %q, billed = %d", res.Sentence, res.BilledWords)
	}

	// 请求头契约：鉴权双轨由 sauc 保证，这里锁流式专属头
	header, body := m.snapshot()
	if header["X-Api-Key"] != "key-1" {
		t.Errorf("X-Api-Key = %q", header["X-Api-Key"])
	}
	if header["X-Api-Request-Id"] == "" {
		t.Error("缺少 X-Api-Request-Id（文档必选）")
	}
	if header["X-Control-Require-Usage-Tokens-Return"] != "*" {
		t.Errorf("X-Control-Require-Usage-Tokens-Return = %q", header["X-Control-Require-Usage-Tokens-Return"])
	}
	if header["X-Api-Resource-Id"] != "seed-tts-2.0" {
		t.Errorf("X-Api-Resource-Id = %q", header["X-Api-Resource-Id"])
	}
	// body 结构：req_params{speaker,text,audio_params}
	params, ok := body["req_params"].(map[string]any)
	if !ok {
		t.Fatalf("body = %v", body)
	}
	if params["speaker"] != "zh_female_vv_uranus_bigtts" {
		t.Errorf("speaker = %v", params["speaker"])
	}
	audio := params["audio_params"].(map[string]any)
	if audio["format"] != "mp3" || audio["sample_rate"] != float64(24000) {
		t.Errorf("audio_params = %v", audio)
	}
	if _, ok := audio["bit_rate"]; ok {
		t.Errorf("bit_rate 零值不应下发: %v", audio)
	}
	if _, ok := audio["enable_subtitle"]; ok != true {
		t.Errorf("enable_subtitle 应下发 true: %v", audio)
	}
}

func TestTTSStreamOptionalParamsForwarded(t *testing.T) {
	m := newTTSStreamMockServer(t, func() []string {
		return []string{`{"code":20000000,"message":"ok"}`}
	})
	c := NewTTSStreamClientWithBaseURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.srv.URL)
	_, err := c.SynthesizeStream(context.Background(), TTSStreamSubmitReq{
		Text: "测试", Speaker: "icl-v", Resource: ttsLongResourceICL, Model: "seed-icl-x",
		Format: "ogg_opus", SampleRate: 48000, BitRate: 160000,
		ExplicitLanguage: "zh-cn", ExplicitDialect: "yue", Pitch: -3,
		SilenceDuration: 500, ContextText: "用伤心的语气", ToneFidelity: true, AIGCWatermark: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	header, body := m.snapshot()
	if header["X-Api-App-Key"] != "app" || header["X-Api-Access-Key"] != "tok" {
		t.Errorf("老版鉴权头 = %v", header)
	}
	if header["X-Api-Resource-Id"] != "seed-icl-2.0" {
		t.Errorf("X-Api-Resource-Id = %q", header["X-Api-Resource-Id"])
	}
	params := body["req_params"].(map[string]any)
	for k, want := range map[string]any{
		"model": "seed-icl-x", "explicit_language": "zh-cn", "explicit_dialect": "yue",
		"silence_duration": float64(500), "tone_fidelity": true, "aigc_watermark": true,
	} {
		if params[k] != want {
			t.Errorf("req_params[%s] = %v, want %v", k, params[k], want)
		}
	}
	if ctxs, ok := params["context_texts"].([]any); !ok || len(ctxs) != 1 || ctxs[0] != "用伤心的语气" {
		t.Errorf("context_texts = %v", params["context_texts"])
	}
	if pp, ok := params["post_process"].(map[string]any); !ok || pp["pitch"] != float64(-3) {
		t.Errorf("post_process = %v", params["post_process"])
	}
}

func TestTTSStreamErrorLine(t *testing.T) {
	m := newTTSStreamMockServer(t, func() []string {
		return []string{`{"code":0,"data":"YQ=="}`, `{"code":45000201,"message":"invalid speaker"}`}
	})
	c := NewTTSStreamClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, err := c.SynthesizeStream(context.Background(), TTSStreamSubmitReq{
		Text: "x", Speaker: "v", Resource: ttsLongResourceSeed, Format: "mp3", SampleRate: 24000,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "45000201") ||
		!strings.Contains(err.Error(), "invalid speaker") || !strings.Contains(err.Error(), "logid-stream-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestTTSStreamStreamCutWithoutFinalLine(t *testing.T) {
	m := newTTSStreamMockServer(t, func() []string {
		return []string{`{"code":0,"data":"YQ=="}`} // 无终止行
	})
	c := NewTTSStreamClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	_, err := c.SynthesizeStream(context.Background(), TTSStreamSubmitReq{
		Text: "x", Speaker: "v", Resource: ttsLongResourceSeed, Format: "mp3", SampleRate: 24000,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "终止帧") {
		t.Fatalf("err = %v, want 流中断报错", err)
	}
}

func TestTTSStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tt-Logid", "logid-500")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom"}`))
	}))
	defer srv.Close()
	c := NewTTSStreamClientWithBaseURL(SpeechCred{APIKey: "key-1"}, srv.URL)
	_, err := c.SynthesizeStream(context.Background(), TTSStreamSubmitReq{
		Text: "x", Speaker: "v", Resource: ttsLongResourceSeed, Format: "mp3", SampleRate: 24000,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "logid-500") {
		t.Fatalf("err = %v", err)
	}
}
