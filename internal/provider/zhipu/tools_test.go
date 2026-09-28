package zhipu

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yann0917/voxbox/internal/provider"
)

// tts 工具：默认音色/产物落盘/_out 重定向/凭证哨兵/ParamSpecs。
func TestTTSToolRun(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte("fake-wav"))
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewTTSTool("zp-test", out)
	tool.client = NewTTSClient("zp-test", srv.URL)

	outPath := filepath.Join(out, "custom.wav")
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "_out": outPath},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Format != "wav" || res.Artifacts[0].Path != "custom.wav" {
		t.Fatalf("产物不符: %+v", res.Artifacts)
	}
	if res.Summary["voice"] != DefaultVoice {
		t.Errorf("默认音色不符: %+v", res.Summary)
	}
	if raw, err := os.ReadFile(outPath); err != nil || string(raw) != "fake-wav" {
		t.Errorf("音频落盘不符: %q err=%v", raw, err)
	}
	if !strings.Contains(body, `"voice":"tongtong"`) {
		t.Errorf("请求体缺少默认音色:\n%s", body)
	}
}

func TestTTSToolValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("校验失败不应发起上游请求")
	}))
	defer srv.Close()
	tool := NewTTSTool("", t.TempDir())
	tool.client = NewTTSClient("", srv.URL)

	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("缺 text 应报参数错误, got %v", err)
	}
	// 超长文本分段合成行为见 TestTTSToolSegmented
	tool.apiKey = ""
	_, err = tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "hi"}},
		func(int, string, map[string]any) {})
	if !errors.Is(err, ErrNoCred) {
		t.Errorf("缺凭证应包装 ErrNoCred, got %v", err)
	}
}

// asr 工具：本地文件直传/扩展名白名单/25MB 拦截/URL 下载转传临时文件清理。
func TestASRToolRun(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotForm = string(raw)
		_, _ = w.Write([]byte(`{"text":"结果文本"}`))
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewASRTool("zp-test", out)
	tool.client = NewASRClient("zp-test", srv.URL)

	src := filepath.Join(t.TempDir(), "clip.mp3")
	if err := os.WriteFile(src, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"_out": filepath.Join(out, "t.txt"), "hotwords": " voxelbox, 智谱"},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary["source"] != "file" || res.Summary["model"] != "glm-asr-2512" {
		t.Errorf("Summary 不符: %+v", res.Summary)
	}
	if !strings.Contains(gotForm, `filename="clip.mp3"`) || !strings.Contains(gotForm, "智谱") {
		t.Errorf("表单应含文件与热词:\n%s", gotForm)
	}
	if raw, err := os.ReadFile(filepath.Join(out, "t.txt")); err != nil || string(raw) != "结果文本" {
		t.Errorf("转写落盘不符: %q err=%v", raw, err)
	}
}

func TestASRToolValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("校验失败不应发起上游请求")
	}))
	defer srv.Close()
	tool := NewASRTool("zp-test", t.TempDir())
	tool.client = NewASRClient("zp-test", srv.URL)
	run := func(params map[string]any, files map[string]string) error {
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: params, Files: files},
			func(int, string, map[string]any) {})
		return err
	}
	if err := run(map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "缺少必填参数") {
		t.Errorf("缺输入应报缺少必填参数, got %v", err)
	}
	if err := run(map[string]any{"url": "ftp://x/a.mp3"}, nil); err == nil || !strings.Contains(err.Error(), "http(s)") {
		t.Errorf("非法 scheme 应报参数错误, got %v", err)
	}
	bad := filepath.Join(t.TempDir(), "song.flac")
	if err := os.WriteFile(bad, []byte("fLaC"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(map[string]any{}, map[string]string{"audio": bad}); err == nil || !strings.Contains(err.Error(), "仅支持 wav / mp3") {
		t.Errorf("flac 应报仅支持 wav/mp3, got %v", err)
	}
	big := filepath.Join(t.TempDir(), "big.mp3")
	if err := os.WriteFile(big, make([]byte, zhipuASRMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	// 超 25MB 不再硬拦截，转自动分段；内容不可探测 → 报「超过 25MB 无法分段」
	if err := run(map[string]any{}, map[string]string{"audio": big}); err == nil || !strings.Contains(err.Error(), "超过 25MB") {
		t.Errorf("超 25MB 不可探测文件应报无法自动分段, got %v", err)
	}
	// 整文件上限（200MB）：稀疏文件只截断不落盘
	huge := filepath.Join(t.TempDir(), "huge.wav")
	if f, ferr := os.Create(huge); ferr != nil {
		t.Fatal(ferr)
	} else if ferr := f.Truncate(asrInputCap + 1); ferr != nil {
		t.Fatal(ferr)
	} else {
		f.Close()
	}
	if err := run(map[string]any{}, map[string]string{"audio": huge}); err == nil || !strings.Contains(err.Error(), "火山引擎") {
		t.Errorf("超 200MB 应拦截并指引火山引擎, got %v", err)
	}
}

// 超长 wav 自动分段：纯 Go 按帧切分（≤29s/段），逐段转写拼接；
// 第 2 段起以上一段文本尾部作 prompt 上下文。
func TestASRToolSegmentedWAV(t *testing.T) {
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		prompts = append(prompts, r.FormValue("prompt"))
		_, _ = w.Write([]byte(`{"text":"段文本"}`))
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewASRTool("zp", out)
	tool.client = NewASRClient("zp", srv.URL)

	// 60s 8kHz 单声道 16bit（960KB），预算 29s → 3 段
	src := filepath.Join(t.TempDir(), "long.wav")
	if err := os.WriteFile(src, testWAV(t, 8000, 8000*60), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"_out": filepath.Join(out, "t.txt")},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 3 {
		t.Fatalf("上游请求数 = %d, want 3", len(prompts))
	}
	// prompt 链累积：第 i 段拿到前 i-1 段拼接文本（≤4000 rune 尾部）
	if prompts[0] != "" || prompts[1] != "段文本" || prompts[2] != "段文本段文本" {
		t.Errorf("prompt 链不符: %q", prompts)
	}
	if res.Summary["segment_num"] != 3 {
		t.Errorf("segment_num = %v, want 3", res.Summary["segment_num"])
	}
	if res.Summary["duration_ms"] != int64(60000) {
		t.Errorf("duration_ms = %v, want 60000", res.Summary["duration_ms"])
	}
	raw := mustRead(t, filepath.Join(out, "t.txt"))
	if string(raw) != "段文本段文本段文本" {
		t.Errorf("拼接文本 = %q", raw)
	}
}

// 复刻工具：缺示例音频/非法扩展名前置拦截；上传+复刻链路 summary.voice 透出。
func TestVoiceCloneTool(t *testing.T) {
	var sampleSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path == pathFiles {
			sampleSeen = strings.Contains(string(raw), "sample.wav")
			_, _ = w.Write([]byte(`{"id":"file-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"voice":"voice_clone_x"}`))
	}))
	defer srv.Close()
	tool := NewVoiceCloneTool("zp")
	tool.client = NewVoiceClient("zp", srv.URL)

	run := func(files map[string]string) (provider.TaskOutput, error) {
		return tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"voice_name": "v1", "input": "试听"},
			Files:  files,
		}, func(int, string, map[string]any) {})
	}
	if _, err := run(nil); err == nil || !strings.Contains(err.Error(), "示例音频") {
		t.Errorf("缺示例音频应报错, got %v", err)
	}
	bad := filepath.Join(t.TempDir(), "s.flac")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(map[string]string{"audio": bad}); err == nil || !strings.Contains(err.Error(), "仅支持 wav / mp3") {
		t.Errorf("flac 应拦截, got %v", err)
	}
	good := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(good, []byte("wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := run(map[string]string{"audio": good})
	if err != nil {
		t.Fatal(err)
	}
	if !sampleSeen || res.Summary["voice"] != "voice_clone_x" {
		t.Errorf("复刻链路不符: sampleSeen=%v summary=%+v", sampleSeen, res.Summary)
	}
}

// 删除工具：voice 必填 + 删除成功 summary。
func TestVoiceDeleteTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"voice":"voice_clone_x"}`))
	}))
	defer srv.Close()
	tool := NewVoiceDeleteTool("zp")
	tool.client = NewVoiceClient("zp", srv.URL)

	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "voice") {
		t.Errorf("缺 voice 应报错, got %v", err)
	}
	res, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"voice": "voice_clone_x"}},
		func(int, string, map[string]any) {})
	if err != nil || res.Summary["status"] != "deleted" {
		t.Errorf("删除不符: %+v err=%v", res.Summary, err)
	}
}

// 超长文本自动分段：按 1024 切段逐段请求（每段 input 均 ≤1024），
// 段间真实 WAV 拼接为单文件，summary 透出 segment_num。
func TestTTSToolSegmented(t *testing.T) {
	var inputs [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		inputs = append(inputs, raw)
		_, _ = w.Write(testWAV(t, 16000, 160)) // 0.01s 真 WAV，可拼接
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewTTSTool("zp", out)
	tool.client = NewTTSClient("zp", srv.URL)

	// 2500 字无标点长串：硬切为 3 段（1024+1024+452）
	text := strings.Repeat("字", 2500)
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": text, "_out": filepath.Join(out, "seg.wav")},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 {
		t.Fatalf("上游请求数 = %d, want 3", len(inputs))
	}
	for i, in := range inputs {
		if n := utf8.RuneCountInString(golangJsonString(in, "input")); n > zhipuTTSMaxChars {
			t.Errorf("第 %d 段 input %d 字符超限", i+1, n)
		}
	}
	if res.Summary["segment_num"] != 3 {
		t.Errorf("segment_num = %v, want 3", res.Summary["segment_num"])
	}
	w, err := provider.ParseWAV(mustRead(t, filepath.Join(out, "seg.wav")))
	if err != nil {
		t.Fatalf("拼接产物不可解析: %v", err)
	}
	if n := w.DataLen(); n != 160*2*3 {
		t.Errorf("拼接 data = %d 字节, want %d", n, 160*2*3)
	}
}

// golangJsonString 从 JSON 请求体里取顶层字符串字段（测试用极简解析）。
func golangJsonString(body []byte, key string) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// testWAV 构造 16bit 单声道 PCM WAV（sampleRate 采样率、frames 帧数）。
func testWAV(t *testing.T, sampleRate, frames int) []byte {
	t.Helper()
	data := make([]byte, 2*frames)
	out := make([]byte, 44+len(data))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(data)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], 1)
	binary.LittleEndian.PutUint32(out[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(data)))
	copy(out[44:], data)
	return out
}

// mp3 全链路：真 mp3 超 28s → ffmpeg 流拷贝分段 → 逐段转写拼接。
// 无 ffmpeg 跳过（可选依赖，与剪辑工具一致）。
func TestASRToolSegmentedMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 未安装，跳过 mp3 分段集成测试")
	}
	oldSecs := zhipuASRMp3SegSecs
	zhipuASRMp3SegSecs = 2
	defer func() { zhipuASRMp3SegSecs = oldSecs }()

	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		_, _ = w.Write([]byte(`{"text":"段"}`))
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewASRTool("zp", out)
	tool.client = NewASRClient("zp", srv.URL)

	// 生成 5s 64kbps mp3（预算 2s → 3 段）
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "long.mp3")
	if err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
		"-c:a", "libmp3lame", "-b:a", "64k", src).Run(); err != nil {
		t.Fatalf("生成测试 mp3 失败: %v", err)
	}
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"_out": filepath.Join(out, "t.txt")},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if count < 2 || count > 4 {
		t.Fatalf("上游请求数 = %d, want 2-4", count)
	}
	if res.Summary["segment_num"] != count {
		t.Errorf("segment_num = %v, want %d", res.Summary["segment_num"], count)
	}
	if res.Summary["duration_ms"] != int64(5000) {
		t.Errorf("duration_ms = %v, want 5000", res.Summary["duration_ms"])
	}
	raw := mustRead(t, filepath.Join(out, "t.txt"))
	if string(raw) != strings.Repeat("段", count) {
		t.Errorf("拼接文本 = %q, want %d 个「段」", raw, count)
	}
}

// ParamSpecs 约束：tts voice 枚举含官方音色、asr 无 model 枚举、Meta 归属。
func TestToolSpecs(t *testing.T) {
	tts := NewTTSTool("", t.TempDir())
	if m := tts.Meta(); m.Provider != "zhipu" || m.Name != "tts" {
		t.Errorf("tts Meta = %+v", tts.Meta())
	}
	found := false
	for _, s := range tts.ParamSpecs() {
		if s.Key == "voice" {
			for _, o := range s.Options {
				if o.Value == DefaultVoice {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("voice 枚举应含默认音色 tongtong")
	}

	asr := NewASRTool("", t.TempDir())
	if m := asr.Meta(); m.Provider != "zhipu" || m.Name != "asr" {
		t.Errorf("asr Meta = %+v", asr.Meta())
	}
	for _, s := range asr.ParamSpecs() {
		if s.Key == "model" {
			t.Error("上游仅 glm-asr-2512 一款模型，ParamSpecs 不应暴露 model 枚举")
		}
	}
}
