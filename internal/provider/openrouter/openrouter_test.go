package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// TestSynthesizeOK 正常链路：请求 wire 结构（model/input/voice/response_format），
// 成功响应为音频二进制（非 JSON）。
func TestSynthesizeOK(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("请求体非 JSON: %v", err)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("fake-mp3-bytes"))
	}))
	defer ts.Close()

	c := NewTTSClient("sk-test", ts.URL)
	data, err := c.Synthesize(context.Background(), TTSReq{Text: "你好", Voice: "Zephyr"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(data) != "fake-mp3-bytes" {
		t.Errorf("音频体 = %q", data)
	}
	if gotPath != pathTTS {
		t.Errorf("路径 = %s, want %s", gotPath, pathTTS)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("鉴权头 = %q", gotAuth)
	}
	if gotBody["model"] != ModelTTS {
		t.Errorf("model = %v, want %s", gotBody["model"], ModelTTS)
	}
	if gotBody["input"] != "你好" {
		t.Errorf("input = %v", gotBody["input"])
	}
	if gotBody["voice"] != "Zephyr" {
		t.Errorf("voice = %v", gotBody["voice"])
	}
	// pcm 裸流无文件头不可播：固定 mp3（自带封装可直接试听）
	if gotBody["response_format"] != "mp3" {
		t.Errorf("response_format = %v, want mp3", gotBody["response_format"])
	}
}

// TestSynthesizeDecodeError 非 2xx：错误体 {"error":{"code","message"}} 转译为可读文案。
func TestSynthesizeDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"code":402,"message":"Insufficient credits"}}`))
	}))
	defer ts.Close()

	c := NewTTSClient("sk-test", ts.URL)
	_, err := c.Synthesize(context.Background(), TTSReq{Text: "测", Voice: "Zephyr"})
	if err == nil {
		t.Fatal("非 2xx 应返回错误")
	}
	if !strings.Contains(err.Error(), "Insufficient credits") {
		t.Errorf("错误未透出上游信息: %v", err)
	}
}

// TestSynthesizeNoVoice voice 缺参在请求前拦截。
func TestSynthesizeNoVoice(t *testing.T) {
	c := NewTTSClient("sk-test", "https://openrouter.ai/api/v1")
	if _, err := c.Synthesize(context.Background(), TTSReq{Text: "测"}); err == nil ||
		!strings.Contains(err.Error(), "voice") {
		t.Errorf("缺 voice 应报参数错误, got %v", err)
	}
}

// TestTTSToolRun 工具链路：合成落盘 tts/<uuid>.mp3，产物与摘要同口径。
func TestTTSToolRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fake-mp3-bytes"))
	}))
	defer ts.Close()

	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL // 同包注入测试入口（BaseURL 为 const，不重赋值）
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "语音合成测试", "voice": "Puck"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(out.Artifacts))
	}
	a := out.Artifacts[0]
	if a.Kind != "audio" || a.Format != "mp3" {
		t.Errorf("artifact = %+v", a)
	}
	if !strings.HasPrefix(a.Path, "tts/") || !strings.HasSuffix(a.Path, ".mp3") {
		t.Errorf("产物路径 = %s, want tts/<id>.mp3", a.Path)
	}
	if out.Summary["voice"] != "Puck" || out.Summary["model"] != ModelTTS {
		t.Errorf("summary = %v", out.Summary)
	}
}

// TestTTSToolNoCred 凭证缺失 → ErrNoCred（CLI 退出码与 Web 业务码 4 的映射源）。
func TestTTSToolNoCred(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "测"},
	}, func(int, string, map[string]any) {})
	if !errors.Is(err, ErrNoCred) {
		t.Fatalf("err = %v, want ErrNoCred", err)
	}
}

// TestTTSToolEmptyText 空文本参数校验。
func TestTTSToolEmptyText(t *testing.T) {
	tool := NewTTSTool("sk-test", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "  "},
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("空文本应报参数错误, got %v", err)
	}
}

// TestVoices 词表完整性：30 个预置音色、无重复、默认音色在表、枚举同源。
func TestVoices(t *testing.T) {
	voices := Voices()
	if len(voices) != 30 {
		t.Fatalf("voices = %d, want 30", len(voices))
	}
	seen := map[string]bool{}
	for _, v := range voices {
		if v.ID == "" || v.Label == "" {
			t.Errorf("音色条目字段为空: %+v", v)
		}
		if seen[v.ID] {
			t.Errorf("音色重复: %s", v.ID)
		}
		seen[v.ID] = true
	}
	if !seen[DefaultVoice] {
		t.Errorf("默认音色 %s 不在词表", DefaultVoice)
	}
	opts := VoiceOptions()
	if len(opts) != len(voices) {
		t.Errorf("枚举数 = %d, want %d", len(opts), len(voices))
	}
	if opts[0].Value != DefaultVoice {
		t.Errorf("枚举首项 = %s, want 默认音色", opts[0].Value)
	}
}

// TestParamSpecs 参数声明：text 必填 + voice 枚举默认音色。
func TestParamSpecs(t *testing.T) {
	specs := NewTTSTool("k", ".").ParamSpecs()
	if len(specs) != 2 {
		t.Fatalf("specs = %d, want 2", len(specs))
	}
	if specs[0].Key != "text" || !specs[0].Required {
		t.Errorf("text spec = %+v", specs[0])
	}
	if specs[1].Key != "voice" || specs[1].Default != DefaultVoice || len(specs[1].Options) == 0 {
		t.Errorf("voice spec = %+v", specs[1])
	}
}
