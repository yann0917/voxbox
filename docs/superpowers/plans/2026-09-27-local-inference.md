# 本地语音推理对接(后端)· 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让一期下载管理的资产真正可执行——引擎按需下载、归档解包、audiocpp 常驻 server 合成、sherpa 一次性子进程识别、`local.tts`/`local.asr` 工具进任务系统,`/api/local/ready` 暴露依赖链。

**Architecture:** catalog 升级 v2(引擎条目带五平台资产映射;模型条目带直链/归档字段),localmodel 下载管线扩展归档解包;新包 `internal/localruntime`(TTS 常驻 server 管理器 + ASR 执行器)与新包 `internal/provider/local`(两个 Tool);前端接入是第二份计划。

**Tech Stack:** Go 1.26 标准库(archive/tar + compress/bzip2、archive/zip)、既有 gin/react-query 体系、ffmpeg(参考音频转码,已有依赖)。

**Spec:** `docs/superpowers/specs/2026-09-27-local-inference-design.md`(论证来源,执行者两份都读)。

## Global Constraints

- 纯 Go 无 CGO(`CGO_ENABLED=0` 五平台交叉编译);只用标准库解包(archive/tar、compress/bzip2、archive/zip、gzip)。
- CI 有 gofmt 检查:每个任务收尾 `gofmt -l -w <pkg>` 无输出、`go vet ./...` 无告警。
- TTS = 常驻 `audiocpp_server`(禁止每请求冷启 CLI);ASR = 一次性 `sherpa-onnx-offline` 子进程。
- macOS 必须**显式** `--backend metal`(server 默认 cuda 不传必挂);其余平台 `cpu`。
- 引擎归档 sha256 必填并强制校验;解包必须防 zip-slip(拒绝绝对路径/`..`)且设 8GB 解包上限。
- 版本钉死:sherpa-onnx `v1.13.8`、audio.cpp `v0.8.2`。
- 参考音频:ffmpeg 转 `-ac 1 -ar 24000 -c:a pcm_s16le`,限 60 秒 / 20MB。
- 中文注释直述;错误文案可定位(未安装类错误指明去设置页下载什么)。
- 工具始终注册,不动态增删注册表;未安装时 Run 报直述错误(经 failErr → CodeTaskFailed=3)。
- 一期的并发约束不变:全局同时 1 个下载;终态写入与 active 清除同临界区。

---

### Task 1: catalog v2——引擎/直链/归档条目与校验

**Files:**
- Modify: `internal/localmodel/catalog.go`
- Modify: `internal/localmodel/catalog.json`(整体替换条目)
- Test: `internal/localmodel/catalog_test.go`(追加用例)

**Interfaces:**
- Consumes: 现有 `Entry`/`parseCatalog`/`catalog`(一期)。
- Produces: `Entry` 新字段 `Archive, ArchiveURL string`、`ArchiveSize int64`、`ArchiveSHA256 string`、`ExtractFiles, Binaries []string`、`Assets map[string]Asset`、`FileURLs map[string]string`、`RequiresEngine string`;新类型 `type Asset struct { URL string; SizeBytes int64; SHA256 string }`;`func (e Entry) ArchiveFor(goos, goarch string) (Asset, bool)`;校验规则见下。Kind 取值扩为 `asr | tts | engine`。

- [ ] **Step 1: 核对资产实数(写 catalog.json 前)**

```bash
# audio.cpp v0.8.2 全部资产名+体积(找齐 macos-arm64/macos-x64/windows-x64-cpu-portable 与 linux cpu 包的确切资产名;linux 若无 arm64 资产则该平台不进目录)
curl -s "https://api.github.com/repos/0xShug0/audio.cpp/releases/tags/v0.8.2" | python3 -c "import json,sys; [print(a['name'], a['size']) for a in json.load(sys.stdin)['assets']]"
# sherpa-onnx v1.13.8 五平台 shared 包体积
curl -s "https://api.github.com/repos/k2-fsa/sherpa-onnx/releases/tags/v1.13.8" | python3 -c "import json,sys; [print(a['name'], a['size']) for a in json.load(sys.stdin)['assets'] if 'shared' in a['name']]"
# sha256 实抓(每个归档资产一次; sherpa 5 个 + audiocpp 4-5 个)
curl -sL "<asset-url>" | shasum -a 256
# GGUF 确切文件名与体积(ModelScope,Recursive)
curl -s "https://modelscope.cn/api/v1/models/HereIsMark/audio.cpp-gguf/repo/files?Revision=master&Recursive=true" | python3 -c "import json,sys; [print(f['Size'], f['Path']) for f in json.load(sys.stdin)['Data']['Files'] if f['Path'].endswith('.gguf')]"
```

把实测的 sha256/体积/确切文件名填进 Step 3 的 catalog.json。**计划中标注的体积是调研近似值,以实测为准;GGUF 文件名以 ModelScope 列表为准(1.7B Base 已确认为 `qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf`)。**

- [ ] **Step 2: 写失败测试**

追加到 `internal/localmodel/catalog_test.go`(沿用 `validEntry()` 基础上改):

```go
func engineEntry() Entry {
	return Entry{
		ID: "audiocpp", Kind: "engine", Name: "audio.cpp 引擎", Summary: "TTS 运行时",
		SizeBytes: 27000000, License: "MIT", LicenseURL: "https://github.com/0xShug0/audio.cpp",
		Requirements:  Requirements{Device: "metal"},
		Archive:       "tar.gz",
		ArchiveSHA256: strings.Repeat("a", 64),
		Binaries:      []string{"audiocpp_server", "audiocpp_cli"},
		Assets: map[string]Asset{
			"darwin/arm64": {URL: "https://example.com/a.tar.gz", SizeBytes: 27000000},
		},
	}
}

func TestParseCatalogEngineEntries(t *testing.T) {
	// 引擎:合法
	if _, err := parseCatalog(mustJSON(t, []Entry{engineEntry()})); err != nil {
		t.Fatalf("合法引擎条目被拒: %v", err)
	}
	// 引擎:缺平台资产
	e := engineEntry()
	e.Assets = nil
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err == nil {
		t.Error("引擎缺 assets 应被拒")
	}
	// 引擎:sha256 缺失(引擎必填)
	e = engineEntry()
	e.ArchiveSHA256 = ""
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err == nil {
		t.Error("引擎缺 sha256 应被拒")
	}
	// 引擎:binaries 缺失
	e = engineEntry()
	e.Binaries = nil
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err == nil {
		t.Error("引擎缺 binaries 应被拒")
	}
	// 引擎:资产缺 sha256
	e = engineEntry()
	asset := e.Assets["darwin/arm64"]
	asset.SHA256 = ""
	e.Assets["darwin/arm64"] = asset
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err == nil {
		t.Error("平台资产缺 sha256 应被拒")
	}
}

func TestParseCatalogModelEntriesV2(t *testing.T) {
	// 模型:FileURLs 覆盖全部文件时 Repo 可空
	e := validEntry()
	e.Repo, e.LicenseURL = "", "https://example.com"
	e.FileURLs = map[string]string{"model.bin": "https://example.com/model.bin"}
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err != nil {
		t.Fatalf("FileURLs 直链条目被拒: %v", err)
	}
	// 模型:既无 Repo 又缺 FileURLs 覆盖
	e2 := validEntry()
	e2.Repo, e2.FileURLs = "", nil
	if _, err := parseCatalog(mustJSON(t, []Entry{e2})); err == nil {
		t.Error("无 Repo 且无 FileURLs 应被拒")
	}
	// 模型:归档条目必填 ArchiveURL + ExtractFiles
	e3 := validEntry()
	e3.Archive = "tar.bz2"
	if _, err := parseCatalog(mustJSON(t, []Entry{e3})); err == nil {
		t.Error("归档条目缺 ArchiveURL 应被拒")
	}
	// device 允许 metal(engine)
	e4 := engineEntry()
	if _, err := parseCatalog(mustJSON(t, []Entry{e4})); err != nil {
		t.Fatalf("metal 设备应允许: %v", err)
	}
	// requires_engine 必须指向存在的 engine 条目
	e5 := validEntry()
	e5.RequiresEngine = "nope"
	if _, err := parseCatalog(mustJSON(t, []Entry{e5})); err == nil {
		t.Error("requires_engine 指向不存在引擎应被拒")
	}
}

func TestArchiveForPlatform(t *testing.T) {
	e := engineEntry()
	a, ok := e.ArchiveFor("darwin", "arm64")
	if !ok || a.URL != "https://example.com/a.tar.gz" {
		t.Fatalf("平台资产选择不符: %+v ok=%v", a, ok)
	}
	if _, ok := e.ArchiveFor("windows", "amd64"); ok {
		t.Error("未声明平台应返回 false")
	}
}
```

- [ ] **Step 3: 实现 catalog.go 扩展 + 重写 catalog.json**

`Entry` 追加字段(带 json tag,风格与现有对齐):

```go
// Asset 引擎的平台归档资产。
type Asset struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
```

```go
	Archive       string            `json:"archive,omitempty"`       // tar.bz2 | tar.gz | zip;空=按 Files 逐文件
	ArchiveURL    string            `json:"archive_url,omitempty"`   // 引擎经 Assets 按平台选择;模型归档条目直接写这里
	ArchiveSize   int64             `json:"archive_size,omitempty"`
	ArchiveSHA256 string            `json:"archive_sha256,omitempty"` // 引擎必填
	ExtractFiles  []string          `json:"extract_files,omitempty"`  // 归档解包白名单
	Binaries      []string          `json:"binaries,omitempty"`       // engine:解包内必须存在的可执行文件
	Assets        map[string]Asset  `json:"assets,omitempty"`         // engine:键 "goos/goarch"
	FileURLs      map[string]string `json:"file_urls,omitempty"`      // file → 直链;Repo 非空时可省
	RequiresEngine string           `json:"requires_engine,omitempty"`
```

`ArchiveFor`:

```go
// ArchiveFor 按 GOOS/GOARCH 取引擎平台资产;未声明平台返回 false(该平台不展示此引擎)。
func (e Entry) ArchiveFor(goos, goarch string) (Asset, bool) {
	a, ok := e.Assets[goos+"/"+goarch]
	return a, ok
}
```

