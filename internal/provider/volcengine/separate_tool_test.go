package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// shortenSepPoll 缩短轮询节奏（包级 var 注入，同 asr URL 模式先例），避免测试等待真实退避间隔。
func shortenSepPoll(t *testing.T) {
	t.Helper()
	oldInterval, oldMax, oldTimeout := sepPollInterval, sepPollMax, sepToolTimeout
	sepPollInterval, sepPollMax, sepToolTimeout = 50*time.Millisecond, 100*time.Millisecond, 5*time.Second
	t.Cleanup(func() { sepPollInterval, sepPollMax, sepToolTimeout = oldInterval, oldMax, oldTimeout })
}

// sepMockServer mock MediaKit API：submit 固定 ack task-sep-1；query 按响应序列依次返回
// （耗尽后重复最后一条）。记录提交次数/查询次数/最近请求（鉴权头与 body）供断言。
type sepMockServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	submits  int
	queries  int
	lastAuth string
	lastBody map[string]any
}

func newSepMockServer(t *testing.T, querySeq []map[string]any) *sepMockServer {
	t.Helper()
	m := &sepMockServer{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == MediaKitSubmitPath {
			m.submits++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.lastBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "task_id": "task-sep-1"})
			return
		}
		// GET /api/v1/tasks/{task_id}：按序列返回，耗尽后重复最后一条。
		m.queries++
		resp := querySeq[len(querySeq)-1]
		if m.queries <= len(querySeq) {
			resp = querySeq[m.queries-1]
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// sepTrackURL 音轨在下载 mock 服务上的直链；sepTrackKey 上游 result 对应字段名。
var (
	sepTrackURL = map[string]string{
		"voice": "/files/voice", "background": "/files/background",
		"music": "/files/music", "sfx": "/files/sfx",
	}
	sepTrackKey = map[string]string{
		"voice": "voice_audio_url", "background": "background_audio_url",
		"music": "music_audio_url", "sfx": "sfx_audio_url",
	}
)

// newSeparateDownloadServer 下载 mock 服务：/files/<track> 各音轨返回不同字节，供逐字节断言。
func newSeparateDownloadServer(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string][]byte{
		"/files/voice":      []byte("voice-payload-1"),
		"/files/background": []byte("background-payload-2"),
		"/files/music":      []byte("music-payload-3"),
		"/files/sfx":        []byte("sfx-payload-4"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sepCompletedResp 构造 completed 查询响应：指定音轨的非空 result URL（直链指向下载 mock）与时长。
func sepCompletedResp(dl *httptest.Server, duration float64, tracks ...string) map[string]any {
	result := map[string]any{"duration": duration}
	for _, tr := range tracks {
		result[sepTrackKey[tr]] = dl.URL + sepTrackURL[tr]
	}
	return map[string]any{"success": true, "task_id": "task-sep-1", "status": "completed", "result": result}
}

// assertSepSummary 校验 Summary 的 scene/duration_s/tracks 三键。
func assertSepSummary(t *testing.T, summary map[string]any, scene string, duration float64, tracks string) {
	t.Helper()
	if summary["scene"] != scene {
		t.Errorf("summary.scene = %v, 期望 %s", summary["scene"], scene)
	}
	if d, _ := summary["duration_s"].(float64); d != duration {
		t.Errorf("summary.duration_s = %v, 期望 %v", summary["duration_s"], duration)
	}
	got, _ := summary["tracks"].([]string)
	if strings.Join(got, ",") != tracks {
		t.Errorf("summary.tracks = %v, 期望 %v", summary["tracks"], tracks)
	}
}

func TestSeparateToolAudioScene(t *testing.T) {
	shortenSepPoll(t)
	dl := newSeparateDownloadServer(t)
	m := newSepMockServer(t, []map[string]any{
		{"success": true, "task_id": "task-sep-1", "status": "running"},
		sepCompletedResp(dl, 12.5, "voice", "background"),
	})
	tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}

	// Files 不消费：MediaKit 首期仅公网 URL，多余 files 静默忽略（不报错、不读取）。
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": "https://example.com/song.mp3"},
		Files:  map[string]string{"audio": filepath.Join(t.TempDir(), "nonexistent.mp3")},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}

	m.mu.Lock()
	submits, queries, auth, body := m.submits, m.queries, m.lastAuth, m.lastBody
	m.mu.Unlock()
	if submits != 1 || queries < 2 {
		t.Fatalf("submits = %d, queries = %d, 期望 1 次提交与至少 2 次查询（running→completed）", submits, queries)
	}
	if auth != "Bearer key-1" {
		t.Errorf("Authorization = %q, 期望 Bearer key-1", auth)
	}
	// 音频扩展名走 audio_url；scene/output_format 透传（scene 缺省 Audio、format 缺省 mp3）。
	if body["audio_url"] != "https://example.com/song.mp3" ||
		body["scene"] != "Audio" || body["output_format"] != "mp3" {
		t.Errorf("submit body = %v", body)
	}
	if _, ok := body["video_url"]; ok {
		t.Errorf("body 不应包含 video_url: %v", body)
	}

	// 2 artifacts：顺序 = tracks 顺序（voice→background），meta.track/格式/文件名/内容逐字节。
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts 共 %d 个, 期望 2: %+v", len(out.Artifacts), out.Artifacts)
	}
	wantPayload := map[string]string{"voice": "voice-payload-1", "background": "background-payload-2"}
	for i, wantTrack := range []string{"voice", "background"} {
		art := out.Artifacts[i]
		if art.Kind != "audio" || art.Format != "mp3" || !strings.HasPrefix(art.Path, "separate/") {
			t.Errorf("artifacts[%d] = %+v", i, art)
		}
		if art.Meta["track"] != wantTrack {
			t.Errorf("artifacts[%d].meta.track = %v, 期望 %s", i, art.Meta["track"], wantTrack)
		}
		if !strings.HasSuffix(art.Path, "_"+wantTrack+".mp3") {
			t.Errorf("artifacts[%d].Path = %s, 期望 <uuid>_%s.mp3", i, art.Path, wantTrack)
		}
		data, err := os.ReadFile(filepath.Join(tool.outDir, art.Path))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != wantPayload[wantTrack] {
			t.Errorf("音轨 %s 内容 = %q, 期望 %q", wantTrack, data, wantPayload[wantTrack])
		}
		if art.Size != int64(len(wantPayload[wantTrack])) {
			t.Errorf("artifacts[%d].Size = %d, 期望 %d", i, art.Size, len(wantPayload[wantTrack]))
		}
	}
	assertSepSummary(t, out.Summary, "Audio", 12.5, "voice,background")
}

