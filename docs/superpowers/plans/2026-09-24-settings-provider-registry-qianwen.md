# 设置页云端/本地重构 + Provider 元数据注册表 + 千问接入 · 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 设置页重构为「云端服务 / 本地环境」两 Tab（云端凭证卡由后端 Provider 元数据注册表动态驱动），并接入千问平台 TTS（qwen3-tts-flash 非流式）与 ASR（filetrans 文件转写），TTS/ASR 功能页与 CLI 增加引擎选择。

**Architecture:** `internal/provider` 新增 `ProviderInfo` 元数据层（各 provider 包声明凭证卡，service 装配估值）；设置 API 改为按卡读写（`PUT /api/settings/providers/<name>` / `PUT /api/settings/storage`），旧平铺 `PUT /api/settings` 删除。千问作为新 provider 包 `internal/provider/qianwen`（tts + asr 两工具，DashScope 协议，固定 BaseURL），对象存储桥与 SRT 构建器从 volcengine 抽到 provider 包共享。前端 SettingsPage 按 providers 数组动态渲染，TTSPage/ASRPage 加引擎切换。

**Tech Stack:** Go 1.26 + gin + viper（后端）；React + TypeScript + vite + @tanstack/react-query（前端）；httptest（Go 测试）。

**Spec:** `docs/superpowers/specs/2026-09-24-settings-provider-registry-qianwen-design.md`

## Global Constraints

- provider 命名统一 `qianwen`（注册表键 / 任务提交 `provider` 字段 / CLI `--engine` 值）。
- 千问 BaseURL 固定 `https://maas.qianwenaiapi.com`，**不提供** `base_url` 配置项。
- 配置落盘键零迁移：`volc.*`、`mvsep.*`、`storage.*` 全部沿用；新增仅 `qianwen.api_key`。
- secret 字段语义：GET 只回 `has_value`；PUT 空串 = 不修改。
- 旧 `PUT /api/settings` 端点整体删除（无兼容期）；GET 响应删除 `volc`/`mvsep` 平铺段，保留 `storage` + `data_dir`，新增 `providers`。
- 所有用户可见文案用中文。
- 后端验证 `go test ./...`；前端验证 `cd web && npm run build`（tsc -b + vite build）。
- 非目标：千问流式/实时/CosyVoice/克隆/设计/SSML、ASR 同步与实时模式、新存储通道类型、`/api/voices` 扩展。

---

### Task 1: provider 包元数据类型 + 公共 SRT 构建器

**Files:**
- Create: `internal/provider/providersettings.go`
- Create: `internal/provider/srt.go`
- Test: `internal/provider/providersettings_test.go`（约束测试并入此文件）

**Interfaces:**
- Produces: `provider.ProviderKind`（`KindCloud`/`KindLocal`）、`provider.FieldKind`（`FieldText`/`FieldSecret`/`FieldSelect`）、`provider.CredentialField`、`provider.ProviderInfo`；`provider.SRTSegment`、`provider.BuildSRT([]SRTSegment) string`（Task 5 消费）。

- [ ] **Step 1: 写失败测试**

`internal/provider/providersettings_test.go`：

```go
package provider

import "testing"

// ValidateCard 卡声明约束：云端卡至少一个字段、每个字段 ConfigKey 非空、secret 字段必为 Required 语义基线、
// select 字段必须给 Options、本地卡不允许带字段。
func TestValidateCard(t *testing.T) {
	cases := []struct {
		name string
		card ProviderInfo
		want string // 期望的错误子串，空=合法
	}{
		{"合法云端卡", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud, Order: 10,
			Fields: []CredentialField{
				{Key: "api_key", Label: "API Key", Kind: FieldSecret, ConfigKey: "demo.api_key"},
			},
		}, ""},
		{"云端卡无字段", ProviderInfo{Name: "demo", Title: "演示", Kind: KindCloud}, "云端卡至少需要 1 个凭证字段"},
		{"字段缺 ConfigKey", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud,
			Fields: []CredentialField{{Key: "k", Label: "K", Kind: FieldText}},
		}, "ConfigKey 不能为空"},
		{"select 无选项", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud,
			Fields: []CredentialField{{Key: "line", Label: "线路", Kind: FieldSelect, ConfigKey: "demo.line"}},
		}, "select 字段必须提供 Options"},
		{"本地卡带字段", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindLocal,
			Fields: []CredentialField{{Key: "k", Label: "K", Kind: FieldText, ConfigKey: "demo.k"}},
		}, "本地能力卡不应有凭证字段"},
		{"卡名空", ProviderInfo{Title: "演示", Kind: KindCloud}, "Name 不能为空"},
	}
	for _, tc := range cases {
		err := tc.card.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: 期望合法，报错 %v", tc.name, err)
			}
			continue
		}
		if err == nil || !contains(err.Error(), tc.want) {
			t.Errorf("%s: 期望错误含 %q，得到 %v", tc.name, tc.want, err)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0))
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestBuildSRT(t *testing.T) {
	got := BuildSRT([]SRTSegment{
		{StartMS: 0, EndMS: 1500, Text: "你好"},
		{StartMS: 1500, EndMS: 62000, Text: "世界"},
	})
	want := "1\n00:00:00,000 --> 00:00:01,500\n你好\n\n2\n00:00:01,500 --> 00:01:02,000\n世界\n"
	if got != want {
		t.Errorf("BuildSRT 输出不符:\n得到:\n%s\n期望:\n%s", got, want)
	}
}
```

（`contains`/`indexOf` 若嫌啰嗦可直接用 `strings.Contains`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/provider/ -run 'TestValidateCard|TestBuildSRT' -v`
Expected: FAIL（`ProviderInfo`/`Validate`/`BuildSRT` 未定义，编译错误）

- [ ] **Step 3: 实现**

`internal/provider/providersettings.go`：

```go
package provider

import "fmt"

// ProviderKind 设置页的分组维度：云端服务（凭证卡，可配置可探活）与本地能力（只读展示）。
type ProviderKind string

const (
	KindCloud ProviderKind = "cloud"
	KindLocal ProviderKind = "local"
)

// FieldKind 凭证字段的表现形态：text 明文输入、secret 敏感输入（回显 has_value、保存留空=不改）、
// select 下拉（Options 给全量可选项）。
type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldSecret FieldKind = "secret"
	FieldSelect FieldKind = "select"
)

// CredentialField 一张凭证卡上的单个字段声明。Key 是 API 载荷键，ConfigKey 是 config.yaml
// 落盘键——两者解耦：mediakit 卡的字段 Key=api_key，落盘仍是 volc.mediakit.api_key。
type CredentialField struct {
	Key         string
	Label       string
	Kind        FieldKind
	ConfigKey   string
	Options     []ParamOption
	Required    bool
	Placeholder string
	Hint        string
}

// ProviderInfo 设置页一张卡的声明。卡声明属静态描述，当前值/已配置状态由 service 装配。
type ProviderInfo struct {
	Name        string
	Title       string
	Description string
	Kind        ProviderKind
	Fields      []CredentialField
	Order       int
}

// Validate 声明合法性：启动/测试路径防呆，非法声明属编程错误。
func (p ProviderInfo) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("卡声明 Name 不能为空（Title=%q）", p.Title)
	}
	if p.Kind == KindLocal {
		if len(p.Fields) > 0 {
			return fmt.Errorf("本地能力卡 %s 不应有凭证字段", p.Name)
		}
		return nil
	}
	if len(p.Fields) == 0 {
		return fmt.Errorf("云端卡 %s 至少需要 1 个凭证字段", p.Name)
	}
	for _, f := range p.Fields {
		if f.Key == "" || f.ConfigKey == "" {
			return fmt.Errorf("卡 %s 字段 %q 的 Key/ConfigKey 不能为空", p.Name, f.Key)
		}
		if f.Kind == FieldSelect && len(f.Options) == 0 {
			return fmt.Errorf("卡 %s select 字段 %q 必须提供 Options", p.Name, f.Key)
		}
	}
	return nil
}
```

`internal/provider/srt.go`：

```go
package provider

import (
	"fmt"
	"strings"
)

// SRTSegment 通用字幕分句：毫秒时间轴 + 文本（ASR 类工具的产物中间形态）。
type SRTSegment struct {
	StartMS int64
	EndMS   int64
	Text    string
}

// BuildSRT 将分句时间戳渲染为标准 SRT 字幕：序号从 1 起，
// 时间轴格式 HH:MM:SS,mmm --> HH:MM:SS,mmm，条目间空行分隔。
func BuildSRT(segments []SRTSegment) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n",
			i+1, srtTimestamp(seg.StartMS), srtTimestamp(seg.EndMS), seg.Text)
	}
	return b.String()
}

// srtTimestamp 毫秒 → SRT 时间戳 HH:MM:SS,mmm（超 1 小时正常进位）。
func srtTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/provider/ -v`
Expected: PASS（全部，含既有 registry/naming 测试）

- [ ] **Step 5: Commit**

```bash
git add internal/provider/providersettings.go internal/provider/srt.go internal/provider/providersettings_test.go
git commit -m "feat(provider): 设置卡元数据类型与公共 SRT 构建器"
```

---

### Task 2: 抽取对象存储桥到 provider 包

**Files:**
- Create: `internal/provider/storagebridge.go`
- Modify: `internal/provider/volcengine/storage_bridge.go`（整文件删除，保留薄包装）
- Test: 既有 `internal/provider/volcengine/storage_bridge_test.go` 须保持通过；新增 `internal/provider/storagebridge_test.go`

**Interfaces:**
- Produces: `provider.EnsureURLInput(ctx context.Context, in TaskInput, paramKey, label, missingErr string, report ProgressReporter) (string, error)`（Task 5 的 qianwen asr 工具消费）。

- [ ] **Step 1: 搬移代码**

把 `internal/provider/volcengine/storage_bridge.go` 全文（`ensureURLInput`/`bridgeObjectName`/`sanitizeObjectName`/`withUploadProgress` + `bridgePresignTTL` 常量 + 包注释）复制到新文件 `internal/provider/storagebridge.go`，做以下机械改动：
- `package volcengine` → `package provider`
- `ensureURLInput` → `EnsureURLInput`（导出）
- `paramString(in.Params, paramKey)` 改为包内私有实现（volcengine 的 `paramString` 不搬，在文件底部加）：

```go
// paramString 取字符串参数（缺失或类型不符返回空串）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}
```

- 「闲时版/极速版依赖 URL 扩展名推断格式」这处文案改为「URL 版工具依赖扩展名推断格式」（跨厂商通用）。

`internal/provider/volcengine/storage_bridge.go` 整文件替换为薄包装（调用点 asr_tool×3 / lark_tool / separate_tool 不动）：

```go
package volcengine

import (
	"context"

	"github.com/yann0917/voxbox/internal/provider"
)

// ensureURLInput URL-only 工具的本地文件桥，实现已上移 provider 包（qianwen 等其他
// provider 共用），此处保留同名薄包装，包内 5 处调用点零改动。
func ensureURLInput(ctx context.Context, in provider.TaskInput, paramKey, label, missingErr string, report provider.ProgressReporter) (string, error) {
	return provider.EnsureURLInput(ctx, in, paramKey, label, missingErr, report)
}
```

- [ ] **Step 2: 搬移测试并适配**

把 `internal/provider/volcengine/storage_bridge_test.go` 的用例搬一份到 `internal/provider/storagebridge_test.go`（`package provider`，`ensureURLInput` → `EnsureURLInput`）。若 volcengine 侧原测试断言含旧文案「闲时版/极速版」，同步更新字符串。

- [ ] **Step 3: 运行确认通过**

Run: `go test ./internal/provider/... -v`
Expected: PASS（含 volcengine 既有全部测试——桥行为不变只是搬家）

- [ ] **Step 4: Commit**

```bash
git add internal/provider/storagebridge.go internal/provider/storagebridge_test.go internal/provider/volcengine/storage_bridge.go internal/provider/volcengine/storage_bridge_test.go
git commit -m "refactor(provider): 对象存储桥上移 provider 包供多厂商共用"
```

---

### Task 3: config 增加 Qianwen 段

**Files:**
- Modify: `internal/config/config.go`（Config 结构 + configFromViper）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config.Qianwen.QianwenConfig`（`APIKey string` 字段；Task 7 消费）。

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 追加（沿用该文件既有的临时配置文件测试模式；若无写盘辅助函数，用 `t.TempDir()` + `os.Setenv("VOXBOX_HOME", dir)` 并 `defer os.Unsetenv`）：

```go
func TestLoadQianwen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VOXBOX_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".voxbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "qianwen:\n  api_key: sk-test-123\n"
	if err := os.WriteFile(filepath.Join(dir, ".voxbox", "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Qianwen.APIKey != "sk-test-123" {
		t.Errorf("Qianwen.APIKey = %q, 期望 sk-test-123", cfg.Qianwen.APIKey)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/config/ -run TestLoadQianwen -v`
Expected: FAIL（编译错误：`cfg.Qianwen` 未定义）

- [ ] **Step 3: 实现**

`internal/config/config.go` 三处改动：

Config 结构体（`MVSep MVSepConfig` 字段后）加：

```go
	// Qianwen 千问平台（platform.qianwenai.com）凭证：语音合成/识别共用一个 API Key，
	// BaseURL 固定官方 maas.qianwenaiapi.com，不提供覆写。
	Qianwen QianwenConfig
```

与 `MVSepConfig` 并列处加：

```go
type QianwenConfig struct{ APIKey string }
```

`configFromViper` 返回值构造中（`MVSep: ...` 后）加：

```go
		Qianwen: QianwenConfig{APIKey: strings.TrimSpace(v.GetString("qianwen.api_key"))},
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): 读取 qianwen.api_key 凭证"
```

---

### Task 4: qianwen 包 · TTS client + 音色表 + tts 工具

