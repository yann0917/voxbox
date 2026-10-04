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
	// 列表(带 lang 过滤):响应含 total;分页 size=1 翻页不重叠
	e4, _ := doJSON(http.MethodGet, "/api/glossary?lang=en", "")
	listData := e4.Data.(map[string]any)
	if got := len(listData["items"].([]any)); got != 2 {
		t.Fatalf("en 应有 2 条, got %d", got)
	}
	if total, _ := listData["total"].(float64); total != 2 {
		t.Fatalf("total 应为 2, got %v", listData["total"])
	}
	e5a, _ := doJSON(http.MethodGet, "/api/glossary?page=1&size=1", "")
	e5b, _ := doJSON(http.MethodGet, "/api/glossary?page=2&size=1", "")
	first := e5a.Data.(map[string]any)["items"].([]any)[0].(map[string]any)["src"]
	second := e5b.Data.(map[string]any)["items"].([]any)[0].(map[string]any)["src"]
	if first == second {
		t.Fatalf("分页两页不应重叠: %v", first)
	}
	// 修改
	putBody := `{"target_language":"en","src":"张三","dst":"Zhang San II"}`
	e6, _ := doJSON(http.MethodPut, fmt.Sprintf("/api/glossary/%d", id), putBody)
	if e6.Data.(map[string]any)["dst"] != "Zhang San II" {
		t.Fatalf("修改应生效: %v", e6.Data)
	}
	// 删除 + 复删 NotFound
	if e7, _ := doJSON(http.MethodDelete, fmt.Sprintf("/api/glossary/%d", id), ""); e7.Code != 0 {
		t.Fatalf("删除应成功: %v", e7)
	}
	if e8, _ := doJSON(http.MethodDelete, fmt.Sprintf("/api/glossary/%d", id), ""); e8.Code != CodeNotFound {
		t.Fatalf("复删应 code=%d(不存在), got %v", CodeNotFound, e8.Code)
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
