package volcengine

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// newTranslateMock 一次成功翻译响应的 mock 服务端。
func newTranslateMock(t *testing.T, translation, detected string, usage MTUsage) *ttsLongMockServer {
	return newMTMockServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		item := map[string]any{"translation": translation, "usage": map[string]any{
			"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens, "total_tokens": usage.TotalTokens,
		}}
		if detected != "" {
			item["detected_source_language"] = detected
		}
		writeJSON(w, map[string]any{
			"code": 20000000, "message": "ok",
			"data": map[string]any{"translation_list": []map[string]any{item}},
		})
	})
}

func TestTranslateToolRun(t *testing.T) {
	m := newTranslateMock(t, "ByteDance is committed to inspiring creativity", "", MTUsage{31, 20, 51})
	outDir := t.TempDir()
	tool := NewTranslateToolWithBaseURL(SpeechCred{APIKey: "key-1"}, outDir, m.srv.URL)

	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "字节跳动致力于激发创造、丰富生活", "target_language": "en",
	}}, func(progress int, note string, detail map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}

	// 请求：单文本列表 + 固定资源头
	req := m.request(0)
	if req.body["target_language"] != "en" {
		t.Errorf("target_language = %v", req.body["target_language"])
	}
	list, _ := req.body["text_list"].([]any)
	if len(list) != 1 || list[0] != "字节跳动致力于激发创造、丰富生活" {
		t.Errorf("text_list = %v", req.body["text_list"])
	}

	// 产物：translate/*.txt 落盘，内容为译文
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	a := out.Artifacts[0]
	if a.Kind != "translation" || a.Format != "txt" {
		t.Errorf("artifact = %+v", a)
	}
	abs := filepath.Join(outDir, a.Path)
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读取译文产物失败: %v", err)
	}
	if string(raw) != "ByteDance is committed to inspiring creativity" {
		t.Errorf("译文内容 = %q", string(raw))
	}

	// 摘要：译文正文（Web 结果区与 CLI --json 都依赖它，缺失则页面只有产物没有文本）+ 用量与字符数
	if got := out.Summary["translation"]; got != "ByteDance is committed to inspiring creativity" {
		t.Errorf("summary.translation = %v, 必须携带译文正文", got)
	}
	if out.Summary["total_tokens"] != 51 || out.Summary["char_count"] != 16 {
		t.Errorf("summary = %v", out.Summary)
	}
	if out.Summary["source_language"] != "" || out.Summary["target_language"] != "en" {
		t.Errorf("summary 语言字段 = %v", out.Summary)
	}
	if _, ok := out.Summary["detected_source_language"]; ok {
		t.Errorf("未自动检测时不应有 detected_source_language: %v", out.Summary)
	}
}

func TestTranslateToolAutoDetectAndTerms(t *testing.T) {
	m := newTranslateMock(t, "火山引擎提供云服务", "en", MTUsage{40, 15, 55})
	tool := NewTranslateToolWithBaseURL(SpeechCred{APIKey: "key-1"}, t.TempDir(), m.srv.URL)

	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "Volcengine provides cloud services", "target_language": "zh",
		"source_language":   "", // 显式空串 = 自动检测
		"terms":             "Volcengine=火山引擎, cloud=云服务\n API=接口",
		"glossary_table_id": "tbl-9",
	}}, func(progress int, note string, detail map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}

	req := m.request(0)
	if _, ok := req.body["source_language"]; ok {
		t.Errorf("空 source_language 不应下发: %v", req.body)
	}
	corpus, ok := req.body["corpus"].(map[string]any)
	if !ok {
		t.Fatalf("corpus 缺失: %v", req.body)
	}
	glossary, _ := corpus["glossary_list"].(map[string]any)
	if len(glossary) != 3 || glossary["Volcengine"] != "火山引擎" || glossary["API"] != "接口" {
		t.Errorf("glossary_list = %v", corpus["glossary_list"])
	}
	if out.Summary["terms_count"] != 3 || out.Summary["detected_source_language"] != "en" {
		t.Errorf("summary = %v", out.Summary)
	}
	if out.Summary["translation"] != "火山引擎提供云服务" {
		t.Errorf("summary.translation = %v", out.Summary["translation"])
	}
}

func TestTranslateToolParamErrors(t *testing.T) {
	m := newTranslateMock(t, "x", "", MTUsage{})
	tool := NewTranslateToolWithBaseURL(SpeechCred{APIKey: "key-1"}, t.TempDir(), m.srv.URL)

	cases := []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"target_language": "en"}, "缺少必填参数: text"},
		{map[string]any{"text": "你好"}, "缺少必填参数: target_language"},
		{map[string]any{"text": "你好", "target_language": "aa"}, "仅支持官方 32 种语言代码"},
		{map[string]any{"text": "你好", "target_language": "en", "source_language": "xx"}, "仅支持官方 32 种语言代码"},
		{map[string]any{"text": "你好", "target_language": "en", "terms": "缺译词"}, "术语格式错误"},
		{map[string]any{"text": "你好", "target_language": "en", "terms": "a="}, "术语格式错误"},
	}
	for _, tc := range cases {
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: tc.params}, func(int, string, map[string]any) {})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("params %v: err = %v, 应含 %q", tc.params, err, tc.want)
		}
	}
	// 参数错误不应发起上游请求
	if n := len(m.reqs); n != 0 {
		t.Errorf("参数错误不应请求上游，实际 %d 次", n)
	}
}

func TestTranslateToolOutRedirect(t *testing.T) {
	m := newTranslateMock(t, "hello", "", MTUsage{1, 1, 2})
	outFile := filepath.Join(t.TempDir(), "custom", "result.txt")
	tool := NewTranslateToolWithBaseURL(SpeechCred{APIKey: "key-1"}, t.TempDir(), m.srv.URL)

	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "你好", "target_language": "en", "_out": outFile,
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if out.Artifacts[0].Path != outFile {
		t.Errorf("path = %q, want %q（_out 绝对路径原样回填）", out.Artifacts[0].Path, outFile)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil || string(raw) != "hello" {
		t.Errorf("raw = %q, err = %v", string(raw), err)
	}
}