**Files:**
- Create: `internal/provider/qianwen/card.go`（卡声明，Task 6 的 providerCards 消费）
- Create: `internal/provider/qianwen/client.go`（BaseURL/路径常量 + TTS 请求响应结构）
- Create: `internal/provider/qianwen/voices.go`（音色静态表）
- Create: `internal/provider/qianwen/tts_tool.go`（含 ParamSpecs + Run）
- Test: `internal/provider/qianwen/tts_client_test.go`、`internal/provider/qianwen/tts_tool_test.go`

**Interfaces:**
- Consumes: 无（首个 qianwen 文件）
- Produces: `qianwen.BaseURL` 常量；`qianwen.ProviderCard() provider.ProviderInfo`；`qianwen.NewTTSTool(apiKey, outDir string) *TTSTool`（实现 provider.Tool）；`qianwen.TTSClient`（`NewTTSClient(apiKey, baseURL string)`，`Synthesize(ctx, TTSReq) (TTSResult, error)`）；`qianwen.VoiceOptions() []provider.ParamOption`。

- [ ] **Step 0（可选）: 真机校准 wire 格式**

文档只给了 SDK 参数形态。若手头有千问 API Key：

```bash
source ~/.voxbox/config.yaml 提取或 export QW_KEY=sk-...
curl -sS -X POST "https://maas.qianwenaiapi.com/api/v1/services/aigc/multimodal-generation/generation" \
  -H "Authorization: Bearer $QW_KEY" -H "Content-Type: application/json" \
  -d '{"model":"qwen3-tts-flash","input":{"messages":[{"role":"user","content":[{"text":"你好"},{"voice":"Cherry"}]}]},"parameters":{"stream":false}}'
```

记录响应 JSON 中音频载体（预期 `output.choices[0].message.content[]` 内含 `audio` 键，值为 URL 或 data URI）。**若实际结构与下方 Step 3 的结构体假设不符，以实测为准调整 `ttsResponse`/`extractAudio` 与测试夹具，其余步骤不变。** 无 Key 则按 DashScope 多模态标准格式实现（测试夹具固化该假设，真机验收在收尾 Task 13）。

- [ ] **Step 1: 写失败测试**

`internal/provider/qianwen/tts_client_test.go`：

```go
package qianwen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TTS 全链路：Bearer 鉴权头、请求体形态（messages 内 text/voice/language_type/instructions）、
// 响应 content 里的音频 URL 被下载为字节。
func TestTTSClientSynthesize(t *testing.T) {
	var auth, body string
	audioBytes := []byte("fake-mp3-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		if r.URL.Path == "/audio.mp3" {
			_, _ = w.Write(audioBytes)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"text":"你好"},{"audio":"` + srvURL(t) + `/audio.mp3"}]}}]}}`))
	}))
	defer srv.Close()

	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{
		Model: "qwen3-tts-flash", Text: "你好", Voice: "Cherry",
		LanguageType: "Chinese", Instructions: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	for _, want := range []string{`"model":"qwen3-tts-flash"`, `"voice":"Cherry"`, `"text":"你好"`, `"language_type":"Chinese"`} {
		if !contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if string(got.Audio) != string(audioBytes) {
		t.Errorf("音频字节不符: %q", got.Audio)
	}
	if got.Format != "mp3" {
		t.Errorf("Format = %q", got.Format)
	}
}

// data URI 音频（content 项为 data:audio/...;base64,...）直接解码，不发起第二次请求。
func TestTTSClientSynthesizeDataURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"audio":"data:audio/wav;base64,QUJD"}]}}]}}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSReq{Model: "qwen3-tts-flash", Text: "hi", Voice: "Cherry"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Audio) != "ABC" {
		t.Errorf("base64 解码不符: %q", got.Audio)
	}
	if got.Format != "wav" {
		t.Errorf("Format = %q", got.Format)
	}
	if hits.Load() != 1 {
		t.Errorf("data URI 不应发起下载请求，hits=%d", hits.Load())
	}
}

// 上游错误（code/message 非空）转为中文错误。
func TestTTSClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key"}`))
	}))
	defer srv.Close()
	c := NewTTSClient("sk-bad", srv.URL)
	if _, err := c.Synthesize(context.Background(), TTSReq{Text: "hi"}); err == nil {
		t.Fatal("期望 401 报错")
	}
}

func srvURL(t *testing.T) string { return "http://" + t.Name() /* 占位，见下 */ }
```

注意：`srvURL` 无法在 handler 闭包外拿到 srv 地址——实现时把第一个测试的 handler 里 `srvURL(t)` 改为直接捕获 `srv.URL`（先声明 `var srv *httptest.Server`，handler 闭包引用 `srv.URL`）。`contains` 用 `strings.Contains`。

`tts_tool_test.go`：

```go
package qianwen

import (
	"context"
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
	tool.baseURL = srv.URL // 注入测试地址

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

// 缺 text / 未配置凭证：参数错误先行（与 volcengine 同序）。
func TestTTSToolValidation(t *testing.T) {
	tool := NewTTSTool("", t.TempDir())
	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("缺 text 应报参数错误, got %v", err)
	}
	if _, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "hi"}},
		func(int, string, map[string]any) {}); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Errorf("缺凭证应报配置指引, got %v", err)
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
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/provider/qianwen/ -v`
Expected: FAIL（包不存在/编译错误）

- [ ] **Step 3: 实现**

`internal/provider/qianwen/card.go`：

```go
// Package qianwen 千问平台（platform.qianwenai.com）语音服务 provider：
// qwen3-tts 非流式合成 + filetrans 文件转写，Bearer API Key 鉴权，BaseURL 固定官方。
package qianwen

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 设置页「千问平台」凭证卡声明。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "qianwen",
		Title: "千问平台 · 语音合成 / 语音识别",
		Description: "阿里千问 AI 平台（platform.qianwenai.com）：qwen3-tts 非流式语音合成与 qwen3-asr 文件转写。" +
			"API Key 在平台控制台「API-KEY 管理」创建。",
		Kind:  provider.KindCloud,
		Order: 20,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "qianwen.api_key", Placeholder: "sk-…（控制台创建）",
				Hint: "语音合成与语音识别共用，Bearer 鉴权"},
		},
	}
}
```

`internal/provider/qianwen/client.go`：

```go
package qianwen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL 千问平台开放 API 固定入口（DashScope 兼容协议），不提供覆写。
const BaseURL = "https://maas.qianwenaiapi.com"

const (
	pathTTSGen  = "/api/v1/services/aigc/multimodal-generation/generation"
	pathASRSub  = "/api/v1/services/audio/asr/transcription"
	pathTaskFmt = "/api/v1/tasks/%s"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// apiError DashScope 风格错误体。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Request string `json:"request_id"`
}

func (e apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("千问 API 错误: %s", e.Code)
	}
	return fmt.Sprintf("千问 API 错误: %s（%s）", e.Message, e.Code)
}

// doJSON 发请求/收 JSON：非 2xx 或 body 带 code/message 错误时返回 apiError。
func doJSON(ctx context.Context, method, url, apiKey string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求千问平台失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var ae apiError
		_ = json.Unmarshal(raw, &ae)
		if ae.Code == "" {
			return fmt.Errorf("千问 API HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析千问响应失败: %w", err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// ---- TTS ----

// TTSReq 非流式合成请求（工具层语义字段，client 负责映射为 messages 协议）。
type TTSReq struct {
	Model        string // qwen3-tts-flash | qwen3-tts-instruct-flash
	Text         string
	Voice        string
	LanguageType string // 可空=不指定
	Instructions string // 仅 instruct 模型
}

// TTSResult 合成产物：音频字节与容器格式（按 data URI mime 或 URL 扩展名推断）。
type TTSResult struct {
	Audio  []byte
	Format string
}

type ttsRequest struct {
	Model      string        `json:"model"`
	Input      ttsInput      `json:"input"`
	Parameters map[string]any `json:"parameters,omitempty"`
}
type ttsInput struct {
	Messages []ttsMessage `json:"messages"`
}
type ttsMessage struct {
	Role    string          `json:"role"`
	Content []ttsContentItem `json:"content"`
}
type ttsContentItem struct {
	Text         string `json:"text,omitempty"`
	Voice        string `json:"voice,omitempty"`
	LanguageType string `json:"language_type,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}
type ttsResponse struct {
	Output struct {
		Choices []struct {
			Message struct {
				Content []map[string]any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`
	apiError
}

// TTSClient 千问非流式 TTS 客户端。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// Synthesize 合成并取回音频字节：响应里的 audio 项为公网 URL（24h 有效）时下载，
// 为 data URI 时直接 base64 解码。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSReq) (TTSResult, error) {
	content := []ttsContentItem{{Text: req.Text}}
	if req.Voice != "" {
		content = append(content, ttsContentItem{Voice: req.Voice})
	}
	if req.LanguageType != "" {
		content = append(content, ttsContentItem{LanguageType: req.LanguageType})
	}
	if req.Instructions != "" {
		content = append(content, ttsContentItem{Instructions: req.Instructions})
	}
	body := ttsRequest{
		Model: req.Model,
		Input: ttsInput{Messages: []ttsMessage{{Role: "user", Content: content}}},
		Parameters: map[string]any{"stream": false},
	}
	var resp ttsResponse
	if err := doJSON(ctx, http.MethodPost, c.baseURL+pathTTSGen, c.apiKey, nil, body, &resp); err != nil {
		return TTSResult{}, err
	}
	for _, ch := range resp.Output.Choices {
		for _, item := range ch.Message.Content {
			v, ok := item["audio"].(string)
			if !ok || v == "" {
				continue
			}
			return fetchAudio(ctx, v)
		}
	}
	return TTSResult{}, fmt.Errorf("千问 TTS 响应中未找到音频（content 项无 audio 键）")
}

// fetchAudio audio 载体两种形态：data URI 直接解码；URL 下载（格式按扩展名推断）。
func fetchAudio(ctx context.Context, v string) (TTSResult, error) {
	if strings.HasPrefix(v, "data:") {
		// data:audio/mpeg;base64,XXXX
		semi := strings.Index(v, ";")
		if semi < 0 || !strings.HasPrefix(v[semi:], ";base64,") {
			return TTSResult{}, fmt.Errorf("无法解析的 data URI 音频")
		}
		mime := strings.TrimPrefix(v[5:semi], "audio/")
		raw, err := base64.StdEncoding.DecodeString(v[semi+len(";base64,"):])
		if err != nil {
			return TTSResult{}, fmt.Errorf("解码音频失败: %w", err)
		}
		return TTSResult{Audio: raw, Format: audioFormatOfMime(mime)}, nil
	}
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return TTSResult{}, fmt.Errorf("音频地址不合法: %s", truncate([]byte(v), 80))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v, nil)
	if err != nil {
		return TTSResult{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return TTSResult{}, fmt.Errorf("下载合成音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TTSResult{}, fmt.Errorf("下载合成音频失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return TTSResult{}, err
	}
	format := strings.TrimPrefix(extOfURL(v), ".")
	if format == "" {
		format = audioFormatOfMime(strings.TrimPrefix(resp.Header.Get("Content-Type"), "audio/"))
	}
	return TTSResult{Audio: raw, Format: format}, nil
}

func audioFormatOfMime(mime string) string {
	switch mime {
	case "mpeg", "mp3":
		return "mp3"
	case "wav", "x-wav", "wave":
		return "wav"
	case "pcm":
		return "pcm"
	}
	return mime
}

func extOfURL(raw string) string {
	u := strings.SplitN(raw, "?", 2)[0]
	if i := strings.LastIndex(u, "."); i >= 0 {
		return strings.ToLower(u[i:])
	}
	return ""
}
```

`internal/provider/qianwen/voices.go`（qwen3-tts 非实时官方音色表，48 个，全部支持 qwen3-tts-flash；来源 platform.qianwenai.com voice-list/qwen-tts）：

```go
package qianwen

import "github.com/yann0917/voxbox/internal/provider"

// qwen3-tts 非实时音色（官方音色列表，值即 voice 名）。
var voices = []provider.ParamOption{
	{Value: "Cherry", Label: "Cherry · 女声 · 阳光积极、亲切自然"},
	{Value: "Serena", Label: "Serena · 女声 · 温柔小姐姐"},
	{Value: "Ethan", Label: "Ethan · 男声 · 阳光温暖的北方口音"},
	{Value: "Chelsie", Label: "Chelsie · 女声 · 二次元虚拟女友"},
	{Value: "Momo", Label: "Momo · 女声 · 撒娇搞怪"},
	{Value: "Vivian", Label: "Vivian · 女声 · 拽拽的可爱小暴躁"},
	{Value: "Moon", Label: "Moon · 男声 · 率性帅气"},
	{Value: "Maia", Label: "Maia · 女声 · 知性与温柔"},
	{Value: "Kai", Label: "Kai · 男声 · 舒缓悦耳"},
	{Value: "Nofish", Label: "Nofish · 男声 · 不会翘舌音的设计师"},
	{Value: "Bella", Label: "Bella · 女声 · 小萝莉"},
	{Value: "Jennifer", Label: "Jennifer · 女声 · 电影质感美语"},
	{Value: "Ryan", Label: "Ryan · 男声 · 节奏拉满、戏感炸裂"},
	{Value: "Katerina", Label: "Katerina · 女声 · 御姐韵律"},
	{Value: "Aiden", Label: "Aiden · 男声 · 精通厨艺的美语大男孩"},
	{Value: "Eldric Sage", Label: "Eldric Sage · 男声 · 沉稳睿智的老者"},
	{Value: "Mia", Label: "Mia · 女声 · 温顺乖巧"},
	{Value: "Mochi", Label: "Mochi · 男声 · 早慧的小大人"},
	{Value: "Bellona", Label: "Bellona · 女声 · 洪亮清晰、江湖豪情"},
	{Value: "Vincent", Label: "Vincent · 男声 · 沙哑烟嗓"},
	{Value: "Bunny", Label: "Bunny · 女声 · 萌属性小萝莉"},
	{Value: "Neil", Label: "Neil · 男声 · 专业新闻主持"},
	{Value: "Elias", Label: "Elias · 女声 · 严谨的知识讲解"},
	{Value: "Arthur", Label: "Arthur · 男声 · 质朴嗓音的乡村老者"},
	{Value: "Nini", Label: "Nini · 女声 · 又软又黏的甜嗓"},
	{Value: "Seren", Label: "Seren · 女声 · 温和舒缓助眠"},
	{Value: "Pip", Label: "Pip · 男声 · 调皮捣蛋充满童真"},
	{Value: "Stella", Label: "Stella · 女声 · 甜腻迷糊少女音"},
	{Value: "Bodega", Label: "Bodega · 男声 · 热情的西班牙大叔"},
	{Value: "Sonrisa", Label: "Sonrisa · 女声 · 热情开朗的拉美大姐"},
	{Value: "Alek", Label: "Alek · 男声 · 俄罗斯风、冷中带暖"},
	{Value: "Dolce", Label: "Dolce · 男声 · 慵懒的意大利大叔"},
	{Value: "Sohee", Label: "Sohee · 女声 · 温柔开朗的韩国欧尼"},
	{Value: "Ono Anna", Label: "Ono Anna · 女声 · 鬼灵精怪的青梅竹马"},
	{Value: "Lenn", Label: "Lenn · 男声 · 理性叛逆的德国青年"},
	{Value: "Emilien", Label: "Emilien · 男声 · 浪漫的法国哥哥"},
	{Value: "Andre", Label: "Andre · 男声 · 磁性沉稳"},
	{Value: "Radio Gol", Label: "Radio Gol · 男声 · 足球解说诗人"},
	{Value: "Jada", Label: "Jada · 女声 · 风风火火的沪上阿姐（上海话）"},
	{Value: "Dylan", Label: "Dylan · 男声 · 北京胡同少年（北京话）"},
	{Value: "Li", Label: "Li · 男声 · 耐心的瑜伽老师（南京话）"},
	{Value: "Marcus", Label: "Marcus · 男声 · 老陕的味道（陕西话）"},
	{Value: "Roy", Label: "Roy · 男声 · 诙谐直爽的台湾哥仔（闽南语）"},
	{Value: "Peter", Label: "Peter · 男声 · 天津相声、专业捧哏（天津话）"},
	{Value: "Sunny", Label: "Sunny · 女声 · 甜到心里的川妹子（四川话）"},
	{Value: "Eric", Label: "Eric · 男声 · 跳脱市井的成都男子（四川话）"},
	{Value: "Rocky", Label: "Rocky · 男声 · 幽默风趣的阿强（粤语）"},
	{Value: "Kiki", Label: "Kiki · 女声 · 甜美的港妹闺蜜（粤语）"},
}

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 与连通性测试共用）。
func VoiceOptions() []provider.ParamOption { return voices }

// DefaultVoice 默认音色。
const DefaultVoice = "Cherry"
```

`internal/provider/qianwen/tts_tool.go`：

```go
package qianwen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// TTSTool 千问非流式语音合成（qwen3-tts-flash / qwen3-tts-instruct-flash）。
type TTSTool struct {
	client  *TTSClient
	apiKey  string
	baseURL string // 默认 BaseURL；测试注入 httptest 地址
	outDir  string
}

func NewTTSTool(apiKey, outDir string) *TTSTool {
	return &TTSTool{client: NewTTSClient(apiKey, BaseURL), apiKey: apiKey, baseURL: BaseURL, outDir: outDir}
}

func (t *TTSTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "qianwen",
		Name:        "tts",
		Title:       "语音合成（千问）",
		Description: "qwen3-tts 非流式合成，48 官方音色，instruct 模型支持自然语言风格指令",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: "qwen3-tts-flash", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "qwen3-tts-flash", Label: "qwen3-tts-flash"},
				{Value: "qwen3-tts-instruct-flash", Label: "qwen3-tts-instruct-flash（支持风格指令）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options: VoiceOptions()},
		{Key: "language_type", Label: "语言", Type: provider.ParamString, Group: "参数",
			Placeholder: "如 Chinese / English；留空不指定"},
		{Key: "instructions", Label: "风格指令", Type: provider.ParamText, Group: "参数",
			Placeholder: "仅 instruct 模型生效：用自然语言描述语速/情感/风格"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum, Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{{Value: "mp3", Label: "MP3"}, {Value: "wav", Label: "WAV"}}},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("未配置千问 API Key：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key")
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = "qwen3-tts-flash"
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}

	report(20, "正在合成", nil)
	res, err := t.client.Synthesize(ctx, TTSReq{
		Model: model, Text: text, Voice: voice,
		LanguageType: paramString(in.Params, "language_type"),
		Instructions: paramString(in.Params, "instructions"),
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	// 上游格式与请求不一致时以实际容器为准
	if res.Format != "" {
		format = res.Format
	}
	report(80, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+format)
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath := relPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.outDir, relPath)
		relPath, _ = filepath.Rel(t.outDir, absPath)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, res.Audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: format,
			Size: int64(len(res.Audio)),
		}},
		Summary: map[string]any{
			"char_count": utf8.RuneCountInString(text),
			"model":      model,
			"voice":      voice,
		},
	}, nil
}

