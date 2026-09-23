package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/provider"
)

// newASRToolWithMockWS 构造带 mock WS 服务端的 ASRTool（auc 指向不可达地址，URL 模式测试另行注入）。
func newASRToolWithMockWS(t *testing.T, audio []byte, format string) *ASRTool {
	t.Helper()
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		serveAckThenCollect(t, conn, audio, format, 32*1024)
	})
	cred := SpeechCred{APIKey: "key-1"}
	return &ASRTool{
		ws:     NewASRClientWithURL(cred, wsURL),
		auc:    NewASRAUCClientWithBaseURL(cred, "http://127.0.0.1:1"),
		cred:   cred,
		outDir: t.TempDir(),
	}
}

func writeTestAudio(t *testing.T, dir, name string, audio []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestASRToolFileMode(t *testing.T) {
	testAudio := make([]byte, 100*1024) // 4 片：32KB×3 + 4KB 尾片
	for i := range testAudio {
		testAudio[i] = byte(i % 251)
	}
	tool := newASRToolWithMockWS(t, testAudio, "mp3")
	audioFile := writeTestAudio(t, t.TempDir(), "sample.mp3", testAudio)

	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files:  map[string]string{"audio": audioFile},
		Params: map[string]any{},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts 共 %d 个, 期望 2: %+v", len(out.Artifacts), out.Artifacts)
	}
	txtArt, srtArt := out.Artifacts[0], out.Artifacts[1]
	if txtArt.Kind != "transcript" || txtArt.Format != "txt" || !strings.HasPrefix(txtArt.Path, "asr/") {
		t.Errorf("artifacts[0] = %+v", txtArt)
	}
	if srtArt.Kind != "subtitle" || srtArt.Format != "srt" || !strings.HasSuffix(srtArt.Path, ".srt") {
		t.Errorf("artifacts[1] = %+v", srtArt)
	}
	txt, err := os.ReadFile(filepath.Join(tool.outDir, txtArt.Path))
	if err != nil {
		t.Fatal(err)
	}
	if string(txt) != "这是字节跳动，今日头条母公司。" {
		t.Errorf("txt = %q", txt)
	}
	srt, err := os.ReadFile(filepath.Join(tool.outDir, srtArt.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(srt), "00:00:00,000 --> 00:00:01,705") {
		t.Errorf("srt 缺少时间轴: %s", srt)
	}
	segs, _ := out.Summary["segments"].([]map[string]any)
	if len(segs) != 2 {
		t.Errorf("summary.segments = %v", out.Summary["segments"])
	}
	if out.Summary["source"] != "file" || out.Summary["duration_ms"] != int64(3696) {
		t.Errorf("summary = %v", out.Summary)
	}
}

func TestASRToolURLMode(t *testing.T) {
	// 缩短轮询节奏，避免测试等待真实退避间隔。
	oldInterval, oldMax, oldTimeout := asrPollInterval, asrPollMax, asrPollTimeout
	asrPollInterval, asrPollMax, asrPollTimeout = 10*time.Millisecond, 20*time.Millisecond, 5*time.Second
	defer func() { asrPollInterval, asrPollMax, asrPollTimeout = oldInterval, oldMax, oldTimeout }()

	var mu sync.Mutex
	queries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", asrAUCCodeOK)
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == asrAUCSubmitPath {
			return // submit ack：空 body
		}
		mu.Lock()
		queries++
		body := map[string]any{"id": "task-1", "status": "Running"}
		if queries >= 2 {
			body = map[string]any{
				"id": "task-1", "status": "Completed",
				"result": map[string]any{
					"text": "你好世界",
					"utterances": []any{
						map[string]any{"text": "你好", "start_time": 0, "end_time": 1000},
						map[string]any{"text": "世界", "start_time": 1000, "end_time": 2000},
					},
				},
				"audio_info": map[string]any{"duration": 2000},
			}
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	cred := SpeechCred{APIKey: "key-1"}
	tool := &ASRTool{
		ws:     NewASRClientWithURL(cred, "ws://127.0.0.1:1"),
		auc:    NewASRAUCClientWithBaseURL(cred, srv.URL),
		cred:   cred,
		outDir: t.TempDir(),
	}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": "https://example.com/audio.mp3"},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if queries < 2 {
		t.Errorf("query 次数 = %d, 期望至少 2 次（Running → Completed）", queries)
	}
	if len(out.Artifacts) != 2 || out.Artifacts[0].Kind != "transcript" || out.Artifacts[1].Kind != "subtitle" {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	if out.Summary["source"] != "url" || out.Summary["duration_ms"] != int64(2000) {
		t.Errorf("summary = %v", out.Summary)
	}
}

func TestASRToolNoInput(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "缺少输入") {
		t.Fatalf("err = %v, 期望包含「缺少输入」", err)
	}
}

func TestASRToolVersionInputConstraints(t *testing.T) {
	// 40KB = 32KB 首片 + 8KB 末片，满足 mock 对首包正 seq / 末包负 seq 的协议断言
	testAudio := make([]byte, 40*1024)
	for i := range testAudio {
		testAudio[i] = byte(i % 251)
	}
	tool := newASRToolWithMockWS(t, testAudio, "mp3")

	// 一句话版只收本地文件，URL 输入报参数错误
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": "https://example.com/a.mp3", "version": "sentence"},
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "一句话识别仅支持本地上传") {
		t.Fatalf("err = %v, 期望一句话版拒绝 URL 输入", err)
	}

	// 旧参数组合 standard+本地文件（无对象存储）→ 自动按一句话识别走 WS（历史任务重跑兼容）
	audioFile := writeTestAudio(t, t.TempDir(), "legacy.mp3", testAudio)
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files:  map[string]string{"audio": audioFile},
		Params: map[string]any{"version": "standard"},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if out.Summary["version"] != "sentence" {
		t.Errorf("standard+本地文件应自动按一句话识别处理，summary.version = %v", out.Summary["version"])
	}
}