`parseCatalog` 校验改造要点(在现有循环内按 `e.Kind == "engine"` 分支):

- engine:强制 `Archive`∈{tar.bz2,tar.gz,zip}、`len(Assets)>0`、`ArchiveSHA256` 非空(64 位 hex)、`len(Binaries)>0`、每个 Asset 的 URL/SHA256 非空且 SizeBytes>0;`Files` 可为空(跳过一期"无文件"检查);`Repo` 可空。
- 非 engine:`Archive` 非空时强制 `ArchiveURL` 非空 + `len(ExtractFiles)>0`;`Repo == ""` 时强制每个 file 在 `FileURLs` 中有非空直链;`FileURLs` 中多出的键(不在 Files)拒绝。
- 公共:`device` 允许 `cpu|cuda|metal`;`RequiresEngine` 非空时必须能在本目录中找到对应 `kind=="engine"` 条目(两遍扫描:先收集 engine id 集)。

`catalog.json` 整体替换(6 条:2 引擎 + 4 模型;sha256/体积/确切 GGUF 名以 Step 1 实测填入;`LicenseURL` 引擎指向其 GitHub 仓库):

```json
[
  {
    "id": "sherpa-onnx", "kind": "engine", "name": "sherpa-onnx 引擎",
    "summary": "SenseVoice 本地语音识别运行时(ONNX,CPU)",
    "size_bytes": 20000000,
    "requirements": { "device": "cpu" },
    "license": "Apache-2.0", "license_url": "https://github.com/k2-fsa/sherpa-onnx",
    "archive": "tar.bz2",
    "archive_sha256": "<实测>",
    "binaries": ["sherpa-onnx-offline"],
    "assets": {
      "darwin/arm64":  { "url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-osx-arm64-shared.tar.bz2", "size_bytes": 20334894, "sha256": "<实测>" },
      "darwin/amd64":  { "url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-osx-x64-shared.tar.bz2", "size_bytes": 22858952, "sha256": "<实测>" },
      "windows/amd64": { "url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-win-x64-shared-MT-Release.tar.bz2", "size_bytes": 24851251, "sha256": "<实测>" },
      "linux/amd64":   { "url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-linux-x64-shared.tar.bz2", "size_bytes": 28207104, "sha256": "<实测>" },
      "linux/arm64":   { "url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-linux-aarch64-shared.tar.bz2", "size_bytes": 28108083, "sha256": "<实测>" }
    }
  },
  {
    "id": "audiocpp", "kind": "engine", "name": "audio.cpp 引擎",
    "summary": "Qwen3-TTS GGUF 本地合成运行时(macOS Metal / CPU)",
    "size_bytes": 27000000,
    "requirements": { "device": "metal" },
    "license": "MIT", "license_url": "https://github.com/0xShug0/audio.cpp",
    "archive": "tar.gz",
    "archive_sha256": "<实测>",
    "binaries": ["audiocpp_server", "audiocpp_cli"],
    "assets": {
      "darwin/arm64":  { "url": "https://github.com/0xShug0/audio.cpp/releases/download/v0.8.2/audio-v0.8.2-bin-macos-arm64-metal.tar.gz", "size_bytes": 28521279, "sha256": "<实测>" },
      "darwin/amd64":  { "url": "https://github.com/0xShug0/audio.cpp/releases/download/v0.8.2/audio-v0.8.2-bin-macos-x64-metal.tar.gz", "size_bytes": 30199488, "sha256": "<实测>" },
      "windows/amd64": { "url": "https://github.com/0xShug0/audio.cpp/releases/download/v0.8.2/audio-v0.8.2-bin-windows-x64-cpu-portable.zip", "size_bytes": 25165824, "sha256": "<实测>" },
      "linux/amd64":   { "url": "<Task 1 Step 1 实测的 ubuntu cpu 包>", "size_bytes": 0, "sha256": "<实测>" }
    }
  },
  {
    "id": "sensevoice-int8", "kind": "asr", "name": "SenseVoice 多语种识别(int8)",
    "summary": "中英日韩粤本地语音识别,自动标点与 ITN,CPU 秒级",
    "size_bytes": 163000000,
    "requirements": { "device": "cpu" },
    "license": "Apache-2.0", "license_url": "https://github.com/k2-fsa/sherpa-onnx/releases/tag/asr-models",
    "requires_engine": "sherpa-onnx",
    "archive": "tar.bz2",
    "archive_url": "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17.tar.bz2",
    "archive_size": 163047772,
    "extract_files": ["model.int8.onnx", "tokens.txt"]
  },
  {
    "id": "qwen3-tts-base-q8", "kind": "tts", "name": "Qwen3-TTS 1.7B 克隆(q8)",
    "summary": "参考音频 3 秒复刻音色的本地语音合成(GGUF 量化)",
    "size_bytes": 2570300000,
    "requirements": { "device": "metal" },
    "license": "Apache-2.0", "license_url": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf",
    "requires_engine": "audiocpp",
    "files": ["qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf"],
    "file_urls": { "qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf/resolve/master/Qwen3-TTS-12Hz-1.7B-Base-GGUF/qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf" }
  },
  {
    "id": "qwen3-tts-customvoice-q8", "kind": "tts", "name": "Qwen3-TTS 预置音色(q8)",
    "summary": "9 个预置音色 + 自然语言风格指令的本地语音合成(GGUF 量化)",
    "size_bytes": 2686500000,
    "requirements": { "device": "metal" },
    "license": "Apache-2.0", "license_url": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf",
    "requires_engine": "audiocpp",
    "files": ["<实测确切文件名>.gguf"],
    "file_urls": { "<实测确切文件名>.gguf": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf/resolve/master/Qwen3-TTS-12Hz-1.7B-CustomVoice-GGUF/<实测确切文件名>.gguf" }
  },
  {
    "id": "qwen3-tts-base-0.6b-q8", "kind": "tts", "name": "Qwen3-TTS 0.6B 克隆(q8)",
    "summary": "低配克隆变体,磁盘与内存占用更小(GGUF 量化)",
    "size_bytes": 1899000000,
    "requirements": { "device": "metal" },
    "license": "Apache-2.0", "license_url": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf",
    "requires_engine": "audiocpp",
    "files": ["<实测确切文件名>.gguf"],
    "file_urls": { "<实测确切文件名>.gguf": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf/resolve/master/Qwen3-TTS-12Hz-0.6B-Base-GGUF/<实测确切文件名>.gguf" }
  }
]
```

注意:`size_bytes`/`archive_size`/资产 `size_bytes` 全部以 Step 1 实测值覆盖计划里的近似数。`requirements.device=metal` 对 TTS 模型与 audiocpp 引擎成立(macOS metal/CPU 两可,展示口径取"最优后端")。

- [ ] **Step 4: 跑测试(新用例绿 + 一期既有用例红→修)**

Run: `go test ./internal/localmodel/ -run 'TestParse|TestArchive|TestEmbedded' -v`
Expected: 一期既有用例中依赖旧 schema 的(validEntry 现在仍合法——Repo 非空不需要 FileURLs,应仍绿;若 `TestEmbeddedCatalog` 因新 catalog 需要调整断言(如条目数),按新现实修断言并注释原因)。

- [ ] **Step 5: 格式化 + 提交**

Run: `gofmt -l -w internal/localmodel && go vet ./internal/localmodel/`

```bash
git add internal/localmodel/
git commit -m "feat(localmodel): catalog v2——引擎条目/平台资产/直链与归档字段"
```

---

### Task 2: 下载管线扩展——平台资产/直链覆盖/归档解包

**Files:**
- Create: `internal/localmodel/extract.go`
- Modify: `internal/localmodel/manager.go`
- Test: `internal/localmodel/manager_test.go`(追加)、`internal/localmodel/extract_test.go`

**Interfaces:**
- Consumes: Task 1 的 Entry v2、`ArchiveFor`、既有 probe/fetchOne/manifest。
- Produces: `newManager(modelsDir, enginesDir, baseURL string, entries []Entry)`(签名变更,enginesDir 新增);`NewManager(dataDir string) *Manager` 内部传 `filepath.Join(dataDir,"engines")`;`func (m *Manager) modelDir(id string)` 按 kind 分流(engine→enginesDir);`extract.go`: `func extractArchive(format, src, destDir string, whitelist []string) error`、`func findBinaries(dir string, names []string) ([]string, error)`、`func pickAssetURL(e Entry) (string, int64, string, error)`(engine 条目按 GOOS/GOARCH 返回 url/size/sha,模型归档条目返回 ArchiveURL/ArchiveSize/ArchiveSHA256)。

- [ ] **Step 1: 写失败测试(extract_test.go)**

```go
package localmodel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTar 构造内存 tar(可再包 gzip/bzip2),entries 键为路径值为内容。
func makeTar(t *testing.T, entries map[string][]byte, wrap func(*bytes.Buffer) []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return wrap(&raw)
}

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractTarGzWhitelist(t *testing.T) {
	payload := makeTar(t, map[string][]byte{
		"top/model.int8.onnx": []byte("onnx-data"),
		"top/tokens.txt":      []byte("tokens"),
		"top/junk.bin":        []byte("junk"),
	}, func(b *bytes.Buffer) []byte {
		var out bytes.Buffer
		gw := gzip.NewWriter(&out)
		gw.Write(b.Bytes())
		gw.Close()
		return out.Bytes()
	})
	dir := t.TempDir()
	src := writeTemp(t, dir, "a.tar.gz", payload)
	dest := t.TempDir()
	if err := extractArchive("tar.gz", src, dest, []string{"model.int8.onnx", "tokens.txt"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"model.int8.onnx": "onnx-data", "tokens.txt": "tokens"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s 解包不符: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "junk.bin")); !os.IsNotExist(err) {
		t.Error("白名单外文件不应被解出")
	}
}

func TestExtractTarBz2AndZip(t *testing.T) {
	// tar.bz2
	payload := makeTar(t, map[string][]byte{"bin/sherpa-onnx-offline": []byte("elf")}, func(b *bytes.Buffer) []byte {
		var out bytes.Buffer
		bw := bzip2.NewWriter(&out)
		bw.Write(b.Bytes())
		bw.Close()
		return out.Bytes()
	})
	dir := t.TempDir()
	dest := t.TempDir()
	if err := extractArchive("tar.bz2", writeTemp(t, dir, "a.tar.bz2", payload), dest, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "sherpa-onnx-offline")); err != nil {
		t.Fatal(err)
	}
	// zip
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("audiocpp_server")
	w.Write([]byte("macho"))
	zw.Close()
	dest2 := t.TempDir()
	if err := extractArchive("zip", writeTemp(t, dir, "a.zip", zbuf.Bytes()), dest2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest2, "audiocpp_server")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	payload := makeTar(t, map[string][]byte{"../evil.txt": []byte("x")}, func(b *bytes.Buffer) []byte { return b.Bytes() })
	dir := t.TempDir()
	err := extractArchive("tar.gz", writeTemp(t, dir, "a.tar.gz", payload), t.TempDir(), nil)
	// gzip.wrap 为恒等:tar.gz 实际是裸 tar——extractArchive 按 format 包装 reader,恒等包应传 "tar"?为消除歧义:本用例直接改用 tar.gz 正确包装,并把 ../ 条目断言为报错。
	if err == nil {
		t.Fatal("上级穿越必须被拒绝")
	}
	if !strings.Contains(err.Error(), "非法") {
		t.Fatalf("错误应指明路径非法: %v", err)
	}
}
```

