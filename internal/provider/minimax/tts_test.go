package minimax

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// t2aHexResp 构造 T2A 成功响应（data.audio 为 hex 编码音频）。
func t2aHexResp(audio []byte) string {
	return fmt.Sprintf(`{"data":{"audio":%q,"status":2},"extra_info":{"audio_length":100,"usage_characters":3},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		hex.EncodeToString(audio))
}

// TestTTSClientSynthesize 正常链路：请求 wire 结构（model/text/stream/voice_setting/
// audio_setting），响应为 JSON 包裹的 hex 音频。
func TestTTSClientSynthesize(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("请求体非 JSON: %v", err)
		}
		_, _ = w.Write([]byte(t2aHexResp([]byte("fake-wav-bytes"))))
	}))
	defer ts.Close()

	c := NewTTSClient("sk-test", ts.URL)
	data, err := c.Synthesize(context.Background(), TTSReq{Text: "你好", Voice: DefaultVoice, Speed: 1.2})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(data) != "fake-wav-bytes" {
		t.Errorf("音频体 = %q", data)
	}
	if gotPath != pathT2A {
		t.Errorf("路径 = %s, want %s", gotPath, pathT2A)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("鉴权头 = %q", gotAuth)
	}
	if gotBody["model"] != ModelTTSHD {
		t.Errorf("model = %v, want %s", gotBody["model"], ModelTTSHD)
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	vs, _ := gotBody["voice_setting"].(map[string]any)
	if vs == nil || vs["voice_id"] != DefaultVoice || vs["speed"] != 1.2 {
		t.Errorf("voice_setting = %v", gotBody["voice_setting"])
	}
	as_, _ := gotBody["audio_setting"].(map[string]any)
	if as_ == nil || as_["format"] != "wav" || as_["sample_rate"] != float64(32000) {
		t.Errorf("audio_setting = %v", gotBody["audio_setting"])
	}
}

// TestTTSClientBaseRespError 业务错误走 HTTP 200 + base_resp.status_code（官方包络）。
func TestTTSClientBaseRespError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`))
	}))
	defer ts.Close()

	c := NewTTSClient("bad-key", ts.URL)
	_, err := c.Synthesize(context.Background(), TTSReq{Text: "测", Voice: DefaultVoice})
	if err == nil || !strings.Contains(err.Error(), "1004") {
		t.Fatalf("base_resp 业务错误应转译为错误, got %v", err)
	}
}

// TestTTSToolRun 单段合成：落盘 tts/<id>.wav，summary 记录模型/音色。
func TestTTSToolRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(t2aHexResp([]byte("fake-wav-bytes"))))
	}))
	defer ts.Close()

	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL // 同包注入测试入口（BaseURL 为 const，不重赋值）
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "语音合成测试", "voice": "female-shaonv"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(out.Artifacts))
	}
	a := out.Artifacts[0]
	if a.Kind != "audio" || a.Format != "wav" {
		t.Errorf("artifact = %+v", a)
	}
	if !strings.HasPrefix(a.Path, "tts/") || !strings.HasSuffix(a.Path, ".wav") {
		t.Errorf("产物路径 = %s, want tts/<id>.wav", a.Path)
	}
	if out.Summary["voice"] != "female-shaonv" || out.Summary["model"] != ModelTTSHD {
		t.Errorf("summary = %v", out.Summary)
	}
}

// miniWAV 构造最小合法 WAV（32kHz/16bit/单声道，n 个采样帧）。
func miniWAV(n int, fill byte) []byte {
	data := bytes.Repeat([]byte{fill}, n*2)
	buf := bytes.NewBuffer(nil)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+len(data)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	for _, v := range []any{uint16(1), uint16(1), uint32(32000), uint32(64000), uint16(2), uint16(16)} {
		binary.Write(buf, binary.LittleEndian, v)
	}
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

// TestTTSToolSegmentation 超限文本自动分段：段数与拼接产物字节一致（wav 拼接纯 Go）。
func TestTTSToolSegmentation(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(t2aHexResp(miniWAV(8, byte(calls)))))
	}))
	defer ts.Close()

	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	// 2100 汉字 > 2000 段预算 → 至少 2 段
	long := strings.Repeat("好。", 1050)
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": long},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls < 2 {
		t.Fatalf("分段调用数 = %d, want >= 2", calls)
	}
	if out.Summary["segment_num"] != calls {
		t.Errorf("summary.segment_num = %v, want %d", out.Summary["segment_num"], calls)
	}
	a := out.Artifacts[0]
	// 产物路径经 resolveOut 锚定 outDir，从 tool.outDir 读回验证拼接结果
	data, err := os.ReadFile(filepath.Join(tool.outDir, a.Path))
	if err != nil {
		t.Fatalf("读取产物: %v", err)
	}
	if len(data) != calls*16+44 {
		t.Errorf("拼接产物 data 长度 = %d, want %d（各段 data 合并 + 单头）", len(data), calls*16+44)
	}
}