// paramString 与 volcengine 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}
```

注意：`NewTTSTool` 里 `client` 已用 `BaseURL` 构造，测试注入 `tool.baseURL = srv.URL` 时需同步重建 client——把测试注入改为 `tool.client = NewTTSClient("sk-test", srv.URL)` 更直接，实现时以测试写法为准（删 baseURL 字段，只留 client）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/provider/qianwen/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/qianwen/
git commit -m "feat(qianwen): qwen3-tts 非流式合成工具与音色表"
```

---

### Task 5: qianwen 包 · ASR client + asr 工具

**Files:**
- Create: `internal/provider/qianwen/asr_client.go`（client + 协议结构，追加进 client.go 亦可，独立文件更清晰）
- Create: `internal/provider/qianwen/asr_tool.go`
- Test: `internal/provider/qianwen/asr_client_test.go`、`internal/provider/qianwen/asr_tool_test.go`

**Interfaces:**
- Consumes: `provider.EnsureURLInput`（Task 2）、`provider.BuildSRT`/`provider.SRTSegment`（Task 1）
- Produces: `qianwen.NewASRTool(apiKey, outDir string) *ASRTool`（实现 provider.Tool，Task 7 注册消费）。

- [ ] **Step 1: 写失败测试**

`internal/provider/qianwen/asr_client_test.go`：

```go
package qianwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 提交：X-DashScope-Async 头、qwen3 单 URL / qwen-audio 数组两种 input 形态、参数透传。
func TestASRClientSubmit(t *testing.T) {
	var async, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		async = r.Header.Get("X-DashScope-Async")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		_, _ = w.Write([]byte(`{"output":{"task_id":"tid-1","task_status":"PENDING"}}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk-test", srv.URL)
	itn := true
	id, err := c.SubmitTranscription(context.Background(), "qwen3-asr-flash-filetrans",
		"https://a.b/x.mp3", ASRParams{LanguageHints: []string{"zh"}, EnableITN: &itn})
	if err != nil {
		t.Fatal(err)
	}
	if id != "tid-1" {
		t.Errorf("task_id = %q", id)
	}
	if async != "enable" {
		t.Errorf("X-DashScope-Async = %q", async)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "qwen3-asr-flash-filetrans" {
		t.Errorf("model = %v", m["model"])
	}
	in := m["input"].(map[string]any)
	if in["file_url"] != "https://a.b/x.mp3" {
		t.Errorf("file_url = %v", in["file_url"])
	}
	p := m["parameters"].(map[string]any)
	if _, ok := p["language_hints"].([]any); !ok {
		t.Errorf("language_hints 缺失: %v", p)
	}
	if p["enable_itn"] != true {
		t.Errorf("enable_itn = %v", p["enable_itn"])
	}
}

// qwen-audio 模型走 file_urls 数组。
func TestASRClientSubmitQwenAudio(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		_, _ = w.Write([]byte(`{"output":{"task_id":"t","task_status":"PENDING"}}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk", srv.URL)
	if _, err := c.SubmitTranscription(context.Background(), "qwen-audio-3.1-asr-flash-filetrans",
		"https://a.b/x.mp3", ASRParams{}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	in := m["input"].(map[string]any)
	if urls, ok := in["file_urls"].([]any); !ok || len(urls) != 1 || urls[0] != "https://a.b/x.mp3" {
		t.Errorf("file_urls = %v", in["file_urls"])
	}
}

// 轮询查询：SUCCEEDED 时带出 transcription_url。
func TestASRClientQueryTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tasks/tid-1" {
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid-1","task_status":"SUCCEEDED",
				"results":[{"transcription_url":"` + srv.URL(t) + `/trans.json"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"transcripts":[{"sentences":[
			{"begin_time":0,"end_time":1500,"text":"你好","speaker_id":"1"},
			{"begin_time":1500,"end_time":3000,"text":"世界"}]}]}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk", srv.URL)
	task, err := c.QueryTask(context.Background(), "tid-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusSucceeded || len(task.TranscriptionURLs) != 1 {
		t.Fatalf("task = %+v", task)
	}
	tr, err := c.FetchTranscription(context.Background(), task.TranscriptionURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Transcripts) != 1 || len(tr.Transcripts[0].Sentences) != 2 {
		t.Fatalf("transcription = %+v", tr)
	}
	if tr.Transcripts[0].Sentences[0].Text != "你好" || tr.Transcripts[0].Sentences[0].SpeakerID != "1" {
		t.Errorf("sentence[0] = %+v", tr.Transcripts[0].Sentences[0])
	}
}
```

同样注意 handler 闭包里引用 `srv.URL`（`srv.URL(t)` 写法不存在，实现测试时直接 `var srv *httptest.Server` 后引用）。`FAILED` 状态查询补一个用例：`task.Status == "FAILED"` 且 error 信息含上游 message。

`internal/provider/qianwen/asr_tool_test.go`：

```go
package qianwen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// asr 工具全链路：URL 直用 → 提交/轮询（一次 PENDING/RUNNING 一次 SUCCEEDED）→ 拉 transcription → txt+srt 产物。
func TestASRToolRunURL(t *testing.T) {
	var srv *httptest.Server
	var poll int
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/services/audio/asr/transcription":
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"PENDING"}}`))
		case r.URL.Path == "/api/v1/tasks/tid":
			poll++
			if poll == 1 {
				_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"RUNNING"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid","task_status":"SUCCEEDED",
				"results":[{"transcription_url":"` + srv.URL + `/trans.json"}]}}`))
		default: // /trans.json
			_, _ = w.Write([]byte(`{"transcripts":[{"sentences":[
				{"begin_time":0,"end_time":1500,"text":"你好"},
				{"begin_time":1500,"end_time":62000,"text":"世界"}]}]}`))
		}
	}))
	defer srv.Close()
	out := t.TempDir()
	tool := NewASRTool("sk-test", out)
	tool.pollInterval = 0 // 测试免等待
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": srv.URL + "/input.mp3"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 2 {
		t.Fatalf("应产出 txt+srt: %+v", res.Artifacts)
	}
	txtAbs := filepath.Join(out, res.Artifacts[0].Path)
	raw, _ := os.ReadFile(txtAbs)
	if !strings.Contains(string(raw), "你好") || !strings.Contains(string(raw), "世界") {
		t.Errorf("txt 内容不符: %q", raw)
	}
	srtAbs := filepath.Join(out, res.Artifacts[1].Path)
	srt, _ := os.ReadFile(srtAbs)
	if !strings.Contains(string(srt), "00:01:02,000") {
		t.Errorf("srt 时间戳不符: %q", srt)
	}
	// summary.segments 供前端文稿联动
	if segs, ok := res.Summary["segments"].([]map[string]any); !ok || len(segs) != 2 {
		t.Errorf("summary.segments = %v", res.Summary["segments"])
	}
}
```

另补两个用例：本地文件（`Files["audio"]` 指向临时文件）且 `Storage` 为 nil 时报「对象存储中转」指引；凭证缺失（`NewASRTool("", out)` + url 输入）时报「千问 API Key」指引。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/provider/qianwen/ -v`
Expected: FAIL（ASRClient/ASRTool 未定义）

- [ ] **Step 3: 实现**

`internal/provider/qianwen/asr_client.go`：

```go
package qianwen

import (
	"context"
	"fmt"
	"net/http"
)

// ASRParams filetrans 提交参数（nil/零值不发送，走上游默认）。
type ASRParams struct {
	LanguageHints      []string
	DiarizationEnabled bool
	EnableITN          *bool
	EnableWords        *bool
}

// 任务状态（DashScope 异步任务口径）。
const (
	StatusPending = "PENDING"
	StatusRunning = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed   = "FAILED"
)

type asrSubmitRequest struct {
	Model      string         `json:"model"`
	Input      asrSubmitInput `json:"input"`
	Parameters *ASRParamsWire `json:"parameters,omitempty"`
}

// qwen3 系模型 input.file_url 单串；qwen-audio / fun-asr 系 input.file_urls 数组。
type asrSubmitInput struct {
	FileURL  string   `json:"file_url,omitempty"`
	FileURLs []string `json:"file_urls,omitempty"`
}

type ASRParamsWire struct {
	LanguageHints      []string `json:"language_hints,omitempty"`
	DiarizationEnabled bool     `json:"diarization_enabled,omitempty"`
	EnableITN          *bool    `json:"enable_itn,omitempty"`
	EnableWords        *bool    `json:"enable_words,omitempty"`
}