(注:`TestExtractRejectsTraversal` 构造时按各 format 的真实包装走,上面骨架里 `../evil.txt` 用 tar.gz 正确 gzip 包装后断言报错——实现以「解包时对每个成员路径做 Clean 后必须留在 destDir 内,否则报 `非法归档条目路径: %s` 并整体失败」为准。)

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/localmodel/ -run TestExtract 2>&1 | head -3`
Expected: 编译错误(无 extractArchive)。

- [ ] **Step 3: 实现 extract.go**

```go
package localmodel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// extractUnlimited 解包字节数上限(防 zip 炸弹),与 yovoice 同值。
const extractLimit = 8 << 30

// extractArchive 解包归档到 destDir。whitelist 非空时只解出命中成员(扁平化到 destDir 根,
// 供模型归档取 onnx/tokens);为空时保留包内相对结构(引擎包自包含 rpath,不能挪动 lib 布局)。
// 安全:成员路径 Clean 后必须留在 destDir 内(拒绝绝对路径与 ..);累计解包字节超 extractLimit 报错。
func extractArchive(format, src, destDir string, whitelist []string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	keep := map[string]bool{}
	for _, w := range whitelist {
		keep[path.Base(w)] = true
	}
	switch format {
	case "zip":
		return extractZip(src, destDir, keep, whitelist)
	case "tar.gz", "tar.bz2", "tar":
		return extractTar(format, f, destDir, keep, whitelist)
	default:
		return fmt.Errorf("不支持的归档格式: %s", format)
	}
}

func openTarReader(format string, r io.Reader) (io.Reader, *tar.Reader, error) {
	switch format {
	case "tar.gz":
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, tar.NewReader(zr), nil
	case "tar.bz2":
		br := bzip2.NewReader(r)
		return br, tar.NewReader(br), nil
	default:
		return r, tar.NewReader(r), nil
	}
}