// TestTTSToolNoCred 凭证缺失 → ErrNoCred。
func TestTTSToolNoCred(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "测"},
	}, func(int, string, map[string]any) {})
	if !errors.Is(err, ErrNoCred) {
		t.Fatalf("err = %v, want ErrNoCred", err)
	}
}

// TestTTSLongFlow 异步链路：create → 轮询（processing→success）→ retrieve_content 下载。
func TestTTSLongFlow(t *testing.T) {
	oldInterval, oldMax := ttsLongPollInterval, ttsLongPollMax
	ttsLongPollInterval, ttsLongPollMax = time.Millisecond, time.Millisecond
	defer func() { ttsLongPollInterval, ttsLongPollMax = oldInterval, oldMax }()

	queries := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == pathT2AAsync:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("请求体非 JSON: %v", err)
			}
			as_, _ := body["audio_setting"].(map[string]any)
			if as_ == nil || as_["audio_sample_rate"] != float64(32000) {
				t.Errorf("异步 audio_setting 应使用 audio_sample_rate 键: %v", body["audio_setting"])
			}
			_, _ = w.Write([]byte(`{"task_id":"12345678","file_id":99,"usage_characters":10,"base_resp":{"status_code":0}}`))
		case r.URL.Path == pathT2AQuery:
			queries++
			if r.URL.Query().Get("task_id") != "12345678" {
				t.Errorf("task_id = %q", r.URL.Query().Get("task_id"))
			}
			if queries < 2 {
				_, _ = w.Write([]byte(`{"task_id":12345678,"status":"Processing","base_resp":{"status_code":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"task_id":12345678,"status":"Success","file_id":99,"base_resp":{"status_code":0}}`))
		case r.URL.Path == pathFileRetrieve:
			if r.URL.Query().Get("file_id") != "99" {
				t.Errorf("file_id = %q", r.URL.Query().Get("file_id"))
			}
			_, _ = w.Write([]byte("fake-mp3-long"))
		default:
			t.Errorf("未预期路径 %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	tool := NewTTSLongTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "长文本合成"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if queries < 2 {
		t.Fatalf("查询次数 = %d, want >= 2", queries)
	}
	a := out.Artifacts[0]
	if a.Kind != "audio" || a.Format != "mp3" {
		t.Errorf("artifact = %+v", a)
	}
	if out.Summary["task_id"] != "12345678" {
		t.Errorf("summary = %v", out.Summary)
	}
}

// TestTTSLongZipDownload 结果包为 zip 时抽取音频条目（官方多文件产物形态）。
func TestTTSLongZipDownload(t *testing.T) {
	oldInterval, oldMax := ttsLongPollInterval, ttsLongPollMax
	ttsLongPollInterval, ttsLongPollMax = time.Millisecond, time.Millisecond
	defer func() { ttsLongPollInterval, ttsLongPollMax = oldInterval, oldMax }()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w1, _ := zw.Create("subtitle.json")
	_, _ = w1.Write([]byte(`{"sentences":[]}`))
	w2, _ := zw.Create("output.mp3")
	_, _ = w2.Write([]byte("audio-in-zip"))
	_ = zw.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathT2AAsync:
			_, _ = w.Write([]byte(`{"task_id":"1","file_id":2,"base_resp":{"status_code":0}}`))
		case pathT2AQuery:
			_, _ = w.Write([]byte(`{"task_id":1,"status":"success","file_id":2,"base_resp":{"status_code":0}}`))
		case pathFileRetrieve:
			_, _ = w.Write(buf.Bytes())
		}
	}))
	defer ts.Close()

	tool := NewTTSLongTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "长文本"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tool.outDir, out.Artifacts[0].Path))
	if err != nil {
		t.Fatalf("读取产物: %v", err)
	}
	if string(data) != "audio-in-zip" {
		t.Errorf("zip 内音频未抽取: %q", data)
	}
}

// TestTTSLongTooLong 超长文本预检（5 万字符硬上限 → 参数类错误）。
func TestTTSLongTooLong(t *testing.T) {
	tool := NewTTSLongTool("sk-test", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": strings.Repeat("字", ttsLongMaxLength+1)},
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "超出长度限制") {
		t.Fatalf("err = %v, want 超出长度限制", err)
	}
}
