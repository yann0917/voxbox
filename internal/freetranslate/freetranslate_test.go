package freetranslate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleFree(t *testing.T) {
	var gotPath string
	var gotQ url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQ = r.URL.Query()
		// translate_a/single 响应形状:data[0] = [[译段, 原段, ...], ...],译段顺序拼接。
		w.Write([]byte(`[[["Hello world","你好,世界",null,null,10],["!","!",null,null,1]],null,"zh-CN"]`))
	}))
	defer ts.Close()
	// 同包测试直接给包级测试缝赋值(端点主机部分,/translate_a/single 路径由实现拼接)。
	testGoogleBase = ts.URL
	t.Cleanup(func() { testGoogleBase = "" })
	got, err := GoogleFree(context.Background(), "你好,世界!", "auto", "en")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello world!" {
		t.Fatalf("GoogleFree=%q", got)
	}
	if gotPath != "/translate_a/single" {
		t.Fatalf("path=%s", gotPath)
	}
	if gotQ.Get("client") != "gtx" || gotQ.Get("tl") != "en" || gotQ.Get("dt") != "t" {
		t.Fatalf("query=%v", gotQ)
	}
}

func TestGoogleFreeLangMap(t *testing.T) {
	// 空目标语言在发起任何请求前即报错(不出网,只测码映射报错路径)。
	if _, err := GoogleFree(context.Background(), "x", "", ""); err == nil {
		t.Fatal("空目标语言应报错")
	}
}

func TestDeepLX(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/translate" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Write([]byte(`{"code":200,"data":"Hello world"}`))
	}))
	defer ts.Close()
	got, err := DeepLX(context.Background(), ts.URL, "你好世界", "zh", "en")
	if err != nil || got != "Hello world" {
		t.Fatalf("DeepLX=%q err=%v", got, err)
	}
}

func TestChain(t *testing.T) {
	// google 失败(403)→ deeplx 兜底:两路共用同一 mock,按路径区分。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "translate_a") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"code":200,"data":"fallback ok"}`))
	}))
	defer ts.Close()
	testGoogleBase = ts.URL
	t.Cleanup(func() { testGoogleBase = "" })
	chain := Chain(ts.URL)
	got, err := chain(context.Background(), "文本", "", "en")
	if err != nil || got != "fallback ok" {
		t.Fatalf("chain=%q err=%v", got, err)
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