func extractTar(format string, f *os.File, destDir string, keep map[string]bool, whitelist []string) error {
	closer, tr, err := openTarReader(format, f)
	if err != nil {
		return err
	}
	defer func() {
		if c, ok := closer.(io.Closer); ok {
			c.Close()
		}
	}()
	destRoot := filepath.Clean(destDir) + string(os.PathSeparator)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("非法归档条目路径: %s", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if whitelist != nil && !keep[path.Base(name)] {
			continue
		}
		rel := name
		if whitelist != nil {
			rel = path.Base(name) // 白名单模式扁平化
		}
		dest := filepath.Join(destDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(dest)+string(os.PathSeparator), destRoot) {
			return fmt.Errorf("非法归档条目路径: %s", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		n, err := copyLimited(dest, tr, hdr.Size, &written)
		if err != nil {
			return err
		}
		_ = n
	}
}

func extractZip(src, destDir string, keep map[string]bool, whitelist []string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	destRoot := filepath.Clean(destDir) + string(os.PathSeparator)
	var written int64
	for _, zf := range zr.File {
		name := path.Clean(zf.Name)
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("非法归档条目路径: %s", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			continue
		}
		if whitelist != nil && !keep[path.Base(name)] {
			continue
		}
		rel := name
		if whitelist != nil {
			rel = path.Base(name)
		}
		dest := filepath.Join(destDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(dest)+string(os.PathSeparator), destRoot) {
			return fmt.Errorf("非法归档条目路径: %s", zf.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		if _, err := copyLimited(dest, rc, int64(zf.UncompressedSize64), &written); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// copyLimited 落一个成员并推进全局解包字节计数(超限即失败)。
func copyLimited(dest string, r io.Reader, size int64, written *int64) (int64, error) {
	if *written+size > extractLimit {
		return 0, fmt.Errorf("归档解包超过 %dGB 上限", extractLimit>>30)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, r)
	*written += n
	if err != nil {
		return n, err
	}
	if size > 0 && n != size {
		return n, fmt.Errorf("归档成员不完整:%s 已解 %d 预期 %d", dest, n, size)
	}
	return n, nil
}

// findBinaries 在解包目录中定位引擎可执行文件(白名单名称,含 .exe 变体);
// 命中后 chmod 0755 并返回绝对路径列表。缺任一即报错(引擎包不完整)。
func findBinaries(dir string, names []string) ([]string, error) {
	var found []string
	for _, name := range names {
		var hits []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			base := d.Name()
			if base == name || base == name+".exe" {
				hits = append(hits, p)
			}
			return nil
		})
		if len(hits) == 0 {
			return nil, fmt.Errorf("引擎包不完整:未找到 %s", name)
		}
		sort.Strings(hits)
		if err := os.Chmod(hits[0], 0o755); err != nil {
			return nil, err
		}
		found = append(found, hits[0])
	}
	return found, nil
}
```

- [ ] **Step 4: manager.go 管线分支**

1. `Manager` struct 加 `enginesDir string`;`NewManager(dataDir)` → `newManager(filepath.Join(dataDir,"models"), filepath.Join(dataDir,"engines"), DefaultBaseURL, catalog)`;`newManager` 签名同步(更新既有测试的 `newTestManager`)。
2. `modelDir(id)` 分流:

```go
func (m *Manager) modelDir(id string) string {
	if e, ok := m.byID[id]; ok && e.Kind == "engine" {
		return filepath.Join(m.enginesDir, id)
	}
	return filepath.Join(m.baseDir, id)
}
```

3. `pickAssetURL`:

```go
// pickAssetURL 归档条目的下载源:engine 按平台选资产,模型归档用条目直属字段。
func pickAssetURL(e Entry) (url string, size int64, sha string, err error) {
	if e.Kind == "engine" {
		a, ok := e.ArchiveFor(runtime.GOOS, runtime.GOARCH)
		if !ok {
			return "", 0, "", fmt.Errorf("当前平台 %s/%s 暂不提供该引擎", runtime.GOOS, runtime.GOARCH)
		}
		return a.URL, a.SizeBytes, a.SHA256, nil
	}
	return e.ArchiveURL, e.ArchiveSize, e.ArchiveSHA256, nil
}
```

4. `run(ctx, e)` 开头分流:

```go
	if e.Archive != "" {
		m.runArchive(ctx, e)
		return
	}
```

`runArchive`:probe(单请求:Range bytes=0-0 拿真实大小,复用现有 probe,文件名任意占位如 `.archive`)→ `fetchOne`(file 名 = URL base 名,sha256 用 `e.ArchiveSHA256` 若非空——fetchOne 现按 `e.SHA256[file]` 查,runArchive 构造一个 `probe := e; probe.SHA256 = map[string]string{archiveName: sha}` 的副本传入,sha 为空则不设)→ `extractArchive(e.Archive, part→final 归档路径, e.modelDir?注意:引擎解到 engines/<id>/,模型解到 models/<id>/)` → engine 条目 `findBinaries` → 写 manifest(Files = 解出文件清单相对路径 + size;engine 追加 manifest `Binary` 字段 = server 二进制路径相对 engines/<id> 的相对路径,取 Binaries 里第一个名字的命中结果——audiocpp 的 server 是 `audiocpp_server`)→ installed。

具体顺序(runArchive 内部):
```go
	// 归档条目:单文件下载到 models|engines/<id>/.archives/<basename>,解包到同级目录后删归档
	url, wantSize, wantSHA, err := pickAssetURL(e)
	archiveName := path.Base(url) — 需 import path;非法 URL 无 base 时报错
	// probe:复用 m.probe(ctx, e, archiveName) 不行——probe 拼 modelscope 模板。归档直链不走模板。
```

**probe/fetchOne 的直链适配**(必须做):现有 `fileURL(e, file)` 拼 modelscope 模板。给 runArchive 与 FileURLs 条目统一走「直链优先」:

```go
// fileURLFor 单文件下载地址:FileURLs 直链覆盖 → modelscope 模板。
func (m *Manager) fileURLFor(e Entry, file string) string {
	if u, ok := e.FileURLs[file]; ok {
		return u
	}
	return m.fileURL(e, file)
}
```

`probe` 与 `fetchOne` 内部把 `m.fileURL(e, file)` 换成 `m.fileURLFor(e, file)`(两处)。GGUF 单文件条目因此直接被现有 Files 逐文件管线覆盖(FileURLs 直链),**无需 runArchive**——只有 Archive != "" 的条目走 runArchive。

runArchive 主体:

```go
// runArchive 归档条目下载管线:单归档下载 → sha256(引擎强制)→ 解包 → (引擎)定位二进制 → manifest。
func (m *Manager) runArchive(ctx context.Context, e Entry) {
	dir := m.modelDir(e.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.setFailed(e.ID, "创建目录失败: "+err.Error())
		return
	}
	url, _, wantSHA, err := pickAssetURL(e)
	if err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	archiveName := path.Base(url)
	if archiveName == "" || archiveName == "/" || archiveName == "." {
		m.setFailed(e.ID, "归档 URL 无文件名: "+url)
		return
	}
	// 复用逐文件管线下载归档本体:构造单文件视图(直链 + 可选 sha256)
	single := e
	single.Files = []string{archiveName}
	single.FileURLs = map[string]string{archiveName: url}
	if wantSHA != "" {
		single.SHA256 = map[string]string{archiveName: wantSHA}
	}
	sizes := map[string]int64{}
	sz, canRange, err := m.probe(ctx, single, archiveName)
	if err != nil {
		m.finish(ctx, e.ID, err)
		return
	}
	_ = canRange
	sizes[archiveName] = sz
	m.mu.Lock()
	m.states[e.ID].TotalBytes = sz
	m.mu.Unlock()
	if err := m.fetchOne(ctx, single, archiveName, sz, true, 0); err != nil {
		m.finish(ctx, e.ID, err)
		return
	}
	m.mu.Lock()
	m.states[e.ID].Status = StatusVerifying
	m.mu.Unlock()
	archivePath := filepath.Join(dir, archiveName)
	if wantSHA != "" {
		sum, err := fileSHA256(archivePath)
		if err != nil {
			m.setFailed(e.ID, err.Error())
			return
		}
		if !strings.EqualFold(sum, wantSHA) {
			m.setFailed(e.ID, "校验失败:归档 sha256 不符(可能下载损坏)")
			return
		}
	}
	dest := filepath.Join(dir, "pkg")
	if err := os.RemoveAll(dest); err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	if err := extractArchive(e.Archive, archivePath, dest, e.ExtractFiles); err != nil {
		m.setFailed(e.ID, "解包失败: "+err.Error())
		return
	}
	if err := os.Remove(archivePath); err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	// 提升解出文件到目录根:白名单条目已扁平;引擎包整体在 pkg/ 下,manifest 记 pkg 相对结构
	mf := manifest{ID: e.ID, Repo: e.Repo, Revision: e.Revision, CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	var total int64
	if e.Kind == "engine" {
		bins, err := findBinaries(dest, e.Binaries)
		if err != nil {
			m.setFailed(e.ID, err.Error())
			return
		}
		rel, err := filepath.Rel(dir, bins[0])
		if err != nil {
			m.setFailed(e.ID, err.Error())
			return
		}
		mf.Binary = rel
	}
	// 记录解包产物(走一遍盘面统计,供 installed 字节数)
	_ = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
				mf.Files = append(mf.Files, manifestFile{Path: filepath.ToSlash(filepath.Rel(dir, p)), Size: fi.Size()})
			}
		}
		return nil
	})
	mfRaw, _ := json.MarshalIndent(mf, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mfRaw, 0o644); err != nil {
		m.setFailed(e.ID, "写入 manifest 失败: "+err.Error())
		return
	}
	m.mu.Lock()
	st := m.states[e.ID]
	st.Status, st.HasPartial, st.DownloadedBytes, st.TotalBytes, st.Error = StatusInstalled, false, total, total, ""
	m.mu.Unlock()
}
```

`manifest` struct 加 `Binary string \`json:"binary,omitempty"\``(引擎 server 二进制的 dir 相对路径)。

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/localmodel/ -race`
Expected: 全绿(含一期用例;`newTestManager` 签名更新后既有测试编译通过)。

- [ ] **Step 6: 格式化 + 提交**

```bash
gofmt -l -w internal/localmodel && go vet ./internal/localmodel/
git add internal/localmodel/
git commit -m "feat(localmodel): 归档解包与引擎安装管线——平台资产/直链覆盖/zip-slip 防护"
```

---

### Task 3: Manager 消费者访问器

**Files:**
- Modify: `internal/localmodel/manager.go`
- Test: `internal/localmodel/manager_test.go`(追加)

**Interfaces:**
- Consumes: Task 2 的引擎 manifest(Binary 字段)。
- Produces(后续任务全靠这批):`func (m *Manager) GetEntry(id string) (Entry, bool)`、`func (m *Manager) Installed(id string) bool`(installed 状态)、`func (m *Manager) EngineBinary(id string) (string, error)`(绝对路径;未安装/非引擎报错)、`func (m *Manager) InstalledModelFile(id string) (string, error)`(裸文件条目已安装时唯一文件的绝对路径;多文件/未安装报错)。

- [ ] **Step 1: 写失败测试**

```go
func TestGetEntryAndInstalled(t *testing.T) {
	m, _ := newTestManager(t, testEntries())
	if e, ok := m.GetEntry("asr-small"); !ok || e.ID != "asr-small" {
		t.Fatal("GetEntry 应返回目录条目")
	}
	if m.Installed("asr-small") {
		t.Fatal("未下载不应报告已安装")
	}
}

func TestEngineBinaryLifecycle(t *testing.T) {
	entries := []Entry{engineEntry()} // Task 1 的 fixture;Assets 带 darwin/arm64
	m, base := newTestManager(t, entries)
	if _, err := m.EngineBinary("audiocpp"); err == nil {
		t.Fatal("未安装应报错")
	}
	// 直接播种一个合法引擎 manifest + 假二进制(不走真下载)
	dir := filepath.Join(base, "audiocpp", "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "audiocpp_server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "audiocpp", Binary: filepath.Join("pkg", "audiocpp_server"), CompletedAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(base, "audiocpp", "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.Installed("audiocpp") {
		t.Fatal("播种 manifest 后应报告已安装")
	}
	got, err := m.EngineBinary("audiocpp")
	if err != nil || got != bin {
		t.Fatalf("EngineBinary 应返回 %s,实际 %s err=%v", bin, got, err)
	}
}

func TestInstalledModelFile(t *testing.T) {
	// 裸单文件条目:直链 fixture
	e := validEntry()
	e.ID, e.Kind = "gguf-model", "tts"
	e.Repo = ""
	e.FileURLs = map[string]string{"model.bin": "https://example.com/model.bin"}
	m, base := newTestManager(t, []Entry{e})
	if _, err := m.InstalledModelFile("gguf-model"); err == nil {
		t.Fatal("未安装应报错")
	}
	dir := filepath.Join(base, "gguf-model")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "gguf-model", Files: []manifestFile{{Path: "model.bin", Size: 4}}, CompletedAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := m.InstalledModelFile("gguf-model")
	if err != nil || got != filepath.Join(dir, "model.bin") {
		t.Fatalf("应返回唯一文件绝对路径,实际 %s err=%v", got, err)
	}
}
```

- [ ] **Step 2: 实现(追加到 manager.go)**

```go
// GetEntry 目录条目只读访问。
func (m *Manager) GetEntry(id string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	return e, ok
}

// Installed 该条目是否已安装(engine 与模型同语义:manifest 存在且状态 installed)。
func (m *Manager) Installed(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.states[id]
	return ok && st.Status == StatusInstalled
}

// EngineBinary 已安装引擎的 server 二进制绝对路径(manifest.Binary 记录,一期语义:Binaries[0])。
func (m *Manager) EngineBinary(id string) (string, error) {
	m.mu.Lock()
	e, ok := m.byID[id]
	st := m.states[id]
	m.mu.Unlock()
	if !ok || e.Kind != "engine" {
		return "", fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	if !ok || st == nil || st.Status != StatusInstalled {
		return "", fmt.Errorf("引擎未安装: %s,请到设置页下载", e.Name)
	}
	dir := m.modelDir(id)
	mf, err := readManifest(dir)
	if err != nil || mf.Binary == "" {
		return "", fmt.Errorf("引擎安装记录损坏: %s,请到设置页删除后重装", e.Name)
	}
	return filepath.Join(dir, filepath.FromSlash(mf.Binary)), nil
}

// InstalledModelFile 已安装「裸单文件」条目(如 GGUF)的唯一文件绝对路径。
func (m *Manager) InstalledModelFile(id string) (string, error) {
	m.mu.Lock()
	e, ok := m.byID[id]
	st := m.states[id]
	m.mu.Unlock()
	if !ok || e.Kind == "engine" {
		return "", fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	if st == nil || st.Status != StatusInstalled {
		return "", fmt.Errorf("模型未安装: %s,请到设置页下载", e.Name)
	}
	if len(e.Files) != 1 {
		return "", fmt.Errorf("条目 %s 不是单文件模型", id)
	}
	return filepath.Join(m.modelDir(id), filepath.FromSlash(e.Files[0])), nil
}
```

- [ ] **Step 3: 跑测试 + 格式化 + 提交**

Run: `go test ./internal/localmodel/ -race && gofmt -l -w internal/localmodel && go vet ./internal/localmodel/`

```bash
git add internal/localmodel/
git commit -m "feat(localmodel): 消费者访问器——引擎二进制/已安装单文件模型"
```

---

### Task 4: localruntime——audiocpp server 管理器(TTS)

**Files:**
- Create: `internal/localruntime/audiocpp.go`
- Test: `internal/localruntime/audiocpp_test.go`

**Interfaces:**
- Consumes: Task 3 的 `EngineBinary`/`InstalledModelFile`/`GetEntry`/`Installed`。
- Produces(Task 6 消费):`package localruntime`;`type TTSRuntime struct`;`func NewTTSRuntime(dataDir string, models *localmodel.Manager) *TTSRuntime`;`func (t *TTSRuntime) Synthesize(ctx context.Context, req SynthRequest, report func(p int, note string)) (string, error)`(返回产物 wav 绝对路径);`func (t *TTSRuntime) Close() error`;`type SynthRequest struct { ModelID, Text, RefWav, RefText, Speaker, Instruct, Language string }`;测试 seam:字段 `BaseURL string`(非空时跳过子进程直连该地址)与 `backendOverride string`。

- [ ] **Step 1: 写失败测试**

```go
package localruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAudiocpp 假 audiocpp_server:/health 可控就绪;/v1/tasks/run 回固定 base64 wav。
func fakeAudiocpp(t *testing.T, readyAfter *atomic.Bool, captured *atomic.Value) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if readyAfter != nil && !readyAfter.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/tasks/run", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured != nil {
			captured.Store(body)
		}
		wav := bytes.Repeat([]byte("RIFFxxxxWAVE"), 64)
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString(wav)})
	})
	return httptest.NewServer(mux)
}