type asrSubmitResponse struct {
	Output struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
	} `json:"output"`
	apiError
}

type TranscriptionTask struct {
	TaskID            string
	Status            string
	TranscriptionURLs []string
	Message           string // FAILED 时的上游信息
}

type taskQueryResponse struct {
	Output struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		Message    string `json:"message"`
		Results    []struct {
			TranscriptionURL string `json:"transcription_url"`
		} `json:"results"`
	} `json:"output"`
	apiError
}

// Transcription transcription_url 指向的转写结果 JSON（transcripts[].sentences[]）。
type Transcription struct {
	Transcripts []struct {
		Sentences []TranscriptionSentence `json:"sentences"`
	} `json:"transcripts"`
}

type TranscriptionSentence struct {
	BeginTime int64  `json:"begin_time"`
	EndTime   int64  `json:"end_time"`
	Text      string `json:"text"`
	SpeakerID string `json:"speaker_id,omitempty"`
	Words     []struct {
		BeginTime int64  `json:"begin_time"`
		EndTime   int64  `json:"end_time"`
		Text      string `json:"text"`
	} `json:"words"`
}

// ASRClient 千问 filetrans 文件转写客户端。
type ASRClient struct{ apiKey, baseURL string }

func NewASRClient(apiKey, baseURL string) *ASRClient {
	return &ASRClient{apiKey: apiKey, baseURL: trimSlash(baseURL)}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// SubmitTranscription 提交异步转写任务。model 决定 input 形态：qwen3 系单 file_url，
// 其余（qwen-audio / fun-asr 系）file_urls 数组。
func (c *ASRClient) SubmitTranscription(ctx context.Context, model, fileURL string, p ASRParams) (string, error) {
	in := asrSubmitInput{}
	if isQwen3Model(model) {
		in.FileURL = fileURL
	} else {
		in.FileURLs = []string{fileURL}
	}
	var wire *ASRParamsWire
	if len(p.LanguageHints) > 0 || p.DiarizationEnabled || p.EnableITN != nil || p.EnableWords != nil {
		wire = &ASRParamsWire{
			LanguageHints:      p.LanguageHints,
			DiarizationEnabled: p.DiarizationEnabled,
			EnableITN:          p.EnableITN,
			EnableWords:        p.EnableWords,
		}
	}
	body := asrSubmitRequest{Model: model, Input: in, Parameters: wire}
	var resp asrSubmitResponse
	if err := doJSON(ctx, http.MethodPost, c.baseURL+pathASRSub, c.apiKey,
		map[string]string{"X-DashScope-Async": "enable"}, body, &resp); err != nil {
		return "", err
	}
	if resp.Output.TaskID == "" {
		return "", fmt.Errorf("千问转写提交未返回 task_id")
	}
	return resp.Output.TaskID, nil
}

func isQwen3Model(model string) bool {
	return len(model) >= 6 && model[:6] == "qwen3-"
}

// QueryTask 查询异步任务状态；SUCCEEDED 时带出转写结果地址。
func (c *ASRClient) QueryTask(ctx context.Context, taskID string) (TranscriptionTask, error) {
	var resp taskQueryResponse
	if err := doJSON(ctx, http.MethodGet, fmt.Sprintf(c.baseURL+pathTaskFmt, taskID), c.apiKey, nil, nil, &resp); err != nil {
		return TranscriptionTask{}, err
	}
	t := TranscriptionTask{
		TaskID:  resp.Output.TaskID,
		Status:  resp.Output.TaskStatus,
		Message: resp.Output.Message,
	}
	for _, r := range resp.Output.Results {
		if r.TranscriptionURL != "" {
			t.TranscriptionURLs = append(t.TranscriptionURLs, r.TranscriptionURL)
		}
	}
	return t, nil
}

// FetchTranscription 拉取 transcription_url 的转写 JSON。
func (c *ASRClient) FetchTranscription(ctx context.Context, url string) (Transcription, error) {
	var tr Transcription
	if err := doJSON(ctx, http.MethodGet, url, c.apiKey, nil, nil, &tr); err != nil {
		return Transcription{}, err
	}
	return tr, nil
}
```

`internal/provider/qianwen/asr_tool.go`：

```go
package qianwen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// filetrans 轮询节奏（var 供测试注入）：起步 5s 指数退避至 30s，总超时 2h（上游 ≤12h，
// 常规文件分钟级完成，2h 未决视为异常留人工重试）。
var (
	asrPollInterval = 5 * time.Second
	asrPollMax      = 30 * time.Second
	asrPollTimeout  = 2 * time.Hour
)

const (
	asrModelQwen3      = "qwen3-asr-flash-filetrans"
	asrModelQwenAudio  = "qwen-audio-3.1-asr-flash-filetrans"
)

// ASRTool 千问文件转写（filetrans）：URL 直用 / 本地文件经对象存储中转，异步提交轮询，
// 产出分句 txt + SRT（与 volcengine asr 产物同构）。
type ASRTool struct {
	client       *ASRClient
	apiKey       string
	outDir       string
	pollInterval time.Duration // 测试注入（0=免等待）
}

func NewASRTool(apiKey, outDir string) *ASRTool {
	return &ASRTool{client: NewASRClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir, pollInterval: asrPollInterval}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "qianwen",
		Name:        "asr",
		Title:       "语音识别（千问）",
		Description: "qwen3-asr 文件转写，支持长音频（≤12h/2GB）、说话人分离与词级时间戳，输出分句文本与 SRT 字幕",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL；留空可配合本地文件（需配置对象存储）"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: asrModelQwen3, Group: "输入",
			Options: []provider.ParamOption{
				{Value: asrModelQwen3, Label: "qwen3-asr-flash-filetrans（推荐）"},
				{Value: asrModelQwenAudio, Label: "qwen-audio-3.1-asr-flash-filetrans（说话人分离更强）"},
			}},
		{Key: "language_hints", Label: "语言", Type: provider.ParamEnum, Default: "", Group: "输入",
			Options: []provider.ParamOption{
				{Value: "", Label: "自动识别"},
				{Value: "zh", Label: "中文"}, {Value: "en", Label: "英语"},
				{Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"},
				{Value: "de", Label: "德语"}, {Value: "fr", Label: "法语"},
				{Value: "ru", Label: "俄语"}, {Value: "es", Label: "西班牙语"},
				{Value: "pt", Label: "葡萄牙语"}, {Value: "it", Label: "意大利语"},
			}},
		{Key: "diarization_enabled", Label: "说话人分离", Type: provider.ParamBool, Default: false, Group: "参数",
			Placeholder: "区分不同说话人（≤2h 且单声道音频）"},
		{Key: "enable_itn", Label: "数字规整（ITN）", Type: provider.ParamBool, Default: true, Group: "参数"},
		{Key: "enable_words", Label: "词级时间戳", Type: provider.ParamBool, Default: false, Group: "参数"},
		{Key: "srt", Label: "生成 SRT 字幕", Type: provider.ParamBool, Default: true, Group: "输出"},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audioURL, err := provider.EnsureURLInput(ctx, in, "url", "音频",
		"缺少输入：千问识别需要音频 URL 或本地文件（对象存储中转）", report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("未配置千问 API Key：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key")
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = asrModelQwen3
	}
	lang := paramString(in.Params, "language_hints")
	var hints []string
	if lang != "" {
		hints = []string{lang}
	}
	itn := paramBool(in.Params, "enable_itn", true)

	report(10, "提交千问转写任务", nil)
	taskID, err := t.client.SubmitTranscription(ctx, model, audioURL, ASRParams{
		LanguageHints:      hints,
		DiarizationEnabled: paramBool(in.Params, "diarization_enabled", false),
		EnableITN:          &itn,
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(30, "任务已提交，等待转写", map[string]any{"task_id": taskID})

	// 轮询：指数退避，ctx 取消优先。
	deadline := time.Now().Add(asrPollTimeout)
	interval := t.pollInterval
	var task TranscriptionTask
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("识别已取消: %w", err)
		}
		if time.Now().After(deadline) {
			return provider.TaskOutput{}, fmt.Errorf("等待千问转写结果超时（2 小时），任务 %s 可能仍在处理，请稍后在历史页查看", taskID)
		}
		task, err = t.client.QueryTask(ctx, taskID)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		switch task.Status {
		case StatusSucceeded:
			if len(task.TranscriptionURLs) == 0 {
				return provider.TaskOutput{}, fmt.Errorf("转写成功但未返回结果地址（任务 %s）", taskID)
			}
			report(80, "拉取转写结果", nil)
			return t.saveArtifacts(in, task, model)
		case StatusFailed:
			msg := task.Message
			if msg == "" {
				msg = "上游未给出原因"
			}
			return provider.TaskOutput{}, fmt.Errorf("千问转写失败（任务 %s）: %s", taskID, msg)
		}
		report(30+min(40, n*2), "等待转写结果", map[string]any{"task_id": taskID, "poll": n, "status": task.Status})
		interval *= 2
		if interval > asrPollMax || interval <= 0 {
			interval = asrPollMax
		}
		select {
		case <-ctx.Done():
			return provider.TaskOutput{}, fmt.Errorf("识别已取消: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}

// saveArtifacts txt + srt 落盘（与 volcengine asr 的 _out/相对路径语义一致），
// summary.segments 供前端文稿联动（含 speaker_id）。
func (t *ASRTool) saveArtifacts(in provider.TaskInput, task TranscriptionTask, model string) (provider.TaskOutput, error) {
	tr, err := t.client.FetchTranscription(context.Background(), task.TranscriptionURLs[0])
	if err != nil {
		return provider.TaskOutput{}, err
	}
	var sentences []TranscriptionSentence
	for _, t := range tr.Transcripts {
		sentences = append(sentences, t.Sentences...)
	}
	if len(sentences) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("转写结果为空（任务 %s）", task.TaskID)
	}

	var text strings.Builder
	var lastSpeaker string
	segs := make([]provider.SRTSegment, 0, len(sentences))
	segSummaries := make([]map[string]any, 0, len(sentences))
	for i, s := range sentences {
		if i > 0 {
			text.WriteByte('\n')
		}
		// 说话人切换时加标注行（开启分离时 speaker_id 非空）
		if s.SpeakerID != "" && s.SpeakerID != lastSpeaker {
			fmt.Fprintf(&text, "[说话人 %s] ", s.SpeakerID)
			lastSpeaker = s.SpeakerID
		}
		text.WriteString(s.Text)
		segs = append(segs, provider.SRTSegment{StartMS: s.BeginTime, EndMS: s.EndTime, Text: s.Text})
		segSummaries = append(segSummaries, map[string]any{
			"text": s.Text, "start_ms": s.BeginTime, "end_ms": s.EndTime, "speaker_id": s.SpeakerID,
		})
	}

	reqID := uuid.NewString()
	txtPath := filepath.Join("asr", reqID+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		txtPath = outParam
	}
	srtPath := strings.TrimSuffix(txtPath, filepath.Ext(txtPath)) + ".srt"
	txtAbs, relTxt := resolveOut(t.outDir, txtPath)
	srtAbs, relSrt := resolveOut(t.outDir, srtPath)
	if err := os.MkdirAll(filepath.Dir(txtAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(txtAbs, []byte(text.String()), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}
	arts := []provider.Artifact{{
		Kind: "transcript", Path: relTxt, Format: "txt",
		Size: int64(text.Len()), DurationMS: sentences[len(sentences)-1].EndTime,
	}}
	if paramBool(in.Params, "srt", true) {
		srtContent := provider.BuildSRT(segs)
		if err := os.WriteFile(srtAbs, []byte(srtContent), 0o644); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
		}
		arts = append(arts, provider.Artifact{
			Kind: "subtitle", Path: relSrt, Format: "srt", Size: int64(len(srtContent)),
		})
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"segments":    segSummaries,
			"duration_ms": sentences[len(sentences)-1].EndTime,
			"model":       model,
			"source":      "url",
		},
	}, nil
}

// resolveOut 相对路径锚定 outDir 并回算相对形态（绝对路径原样）。
func resolveOut(outDir, p string) (abs, rel string) {
	abs = p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(outDir, p)
		rel, _ = filepath.Rel(outDir, abs)
		return abs, rel
	}
	return abs, p
}

// paramBool 取布尔参数（缺失用默认；兼容字符串 "false"）。
func paramBool(params map[string]any, key string, def bool) bool {
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		if strings.EqualFold(v, "false") {
			return false
		}
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return def
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/provider/qianwen/ -v`
Expected: PASS（含 Task 4 的 TTS 测试）

- [ ] **Step 5: Commit**

```bash
git add internal/provider/qianwen/
git commit -m "feat(qianwen): filetrans 文件转写工具（txt+srt 产物、说话人分离）"
```

---

### Task 6: 各 provider 包卡声明 + service 装配

**Files:**
- Create: `internal/provider/volcengine/card.go`（volcengine + mediakit 两张卡）
- Create: `internal/provider/mvsep/card.go`
- Create: `internal/provider/audiotool/card.go`
- Create: `internal/provider/gsgc/card.go`
- Create: `internal/service/cards.go`
- Test: `internal/service/cards_test.go`

**Interfaces:**
- Consumes: Task 1 的 `ProviderInfo` 系列
- Produces: `service.providerCards() []provider.ProviderInfo`、`service.cardFieldValues(*config.Config) map[string]map[string]string`、`service.cardConfigured(name string, vals map[string]string) bool`、`(*Service).ProviderStates(cfg *config.Config) []ProviderState`（`ProviderState`/`FieldState` 结构体，Task 8 的路由消费）。

- [ ] **Step 1: 写失败测试**

`internal/service/cards_test.go`：

```go
package service

import (
	"testing"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// 全部卡声明合法（Validate 防呆）+ 云端卡名唯一 + 必要卡在场。
func TestProviderCardsValid(t *testing.T) {
	cards := providerCards()
	seen := map[string]bool{}
	for _, c := range cards {
		if err := c.Validate(); err != nil {
			t.Errorf("卡 %s 声明非法: %v", c.Name, err)
		}
		if seen[c.Name] {
			t.Errorf("卡名重复: %s", c.Name)
		}
		seen[c.Name] = true
	}
	for _, want := range []string{"volcengine", "mediakit", "mvsep", "qianwen", "audiotool", "gsgc"} {
		if !seen[want] {
			t.Errorf("缺少卡: %s", want)
		}
	}
	// 卡的字段声明必须与 cardFieldValues 的键完全对齐
	vals := cardFieldValues(&config.Config{})
	for _, c := range cards {
		if c.Kind != provider.KindCloud {
			continue
		}
		m := vals[c.Name]
		for _, f := range c.Fields {
			if m == nil {
				t.Errorf("cardFieldValues 缺卡 %s", c.Name)
				break
			}
			if _, ok := m[f.Key]; !ok {
				t.Errorf("cardFieldValues[%s] 缺字段 %s", c.Name, f.Key)
			}
		}
	}
}

// configured 判定：volcengine 两种凭证组合任一可用；单 secret 卡看 secret。
func TestCardConfigured(t *testing.T) {
	if !cardConfigured("volcengine", map[string]string{"app_id": "a", "access_token": "t"}) {
		t.Error("APP ID+Token 组合应视为已配置")
	}
	if !cardConfigured("volcengine", map[string]string{"api_key": "k"}) {
		t.Error("单 API Key 应视为已配置")
	}
	if cardConfigured("volcengine", map[string]string{"app_id": "a"}) {
		t.Error("只有 APP ID 不应视为已配置")
	}
	if !cardConfigured("qianwen", map[string]string{"api_key": "k"}) {
		t.Error("千问配 key 应视为已配置")
	}
	if cardConfigured("qianwen", map[string]string{}) {
		t.Error("千问空 key 不应视为已配置")
	}
}

// ProviderStates：secret 只出 has_value、text 出 value、本地卡带 tools_count、configured 正确。
func TestProviderStates(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	states := svc.ProviderStates(svc.Config())
	var volc, qwen, audio *ProviderState
	for i := range states {
		switch states[i].Name {
		case "volcengine":
			volc = &states[i]
		case "qianwen":
			qwen = &states[i]
		case "audiotool":
			audio = &states[i]
		}
	}
	if volc == nil || qwen == nil || audio == nil {
		t.Fatalf("缺少期望的卡: %+v", states)
	}
	if volc.Configured {
		t.Error("空凭证 volcengine 不应 configured")
	}
	for _, f := range volc.Fields {
		if f.Key == "app_id" && f.Value != "" {
			t.Error("app_id 应为 text 且值可回显（空配置下为空串）")
		}
		if f.Key == "access_token" && f.HasValue {
			t.Error("未配置 access_token 时 has_value 应为 false")
		}
	}
	if qwen.Configured {
		t.Error("空凭证 qianwen 不应 configured")
	}
	if audio.Kind != provider.KindLocal || audio.ToolsCount < 10 {
		t.Errorf("audiotool 本地卡 tools_count = %d", audio.ToolsCount)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service/ -run 'TestProviderCards|TestCardConfigured|TestProviderStates' -v`
Expected: FAIL（`providerCards` 等未定义）

- [ ] **Step 3: 实现**

`internal/provider/volcengine/card.go`：

```go
package volcengine

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 火山语音凭证卡（文案沿用现设置页：播客必须 APP ID+Token，TTS/ASR 双轨凭证）。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "volcengine",
		Title: "火山引擎 · 语音合成 / 识别 / 播客",
		Description: "播客生成必须 APP ID + Access Token（播客协议只认这对凭证）；TTS / 语音识别两者皆可——" +
			"APP ID + Access Token，或新版 API Key 单键。",
		Kind:  provider.KindCloud,
		Order: 10,
		Fields: []provider.CredentialField{
			{Key: "app_id", Label: "APP ID", Kind: provider.FieldText, ConfigKey: "volc.speech.app_id",
				Placeholder: "火山控制台获取"},
			{Key: "access_token", Label: "Access Token", Kind: provider.FieldSecret, ConfigKey: "volc.speech.access_token",
				Placeholder: "留空表示不修改", Hint: "与 APP ID 配套"},
			{Key: "api_key", Label: "新版 API Key", Kind: provider.FieldSecret, ConfigKey: "volc.speech.api_key",
				Placeholder: "留空表示不修改", Hint: "仅 TTS / 语音识别可用，播客不支持"},
		},
	}
}

// MediaKitCard AI MediaKit 人声分离凭证卡（独立于火山语音凭证体系，落盘仍 volc.mediakit.*）。
func MediaKitCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "mediakit",
		Title:       "AI MediaKit · 人声分离",
		Description: "仅用于人声背景音分离，与火山语音是两套独立凭证。",
		Kind:        provider.KindCloud,
		Order:       15,
		Fields: []provider.CredentialField{
			{Key: "api_key", Label: "MediaKit API Key", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "volc.mediakit.api_key", Placeholder: "在 AI MediaKit 控制台创建"},
		},
	}
}
```

`internal/provider/mvsep/card.go`：

```go
package mvsep

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard MVSep 凭证卡。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "mvsep",
		Title: "MVSep · 音频源分离",
		Description: "MVSep（mvsep.com）音乐源分离：人声/伴奏、鼓、贝斯等 120+ 算法，注册账号每天 50 次免费。",
		Kind:  provider.KindCloud,
		Order: 30,
		Fields: []provider.CredentialField{
			{Key: "api_token", Label: "API Token", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "mvsep.api_token", Placeholder: "mvsep.com 全页 API 页获取"},
			{Key: "base_url", Label: "接入线路", Kind: provider.FieldSelect, ConfigKey: "mvsep.base_url",
				Options: []provider.ParamOption{
					{Value: "", Label: "主站（自动就近）"},
					{Value: "https://hk.mvsep.com", Label: "香港 hk.mvsep.com"},
					{Value: "https://de.mvsep.com", Label: "德国 de.mvsep.com"},
					{Value: "https://de2.mvsep.com", Label: "德国 2 de2.mvsep.com"},
				},
				Hint: "同一任务只能由接单节点出结果，须全程固定线路"},
		},
	}
}
```

`internal/provider/audiotool/card.go`：

```go
package audiotool

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地能力卡：ffmpeg 音频剪辑 12 工具，无凭证、只读展示。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "audiotool",
		Title:       "本地音频剪辑",
		Description: "ffmpeg 本地处理：裁剪/合并/变调/均衡/闪避等 12 个工具，无凭证、离线可用。",
		Kind:        provider.KindLocal,
		Order:       90,
	}
}
```

`internal/provider/gsgc/card.go`：

```go
package gsgc

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地能力卡：格式工厂站点工具（匿名），只读展示。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "gsgc",
		Title:       "站点工具（格式工厂）",
		Description: "人声分离与音视频/图片转换压缩的站点协议通道，匿名可用、无凭证。",
		Kind:        provider.KindLocal,
		Order:       95,
	}
}
```

`internal/service/cards.go`：

```go
// 设置页「云端服务/本地环境」两分组的卡片装配：声明来自各 provider 包，
// 当前值与已配置状态来自配置快照，工具数来自注册表。
package service

