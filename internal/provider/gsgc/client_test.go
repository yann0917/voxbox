package gsgc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// fastPoll 测试期把轮询压缩到毫秒级（defer 恢复原值）。
func fastPoll(t *testing.T) {
	t.Helper()
	oi, om := pollInterval, pollMax
	pollInterval, pollMax = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pollInterval, pollMax = oi, om })
}

func TestFetchUploadURLAndCreate(t *testing.T) {
	var got struct {
		fileName  string
		createLen int
		body      map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case fetchUploadURLPath:
			got.fileName = r.URL.Query().Get("fileName")
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"upload_url":"http://tos.example/put","input_path_id":"pid-1"}}`))
		case createTaskPath:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got.body)
			got.createLen++
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"task_id":"2100632999412961280","balance":0}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, DefaultModel)
	uploadURL, pid, err := c.FetchUploadURL(context.Background(), "song.mp3")
	if err != nil {
		t.Fatalf("FetchUploadURL() err = %v", err)
	}
	if got.fileName != "song.mp3" {
		t.Errorf("fileName query = %q, want song.mp3", got.fileName)
	}
	if uploadURL != "http://tos.example/put" || pid != "pid-1" {
		t.Errorf("uploadURL/pid = %q/%q", uploadURL, pid)
	}

	t.Run("CreateTask 透传 payload", func(t *testing.T) {
		taskID, err := c.CreateTask(context.Background(), map[string]any{
			"task_type":     "audio_separate",
			"input_path_id": []string{pid},
			"stem":          []string{StemInstrumental, StemVocals},
			"model":         DefaultModel,
		})
		if err != nil {
			t.Fatalf("CreateTask() err = %v", err)
		}
		if taskID != "2100632999412961280" {
			t.Errorf("taskID = %q", taskID)
		}
		if got.body["task_type"] != "audio_separate" {
			t.Errorf("task_type = %v", got.body["task_type"])
		}
		ids, _ := got.body["input_path_id"].([]any)
		if len(ids) != 1 || ids[0] != "pid-1" {
			t.Errorf("input_path_id = %v, want [pid-1]", got.body["input_path_id"])
		}
		if stems := fmtStems(got.body["stem"]); stems != "instrumental,vocals" {
			t.Errorf("stem = %v, want 官方顺序双轨", got.body["stem"])
		}
	})
}

// TestSeparateStemsTransform 分离参数形状转换：stems 枚举 → 官方顺序 stem 数组
// （乱序或缺 model 会让上游静默退化单轨——回归守护）。
func TestSeparateStemsTransform(t *testing.T) {
	cases := []struct{ stems, want string }{
		{"", "instrumental,vocals"},
		{"both", "instrumental,vocals"},
		{"vocals", "vocals"},
		{"instrumental", "instrumental"},
	}
	for _, tc := range cases {
		payload := map[string]any{"task_type": "audio_separate", "stems": tc.stems}
		separateStemsTransform(payload)
		if got := fmtStems(payload["stem"]); got != tc.want {
			t.Errorf("stems=%q → %v, want %q", tc.stems, payload["stem"], tc.want)
		}
		if _, has := payload["stems"]; has {
			t.Errorf("stems 键应被替换为 stem")
		}
	}
}

// TestSeparateSiteFuncParams siteFunc 表里 separate 条目的 Defaults 与参数构建。
func TestSeparateSiteFuncParams(t *testing.T) {
	var fn *siteFunc
	for i := range siteFuncs {
		if siteFuncs[i].Kind == "separate" {
			fn = &siteFuncs[i]
		}
	}
	if fn == nil {
		t.Fatal("siteFuncs 缺少 separate 条目")
	}
	tool := newSiteTool(lineGSGC, *fn, t.TempDir())
	payload, err := tool.buildPayload(map[string]any{"stems": "vocals"})
	if err != nil {
		t.Fatal(err)
	}
	if payload["model"] != DefaultModel {
		t.Errorf("model 缺省 = %v, want %s", payload["model"], DefaultModel)
	}
	if stems := fmtStems(payload["stem"]); stems != "vocals" {
		t.Errorf("stem = %v, want [vocals]", payload["stem"])
	}
	// 用户显式覆盖 model
	payload, err = tool.buildPayload(map[string]any{"stems": "both", "model": "207"})
	if err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "207" {
		t.Errorf("覆写 model = %v, want 207", payload["model"])
	}
	if stems := fmtStems(payload["stem"]); stems != "instrumental,vocals" {
		t.Errorf("stems=both → %v, want 官方顺序双轨", payload["stem"])
	}
}

func fmtStems(v any) string {
	var parts []string
	switch arr := v.(type) {
	case []any:
		for _, s := range arr {
			parts = append(parts, fmt.Sprint(s))
		}
	case []string:
		parts = arr
	}
	return strings.Join(parts, ",")
}

// TestZhuanhuanmaoSeparatePayload 转换猫线路分离参数形态回归守护：与其前端 chunk
// 一致——stem 为字符串（both→"instrumental_vocals"）且 payload 不含 model；
// 与格式工厂线路（stem 数组 + model:"103"）互为镜像但不可混用。
func TestZhuanhuanmaoSeparatePayload(t *testing.T) {
	tool := newSiteTool(lineZHM, zhmSeparateFunc, t.TempDir())
	if got := tool.Meta().Provider; got != "zhuanhuanmao" {
		t.Errorf("Meta().Provider = %q, want zhuanhuanmao", got)
	}
	if got := tool.client.BaseURL(); got != ZHMBaseURL {
		t.Errorf("BaseURL() = %q, want %s", got, ZHMBaseURL)
	}
	cases := []struct{ stems, want string }{
		{"", "instrumental_vocals"},
		{"both", "instrumental_vocals"},
		{"vocals", "vocals"},
		{"instrumental", "instrumental"},
	}
	for _, tc := range cases {
		payload, err := tool.buildPayload(map[string]any{"stems": tc.stems})
		if err != nil {
			t.Fatal(err)
		}
		if payload["stem"] != tc.want {
			t.Errorf("stems=%q → stem=%v, want %q", tc.stems, payload["stem"], tc.want)
		}
		if _, has := payload["model"]; has {
			t.Errorf("stems=%q → 转换猫 payload 不应携带 model（与站点前端一致）", tc.stems)
		}
		if _, has := payload["stems"]; has {
			t.Errorf("stems 键应被替换为 stem")
		}
	}
	// model 不在参数表里：显式传入也不透传（两站形态差异由线路定义锁死）
	payload, err := tool.buildPayload(map[string]any{"stems": "both", "model": "103"})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := payload["model"]; has {
		t.Error("转换猫线路显式 model 也不应透传")
	}
}

// TestNewForSite 站点构造器：裸 host 补 https、去尾斜杠；默认模型固定。
func TestNewForSite(t *testing.T) {
	c := NewForSite("www.zhuanhuanmao.com/")
	if got := c.BaseURL(); got != "https://www.zhuanhuanmao.com" {
		t.Errorf("BaseURL() = %q, want https://www.zhuanhuanmao.com", got)
	}
	if got := NewForSite("").BaseURL(); got != DefaultBaseURL {
		t.Errorf("空地址应回落默认线路, got %q", got)
	}
}

// TestFetchDownloadURLShapes 覆盖两站响应差异：格式工厂新版 info 为对象
// {"stem":...} 且键名 file_size；转换猫旧版 info 为字符串且键名 fileSize。
func TestFetchDownloadURLShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == fetchDownloadPath {
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":[
				{"index":0,"url":"http://x/instrumental.wav","file_size":10,"info":{"stem":"instrumental"}},
				{"index":1,"url":"http://x/vocals.wav","fileSize":"20","info":"vocals"}
			]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	files, err := newWithBaseURL(srv.URL, DefaultModel).FetchDownloadURL(context.Background(), "t1")
	if err != nil {
		t.Fatalf("FetchDownloadURL() err = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if files[0].Stem != "instrumental" || files[0].Size != 10 {
		t.Errorf("files[0] = %+v, want instrumental/10", files[0])
	}
	if files[1].Stem != "vocals" || files[1].Size != 20 {
		t.Errorf("files[1] = %+v, want vocals/20（字符串 info + fileSize 键 + 字符串数字）", files[1])
	}
}

// TestEnvelopeErrors 覆盖两种错误文案形态：message（参数错误）与 status（"系统异常"）。
func TestEnvelopeErrors(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"code":400,"status":"error","message":"task_id参数必填"}`, "task_id参数必填"},
		{`{"code":-1,"status":"系统异常","data":null}`, "系统异常"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := newWithBaseURL(srv.URL, DefaultModel).BatchGet(context.Background(), "t1")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("BatchGet() err = %v, want contains %q", err, tc.want)
		}
	}
}

