package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 术语表端点测试:人工 CRUD 全流程 + 撞唯一约束回显 + 语言参数过滤 + 未登录拦截。
func TestGlossaryCRUDEndpoints(t *testing.T) {
	ts, _, ac := newTestServer(t)
	doJSON := func(method, path, body string) (envelope, int) {
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := ac.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e envelope
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return e, resp.StatusCode
	}
	// 新增两条
	e, code := doJSON(http.MethodPost, "/api/glossary", `{"target_language":"en","src":"张三","dst":"Zhang San"}`)
	if code != 200 || e.Code != 0 {
		t.Fatalf("新增失败: code=%d e=%v", code, e)
	}
	id := uint(e.Data.(map[string]any)["id"].(float64))
	if _, code := doJSON(http.MethodPost, "/api/glossary", `{"target_language":"en","src":"云帆号","dst":"Yunfan"}`); code != 200 {
		t.Fatal("第二条新增失败")
	}
	// 撞唯一约束:同语言同原文 → 200 包络 + code!=0
	if e2, _ := doJSON(http.MethodPost, "/api/glossary", `{"target_language":"en","src":"张三","dst":"x"}`); e2.Code == 0 {
		t.Fatalf("撞约束应报错: %v", e2)
	}
	// 非法语言 → 参数错误
	if e3, _ := doJSON(http.MethodPost, "/api/glossary", `{"target_language":"klingon","src":"x","dst":"y"}`); e3.Code == 0 {
		t.Fatalf("非法语言应报错: %v", e3)
	}
	// 列表(带 lang 过滤)
	e4, _ := doJSON(http.MethodGet, "/api/glossary?lang=en", "")
	items := e4.Data.(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("en 应有 2 条, got %d", len(items))
	}
	// 修改
	e5, _ := doJSON(http.MethodPut, fmt.Sprintf("/api/glossary/%d", id), `{"target_language":"en","src":"张三","dst":"Zhang San II"}`)
	if e5.Data.(map[string]any)["dst"] != "Zhang San II" {
		t.Fatalf("修改应生效: %v", e5.Data)
	}
	// 删除 + 复删 NotFound
	if e6, _ := doJSON(http.MethodDelete, fmt.Sprintf("/api/glossary/%d", id), ""); e6.Code != 0 {
		t.Fatalf("删除应成功: %v", e6)
	}
	if e7, _ := doJSON(http.MethodDelete, fmt.Sprintf("/api/glossary/%d", id), ""); e7.Code != CodeNotFound {
		t.Fatalf("复删应 code=%d(不存在), got %v", CodeNotFound, e7.Code)
	}
	// 未登录访问被拦
	resp, err := http.Get(ts.URL + "/api/glossary")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("未登录不应看到术语表")
	}
}