// TestASRToolStandardBridgesFileViaStorage 标准版+本地文件+对象存储：转存后走真标准版异步
// （不再降级一句话），提交上游的 URL 为签名地址，summary 标记 version=standard/source=url。
func TestASRToolStandardBridgesFileViaStorage(t *testing.T) {
	var mu sync.Mutex
	var audioURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AudioURL string `json:"audio_url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if body.AudioURL != "" { // query 请求体只有 id，不能覆盖 submit 捕获的 URL
			audioURL = body.AudioURL
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Api-Status-Code", "20000000")
		// submit 与 query 共用此 handler：query 侧以 body status=Completed 直达终态（防轮询超时）
		w.Write([]byte(`{"status":"Completed","result":{"text":"你好"},"audio_info":{"duration":1500}}`))
	}))
	t.Cleanup(srv.Close)

	cred := SpeechCred{APIKey: "key-1"}
	tool := &ASRTool{
		ws:     NewASRClientWithURL(cred, "ws://127.0.0.1:1"),
		auc:    NewASRAUCClientWithBaseURL(cred, srv.URL),
		cred:   cred,
		outDir: t.TempDir(),
	}
	audioFile := writeTestAudio(t, t.TempDir(), "long.wav", []byte("audio"))
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files:   map[string]string{"audio": audioFile},
		Params:  map[string]any{"version": "standard"},
		Storage: &fakeStorage{},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if !strings.HasPrefix(audioURL, "https://bucket.tos-cn-beijing.volces.com/") ||
		!strings.HasSuffix(strings.Split(audioURL, "?")[0], ".wav") {
		t.Fatalf("提交上游的 URL = %q, 期望为转存签名 URL", audioURL)
	}
	if out.Summary["version"] != "standard" || out.Summary["source"] != "url" {
		t.Fatalf("summary = %v, 期望 version=standard source=url", out.Summary)
	}
}

// TestASRToolStandardRejectsBadFormatFile 标准版与闲时/极速版共用格式白名单
// （wav/mp3/ogg/spx/amr/aac/m4a），白名单外的扩展名转存前拦截。
func TestASRToolStandardRejectsBadFormatFile(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	audioFile := writeTestAudio(t, t.TempDir(), "song.flac", []byte("audio"))
	st := &fakeStorage{}
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files:   map[string]string{"audio": audioFile},
		Params:  map[string]any{"version": "standard"},
		Storage: st,
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "标准") {
		t.Fatalf("err = %v, 期望标准版格式白名单拦截", err)
	}
	if len(st.keys) != 0 {
		t.Fatalf("格式不合规不应触发转存, keys = %v", st.keys)
	}
}

func TestASRToolIdleMode(t *testing.T) {
	oldInterval, oldMax, oldTimeout := asrIdlePollInterval, asrIdlePollMax, asrIdlePollTimeout
	asrIdlePollInterval, asrIdlePollMax, asrIdlePollTimeout = 5*time.Millisecond, 10*time.Millisecond, 3*time.Second
	defer func() { asrIdlePollInterval, asrIdlePollMax, asrIdlePollTimeout = oldInterval, oldMax, oldTimeout }()

	var mu sync.Mutex
	queries := 0
	var submitHeaders map[string]string
	var submitBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("X-Api-Status-Code", asrAUCCodeOK)
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case asrIdleSubmitPath:
			submitHeaders = map[string]string{
				"X-Api-Resource-Id": r.Header.Get("X-Api-Resource-Id"),
				"X-Api-Sequence":    r.Header.Get("X-Api-Sequence"),
			}
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			submitBody = m
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "idle-task-1"})
		case asrIdleQueryPath:
			// 查询必须以 submit 返回的任务 ID 回传 X-Api-Request-Id。
			if r.Header.Get("X-Api-Request-Id") != "idle-task-1" {
				w.Header().Set("X-Api-Status-Code", "45000201")
				return
			}
			queries++
			if queries == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"text": "你好世界",
					"utterances": []any{
						map[string]any{"text": "你好", "start_time": 0, "end_time": 1000},
					},
				},
				"audio_info": map[string]any{"duration": 1000},
			})
		default:
			t.Errorf("闲时模式不应请求 %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	cred := SpeechCred{APIKey: "key-1"}
	tool := &ASRTool{
		ws:     NewASRClientWithURL(cred, "ws://127.0.0.1:1"),
		auc:    NewASRAUCClientWithBaseURL(cred, srv.URL),
		cred:   cred,
		outDir: t.TempDir(),
	}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"url":      "https://example.com/podcast.MP3?sig=x",
			"version":  "idle",
			"language": "zh-CN",
			"hotwords": "火山引擎, 语音合成",
		},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if queries < 2 {
		t.Errorf("query 次数 = %d, 期望至少 2 次（中间态 → Completed）", queries)
	}
	if submitHeaders["X-Api-Resource-Id"] != asrIdleResourceID || submitHeaders["X-Api-Sequence"] != "-1" {
		t.Errorf("submit headers = %v", submitHeaders)
	}
	audio, _ := submitBody["audio"].(map[string]any)
	if audio == nil || audio["url"] != "https://example.com/podcast.MP3?sig=x" ||
		audio["format"] != "mp3" || audio["language"] != "zh-CN" {
		t.Errorf("submit audio = %v（大写扩展名应归一为小写 format）", submitBody["audio"])
	}
	req, _ := submitBody["request"].(map[string]any)
	if req == nil || req["model_name"] != "bigmodel" || req["show_utterances"] != true {
		t.Errorf("submit request = %v", submitBody["request"])
	}
	corpus, _ := req["corpus"].(map[string]any)
	if corpus == nil || !strings.Contains(fmt.Sprint(corpus["context"]), "火山引擎") {
		t.Errorf("热词应打包进 corpus.context: %v", req)
	}
	if out.Summary["version"] != "idle" || out.Summary["source"] != "url" || out.Summary["duration_ms"] != int64(1000) {
		t.Errorf("summary = %v", out.Summary)
	}
	if len(out.Artifacts) != 2 || out.Artifacts[0].Kind != "transcript" || out.Artifacts[1].Kind != "subtitle" {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
}

func TestASRToolFlashMode(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != asrFlashPath {
			t.Errorf("极速模式不应请求 %s", r.URL.Path)
		}
		calls++
		w.Header().Set("X-Api-Status-Code", asrAUCCodeOK)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id": "flash-1",
			"result": map[string]any{
				"text": "极速结果",
				"utterances": []any{
					map[string]any{"text": "极速", "start_time": 0, "end_time": 500},
				},
			},
			"audio_info": map[string]any{"duration": 1500},
		})
	}))
	t.Cleanup(srv.Close)

	cred := SpeechCred{APIKey: "key-1"}
	tool := &ASRTool{
		ws:     NewASRClientWithURL(cred, "ws://127.0.0.1:1"),
		auc:    NewASRAUCClientWithBaseURL(cred, srv.URL),
		cred:   cred,
		outDir: t.TempDir(),
	}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"url":     "https://example.com/note.m4a",
			"version": "flash",
		},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if calls != 1 {
		t.Errorf("极速版为同步接口, 请求次数 = %d, 期望 1", calls)
	}
	if out.Summary["version"] != "flash" || out.Summary["duration_ms"] != int64(1500) {
		t.Errorf("summary = %v", out.Summary)
	}
	if len(out.Artifacts) != 2 || out.Artifacts[0].Kind != "transcript" {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
}

func TestASRToolIdleFlashRejectFileWithoutStorage(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	audioFile := writeTestAudio(t, t.TempDir(), "sample.mp3", []byte("audio"))
	for _, version := range []string{"idle", "flash"} {
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Files:  map[string]string{"audio": audioFile},
			Params: map[string]any{"version": version},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "对象存储") {
			t.Fatalf("version=%s err = %v, 期望提示配置对象存储", version, err)
		}
	}
}

// fakeStorage 桥接测试用假存储客户端：记录 Put 的 key/size，PresignGet 返回可断言的固定域名 URL。
type fakeStorage struct {
	mu       sync.Mutex
	keys     []string
	sizes    []int64
	presignT time.Duration
}

func (f *fakeStorage) Put(_ context.Context, key, _ string, _ io.Reader, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys, f.sizes = append(f.keys, key), append(f.sizes, size)
	return nil
}

func (f *fakeStorage) PresignGet(key string, ttl time.Duration) (string, error) {
	f.presignT = ttl
	return "https://bucket.tos-cn-beijing.volces.com/" + key + "?X-Tos-Algorithm=fake", nil
}

func TestASRToolIdleFlashBridgesFileViaStorage(t *testing.T) {
	var mu sync.Mutex
	var audioURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Audio struct {
				URL    string `json:"url"`
				Format string `json:"format"`
			} `json:"audio"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		audioURL = body.Audio.URL
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":{"text":"你好"},"audio_info":{"duration":1500}}`)
	}))
	t.Cleanup(srv.Close)

	cred := SpeechCred{APIKey: "key-1"}
	tool := &ASRTool{
		ws:     NewASRClientWithURL(cred, "ws://127.0.0.1:1"),
		auc:    NewASRAUCClientWithBaseURL(cred, srv.URL),
		cred:   cred,
		outDir: t.TempDir(),
	}
	audioFile := writeTestAudio(t, t.TempDir(), "sample.mp3", []byte("audio"))
	st := &fakeStorage{}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Files:   map[string]string{"audio": audioFile},
		Params:  map[string]any{"version": "flash"},
		Storage: st,
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(st.keys) != 1 || !strings.HasSuffix(st.keys[0], ".mp3") {
		t.Fatalf("转存 key = %v, 期望保留 .mp3 扩展名", st.keys)
	}
	if !strings.HasPrefix(audioURL, "https://bucket.tos-cn-beijing.volces.com/") ||
		!strings.HasSuffix(strings.Split(audioURL, "?")[0], ".mp3") {
		t.Fatalf("提交上游的 URL = %q, 期望为转存签名 URL", audioURL)
	}
	if out.Summary["version"] != "flash" || out.Summary["source"] != "url" {
		t.Fatalf("summary = %v", out.Summary)
	}
}

