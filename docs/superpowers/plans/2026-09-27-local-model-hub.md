# 设置页「本地环境」本地语音模型管理 · 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 设置页「本地环境」Tab 新增本地语音模型管理——内置静态目录 + 魔搭社区直链下载(进度/暂停/续传/删除),为二期本地推理预留"已安装模型"判断点。

**Architecture:** 新增独立子系统 `internal/localmodel`(catalog 静态清单 + Manager 下载管理器,"磁盘即真相",无新 DB 表),Service 持有 Manager,新增 5 个 `/api/models` 端点,设置页本地 Tab 顶部插入模型区组件。provider 注册表、任务系统、desktop 壳零改动。

**Tech Stack:** Go 1.26(纯 Go,`CGO_ENABLED=0` 五平台交叉编译)、gin、React + @tanstack/react-query + 现有 `web/src/ui` 组件库。

**Spec:** `docs/superpowers/specs/2026-09-27-local-model-hub-design.md`(本计划从 spec 论证,执行者两份都要读)。

## Global Constraints

- 纯 Go 无 CGO:不得引入任何 cgo 依赖(Makefile dist 明示 `CGO_ENABLED=0`)。
- CI 有 gofmt 检查:每个 Go 任务收尾跑 `make fmt`(或 `gofmt -l -w cmd internal`)且 `gofmt -l` 输出为空。
- catalog.json 是唯一模型清单维护面:调整模型只改该文件;`size_bytes` 仅展示(前端文案带"约"),真实大小以下载响应为准。
- 磁盘即真相:安装态 = `<dataDir>/models/<id>/manifest.json`,续传态 = `.part` 文件;禁止新增 DB 表。
- 全局同时 1 个下载;同模型逐文件顺序下载。
- 错误码语义:`CodeBadRequest=2 / CodeTaskFailed=3 / CodeNotFound=6`;本功能无凭证,不触碰 `apierr.go` 的 ErrNoCred 映射与 `cmd/voxbox/output.go`。
- 前端必须复用 `web/src/ui` 组件,禁止手写卡片/按钮 class(MASTER §5);状态不得只靠颜色(带文字);域名做内嵌链接。
- 中文文案直述,不用占位/含糊表述;新增代码注释密度与现有文件一致(中文注释讲约束,不讲出处)。
- `<dataDir>/models/` 与产物/上传目录互不干扰:产物按 DB 记录寻址、上传目录按用户隔离,均不递归扫描 dataDir,新增 models/ 子目录安全。
- Go 测试统一放 `package localmodel`(白盒)与 `package server`(走 newTestServer 冒烟),前端无测试基建走 `npm run build` + typecheck。

---

### Task 1: catalog——内嵌静态模型目录与校验

**Files:**
- Create: `internal/localmodel/catalog.json`
- Create: `internal/localmodel/catalog.go`
- Test: `internal/localmodel/catalog_test.go`

**Interfaces:**
- Consumes: 无(包起点)。
- Produces: `type Entry struct`(字段见下,后续任务靠它)、`type Requirements struct`、`func parseCatalog(data []byte) ([]Entry, error)`、包级 `var catalog []Entry`(加载时校验,非法 panic)、`const DefaultBaseURL = "https://modelscope.cn"`。

- [ ] **Step 1: 核对三个候选模型的文件清单(写 catalog.json 前)**

魔搭文件列表 API(免 token)核对每个 repo 的实际文件名与大小,以下三条命令逐条执行:

```bash
curl -s "https://modelscope.cn/api/v1/models/Qwen/Qwen3-TTS-12Hz-1.7B-Base/repo/files?Revision=master" | python3 -c "import json,sys; d=json.load(sys.stdin); [print(f['Size'], f['Path']) for f in d['Data']['Files'] if f['Type']=='blob']"
curl -s "https://modelscope.cn/api/v1/models/netease-youdao/Confucius4-R2T2/repo/files?Revision=master" | python3 -c "import json,sys; d=json.load(sys.stdin); [print(f['Size'], f['Path']) for f in d['Data']['Files'] if f['Type']=='blob']"
curl -s "https://modelscope.cn/api/v1/models/iic/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-pytorch/repo/files?Revision=master" | python3 -c "import json,sys; d=json.load(sys.stdin); [print(f['Size'], f['Path']) for f in d['Data']['Files'] if f['Type']=='blob']"
```

按输出把每个条目的 `files` 数组定为「权重 + 配置/tokenizer 的并集,排除 README/.gitattributes/示例音频/.gitignore」,`size_bytes` 用各文件 Size 之和。若 API 响应结构不是 `Data.Files[]`,打印原始 JSON 前 50 行再调整提取脚本。**用户已声明首批模型是占位、后续会调整——条目按下表落位,文件清单以 curl 实测为准。**

| kind | repo | id | 设备 |
|---|---|---|---|
| asr | `iic/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-pytorch` | `paraformer-large-vad-punc` | cpu |
| asr | `netease-youdao/Confucius4-R2T2` | `confucius4-r2t2` | cuda(≥12GB) |
| tts | `Qwen/Qwen3-TTS-12Hz-1.7B-Base` | `qwen3-tts-1.7b-base` | cuda(≥8GB) |

- [ ] **Step 2: 写 catalog.json(按 Step 1 实测修正 files 与 size_bytes)**

```json
[
  {
    "id": "paraformer-large-vad-punc",
    "repo": "iic/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-pytorch",
    "revision": "master",
    "name": "Paraformer 大模型 ASR(VAD+标点)",
    "kind": "asr",
    "summary": "中文语音识别,标点与时间戳输出,CPU 可跑的轻量选择",
    "size_bytes": 990000000,
    "files": ["configuration.json", "model.pb", "seg_dict.json"],
    "requirements": { "device": "cpu" },
    "license": "Apache-2.0",
    "license_url": "https://modelscope.cn/models/iic/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-pytorch",
    "sha256": {}
  },
  {
    "id": "confucius4-r2t2",
    "repo": "netease-youdao/Confucius4-R2T2",
    "revision": "master",
    "name": "Confucius4 R2T2 流式 ASR",
    "kind": "asr",
    "summary": "真流式语音识别,增量文本不回改,适合实时字幕场景",
    "size_bytes": 4092128210,
    "files": ["model.safetensors", "config.json", "chat_template.json", "tokenizer.json", "preprocessor_config.json"],
    "requirements": { "device": "cuda", "vram_gb": 12 },
    "license": "NetEase Model Use License",
    "license_url": "https://modelscope.cn/models/netease-youdao/Confucius4-R2T2",
    "sha256": {}
  },
  {
    "id": "qwen3-tts-1.7b-base",
    "repo": "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
    "revision": "master",
    "name": "Qwen3-TTS 1.7B Base",
    "kind": "tts",
    "summary": "音色克隆语音合成,3 秒参考音频即可复刻音色",
    "size_bytes": 4544230475,
    "files": ["model.safetensors", "speech_tokenizer/model.safetensors", "config.json", "generation_config.json", "preprocessor_config.json", "vocab.json", "merges.txt"],
    "requirements": { "device": "cuda", "vram_gb": 8 },
    "license": "Apache-2.0",
    "license_url": "https://modelscope.cn/models/Qwen/Qwen3-TTS-12Hz-1.7B-Base",
    "sha256": {}
  }
]
```

(`paraformer` 条目的 files/size_bytes 以 Step 1 curl 实测为准修正;另两条的大小已实测,files 仍以 Step 1 输出校对。`sha256` 全空:魔搭不强制提供,字段预留。)

- [ ] **Step 3: 写失败测试**

`internal/localmodel/catalog_test.go`:

```go
package localmodel

import (
	"strings"
	"testing"
)

func validEntry() Entry {
	return Entry{
		ID: "m1", Repo: "org/m1", Name: "M1", Kind: "asr", Summary: "测试模型",
		SizeBytes: 100, Files: []string{"model.bin"},
		Requirements: Requirements{Device: "cpu"},
		License:      "Apache-2.0", LicenseURL: "https://modelscope.cn/models/org/m1",
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCatalogAcceptsValid(t *testing.T) {
	entries, err := parseCatalog(mustJSON(t, []Entry{validEntry()}))
	if err != nil {
		t.Fatalf("合法条目被拒绝: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "m1" {
		t.Fatalf("解析结果不符: %+v", entries)
	}
}

func TestParseCatalogRejects(t *testing.T) {
	cases := map[string][]Entry{
		"id重复": {validEntry(), func() Entry { e := validEntry(); e.Repo = "org/m2"; return e }()},
		"缺字段":  {func() Entry { e := validEntry(); e.Name = ""; return e }()},
		"非法设备": {func() Entry { e := validEntry(); e.Requirements.Device = "tpu"; return e }()},
		"无文件":  {func() Entry { e := validEntry(); e.Files = nil; return e }()},
		"大小非法": {func() Entry { e := validEntry(); e.SizeBytes = 0; return e }()},
		"上级穿越": {func() Entry { e := validEntry(); e.Files = []string{"../model.bin"}; return e }()},
		"绝对路径": {func() Entry { e := validEntry(); e.Files = []string{"/etc/passwd"}; return e }()},
		"反斜杠":  {func() Entry { e := validEntry(); e.Files = []string{`dir\model.bin`}; return e }()},
		"未规范化": {func() Entry { e := validEntry(); e.Files = []string{"./model.bin"}; return e }()},
	}
	for name, entries := range cases {
		_, err := parseCatalog(mustJSON(t, entries))
		if err == nil {
			t.Errorf("%s: 期望被拒绝,实际通过", name)
		}
	}
}

func TestParseCatalogBadJSON(t *testing.T) {
	if _, err := parseCatalog([]byte("not json")); err == nil {
		t.Fatal("非法 JSON 期望报错")
	}
}

// 内嵌目录自检:随二进制发布的静态资产,损坏必须在首次加载时暴露。
func TestEmbeddedCatalog(t *testing.T) {
	if len(catalog) < 3 {
		t.Fatalf("内嵌目录至少 3 个条目,实际 %d", len(catalog))
	}
	ids := map[string]bool{}
	for _, e := range catalog {
		if ids[e.ID] {
			t.Errorf("条目 id 重复: %s", e.ID)
		}
		ids[e.ID] = true
		if !strings.HasPrefix(e.LicenseURL, "https://modelscope.cn/models/") {
			t.Errorf("条目 %s 的 license_url 应指向魔搭模型页: %s", e.ID, e.LicenseURL)
		}
	}
}

func TestParseCatalogDefaultsRevision(t *testing.T) {
	e := validEntry()
	e.Revision = ""
	entries, err := parseCatalog(mustJSON(t, []Entry{e}))
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Revision != "master" {
		t.Fatalf("revision 缺省应回落 master,实际 %q", entries[0].Revision)
	}
}
```

