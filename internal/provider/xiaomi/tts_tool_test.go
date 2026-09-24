package xiaomi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// ttsFixture 非流式响应夹具：choices[0].message.audio.data 为 base64。
func ttsFixture(dataB64 string) string {
	return `{"choices":[{"message":{"role":"assistant","audio":{"data":"` + dataB64 + `"}}}]}`
}

// tts 工具：默认参数补全（voice=mimo_default/format=wav）、产物落盘、_out 重定向。
func TestTTSToolRun(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
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
		t.Errorf("产物格式应取请求 format 默认 wav, got %q", res.Artifacts[0].Format)
	}
	if res.Summary["voice"] != DefaultVoice || res.Summary["model"] != ModelPreset {
		t.Errorf("Summary 默认值不符: %+v", res.Summary)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || string(raw) != "ABCDEF" {
		t.Errorf("音频落盘不符: %q err=%v", raw, err)
	}
	for _, want := range []string{`"voice":"mimo_default"`, `"format":"wav"`, `"content":"你好"`} {
		if !strings.Contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
}

// voice 仅预置音色模型下发；voicedesign 音色描述进 user 消息且不携带 voice，
// 缺音色描述在工具层前置拦截（上游要求 voicedesign 必须有 user 消息）。
func TestTTSToolVoiceGating(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(ttsFixture("QUJD")))
	}))
	defer srv.Close()
	tool := NewTTSTool("sk-test", t.TempDir())
	tool.client = NewTTSClient("sk-test", srv.URL)

	if _, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "model": ModelVoiceDesign},
	}, func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "音色描述") {
		t.Errorf("voicedesign 缺音色描述应前置报错, got %v", err)
	}

	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好", "model": ModelVoiceDesign, "voice": "冰糖",
			"instructions": "低沉缓慢的男声"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, `"voice"`) {
		t.Errorf("voicedesign 不应携带 audio.voice:\n%s", body)
	}
	if !strings.Contains(body, `"content":"低沉缓慢的男声"`) {
		t.Errorf("音色描述应进 user 消息:\n%s", body)
	}
	if res.Summary["voice"] != "" {
		t.Errorf("voicedesign Summary.voice 应为空, got %v", res.Summary["voice"])
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
		Params: map[string]any{"text": "你好", "_out": outPath, "format": "mp3"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Path != outPath {
		t.Fatalf("outDir 外的绝对 _out 应原样保留绝对路径: %+v", res.Artifacts)
	}
	if res.Artifacts[0].Format != "mp3" {
		t.Errorf("显式 format 应透传为产物格式, got %q", res.Artifacts[0].Format)
	}
	if raw, err := os.ReadFile(outPath); err != nil || string(raw) != "ABCDEF" {
		t.Errorf("音频应落盘到指定绝对路径: %q err=%v", raw, err)
	}
}

// 缺 text / 未配置凭证：参数错误先行（与 qianwen 同序）。
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

// ParamSpecs 约束：voice 枚举含默认音色与中文音色；format 枚举仅 wav|mp3；Meta 归属 xiaomi.tts。
func TestTTSToolSpecs(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if m := tool.Meta(); m.Provider != "xiaomi" || m.Name != "tts" {
		t.Errorf("Meta = %+v", tool.Meta())
	}
	hasDefault, hasZh, formatOK := false, false, false
	for _, s := range tool.ParamSpecs() {
		if s.Key == "voice" {
			for _, o := range s.Options {
				if o.Value == DefaultVoice {
					hasDefault = true
				}
				if o.Value == "冰糖" {
					hasZh = true
				}
			}
		}
		if s.Key == "format" {
			formatOK = len(s.Options) == 2
		}
	}
	if !hasDefault || !hasZh {
		t.Error("voice 枚举应含默认音色与中文预置音色")
	}
	if !formatOK {
		t.Error("format 枚举应为 wav|mp3 两项")
	}
}