func TestASRToolIdleFlashRejectsOversizeFileBeforeUpload(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	audioFile := writeTestAudio(t, t.TempDir(), "huge.mp3", []byte("audio"))
	if err := os.WriteFile(audioFile, make([]byte, 101<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &fakeStorage{}
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files:   map[string]string{"audio": audioFile},
		Params:  map[string]any{"version": "flash"},
		Storage: st,
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "100MB") {
		t.Fatalf("err = %v, 期望转存前拦截超限", err)
	}
	if len(st.keys) != 0 {
		t.Fatalf("超限文件不应触发转存, keys = %v", st.keys)
	}
}

func TestASRToolIdleFlashURLFormatRequired(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	for _, tc := range []struct{ name, url string }{
		{"无扩展名", "https://example.com/audio"},
		{"不支持的扩展名", "https://example.com/audio.flac"},
	} {
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": tc.url, "version": "idle"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "仅支持") {
			t.Fatalf("%s: err = %v, 期望包含「仅支持」", tc.name, err)
		}
	}
}

func TestASRToolBadFormat(t *testing.T) {
	tool := newASRToolWithMockWS(t, []byte("audio"), "mp3")
	// m4a 已在官方 format 白名单内；flac 不在，应报「暂不支持」
	audioFile := writeTestAudio(t, t.TempDir(), "song.flac", []byte("fake-flac"))
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": audioFile},
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "暂不支持") {
		t.Fatalf("err = %v, 期望包含「暂不支持」", err)
	}
}

