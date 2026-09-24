package zhipu

import (
	"context"
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

// tts 工具：默认音色/产物落盘/_out 重定向/1024 字符上限/凭证哨兵/ParamSpecs。
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
	long := strings.Repeat("字", zhipuTTSMaxChars+1)
	tool.apiKey = "zp"
	_, err = tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": long}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "1024") {
		t.Errorf("超长文本应报 1024 上限, got %v", err)
	}
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
	if err := run(map[string]any{}, map[string]string{"audio": big}); err == nil || !strings.Contains(err.Error(), "25MB") {
		t.Errorf("超 25MB 应拦截, got %v", err)
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