func TestSeparateToolDramaScene(t *testing.T) {
	shortenSepPoll(t)
	dl := newSeparateDownloadServer(t)
	m := newSepMockServer(t, []map[string]any{
		sepCompletedResp(dl, 30, "voice", "music", "sfx"),
	})
	tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}

	out, err := tool.Run(context.Background(), provider.TaskInput{
		// 视频扩展名走 video_url（查询串不影响判定）；scene 大小写不敏感归一 Drama。
		Params: map[string]any{"url": "https://example.com/drama.mp4?sign=x", "scene": "drama", "output_format": "aac"},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}

	m.mu.Lock()
	body := m.lastBody
	m.mu.Unlock()
	if body["video_url"] != "https://example.com/drama.mp4?sign=x" ||
		body["scene"] != "Drama" || body["output_format"] != "aac" {
		t.Errorf("submit body = %v", body)
	}
	if _, ok := body["audio_url"]; ok {
		t.Errorf("body 不应包含 audio_url: %v", body)
	}

	// Drama 3 轨：voice→music→sfx，格式 aac。
	if len(out.Artifacts) != 3 {
		t.Fatalf("artifacts 共 %d 个, 期望 3: %+v", len(out.Artifacts), out.Artifacts)
	}
	for i, wantTrack := range []string{"voice", "music", "sfx"} {
		art := out.Artifacts[i]
		if art.Kind != "audio" || art.Format != "aac" || art.Meta["track"] != wantTrack {
			t.Errorf("artifacts[%d] = %+v, 期望 audio/aac/%s", i, art, wantTrack)
		}
		if !strings.HasSuffix(art.Path, wantTrack+".aac") {
			t.Errorf("artifacts[%d].Path = %s, 期望以 %s.aac 结尾", i, art.Path, wantTrack)
		}
	}
	assertSepSummary(t, out.Summary, "Drama", 30, "voice,music,sfx")
}

func TestSeparateToolValidation(t *testing.T) {
	newTool := func(apiKey string) (*SeparateTool, *sepMockServer) {
		m := newSepMockServer(t, nil)
		return &SeparateTool{client: NewMediaKitClientWithBaseURL(apiKey, m.srv.URL), outDir: t.TempDir()}, m
	}

	t.Run("no_url", func(t *testing.T) {
		tool, _ := newTool("key-1")
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "缺少输入") {
			t.Fatalf("err = %v, 期望包含「缺少输入」", err)
		}
	})

	t.Run("bad_scene", func(t *testing.T) {
		tool, _ := newTool("key-1")
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3", "scene": "Video"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "暂不支持") {
			t.Fatalf("err = %v, 期望包含「暂不支持」", err)
		}
	})

	t.Run("bad_format", func(t *testing.T) {
		tool, _ := newTool("key-1")
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3", "output_format": "ogg"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "暂不支持") {
			t.Fatalf("err = %v, 期望包含「暂不支持」", err)
		}
	})

	t.Run("no_api_key", func(t *testing.T) {
		tool, m := newTool("")
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3"},
		}, nopReport)
		if err == nil || !errors.Is(err, ErrNoCred) {
			t.Fatalf("err = %v, 期望包装 ErrNoCred", err)
		}
		if !strings.Contains(err.Error(), "volc.mediakit.api_key") {
			t.Errorf("错误文案应指引 mediakit 配置: %v", err)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.submits != 0 || m.queries != 0 {
			t.Errorf("空 apiKey 不应建连: submits = %d, queries = %d", m.submits, m.queries)
		}
	})
}