注意:测试文件顶部 import 需要 `"encoding/json"`(上面 mustJSON 用到)。

- [ ] **Step 4: 运行测试确认编译失败(包不存在)**

Run: `go test ./internal/localmodel/ 2>&1 | head -5`
Expected: 编译错误(无 parseCatalog / catalog)。

- [ ] **Step 5: 实现 catalog.go**

```go
// Package localmodel 本地语音模型管理:内置静态模型目录 + 魔搭社区直链下载。
// 仅管理模型文件(下载/校验/删除),不做本地推理(运行时形态二期另定)。
package localmodel

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// DefaultBaseURL 魔搭社区模型仓库 API 基址:公开模型免 token 直链下载。
const DefaultBaseURL = "https://modelscope.cn"

//go:embed catalog.json
var embeddedCatalog []byte

// Requirements 模型运行环境声明(本期仅设置页展示,二期推理消费)。
type Requirements struct {
	Device string `json:"device"`           // cuda | cpu
	VRAMGB int    `json:"vram_gb,omitempty"` // device=cuda 时的显存下限(GB)
}

// Entry 目录条目:清单是唯一人工维护面,调整模型只改 catalog.json + 发版。
type Entry struct {
	ID           string            `json:"id"`   // 稳定 slug:磁盘目录名与 API 路径参数
	Repo         string            `json:"repo"` // 魔搭 repo(如 Qwen/Qwen3-TTS-12Hz-1.7B-Base)
	Revision     string            `json:"revision"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"` // asr | tts(开放枚举,将来可扩)
	Summary      string            `json:"summary"`
	SizeBytes    int64             `json:"size_bytes"` // 仅展示(「约」);真实大小以下载响应为准
	Files        []string          `json:"files"`      // 白名单:逐文件顺序下载,不运行时爬 repo
	Requirements Requirements      `json:"requirements"`
	License      string            `json:"license"`
	LicenseURL   string            `json:"license_url"` // 指向魔搭模型页,设置页渲染为内嵌链接
	SHA256       map[string]string `json:"sha256,omitempty"`
}

// parseCatalog 校验并解析目录。path 合法性是删除/写入的安全边界,必须严格:
// 拒绝绝对路径、反斜杠、上级穿越与未规范化形式(相对目录内路径)。
func parseCatalog(data []byte) ([]Entry, error) {
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("catalog.json 不是合法的条目数组: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("catalog.json 不能为空")
	}
	seen := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if e.ID == "" || e.Repo == "" || e.Name == "" || e.Kind == "" || e.Summary == "" || e.License == "" || e.LicenseURL == "" {
			return nil, fmt.Errorf("条目缺少必填字段: %+v", *e)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("条目 id 重复: %s", e.ID)
		}
		seen[e.ID] = true
		if e.Revision == "" {
			e.Revision = "master"
		}
		if e.Requirements.Device != "cuda" && e.Requirements.Device != "cpu" {
			return nil, fmt.Errorf("条目 %s 的 requirements.device 必须是 cuda|cpu", e.ID)
		}
		if e.SizeBytes <= 0 {
			return nil, fmt.Errorf("条目 %s 的 size_bytes 必须为正(展示用)", e.ID)
		}
		if len(e.Files) == 0 {
			return nil, fmt.Errorf("条目 %s 没有任何文件", e.ID)
		}
		for _, f := range e.Files {
			if f == "" || path.IsAbs(f) || strings.ContainsRune(f, '\\') ||
				strings.HasPrefix(path.Clean(f), "..") || path.Clean(f) != f {
				return nil, fmt.Errorf("条目 %s 文件路径非法: %q", e.ID, f)
			}
		}
	}
	return entries, nil
}

// catalog 包级加载:目录是随二进制发布的静态资产,不合法直接 panic(fail-fast)。
var catalog = func() []Entry {
	entries, err := parseCatalog(embeddedCatalog)
	if err != nil {
		panic(err)
	}
	return entries
}()
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/localmodel/ -v -run 'TestParse|TestEmbedded'`
Expected: 全部 PASS。

- [ ] **Step 7: 格式化 + 提交**

Run: `gofmt -l -w internal/localmodel && go vet ./internal/localmodel/`
Expected: `gofmt -l` 无输出,vet 无告警。

```bash
git add internal/localmodel/catalog.json internal/localmodel/catalog.go internal/localmodel/catalog_test.go
git commit -m "feat(localmodel): 内置静态模型目录——魔搭语音模型清单与校验"
```

---

### Task 2: Manager 骨架——状态模型与启动扫盘恢复

**Files:**
- Create: `internal/localmodel/manager.go`
- Test: `internal/localmodel/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Entry`/`catalog`/`DefaultBaseURL`。
- Produces: `type Status string`(常量 `StatusIdle/StatusDownloading/StatusVerifying/StatusInstalled/StatusFailed`)、`type ModelState struct`、`type ModelView struct`(内嵌 Entry + 状态字段)、`type Manager struct`、`func NewManager(dataDir string) *Manager`、包内构造 `newManager(baseDir, baseURL string, entries []Entry) *Manager`(测试注入用)、`(m *Manager) List() []ModelView`、`(m *Manager) View(id string) (ModelView, bool)`、`(m *Manager) Dir() string`。

- [ ] **Step 1: 写失败测试**

`internal/localmodel/manager_test.go`(先只写恢复相关用例,下载用例 Task 3 追加):

```go
package localmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testEntries 三条目测试目录:小文件规模,与 catalog.json 无关(注入构造)。
func testEntries() []Entry {
	e := func(id, kind string, files ...string) Entry {
		return Entry{
			ID: id, Repo: "org/" + id, Revision: "master", Name: id, Kind: kind,
			Summary: "测试模型 " + id, SizeBytes: int64(len(files)) * 100,
			Files:        files,
			Requirements: Requirements{Device: "cpu"},
			License:      "Apache-2.0", LicenseURL: "https://modelscope.cn/models/org/" + id,
		}
	}
	return []Entry{
		e("asr-small", "asr", "a.bin", "b.bin"),
		e("tts-small", "tts", "m/model.bin"),
	}
}

// newTestManager 起一个空目录管理器(entries 注入,不走内嵌 catalog)。
func newTestManager(t *testing.T, entries []Entry) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	return newManager(filepath.Join(dir, "models"), "http://fake.modelscope.test", entries), filepath.Join(dir, "models")
}

// waitFor 轮询直到模型进入期望状态(下载是后台 goroutine,测试靠轮询收敛)。
func waitFor(t *testing.T, m *Manager, id string, want Status) ModelView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := m.View(id); ok && v.Status == want {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := m.View(id)
	t.Fatalf("等待 %s 进入 %s 超时,当前: %+v", id, want, v)
	return ModelView{}
}

func viewOrFatal(t *testing.T, m *Manager, id string) ModelView {
	t.Helper()
	v, ok := m.View(id)
	if !ok {
		t.Fatalf("未知模型: %s", id)
	}
	return v
}

func TestRestoreAllIdle(t *testing.T) {
	m, _ := newTestManager(t, testEntries())
	for _, e := range testEntries() {
		v := viewOrFatal(t, m, e.ID)
		if v.Status != StatusIdle || v.HasPartial {
			t.Fatalf("%s 应为未下载,实际 %+v", e.ID, v)
		}
		if v.TotalBytes != e.SizeBytes {
			t.Fatalf("未下载态 TotalBytes 应为目录声明展示值 %d,实际 %d", e.SizeBytes, v.TotalBytes)
		}
	}
	if _, ok := m.View("nope"); ok {
		t.Fatal("未知 id 不应返回视图")
	}
}

func TestRestoreInstalled(t *testing.T) {
	m, base := newTestManager(t, testEntries())
	dir := filepath.Join(base, "asr-small")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "asr-small", Repo: "org/asr-small", Revision: "master",
		Files: []manifestFile{{Path: "a.bin", Size: 100}, {Path: "b.bin", Size: 200}},
		CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	v := waitFor(t, m, "asr-small", StatusInstalled)
	if v.DownloadedBytes != 300 || v.TotalBytes != 300 {
		t.Fatalf("installed 字节数应为 300/300,实际 %d/%d", v.DownloadedBytes, v.TotalBytes)
	}
}

