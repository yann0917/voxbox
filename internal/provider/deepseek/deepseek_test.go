package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchRequestAndParse(t *testing.T) {
	var gotPath, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query       string `json:"query"`
			Count       int    `json:"count"`
			OriQuestion string `json:"ori_question"`
			NeedContent bool   `json:"need_content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Query != "DeepSeek V4" || body.Count != 3 || body.OriQuestion != "最新消息" || body.NeedContent {
			t.Errorf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "DeepSeek V4 model released", "url": "https://example.com/v4",
			"snippet": "short", "summary": "long summary",
			"site_name": "Example News", "date": "2026-06-01T00:00:00+00:00",
			"authority": 3, "rerank_score": 2.4,
		}}})
	}))
	defer ts.Close()

	results, err := NewClient("sk-test", ts.URL).WebSearch(context.Background(), "DeepSeek V4", 3, "最新消息")
	if err != nil {
		t.Fatalf("WebSearch: %v", err)
	}
	if gotPath != "/v1/tools/search" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if len(results) != 1 || results[0].Title != "DeepSeek V4 model released" ||
		results[0].Summary != "long summary" || results[0].Authority != 3 {
		t.Fatalf("results = %+v", results)
	}
}

func TestWebSearchClamps(t *testing.T) {
	// count 钳制 1~10；query/ori_question 截到 128 rune；空 query 本地拒绝不出网
	var gotCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotCount = body.Count
		if runes := len([]rune(body.Query)); runes > SearchQueryMax {
			t.Errorf("query 长度 %d 超上限", runes)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer ts.Close()

	long := strings.Repeat("搜", 300)
	if _, err := NewClient("k", ts.URL).WebSearch(context.Background(), long, 99, ""); err != nil {
		t.Fatalf("WebSearch: %v", err)
	}
	if gotCount != 10 {
		t.Fatalf("count = %d, want 钳到 10", gotCount)
	}
	if _, err := NewClient("k", ts.URL).WebSearch(context.Background(), "  ", 3, ""); err == nil {
		t.Fatal("空 query 应本地报错")
	}
}

func TestWebFetch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/fetch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.URL != "https://example.com/a" {
			t.Errorf("url = %q", body.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Example article","content":"# Example article\n\n正文。"}`))
	}))
	defer ts.Close()

	title, content, err := NewClient("k", ts.URL).WebFetch(context.Background(), "https://example.com/a")
	if err != nil || title != "Example article" || !strings.Contains(content, "正文") {
		t.Fatalf("got %q %q %v", title, content, err)
	}
	if _, _, err := NewClient("k", ts.URL).WebFetch(context.Background(), "ftp://example.com"); err == nil {
		t.Fatal("非 http/https 应报错")
	}
}

func TestDecodeErrorAndProbe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			if r.Header.Get("Authorization") != "Bearer sk-live" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"Authentication Fails","type":"authentication_error"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash"},{"id":"deepseek-v4-pro"}]}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"余额不足","type":"insufficient_balance"}}`))
	}))
	defer ts.Close()

	if err := NewClient("sk-live", ts.URL).ProbeModels(context.Background()); err != nil {
		t.Fatalf("ProbeModels: %v", err)
	}
	err := NewClient("bad", ts.URL).ProbeModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Authentication Fails") {
		t.Fatalf("err = %v, want 含官方错误消息", err)
	}
	_, err = NewClient("k", ts.URL).WebSearch(context.Background(), "q", 1, "")
	if err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("err = %v, want 402 余额不足", err)
	}
}