func TestSynthesizeViaBaseURL(t *testing.T) {
	var captured atomic.Value
	srv := fakeAudiocpp(t, nil, &captured)
	defer srv.Close()

	dir := t.TempDir()
	rt := NewTTSRuntime(dir, nil) // models 传 nil:BaseURL seam 下不查安装态
	rt.BaseURL = srv.URL
	defer rt.Close()

	out, err := rt.Synthesize(context.Background(), SynthRequest{
		ModelID: "qwen3-tts-base-q8", Text: "你好", Language: "Chinese",
	}, func(p int, note string) {})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Fatalf("产物应为解码后的 wav: %v", err)
	}
	req := captured.Load().(map[string]any)
	if req["model"] != "qwen3-tts-base-q8" {
		t.Fatalf("RPC model 不符: %v", req)
	}
	// 产物必须落在 dataDir/tts/ 下且为 .wav
	if filepath.Dir(out) != filepath.Join(dir, "tts") {
		t.Fatalf("产物目录不符: %s", out)
	}
}

func TestSynthesizeRequestOptions(t *testing.T) {
	var captured atomic.Value
	srv := fakeAudiocpp(t, nil, &captured)
	defer srv.Close()
	rt := NewTTSRuntime(t.TempDir(), nil)
	rt.BaseURL = srv.URL
	defer rt.Close()
	ref := filepath.Join(t.TempDir(), "ref.wav")
	_ = os.WriteFile(ref, []byte("wav"), 0o644)
	_, err := rt.Synthesize(context.Background(), SynthRequest{
		Text: "x", RefWav: ref, RefText: "参考原文",
	}, func(p int, note string) {})
	if err != nil {
		t.Fatal(err)
	}
	req := captured.Load().(map[string]any)
	inner := req["request"].(map[string]any)
	if inner["voice_ref"] != ref || inner["options"].(map[string]any)["reference_text"] != "参考原文" {
		t.Fatalf("克隆参数不符: %v", inner)
	}
}

func TestSynthesizeWaitsForHealth(t *testing.T) {
	var ready atomic.Bool
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !ready.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/v1/tasks/run", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString([]byte("RIFF"))})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := NewTTSRuntime(t.TempDir(), nil)
	rt.BaseURL = srv.URL
	rt.healthInterval = 10 * time.Millisecond
	defer rt.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		ready.Store(true)
	}()
	if _, err := rt.Synthesize(context.Background(), SynthRequest{Text: "x"}, func(p int, note string) {}); err != nil {
		t.Fatalf("health 恢复后应成功: %v", err)
	}
	if hits.Load() < 2 {
		t.Fatal("应轮询 health 多次直到就绪")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/localruntime/ 2>&1 | head -3`
Expected: 编译错误(包不存在)。

- [ ] **Step 3: 实现 audiocpp.go**

```go
// Package localruntime 本地推理运行时:audiocpp 常驻 server(TTS)与 sherpa 一次性子进程(ASR)。
// 仅子进程/HTTP 调用,不引入 cgo。
package localruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
)

// SynthRequest 一次合成请求(参数与 audiocpp_server /v1/tasks/run 对齐)。
type SynthRequest struct {
	ModelID  string // 已安装的 qwen3 GGUF 条目 id
	Text     string
	RefWav   string // 克隆:参考 wav 绝对路径(已转码 24k mono pcm16);空=preset 模式
	RefText  string // 克隆:参考音频转写;空则走 x_vector_only_mode
	Speaker  string // preset:预置音色名
	Instruct string // 可选风格指令
	Language string
}

// TTSRuntime audiocpp_server 生命周期管理:懒启动、健康轮询、崩溃自愈、退出回收。
type TTSRuntime struct {
	dataDir string
	models  *localmodel.Manager

	mu       sync.Mutex
	cmd      *exec.Cmd
	baseURL  string
	procAttr procStarter

	// 测试 seam:BaseURL 非空时跳过子进程直连;backendOverride 强制后端。
	BaseURL         string
	backendOverride string
	healthInterval  time.Duration
}

type procStarter func(cmd *exec.Cmd) error

func NewTTSRuntime(dataDir string, models *localmodel.Manager) *TTSRuntime {
	return &TTSRuntime{
		dataDir:        dataDir,
		models:         models,
		healthInterval: 250 * time.Millisecond,
		procAttr:       func(cmd *exec.Cmd) error { return cmd.Start() },
	}
}

// Close 回收 server 子进程(服务关闭时调用;幂等)。
func (t *TTSRuntime) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		t.cmd = nil
		t.baseURL = ""
	}
	return nil
}

// ensureHealth 轮询 /health 至就绪(上限 120s);server 未起则先拉起。
func (t *TTSRuntime) ensureHealth(ctx context.Context) (string, error) {
	t.mu.Lock()
	base := t.BaseURL
	if base == "" && t.cmd == nil {
		url, err := t.startLocked()
		if err != nil {
			t.mu.Unlock()
			return "", err
		}
		base = url
	} else if base == "" {
		base = t.baseURL
	}
	t.mu.Unlock()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return base, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(t.healthInterval):
		}
	}
	return "", fmt.Errorf("本地合成引擎健康检查超时(120s)")
}

// startLocked 拉起 audiocpp_server(调用方持锁)。返回监听地址。
func (t *TTSRuntime) startLocked() (string, error) {
	bin, err := t.models.EngineBinary("audiocpp")
	if err != nil {
		return "", err
	}
	port, err := freePort()
	if err != nil {
		return "", err
	}
	backend := t.backendOverride
	if backend == "" {
		if runtime.GOOS == "darwin" {
			backend = "metal" // server 默认 cuda,macOS 必须显式 metal(调研实测)
		} else {
			backend = "cpu"
		}
	}
	// models[]:已安装的 qwen3 GGUF 条目逐个注册(family 固定 qwen3_tts)
	type serverModel struct {
		ID     string `json:"id"`
		Family string `json:"family"`
		Path   string `json:"path"`
		Task   string `json:"task"`
		Mode   string `json:"mode"`
	}
	var serverModels []serverModel
	for _, e := range t.models.List() {
		if e.Kind != "tts" || !t.models.Installed(e.ID) {
			continue
		}
		p, err := t.models.InstalledModelFile(e.ID)
		if err != nil {
			continue // 已安装但文件异常:跳过,Run 时会再校验
		}
		serverModels = append(serverModels, serverModel{ID: e.ID, Family: "qwen3_tts", Path: p, Task: "tts", Mode: "offline"})
	}
	if len(serverModels) == 0 {
		return "", fmt.Errorf("没有已安装的本地 TTS 模型:请到设置页下载 Qwen3-TTS")
	}
	cfg := map[string]any{
		"host": "127.0.0.1", "port": port, "backend": backend, "device": 0,
		"threads": 4, "lazy_load": true, "max_loaded_models": 1,
		"idle_unload_ms": 300000, "max_request_body_bytes": 1048576,
		"models": serverModels,
	}
	cfgPath := filepath.Join(t.dataDir, "engines", "audiocpp-server.json")
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "--config", cfgPath, "--no-ui")
	if err := t.procAttr(cmd); err != nil {
		return "", fmt.Errorf("拉起本地合成引擎失败: %w", err)
	}
	t.cmd = cmd
	t.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	return t.baseURL, nil
}