// TestBatchGetFloatProgress 回归：上游 progress 为百分比浮点（真实事故 payload），
// 严格 int 解析曾让整次轮询报「解析任务状态响应失败」。
func TestBatchGetFloatProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == batchGetPath {
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"tasks":[
				{"task_id":"t1","status":"running","progress":26.08695652173913,"consume_duration":30.0408,"queue_tasks":0}
			]}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tasks, err := newWithBaseURL(srv.URL, DefaultModel).BatchGet(context.Background(), "t1")
	if err != nil {
		t.Fatalf("BatchGet() err = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "running" || tasks[0].Progress < 26.08 || tasks[0].Progress > 26.09 {
		t.Errorf("tasks = %+v, want 1 条 running 且 progress≈26.087", tasks)
	}
}

// TestSeparateToolRun 通用 siteTool 全链路（以 separate 表项为例）：
// URL 输入 → 下载 → 直传 → 创建 → 轮询（running×2 → completed）→ 双轨产物落盘。
func TestSeparateToolRun(t *testing.T) {
	fastPoll(t)
	const audio = "RIFF_WAVE"

	var uploads [][]byte
	polls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case fetchUploadURLPath:
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"upload_url":"` + srv.URL + `/tos-put?f=a.mp3","input_path_id":"pid-1"}}`))
		case "/tos-put":
			b, _ := io.ReadAll(r.Body)
			uploads = append(uploads, b)
			w.WriteHeader(http.StatusOK)
		case createTaskPath:
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			if body["model"] != DefaultModel {
				t.Errorf("create payload model = %v, want %s", body["model"], DefaultModel)
			}
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"task_id":"task-1","balance":0}}`))
		case batchGetPath:
			polls++
			if polls < 3 {
				_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"tasks":[{"task_id":"task-1","status":"running","progress":0,"queue_tasks":0}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"tasks":[{"task_id":"task-1","status":"completed","progress":100,"consume_duration":30.04,"queue_tasks":0}]}}`))
		case fetchDownloadPath:
			_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":[
				{"index":0,"url":"` + srv.URL + `/dl/vocals.wav","file_size":4,"info":{"stem":"vocals"}},
				{"index":1,"url":"` + srv.URL + `/dl/instrumental.wav","file_size":4,"info":{"stem":"instrumental"}}
			]}`))
		case "/dl/input.mp3":
			_, _ = w.Write([]byte(audio))
		default:
			if strings.HasPrefix(r.URL.Path, "/dl/") {
				_, _ = w.Write([]byte("WAVE"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	var fn siteFunc
	for i := range siteFuncs {
		if siteFuncs[i].Kind == "separate" {
			fn = siteFuncs[i]
		}
	}
	tool := newSiteTool(lineGSGC, fn, outDir)
	tool.client = newWithBaseURL(srv.URL, DefaultModel)
	// 后处理 stub：ffmpeg 不可用 → 保留 WAV，专注验证分离链路本身
	ffmpegLookPath = func() error { return fmt.Errorf("stub: no ffmpeg") }
	t.Cleanup(func() { ffmpegLookPath = func() error { _, err := exec.LookPath("ffmpeg"); return err } })

	var notes []string
	prog := func(p int, note string, _ map[string]any) { notes = append(notes, note) }

	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": srv.URL + "/dl/input.mp3", "stems": "both"},
	}, prog)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(uploads) != 1 || string(uploads[0]) != audio {
		t.Fatalf("输入文件未直传（uploads=%d）", len(uploads))
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("产物数量 = %d, want 2", len(out.Artifacts))
	}
	tracks := map[string]bool{}
	for _, a := range out.Artifacts {
		if a.Kind != "audio" || a.Format != "wav" {
			t.Errorf("产物字段异常: %+v", a)
		}
		track, _ := a.Meta["track"].(string)
		tracks[track] = true
		if _, err := os.Stat(filepath.Join(outDir, a.Path)); err != nil {
			t.Errorf("产物文件未落盘: %v", err)
		}
	}
	if !tracks["vocals"] || !tracks["instrumental"] {
		t.Errorf("轨道键 = %v, want vocals+instrumental", tracks)
	}
	if out.Summary["task_id"] != "task-1" || out.Summary["task_type"] != "audio_separate" {
		t.Errorf("summary = %v", out.Summary)
	}
}

