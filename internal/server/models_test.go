package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// decodeBody 解码 JSON 业务包络并关闭响应体(单一关闭路径,本文件用例共用)。
func decodeBody(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}

func TestModelsListShape(t *testing.T) {
	ts, _, ac := newTestServer(t)
	resp, err := ac.Get(ts.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	var e envelope
	if err := decodeBody(resp, &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d: %s", e.Code, e.Message)
	}
	items, ok := e.Data.(map[string]any)["items"].([]any)
	if !ok || len(items) < 3 {
		t.Fatalf("items 应 ≥3 个条目: %#v", e.Data)
	}
	first, _ := items[0].(map[string]any)
	for _, key := range []string{"id", "name", "kind", "summary", "size_bytes", "requirements", "license", "license_url", "status", "has_partial", "downloaded_bytes", "total_bytes"} {
		if _, present := first[key]; !present {
			t.Fatalf("条目缺字段 %s: %#v", key, first)
		}
	}
	if first["status"] != "idle" {
		t.Fatalf("新环境应全部 idle,实际 %v", first["status"])
	}
}

func TestModelsUnknownIDNotFound(t *testing.T) {
	ts, _, ac := newTestServer(t)
	for _, spec := range []struct{ method, path string }{
		{"POST", "/api/models/nope/download"},
		{"POST", "/api/models/nope/stop"},
		{"DELETE", "/api/models/nope"},
	} {
		req, _ := http.NewRequest(spec.method, ts.URL+spec.path, nil)
		resp, err := ac.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var e envelope
		_ = decodeBody(resp, &e)
		if e.Code != CodeNotFound || !strings.Contains(e.Message, "未知模型") {
			t.Fatalf("%s %s 应为 NotFound/未知模型,实际 code=%d msg=%s", spec.method, spec.path, e.Code, e.Message)
		}
	}
}

func TestModelsStopWhenIdle(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 内嵌目录首个条目(空闲态):Stop 应报「未在下载」业务错误
	var e envelope
	resp, err := ac.Get(ts.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = decodeBody(resp, &e)
	items := e.Data.(map[string]any)["items"].([]any)
	id := items[0].(map[string]any)["id"].(string)

	req, _ := http.NewRequest("POST", ts.URL+"/api/models/"+id+"/stop", nil)
	resp2, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e2 envelope
	_ = decodeBody(resp2, &e2)
	if e2.Code != CodeBadRequest || !strings.Contains(e2.Message, "未在下载") {
		t.Fatalf("空闲态 stop 应 BadRequest/未在下载,实际 code=%d msg=%s", e2.Code, e2.Message)
	}
}