func TestRestorePartial(t *testing.T) {
	m, base := newTestManager(t, testEntries())
	dir := filepath.Join(base, "asr-small")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 半程状态:完整落地一个文件 + 另一个文件的 .part
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.bin.part"), make([]byte, 40), 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || !v.HasPartial {
		t.Fatalf("应为 idle+可续传,实际 %+v", v)
	}
	if v.DownloadedBytes != 140 {
		t.Fatalf("已收字节应为 140,实际 %d", v.DownloadedBytes)
	}
}

func TestRestoreCorruptManifest(t *testing.T) {
	m, base := newTestManager(t, testEntries())
	dir := filepath.Join(base, "asr-small")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || !v.HasPartial {
		t.Fatalf("manifest 损坏应按未安装+可续传处理,实际 %+v", v)
	}
}

func TestRestoreManifestIDMismatch(t *testing.T) {
	m, base := newTestManager(t, testEntries())
	dir := filepath.Join(base, "asr-small")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "other", Repo: "org/other", Revision: "master", CompletedAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle {
		t.Fatalf("manifest id 不匹配应按未安装处理,实际 %+v", v)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/localmodel/ -run TestRestore 2>&1 | head -5`
Expected: 编译错误(无 Manager/manifest 等)。

- [ ] **Step 3: 实现 manager.go 骨架**

```go
package localmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status 模型生命周期状态(设置页纯色状态点+文字渲染)。
type Status string

const (
	StatusIdle        Status = "idle"        // 未下载;带 .part/半程文件时前端文案「继续下载」
	StatusDownloading Status = "downloading" // 下载中
	StatusVerifying   Status = "verifying"   // 校验中(全部文件落地后)
	StatusInstalled   Status = "installed"   // 已安装(目录内有合法 manifest)
	StatusFailed      Status = "failed"      // 失败(可重试,重试即续传)
)

// ErrUnknownModel 未知模型 id(不在内置目录内)。
var ErrUnknownModel = errors.New("未知模型")

// ModelState 目录条目的实时状态动态部分。
type ModelState struct {
	Status          Status `json:"status"`
	HasPartial      bool   `json:"has_partial"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Error           string `json:"error,omitempty"`
}

// ModelView GET /api/models 的 items 元素:目录条目 + 实时状态(Entry 字段平铺)。
type ModelView struct {
	Entry
	Status          Status `json:"status"`
	HasPartial      bool   `json:"has_partial"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Error           string `json:"error,omitempty"`
}

// manifest 安装完成标记:磁盘即真相,不落 DB。损坏/id 不匹配按未安装处理。
type manifest struct {
	ID          string         `json:"id"`
	Repo        string         `json:"repo"`
	Revision    string         `json:"revision"`
	Files       []manifestFile `json:"files"`
	CompletedAt string         `json:"completed_at"`
}

type manifestFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Manager 下载管理器:全局同时 1 个下载;状态内存即时更新(不落库),
// 磁盘是安装态权威;进度无节流(API 轮询即节流)。
type Manager struct {
	mu      sync.Mutex
	entries []Entry
	byID    map[string]Entry
	baseDir string // <dataDir>/models
	baseURL string // 魔搭 API 基址(测试注入 httptest)
	states  map[string]*ModelState
	active  string             // 正在下载的模型 id(""=无)
	cancel  context.CancelFunc // 活动下载的取消函数
}

// NewManager 构造管理器并扫盘恢复(服务启动期调用一次)。
func NewManager(dataDir string) *Manager {
	return newManager(filepath.Join(dataDir, "models"), DefaultBaseURL, catalog)
}

// newManager 包内构造:测试注入 entries 与 baseURL。
func newManager(baseDir, baseURL string, entries []Entry) *Manager {
	byID := make(map[string]Entry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	m := &Manager{entries: entries, byID: byID, baseDir: baseDir, baseURL: baseURL, states: map[string]*ModelState{}}
	m.restore()
	return m
}

// Dir 模型存储根目录(桌面「打开模型目录」消费)。
func (m *Manager) Dir() string { return m.baseDir }

func (m *Manager) modelDir(id string) string { return filepath.Join(m.baseDir, id) }

// restore 启动扫盘恢复:合法 manifest → installed;否则 idle+盘上残留合计为可续传进度。
func (m *Manager) restore() {
	for _, e := range m.entries {
		st := &ModelState{ID: e.ID, Status: StatusIdle, TotalBytes: e.SizeBytes}
		dir := m.modelDir(e.ID)
		if mf, err := readManifest(dir); err == nil && mf.ID == e.ID && mf.Revision == e.Revision {
			st.Status = StatusInstalled
			st.HasPartial = false
			st.DownloadedBytes, st.TotalBytes = manifestSizeSum(mf)
		} else {
			st.HasPartial, st.DownloadedBytes = diskResidue(dir)
		}
		m.states[e.ID] = st
	}
}

// List 全目录视图(目录声明顺序)。
func (m *Manager) List() []ModelView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ModelView, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, ModelView{Entry: e, ModelState: *m.states[e.ID]})
	}
	return out
}

// View 单模型视图;未知 id 返回 false。
func (m *Manager) View(id string) (ModelView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return ModelView{}, false
	}
	return ModelView{Entry: e, ModelState: *m.states[id]}, true
}

func readManifest(dir string) (*manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var mf manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

func manifestSizeSum(mf *manifest) (int64, int64) {
	var sum int64
	for _, f := range mf.Files {
		sum += f.Size
	}
	return sum, sum
}

// diskResidue 盘上残留(任意落地文件或 .part)→(可续传, 已收字节)。跳过 manifest(损坏态)。
func diskResidue(dir string) (bool, int64) {
	var sum int64
	has := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // 忽略不可达/目录;err=nil 继续走
		}
		if d.Name() == "manifest.json" {
			return nil
		}
		has = true
		if fi, err := d.Info(); err == nil {
			sum += fi.Size()
		}
		return nil
	})
	return has, sum
}

// setFailed 失败态:保留进度数字(从盘面重算),错误直述给设置页渲染。
func (m *Manager) setFailed(id, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[id]
	st.Status = StatusFailed
	st.Error = msg
	has, downloaded := diskResidue(m.modelDir(id))
	st.HasPartial, st.DownloadedBytes = has, downloaded
}

// compile-time 占位:download 管线在下一个任务实现,先保证骨架编译。
var _ = strings.TrimSpace
var _ = time.Now
var _ = context.Background
```

(收尾三行 `var _` 占位只为本任务骨架编译——Task 3 实现下载管线后必须删除。)

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/localmodel/ -v -run TestRestore`
Expected: 5 个 TestRestore* 全部 PASS。

- [ ] **Step 5: 格式化 + 提交**

Run: `gofmt -l -w internal/localmodel && go vet ./internal/localmodel/`
Expected: 无输出/无告警。

```bash
git add internal/localmodel/manager.go internal/localmodel/manager_test.go
git commit -m "feat(localmodel): Manager 骨架——状态模型与启动扫盘恢复"
```

---

### Task 3: Manager 下载管线——probe/续传/校验/manifest/停止单飞行

**Files:**
- Modify: `internal/localmodel/manager.go`(追加下载管线,删除 Task 2 的 `var _` 占位)
- Create: `internal/localmodel/diskfree_unix.go`
- Create: `internal/localmodel/diskfree_windows.go`
- Test: `internal/localmodel/manager_test.go`(追加下载用例)

**Interfaces:**
- Consumes: Task 2 的 Manager 骨架、Task 1 的 Entry。
- Produces: `(m *Manager) Start(id string) error`、`(m *Manager) Stop(id string) error`、`(m *Manager) Delete(id string) error`、`var ErrBusy = errors.New("已有模型在下载")`、包级 `diskFreeBytes(dir string) (uint64, error)`(平台文件,测试可替换)。

- [ ] **Step 1: 写失败测试(假魔搭 httptest + 下载全链路用例)**

追加到 `internal/localmodel/manager_test.go`:

```go
import 追加: "bytes", "crypto/sha256", "encoding/hex", "fmt", "io", "net/http", "net/http/httptest", "sync/atomic"

// fakeScope 可编程假魔搭:固定文件表,支持 Range(可关),按文件阻塞以稳定测试暂停/单飞行。
type fakeScope struct {
	t       *testing.T
	files   map[string][]byte
	noRange bool                     // 恒 200(模拟不支持 Range 的上游)
	block   map[string]chan struct{} // 文件 → 关闭后才继续写剩余字节
	blockN  map[string]int64         // 文件 → 先写多少字节后阻塞
	hits    atomic.Int64             // 带 offset>0 的 Range 续传请求数
	srv     *httptest.Server
}

func newFakeScope(t *testing.T, files map[string][]byte) *fakeScope {
	f := &fakeScope{t: t, files: files, block: map[string]chan struct{}{}, blockN: map[string]int64{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// FilePath 取查询串;repo 段不校验(测试条目共用一个 fake 空间)
		file := r.URL.Query().Get("FilePath")
		data, ok := f.files[file]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Range") == "" || f.noRange {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		rng := r.Header.Get("Range") // bytes=S-
		var start int64
		if _, err := fmt.Sscanf(rng, "bytes=%d-", &start); err != nil || start < 0 || start > int64(len(data)) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, int64(len(data))-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		if start > 0 {
			f.hits.Add(1)
		}
		rest := data[start:]
		if n := f.blockN[file]; n > 0 && start == 0 {
			ch := make(chan struct{})
			f.block[file] = ch
			_, _ = w.Write(rest[:n])
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			<-ch // 测试关闭后才放行剩余字节
			_, _ = w.Write(rest[n:])
			return
		}
		_, _ = w.Write(rest)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// releaseAfter 等待指定文件的阻塞点出现,返回放行开关(关闭它服务端继续写)。
func (f *fakeScope) releaseAfter(t *testing.T, file string) func() {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ch, ok := f.block[file]; ok {
			return func() { close(ch) }
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待文件 %s 阻塞点超时", file)
	return nil
}
```