// TestEnsureStemMP3 分离产物后处理集成：WAV → 标准 MP3 128k；已是 MP3 原样保留；
// 垃圾数据不盲转。ffmpeg 缺失的机器自动跳过。
func TestEnsureStemMP3(t *testing.T) {
	if err := ffmpegLookPath(); err != nil {
		t.Skip("本机无 ffmpeg/ffprobe，跳过")
	}
	ctx := context.Background()

	// 用 ffmpeg 生成 1 秒正弦波 WAV 作为站点产物样本
	wav := t.TempDir() + "/src.wav"
	if out, gerr := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=1", "-ac", "2", wav).CombinedOutput(); gerr != nil {
		t.Fatalf("生成样本 WAV 失败: %v: %s", gerr, out)
	}

	final, format, transcoded, bitrate, warn := ensureStemMP3(ctx, wav)
	if warn != "" {
		t.Fatalf("WAV 转码不应有警告: %s", warn)
	}
	if !transcoded || format != "mp3" || strings.HasSuffix(final, ".wav") {
		t.Fatalf("transcoded=%v format=%s final=%s, want mp3", transcoded, format, final)
	}
	if bitrate <= 0 {
		t.Errorf("源码率未记录: %d", bitrate)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("MP3 产物未落盘: %v", err)
	}
	if _, err := os.Stat(wav); !os.IsNotExist(err) {
		t.Errorf("源 WAV 应已删除")
	}

	// 已是 MP3：原样保留
	final2, format2, transcoded2, _, warn2 := ensureStemMP3(ctx, final)
	if transcoded2 || warn2 != "" || format2 != "mp3" || final2 != final {
		t.Fatalf("MP3 输入应原样保留: transcoded=%v format=%s warn=%q", transcoded2, format2, warn2)
	}

	// 垃圾数据：不盲转，保留原样并给出警告
	junk := t.TempDir() + "/junk.wav"
	_ = os.WriteFile(junk, []byte("not audio"), 0o644)
	_, fmtGot, transJunk, _, warnJunk := ensureStemMP3(ctx, junk)
	if transJunk {
		t.Error("垃圾数据不应触发转码")
	}
	if fmtGot != "wav" || warnJunk == "" {
		t.Errorf("垃圾数据应保留 wav 并有警告: fmt=%s warn=%q", fmtGot, warnJunk)
	}
}