import (
	"sort"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

// providerCards 全部卡声明（按 Order 排序）。新增厂商 = 包内声明 + 此处加一行。
func providerCards() []provider.ProviderInfo {
	cards := []provider.ProviderInfo{
		volcengine.ProviderCard(),
		volcengine.MediaKitCard(),
		mvsep.ProviderCard(),
		qianwen.ProviderCard(),
		audiotool.ProviderCard(),
		gsgc.ProviderCard(),
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Order < cards[j].Order })
	return cards
}

// cardFieldValues 各卡字段的当前值（来自配置快照）。键必须与卡声明 Fields 完全对齐
//（cards_test 防呆）；secret 值只用于 configured/has_value 判定，不出 HTTP 响应。
func cardFieldValues(cfg *config.Config) map[string]map[string]string {
	return map[string]map[string]string{
		"volcengine": {
			"app_id":        cfg.Volc.Speech.AppID,
			"access_token":  cfg.Volc.Speech.AccessToken,
			"api_key":       cfg.Volc.Speech.APIKey,
		},
		"mediakit": {"api_key": cfg.Volc.MediaKit.APIKey},
		"mvsep":    {"api_token": cfg.MVSep.APIToken, "base_url": cfg.MVSep.BaseURL},
		"qianwen":  {"api_key": cfg.Qianwen.APIKey},
	}
}

// cardConfigured 卡级「已配置」判定，沿用各卡现口径。
func cardConfigured(name string, vals map[string]string) bool {
	switch name {
	case "volcengine":
		return (vals["app_id"] != "" && vals["access_token"] != "") || vals["api_key"] != ""
	case "mediakit", "qianwen":
		return vals["api_key"] != ""
	case "mvsep":
		return vals["api_token"] != ""
	}
	return false
}

// FieldState 卡字段的 HTTP 形态：text/select 回 value，secret 只回 has_value。
type FieldState struct {
	Key         string                 `json:"key"`
	Label       string                 `json:"label"`
	Kind        provider.FieldKind     `json:"kind"`
	Value       string                 `json:"value,omitempty"`
	HasValue    bool                   `json:"has_value,omitempty"`
	Options     []provider.ParamOption `json:"options,omitempty"`
	Placeholder string                 `json:"placeholder,omitempty"`
	Hint        string                 `json:"hint,omitempty"`
	Required    bool                   `json:"required,omitempty"`
}

// ProviderState 卡的 HTTP 形态（GET /api/settings 的 providers 数组元素）。
type ProviderState struct {
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Kind        provider.ProviderKind  `json:"kind"`
	Order       int                    `json:"order"`
	Configured  bool                   `json:"configured"`
	ToolsCount  int                    `json:"tools_count"`
	Fields      []FieldState           `json:"fields"`
}

