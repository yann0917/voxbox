package localruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
)

// fakeAudiocpp 假 audiocpp_server:/health 可控就绪;/v1/tasks/run 回固定 base64 wav。
func fakeAudiocpp(t *testing.T, readyAfter *atomic.Bool, captured *atomic.Value) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if readyAfter != nil && !readyAfter.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/tasks/run", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured != nil {
			captured.Store(body)
		}
		wav := bytes.Repeat([]byte("RIFFxxxxWAVE"), 64)
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString(wav)})
	})
	return httptest.NewServer(mux)
}

func TestSynthesizeViaBaseURL(t *testing.T) {
	var captured atomic.Value
	srv := fakeAudiocpp(t, nil, &captured)
	defer srv.Close()

	dir := t.TempDir()
	rt := NewTTSRuntime(dir, nil) // models 传 nil:BaseURL seam 下不查安装态
	rt.BaseURL = srv.URL
	defer rt.Close()

	out, err := rt.Synthesize(context.Background(), SynthRequest{
		ModelID: "qwen3-tts-base-q8", Text: "你好", Language: "Chinese",
	}, func(p int, note string) {})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Fatalf("产物应为解码后的 wav: %v", err)
	}
	req := captured.Load().(map[string]any)
	if req["model"] != "qwen3-tts-base-q8" {
		t.Fatalf("RPC model 不符: %v", req)
	}
	// 产物必须落在 dataDir/tts/ 下且为 .wav
	if filepath.Dir(out) != filepath.Join(dir, "tts") {
		t.Fatalf("产物目录不符: %s", out)
	}
}

func TestSynthesizeRequestOptions(t *testing.T) {
	var captured atomic.Value
	srv := fakeAudiocpp(t, nil, &captured)
	defer srv.Close()
	rt := NewTTSRuntime(t.TempDir(), nil)
	rt.BaseURL = srv.URL
	defer rt.Close()
	ref := filepath.Join(t.TempDir(), "ref.wav")
	_ = os.WriteFile(ref, []byte("wav"), 0o644)
	_, err := rt.Synthesize(context.Background(), SynthRequest{
		Text: "x", RefWav: ref, RefText: "参考原文",
	}, func(p int, note string) {})
	if err != nil {
		t.Fatal(err)
	}
	req := captured.Load().(map[string]any)
	inner := req["request"].(map[string]any)
	if inner["voice_ref"] != ref || inner["options"].(map[string]any)["reference_text"] != "参考原文" {
		t.Fatalf("克隆参数不符: %v", inner)
	}
}

func TestSynthesizeWaitsForHealth(t *testing.T) {
	var ready atomic.Bool
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !ready.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/v1/tasks/run", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString([]byte("RIFF"))})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := NewTTSRuntime(t.TempDir(), nil)
	rt.BaseURL = srv.URL
	rt.healthInterval = 10 * time.Millisecond
	defer rt.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		ready.Store(true)
	}()
	if _, err := rt.Synthesize(context.Background(), SynthRequest{Text: "x"}, func(p int, note string) {}); err != nil {
		t.Fatalf("health 恢复后应成功: %v", err)
	}
	if hits.Load() < 2 {
		t.Fatal("应轮询 health 多次直到就绪")
	}
}

// seedTTSManager 用内嵌真实目录播种 audiocpp 引擎与一个 qwen3 TTS 模型的安装盘面
// (磁盘即真相:manifest 合法即已安装),返回与生产同路径构造的 localmodel.Manager。
func seedTTSManager(t *testing.T, dir string) *localmodel.Manager {
	t.Helper()
	writeManifest := func(dirPath, id, extra string) {
		t.Helper()
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf(`{"id":%q,"repo":"org/%s","revision":"master"%s,"files":[{"path":"f","size":1}]}`, id, id, extra)
		if err := os.WriteFile(filepath.Join(dirPath, "manifest.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest(filepath.Join(dir, "engines", "audiocpp"), "audiocpp", `,"binary":"pkg/bin/audiocpp_server"`)
	writeManifest(filepath.Join(dir, "models", "qwen3-tts-base-q8"), "qwen3-tts-base-q8", "")
	return localmodel.NewManager(dir)
}

// startFakeOnConfigPort 从 cmd 参数解析 --config 的 server.json,按其中 host/port 起真
// HTTP 监听(/health 即 200),模拟重建后的新进程就绪;监听器挂 t.Cleanup 回收。
func startFakeOnConfigPort(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	var cfgPath string
	for i, a := range cmd.Args {
		if a == "--config" && i+1 < len(cmd.Args) {
			cfgPath = cmd.Args[i+1]
		}
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var cfg struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })
	return nil
}

// TestEnsureHealthRestartAfterCrash 崩溃自愈:进程退出标志置位后,ensureHealth 轮询中
// 触发恰好一次重建(每任务至多一次),重建后 /health 就绪即成功。
func TestEnsureHealthRestartAfterCrash(t *testing.T) {
	dir := t.TempDir()
	rt := NewTTSRuntime(dir, seedTTSManager(t, dir))
	rt.healthInterval = 5 * time.Millisecond

	var starts atomic.Int64
	rt.procAttr = func(cmd *exec.Cmd) error {
		if starts.Add(1) == 1 {
			// 第一次:假装拉起成功但进程立即崩溃——不真正 Start,
			// wait goroutine 的 cmd.Wait() 立即报错 → 退出标志置位。
			return nil
		}
		// 重建:按 server.json 的 host/port 起真监听,模拟新进程 /health 就绪。
		return startFakeOnConfigPort(t, cmd)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := rt.ensureHealth(ctx); err != nil {
		t.Fatalf("进程崩溃后应触发一次重建并就绪: %v", err)
	}
	if n := starts.Load(); n != 2 {
		t.Fatalf("应恰好拉起 2 次(首次+崩溃重建一次),实际 %d 次", n)
	}
	// server.json 落盘与 spec §4 关键字段:darwin 显式 metal,其余 cpu
	raw, err := os.ReadFile(filepath.Join(dir, "engines", "audiocpp-server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Host            string           `json:"host"`
		Port            int              `json:"port"`
		Backend         string           `json:"backend"`
		LazyLoad        bool             `json:"lazy_load"`
		IdleUnloadMS    int              `json:"idle_unload_ms"`
		MaxLoadedModels int              `json:"max_loaded_models"`
		Models          []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	wantBackend := "cpu"
	if runtime.GOOS == "darwin" {
		wantBackend = "metal"
	}
	if cfg.Host != "127.0.0.1" || cfg.Port == 0 || cfg.Backend != wantBackend ||
		!cfg.LazyLoad || cfg.IdleUnloadMS != 300000 || cfg.MaxLoadedModels != 1 {
		t.Fatalf("server.json 关键字段不符: %+v", cfg)
	}
	// models[]:每个已安装 qwen3 GGUF 一条(此处仅 base-q8 已安装)
	if len(cfg.Models) != 1 || cfg.Models[0]["id"] != "qwen3-tts-base-q8" ||
		cfg.Models[0]["family"] != "qwen3_tts" || cfg.Models[0]["task"] != "tts" ||
		cfg.Models[0]["mode"] != "offline" {
		t.Fatalf("server.json models[] 不符: %+v", cfg.Models)
	}
}
