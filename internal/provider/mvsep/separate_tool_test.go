package mvsep

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// fastPoll 测试期把轮询与总超时压缩到毫秒级（ defer 恢复原值）。
func fastPoll(t *testing.T) {
	t.Helper()
	oi, om, ot := pollInterval, pollMax, toolTimeout
	pollInterval, pollMax, toolTimeout = 5*time.Millisecond, 10*time.Millisecond, 3*time.Second
	t.Cleanup(func() { pollInterval, pollMax, toolTimeout = oi, om, ot })
}

// nopReport 测试用进度回调（丢弃）。
func nopReport(int, string, map[string]any) {}

// sepScript 按轮询次数 scripted 响应 separation/get。
type sepScript struct {
	mu      sync.Mutex
	polls   int
	creates int
	getN    int
	resp    []string // 每次轮询返回的 body；超出后重复最后一个
	cancels []string
}

func (s *sepScript) getBody() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.getN
	if i >= len(s.resp) {
		i = len(s.resp) - 1
	}
	s.getN++
	return s.resp[i]
}

func newScriptServer(t *testing.T, script *sepScript, audio []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/separation/create"):
			script.mu.Lock()
			script.creates++
			script.mu.Unlock()
			_ = r.ParseMultipartForm(32 << 20)
			if fss := r.MultipartForm.File["audiofile"]; len(fss) > 0 && audio != nil {
				script.mu.Lock()
				_ = fss // 文件内容断言在各自用例内做（此处只求不 panic）
				script.mu.Unlock()
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"hash":"h-test.mp3"}}`))
		case strings.HasSuffix(r.URL.Path, "/separation/get"):
			_, _ = w.Write([]byte(script.getBody()))
		case strings.HasSuffix(r.URL.Path, "/separation/cancel"):
			script.mu.Lock()
			script.cancels = append(script.cancels, r.PostFormValue("hash"))
			script.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true}`))
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			_, _ = w.Write(audio)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func statuses(waits int, done string) []string {
	out := make([]string, 0, waits+1)
	for i := 0; i < waits; i++ {
		out = append(out, `{"success":true,"status":"waiting","data":{"queue_count":9,"current_order":2}}`)
	}
	out = append(out, done)
	return out
}

const doneBody = `{"success":true,"status":"done","data":{"algorithm":"BS Roformer","output_format":"wav",
 "files":[{"type":"Vocals","url":"URLBASE/dl/vocals.wav","bytes":4,"size":"4 B"},
          {"type":"Other","url":"URLBASE/dl/other.wav","bytes":4,"size":"4 B"}]}}`

func TestSeparateToolRun(t *testing.T) {
	fastPoll(t)
	script := &sepScript{}
	srv := newScriptServer(t, script, []byte("WAVE"))
	done := strings.ReplaceAll(doneBody, "URLBASE", srv.URL)
	script.resp = statuses(2, done)
	defer srv.Close()

	outDir := t.TempDir()
	tool := NewSeparateTool("tok-1", srv.URL, outDir)

	var notes []string
	prog := func(p int, note string, _ map[string]any) { notes = append(notes, note) }

	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"sep_type": "26", "output_format": "0", "url": srv.URL + "/dl/input.mp3"},
	}, prog)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("产物数量 = %d, want 2", len(out.Artifacts))
	}
	a := out.Artifacts[0]
	if a.Kind != "audio" || a.Format != "wav" || a.Meta["track"] != "vocals" {
		t.Errorf("首个产物错误: %+v", a)
	}
	if !strings.Contains(a.Path, "separate") || filepath.IsAbs(a.Path) {
		t.Errorf("产物路径应为 data 目录相对路径: %q", a.Path)
	}
	data, err := os.ReadFile(filepath.Join(outDir, a.Path))
	if err != nil || string(data) != "WAVE" {
		t.Errorf("产物落盘内容错误: %q err=%v", data, err)
	}
	if out.Summary["algorithm"] != "BS Roformer" {
		t.Errorf("summary = %v", out.Summary)
	}
}

// withReplacedBody 已移除：doneBody 的链接在用例内直接替换为 mock 地址。

func TestSeparateToolInputErrors(t *testing.T) {
	fastPoll(t)
	script := &sepScript{}
	srv := newScriptServer(t, script, nil)
	defer srv.Close()
	tool := NewSeparateTool("tok-1", srv.URL, t.TempDir())

	cases := []struct {
		name   string
		params map[string]any
		input  provider.TaskInput
		want   string
	}{
		{"缺输入", map[string]any{"sep_type": "26"}, provider.TaskInput{Params: map[string]any{}}, "缺少输入"},
		{"URL 非法", map[string]any{"sep_type": "26", "url": "ftp://x"}, provider.TaskInput{Params: map[string]any{}}, "http(s)"},
		{"sep_type 非法", map[string]any{"sep_type": "abc"}, provider.TaskInput{Params: map[string]any{}}, "sep_type"},
		{"output_format 非法", map[string]any{"sep_type": "26", "output_format": "9"}, provider.TaskInput{Params: map[string]any{}}, "output_format"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.input
			if in.Params == nil {
				in.Params = c.params
			} else {
				for k, v := range c.params {
					in.Params[k] = v
				}
			}
			_, err := tool.Run(context.Background(), in, func(int, string, map[string]any) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
		})
	}
}