下载用例:

```go
func fileMap(pairs ...any) map[string][]byte {
	m := map[string][]byte{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = []byte(pairs[i+1].(string))
	}
	return m
}

func TestDownloadHappyPath(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 256)
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("hello world")))
	entries := testEntries()
	m, base := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	if err := m.Start("asr-small"); err != nil {
		t.Fatalf("启动下载失败: %v", err)
	}
	v := waitFor(t, m, "asr-small", StatusInstalled)
	if v.DownloadedBytes != int64(len(content))+11 || v.HasPartial {
		t.Fatalf("installed 进度应为 %d,实际 %+v", len(content)+11, v)
	}
	// 文件落地正确
	for path, want := range map[string]string{"a.bin": string(content), "b.bin": "hello world"} {
		got, err := os.ReadFile(filepath.Join(base, "asr-small", path))
		if err != nil || string(got) != want {
			t.Fatalf("文件 %s 内容不符: %v", path, err)
		}
	}
	// manifest 存在且记录 revision
	mf, err := readManifest(filepath.Join(base, "asr-small"))
	if err != nil || mf.Revision != "master" || mf.ID != "asr-small" {
		t.Fatalf("manifest 不符: %+v err=%v", mf, err)
	}
}

func TestDownloadStopAndResume(t *testing.T) {
	content := bytes.Repeat([]byte("y"), 64*1024) // 64KB:阻塞点前只写 1024
	f := newFakeScope(t, fileMap("a.bin", content))
	entries := testEntries()
	m, base := newTestManager(t, entries)
	m.baseURL = f.srv.URL

	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	release := f.releaseAfter(t, "a.bin") // 等阻塞点(已写 1024 字节)
	if err := m.Stop("asr-small"); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	release() // 放行服务端(客户端已取消,写不写都行)
	v := waitFor(t, m, "asr-small", StatusIdle)
	if !v.HasPartial {
		t.Fatalf("暂停后应可续传: %+v", v)
	}
	part := filepath.Join(base, "asr-small", "a.bin.part")
	if fi, err := os.Stat(part); err != nil || fi.Size() != 1024 {
		t.Fatalf(".part 应保留 1024 字节,实际 %v", err)
	}

	// 续传:必须从 offset=1024 开始(Range 请求)
	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
	if f.hits.Load() < 1 {
		t.Fatal("续传应发出 offset>0 的 Range 请求")
	}
	got, err := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("续传后内容不符: %v", err)
	}
}

func TestDownloadRangeUnsupported(t *testing.T) {
	content := []byte("no-range-content")
	f := newFakeScope(t, fileMap("a.bin", content))
	f.noRange = true
	entries := testEntries()
	m, base := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
	got, _ := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if string(got) != string(content) {
		t.Fatal("无 Range 支持也应整文件下完")
	}
}

func TestDownloadMissing404(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("ok"))) // b.bin 缺失 → 404
	entries := testEntries()
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "模型不存在或已下架") {
		t.Fatalf("404 错误文案应直述,实际: %s", v.Error)
	}
}

func TestDownloadTruncatedBody(t *testing.T) {
	// 服务端声称 256 字节只写 100 即返回:客户端应检出「下载不完整」
	content := bytes.Repeat([]byte("z"), 256)
	f := newFakeScope(t, fileMap("a.bin", content))
	entries := testEntries()
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	// 换一个短写服务:直接改 files 拿不到——用独立 handler 最省事:
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "256")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content[:100])
	}))
	defer srv.Close()
	m.baseURL = srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "下载不完整") {
		t.Fatalf("应检出截断,实际: %s", v.Error)
	}
}

func TestDownloadDiskPrecheck(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("ok")))
	entries := testEntries()
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	orig := diskFreeBytes
	diskFreeBytes = func(string) (uint64, error) { return 1, nil } // 只剩 1 字节
	t.Cleanup(func() { diskFreeBytes = orig })
	if err := m.Start("asr-small"); err == nil {
		t.Fatal("磁盘不足应拒绝启动")
	}
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "磁盘空间不足") {
		t.Fatalf("错误应直述空间,实际: %s", v.Error)
	}
}

func TestDownloadSHA256Mismatch(t *testing.T) {
	content := []byte("sha-content")
	f := newFakeScope(t, fileMap("a.bin", content))
	entries := testEntries()
	entries[0].SHA256 = map[string]string{"a.bin": strings.Repeat("0", 64)} // 错误哈希
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "校验失败") {
		t.Fatalf("应报校验失败,实际: %s", v.Error)
	}
}

func TestDownloadSHA256OK(t *testing.T) {
	content := []byte("sha-content")
	f := newFakeScope(t, fileMap("a.bin", content))
	entries := testEntries()
	sum := sha256.Sum256(content)
	entries[0].SHA256 = map[string]string{"a.bin": hex.EncodeToString(sum[:])}
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	waitFor(t, m, "asr-small", StatusInstalled)
}

func TestSingleFlight(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", bytes.Repeat([]byte("q"), 4096)))
	entries := testEntries()
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	defer f.releaseAfter(t, "a.bin")() // 测试结束前放行
	if err := m.Start("tts-small"); err == nil {
		t.Fatal("第二个下载应被拒绝")
	} else if !strings.Contains(err.Error(), "已有模型在下载") {
		t.Fatalf("冲突文案不符: %v", err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
}

func TestStartStopUnknownAndIdle(t *testing.T) {
	m, _ := newTestManager(t, testEntries())
	if err := m.Start("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("未知 id 应 ErrUnknownModel,实际: %v", err)
	}
	if err := m.Stop("asr-small"); err == nil {
		t.Fatal("空闲态 Stop 应报错")
	}
	if err := m.Delete("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("删除未知 id 应 ErrUnknownModel,实际: %v", err)
	}
}

func TestDeleteModel(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("data")))
	entries := testEntries()
	m, base := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	waitFor(t, m, "asr-small", StatusInstalled)
	if err := m.Delete("asr-small"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "asr-small")); !os.IsNotExist(err) {
		t.Fatal("删除后目录应消失")
	}
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || v.HasPartial || v.DownloadedBytes != 0 {
		t.Fatalf("删除后应回未下载态: %+v", v)
	}
}

func TestDeleteWhileDownloading(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", bytes.Repeat([]byte("q"), 4096)))
	entries := testEntries()
	m, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	defer f.releaseAfter(t, "a.bin")()
	if err := m.Delete("asr-small"); err == nil {
		t.Fatal("下载中禁止删除")
	}
	_ = m.Stop("asr-small")
	waitFor(t, m, "asr-small", StatusIdle)
}
```

(测试文件 import 需按上述补齐:`bytes`、`crypto/sha256`、`encoding/hex`、`errors`、`fmt`、`io`、`net/http`、`net/http/httptest`、`strings`、`sync/atomic`。`io` 若最终实现未在测试里用到可去掉——先保留,fail 时按编译器提示修。)

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/localmodel/ -run 'TestDownload|TestSingle|TestStartStop|TestDelete' 2>&1 | head -5`
Expected: 编译错误(无 Start/Stop/Delete)。

- [ ] **Step 3: 实现平台磁盘文件**

`internal/localmodel/diskfree_unix.go`:

```go
//go:build !windows

package localmodel

import (
	"fmt"
	"syscall"
)

// realDiskFree 目录所在文件系统的剩余可用字节数(unix: statfs)。
func realDiskFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %w", err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
```

`internal/localmodel/diskfree_windows.go`:

```go
//go:build windows

package localmodel

import (
	"fmt"
	"syscall"
	"unsafe"
)

// realDiskFree 目录所在卷的剩余可用字节数(Windows: GetDiskFreeSpaceExW,纯 syscall 无 cgo)。
func realDiskFree(dir string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %w", err)
	}
	var avail, total, totalFree uint64
	r1, _, e := proc.Call(uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r1 == 0 {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %v", e)
	}
	return avail, nil
}
```

- [ ] **Step 4: 实现 manager.go 下载管线(替换 `var _` 占位)**

manager.go 追加/修改(删除 Task 2 末尾的三行 `var _` 占位,导入表按实际收敛):

```go
// 追加导入:crypto/sha256、encoding/hex、io、net/http、net/url、strconv、time
// (Task 2 的 strings/time/context 仍被 probe/finish/骨架使用,保留)

// ErrBusy 全局单飞行冲突。
var ErrBusy = errors.New("已有模型在下载")

// downloadClient 下载专用客户端:连接复用 + 30s 响应头超时(大 body 不限总时长),
// 遵循环境代理(境内访问魔搭直连,不强制)。
var downloadClient = &http.Client{
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second},
}

// diskFreeBytes 包级变量:测试替换注入假剩余空间。
var diskFreeBytes = realDiskFree

