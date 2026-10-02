package server

// live_smoke_test.go 实时字幕 WS relay 真机冒烟(硬验收;不进 CI):
//
//	本地链路:VOXBOX_LIVE_SMOKE=local go test ./internal/server -run TestSmokeLiveRelayLocal
//	  前置:audiocpp_server 已按 streaming 条目起服(VOXBOX_LIVE_BASE,默认
//	  http://127.0.0.1:46920),~/.voxbox 已播种 r2t2-q8_0;
//	火山链路:VOXBOX_LIVE_SMOKE=volc go test ./internal/server -run TestSmokeLiveRelayVolcengine
//	  前置:~/.voxbox/config.yaml 配置火山语音凭证。
//
// 两链路均按实时节奏(200ms/包)喂 /tmp/voxbox-duo-smoke-16k.pcm(缺文件自动跳过),
// 断言:增量出字 → stop → final → save 落库(任务+产物)。

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

const smokeSample = "/tmp/voxbox-duo-smoke-16k.pcm"

// smokeLiveServer 构造独立 Service(真实凭证+隔离数据目录)与 httptest 服务。
func smokeLiveServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("读取本机配置失败，跳过真机冒烟: %v", err)
	}
	cfg.DataDir = t.TempDir() // 凭证真实、数据隔离
	svc, err := service.New(cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

// feedRealtimeWS 按 200ms/包实时节奏向 WS 推 PCM 文件流,返回推完耗时。
func feedRealtimeWS(t *testing.T, ws *websocket.Conn, pcm []byte) time.Duration {
	t.Helper()
	const chunkMs = 200
	const chunk = 16000 * 2 * chunkMs / 1000 // 6400B
	start := time.Now()
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, pcm[off:end]); err != nil {
			t.Fatalf("推送音频失败: %v", err)
		}
		if end < len(pcm) {
			time.Sleep(chunkMs * time.Millisecond)
		}
	}
	return time.Since(start)
}

// readLiveUntil 读到指定类型消息为止(跳过 partial),超时 Fatal。
func readLiveUntil(t *testing.T, ws *websocket.Conn, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_ = ws.SetReadDeadline(time.Now().Add(timeout))
		mt, raw, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("读 WS 失败(等 %s): %v", want, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("非 JSON 帧: %s", raw)
		}
		if mt == websocket.TextMessage && m["type"] == want {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %s 超时", want)
		}
	}
}

// TestSmokeLiveRelayLocal 本地链路真机冒烟:audiocpp streaming 条目 + WS relay 全流程。
func TestSmokeLiveRelayLocal(t *testing.T) {
	if os.Getenv("VOXBOX_LIVE_SMOKE") != "local" {
		t.Skip("设 VOXBOX_LIVE_SMOKE=local 启用本地链路真机冒烟")
	}
	base := os.Getenv("VOXBOX_LIVE_BASE")
	if base == "" {
		base = "http://127.0.0.1:46920"
	}
	pcm, err := os.ReadFile(smokeSample)
	if err != nil {
		t.Skipf("无冒烟样本 %s,跳过: %v", smokeSample, err)
	}

	// 生产路径:engine=local 经 defaultNewLiveEngine 解析模型 → 需目录有已安装条目。
	// 隔离 home 里伪造安装记录(manifest+符号链接指向真实 gguf),引擎地址用 BaseURL 缝。
	ts, s := smokeLiveServer(t)
	home := s.svc.Config().DataDir
	e, ok := s.svc.LocalModels().GetEntry("r2t2-q8_0")
	if !ok {
		t.Skip("模型目录无 r2t2-q8_0 条目,跳过")
	}
	dir := filepath.Join(home, "models", "r2t2-q8_0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf, _ := json.Marshal(map[string]any{
		"id": "r2t2-q8_0", "repo": "", "revision": e.Revision,
		"files":        []map[string]any{{"path": "r2t2-q8_0.gguf", "size": e.SizeBytes}},
		"completed_at": time.Now().Format(time.RFC3339),
	})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mf, 0o644); err != nil {
		t.Fatal(err)
	}
	realGGUF := filepath.Join(os.Getenv("HOME"), ".voxbox", "data", "models", "r2t2-q8_0", "r2t2-q8_0.gguf")
	if _, err := os.Stat(realGGUF); err == nil {
		_ = os.Symlink(realGGUF, filepath.Join(dir, "r2t2-q8_0.gguf"))
	}
	s.svc.TTSRuntime().BaseURL = base // URL 缝:直连已起的 audiocpp_server

	ac := loginTestClient(t, s, ts)
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	sendJSON(t, ws, `{"type":"start","engine":"local","language":"zh","hotwords":"开会"}`)
	expectType(t, ws, "ready")
	t.Log("ready: 会话建立")

	feedDur := feedRealtimeWS(t, ws, pcm)
	t.Logf("音频推完 wall=%v(样本 %.1fs)", feedDur, float64(len(pcm))/32000)

	partialN := 0
	var lastCommitted string
	deadline := time.Now().Add(30 * time.Second)
	for {
		_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		if time.Now().After(deadline) {
			t.Fatal("等待增量超时")
		}
		m := readLiveUntil(t, ws, "partial", 30*time.Second)
		partialN++
		if v, ok := m["committed"].(string); ok {
			lastCommitted = v
		}
		if partialN >= 2 {
			break
		}
	}
	t.Logf("增量 partial %d 次,末次 committed=%q", partialN, lastCommitted)
	if lastCommitted == "" {
		t.Fatal("未收到任何识别文本")
	}

	sendJSON(t, ws, `{"type":"stop"}`)
	final := readLiveUntil(t, ws, "final", 30*time.Second)
	text, _ := final["text"].(string)
	t.Logf("final: text=%q duration_ms=%v", text, final["duration_ms"])
	if text == "" {
		t.Fatal("final 文本为空")
	}
	if !strings.Contains(text, "开会") {
		t.Errorf("final 文本应含样本关键词「开会」: %q", text)
	}

	sendJSON(t, ws, `{"type":"save"}`)
	saved := readLiveUntil(t, ws, "saved", 10*time.Second)
	taskID, _ := saved["task_id"].(string)
	if taskID == "" {
		t.Fatalf("saved = %v", saved)
	}
	tk, err := s.svc.DB().GetTask(taskID)
	if err != nil {
		t.Fatalf("任务未落库: %v", err)
	}
	if tk.Provider != "local" || tk.Tool != "asr" || tk.Status != store.StatusSucceeded {
		t.Errorf("task = %+v", tk)
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(tk.Params), &params)
	if params["version"] != "live" || params["engine"] != "local" {
		t.Errorf("params = %v", params)
	}
	arts, _ := s.svc.DB().ListArtifacts(taskID)
	if len(arts) != 1 || arts[0].Kind != "transcript" {
		t.Fatalf("artifacts = %+v(本地会话仅 txt)", arts)
	}
	t.Logf("已存任务 %s(title=%s),产物 %s(%dB)", taskID, tk.Title, arts[0].Path, arts[0].Size)
}