// ProviderStates 卡声明 + 配置快照 + 注册表工具计数 → HTTP 形态。
func (s *Service) ProviderStates(cfg *config.Config) []ProviderState {
	counts := map[string]int{}
	for _, m := range s.reg.List() {
		counts[m.Provider]++
	}
	vals := cardFieldValues(cfg)
	out := make([]ProviderState, 0, len(providerCards()))
	for _, c := range providerCards() {
		st := ProviderState{
			Name: c.Name, Title: c.Title, Description: c.Description,
			Kind: c.Kind, Order: c.Order, ToolsCount: counts[c.Name],
			Fields: []FieldState{},
		}
		cv := vals[c.Name]
		st.Configured = cardConfigured(c.Name, cv)
		for _, f := range c.Fields {
			fs := FieldState{
				Key: f.Key, Label: f.Label, Kind: f.Kind,
				Options: f.Options, Placeholder: f.Placeholder, Hint: f.Hint, Required: f.Required,
			}
			if f.Kind == provider.FieldSecret {
				fs.HasValue = cv[f.Key] != ""
			} else {
				fs.Value = cv[f.Key]
			}
			st.Fields = append(st.Fields, fs)
		}
		out = append(out, st)
	}
	return out
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/service/ ./internal/provider/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/volcengine/card.go internal/provider/mvsep/card.go internal/provider/audiotool/card.go internal/provider/gsgc/card.go internal/service/cards.go internal/service/cards_test.go
git commit -m "feat(service): provider 卡声明装配与设置态估值"
```

---

### Task 7: service · SaveProviderFields + 热加载映射 + qianwen 注册 + 探活

**Files:**
- Create: `internal/provider/qianwen/provider.go`（RegisterAll/ReRegisterAll）
- Modify: `internal/service/service.go`（newWithRoot 注册 qianwen、SaveProviderFields、reloadCard、ReloadDiskConfig、TestQianwenConnection、删除 SaveCredentials）

**Interfaces:**
- Consumes: Task 4/5 的 `qianwen.NewTTSTool`/`NewASRTool`/`BaseURL`、Task 6 的 `providerCards`/`cardFieldValues`
- Produces: `qianwen.RegisterAll(reg, cfg config.Config, dataDir string) error`、`qianwen.ReRegisterAll(reg, cfg, dataDir)`；`(*Service).SaveProviderFields(name string, fields map[string]string) error`、`(*Service).TestQianwenConnection() (string, bool)`（Task 8 消费）。

- [ ] **Step 1: 写失败测试**

在 `internal/service/service_test.go` 追加（沿用该文件既有的 Service 构造与 config 写盘模式）：

```go
// SaveProviderFields：字段落盘、快照更新、secret 留空不改、未知字段拒绝、热重注册触发。
func TestSaveProviderFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VOXBOX_HOME", home)
	svc, err := NewWithHome(home)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// 1) 保存千问 key：落盘 + 快照可见
	if err := svc.SaveProviderFields("qianwen", map[string]string{"api_key": "sk-1"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Qianwen.APIKey; got != "sk-1" {
		t.Errorf("快照 Qianwen.APIKey = %q", got)
	}
	disk, err := config.Load()
	if err != nil || disk.Qianwen.APIKey != "sk-1" {
		t.Errorf("落盘不符: %q err=%v", disk.Qianwen.APIKey, err)
	}

	// 2) secret 留空 = 不改（空串提交不覆盖）
	if err := svc.SaveProviderFields("qianwen", map[string]string{"api_key": ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Qianwen.APIKey; got != "sk-1" {
		t.Errorf("secret 留空不应清空: %q", got)
	}

	// 3) 未知字段拒绝
	if err := svc.SaveProviderFields("qianwen", map[string]string{"hacker": "x"}); err == nil {
		t.Error("未知字段应报错")
	}

	// 4) 未知卡拒绝
	if err := svc.SaveProviderFields("nope", map[string]string{}); err == nil {
		t.Error("未知卡应报错")
	}

	// 5) 热重注册：保存火山凭证后注册表内 tts 工具可取出（凭证已进实例）
	if err := svc.SaveProviderFields("volcengine", map[string]string{"api_key": "vk-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Registry().Get("volcengine", "tts"); !ok {
		t.Error("volcengine.tts 应已注册")
	}
	if _, ok := svc.Registry().Get("qianwen", "tts"); !ok {
		t.Error("qianwen.tts 应已注册（Task 7 newWithRoot 注册）")
	}
}

// TestQianwenConnection：未配置直接报未配置。
func TestQianwenConnectionUnconfigured(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	msg, ok := svc.TestQianwenConnection()
	if ok || msg == "" {
		t.Errorf("未配置应 (false, 提示), got (%v, %q)", ok, msg)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service/ -run 'TestSaveProviderFields|TestQianwenConnection' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`internal/provider/qianwen/provider.go`：

```go
package qianwen

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 启动路径注册（同 key 重复注册报错）。凭证缺失仍注册，Run 时再报凭证错误。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(cfg, dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 凭证热加载覆盖重注册（与 volcengine 同款契约）。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(cfg, dataDir) {
		reg.Replace(t)
	}
}

func allTools(cfg config.Config, dataDir string) []provider.Tool {
	return []provider.Tool{
		NewTTSTool(cfg.Qianwen.APIKey, dataDir),
		NewASRTool(cfg.Qianwen.APIKey, dataDir),
	}
}
```

`internal/service/service.go` 四处改动：

1. import 加 `"github.com/yann0917/voxbox/internal/provider/qianwen"`；

2. `newWithRoot` 中 `mvsep.RegisterAll` 之后加：

```go
	if err := qianwen.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
```

3. **删除** `SaveCredentials` 整个方法（140-188 行），替换为：

```go
// SaveProviderFields 保存一张凭证卡的字段并热应用：按卡声明校验（未知卡/未知字段拒绝），
// secret 留空=不修改，text/select 按提交值落盘（select 空串=合法取值，如 MVSep 主站）。
// 成功后只重注册该卡对应的工具集，进行中任务持有旧实例不受影响。
func (s *Service) SaveProviderFields(name string, fields map[string]string) error {
	var card *provider.ProviderInfo
	for _, c := range providerCards() {
		if c.Name == name {
			cc := c
			card = &cc
			break
		}
	}
	if card == nil || card.Kind != provider.KindCloud {
		return fmt.Errorf("未知凭证卡: %s", name)
	}
	for k := range fields {
		known := false
		for _, f := range card.Fields {
			if f.Key == k {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("字段不属于 %s 卡: %s", name, k)
		}
	}
	for _, f := range card.Fields {
		v, ok := fields[f.Key]
		if !ok || (f.Kind == provider.FieldSecret && v == "") {
			continue // 未提交或 secret 留空 = 不修改
		}
		if err := config.Set(f.ConfigKey, v); err != nil {
			return err
		}
	}
	nc := *s.cfg.Load()
	applyCardFields(&nc, name, fields)
	s.cfg.Store(&nc)
	s.reloadCard(name, nc)
	return nil
}

// applyCardFields 把提交字段套进内存快照（secret 留空不覆盖）。
func applyCardFields(nc *config.Config, name string, fields map[string]string) {
	get := func(k string) (string, bool) {
		v, ok := fields[k]
		return v, ok
	}
	switch name {
	case "volcengine":
		if v, ok := get("app_id"); ok && v != "" {
			nc.Volc.Speech.AppID = v
		}
		if v, ok := get("access_token"); ok && v != "" {
			nc.Volc.Speech.AccessToken = v
		}
		if v, ok := get("api_key"); ok && v != "" {
			nc.Volc.Speech.APIKey = v
		}
	case "mediakit":
		if v, ok := get("api_key"); ok && v != "" {
			nc.Volc.MediaKit.APIKey = v
		}
	case "mvsep":
		if v, ok := get("api_token"); ok && v != "" {
			nc.MVSep.APIToken = v
		}
		if v, ok := get("base_url"); ok {
			nc.MVSep.BaseURL = v
		}
	case "qianwen":
		if v, ok := get("api_key"); ok && v != "" {
			nc.Qianwen.APIKey = v
		}
	}
}

// reloadCard 卡 → 工具热重注册映射（mediakit 的分离工具在 volcengine 包，共用其重注册）。
func (s *Service) reloadCard(name string, nc config.Config) {
	switch name {
	case "volcengine", "mediakit":
		volcengine.ReRegisterAll(s.reg, nc, nc.DataDir)
	case "mvsep":
		mvsep.ReRegisterAll(s.reg, nc, nc.DataDir)
	case "qianwen":
		qianwen.ReRegisterAll(s.reg, nc, nc.DataDir)
	}
}
```

4. `ReloadDiskConfig` 中 `nc.MVSep = disk.MVSep` 后加 `nc.Qianwen = disk.Qianwen`，重注册列表加 `qianwen.ReRegisterAll(s.reg, nc, nc.DataDir)`。

并在文件末尾（TestMediaKitConnection 之后）加：

```go
// TestQianwenConnection 千问连通性探测：极短文本合成（消耗少量额度，同火山语音模式）。
func (s *Service) TestQianwenConnection() (string, bool) {
	key := s.cfg.Load().Qianwen.APIKey
	if key == "" {
		return "未配置千问 API Key：请执行 voxbox config set qianwen.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := qianwen.NewTTSClient(key, qianwen.BaseURL)
	if _, err := client.Synthesize(ctx, qianwen.TTSReq{
		Model: "qwen3-tts-flash", Text: "测", Voice: qianwen.DefaultVoice,
	}); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}
```

- [ ] **Step 4: 编译修复 + 运行确认通过**

`SaveCredentials` 删除后 `internal/server/routes.go` 的 `putSettings` 编译报错——本任务临时把 `putSettings` 中该调用行改为 `_ = req // Task 8 重写本 handler`（或注释掉凭证保存段，storage 分支保留），让包可编译。这是有意的过渡态，Task 8 消除。

Run: `go build ./... && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/qianwen/provider.go internal/service/service.go internal/service/service_test.go internal/server/routes.go
git commit -m "feat(service): 按卡保存凭证并热重注册；接入 qianwen 工具注册"
```

---

### Task 8: settings API 三端点改造 + test-connection 新结构

**Files:**
- Modify: `internal/server/routes.go`（路由注册 55-57 行区域 + getSettings/putSettings/testConnection）
- Test: `internal/server/routes_test.go`

**Interfaces:**
- Consumes: Task 6 `ProviderStates`、Task 7 `SaveProviderFields`/`TestQianwenConnection`
- Produces: HTTP 契约（Task 9 前端消费）：
  - `GET /api/settings` → `{providers: ProviderState[], storage: {...现状...}, data_dir}`
  - `PUT /api/settings/providers/:name` body `{"fields": {"api_key": "…"}}` → `{ok, note}`
  - `PUT /api/settings/storage` body 同旧 storage 分支 → `{ok, note}`
  - `POST /api/settings/test-connection` → `{results: [{name, ok, message}], storage: {ok, message}}`

- [ ] **Step 1: 写失败测试**

在 `internal/server/routes_test.go` 追加（沿用该文件既有的测试 Server 构造/登录模式；若已有 settings 相关用例，同步改造为以下断言）：

```go
// 新读端点：providers 数组在场，volc/mvsep 平铺段已删除。
func TestGetSettingsProviders(t *testing.T) {
	s := newTestServer(t) // 既有测试辅助，按实际函数名调整
	w := s.getJSON(t, "/api/settings")
	var body struct {
		Providers []struct {
			Name, Title string
			Kind        string
			Configured  bool
			Fields      []struct{ Key, Kind, Value string; HasValue bool }
		} []struct {
			Provider string `json:"provider"`
			Enabled  bool
		}
		Storage map[string]any
		DataDir string `json:"data_dir"`
		Volc    map[string]any
		MVSEP   map[string]any `json:"mvsep"`
	}
	s.decode(t, w, &body)
	if len(body.Providers) < 6 {
		t.Errorf("providers 数量 = %d", len(body.Providers))
	}
	if body.Volc != nil || body.MVSEP != nil {
		t.Error("旧 volc/mvsep 平铺段应已删除")
	}
	names := map[string]bool{}
	for _, p := range body.Providers {
		names[p.Name] = true
		if p.Kind != "cloud" && p.Kind != "local" {
			t.Errorf("卡 %s kind = %q", p.Name, p.Kind)
		}
	}
	for _, want := range []string{"volcengine", "mediakit", "mvsep", "qianwen", "audiotool", "gsgc"} {
		if !names[want] {
			t.Errorf("缺少卡 %s", want)
		}
	}
}

// 新写端点：按卡保存成功；未知卡 4xx；storage 独立端点可用；旧 PUT /api/settings 404。
func TestPutSettingsProviders(t *testing.T) {
	s := newTestServer(t)
	w := s.putJSON(t, "/api/settings/providers/qianwen", map[string]any{
		"fields": map[string]string{"api_key": "sk-route-1"},
	})
	if !s.isOK(t, w) {
		t.Fatalf("保存失败: %s", w.Body)
	}
	w = s.putJSON(t, "/api/settings/providers/nope", map[string]any{"fields": map[string]string{}})
	if w.Code == 200 {
		t.Error("未知卡应 4xx")
	}
	w = s.putJSON(t, "/api/settings", map[string]any{"api_key": "x"})
	if w.Code != 404 {
		t.Errorf("旧端点应 404, got %d", w.Code)
	}
}

// test-connection 新结构：results 数组 + storage 段。
func TestTestConnectionShape(t *testing.T) {
	s := newTestServer(t)
	w := s.postJSON(t, "/api/settings/test-connection", nil)
	var body struct {
		Results []struct{ Name string; OK bool; Message string } `json:"results"`
		Storage struct{ OK bool; Message string }                `json:"storage"`
	}
	s.decode(t, w, &body)
	if len(body.Results) < 4 {
		t.Errorf("results = %+v", body.Results)
	}
	for _, r := range body.Results {
		if r.Message == "" {
			t.Errorf("卡 %s 缺 message", r.Name)
		}
	}
}
```

（`newTestServer`/`getJSON`/`putJSON`/`postJSON`/`decode`/`isOK` 按 routes_test.go 里既有的辅助函数实际名称与签名替换；该文件已有 settings 用例的话直接改写它们。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/server/ -run 'Settings|TestConnection' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`internal/server/routes.go`：

路由注册区（55-57 行）替换为：

```go
		api.GET("/settings", s.getSettings)
		api.PUT("/settings/providers/:name", s.requireAdmin(), s.putProviderSettings)
		api.PUT("/settings/storage", s.requireAdmin(), s.putStorageSettings)
		api.POST("/settings/test-connection", s.requireAdmin(), s.testConnection)
```

`getSettings`（456-488 行）替换为：

```go
func (s *Server) getSettings(c *gin.Context) {
	cfg := s.svc.Config()
	st := cfg.Storage
	ok(c, gin.H{
		"providers": s.svc.ProviderStates(cfg),
		// secret_key 不回传（回传 has_secret_key 供设置页展示「已配置」）。
		"storage": gin.H{
			"provider":       st.Provider,
			"endpoint":       st.Endpoint,
			"region":         st.Region,
			"bucket":         st.Bucket,
			"access_key":     st.AccessKey,
			"has_secret_key": st.SecretKey != "",
			"prefix":         st.Prefix,
			"enabled":        s.svc.StorageClient() != nil,
			"channels":       storageChannelsPayload(cfg.StorageChannels),
		},
		"data_dir": cfg.DataDir,
	})
}
```

`putSettingsReq`/`putSettings`（507-556 行）替换为两个新 handler（`putStorageReq` 结构体保留复用）：

```go
type putProviderSettingsReq struct {
	Fields map[string]string `json:"fields"`
}

// putProviderSettings 按卡保存凭证：声明校验 + 落盘 + 热重注册在 SaveProviderFields 内一体完成。
func (s *Server) putProviderSettings(c *gin.Context) {
	var req putProviderSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.SaveProviderFields(c.Param("name"), req.Fields); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true, "note": "凭证已保存并即时生效"})
}

// putStorageSettings 对象存储独立保存端点（body 与旧 PUT /api/settings 的 storage 分支一致）。
func (s *Server) putStorageSettings(c *gin.Context) {
	var req putStorageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.SaveStorage(config.StorageConfig{
		Provider:  req.Provider,
		Endpoint:  req.Endpoint,
		Region:    req.Region,
		Bucket:    req.Bucket,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Prefix:    req.Prefix,
	}); err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"ok": true, "note": "存储配置已保存"})
}
```

`testConnection`（558-572 行）替换为：

```go
// providerTest 卡探活结果（test-connection 的 results 数组元素）。
type providerTest struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) testConnection(c *gin.Context) {
	// 按卡动态探测：volcengine 极短合成、mediakit 鉴权探测、mvsep token+免费额度、
	// qianwen 极短合成；storage 桶探活（HeadBucket 不计费）。
	tests := []struct {
		name string
		fn   func() (string, bool)
	}{
		{"volcengine", s.svc.TestSpeechConnection},
		{"mediakit", s.svc.TestMediaKitConnection},
		{"mvsep", s.svc.TestMVSepConnection},
		{"qianwen", s.svc.TestQianwenConnection},
	}
	results := make([]providerTest, 0, len(tests))
	for _, tt := range tests {
		msg, okv := tt.fn()
		results = append(results, providerTest{Name: tt.name, OK: okv, Message: msg})
	}
	stMsg, stOK := s.svc.TestStorageConnection()
	ok(c, gin.H{
		"results": results,
		"storage": gin.H{"ok": stOK, "message": stMsg},
	})
}
```

同时清理 Task 7 的临时过渡（`_ = req` 等），删除 `putSettingsReq` 中凭证字段（整个类型删除，只留 `putStorageReq`）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/server/ -v && go build ./...`
Expected: PASS（既有引用旧端点/旧响应的用例按新契约改写后通过）

- [ ] **Step 5: Commit**

```bash
git add internal/server/routes.go internal/server/routes_test.go
git commit -m "feat(server): 设置 API 按卡读写，storage 独立端点，test-connection 动态结果"
```

---

### Task 9: 前端 · SettingsShape/useProviders + SettingsPage 两 Tab 重写

**Files:**
- Modify: `web/src/lib/useStorageEnabled.ts`
- Modify: `web/src/pages/SettingsPage.tsx`（整文件重写）

**Interfaces:**
- Consumes: Task 8 的 HTTP 契约
- Produces: `SettingsShape`（新版）、`ProviderShape`/`ProviderFieldShape` 类型、`useProviders()`/`useProviderConfigured(name)` hook（Task 10/11 消费）。

- [ ] **Step 1: 更新 useStorageEnabled.ts**

整文件替换为：

```ts
import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

/** 单个存储类型的独立配置段（secret 只回传 has_secret_key）。 */
export interface StorageChannelShape {
  endpoint: string;
  region: string;
  bucket: string;
  access_key: string;
  has_secret_key: boolean;
  prefix: string;
}

/** 凭证卡字段（secret 只回传 has_value）。 */
export interface ProviderFieldShape {
  key: string;
  label: string;
  kind: "text" | "secret" | "select";
  value?: string;
  has_value?: boolean;
  options?: { value: string; label: string }[];
  placeholder?: string;
  hint?: string;
  required?: boolean;
}

/** 凭证卡/能力卡（云端=凭证卡，本地=只读能力展示）。 */
export interface ProviderShape {
  name: string;
  title: string;
  description: string;
  kind: "cloud" | "local";
  order: number;
  configured: boolean;
  tools_count: number;
  fields: ProviderFieldShape[];
}

export interface SettingsShape {
  providers: ProviderShape[];
  storage?: {
    provider: string;
    endpoint: string;
    region: string;
    bucket: string;
    access_key: string;
    has_secret_key: boolean;
    prefix: string;
    enabled: boolean;
    /** 各存储类型独立配置（键=provider 名）：切换类型按段换显，互不覆盖。 */
    channels: Record<string, StorageChannelShape>;
  };
  data_dir: string;
}

/**
 * 设置读取（与设置页共用 ["settings"] 缓存：设置页保存后自动刷新）。
 */
export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => fetchJSON<SettingsShape>("/api/settings"),
  });
}

/**
 * 对象存储配置与启用态：enabled = provider 已配置且连接参数齐全，
 * URL-only 工具页据此展示「本地上传」通道。
 */
export function useStorageEnabled() {
  const q = useSettings();
  return {
    storage: q.data?.storage,
    enabled: !!q.data?.storage?.enabled,
    isLoading: q.isLoading,
  };
}

/** 全部卡（云端+本地）。 */
export function useProviders() {
  const q = useSettings();
  return { providers: q.data?.providers ?? [], isLoading: q.isLoading, refetch: q.refetch };
}

/** 指定厂商是否已配置凭证（undefined=设置未加载完成）。 */
export function useProviderConfigured(name: string) {
  const q = useSettings();
  return q.data?.providers.find((p) => p.name === name)?.configured;
}
```

- [ ] **Step 2: 重写 SettingsPage.tsx**

结构：`SecretInput`/`ConnBadge` 组件原样保留（现文件 52-96 行）；新增 `ProviderCardForm` 动态渲染一张卡；页面改为「云端服务 / 本地环境」两 Tab（用既有 `Tabs` 组件）。完整骨架：

```tsx
import { useState, type ChangeEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AudioLines, Bell, CheckCircle2, CloudUpload, Eye, EyeOff, FolderOpen, KeyRound, PlugZap, Server, XCircle } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { rotateToken, useMe } from "../lib/auth";
import { useSettings, type ProviderShape, type ProviderFieldShape } from "../lib/useStorageEnabled";
import { Button, Card, CardBody, CardHeader, Field, IconButton, Input, MicroLabel, PageHeader, Select, Skeleton, Tabs, useToast, type TabItem } from "../ui";

// —— SecretInput / ConnBadge：从现 SettingsPage.tsx 52-96 行原样搬入（一字不改）——
function SecretInput(props: { name: string; placeholder: string; id?: string } & Record<string, unknown>) { /* 搬入现实现 */ }
function ConnBadge({ result }: { result?: { ok: boolean; message: string } }) { /* 搬入现实现 */ }

/** 单卡状态徽标文案 */
function cardAside(p: ProviderShape) {
  return (
    <span className={`micro ${p.configured ? "" : "text-warn"}`}>
      {p.configured ? "已配置" : "未配置"}
    </span>
  );
}

/** 动态凭证卡：字段类型驱动渲染，整卡独立保存。 */
function ProviderCardForm({ card, onSaved }: { card: ProviderShape; onSaved: () => void }) {
  const { toast } = useToast();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      fetchJSON(`/api/settings/providers/${card.name}`, {
        method: "PUT",
        body: JSON.stringify({ fields: drafts }),
      }),
    onSuccess: () => {
      toast({ tone: "ok", title: "凭证已保存", description: "已即时生效，无需重启服务。" });
      setDrafts({});
      onSaved();
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });
  const fieldVal = (f: ProviderFieldShape) => drafts[f.key] ?? (f.kind === "select" ? (f.value ?? "") : "");
  return (
    <Card>
      <CardHeader title={card.title} icon={<KeyRound size={15} strokeWidth={1.75} />} aside={cardAside(card)} />
      <CardBody className="space-y-4">
        <p className="text-xs text-muted">{card.description}</p>
        {card.fields.map((f) => (
          <Field key={f.key} label={f.label} hint={f.hint ?? (f.kind === "secret" && f.has_value ? "当前已配置" : undefined)}>
            {({ id, ...rest }) =>
              f.kind === "select" ? (
                <Select id={id} value={fieldVal(f)} onChange={(e) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))} {...rest}>
                  {(f.options ?? []).map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </Select>
              ) : f.kind === "secret" ? (
                <SecretInput
                  id={id}
                  name={f.key}
                  value={fieldVal(f)}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))}
                  placeholder={f.placeholder ?? "留空表示不修改"}
                  {...rest}
                />
              ) : (
                <Input
                  id={id}
                  value={fieldVal(f)}
                  onChange={(e) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))}
                  placeholder={f.placeholder ?? ""}
                  {...rest}
                />
              )
            }
          </Field>
        ))}
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          保存{card.title.split(" ·")[0]}凭证
        </Button>
      </CardBody>
    </Card>
  );
}