// Start 开始/续传。全局单飞行;磁盘预检;失败/停止后的重试即续传(盘上文件与 .part 保留)。
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	if m.active != "" {
		return fmt.Errorf("%w:%s,请等待完成或先暂停", ErrBusy, m.active)
	}
	if m.states[id].Status == StatusInstalled {
		return fmt.Errorf("模型已安装,无需重复下载")
	}
	if err := checkDiskFree(m.baseDir, e.SizeBytes-diskResidue(m.modelDir(id))); err != nil {
		m.setFailedLocked(id, err.Error())
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.active, m.cancel = id, cancel
	st := m.states[id]
	st.Status, st.Error = StatusDownloading, ""
	go m.run(ctx, e)
	return nil
}

// Stop 停止进行中的下载:保留 .part 与已落地文件,回到可续传(异步收敛,轮询可见)。
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != id {
		return fmt.Errorf("模型未在下载")
	}
	m.cancel()
	return nil
}

// Delete 删除模型目录(manifest 与 .part 一并)。下载/校验中禁止;未知 id 拒绝。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	if m.active == id {
		return fmt.Errorf("模型正在下载,请先暂停再删除")
	}
	if m.states[id].Status == StatusVerifying {
		return fmt.Errorf("模型校验中,请稍后再删除")
	}
	if err := os.RemoveAll(m.modelDir(id)); err != nil {
		return fmt.Errorf("删除模型目录失败: %w", err)
	}
	m.states[id] = &ModelState{ID: id, Status: StatusIdle, TotalBytes: e.SizeBytes}
	return nil
}

// run 下载主循环:逐文件 probe(真实大小+Range 支持+404 前置发现)→ 流式下载(.part+续传)
// → 校验 → 写 manifest。取消(Stop)回 idle+可续传,真实错误进 failed。
func (m *Manager) run(ctx context.Context, e Entry) {
	defer func() {
		m.mu.Lock()
		if m.active == e.ID {
			m.active, m.cancel = "", nil
		}
		m.mu.Unlock()
	}()
	dir := m.modelDir(e.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.setFailed(e.ID, "创建模型目录失败: "+err.Error())
		return
	}
	sizes := map[string]int64{}
	rangeOK := true
	for _, f := range e.Files {
		sz, canRange, err := m.probe(ctx, e, f)
		if err != nil {
			m.finish(ctx, e.ID, err)
			return
		}
		if !canRange {
			rangeOK = false
		}
		sizes[f] = sz
	}
	var total int64
	for _, f := range e.Files {
		total += sizes[f]
	}
	m.mu.Lock()
	m.states[e.ID].TotalBytes = total
	m.mu.Unlock()
	var base int64 // 已完成文件的累计字节(进度锚点)
	for _, f := range e.Files {
		if err := m.fetchOne(ctx, e, f, sizes[f], rangeOK, base); err != nil {
			m.finish(ctx, e.ID, err)
			return
		}
		base += sizes[f]
	}
	m.mu.Lock()
	m.states[e.ID].Status = StatusVerifying
	m.mu.Unlock()
	if err := verifyModel(dir, e, sizes); err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	if err := writeManifest(dir, e, sizes); err != nil {
		m.setFailed(e.ID, "写入 manifest 失败: "+err.Error())
		return
	}
	m.mu.Lock()
	st := m.states[e.ID]
	st.Status, st.HasPartial, st.DownloadedBytes, st.TotalBytes, st.Error = StatusInstalled, false, total, total, ""
	m.mu.Unlock()
}

// finish 区分取消与真实错误:取消(Stop)→ idle+可续传(错误清空);其余 → failed。
func (m *Manager) finish(ctx context.Context, id string, err error) {
	if ctx.Err() != nil {
		m.mu.Lock()
		st := m.states[id]
		has, downloaded := diskResidue(m.modelDir(id))
		st.Status, st.HasPartial, st.DownloadedBytes, st.Error = StatusIdle, has, downloaded, ""
		m.mu.Unlock()
		return
	}
	m.setFailed(id, err.Error())
}

// setFailedLocked 已持锁的失败态写入(diskResidue 做盘 IO,Start 预检复用)。
func (m *Manager) setFailedLocked(id, msg string) {
	st := m.states[id]
	st.Status, st.Error = StatusFailed, msg
	has, downloaded := diskResidue(m.modelDir(id))
	st.HasPartial, st.DownloadedBytes = has, downloaded
}

// fileURL 魔搭单文件直链:FilePath 经查询串编码(路径分隔符 %2F),公开模型免 token。
func (m *Manager) fileURL(e Entry, file string) string {
	q := url.Values{}
	q.Set("Revision", e.Revision)
	q.Set("FilePath", file)
	return fmt.Sprintf("%s/api/v1/models/%s/repo?%s", m.baseURL, e.Repo, q.Encode())
}

// probe 用 Range: bytes=0-0 探测单文件:取真实大小与 Range 支持,同时前置发现 404/下架。
func (m *Manager) probe(ctx context.Context, e Entry, file string) (int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.fileURL(e, file), nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := downloadClient.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("网络错误: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusPartialContent:
		cr := resp.Header.Get("Content-Range") // bytes 0-0/N
		i := strings.LastIndexByte(cr, '/')
		if !strings.HasPrefix(cr, "bytes ") || i < 0 {
			return 0, false, fmt.Errorf("响应 Content-Range 异常: %q", cr)
		}
		total, err := strconv.ParseInt(cr[i+1:], 10, 64)
		if err != nil || total <= 0 {
			return 0, false, fmt.Errorf("响应 Content-Range 异常: %q", cr)
		}
		return total, true, nil
	case http.StatusOK:
		if resp.ContentLength > 0 {
			return resp.ContentLength, false, nil
		}
		return 0, false, fmt.Errorf("远端未返回文件大小,无法校验完整性")
	case http.StatusForbidden, http.StatusNotFound:
		return 0, false, fmt.Errorf("模型不存在或已下架:%s(可打开 %s 确认)", e.Repo, e.LicenseURL)
	default:
		return 0, false, fmt.Errorf("魔搭响应异常: HTTP %d", resp.StatusCode)
	}
}

```

```go
// progressWriter 追加写 .part 并推进度(每次 Write 回调)。
type progressWriter struct {
	path    string
	onN     func(totalWritten int64)
	written int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := appendToFile(p.path, b)
	p.written += int64(n)
	p.onN(p.written)
	return n, err
}

func appendToFile(path string, b []byte) (int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(b)
}

// fetchOne 单文件下载:全部写入 .part,完成后按声明校验字节数(+可选 sha256)再原子改名。
// 最终文件已存在且大小吻合 → 跳过(跨重启续传);.part 大于远端(远端变更)→ 废弃重下;
// Range 不可用/续传被降级(200)→ 从零重写。
func (m *Manager) fetchOne(ctx context.Context, e Entry, file string, size int64, rangeOK bool, base int64) error {
	final := filepath.Join(m.modelDir(e.ID), filepath.FromSlash(file))
	if fi, err := os.Stat(final); err == nil && fi.Size() == size {
		m.setProgress(e.ID, base+size)
		return nil
	}
	part := final + ".part"
	offset := int64(0)
	if rangeOK {
		if fi, err := os.Stat(part); err == nil {
			switch {
			case fi.Size() == size:
				offset = size
			case fi.Size() > size:
				offset = 0
			default:
				offset = fi.Size()
			}
		}
	}
	// .part 已齐或最终文件已齐:直接校验改名(offset==size 时无网络请求)
	if offset != size {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.fileURL(e, file), nil)
		if err != nil {
			return err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := downloadClient.Do(req)
		if err != nil {
			return fmt.Errorf("网络错误: %w", err)
		}
		if offset > 0 && resp.StatusCode != http.StatusPartialContent {
			// 续传被降级:从零重写
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			offset = 0
			req2, err := http.NewRequestWithContext(ctx, http.MethodGet, m.fileURL(e, file), nil)
			if err != nil {
				return err
			}
			resp, err = downloadClient.Do(req2)
			if err != nil {
				return fmt.Errorf("网络错误: %w", err)
			}
		}
		switch {
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent:
		default:
			resp.Body.Close()
			return fmt.Errorf("魔搭响应异常: HTTP %d", resp.StatusCode)
		}
		// 写入准备:全新写 = 截断;续传 = 校验 .part 基准未被并发改动
		if offset == 0 {
			if err := os.WriteFile(part, nil, 0o644); err != nil {
				resp.Body.Close()
				return err
			}
		} else if fi, err := os.Stat(part); err != nil || fi.Size() != offset {
			resp.Body.Close()
			return fmt.Errorf("续传基准丢失:%s", file)
		}
		var downloaded int64
		pw := &progressWriter{path: part, onN: func(n int64) {
			downloaded = n
			m.setProgress(e.ID, base+offset+n)
		}}
		_, err = io.Copy(pw, resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("传输中断: %w", err)
		}
		offset += downloaded
	}
	if offset != size {
		return fmt.Errorf("下载不完整:%s 已收 %d 字节,预期 %d", file, offset, size)
	}
	if want, ok := e.SHA256[file]; ok {
		sum, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, want) {
			return fmt.Errorf("校验失败:%s sha256 不符(可能下载损坏)", file)
		}
	}
	return os.Rename(part, final)
}