func TestSeparateToolFailed(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		shortenSepPoll(t)
		m := newSepMockServer(t, []map[string]any{
			{"success": true, "task_id": "task-sep-1", "status": "failed",
				"error": map[string]any{"code": "InternalError", "message": "处理失败"}},
		})
		tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "分离任务失败") || !strings.Contains(err.Error(), "InternalError") {
			t.Fatalf("err = %v, 期望「分离任务失败」含上游 code", err)
		}
	})

	t.Run("empty_result", func(t *testing.T) {
		shortenSepPoll(t)
		// completed 但 result 缺失：Query 返回 res 为 nil，工具须报「分离结果为空」而非 panic。
		m := newSepMockServer(t, []map[string]any{
			{"success": true, "task_id": "task-sep-1", "status": "completed"},
		})
		tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "分离结果为空") {
			t.Fatalf("err = %v, 期望包含「分离结果为空」", err)
		}
	})

	t.Run("empty_tracks", func(t *testing.T) {
		shortenSepPoll(t)
		// completed 且 result 存在但无任何音轨 URL：Tracks 为空，同报「分离结果为空」。
		m := newSepMockServer(t, []map[string]any{
			{"success": true, "task_id": "task-sep-1", "status": "completed",
				"result": map[string]any{"duration": 12.5}},
		})
		tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "分离结果为空") {
			t.Fatalf("err = %v, 期望包含「分离结果为空」", err)
		}
	})
}

func TestSeparateToolOutDirRedirect(t *testing.T) {
	shortenSepPoll(t)
	dl := newSeparateDownloadServer(t)
	m := newSepMockServer(t, []map[string]any{sepCompletedResp(dl, 12.5, "voice", "background")})
	tool := &SeparateTool{client: NewMediaKitClientWithBaseURL("key-1", m.srv.URL), outDir: t.TempDir()}
	in := func(outParam any) provider.TaskInput {
		return provider.TaskInput{
			Params: map[string]any{"url": "https://example.com/a.mp3", "_out": outParam},
		}
	}

	// 1) 相对 _out：产物落 outDir 相对段下（文件名不变，不再叠加 separate/ 前缀），artifact 路径保持相对 outDir。
	out, err := tool.Run(context.Background(), in("custom"), nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts 共 %d 个, 期望 2: %+v", len(out.Artifacts), out.Artifacts)
	}
	for i, art := range out.Artifacts {
		if !strings.HasPrefix(art.Path, "custom"+string(filepath.Separator)) {
			t.Errorf("artifacts[%d].Path = %s, 期望位于 custom/ 下", i, art.Path)
		}
		if strings.Contains(art.Path, "separate") {
			t.Errorf("artifacts[%d].Path = %s, 重定向后不应保留 separate/ 前缀", i, art.Path)
		}
		if _, err := os.Stat(filepath.Join(tool.outDir, art.Path)); err != nil {
			t.Errorf("产物 %s 未落盘: %v", art.Path, err)
		}
	}

	// 2) 绝对 _out：产物直接写入该目录，artifact 路径保持绝对，内容逐字节一致。
	absDir := t.TempDir()
	out, err = tool.Run(context.Background(), in(absDir), nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	wantPayload := map[string]string{"voice": "voice-payload-1", "background": "background-payload-2"}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts 共 %d 个, 期望 2: %+v", len(out.Artifacts), out.Artifacts)
	}
	for i, art := range out.Artifacts {
		if !filepath.IsAbs(art.Path) || filepath.Dir(art.Path) != absDir {
			t.Errorf("artifacts[%d].Path = %s, 期望绝对路径且位于 %s", i, art.Path, absDir)
		}
		data, err := os.ReadFile(art.Path)
		if err != nil {
			t.Fatal(err)
		}
		track, _ := art.Meta["track"].(string)
		if string(data) != wantPayload[track] {
			t.Errorf("音轨 %s 内容 = %q, 期望 %q", track, data, wantPayload[track])
		}
	}
}