/** 存储卡 + 通知卡 + 数据目录卡 + API Token 卡 + 连通性卡：
    从现 SettingsPage.tsx 对应段落搬入，仅两处改动：
    1) saveStorage 的 URL 改为 "/api/settings/storage"（fetchJSON PUT）；
    2) 连通性测试结果渲染改为 results 数组：
       {test.data?.results?.map((r) => <ConnBadge key={r.name} result={{ ok: r.ok, message: `${卡名(r.name)}：${r.message}` }} />)}
       卡名映射：const CARD_NAMES: Record<string,string> = { volcengine: "火山语音", mediakit: "MediaKit", mvsep: "MVSep", qianwen: "千问" } */

type SettingsTab = "cloud" | "local";

export default function SettingsPage() {
  const [tab, setTab] = useState<SettingsTab>("cloud");
  const { data, isLoading, refetch } = useSettings();
  const qc = useQueryClient();
  const cloud = (data?.providers ?? []).filter((p) => p.kind === "cloud");
  const local = (data?.providers ?? []).filter((p) => p.kind === "local");
  const TABS: TabItem<SettingsTab>[] = [
    { value: "cloud", label: "云端服务", icon: <CloudUpload size={13} strokeWidth={1.75} /> },
    { value: "local", label: "本地环境", icon: <Server size={13} strokeWidth={1.75} /> },
  ];
  const refresh = () => {
    void refetch();
    void qc.invalidateQueries({ queryKey: ["settings"] });
  };

  return (
    <>
      <PageHeader title="设置" description="云端服务凭证与本地环境，保存即时生效" />
      <div className="mb-4">
        <Tabs items={TABS} value={tab} onChange={setTab} />
      </div>

      {tab === "cloud" ? (
        <div className="space-y-4">
          {isLoading ? <Skeleton className="h-32 w-full" /> : cloud.map((p) => <ProviderCardForm key={p.name} card={p} onSaved={refresh} />)}
          {/* 存储卡（搬入现 325-443 行，saveStorage URL 改 /api/settings/storage） */}
          {/* 连通性测试卡（搬入现 485-524 行，结果渲染改 results 数组） */}
        </div>
      ) : (
        <div className="space-y-4">
          {/* 本地能力卡：local.map((p) => 卡片：title/description + `${p.tools_count} 个工具`) */}
          {/* 数据目录卡（搬入现 526-538 行） */}
          {/* 通知卡（搬入现 445-483 行） */}
          {/* API Token 卡（搬入现 539-563 行） */}
        </div>
      )}
    </>
  );
}
```

注释里标注的「搬入」段落按现文件行号整段复制，保持内部逻辑（localStorage 通知、rotateToken、通道草稿 state 等）不变。`data?.volc`/`data?.mvsep` 的旧引用全部移除（表单值来自 providers 数组）。

- [ ] **Step 3: 排查其他旧字段消费者**

Run: `cd web && grep -rn "\.volc\b\|mvsep\?" src/ --include="*.ts*"`
对命中处（如 AboutPage/SeparatePage 若读旧字段）改用 `useSettings()` 的对应段。SeparatePage 若用 `useStorageEnabled().enabled` 则无需改动。

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build`
Expected: tsc + vite 构建通过，无类型错误

- [ ] **Step 5: 手工走查 + Commit**

`npm run dev` 起本地前端对后端走查：两 Tab 切换、每张卡保存（观察 network 面板命中新端点）、存储卡保存、连通性测试渲染、本地 Tab 各卡。

```bash
git add web/src/lib/useStorageEnabled.ts web/src/pages/SettingsPage.tsx web/src/pages/AboutPage.tsx web/src/pages/SeparatePage.tsx
git commit -m "feat(web): 设置页云端/本地两 Tab，凭证卡动态渲染"
```

---

### Task 10: 前端 · TTSPage 引擎切换 + 千问面板

**Files:**
- Modify: `web/src/pages/TTSPage.tsx`
- Create: `web/src/pages/tts/QianwenTTSPanel.tsx`
- Create: `web/src/lib/qianwenVoices.ts`

**Interfaces:**
- Consumes: Task 9 的 `useProviderConfigured`、后端 qianwen.tts ParamSpecs
- Produces: `/tts?engine=qianwen` 路由态（ASRPage 同款模式）。

- [ ] **Step 1: 音色常量**

`web/src/lib/qianwenVoices.ts`（与后端 `qianwen/voices.go` 同词表，模式对齐 ASR_LANGUAGES 的「前端镜像后端」惯例）：

```ts
/** 千问 qwen3-tts 非实时音色（与后端 qianwen/voices.go 同词表） */
export const QIANWEN_VOICES: { value: string; label: string }[] = [
  { value: "Cherry", label: "Cherry · 阳光亲切（女）" },
  { value: "Serena", label: "Serena · 温柔（女）" },
  { value: "Ethan", label: "Ethan · 阳光北方（男）" },
  { value: "Chelsie", label: "Chelsie · 二次元（女）" },
  { value: "Momo", label: "Momo · 撒娇搞怪（女）" },
  { value: "Vivian", label: "Vivian · 可爱小暴躁（女）" },
  { value: "Moon", label: "Moon · 率性帅气（男）" },
  { value: "Maia", label: "Maia · 知性温柔（女）" },
  { value: "Kai", label: "Kai · 舒缓悦耳（男）" },
  { value: "Nofish", label: "Nofish · 设计师（男）" },
  { value: "Bella", label: "Bella · 小萝莉（女）" },
  { value: "Jennifer", label: "Jennifer · 电影质感美语（女）" },
  { value: "Ryan", label: "Ryan · 戏感炸裂（男）" },
  { value: "Katerina", label: "Katerina · 御姐韵律（女）" },
  { value: "Aiden", label: "Aiden · 美语大男孩（男）" },
  { value: "Eldric Sage", label: "Eldric Sage · 沉稳老者（男）" },
  { value: "Mia", label: "Mia · 温顺乖巧（女）" },
  { value: "Mochi", label: "Mochi · 早慧小大人（男）" },
  { value: "Bellona", label: "Bellona · 江湖豪情（女）" },
  { value: "Vincent", label: "Vincent · 沙哑烟嗓（男）" },
  { value: "Bunny", label: "Bunny · 萌小萝莉（女）" },
  { value: "Neil", label: "Neil · 新闻主持（男）" },
  { value: "Elias", label: "Elias · 知识讲解（女）" },
  { value: "Arthur", label: "Arthur · 乡村老者（男）" },
  { value: "Nini", label: "Nini · 软黏甜嗓（女）" },
  { value: "Seren", label: "Seren · 温和助眠（女）" },
  { value: "Pip", label: "Pip · 调皮童真（男）" },
  { value: "Stella", label: "Stella · 甜腻少女（女）" },
  { value: "Bodega", label: "Bodega · 西班牙大叔（男）" },
  { value: "Sonrisa", label: "Sonrisa · 拉美大姐（女）" },
  { value: "Alek", label: "Alek · 俄罗斯风（男）" },
  { value: "Dolce", label: "Dolce · 慵懒意大利（男）" },
  { value: "Sohee", label: "Sohee · 韩国欧尼（女）" },
  { value: "Ono Anna", label: "Ono Anna · 青梅竹马（女）" },
  { value: "Lenn", label: "Lenn · 德国青年（男）" },
  { value: "Emilien", label: "Emilien · 浪漫法国（男）" },
  { value: "Andre", label: "Andre · 磁性沉稳（男）" },
  { value: "Radio Gol", label: "Radio Gol · 足球解说（男）" },
  { value: "Jada", label: "Jada · 沪上阿姐（上海话）" },
  { value: "Dylan", label: "Dylan · 胡同少年（北京话）" },
  { value: "Li", label: "Li · 瑜伽老师（南京话）" },
  { value: "Marcus", label: "Marcus · 老陕（陕西话）" },
  { value: "Roy", label: "Roy · 台湾哥仔（闽南语）" },
  { value: "Peter", label: "Peter · 天津捧哏（天津话）" },
  { value: "Sunny", label: "Sunny · 川妹子（四川话）" },
  { value: "Eric", label: "Eric · 成都男子（四川话）" },
  { value: "Rocky", label: "Rocky · 粤语阿强（男）" },
  { value: "Kiki", label: "Kiki · 港妹（女）" },
];

export const QIANWEN_DEFAULT_VOICE = "Cherry";
```