// TestSmokeLiveRelayVolcengine 火山链路真机冒烟:真实凭据 + WS relay 全流程。
func TestSmokeLiveRelayVolcengine(t *testing.T) {
	if os.Getenv("VOXBOX_LIVE_SMOKE") != "volc" {
		t.Skip("设 VOXBOX_LIVE_SMOKE=volc 启用火山链路真机冒烟")
	}
	pcm, err := os.ReadFile(smokeSample)
	if err != nil {
		t.Skipf("无冒烟样本 %s,跳过: %v", smokeSample, err)
	}

	ts, s := smokeLiveServer(t)
	ac := loginTestClient(t, s, ts)
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	sendJSON(t, ws, `{"type":"start","engine":"volcengine","speaker":true}`)
	expectType(t, ws, "ready")
	t.Log("ready: 火山会话建立")

	go func() { _ = feedRealtimeWS(t, ws, pcm); sendJSON(t, ws, `{"type":"stop"}`) }()

	// 读增量:至少一次 partial(committed 或 unstable 非空)。
	partialN := 0
	var lastPartial map[string]any
	final := func() map[string]any {
		deadline := time.Now().Add(60 * time.Second)
		for {
			_ = ws.SetReadDeadline(deadline)
			mt, raw, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("读 WS 失败: %v", err)
			}
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if mt == websocket.TextMessage {
				switch m["type"] {
				case "partial":
					partialN++
					lastPartial = m
				case "final":
					return m
				case "error":
					t.Fatalf("服务端错误: %v", m["message"])
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("等待 final 超时")
			}
		}
	}()
	t.Logf("增量 partial %d 次,末次 committed=%q unstable=%q",
		partialN, lastPartial["committed"], lastPartial["unstable"])
	if partialN < 2 {
		t.Errorf("增量 partial 应 ≥2 次, got %d", partialN)
	}
	text, _ := final["text"].(string)
	t.Logf("final: text=%q duration_ms=%v", text, final["duration_ms"])
	if text == "" {
		t.Fatal("final 文本为空")
	}
	segs, _ := final["segments"].([]any)
	if len(segs) == 0 {
		t.Error("火山会话 final 应含 segments")
	}
	if _, has := final["segments"]; !has {
		t.Error("final 缺 segments 键")
	}

	sendJSON(t, ws, `{"type":"save"}`)
	saved := readLiveUntil(t, ws, "saved", 10*time.Second)
	taskID, _ := saved["task_id"].(string)
	if taskID == "" {
		t.Fatalf("saved = %v", saved)
	}
	tk, err := s.svc.DB().GetTask(taskID)
	if err != nil {
		t.Fatalf("任务未落库: %v", err)
	}
	if tk.Provider != "volcengine" || tk.Tool != "asr" || tk.Status != store.StatusSucceeded {
		t.Errorf("task = %+v", tk)
	}
	var summary map[string]any
	_ = json.Unmarshal([]byte(tk.Summary), &summary)
	if _, has := summary["segments"]; !has {
		t.Errorf("summary = %v(火山会话应有 segments)", summary)
	}
	arts, _ := s.svc.DB().ListArtifacts(taskID)
	kinds := map[string]bool{}
	for _, a := range arts {
		kinds[a.Kind] = true
	}
	if !kinds["transcript"] || !kinds["subtitle"] {
		t.Errorf("artifacts = %+v(火山会话应有 txt+srt)", arts)
	}
	t.Logf("已存任务 %s(title=%s),产物 %d 个(txt+srt)", taskID, tk.Title, len(arts))
}