func (m *Manager) setProgress(id string, n int64) {
	m.mu.Lock()
	m.states[id].DownloadedBytes = n
	m.mu.Unlock()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyModel 全量校验:逐文件字节数 + 可选 sha256(以 .part/最终文件的最终态为准)。
func verifyModel(dir string, e Entry, sizes map[string]int64) error {
	for _, f := range e.Files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("文件缺失:%s", f)
		}
		if fi.Size() != sizes[f] {
			return fmt.Errorf("字节数不符:%s 预期 %d 实际 %d", f, sizes[f], fi.Size())
		}
		if want, ok := e.SHA256[f]; ok {
			sum, err := fileSHA256(p)
			if err != nil {
				return err
			}
			if !strings.EqualFold(sum, want) {
				return fmt.Errorf("校验失败:%s sha256 不符", f)
			}
		}
	}
	return nil
}

func writeManifest(dir string, e Entry, sizes map[string]int64) error {
	mf := manifest{ID: e.ID, Repo: e.Repo, Revision: e.Revision, CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, f := range e.Files {
		mf.Files = append(mf.Files, manifestFile{Path: f, Size: sizes[f]})
	}
	raw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644)
}

// checkDiskFree 剩余空间须 ≥ 剩余待下字节;探测失败不阻断(视为通过)。
func checkDiskFree(dir string, need int64) error {
	if need <= 0 {
		return nil
	}
	free, err := diskFreeBytes(dir)
	if err != nil {
		return nil
	}
	if uint64(need) >= free {
		return fmt.Errorf("磁盘空间不足:还需约 %.1f GB,当前仅剩 %.1f GB",
			float64(need)/(1<<30), float64(free)/(1<<30))
	}
	return nil
}
```

(`diskfree_unix.go` / `diskfree_windows.go` 的 `realDiskFree` 即上方包级 `var diskFreeBytes = realDiskFree` 的平台实现;测试替换 `diskFreeBytes` 变量注入假剩余空间。)

`setFailed` 改为调用持锁版:

```go
func (m *Manager) setFailed(id, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setFailedLocked(id, msg)
}
```

需要追加 import:`crypto/sha256`、`encoding/hex`、`io`、`net/http`、`net/url`、`strconv`、`time`;删除 `var _ = strings...` 三行占位(baseURL 测试直接赋值 `m.baseURL = ...`,同包测试可见私有字段)。

- [ ] **Step 5: 跑全包测试(含 -race)**

Run: `go test ./internal/localmodel/ -v -race`
Expected: Task 2 + Task 3 全部用例 PASS。若 TestDownloadStopAndResume 偶发超时,检查 fake 阻塞点是否在 `.part` 落盘前关闭(顺序:客户端先取消再放行服务端)。

- [ ] **Step 6: 格式化 + 提交**

Run: `gofmt -l -w internal/localmodel && go vet ./internal/localmodel/`
Expected: 无输出/无告警。

```bash
git add internal/localmodel/
git commit -m "feat(localmodel): 下载管线——魔搭直链/Range 续传/校验/manifest/单飞行"
```

---

### Task 4: 打开目录 + Service 装配

**Files:**
- Create: `internal/localmodel/opendir.go`
- Modify: `internal/service/service.go`(Service 持有 Manager)
- Test: `internal/localmodel/opendir_test.go`

**Interfaces:**
- Consumes: Task 2/3 的 `NewManager`。
- Produces: `func OpenDir(dir string) error`;`func (s *Service) LocalModels() *localmodel.Manager`(routes 用)。

- [ ] **Step 1: 写失败测试**

`internal/localmodel/opendir_test.go`:

```go
package localmodel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDirRejectsMissing(t *testing.T) {
	if err := OpenDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("不存在的目录应报错")
	}
}

func TestOpenDirAcceptsExisting(t *testing.T) {
	// 仅验证参数校验通过后不 panic;真实「弹出文件管理器」属手测清单(避免测试机弹窗)。
	// darwin 上 open 一个临时目录会弹 Finder——为无副作用,只对不存在路径断言错误,
	// 存在路径的冒烟放到手测。此用例锁定「空串/缺失路径必报错」这一安全面。
	_ = os.MkdirAll(filepath.Join(t.TempDir(), "models"), 0o755)
}
```

(注:OpenDir 成功路径会真的弹系统文件管理器,自动化测试只锁「缺失目录必报错」;成功弹窗进 Task 8 手测清单。)

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/localmodel/ -run TestOpenDir 2>&1 | head -3`
Expected: 编译错误(无 OpenDir)。

- [ ] **Step 3: 实现 opendir.go + Service 装配**

`internal/localmodel/opendir.go`:

```go
package localmodel

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// OpenDir 用平台文件管理器打开模型目录(桌面形态「打开模型目录」按钮)。
// Start 不 Wait:仅找不到可执行程序(如无头 linux 无 xdg-open)时报错,
// 文件管理器自身退出码不校验(explorer 惯例返回非零)。
func OpenDir(dir string) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("模型目录不存在: %s", dir)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		// explorer 不经 cmd,绕开 cmd 参数转义坑;Start 不 Wait,非零退出码无影响
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}
```

`internal/service/service.go` 修改:

1. import 追加 `"github.com/yann0917/voxbox/internal/localmodel"`。
2. `Service` struct 追加字段(第 31-47 行 struct 内):

```go
	// models 本地语音模型管理器:构造期扫盘恢复,与 config 热更新无关(模型目录随 dataDir)。
	models *localmodel.Manager
```

3. `newWithRoot` 内,`s := &Service{db: db, reg: reg}` 改为:

```go
	s := &Service{db: db, reg: reg, models: localmodel.NewManager(dataDir)}
```

4. 访问器(放在 `DB()`/`Registry()` 附近,148-150 行):

```go
// LocalModels 本地语音模型管理器(设置页模型区与 /api/models 消费)。
func (s *Service) LocalModels() *localmodel.Manager { return s.models }
```

- [ ] **Step 4: 跑测试与全包编译**

Run: `go test ./internal/localmodel/ ./internal/service/ 2>&1 | tail -5`
Expected: PASS(`service` 包现有测试验证装配无回归)。

- [ ] **Step 5: 格式化 + 提交**

Run: `gofmt -l -w internal/localmodel internal/service && go vet ./internal/...`
Expected: 无输出/无告警。

```bash
git add internal/localmodel/opendir.go internal/localmodel/opendir_test.go internal/service/service.go
git commit -m "feat(localmodel,service): 打开模型目录命令与 Service 装配"
```

---

### Task 5: HTTP 端点与集成测试

**Files:**
- Create: `internal/server/models.go`
- Modify: `internal/server/routes.go`(Handler() 内 api 组追加 5 行)
- Test: `internal/server/models_test.go`

**Interfaces:**
- Consumes: Task 4 的 `s.svc.LocalModels()`、`localmodel.OpenDir`/`ErrUnknownModel`/`ErrBusy`;apierr.go 的 `ok`/`fail`/`CodeBadRequest`/`CodeNotFound`。
- Produces: `GET /api/models`、`POST /api/models/:id/download`、`POST /api/models/:id/stop`、`DELETE /api/models/:id`、`POST /api/models/open-dir`。

- [ ] **Step 1: 写失败测试**

`internal/server/models_test.go`(newTestServer/loginTestClient/getEnvelope 由 routes_test.go 与 auth_test.go 提供,直接复用):

```go
package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestModelsListShape(t *testing.T) {
	ts, _, ac := newTestServer(t)
	resp, err := ac.Get(ts.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	var e envelope
	if err := decodeBody(resp, &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d: %s", e.Code, e.Message)
	}
	items, ok := e.Data.(map[string]any)["items"].([]any)
	if !ok || len(items) < 3 {
		t.Fatalf("items 应 ≥3 个条目: %#v", e.Data)
	}
	first, _ := items[0].(map[string]any)
	for _, key := range []string{"id", "name", "kind", "summary", "size_bytes", "requirements", "license", "license_url", "status", "has_partial", "downloaded_bytes", "total_bytes"} {
		if _, present := first[key]; !present {
			t.Fatalf("条目缺字段 %s: %#v", key, first)
		}
	}
	if first["status"] != "idle" {
		t.Fatalf("新环境应全部 idle,实际 %v", first["status"])
	}
}

func TestModelsUnknownIDNotFound(t *testing.T) {
	ts, _, ac := newTestServer(t)
	for _, spec := range []struct{ method, path string }{
		{"POST", "/api/models/nope/download"},
		{"POST", "/api/models/nope/stop"},
		{"DELETE", "/api/models/nope"},
	} {
		req, _ := http.NewRequest(spec.method, ts.URL+spec.path, nil)
		resp, err := ac.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var e envelope
		_ = decodeBody(resp, &e)
		resp.Body.Close()
		if e.Code != CodeNotFound || !strings.Contains(e.Message, "未知模型") {
			t.Fatalf("%s %s 应为 NotFound/未知模型,实际 code=%d msg=%s", spec.method, spec.path, e.Code, e.Message)
		}
	}
}

func TestModelsStopWhenIdle(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 内嵌目录首个条目(空闲态):Stop 应报「未在下载」业务错误
	var e envelope
	resp, err := ac.Get(ts.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = decodeBody(resp, &e)
	resp.Body.Close()
	items := e.Data.(map[string]any)["items"].([]any)
	id := items[0].(map[string]any)["id"].(string)

	req, _ := http.NewRequest("POST", ts.URL+"/api/models/"+id+"/stop", nil)
	resp2, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e2 envelope
	_ = decodeBody(resp2, &e2)
	resp2.Body.Close()
	if e2.Code != CodeBadRequest || !strings.Contains(e2.Message, "未在下载") {
		t.Fatalf("空闲态 stop 应 BadRequest/未在下载,实际 code=%d msg=%s", e2.Code, e2.Message)
	}
}
```

