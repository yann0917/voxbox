package minimax

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// verboseResp 构造 verbose_json 识别响应（说话人分离 + 句级时间戳）。
const verboseResp = `{"text":"嘎嘎会，可以，这把稳了。来检查一下，读下题。","duration":12.744,"n_speakers":2,` +
	`"segments":[{"id":0,"start":0.1,"end":1.66,"speaker":"S1","text":"嘎嘎会，可以，这把稳了。"},` +
	`{"id":1,"start":2,"end":6.1,"speaker":"S2","text":"来检查一下，读下题。"}],` +
	`"trace_id":"0217"}`

// TestASRClientTranscribe 正常链路：multipart 字段（model/response_format/timestamp_level）、
// language 走请求头（官方走 header 不走表单）。
func TestASRClientTranscribe(t *testing.T) {
	var gotPath, gotAuth, gotLang, gotFileField string
	var gotForm map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotLang = r.Header.Get("language")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("请求体非 multipart: %v", err)
		}
		gotForm = map[string]string{
			"model":           r.FormValue("model"),
			"response_format": r.FormValue("response_format"),
			"timestamp_level": r.FormValue("timestamp_level"),
		}
		if f, _, err := r.FormFile("file"); err == nil {
			data, _ := io.ReadAll(f)
			f.Close()
			gotFileField = string(data)
		}
		_, _ = w.Write([]byte(verboseResp))
	}))
	defer ts.Close()

	audio := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(audio, []byte("fake-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewASRClient("sk-test", ts.URL)
	resp, err := c.Transcribe(context.Background(), audio, "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != pathASR {
		t.Errorf("路径 = %s, want %s", gotPath, pathASR)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("鉴权头 = %q", gotAuth)
	}
	if gotLang != "zh" {
		t.Errorf("language 头 = %q, want zh", gotLang)
	}
	if gotForm["model"] != ASRModel || gotForm["response_format"] != "verbose_json" || gotForm["timestamp_level"] != "sentence" {
		t.Errorf("form = %v", gotForm)
	}
	if gotFileField != "fake-wav" {
		t.Errorf("file 字段 = %q", gotFileField)
	}
	if len(resp.Segments) != 2 || resp.Segments[0].Speaker != "S1" {
		t.Errorf("segments = %+v", resp.Segments)
	}
}

// TestASRClientError OpenAI 风格错误体（真实 HTTP 状态码）转译。
func TestASRClientError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"insufficient_balance_error","message":"insufficient balance (1008)","http_code":"402"},"request_id":"x"}`))
	}))
	defer ts.Close()

	audio := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(audio, []byte("fake-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewASRClient("sk-test", ts.URL)
	_, err := c.Transcribe(context.Background(), audio, "")
	if err == nil || !strings.Contains(err.Error(), "insufficient balance") {
		t.Fatalf("错误应透出上游信息, got %v", err)
	}
}

// TestASRToolRun 短音频直传：txt + SRT 双产物，summary 带时间轴分句与说话人数。
func TestASRToolRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(verboseResp))
	}))
	defer ts.Close()

	audio := filepath.Join(t.TempDir(), "in.wav")
	if err := os.WriteFile(audio, []byte("fake-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"language": "zh"},
		Files:  map[string]string{"audio": audio},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2（txt+srt）", len(out.Artifacts))
	}
	if out.Artifacts[0].Kind != "transcript" || out.Artifacts[1].Kind != "subtitle" {
		t.Errorf("artifacts = %+v", out.Artifacts)
	}
	srt, err := os.ReadFile(filepath.Join(tool.outDir, out.Artifacts[1].Path))
	if err != nil {
		t.Fatalf("读取 SRT: %v", err)
	}
	if !strings.Contains(string(srt), "00:00:00,100 --> 00:00:01,660") {
		t.Errorf("SRT 时间轴异常: %s", srt)
	}
	segs, _ := out.Summary["segments"].([]map[string]any)
	if len(segs) != 2 || segs[0]["speaker"] != "S1" {
		t.Errorf("summary.segments = %v", out.Summary["segments"])
	}
	if out.Summary["speakers_count"] != 2 {
		t.Errorf("speakers_count = %v", out.Summary["speakers_count"])
	}
	if out.Summary["duration_ms"] != int64(12744) {
		t.Errorf("duration_ms = %v", out.Summary["duration_ms"])
	}
}

// TestASRToolSegmentOffset 多段转写时间轴归一：第二段时间戳叠加首段时长偏移。
func TestASRToolSegmentOffset(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".wav") || strings.Contains(r.URL.Path, "seg-") {
			_, _ = w.Write([]byte(verboseResp))
			return
		}
		_, _ = w.Write([]byte(verboseResp))
	}))
	defer ts.Close()

	tool := NewASRTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	parts := make([]string, 0, 2)
	for _, name := range []string{"p1.wav", "p2.wav"} {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte("fake-wav"), 0o644); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, p)
	}
	segs, _, _, err := tool.transcribeParts(context.Background(), parts, "", 25.5, nil)
	if err != nil {
		t.Fatalf("transcribeParts: %v", err)
	}
	if len(segs) != 4 {
		t.Fatalf("分句数 = %d, want 4", len(segs))
	}
	// 第二段（p2）的起始时间 = 12.744s（首段 duration）+ 0.1s，毫秒口径与实现一致
	want := secToMS(12.744) + 100
	if segs[2].StartMS != want {
		t.Errorf("第二段起始 = %d, want %d", segs[2].StartMS, want)
	}
}

// TestASRToolNoCred 凭证缺失 → ErrNoCred。
func TestASRToolNoCred(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "in.wav")
	if err := os.WriteFile(audio, []byte("fake-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": audio},
	}, func(int, string, map[string]any) {})
	if !errors.Is(err, ErrNoCred) {
		t.Fatalf("err = %v, want ErrNoCred", err)
	}
}

// TestASRToolBadExt 官方白名单外格式拦截（不含裸 PCM）。
func TestASRToolBadExt(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "in.pcm")
	if err := os.WriteFile(audio, []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": audio},
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "仅支持") {
		t.Fatalf("err = %v, want 格式白名单错误", err)
	}
}

// TestASRToolJSONFallback response_format 兜底为纯文本（无时间轴）时不产 SRT。
func TestASRToolJSONFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"text":"只有全文。","duration":3.2,"trace_id":"x"}`))
	}))
	defer ts.Close()

	audio := filepath.Join(t.TempDir(), "in.mp3")
	if err := os.WriteFile(audio, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": audio},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "transcript" {
		t.Fatalf("无时间轴时只应产出 txt: %+v", out.Artifacts)
	}
	if _, has := out.Summary["segments"]; has {
		t.Errorf("无时间轴不应产出 segments: %v", out.Summary)
	}
}

