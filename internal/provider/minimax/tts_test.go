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

// TestValidatePauseMarkers 停顿标记预检：数值/精度/连续/首尾位置四类拦截。
func TestValidatePauseMarkers(t *testing.T) {
	ok := []string{
		"今天<#0.5#>真好",             // 正常
		"今天<#1#>真好。<#99.99#>是的",   // 边界值
		"多段\n\n换行<#0.01#>正常",      // 换行后标记
		"语气词(laughs)与发音(he2)不受影响", // 非停顿标记
		"没有标记的普通文本",
	}
	for _, s := range ok {
		if err := validatePauseMarkers(s); err != nil {
			t.Errorf("%q 应通过, got %v", s, err)
		}
	}
	bad := map[string]string{
		"开头<#x#>标记":          "数值",    // 非数值
		"超范围<#100#>标记":       "数值",    // >99.99
		"过小<#0.001#>标记":      "数值",    // <0.01
		"三位小数<#1.234#>标记":    "数值",    // 精度超两位
		"连续<#1#><#2#>标记":     "连续",    // 紧邻
		"连续带空白<#1#> <#2#>标记": "连续",    // 仅空白相隔
		"<#1#>开头标记":          "开头或结尾", // 文本开头
		"结尾标记<#1#>":          "开头或结尾", // 文本结尾
	}
	for s, want := range bad {
		err := validatePauseMarkers(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q 应报 %s 类错误, got %v", s, want, err)
		}
		if !strings.HasPrefix(err.Error(), "参数错误") {
			t.Errorf("校验错误应是参数类（业务码 2）: %v", err)
		}
	}
}

// TestGluePauseMarkers 分段边界的标记粘合：段首标记挪到前段尾部，语义等价。
func TestGluePauseMarkers(t *testing.T) {
	segs := gluePauseMarkers([]string{"第一段。", "<#2#>第二段。", "第三段。"})
	if segs[0] != "第一段。<#2#>" || segs[1] != "第二段。" {
		t.Fatalf("粘合结果 = %#v", segs)
	}
	// 多个连续段首标记全部前移
	segs = gluePauseMarkers([]string{"A。", " <#1#><#2#>B"})
	if segs[0] != "A。 <#1#><#2#>" || segs[1] != "B" {
		t.Fatalf("连续段首标记粘合 = %#v", segs)
	}
	// 无标记时原样
	segs = gluePauseMarkers([]string{"A。", "B。"})
	if segs[0] != "A。" || segs[1] != "B。" {
		t.Fatalf("无标记不应改动 = %#v", segs)
	}
}

// TestTTSToolPauseGlueEndToEnd 分段+粘合全链路：mock 捕获各段请求体，确认没有请求
// 以停顿标记开头（官方要求标记前有可发音文本）。
func TestTTSToolPauseGlueEndToEnd(t *testing.T) {
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body["text"].(string))
		_, _ = w.Write([]byte(t2aHexResp(miniWAV(8, 1))))
	}))
	defer ts.Close()

	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client.baseURL = ts.URL
	// 2050 字符文本，跨段放置停顿标记：段边界恰好落在标记所在的下一句
	text := strings.Repeat("长", 1044) + "。<#1.5#>" + strings.Repeat("短", 1000) + "。"
	if _, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": text},
	}, func(int, string, map[string]any) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(bodies) < 2 {
		t.Fatalf("应分段合成, got %d 段", len(bodies))
	}
	for i, b := range bodies {
		if strings.HasPrefix(b, "<#") {
			t.Errorf("第 %d 段以停顿标记开头（应粘合到前段尾部）: %.20q", i+1, b)
		}
	}
	if !strings.Contains(bodies[0], "<#1.5#>") {
		t.Errorf("粘合后前段尾部应含标记: %.40q", bodies[0])
	}
}