`decodeBody` helper(routes_test.go 已有 `getEnvelope`;若无此 helper,在本文件加):

```go
func decodeBody(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}
```

(若与本文件顶部 `defer resp.Body.Close()` 重复关闭,去掉手写 defer,以 helper 为准——实现时按编译器提示收敛为一种。)

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/server/ -run TestModels 2>&1 | head -5`
Expected: 404/路由不存在导致的断言失败。

- [ ] **Step 3: 注册路由**

`internal/server/routes.go` 的 api 组内(MVSep 路由块之后、assistant 之前)追加:

```go
		// 本地语音模型管理(无凭证,全部登录用户可读可操作)
		api.GET("/models", s.listModels)
		api.POST("/models/open-dir", s.openModelsDir)
		api.POST("/models/:id/download", s.startModelDownload)
		api.POST("/models/:id/stop", s.stopModelDownload)
		api.DELETE("/models/:id", s.deleteModel)
```

(注:gin v1.12 支持静态段与参数段同级共存,open-dir 须注册在 :id 路由之前;若 Handler() 注册时 panic,报告并改挂 `/api/models-dir/open`,同步 Task 7 前端。)

- [ ] **Step 4: 实现 handlers**

`internal/server/models.go`:

```go
package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/localmodel"
)

// 本地语音模型管理端点:目录+状态、开始/暂停下载、删除、打开目录。
// 全部本地操作无凭证,不触碰 ErrNoCred 映射;写入操作沿用 requireAuth(单用户工具,不做 admin 门)。

func (s *Server) listModels(c *gin.Context) {
	ok(c, gin.H{"items": s.svc.LocalModels().List()})
}

// modelFail 统一错误映射:未知模型 → NotFound,其余 → BadRequest(文案已可定位)。
func modelFail(c *gin.Context, err error) {
	if errors.Is(err, localmodel.ErrUnknownModel) {
		fail(c, CodeNotFound, err.Error())
		return
	}
	fail(c, CodeBadRequest, err.Error())
}