// TestVoices 静态音色表完整性：无重复 ID、默认音色在表、枚举同源。
func TestVoices(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range Voices() {
		if v.ID == "" {
			t.Fatal("存在空 ID 音色")
		}
		if seen[v.ID] {
			t.Fatalf("重复音色 ID: %s", v.ID)
		}
		seen[v.ID] = true
	}
	if !seen[DefaultVoice] {
		t.Fatalf("默认音色 %s 不在静态表", DefaultVoice)
	}
	for _, v := range Voices() {
		if v.Label == "" {
			t.Fatalf("音色 %s 缺少显示名", v.ID)
		}
	}
}

// TestGetVoiceVoiceOptions 运行时音色列表映射：系统音色主显官方名，复刻/文生回落 ID。
func TestGetVoiceVoiceOptions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathGetVoice {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["voice_type"] != "all" {
			t.Errorf("voice_type = %q, want all", body["voice_type"])
		}
		_, _ = w.Write([]byte(`{"system_voice":[{"voice_id":"male-qn-qingse","voice_name":"青涩青年音色","description":["青年男声"]}],` +
			`"voice_cloning":[{"voice_id":"clone-1","description":["用户复刻声音"]}],` +
			`"voice_generation":[{"voice_id":"ttv-1","description":[]}],` +
			`"base_resp":{"status_code":0}}`))
	}))
	defer ts.Close()

	opts, err := NewVoiceClient("sk-test", ts.URL).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(opts) != 3 {
		t.Fatalf("音色数 = %d, want 3", len(opts))
	}
	if opts[0].Label != "青涩青年音色" || opts[1].Label != "用户复刻声音" || opts[2].Label != "ttv-1" {
		t.Errorf("labels = %q %q %q", opts[0].Label, opts[1].Label, opts[2].Label)
	}
}

// TestGetVoiceLangInference 运行时音色语种标注：快照 ID 直查、新增音色按官方
// 命名前缀推断、无命名规律的 UUID 归空（前端落「其他」组）——get_voice 不返回
// 语种，缺了它 327 个音色会挤进一个不分语言的组。
func TestGetVoiceLangInference(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"system_voice":[` +
			`{"voice_id":"male-qn-qingse","voice_name":"青涩青年音色"},` +
			`{"voice_id":"English_NewVoice_X","voice_name":"New Voice"},` +
			`{"voice_id":"Cantonese_NewLady","voice_name":"粤语新声"},` +
			`{"voice_id":"moss_audio_ce44fc67-7ce3-11f0","voice_name":"莫斯声"}],` +
			`"base_resp":{"status_code":0}}`))
	}))
	defer ts.Close()

	voices, err := NewVoiceClient("sk-test", ts.URL).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]string{
		"male-qn-qingse":                "中文",      // 静态快照直查
		"English_NewVoice_X":            "英文",      // 命名前缀推断
		"Cantonese_NewLady":             "中文 (粤语)", // 命名前缀推断
		"moss_audio_ce44fc67-7ce3-11f0": "",        // 无规律 UUID 不猜
	}
	for _, v := range voices {
		if want[v.ID] != v.Lang {
			t.Errorf("音色 %s 语种 = %q, want %q", v.ID, v.Lang, want[v.ID])
		}
	}
}