// TestASRToolSentenceAcceptsExtendedFormats 一句话版格式白名单对齐官方文档：
// wav/mp3/ogg/pcm/spx/amr/aac/m4a 八种均可用。
func TestASRToolSentenceAcceptsExtendedFormats(t *testing.T) {
	testAudio := make([]byte, 100*1024) // ≥2 片：mock 断言首个音频包 seq 为正
	for i := range testAudio {
		testAudio[i] = byte(i % 251)
	}
	for _, ext := range []string{"wav", "mp3", "ogg", "pcm", "spx", "amr", "aac", "m4a"} {
		t.Run(ext, func(t *testing.T) {
			tool := newASRToolWithMockWS(t, testAudio, ext)
			audioFile := writeTestAudio(t, t.TempDir(), "sample."+ext, testAudio)
			out, err := tool.Run(context.Background(), provider.TaskInput{
				Files: map[string]string{"audio": audioFile},
			}, nopReport)
			if err != nil {
				t.Fatalf("ext=%s Run() err = %v", ext, err)
			}
			if len(out.Artifacts) == 0 {
				t.Fatalf("ext=%s 未产出产物", ext)
			}
		})
	}
}

func TestASRToolOutRedirect(t *testing.T) {
	testAudio := make([]byte, 100*1024)
	for i := range testAudio {
		testAudio[i] = byte(i % 251)
	}
	tool := newASRToolWithMockWS(t, testAudio, "mp3")
	audioFile := writeTestAudio(t, t.TempDir(), "sample.mp3", testAudio)
	in := func(outParam string) provider.TaskInput {
		return provider.TaskInput{
			Files:  map[string]string{"audio": audioFile},
			Params: map[string]any{"_out": outParam},
		}
	}

	// 1) 相对 _out：产物落 outDir 相对段下，artifact 路径保持相对。
	out, err := tool.Run(context.Background(), in(filepath.Join("custom", "result.txt")), nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if out.Artifacts[0].Path != filepath.Join("custom", "result.txt") ||
		out.Artifacts[1].Path != filepath.Join("custom", "result.srt") {
		t.Fatalf("相对 _out artifact 路径 = %s, %s", out.Artifacts[0].Path, out.Artifacts[1].Path)
	}
	for _, p := range []string{out.Artifacts[0].Path, out.Artifacts[1].Path} {
		if _, err := os.Stat(filepath.Join(tool.outDir, p)); err != nil {
			t.Errorf("产物 %s 未落盘: %v", p, err)
		}
	}

	// 2) 绝对 _out：txt/srt 均为绝对路径，srt = txt 换扩展名。
	absTxt := filepath.Join(t.TempDir(), "out.txt")
	absSrt := strings.TrimSuffix(absTxt, ".txt") + ".srt"
	out, err = tool.Run(context.Background(), in(absTxt), nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if out.Artifacts[0].Path != absTxt || out.Artifacts[1].Path != absSrt {
		t.Fatalf("绝对 _out artifact 路径 = %s, %s, 期望 %s, %s",
			out.Artifacts[0].Path, out.Artifacts[1].Path, absTxt, absSrt)
	}
	if _, err := os.Stat(absSrt); err != nil {
		t.Errorf("SRT %s 未落盘: %v", absSrt, err)
	}
}
