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

	"github.com/yann0917/voxbox/internal/provider"
)

// tts 工具：默认参数补全（voice=Cherry/model=flash/format=mp3）、产物落盘、_out 重定向。
func TestTTSToolRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"audio":"data:audio/mpeg;base64,QUJDREVG"}]}}]}}`))
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewTTSTool("sk-test", out)
	tool.client = NewTTSClient("sk-test", srv.URL) // 注入测试地址

	outPath := filepath.Join(out, "custom.mp3")
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "_out": outPath},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Kind != "audio" {
		t.Fatalf("产物不符: %+v", res.Artifacts)
	}
	if res.Artifacts[0].Path != "custom.mp3" {
		t.Errorf("产物路径 = %q（_out 重定向后应为相对 out 的 custom.mp3）", res.Artifacts[0].Path)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || string(raw) != "ABCDEF" {
		t.Errorf("音频落盘不符: %q err=%v", raw, err)
	}
}

// 绝对 _out 指向 outDir 外：Path 保持该绝对路径（相对化会回算 ../ 逃逸路径，
// 破坏产物越界防护与 CLI --json 对绝对 path 的消费）。
func TestTTSToolOutOutsideDataDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"audio":"data:audio/mpeg;base64,QUJDREVG"}]}}]}}`))
	}))
	defer srv.Close()
	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client = NewTTSClient("sk-test", srv.URL) // 注入测试地址

	outPath := filepath.Join(t.TempDir(), "outside.mp3") // 另一目录（outDir 外）
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

// 缺 text / 未配置凭证：参数错误先行（与 volcengine 同序）。
func TestTTSToolValidation(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("缺 text 应报参数错误, got %v", err)
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

// ParamSpecs 约束：voice 枚举含 Cherry；Meta 的 provider 为 qianwen。
func TestTTSToolSpecs(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if m := tool.Meta(); m.Provider != "qianwen" || m.Name != "tts" {
		t.Errorf("Meta = %+v", tool.Meta())
	}
	found := false
	for _, s := range tool.ParamSpecs() {
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
}
