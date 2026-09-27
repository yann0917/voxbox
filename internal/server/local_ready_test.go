package server

import (
	"net/http"
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
