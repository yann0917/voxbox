package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalReady(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 全新环境:引擎未装 → tts 与 asr 都不 ready,missing 列出引擎与模型
	resp, err := ac.Get(ts.URL + "/api/local/ready?tool=tts")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d: %s", e.Code, e.Message)
	}
	data := e.Data.(map[string]any)
	if data["ready"] != false {
		t.Fatal("全新环境 tts 不应 ready")
	}
	missing := data["missing"].([]any)
	if len(missing) < 2 {
		t.Fatalf("应同时缺引擎与模型: %#v", missing)
	}
	// 非法 tool
	req, _ := http.NewRequest("GET", ts.URL+"/api/local/ready?tool=nope", nil)
	resp2, _ := ac.Do(req)
	var e2 envelope
	_ = decodeBody(resp2, &e2)
	if e2.Code != CodeBadRequest {
		t.Fatalf("非法 tool 应 BadRequest,实际 %d", e2.Code)
	}
}

func TestVoicesLocal(t *testing.T) {
	ts, _, ac := newTestServer(t)
	resp, err := ac.Get(ts.URL + "/api/voices?provider=local")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d", e.Code)
	}
	voices := e.Data.(map[string]any)["voices"].([]any)
	if len(voices) != 9 {
		t.Fatalf("本地预置音色应 9 个: %d", len(voices))
	}
}

// TestVoicesLocalKokoro family=kokoro 返回内置音色库:53 个,首项 af_alloy(sid 0)。
func TestVoicesLocalKokoro(t *testing.T) {
	ts, _, ac := newTestServer(t)
	resp, err := ac.Get(ts.URL + "/api/voices?provider=local&family=kokoro")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d", e.Code)
	}
	voices := e.Data.(map[string]any)["voices"].([]any)
	if len(voices) != 53 {
		t.Fatalf("kokoro 内置音色应 53 个: %d", len(voices))
	}
	first := voices[0].(map[string]any)
	if first["id"] != "af_alloy" {
		t.Fatalf("首音色应为 af_alloy: %#v", first)
	}
	// sid=0 被 omitempty 省略是合法形态:存在则必须为 0
	if sid, ok := first["sid"]; ok && sid.(float64) != 0 {
		t.Fatalf("首音色 sid 应为 0: %#v", first)
	}
}

// TestLocalReadyKokoroPair 依赖链按条目推导的回归锁定:只装 sherpa 引擎 + kokoro
// 模型(不装 audiocpp)也应 ready——任一「引擎+模型」成对齐备即可用。
func TestLocalReadyKokoroPair(t *testing.T) {
	ts, s, ac := newTestServer(t)
	modelsRoot := s.svc.LocalModels().Dir()
	dataDir := filepath.Dir(modelsRoot)
	seedInstalledEngine(t, dataDir, "sherpa-onnx")
	seedInstalledModel(t, modelsRoot, "kokoro-v1.0")
	resp, err := ac.Get(ts.URL + "/api/local/ready?tool=tts")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	data := e.Data.(map[string]any)
	if data["ready"] != true {
		t.Fatalf("kokoro+sherpa 成对齐备应 ready: %#v", data)
	}
}

// seedInstalledEngine 落一个引擎安装标记(manifest 即安装标记,id/revision 与目录条目一致)。
func seedInstalledEngine(t *testing.T, dataDir, id string) {
	t.Helper()
	dir := filepath.Join(dataDir, "engines", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := map[string]any{"id": id, "revision": "master", "binary": "pkg/" + id, "completed_at": "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedInstalledModel 落一个模型安装标记(modelsRoot 为 Manager.Dir())。
func seedInstalledModel(t *testing.T, modelsRoot, id string) {
	t.Helper()
	dir := filepath.Join(modelsRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := map[string]any{"id": id, "revision": "master", "completed_at": "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