func TestSeparateToolNoToken(t *testing.T) {
	tool := NewSeparateTool("", "", t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"sep_type": "26"},
		Files:  map[string]string{"audio": "whatever"},
	}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "mvsep.api_token") {
		t.Fatalf("err = %v, want 凭证配置指引", err)
	}
}

func TestSeparateToolLocalFileAndOpts(t *testing.T) {
	fastPoll(t)
	var mu sync.Mutex
	var createForm map[string]string
	script := &sepScript{}
	srv := newScriptServer(t, script, []byte("WAVE"))
	// 包装 create 处理以捕获表单
	base := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/separation/create") {
			_ = r.ParseMultipartForm(32 << 20)
			mu.Lock()
			createForm = map[string]string{}
			for k, vs := range r.MultipartForm.Value {
				if len(vs) > 0 {
					createForm[k] = vs[0]
				}
			}
			mu.Unlock()
		}
		base.ServeHTTP(w, r)
	})
	done := strings.ReplaceAll(doneBody, "URLBASE", srv.URL)
	script.resp = statuses(0, done)
	defer srv.Close()

	src := filepath.Join(t.TempDir(), "in.mp3")
	if err := os.WriteFile(src, []byte("AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewSeparateTool("tok-1", srv.URL, t.TempDir())
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"sep_type": "26", "add_opt1": "0", "add_opt2": "7", "add_opt3": ""},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("产物数量 = %d", len(out.Artifacts))
	}
	mu.Lock()
	defer mu.Unlock()
	if createForm["sep_type"] != "26" || createForm["add_opt1"] != "0" || createForm["add_opt2"] != "7" {
		t.Errorf("表单错误: %v", createForm)
	}
	if _, ok := createForm["add_opt3"]; ok {
		t.Errorf("空 add_opt3 不应提交")
	}
}

func TestSeparateToolCancelOnContextCancel(t *testing.T) {
	fastPoll(t)
	script := &sepScript{resp: statuses(10000, `{"success":true,"status":"processing"}`)}
	srv := newScriptServer(t, script, nil)
	defer srv.Close()

	src := filepath.Join(t.TempDir(), "in.mp3")
	_ = os.WriteFile(src, []byte("A"), 0o644)
	tool := NewSeparateTool("tok-1", srv.URL, t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := tool.Run(ctx, provider.TaskInput{
		Params: map[string]any{"sep_type": "26"}, Files: map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err == nil {
		t.Fatal("取消后 Run 应返回错误")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		script.mu.Lock()
		n := len(script.cancels)
		script.mu.Unlock()
		if n == 1 {
			return // 上游取消已触发
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ctx 取消后未 best-effort 调用上游 cancel")
}

func TestParseSepTypeAndFormat(t *testing.T) {
	if n, err := parseSepType("26"); err != nil || n != 26 {
		t.Errorf("parseSepType(\"26\") = %d, %v", n, err)
	}
	if n, err := parseSepType(json.Number("26")); err != nil || n != 26 {
		t.Errorf("parseSepType(json.Number) = %d, %v", n, err)
	}
	if _, err := parseSepType("0"); err == nil {
		t.Errorf("sep_type=0 应报错")
	}
	if n, _ := parseOutputFormat(""); n != 0 {
		t.Errorf("output_format 空 = %d, want 0", n)
	}
	if _, err := parseOutputFormat("-1"); err == nil {
		t.Errorf("output_format=-1 应报错")
	}
}

// fakeCacheBucket 假对象存储：Put 存内存、PresignGet 返回本地取回 URL（分离缓存专用）。
type fakeCacheBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
	srv     *httptest.Server
}

func newFakeCacheBucket(t *testing.T) *fakeCacheBucket {
	t.Helper()
	b := &fakeCacheBucket{objects: map[string][]byte{}}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := url.QueryUnescape(r.URL.Query().Get("key"))
		if r.Method == http.MethodPut {
			data, _ := io.ReadAll(r.Body)
			b.mu.Lock()
			b.objects[key] = data
			b.mu.Unlock()
			w.WriteHeader(200)
			return
		}
		b.mu.Lock()
		data, ok := b.objects[key]
		b.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeCacheBucket) Put(_ context.Context, key, _ string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.objects[key] = data
	b.mu.Unlock()
	return nil
}

func (b *fakeCacheBucket) PresignGet(key string, _ time.Duration) (string, error) {
	return b.srv.URL + "/fetch?key=" + url.QueryEscape(key), nil
}

// TestSeparateToolConvertingStatus converting（服务端格式转换/音轨初始化）是正常
// 过渡态而非错误：轮询期间不报"未知状态"，最终照常出产物。
func TestSeparateToolConvertingStatus(t *testing.T) {
	fastPoll(t)
	script := &sepScript{}
	srv := newScriptServer(t, script, []byte("WAVE"))
	done := strings.ReplaceAll(doneBody, "URLBASE", srv.URL)
	script.resp = []string{
		`{"success":true,"status":"converting","data":{}}`,
		`{"success":true,"status":"converting","data":{}}`,
		done,
	}

	tool := NewSeparateTool("tok-1", srv.URL, t.TempDir())
	var notes []string
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"sep_type": "48", "output_format": "0", "url": srv.URL + "/dl/input.mp3"},
	}, func(_ int, note string, _ map[string]any) { notes = append(notes, note) })
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("产物 = %d, want 2", len(out.Artifacts))
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "converting") {
		t.Errorf("进度应包含 converting 提示: %v", notes)
	}
}
