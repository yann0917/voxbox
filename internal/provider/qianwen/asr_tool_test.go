package qianwen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// asr 工具全链路：URL 直用 → 提交/轮询（一次 RUNNING 一次 SUCCEEDED）→ 拉 transcription → txt+srt 产物。
func TestASRToolRunURL(t *testing.T) {
	var srv *httptest.Server
	var poll int
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/services/audio/asr/transcription":
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"PENDING"}}`))
		case r.URL.Path == "/api/v1/tasks/tid":
			poll++
			if poll == 1 {
				_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"RUNNING"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"SUCCEEDED",
				"results":[{"transcription_url":"` + srv.URL + `/trans.json"}]}}`))
		default: // /trans.json
			_, _ = w.Write([]byte(`{"transcripts":[{"sentences":[
				{"begin_time":0,"end_time":1500,"text":"你好"},
				{"begin_time":1500,"end_time":62000,"text":"世界"}]}]}`))
		}
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewASRTool("sk-test", out)
	tool.client = NewASRClient("sk-test", srv.URL) // 注入测试地址
	tool.pollInterval = 0                          // 测试免等待
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": srv.URL + "/input.mp3"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 2 {
		t.Fatalf("应产出 txt+srt: %+v", res.Artifacts)
	}
	txtAbs := filepath.Join(out, res.Artifacts[0].Path)
	raw, _ := os.ReadFile(txtAbs)
	if !strings.Contains(string(raw), "你好") || !strings.Contains(string(raw), "世界") {
		t.Errorf("txt 内容不符: %q", raw)
	}
	srtAbs := filepath.Join(out, res.Artifacts[1].Path)
	srt, _ := os.ReadFile(srtAbs)
	if !strings.Contains(string(srt), "00:01:02,000") {
		t.Errorf("srt 时间戳不符: %q", srt)
	}
	// summary.segments 供前端文稿联动
	if segs, ok := res.Summary["segments"].([]map[string]any); !ok || len(segs) != 2 {
		t.Errorf("summary.segments = %v", res.Summary["segments"])
	}
}

// 本地文件（Storage 为 nil）：报「对象存储中转」配置指引。
func TestASRToolLocalFileNeedsStorage(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(audio, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", dir)
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": audio}, // Storage 为 nil
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "对象存储中转") {
		t.Fatalf("应报对象存储中转指引, got %v", err)
	}
}

// 凭证缺失（url 输入直用）：报「千问 API Key」配置指引。
func TestASRToolMissingAPIKey(t *testing.T) {
	tool := NewASRTool("", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": "https://a.b/x.mp3"},
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "千问 API Key") {
		t.Fatalf("应报千问 API Key 指引, got %v", err)
	}
}
