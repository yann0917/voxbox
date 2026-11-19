package deepseek

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

func TestASRTranscribeWire(t *testing.T) {
	var gotPath, gotAuth, gotFilename, gotContentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if !strings.HasPrefix(gotContentType, "multipart/form-data") {
			gotContentType = r.Header.Get("Content-Type")
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FormName() == "file" {
				gotFilename = p.FileName()
			}
		}
		_, _ = w.Write([]byte(`{"request_id":"req-1","text":"上海今天天气怎么样？","duration_ms":3200}`))
	}))
	defer ts.Close()

	text, ms, err := NewClient("sk-test", ts.URL).Transcribe(context.Background(), mkWAV(24000, 64))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != "/v1/tools/asr" || gotAuth != "Bearer sk-test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if gotContentType == "" || gotFilename != "audio.wav" {
		t.Fatalf("contentType=%q filename=%q", gotContentType, gotFilename)
	}
	if text != "上海今天天气怎么样？" || ms != 3200 {
		t.Fatalf("text=%q ms=%d", text, ms)
	}

	// 空文本响应视为失败
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":"req-2","text":"","duration_ms":0}`))
	}))
	defer ts2.Close()
	if _, _, err := NewClient("k", ts2.URL).Transcribe(context.Background(), mkWAV(24000, 8)); err == nil {
		t.Fatal("空 text 应报错")
	}
}

func TestASRToolDirectWAVSplit(t *testing.T) {
	// 直传条件（16bit PCM + 白名单采样率）下的 8kHz WAV，注入小段预算触发分段：
	// 6 秒 @8kHz mono 16bit = 96KB data → 段预算 64KB → 2 段（4s + 2s）。
	origSeg := asrSegBytes
	asrSegBytes = 64 << 10
	t.Cleanup(func() { asrSegBytes = origSeg })

	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(fmt.Sprintf(`{"request_id":"r%d","text":"第%d段。","duration_ms":3000}`, calls, calls)))
	}))
	defer ts.Close()

	audio := mkWAV(8000, 6*8000*2)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.wav")
	if err := os.WriteFile(src, audio, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewASRTool("sk-test", dir)
	tool.client = NewClient("sk-test", ts.URL)
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("上游调用次数 = %d, want 2（96KB 按 64KB 段预算切两段）", calls)
	}
	if out.Summary["segment_num"] != 2 {
		t.Fatalf("summary = %+v", out.Summary)
	}
	if got := out.Summary["duration_ms"].(int64); got != 6000 {
		t.Fatalf("duration_ms = %d, want 6000（两段响应累加）", got)
	}
	// 拼接文本落盘
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	abs, _ := resolveOut(dir, out.Artifacts[0].Path)
	raw, _ := os.ReadFile(abs)
	if string(raw) != "第1段。第2段。" {
		t.Fatalf("拼接文本 = %q", raw)
	}
}

func TestASRToolNoCredAndMissingInput(t *testing.T) {
	nc := NewASRTool("", t.TempDir())
	_, err := nc.Run(context.Background(), provider.TaskInput{}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "缺少必填参数") {
		t.Fatalf("无输入 err = %v, want 缺少必填参数", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.wav")
	if err := os.WriteFile(src, mkWAV(24000, 16), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("", dir)
	_, err = tool.Run(context.Background(), provider.TaskInput{Files: map[string]string{"audio": src}}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "DeepSeek API Key 未配置") {
		t.Fatalf("err = %v, want ErrNoCred", err)
	}
}