// Synthesize 合成一段语音:健康就绪 → RPC → base64 WAV 落盘(.part→rename)。
func (t *TTSRuntime) Synthesize(ctx context.Context, req SynthRequest, report func(p int, note string)) (string, error) {
	report(5, "检查本地合成引擎…", nil)
	base, err := t.ensureHealth(ctx)
	if err != nil {
		return "", err
	}
	if t.models != nil {
		if !t.models.Installed(req.ModelID) {
			return "", fmt.Errorf("本地模型未安装: %s,请到设置页下载", req.ModelID)
		}
	}
	inner := map[string]any{"text": req.Text}
	opts := map[string]any{}
	if req.RefWav != "" {
		inner["voice_ref"] = req.RefWav
		if req.RefText != "" {
			opts["reference_text"] = req.RefText
			opts["x_vector_only_mode"] = false
		} else {
			opts["x_vector_only_mode"] = true
		}
	}
	if req.Speaker != "" {
		opts["speaker"] = req.Speaker
	}
	if req.Instruct != "" {
		opts["instruct"] = req.Instruct
	}
	if len(opts) > 0 {
		inner["options"] = opts
	}
	if req.Language != "" {
		inner["language"] = req.Language
	}
	payload, _ := json.Marshal(map[string]any{"model": req.ModelID, "request": inner})

	report(15, "提交合成任务…", nil)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/tasks/run", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("本地合成请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("本地合成引擎响应异常: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Audio string `json:"audio"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Audio == "" {
		return "", fmt.Errorf("本地合成引擎响应无法解析")
	}
	report(80, "合成完成,写入产物…", nil)
	raw, err := base64.StdEncoding.DecodeString(out.Audio)
	if err != nil {
		return "", fmt.Errorf("音频数据解码失败: %w", err)
	}
	outDir := filepath.Join(t.dataDir, "tts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("local_%d.wav", time.Now().UnixNano())
	final := filepath.Join(outDir, name)
	part := final + ".part"
	if err := os.WriteFile(part, raw, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	report(100, "本地合成完成", nil)
	return final, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
```

- [ ] **Step 4: 跑测试 + 格式化 + 提交**

Run: `go test ./internal/localruntime/ -race && gofmt -l -w internal/localruntime && go vet ./internal/localruntime/`

```bash
git add internal/localruntime/
git commit -m "feat(localruntime): audiocpp 常驻 server 管理器——懒启动/健康轮询/RPC 合成"
```

---

### Task 5: localruntime——sherpa ASR 执行器

**Files:**
- Create: `internal/localruntime/sherpa.go`
- Test: `internal/localruntime/sherpa_test.go`

**Interfaces:**
- Consumes: Task 3 的 `EngineBinary("sherpa-onnx")`、`GetEntry("sensevoice-int8")`。
- Produces(Task 6 消费):`type SherpaResult struct { Text, Lang, Emotion string; Timestamps []float64 }`;`func Transcribe(ctx context.Context, binPath, modelDir, wav string, language string, itn bool) (SherpaResult, error)`;模型目录约定:`<modelDir>/model.int8.onnx`、`<modelDir>/tokens.txt`。

- [ ] **Step 1: 写失败测试**

```go
package localruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeSherpa 生成一个可执行 mock(打印一行固定 JSON 到 stdout),用于命令拼装与解析测试。
// Windows 无 shebang 语义,mock 用例在 windows 跳过(执行器本体由冒烟覆盖)。
func writeFakeSherpa(t *testing.T, dir, stdout string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-sherpa")
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestTranscribeParsesStdoutJSON(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, `{"lang": "<|zh|>", "emotion": "<|NEUTRAL|>", "event": "<|Speech|>", "text": "你好世界", "timestamps": [0.1, 0.2]}`)
	res, err := Transcribe(context.Background(), bin, dir, filepath.Join(dir, "in.wav"), "zh", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "你好世界" || res.Lang != "<|zh|>" || len(res.Timestamps) != 2 {
		t.Fatalf("解析结果不符: %+v", res)
	}
}

func TestTranscribeFailsOnMissingModelFiles(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, "{}")
	if _, err := Transcribe(context.Background(), bin, dir, "in.wav", "auto", true); err == nil {
		t.Fatal("模型文件缺失应报错(启动前校验)")
	}
}

func TestTranscribeRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, "not-json at all")
	// mock 缺模型文件也会先报错——补两个空模型文件再测解析失败
	_ = os.WriteFile(filepath.Join(dir, "model.int8.onnx"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte("x"), 0o644)
	if _, err := Transcribe(context.Background(), bin, dir, filepath.Join(dir, "in.wav"), "auto", true); err == nil {
		t.Fatal("非 JSON stdout 应报错")
	}
}
```

- [ ] **Step 2: 实现 sherpa.go**

```go
package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SherpaResult sherpa-onnx-offline stdout 的解析结果(字段为其实测 JSON 输出)。
type SherpaResult struct {
	Text       string    `json:"text"`
	Lang       string    `json:"lang"`
	Emotion    string    `json:"emotion"`
	Timestamps []float64 `json:"timestamps"`
}

// Transcribe 一次性子进程识别:启动前校验模型文件在位,stdout 解析一行 JSON,
// 失败取 stderr 末行(audiotool runner 同款报错风格)。ctx 取消即 kill 子进程。
func Transcribe(ctx context.Context, binPath, modelDir, wav string, language string, itn bool) (SherpaResult, error) {
	var res SherpaResult
	onnx := filepath.Join(modelDir, "model.int8.onnx")
	tokens := filepath.Join(modelDir, "tokens.txt")
	for _, p := range []string{onnx, tokens, wav} {
		if _, err := os.Stat(p); err != nil {
			return res, fmt.Errorf("输入缺失: %s", p)
		}
	}
	args := []string{
		"--sense-voice-model=" + onnx,
		"--tokens=" + tokens,
		"--sense-voice-use-itn=" + strconv.FormatBool(itn),
	}
	if language != "" && language != "auto" {
		args = append(args, "--sense-voice-language="+language)
	}
	args = append(args, wav)
	cmd := exec.CommandContext(ctx, binPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("任务已取消")
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return res, fmt.Errorf("本地识别失败: %s", msg)
	}
	line := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] // 实测 stdout 为一行 JSON;多行时取首行容错
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return res, fmt.Errorf("识别输出无法解析: %w", err)
	}
	return res, nil
}
```

- [ ] **Step 3: 跑测试 + 格式化 + 提交**

Run: `go test ./internal/localruntime/ -race && gofmt -l -w internal/localruntime && go vet ./internal/localruntime/`

```bash
git add internal/localruntime/
git commit -m "feat(localruntime): sherpa 一次性子进程识别——命令拼装/stdout JSON 解析"
```

---

### Task 6: provider/local——local.tts / local.asr 工具

**Files:**
- Create: `internal/provider/local/card.go`、`register.go`、`tts.go`、`asr.go`、`voices.go`
- Test: `internal/provider/local/local_test.go`

**Interfaces:**
- Consumes: Task 3 的 `Manager.Installed/GetEntry/EngineBinary/InstalledModelFile`;Task 4 的 `TTSRuntime.Synthesize`;Task 5 的 `Transcribe`。
- Produces(Task 7 消费):`func RegisterAll(reg *provider.Registry, dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) error`;`func AllTools(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) []provider.Tool`;`func ProviderCard() provider.ProviderInfo`(Name:"local", Title:"本地推理", Kind:KindLocal, Order:85);`func CustomVoices() []LocalVoice`(`type LocalVoice struct { ID, Name string }`,9 音色);工具 Meta:`{Provider:"local", Name:"tts"|"asr"}`;测试 seam:ttsTool 字段 `synthesizeFn func(...)`、asrTool 字段 `transcribeFn func(...)`(缺省绑 localruntime 实现)。

- [ ] **Step 1: 写失败测试**

```go
package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/provider"
)

func newTestPkg(t *testing.T) (string, *localmodel.Manager) {
	t.Helper()
	dir := t.TempDir()
	// 用未导出构造不可行——provider 包只拿 *localmodel.Manager;直接 NewManager 后靠 seam 注入不查盘。
	// 事实:工具的安装校验通过 models.Installed;测试用 manager 的真实目录播种即可跳过下载。
	m := localmodel.NewManager(filepath.Join(dir, "data"))
	return filepath.Join(dir, "data"), m
}

func TestTTSRequiresEngineAndModel(t *testing.T) {
	_, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil) // runtime nil:synthesizeFn seam 会替换
	if _, err := tts.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"model": "qwen3-tts-base-q8", "mode": "clone",
	}}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("引擎未安装应报错")
	}
}

func TestTTSCloneRequiresRefAudio(t *testing.T) {
	_, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil)
	// 播种 audiocpp 引擎已安装态:直接造 manifest
	// (EngineBinary 校验 manifest;播种 helper 见下 seedEngine)
	seedEngine(t, m, "audiocpp", "audiocpp_server")
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "qwen3-tts-base-q8", "mode": "clone"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("clone 模式缺参考音频应报错")
	}
}

func TestTTSModeModelMismatch(t *testing.T) {
	_, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil)
	seedEngine(t, m, "audiocpp", "audiocpp_server")
	// seedModel 安装一个 base 条目,然后 preset 模式选 speaker → 应报模式/模型不匹配
	seedModelFile(t, m, "qwen3-tts-base-q8", "x.gguf")
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "qwen3-tts-base-q8", "mode": "preset", "speaker": "Vivian"},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("base 模型配 preset 模式应报错")
	}
}

func TestASRRequiresInstallations(t *testing.T) {
	_, m := newTestPkg(t)
	asr := newASRTool(t.TempDir(), m)
	if _, err := asr.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"language": "auto", "itn": true},
		Files:  map[string]string{"audio": "in.wav"},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("sherpa 引擎未安装应报错")
	}
}

func TestCustomVoicesNine(t *testing.T) {
	if len(CustomVoices()) != 9 {
		t.Fatalf("预置音色应为 9 个,实际 %d", len(CustomVoices()))
	}
}

func TestProviderCardShape(t *testing.T) {
	c := ProviderCard()
	if c.Name != "local" || c.Kind != provider.KindLocal || c.Order != 85 {
		t.Fatalf("卡声明不符: %+v", c)
	}
}

// —— 播种 helper:绕过下载,直接构造 installed 态(Task 3 的访问器读 manifest)——

func seedEngine(t *testing.T, m *localmodel.Manager, id, binary string) {
	t.Helper()
	// 目录布局:<dataDir>/engines/<id>;NewManager(dataDir) 的 engines 目录为 <dataDir>/engines
}
```

(注:`seedEngine`/`seedModelFile` 的实现依赖 `localmodel.NewManager(dataDir)` 的目录布局:dataDir 参数即 `<dataDir>`,engines 目录为其下 `engines/`、模型目录为其下 `models/`。播种 = 手写 `engines/<id>/manifest.json`(含 `binary` 字段指向已存在的假二进制文件)+ 假二进制文件 chmod 0755;模型 = `models/<id>/manifest.json` + 单文件。实现时若发现布局不匹配,以 Task 3 访问器实际读取路径为准修正播种路径,并保持断言不变。)

- [ ] **Step 2: 实现 card.go / register.go / voices.go / tts.go / asr.go**

`card.go`:

```go
package local

import "github.com/yann0917/voxbox/internal/provider"

// ProviderCard 本地推理卡:已安装引擎+模型驱动的 TTS/ASR 工具,无凭证。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "local",
		Title:       "本地推理",
		Description: "本地引擎(audio.cpp / sherpa-onnx)驱动已下载模型的合成与识别,离线可用;引擎与模型在设置页「本地环境」下载。",
		Kind:        provider.KindLocal,
		Order:       85,
	}
}
```

`register.go`:

```go
// RegisterAll 本地推理工具注册:与 audiotool 同为无凭证本地能力,注册一次不参与热更新。
package local

import (
	"errors"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
)

// AllTools 返回工具实例(seam 参数供测试注入假实现)。
func AllTools(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) []provider.Tool {
	return []provider.Tool{
		newTTSTool(dataDir, models, tts),
		newASRTool(dataDir, models),
	}
}

