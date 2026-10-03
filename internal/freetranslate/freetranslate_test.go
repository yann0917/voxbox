package freetranslate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeepLX(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/translate" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("请求体解析失败: %v", err)
		}
		// wire 契约:text 进 body,语言码转大写去区域(zh-Hant→ZH)。
		if req["text"] != "你好世界" || req["source_lang"] != "ZH" || req["target_lang"] != "EN" {
			t.Errorf("wire: %+v", req)
		}
		w.Write([]byte(`{"code":200,"data":"Hello world"}`))
	}))
	defer ts.Close()
	got, err := DeepLX(context.Background(), ts.URL, "你好世界", "zh-Hant", "en")
	if err != nil || got != "Hello world" {
		t.Fatalf("DeepLX=%q err=%v", got, err)
	}
}

func TestDeepLXEmptyEndpoint(t *testing.T) {
	_, err := DeepLX(context.Background(), "", "文本", "zh", "en")
	if err == nil {
		t.Fatal("空端点应报错")
	}
	if !strings.Contains(err.Error(), "translate.deeplx_url") {
		t.Fatalf("报错应带配置指引: %v", err)
	}
}

func TestChain(t *testing.T) {
	// 繁体守卫直通 DeepLX:mock 校验请求确实到达端点。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":"fallback ok"}`))
	}))
	defer ts.Close()
	got, err := Chain(ts.URL)(context.Background(), "文本", "", "en")
	if err != nil || got != "fallback ok" {
		t.Fatalf("chain=%q err=%v", got, err)
	}
}

func TestChainEmptyEndpointGuided(t *testing.T) {
	_, err := Chain("")(context.Background(), "文本", "", "en")
	if err == nil || !strings.Contains(err.Error(), "deeplx_url") {
		t.Fatalf("未配端点应报带指引的错误: %v", err)
	}
}

func TestChainHantUnsupported(t *testing.T) {
	chain := Chain("")
	if _, err := chain(context.Background(), "文本", "zh", "zh-Hant"); err == nil {
		t.Fatal("免费链不支持繁体目标应报错")
	}
	// 不区分大小写。
	if _, err := chain(context.Background(), "文本", "zh", "ZH-HANT"); err == nil {
		t.Fatal("zh-Hant 不区分大小写应报错")
	}
}
