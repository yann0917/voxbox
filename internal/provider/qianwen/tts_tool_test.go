package qianwen

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yann0917/voxbox/internal/provider"
)

// 真机校准后的合成响应夹具：output.audio.data 为 base64（工具层统一走 data URI 解码）。
func ttsFixture(dataB64 string) string {
	return `{"status_code":200,"output":{"finish_reason":"stop","audio":{"url":"","data":"` + dataB64 + `"}}}`
}

// tts 工具：默认参数补全（voice=Cherry/model=flash）、产物落盘、_out 重定向；
// 产物格式以上游实际容器为准（夹具 data URI 为 wav）。
func TestTTSToolRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ttsFixture("QUJDREVG"))) // "ABCDEF"
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewTTSTool("sk-test", out)
	tool.client = NewTTSClient("sk-test", srv.URL) // 注入测试地址

	outPath := filepath.Join(out, "custom.wav")
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "_out": outPath},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Kind != "audio" {
		t.Fatalf("产物不符: %+v", res.Artifacts)
	}
	if res.Artifacts[0].Path != "custom.wav" {
		t.Errorf("产物路径 = %q（_out 重定向后应为相对 out 的 custom.wav）", res.Artifacts[0].Path)
	}
	if res.Artifacts[0].Format != "wav" {
		t.Errorf("产物格式应取上游实际容器 wav, got %q", res.Artifacts[0].Format)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || string(raw) != "ABCDEF" {
		t.Errorf("音频落盘不符: %q err=%v", raw, err)
	}
}

// instructions 仅 instruct 模型下发；flash 请求体不得携带 instructions。
func TestTTSToolInstructionsGating(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		body = string(raw)
		_, _ = w.Write([]byte(ttsFixture("QUJD")))
	}))
	defer srv.Close()
	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client = NewTTSClient("sk-test", srv.URL)

	run := func(model string) {
		t.Helper()
		if _, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"text": "你好", "model": model, "instructions": "语速轻快"},
		}, func(int, string, map[string]any) {}); err != nil {
			t.Fatal(err)
		}
	}
	run("qwen3-tts-flash")
	if strings.Contains(body, "instructions") {
		t.Errorf("flash 模型不应携带 instructions:\n%s", body)
	}
	run("qwen3-tts-instruct-flash")
	if !strings.Contains(body, `"instructions":"语速轻快"`) {
		t.Errorf("instruct 模型应携带 instructions:\n%s", body)
	}
}

// 绝对 _out 指向 outDir 外：Path 保持该绝对路径（相对化会回算 ../ 逃逸路径，
// 破坏产物越界防护与 CLI --json 对绝对 path 的消费）。
func TestTTSToolOutOutsideDataDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ttsFixture("QUJDREVG")))
	}))
	defer srv.Close()
	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client = NewTTSClient("sk-test", srv.URL) // 注入测试地址

	outPath := filepath.Join(t.TempDir(), "outside.wav") // 另一目录（outDir 外）
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "_out": outPath},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Path != outPath {
		t.Fatalf("outDir 外的绝对 _out 应原样保留绝对路径: %+v", res.Artifacts)
	}
	if raw, err := os.ReadFile(outPath); err != nil || string(raw) != "ABCDEF" {
		t.Errorf("音频应落盘到指定绝对路径: %q err=%v", raw, err)
	}
}

// 缺 text / 超 600 字符 / 未配置凭证：参数错误先行（与 volcengine 同序）。
func TestTTSToolValidation(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("缺 text 应报参数错误, got %v", err)
	}
	long := strings.Repeat("字", qwenTTSMaxChars+1)
	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": long}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "600") {
		t.Errorf("超长文本应报 600 字符上限, got %v", err)
	}
	if n := utf8.RuneCountInString(strings.Repeat("字", qwenTTSMaxChars)); n != qwenTTSMaxChars {
		t.Fatalf("边界自检失败: %d", n)
	}
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "hi"}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Errorf("缺凭证应报配置指引, got %v", err)
	}
	if !errors.Is(err, ErrNoCred) {
		t.Errorf("缺凭证错误应包装 ErrNoCred 哨兵（CLI/服务端按其映射退出码 4）, got %v", err)
	}
}

// ParamSpecs 约束：voice 枚举含 Cherry；无 format 参数（上游无该字段）；Meta 的 provider 为 qianwen。
func TestTTSToolSpecs(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if m := tool.Meta(); m.Provider != "qianwen" || m.Name != "tts" {
		t.Errorf("Meta = %+v", tool.Meta())
	}
	found, hasFormat := false, false
	for _, s := range tool.ParamSpecs() {
		if s.Key == "format" {
			hasFormat = true
		}
		if s.Key == "voice" {
			for _, o := range s.Options {
				if o.Value == "Cherry" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("voice 枚举应含 Cherry")
	}
	if hasFormat {
		t.Error("上游无 format 参数，ParamSpecs 不应暴露")
	}
}