// RegisterAll 启动路径注册。
func RegisterAll(reg *provider.Registry, dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) error {
	var errs []error
	for _, t := range AllTools(dataDir, models, tts) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

`voices.go`:

```go
package local

// LocalVoice 预置音色(CustomVoice GGUF 的 speaker 名单,来源 audio.cpp model_specs)。
type LocalVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CustomVoices 九个预置音色(audio.cpp 官方 speaker 集合)。
func CustomVoices() []LocalVoice {
	return []LocalVoice{
		{ID: "Vivian", Name: "Vivian · 女声"},
		{ID: "Serena", Name: "Serena · 女声"},
		{ID: "Uncle_Fu", Name: "Uncle Fu · 男声"},
		{ID: "Dylan", Name: "Dylan · 男声"},
		{ID: "Eric", Name: "Eric · 男声"},
		{ID: "Ryan", Name: "Ryan · 男声"},
		{ID: "Aiden", Name: "Aiden · 男声"},
		{ID: "Ono_Anna", Name: "Ono Anna · 女声(日语向)"},
		{ID: "Sohee", Name: "Sohee · 女声(韩语向)"},
	}
}
```

`tts.go`(核心:校验链 + seam + 转码限制 + 产物):

```go
package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
)

const (
	refMaxSeconds = 60
	refMaxBytes   = 20 << 20
)

type ttsTool struct {
	dataDir string
	models  *localmodel.Manager
	tts     *localruntime.TTSRuntime

	// 测试 seam:缺省绑 TTSRuntime.Synthesize;转码 ffmpeg 可替换探测。
	synthesizeFn func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error)
	lookPath     func(string) (string, error)
}

func newTTSTool(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) *ttsTool {
	t := &ttsTool{dataDir: dataDir, models: models, tts: tts, lookPath: exec.LookPath}
	t.synthesizeFn = tts.Synthesize
	return t
}

func (t *ttsTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "tts", Title: "本地语音合成",
		Description: "audio.cpp 引擎驱动已安装的 Qwen3-TTS:参考音频克隆或预置音色,离线合成。", Group: "合成"}
}

func (t *ttsTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "model", Label: "本地模型", Type: provider.ParamEnum, Required: true, Group: "本地推理",
			Placeholder: "设置页已安装的 Qwen3-TTS 条目"},
		{Key: "mode", Label: "音色模式", Type: provider.ParamEnum, Required: true, Default: "clone",
			Options: []provider.ParamOption{{Value: "clone", Label: "参考音频克隆"}, {Value: "preset", Label: "预置音色"}}, Group: "本地推理"},
		{Key: "ref_text", Label: "参考音频转写", Type: provider.ParamText, Group: "本地推理",
			Placeholder: "参考音频实际说的内容(留空走纯音色克隆)"},
		{Key: "speaker", Label: "预置音色", Type: provider.ParamEnum, Group: "本地推理",
			Options: speakerOptions()},
		{Key: "instruct", Label: "风格指令", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "如:Very happy and energetic"},
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "Chinese",
			Options: []provider.ParamOption{{Value: "Chinese", Label: "中文"}, {Value: "English", Label: "英文"},
				{Value: "Japanese", Label: "日语"}, {Value: "Korean", Label: "韩语"}}, Group: "本地推理"},
	}
}

func speakerOptions() []provider.ParamOption {
	var opts []provider.ParamOption
	for _, v := range CustomVoices() {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.Name})
	}
	return opts
}

func (t *ttsTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	modelID, _ := in.Params["model"].(string)
	mode, _ := in.Params["mode"].(string)
	if modelID == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: model")
	}
	if mode != "clone" && mode != "preset" {
		return provider.TaskOutput{}, fmt.Errorf("参数错误: mode 仅支持 clone|preset")
	}
	// 引擎与模型安装校验(错误文案直述去哪装)
	e, ok := t.models.GetEntry(modelID)
	if !ok {
		return provider.TaskOutput{}, fmt.Errorf("未知本地模型: %s", modelID)
	}
	if !t.models.Installed(e.RequiresEngine) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 %s 引擎", e.RequiresEngine)
	}
	if !t.models.Installed(modelID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 %s", e.Name)
	}
	// 模式与模型变体匹配:文件名含 customvoice 才支持 preset;含 base 才支持 clone
	switch {
	case mode == "preset" && !strings.Contains(e.ID, "customvoice"):
		return provider.TaskOutput{}, fmt.Errorf("预置音色需要 CustomVoice 模型:当前 %s 为克隆模型,请切换音色模式或下载 CustomVoice 条目", e.Name)
	case mode == "clone" && strings.Contains(e.ID, "customvoice"):
		return provider.TaskOutput{}, fmt.Errorf("参考音频克隆需要 Base 模型:当前 %s 为预置音色模型,请切换音色模式或下载 Base 条目", e.Name)
	}

	req := localruntime.SynthRequest{ModelID: modelID, Language: paramString(in.Params, "language", "Chinese")}
	if text := paramString(in.Params, "text", ""); text == "" {
		// 文本不在 params:与云端 TTS 一致由工具页 params.text 传入
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	} else {
		req.Text = text
	}
	switch mode {
	case "clone":
		ref := in.Files["audio"]
		if ref == "" {
			return provider.TaskOutput{}, fmt.Errorf("克隆模式需要参考音频:请上传或录制 3-60 秒清晰人声")
		}
		converted, err := t.convertRef(ctx, ref)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		defer os.Remove(converted)
		req.RefWav = converted
		req.RefText = paramString(in.Params, "ref_text", "")
	case "preset":
		sp := paramString(in.Params, "speaker", "")
		if sp == "" {
			return provider.TaskOutput{}, fmt.Errorf("预置模式需要选择音色: speaker")
		}
		req.Speaker = sp
		req.Instruct = paramString(in.Params, "instruct", "")
	}

	report(5, "准备本地合成…", nil)
	out, err := t.synthesizeFn(ctx, req, func(p int, note string) { report(p, note, nil) })
	if err != nil {
		return provider.TaskOutput{}, err
	}
	rel, err := filepath.Rel(t.dataDir, out)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	fi, err := os.Stat(out)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "本地合成完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: rel, Format: "wav", Size: fi.Size(),
			Meta: map[string]any{"engine": "audiocpp", "model": modelID, "mode": mode},
		}},
		Summary: map[string]any{"engine": "audiocpp", "model": e.Name, "mode": mode},
	}, nil
}

// convertRef 参考音频统一转 24kHz 单声道 pcm16(ffmpeg;限 60 秒/20MB)。
func (t *ttsTool) convertRef(ctx context.Context, src string) (string, error) {
	if _, err := t.lookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("参考音频转码需要 ffmpeg:请安装后重试(剪辑工具同款依赖)")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("参考音频不存在: %s", src)
	}
	if fi.Size() > refMaxBytes {
		return "", fmt.Errorf("参考音频超过 %dMB 上限", refMaxBytes>>20)
	}
	out := filepath.Join(t.dataDir, "tts", fmt.Sprintf("ref_%d.wav", time.Now().UnixNano()))
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-y", "-v", "error",
		"-i", src, "-t", fmt.Sprintf("%d", refMaxSeconds),
		"-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", out)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("任务已取消")
		}
		return "", fmt.Errorf("参考音频转码失败:请确认文件为有效音频")
	}
	return out, nil
}

// paramString 宽容取参(string/数值均收,audiotool 同款)。
func paramString(params map[string]any, key, def string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch s := v.(type) {
	case string:
		if s == "" {
			return def
		}
		return s
	default:
		return def
	}
}
```

`asr.go`:

```go
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/provider"
)

const (
	asrEngineID = "sherpa-onnx"
	asrModelID  = "sensevoice-int8"
)

type asrTool struct {
	dataDir string
	models  *localmodel.Manager
	// 测试 seam
	transcribeFn func(ctx context.Context, binPath, modelDir, wav, language string, itn bool) (localruntime.SherpaResult, error)
	langOptions  []provider.ParamOption
}

func newASRTool(dataDir string, models *localmodel.Manager) *asrTool {
	a := &asrTool{dataDir: dataDir, models: models}
	a.transcribeFn = localruntime.Transcribe
	return a
}

func (a *asrTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "asr", Title: "本地语音识别",
		Description: "sherpa-onnx 引擎驱动 SenseVoice:中英日韩粤本地识别,自动标点与 ITN。", Group: "识别"}
}

func (a *asrTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "auto", Group: "本地推理",
			Options: []provider.ParamOption{
				{Value: "auto", Label: "自动"}, {Value: "zh", Label: "中文"}, {Value: "en", Label: "英文"},
				{Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"}, {Value: "yue", Label: "粤语"},
			}},
		{Key: "itn", Label: "文本规整(ITN)", Type: provider.ParamBool, Default: true, Group: "本地推理"},
	}
}

func (a *asrTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	wav := in.Files["audio"]
	if wav == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入:请上传音频文件")
	}
	if !a.models.Installed(asrEngineID) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 sherpa-onnx 引擎")
	}
	if !a.models.Installed(asrModelID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 SenseVoice")
	}
	bin, err := a.models.EngineBinary(asrEngineID)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	modelDir := filepath.Join(a.dataDir, "models", asrModelID)
	language := paramString(in.Params, "language", "auto")
	itn := true
	if v, ok := in.Params["itn"].(bool); ok {
		itn = v
	}
	report(10, "启动本地识别…", nil)
	res, err := a.transcribeFn(ctx, bin, modelDir, wav, language, itn)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(90, "写入产物…", nil)
	outDir := filepath.Join("asr", newReqID())
	if err := os.MkdirAll(filepath.Join(a.dataDir, outDir), 0o755); err != nil {
		return provider.TaskOutput{}, err
	}
	rel := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(wav), filepath.Ext(wav))+"_local.txt")
	if err := os.WriteFile(filepath.Join(a.dataDir, rel), []byte(res.Text), 0o644); err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "本地识别完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: rel, Format: "txt", Size: int64(len(res.Text)),
			Meta: map[string]any{"engine": asrEngineID, "model": asrModelID, "lang": res.Lang},
		}},
		Summary: map[string]any{"engine": asrEngineID, "text": res.Text},
	}, nil
}

func newReqID() string {
	// uuid 工具与 audiotool 一致(直接引 uuid 包)
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}
```

(`asr.go` 顶部 import 需补 `"strings"`、`"github.com/google/uuid"`——uuid 已在 go.mod。`tts.go` 的 paramString 与 asr 共用,放 tts.go 或独立 params.go 均可,以文件单一职责为准。)

- [ ] **Step 3: 跑测试 + 格式化 + 提交**

Run: `go test ./internal/provider/local/ -race && gofmt -l -w internal/provider/local && go vet ./internal/provider/local/`

```bash
git add internal/provider/local/
git commit -m "feat(provider/local): 本地推理工具——tts 克隆/预置与 asr 识别"
```

---

### Task 7: Service 装配 + HTTP 面 + 全量验证

**Files:**
- Modify: `internal/service/service.go`(构造 runtime + 注册 local provider)、`internal/service/cards.go`(加 local 卡)
- Modify: `internal/server/routes.go`(ready 端点 + voices local 分支)
- Create: `internal/server/local_ready_test.go`
- Modify: `internal/provider/registry.go` 无需动(RegisterAll 已有)

**Interfaces:**
- Consumes: Task 2-6 全部。
- Produces: `GET /api/local/ready?tool=tts|asr` → `{"ready":bool,"missing":[{"type":"engine"|"model","id","name"}]}`;`/api/voices?provider=local` → 9 音色;Service 构造完成 local provider 注册。

- [ ] **Step 1: 写失败测试**

`internal/server/local_ready_test.go`:

```go
package server

import (
	"net/http"
	"testing"
)

func TestLocalReady(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 全新环境:引擎未装 → tts 与 asr 都不 ready,missing 列出引擎与模型
	resp, err := ac.Get(ts.URL + "/api/local/ready?tool=tts")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d: %s", e.Code, e.Message)
	}
	data := e.Data.(map[string]any)
	if data["ready"] != false {
		t.Fatal("全新环境 tts 不应 ready")
	}
	missing := data["missing"].([]any)
	if len(missing) < 2 {
		t.Fatalf("应同时缺引擎与模型: %#v", missing)
	}
	// 非法 tool
	req, _ := http.NewRequest("GET", ts.URL+"/api/local/ready?tool=nope", nil)
	resp2, _ := ac.Do(req)
	var e2 envelope
	_ = decodeBody(resp2, &e2)
	resp2.Body.Close()
	if e2.Code != CodeBadRequest {
		t.Fatalf("非法 tool 应 BadRequest,实际 %d", e2.Code)
	}
}

func TestVoicesLocal(t *testing.T) {
	ts, _, ac := newTestServer(t)
	resp, err := ac.Get(ts.URL + "/api/voices?provider=local")
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = decodeBody(resp, &e)
	if e.Code != CodeOK {
		t.Fatalf("业务码 %d", e.Code)
	}
	voices := e.Data.(map[string]any)["voices"].([]any)
	if len(voices) != 9 {
		t.Fatalf("本地预置音色应 9 个: %d", len(voices))
	}
}
```

- [ ] **Step 2: 实现**

`internal/service/service.go`:

1. `newWithRoot` 中,`s := &Service{db: db, reg: reg, models: localmodel.NewManager(dataDir)}` 之后:

```go
	ttsRuntime := localruntime.NewTTSRuntime(dataDir, s.models)
	if err := local.RegisterAll(reg, dataDir, s.models, ttsRuntime); err != nil {
		return nil, err
	}
```

`s := &Service{...}` 需要改为先建 models 再建 s(models 是字段):

```go
	models := localmodel.NewManager(dataDir)
	s := &Service{db: db, reg: reg, models: models}
	ttsRuntime := localruntime.NewTTSRuntime(dataDir, models)
	if err := local.RegisterAll(reg, dataDir, models, ttsRuntime); err != nil {
		return nil, err
	}
```

2. `Service` struct 加字段 `ttsRuntime *localruntime.TTSRuntime`;`s.ttsRuntime = ttsRuntime` 赋值;`Close()` 回收:

```go
func (s *Service) Close() error {
	if s.ttsRuntime != nil {
		return s.ttsRuntime.Close()
	}
	return nil
}
```

3. import 追加 `internal/localruntime`、`internal/provider/local`。

`internal/service/cards.go`:`providerCards()` 追加 `local.ProviderCard(),`(import `internal/provider/local`)。

`internal/server/routes.go`:

1. `Handler()` 的 api 组内追加:

```go
		api.GET("/local/ready", s.localReady)
```

2. `listVoices` 的 switch 追加:

```go
	case "local":
		ok(c, gin.H{"voices": local.CustomVoices()})
		return
```

(import `internal/provider/local`。)

3. 新 handler(放 `internal/server/models.go` 尾部或新文件 `internal/server/local.go`——选新文件):

```go
package server

import (
	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/localmodel"
)

// 本地推理就绪查询:一次判定引擎+模型依赖链,前端「本地」页签引导卡的数据源。

type missingItem struct {
	Type string `json:"type"` // engine | model
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) localReady(c *gin.Context) {
	var engineID, modelID string
	switch c.Query("tool") {
	case "tts":
		engineID = "audiocpp"
	case "asr":
		engineID = "sherpa-onnx"
		modelID = "sensevoice-int8"
	default:
		fail(c, CodeBadRequest, "参数错误:tool 仅支持 tts|asr")
		return
	}
	m := s.svc.LocalModels()
	var missing []missingItem
	ensure := func(kind, id string) {
		e, ok := m.GetEntry(id)
		if !ok {
			return // 目录无此条目(理论上不发生):不进 missing
		}
		if !m.Installed(id) {
			missing = append(missing, missingItem{Type: kind, ID: id, Name: e.Name})
		}
	}
	ensure("engine", engineID)
	if modelID != "" {
		ensure("model", modelID)
	}
	if c.Query("tool") == "tts" {
		// tts:任一已安装的 qwen3 模型即可;一个都没装才把 TTS 模型们列进 missing
		anyInstalled := false
		for _, v := range m.List() {
			if v.Kind == "tts" && m.Installed(v.ID) {
				anyInstalled = true
				break
			}
		}
		if !anyInstalled {
			for _, v := range m.List() {
				if v.Kind == "tts" {
					missing = append(missing, missingItem{Type: "model", ID: v.ID, Name: v.Name})
				}
			}
		}
	}
	ok(c, gin.H{"ready": len(missing) == 0, "missing": missing})
}
```

(`localmodel` import 若未用到则去掉;`ensure` 闭包中 `kind` 参数用 `Type` 字段值。)

- [ ] **Step 3: 跑测试 + 全量验证**

Run: `go test ./... && go test -race ./internal/localmodel/ ./internal/localruntime/ ./internal/provider/local/ ./internal/server/`
Expected: 全绿(`newTestServer` 构造会连带装配 local provider——注册名不冲突)。

- [ ] **Step 4: gofmt/vet + 五平台交叉编译**

```bash
gofmt -l cmd internal; go vet ./...
for p in darwin/arm64 darwin/amd64 windows/amd64 linux/amd64 linux/arm64; do os=${p%/*}; arch=${p#*/}; GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build ./... || exit 1; done
```

- [ ] **Step 5: 提交**

```bash
git add internal/service/ internal/server/ internal/provider/local/
git commit -m "feat(service,server): 本地推理装配——local provider 注册与 /api/local/ready"
```

---

### Task 8: 真机冒烟(macOS metal 端到端)

**Files:**
- 无新代码(运行验证;发现问题走修复)

- [ ] **Step 1: 自动化门禁**

```bash
go test ./... && gofmt -l cmd internal && go vet ./... && (cd web && npm run build)
```

- [ ] **Step 2: 真机端到端冒烟(用 API,不经 UI;一次性脚本式执行)**

用 `VOXBOX_HOME=/tmp/voxbox-lt-home`(确认 serve 命令的 home 覆盖机制,一期冒烟用过同款)启动真实服务,依次:

1. `GET /api/models` → 2 引擎 + 4 模型,全部 idle。
2. `POST /api/models/audiocpp/download` → 轮询至 installed(约 27MB)。
3. `POST /api/models/qwen3-tts-base-0.6b-q8/download`(1.9GB,最快模型;若磁盘/时长不允许,改 sensevoice-int8 155MB + asr 链路优先)→ installed。
4. `GET /api/local/ready?tool=asr` → false(missing: sherpa-onnx 引擎);下载 `sherpa-onnx` 引擎 + `sensevoice-int8` → ready=true。
5. ASR 冒烟:找一段本地 wav(可用 `/tmp` 里一期冒烟产物或 SenseVoice 包内 test_wavs——解包目录 `<dataDir>/models/sensevoice-int8/` 没有 test_wavs(白名单只留 onnx/tokens),另用 ffmpeg 生成:`ffmpeg -f lavfi -i "sine=frequency=440:duration=2" /tmp/test.wav` 静音不含语音,改为用系统 `say -o /tmp/test.wav "你好世界"`(macOS)生成语音)→ `POST /api/uploads` → `POST /api/tasks {provider:"local", tool:"asr", params:{language:"zh", itn:true}, file_ids:[id]}` → 轮询任务至 succeeded → `GET /api/tasks/:id` 断言产物 txt 非空。
6. TTS 冒烟(若下了 GGUF):`POST /api/tasks {provider:"local", tool:"tts", params:{model:"qwen3-tts-base-0.6b-q8", mode:"preset"→不允许(base 模型),改 clone + file_ids 参考音频}}`——**按实际下载的模型条目组合参数**:base 模型 + clone 模式 + 参考音频(用 say 生成 3 秒人声);断言 audio 产物存在且可播放大小合理。
7. 清理:`DELETE` 已装条目 + 杀服务 + 删 `/tmp/voxbox-lt-home`。

任何一步失败:诊断、修复(经正常提交)、重跑;完成后写冒烟记录到报告文件。

- [ ] **Step 3: 收尾提交(若有修复)**

```bash
git add -A && git commit -m "fix(localruntime): 真机冒烟修复(如有)"
```

---

## Self-Review 结论(已执行)

1. **Spec 覆盖**:§3 目录 v2+解包(Task 1/2)、§3.2 消费者访问器(§4/§5 的前置,Task 3)、§4 TTS 管理器(Task 4)、§5 ASR 执行器(Task 5)、§6 provider(Task 6)、§7 HTTP 面(Task 7)、§9 测试与真机冒烟(Task 8);§8 前端属第二份计划(范围检查时已声明拆分)。
2. **占位扫描**:catalog.json 的 sha256/确切 GGUF 文件名/linux 资产名标为「Task 1 Step 1 实测」并给了具体 curl 命令与判定标准——与一期同模式,非 TBD。`<实测确切文件名>` 占位符仅存在于 Step 3 的 JSON 样例中且被 Step 1 的明确指令覆盖。
3. **类型一致性**:`newManager(modelsDir, enginesDir, ...)` 与 `newTestManager` 更新说明一致;`SynthRequest/Transcribe/Synthesize` 签名在 Task 4/5 定义、Task 6 消费一致;`GetEntry/Installed/EngineBinary/InstalledModelFile` 在 Task 3 定义、Task 4/6/7 消费一致;`decodeBody` 复用一期 models_test.go 的 helper(server 包内已存在)。