- [ ] **Step 2: QianwenTTSPanel**

`web/src/pages/tts/QianwenTTSPanel.tsx`：以 TTSSyncPanel.tsx 为模板（整文件结构照搬：左文本卡 + 右参数卡 + 结果区 AudioRow/ProgressBody），改动点：
- 提交体：`{ provider: "qianwen", tool: "tts", params: { text, model, voice, language_type, instructions, format } }`；
- 参数区：模型 Select（qwen3-tts-flash / qwen3-tts-instruct-flash）、音色 Select（`QIANWEN_VOICES`）、语言 Input（placeholder「如 Chinese / English；留空不指定」）、`instructions` Textarea（仅 model 为 instruct 时渲染）、格式 Select（mp3/wav）；
- 删除 VoicePicker/voicesQuery/长文本分段警告（千问非流式单请求）与语速音量滑杆；
- `aside` 徽标：`qianwen · tts`；
- 结果区 `summary.char_count` 展示逻辑保留。

- [ ] **Step 3: TTSPage 引擎切换**

`web/src/pages/TTSPage.tsx` 改动：

```tsx
import QianwenTTSPanel from "./tts/QianwenTTSPanel";
import { useProviderConfigured } from "../lib/useStorageEnabled";
import { Link } from "react-router-dom";
// ...

type Engine = "volcengine" | "qianwen";

export default function TTSPage() {
  const [params, setParams] = useSearchParams();
  const engine: Engine = params.get("engine") === "qianwen" ? "qianwen" : "volcengine";
  const qianwenReady = useProviderConfigured("qianwen");
  // ... 现有 tab 逻辑仅 engine==="volcengine" 时生效

  const ENGINE_TABS: TabItem<Engine>[] = [
    { value: "volcengine", label: "火山引擎" },
    { value: "qianwen", label: "千问平台", disabled: qianwenReady === false },
  ];
  const switchEngine = (v: Engine) => {
    if (v === "volcengine") setParams({});
    else setParams({ engine: v });
  };

  return (
    <>
      <PageHeader title="语音合成" description="多引擎语音合成：火山引擎三通道 / 千问平台非流式" actions={/* 现历史链接 */} />
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <Tabs items={ENGINE_TABS} value={engine} onChange={switchEngine} />
      </div>
      {engine === "qianwen" ? (
        qianwenReady === false ? (
          <Card><CardBody>
            <p className="text-sm text-fg-2">尚未配置千问平台凭证。</p>
            <Link to="/settings" className="text-xs text-accent hover:opacity-80">去设置页配置千问 API Key →</Link>
          </CardBody></Card>
        ) : (
          <QianwenTTSPanel />
        )
      ) : (
        <>
          {/* 现有通道 Tabs + 通道说明条 + 三面板，原样保留 */}
        </>
      )}
    </>
  );
}
```

（`Tabs` 组件若不支持 `disabled`，查 `web/src/ui` 的 TabItem 类型；不支持就渲染千问 Tab 但切换时 toast 提示未配置。）

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build`
Expected: 构建通过

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/TTSPage.tsx web/src/pages/tts/QianwenTTSPanel.tsx web/src/lib/qianwenVoices.ts
git commit -m "feat(web): TTS 页引擎切换与千问非流式面板"
```

---

### Task 11: 前端 · ASRPage 引擎切换

**Files:**
- Modify: `web/src/pages/ASRPage.tsx`

**Interfaces:**
- Consumes: Task 9 `useProviderConfigured`/`useStorageEnabled`、后端 qianwen.asr ParamSpecs。

- [ ] **Step 1: 引擎态与提交分支**

在 ASRPage 顶部加：

```tsx
type Engine = "volcengine" | "qianwen";
// state: const [engine, setEngine] = useState<Engine>("volcengine");
const qianwenReady = useProviderConfigured("qianwen");
```

提交 mutation 的 `provider` 改为变量，params 组装按引擎分支：

```tsx
const provider = engine;
if (engine === "qianwen") {
  params = { srt: true, language_hints: language, diarization_enabled: diarization, model: qwenModel };
} else {
  params = { srt: true, language: language, version: artifactMode ? "sentence" : version };
  if (hotwords.trim()) params.hotwords = hotwords.trim();
}
// 三处 fetchJSON 的 "volcengine" 均改为 provider 变量
```

新增 state：`const [diarization, setDiarization] = useState(false); const [qwenModel, setQwenModel] = useState("qwen3-asr-flash-filetrans");`

- [ ] **Step 2: 输入区与参数区按引擎渲染**

- 「识别版本」Field：engine=qianwen 时换成「转写模型」Select（两个模型 option）+「说话人分离」checkbox（hint「区分不同说话人（≤2h 且单声道）」）；volcengine 时原样。
- 输入通道 Tabs（upload/url/recording）：qianwen 引擎只开放 `url` 与（storageEnabled 时的）`upload`，无 recording、无 sentence；`changeVersion` 等既有函数仅 volcengine 引擎调用。
- 语言 Select：两引擎共用同一个 `language` state，提交时按引擎映射为 `language_hints`（qianwen，值域 zh/en/ja/…）或 `language`（volcengine，值域 zh-CN/…）。qianwen 引擎下的语言下拉用简化词表：

```tsx
const QW_LANGUAGES = [
  { value: "", label: "自动识别" },
  { value: "zh", label: "中文" }, { value: "en", label: "英语" },
  { value: "ja", label: "日语" }, { value: "ko", label: "韩语" },
  { value: "de", label: "德语" }, { value: "fr", label: "法语" },
  { value: "ru", label: "俄语" }, { value: "es", label: "西班牙语" },
  { value: "pt", label: "葡萄牙语" }, { value: "it", label: "意大利语" },
];
```

- 热词 Field：仅 volcengine 渲染（qianwen v1 不支持热词参数）。
- 右侧参数卡 aside：`{engine} · asr`。
- 引擎 Tabs 放 PageHeader 下（与 TTSPage 同款，含未配置引导）：

```tsx
<div className="mb-4 flex flex-wrap items-center gap-3">
  <Tabs<Engine>
    items={[
      { value: "volcengine", label: "火山引擎" },
      { value: "qianwen", label: "千问平台", disabled: qianwenReady === false },
    ]}
    value={engine}
    onChange={(v) => { setEngine(v); setFile(null); setUrl(""); setFileError(""); }}
  />
</div>
```

- 切引擎时重置 version 相关 UI（version 回 sentence 不必要——qianwen 不用 version）。

- [ ] **Step 3: 构建验证 + 手工走查**

Run: `cd web && npm run build`
Expected: 构建通过。dev 模式走查：引擎切换、qianwen 提交载荷（network 面板 `{provider:"qianwen",tool:"asr"}`）、结果区/文稿联动复用。

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/ASRPage.tsx
git commit -m "feat(web): 语音识别页引擎切换（火山/千问 filetrans）"
```

---

### Task 12: CLI --engine

**Files:**
- Modify: `cmd/voxbox/tts.go`
- Modify: `cmd/voxbox/asr.go`

**Interfaces:**
- Consumes: Task 4/5 的工具 ParamSpecs（参数键 text/model/voice/language_type/instructions/format；url/model/language_hints/diarization_enabled/enable_itn）。

- [ ] **Step 1: tts.go**

`newTTSCommand` 增加 engine 参数并按引擎组装 params：

```go
var engine string
// flags:
f.StringVar(&engine, "engine", "volcengine", "合成引擎: volcengine|qianwen")
// 默认值随引擎：
if engine == "qianwen" && voice == "zh_female_cancan_mars_bigtts" && !cmd.Flags().Changed("voice") {
    voice = "Cherry"
}
var params map[string]any
switch engine {
case "volcengine":
    params = map[string]any{"text": text, "voice": voice, "format": format,
        "speed_ratio": speedRatio, "volume_ratio": volumeRatio}
case "qianwen":
    params = map[string]any{"text": text, "voice": voice, "format": format,
        "model": qwenModel, "instructions": instructions}
default:
    return fmt.Errorf("不支持的引擎 %q（可选 volcengine / qianwen）", engine)
}
return runToolSync(c, engine, "tts", params, nil, outPath, jsonOut)
```

新增 flags：`f.StringVar(&qwenModel, "qwen-model", "qwen3-tts-flash", "千问模型: qwen3-tts-flash|qwen3-tts-instruct-flash")`、`f.StringVar(&instructions, "instructions", "", "风格指令（仅千问 instruct 模型）")`。`--engine qianwen` 时 format 合法值 mp3/wav（默认 mp3）。

- [ ] **Step 2: asr.go**

同款 engine flag；qianwen 引擎参数集（无 version/hotwords）：

```go
case "qianwen":
    params = map[string]any{
        "srt": srt, "model": qwModel,
        "language_hints": language, "diarization_enabled": diarization,
    }
    if audioURL != "" {
        params["url"] = audioURL
    }
```

新增 flags：`f.StringVar(&qwModel, "qwen-model", "qwen3-asr-flash-filetrans", "千问转写模型")`、`f.BoolVar(&diarization, "diarization", false, "说话人分离（千问引擎）")`。volcengine 分支保持现逻辑；`--engine qianwen` 且 version 显式指定时报参数冲突。

- [ ] **Step 3: 编译 + 冒烟**

Run: `go build ./cmd/voxbox && ./$(go build -o /tmp/voxbox . 2>/dev/null; echo /tmp/voxbox) tts --engine qianwen "测试" 2>&1 | head -3`
Expected: 编译通过；无凭证时报「未配置千问 API Key…」指引（证明引擎路由到 qianwen 工具）。

- [ ] **Step 4: Commit**

```bash
git add cmd/voxbox/tts.go cmd/voxbox/asr.go
git commit -m "feat(cli): tts/asr 支持 --engine qianwen"
```

---

### Task 13: 收尾验证 + webdist 重建 + 文档

**Files:**
- Rebuild: `cmd/voxbox/webdist/`（`make web` 产物，仓库内提交）
- Modify: `docs/mcp.md` / `README.md`（如列有工具清单，补 qianwen.tts/qianwen.asr 与新设置端点说明）

- [ ] **Step 1: 全量后端测试**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 2: 前端构建并重建嵌入产物**

Run: `cd web && npm run build && cd .. && make web`
Expected: webdist 更新（git status 可见资产变更）

- [ ] **Step 3: 真机验收（有千问 Key 时）**

```bash
voxbox config set qianwen.api_key sk-…
voxbox tts --engine qianwen "你好，千问" --voice Cherry   # 产出 mp3 可播放
voxbox asr --engine qianwen --url https://…/sample.mp3    # 产出 txt+srt，时间戳正确
```

设置页走查：千问卡保存→连通性测试「千问平台：连接成功」；删 key 后工具报配置指引。
无 Key 环境跳过此步（httptest 覆盖协议逻辑），在交付说明里注明。

- [ ] **Step 4: 文档增补 + 终检提交**

Run: `grep -rn "volc.speech\|/api/settings" docs/ README.md | head`
对命中处补新端点/新厂商说明（简短即可）。

```bash
git add cmd/voxbox/webdist docs README.md
git commit -m "chore: 重建 webdist 并增补千问接入文档"
```

---

## Self-Review 记录

- **Spec 覆盖**：spec §3 元数据（Task 1/6）、§4 API（Task 8）、§5 设置页（Task 9）、§6 千问 TTS/ASR/探活（Task 4/5/7）、§7 引擎切换（Task 10/11）、§8 CLI（Task 12）、§9 测试（各任务内嵌 + Task 13 收尾）——全覆盖。
- **类型一致性**：`provider.EnsureURLInput`（Task 2 定义，Task 5 消费）、`provider.BuildSRT/SRTSegment`（Task 1→Task 5）、`qianwen.NewTTSTool/NewASRTool/RegisterAll/ReRegisterAll/BaseURL/DefaultVoice`（Task 4/5/7 内一致）、`SaveProviderFields(name, map[string]string)`（Task 7 定义，Task 8 调用）、`ProviderStates/FieldState`（Task 6→8）、前端 `useProviderConfigured`（Task 9→10/11）——已核对。
- **已知风险点**（执行时留意）：① 千问 TTS 原始 HTTP 体以 Task 4 Step 0 实测为准；② 前端 `Tabs` 是否支持 `disabled` 需查 ui 包；③ routes_test.go 既有 settings 用例需同步改写（Task 8 Step 1 已注明）。