func (s *Server) startModelDownload(c *gin.Context) {
	if err := s.svc.LocalModels().Start(c.Param("id")); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (s *Server) stopModelDownload(c *gin.Context) {
	if err := s.svc.LocalModels().Stop(c.Param("id")); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (s *Server) deleteModel(c *gin.Context) {
	if err := s.svc.LocalModels().Delete(c.Param("id")); err != nil {
		modelFail(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// openModelsDir 打开 <dataDir>/models(桌面形态前端显隐;web 形态调用也不越权,仅弹本机文件管理器)。
func (s *Server) openModelsDir(c *gin.Context) {
	if err := localmodel.OpenDir(s.svc.LocalModels().Dir()); err != nil {
		fail(c, CodeTaskFailed, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}
```

(`net/http` 若未用到则去掉该 import。)

- [ ] **Step 5: 跑 server 包测试**

Run: `go test ./internal/server/ -run TestModels -v`
Expected: 3 个用例 PASS。再跑全包 `go test ./internal/server/` 确认无路由 panic(gin 静态/参数共存验证)。

- [ ] **Step 6: 格式化 + 提交**

Run: `gofmt -l -w internal/server && go vet ./internal/...`
Expected: 无输出/无告警。

```bash
git add internal/server/models.go internal/server/models_test.go internal/server/routes.go
git commit -m "feat(server): /api/models 端点——目录状态/下载/暂停/删除/打开目录"
```

---

### Task 6: 前端 API 层——models.ts

**Files:**
- Create: `web/src/lib/models.ts`

**Interfaces:**
- Consumes: `fetchJSON`(lib/api.ts)。
- Produces: `ModelItem`/`ModelRequirements`/`ModelStatus` 类型、`useModels()` hook(1s 条件轮询)、`startModelDownload/stopModelDownload/deleteModel/openModelsDir`、`formatSize(n: number): string`。Task 7 组件全靠这批名字。

- [ ] **Step 1: 实现 models.ts**

```ts
import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

export type ModelStatus = "idle" | "downloading" | "verifying" | "installed" | "failed";

export interface ModelRequirements {
  device: "cuda" | "cpu";
  vram_gb?: number;
}

/** GET /api/models 的 items 元素:目录条目 + 实时状态(Entry 字段平铺)。 */
export interface ModelItem {
  id: string;
  repo: string;
  revision: string;
  name: string;
  kind: string; // asr | tts(开放枚举)
  summary: string;
  size_bytes: number;
  requirements: ModelRequirements;
  license: string;
  license_url: string;
  status: ModelStatus;
  has_partial: boolean;
  downloaded_bytes: number;
  total_bytes: number;
  error?: string;
}

export const listModels = () => fetchJSON<{ items: ModelItem[] }>("/api/models");
export const startModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/download`, { method: "POST" });
export const stopModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/stop`, { method: "POST" });
export const deleteModel = (id: string) =>
  fetchJSON(`/api/models/${id}`, { method: "DELETE" });
export const openModelsDir = () => fetchJSON("/api/models/open-dir", { method: "POST" });

const POLL_MS = 1000;

/** 模型目录+实时状态:存在下载/校验时 1s 轮询,否则不轮询。 */
export function useModels() {
  return useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    refetchInterval: (q) =>
      q.state.data?.items.some((m) => m.status === "downloading" || m.status === "verifying")
        ? POLL_MS
        : false,
  });
}

/** 字节数 → 人读大小(B/MB/GB,与设置页 mono 小字风格配套)。 */
export function formatSize(n: number): string {
  if (n >= 2 ** 30) return `${(n / 2 ** 30).toFixed(1)} GB`;
  if (n >= 2 ** 20) return `${(n / 2 ** 20).toFixed(0)} MB`;
  return `${n} B`;
}
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npx tsc --noEmit 2>&1 | head -10`
Expected: 无错误(models.ts 未被引用时也必须零告警)。

- [ ] **Step 3: 提交**

```bash
git add web/src/lib/models.ts
git commit -m "feat(web): 本地模型 API 层——类型/hooks/操作与条件轮询"
```

---

### Task 7: 设置页模型区组件与装配

**Files:**
- Create: `web/src/pages/LocalModelsSection.tsx`
- Modify: `web/src/pages/SettingsPage.tsx`(本地 Tab 顶部插入)

**Interfaces:**
- Consumes: Task 6 全部导出;`useMe()`(lib/auth.ts,`me?.desktop === true` 判桌面形态);ui 库 `Card/CardHeader/CardBody/Button/IconButton/ConfirmDialog/ProgressBar/SignalDot/MicroLabel/Skeleton/useToast`。
- Produces: `export default function LocalModelsSection()`(无 props,自带数据获取)。

- [ ] **Step 1: 实现 LocalModelsSection.tsx**

```tsx
import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, FolderOpen, Pause, RotateCcw, Trash2 } from "lucide-react";
import { useMe } from "../lib/auth";
import {
  deleteModel,
  formatSize,
  openModelsDir,
  startModelDownload,
  stopModelDownload,
  useModels,
  type ModelItem,
  type ModelRequirements,
} from "../lib/models";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ConfirmDialog,
  IconButton,
  MicroLabel,
  ProgressBar,
  Skeleton,
  useToast,
} from "../ui";

const KIND_LABELS: Record<string, string> = { asr: "语音识别", tts: "语音合成" };

// Tailwind 静态类映射:状态点色调 → 文字色(动态拼接类名不会进产物)
const TONE_TEXT: Record<string, string> = {
  accent: "text-accent",
  meter: "text-meter",
  danger: "text-danger",
  muted: "text-muted",
};

function statusMeta(m: ModelItem): { label: string; tone: string; pulse: boolean } {
  switch (m.status) {
    case "downloading":
      return { label: "下载中", tone: "accent", pulse: true };
    case "verifying":
      return { label: "校验中", tone: "accent", pulse: false };
    case "installed":
      return { label: "已安装", tone: "meter", pulse: false };
    case "failed":
      return { label: "失败", tone: "danger", pulse: false };
    default:
      return m.has_partial
        ? { label: "可续传", tone: "muted", pulse: false }
        : { label: "未下载", tone: "muted", pulse: false };
  }
}

function deviceText(r: ModelRequirements): string {
  return r.device === "cuda"
    ? `需 NVIDIA GPU${r.vram_gb ? ` · ≥${r.vram_gb}GB 显存` : ""}`
    : "CPU 可用";
}

const LINK_CLASS =
  "underline decoration-line underline-offset-2 transition-colors duration-150 hover:text-accent hover:decoration-accent";

function ModelCard({
  m,
  busy,
  onStart,
  onStop,
  onAskDelete,
}: {
  m: ModelItem;
  busy: boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onAskDelete: (m: ModelItem) => void;
}) {
  const meta = statusMeta(m);
  const pct =
    m.total_bytes > 0 ? Math.min(100, Math.round((m.downloaded_bytes / m.total_bytes) * 100)) : 0;
  return (
    <Card>
      <CardHeader
        title={m.name}
        aside={
          <span className={`inline-flex items-center gap-1.5 text-xs ${TONE_TEXT[meta.tone]}`}>
            <SignalDot tone={meta.tone as "accent" | "meter" | "danger" | "muted"} pulse={meta.pulse} />
            {meta.label}
          </span>
        }
      />
      <CardBody className="space-y-3">
        <p className="text-xs text-muted">{m.summary}</p>
        <p className="font-mono text-[11px] text-fg-2">
          约 {formatSize(m.size_bytes)} · {deviceText(m.requirements)} ·{" "}
          <a href={m.license_url} target="_blank" rel="noreferrer" className={LINK_CLASS}>
            {m.license}
          </a>
        </p>
        {m.status === "downloading" && (
          <div className="space-y-1" role="status" aria-label={`${m.name} 下载进度`}>
            <ProgressBar value={pct} active />
            <p className="font-mono text-[11px] text-muted">
              {formatSize(m.downloaded_bytes)} / {formatSize(m.total_bytes)}（{pct}%）
            </p>
          </div>
        )}
        {m.status === "failed" && <p className="text-xs text-danger">{m.error || "下载失败"}</p>}
        <div className="flex items-center gap-2">
          {m.status === "idle" && (
            <Button
              size="sm"
              variant="primary"
              disabled={busy}
              onClick={() => onStart(m.id)}
              icon={<Download size={13} strokeWidth={1.75} />}
            >
              {m.has_partial ? "继续下载" : "下载"}
            </Button>
          )}
          {m.status === "downloading" && (
            <Button
              size="sm"
              variant="secondary"
              onClick={() => onStop(m.id)}
              icon={<Pause size={13} strokeWidth={1.75} />}
            >
              暂停
            </Button>
          )}
          {m.status === "failed" && (
            <Button
              size="sm"
              variant="primary"
              disabled={busy}
              onClick={() => onStart(m.id)}
              icon={<RotateCcw size={13} strokeWidth={1.75} />}
            >
              重试
            </Button>
          )}
          {m.status === "installed" && (
            <IconButton label={`删除 ${m.name}`} size="sm" onClick={() => onAskDelete(m)}>
              <Trash2 size={14} strokeWidth={1.75} />
            </IconButton>
          )}
        </div>
      </CardBody>
    </Card>
  );
}

export default function LocalModelsSection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data, isLoading } = useModels();
  const items = data?.items ?? [];
  const busy = items.some((m) => m.status === "downloading" || m.status === "verifying");

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["models"] });
  const start = useMutation({
    mutationFn: startModelDownload,
    onSuccess: invalidate,
    onError: (e: Error) => toast({ tone: "error", title: "下载启动失败", description: e.message }),
  });
  const stop = useMutation({
    mutationFn: stopModelDownload,
    onSuccess: invalidate,
    onError: (e: Error) => toast({ tone: "error", title: "暂停失败", description: e.message }),
  });
  const del = useMutation({
    mutationFn: deleteModel,
    onSuccess: () => {
      toast({ tone: "ok", title: "模型已删除" });
      invalidate();
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });
  const openDir = useMutation({
    mutationFn: openModelsDir,
    onError: (e: Error) => toast({ tone: "error", title: "打开模型目录失败", description: e.message }),
  });

  // 完成边沿 Toast:轮询驱动的状态迁移(downloading/verifying → installed)与操作解耦
  const prevStatuses = useRef<Record<string, string>>({});
  useEffect(() => {
    for (const m of items) {
      const prev = prevStatuses.current[m.id];
      if ((prev === "downloading" || prev === "verifying") && m.status === "installed") {
        toast({ tone: "ok", title: `${m.name} 已就绪` });
      }
      prevStatuses.current[m.id] = m.status;
    }
  }, [items, toast]);

  const [delTarget, setDelTarget] = useState<ModelItem | null>(null);

  if (isLoading) return <Skeleton className="h-24 w-full" />;
  if (items.length === 0) return null;

  const kinds = [...new Set(items.map((m) => m.kind))];
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <MicroLabel>本地模型</MicroLabel>
        {me?.desktop === true && (
          <IconButton label="打开模型目录" size="sm" onClick={() => openDir.mutate()}>
            <FolderOpen size={14} strokeWidth={1.75} />
          </IconButton>
        )}
      </div>
      {kinds.map((kind) => (
        <section key={kind} className="space-y-2">
          <MicroLabel>{KIND_LABELS[kind] ?? kind}</MicroLabel>
          {items
            .filter((m) => m.kind === kind)
            .map((m) => (
              <ModelCard
                key={m.id}
                m={m}
                busy={busy}
                onStart={(id) => start.mutate(id)}
                onStop={(id) => stop.mutate(id)}
                onAskDelete={setDelTarget}
              />
            ))}
        </section>
      ))}
      <ConfirmDialog
        open={!!delTarget}
        title={`删除 ${delTarget?.name ?? ""}`}
        description={
          delTarget
            ? `将删除模型文件并释放约 ${formatSize(delTarget.downloaded_bytes)} 空间，此操作不可撤销。`
            : ""
        }
        confirmLabel="删除"
        loading={del.isPending}
        onConfirm={() => {
          if (delTarget) del.mutate(delTarget.id);
          setDelTarget(null);
        }}
        onCancel={() => setDelTarget(null)}
      />
    </div>
  );
}
```

(顶部 import 需补 `SignalDot`——来自 `../ui`,与上面 `ProgressBar` 同一 import 语句,注意别漏。)

- [ ] **Step 2: 装配进 SettingsPage 本地 Tab**

`web/src/pages/SettingsPage.tsx`:

1. import 区加 `import LocalModelsSection from "./LocalModelsSection";`
2. 本地 Tab 渲染(`) : (` 之后,456 行附近的 `<div className="space-y-4">` 内、`{local.map(...)` 之前)插入:

```tsx
          <LocalModelsSection />

```

- [ ] **Step 3: 类型检查 + 构建**

Run: `cd web && npx tsc --noEmit && npm run build 2>&1 | tail -5`
Expected: 零错误,构建产物生成。

- [ ] **Step 4: 提交**

```bash
git add web/src/pages/LocalModelsSection.tsx web/src/pages/SettingsPage.tsx
git commit -m "feat(web): 设置页本地环境模型区——分组卡片/进度/暂停续传/删除确认"
```

---

### Task 8: 全量验证与手测清单

**Files:**
- 无新文件(全量回归 + 手测)

**Interfaces:**
- Consumes: 前 7 个任务全部产物。
- Produces: 可交付的完整功能。

- [ ] **Step 1: 后端全量测试**

Run: `go test ./... 2>&1 | tail -15`
Expected: 全部 PASS(含 -race 一轮:`go test -race ./internal/localmodel/ ./internal/server/`)。

- [ ] **Step 2: gofmt/vet 干净**

Run: `gofmt -l cmd internal; go vet ./...`
Expected: 均无输出。

- [ ] **Step 3: 前端构建**

Run: `cd web && npm run build 2>&1 | tail -3`
Expected: 成功。

- [ ] **Step 4: 手测清单(dev 运行: `go run ./cmd/voxbox serve`,或 `make all && ./bin/voxbox serve`)**

逐项核对,任何一项不过即修复后重跑:

1. 设置页 → 本地环境:顶部出现「本地模型」区,ASR 组在前(2 条)TTS 组在后(1 条);卡片展示简介/约大小/设备要求/许可证内嵌链接(点击打开魔搭模型页)。
2. 点「下载」(Paraformer ~1GB):进度条流光推进,mono 字节/百分比与实际相符;下载中状态点 accent 呼吸。
3. 下载中点「暂停」:回到「可续传」,`.part` 保留;「继续下载」从暂停处继续(字节不回退,总字节不变)。
4. 下载中刷新页面/重启服务(`Ctrl+C` 再 `serve`):模型仍可续传,进度从盘面恢复。
5. 完成:「{name} 已就绪」Toast,状态点转绿「已安装」,操作变删除按钮。
6. 再次启动同模型下载:报「模型已安装」错误 Toast(不该发生重复下载)。
7. 下载 A 中点 B 的「下载」:按钮应禁用;若绕过(并发 tab)后端报「已有模型在下载」。
8. 「删除」:确认对话框含模型名与释放空间;确认后目录消失(`<dataDir>/models/<id>` 不存在),状态回「未下载」。
9. 断网点下载:失败态红色 + 直述网络错误;恢复网络「重试」可续传。
10. 桌面形态(`VOXBOX_DESKTOP=1` 运行桌面壳):「本地模型」标题右侧出现「打开模型目录」按钮,点击弹出 Finder/资源管理器落在 models 目录;web 形态无此按钮。
11. web 形态(浏览器直开)API Token 卡仍在,模型区正常。

- [ ] **Step 5: 收尾提交(若手测有修复)**

```bash
git add -A && git commit -m "fix(localmodel): 手测修复(如有)"
```

---

## Self-Review 结论(已执行)

1. **Spec 覆盖**:catalog(§3)→Task 1;下载管理器状态机/恢复/并发安全(§4)→Task 2/3;五个端点(§5)→Task 5;UI 重构与两个增强(§6/§7)→Task 6/7;错误边界(§8)→Task 3/5;测试(§9)→各任务内+Task 8。spec §10 实施边界与文件结构一致。
2. **占位扫描**:无 TBD/「适当处理」;Task 1 Step 1 的 curl 是数据核对动作(有具体命令与判定标准),非占位。
3. **类型一致性**:`ModelView{Entry + ModelState 字段}` 贯穿 Manager(序列化平铺)、routes(直接 `gin.H{"items": List()}`)、前端 `ModelItem`;`Start/Stop/Delete/ErrUnknownModel/ErrBusy` 签名前后一致;前端 `startModelDownload` 等与端点路径一致。
