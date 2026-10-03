package server

// /api/subtitles/translate SSE 协议测试：引擎经 subtitleTranslate 测试缝替换为假实现
// （离线跑协议，不外联）；langs 端点直连清单。服务构造与鉴权同 refine_test.go 手法。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/subtitle"
	"github.com/yann0917/voxbox/internal/translate"
)

// setSubtitleTranslate 替换引擎测试缝并在测试结束还原（refineStream 同做法）。
func setSubtitleTranslate(t *testing.T, fn func(ctx context.Context, cfg *config.Config, segs []subtitle.Segment, o translate.Options, onProgress translate.Progress) (*translate.Result, error)) {
	t.Helper()
	orig := subtitleTranslate
	t.Cleanup(func() { subtitleTranslate = orig })
	subtitleTranslate = fn
}

// readAll 读全响应体（SSE/JSON 包络共用）。
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestSubtitleTranslateSSE 全流程：请求定形（target_language/segments 透传引擎）、
// progress 帧 → done 终帧（segments 含 translation、stats 六键）。假实现只回放，
// 收到的 options 与 segments 在响应后断言（handler 在服务 goroutine 运行）。
func TestSubtitleTranslateSSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)
	defer ts.Close()

	var gotOpts translate.Options
	var gotSegs []subtitle.Segment
	setSubtitleTranslate(t, func(ctx context.Context, cfg *config.Config, segs []subtitle.Segment, o translate.Options, onProgress translate.Progress) (*translate.Result, error) {
		gotOpts, gotSegs = o, segs
		onProgress(1, 2)
		return &translate.Result{
			Segments: []subtitle.Segment{
				{Text: "一", Translation: "one", StartMS: 0, EndMS: 1000},
				{Text: "二", Translation: "two", StartMS: 1000, EndMS: 2000},
			},
			Stats: translate.Stats{Total: 2, Translated: 2, Source: "free"},
		}, nil
	})

	resp, err := ac.Post(ts.URL+"/api/subtitles/translate", "application/json",
		strings.NewReader(`{"segments":[{"text":"一","start_ms":0,"end_ms":1000},{"text":"二","start_ms":1000,"end_ms":2000}],"target_language":"en","source":"free"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := readAll(t, resp)

	if gotOpts.TargetLang != "en" || gotOpts.Source != "free" {
		t.Errorf("引擎收到的 options = %+v", gotOpts)
	}
	if len(gotSegs) != 2 || gotSegs[0].Text != "一" || gotSegs[1].EndMS != 2000 {
		t.Errorf("引擎收到的 segments = %+v", gotSegs)
	}
	if !strings.Contains(raw, `"progress":{"done":1,"total":2}`) {
		t.Errorf("缺 progress 帧: %s", raw)
	}
	if !strings.Contains(raw, `"translation":"one"`) || !strings.Contains(raw, `"source":"free"`) {
		t.Errorf("缺结果: %s", raw)
	}
	if !strings.Contains(raw, `"done":true`) {
		t.Errorf("缺终止帧: %s", raw)
	}
}

// TestSubtitleTranslateBadReq 预检错误走 JSON 包络（不换 SSE）：空 segments。
func TestSubtitleTranslateBadReq(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)
	defer ts.Close()
	resp, _ := ac.Post(ts.URL+"/api/subtitles/translate", "application/json",
		strings.NewReader(`{"segments":[]}`))
	body := readAll(t, resp)
	if !strings.Contains(body, "message") || resp.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("预检错误应走 JSON 包络: %s ct=%s", body, resp.Header.Get("Content-Type"))
	}
}

// TestSubtitleLangs 语言清单：火山 32 语种（三源统一口径），含 zh-Hant。
// 先对原始响应体做大小写敏感断言锁线格式（Go JSON 解码大小写不敏感，
// 若仅解码会漏掉 MTLang json tag 被删后线格式退回大写 Code/Name 的情况）。
func TestSubtitleLangs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)
	defer ts.Close()
	resp, err := ac.Get(ts.URL + "/api/subtitles/langs")
	if err != nil {
		t.Fatal(err)
	}
	raw := readAll(t, resp)
	for _, key := range []string{`"code":`, `"name":`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("langs 响应缺小写键 %s: %s", key, raw)
		}
	}
	for _, key := range []string{`"Code":`, `"Name":`} {
		if strings.Contains(raw, key) {
			t.Fatalf("langs 响应出现大写键 %s（MTLang json tag 缺失）: %s", key, raw)
		}
	}
	var payload struct {
		Code int `json:"code"`
		Data []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 || len(payload.Data) < 30 {
		t.Fatalf("langs=%+v", payload)
	}
	found := false
	for _, l := range payload.Data {
		if l.Code == "zh-Hant" {
			found = true
		}
	}
	if !found {
		t.Fatal("语言清单缺 zh-Hant")
	}
}
