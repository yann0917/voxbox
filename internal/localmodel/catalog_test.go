package localmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func validEntry() Entry {
	return Entry{
		ID: "m1", Repo: "org/m1", Name: "M1", Kind: "asr", Summary: "测试模型",
		SizeBytes: 100, Files: []string{"model.bin"},
		Requirements: Requirements{Device: "cpu"},
		License:      "Apache-2.0", LicenseURL: "https://modelscope.cn/models/org/m1",
		RequiresEngine: "audiocpp", // 二期裁定:asr/tts 条目必须挂已声明的引擎
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

// ttsEntry family 校验用例的 tts 基线条目(validEntry 的 tts 变体):
// family 规则只对 kind=tts 生效,基线自带合法族值 qwen3_tts。
func ttsEntry() Entry {
	e := validEntry()
	e.Kind = "tts"
	e.Family = "qwen3_tts"
	return e
}

func TestParseCatalogAcceptsValid(t *testing.T) {
	entries, err := parseCatalog(withEngine(t, validEntry()))
	if err != nil {
		t.Fatalf("合法条目被拒绝: %v", err)
	}
	if len(entries) != 2 || entries[1].ID != "m1" {
		t.Fatalf("解析结果不符: %+v", entries)
	}
}

func TestParseCatalogRejects(t *testing.T) {
	cases := map[string][]Entry{
		"id重复":        {validEntry(), func() Entry { e := validEntry(); e.Repo = "org/m2"; return e }()},
		"缺字段":         {func() Entry { e := validEntry(); e.Name = ""; return e }()},
		"非法设备":        {func() Entry { e := validEntry(); e.Requirements.Device = "tpu"; return e }()},
		"无文件":         {func() Entry { e := validEntry(); e.Files = nil; return e }()},
		"大小非法":        {func() Entry { e := validEntry(); e.SizeBytes = 0; return e }()},
		"上级穿越":        {func() Entry { e := validEntry(); e.Files = []string{"../model.bin"}; return e }()},
		"绝对路径":        {func() Entry { e := validEntry(); e.Files = []string{"/etc/passwd"}; return e }()},
		"反斜杠":         {func() Entry { e := validEntry(); e.Files = []string{`dir\model.bin`}; return e }()},
		"未规范化":        {func() Entry { e := validEntry(); e.Files = []string{"./model.bin"}; return e }()},
		"保留名manifest": {func() Entry { e := validEntry(); e.Files = []string{"manifest.json"}; return e }()},
	}
	for name, entries := range cases {
		// 引擎条目前置:模型条目的 requires_engine 引用需要可解析,失败才归因于被测用例本身
		_, err := parseCatalog(withEngine(t, entries...))
		if err == nil {
			t.Errorf("%s: 期望被拒绝,实际通过", name)
		}
	}
}

// TestParseCatalogFamilyRuling 落实 family 校验规则:
// kind=tts 必填且 ∈{qwen3_tts, index_tts2};kind∈{asr,engine} 必须为空。
func TestParseCatalogFamilyRuling(t *testing.T) {
	// tts:两个合法族值都通过
	if _, err := parseCatalog(withEngine(t, ttsEntry())); err != nil {
		t.Fatalf("qwen3_tts 族 tts 条目被拒: %v", err)
	}
	e := ttsEntry()
	e.Family = "index_tts2"
	if _, err := parseCatalog(withEngine(t, e)); err != nil {
		t.Fatalf("index_tts2 族 tts 条目被拒: %v", err)
	}
	// tts:缺 family → 拒
	e = ttsEntry()
	e.Family = ""
	if _, err := parseCatalog(withEngine(t, e)); err == nil {
		t.Error("tts 条目缺 family 应被拒")
	}
	// tts:非法族值 → 拒
	e = ttsEntry()
	e.Family = "cosyvoice"
	if _, err := parseCatalog(withEngine(t, e)); err == nil {
		t.Error("tts 条目 family 非法值应被拒")
	}
	// asr:声明 family → 拒
	e = validEntry()
	e.Family = "qwen3_tts"
	if _, err := parseCatalog(withEngine(t, e)); err == nil {
		t.Error("asr 条目声明 family 应被拒")
	}
	// engine:声明 family → 拒
	eng := engineEntry()
	eng.Family = "qwen3_tts"
	if _, err := parseCatalog(mustJSON(t, []Entry{eng})); err == nil {
		t.Error("引擎条目声明 family 应被拒")
	}
}

func TestParseCatalogBadJSON(t *testing.T) {
	if _, err := parseCatalog([]byte("not json")); err == nil {
		t.Fatal("非法 JSON 期望报错")
	}
}

// 内嵌目录自检:随二进制发布的静态资产,损坏必须在首次加载时暴露。
// 断言口径(裁定 2):二期目录为 2 引擎 + 5 模型;license_url 不再统一指向魔搭模型页——
// 引擎条目指向其上游 GitHub 仓库(audiocpp / sherpa-onnx),模型条目指向魔搭模型页
// 或上游 releases/tag 页(sensevoice 指向 sherpa-onnx 的 asr-models tag 页,
// 同为 https://github.com/ 前缀),故按 kind 分支断言前缀,统一只要求非空 https。
func TestEmbeddedCatalog(t *testing.T) {
	wantIDs := []string{
		"sherpa-onnx", "audiocpp",
		"sensevoice-int8", "qwen3-tts-base-q8", "qwen3-tts-customvoice-q8", "qwen3-tts-base-0.6b-q8",
		"index-tts2_5-q8",
	}
	if len(catalog) != len(wantIDs) {
		t.Fatalf("内嵌目录应为 %d 条(2 引擎 + 5 模型),实际 %d", len(wantIDs), len(catalog))
	}
	engineIDs := map[string]bool{}
	for _, e := range catalog {
		if e.Kind == "engine" {
			engineIDs[e.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, e := range catalog {
		if seen[e.ID] {
			t.Errorf("条目 id 重复: %s", e.ID)
			continue
		}
		seen[e.ID] = true
		if e.LicenseURL == "" || !strings.HasPrefix(e.LicenseURL, "https://") {
			t.Errorf("条目 %s 的 license_url 必须为非空 https 链接: %s", e.ID, e.LicenseURL)
		}
		switch e.Kind {
		case "engine":
			if !strings.HasPrefix(e.LicenseURL, "https://github.com/") {
				t.Errorf("引擎条目 %s 的 license_url 应指向其 GitHub 仓库: %s", e.ID, e.LicenseURL)
			}
			if e.RequiresEngine != "" {
				t.Errorf("引擎条目 %s 不应声明 requires_engine: %s", e.ID, e.RequiresEngine)
			}
			if len(e.Assets) == 0 {
				t.Errorf("引擎条目 %s 应声明平台 assets", e.ID)
			}
			if e.Family != "" {
				t.Errorf("引擎条目 %s 不应声明 family: %q", e.ID, e.Family)
			}
		default:
			if !strings.HasPrefix(e.LicenseURL, "https://modelscope.cn/models/") &&
				!strings.HasPrefix(e.LicenseURL, "https://github.com/") {
				t.Errorf("模型条目 %s 的 license_url 应指向魔搭模型页或 GitHub releases 页: %s", e.ID, e.LicenseURL)
			}
			// 裁定 1:asr/tts 条目 requires_engine 必填且指向已声明的 engine 条目
			if e.RequiresEngine == "" || !engineIDs[e.RequiresEngine] {
				t.Errorf("模型条目 %s 的 requires_engine 必须指向已声明引擎: %q", e.ID, e.RequiresEngine)
			}
			// family 规则:tts 必填族值,asr 必须为空
			if e.Kind == "tts" {
				if e.Family != "qwen3_tts" && e.Family != "index_tts2" {
					t.Errorf("tts 条目 %s 的 family 必须是 qwen3_tts|index_tts2: %q", e.ID, e.Family)
				}
			} else if e.Family != "" {
				t.Errorf("asr 条目 %s 不应声明 family: %q", e.ID, e.Family)
			}
		}
	}
	for _, id := range wantIDs {
		if !seen[id] {
			t.Errorf("内嵌目录缺少条目: %s", id)
		}
	}
}

func TestParseCatalogDefaultsRevision(t *testing.T) {
	e := validEntry()
	e.Revision = ""
	entries, err := parseCatalog(withEngine(t, e))
	if err != nil {
		t.Fatal(err)
	}
	if entries[1].Revision != "master" {
		t.Fatalf("revision 缺省应回落 master,实际 %q", entries[1].Revision)
	}
}

// engineEntry 二期引擎条目基线。基线平台资产必须自带 sha256:
// 引擎校验要求每个资产的 sha256 非空,负例用例会单独清空它。
func engineEntry() Entry {
	return Entry{
		ID: "audiocpp", Kind: "engine", Name: "audio.cpp 引擎", Summary: "TTS 运行时",
		SizeBytes: 27000000, License: "MIT", LicenseURL: "https://github.com/0xShug0/audio.cpp",
		Requirements:  Requirements{Device: "metal"},
		Archive:       "tar.gz",
		ArchiveSHA256: strings.Repeat("a", 64),
		Binaries:      []string{"audiocpp_server", "audiocpp_cli"},
		Assets: map[string]Asset{
			"darwin/arm64": {URL: "https://example.com/a.tar.gz", SizeBytes: 27000000, SHA256: strings.Repeat("b", 64)},
		},
	}
}

// withEngine 在模型条目前补一个合法引擎条目再序列化:
// asr/tts 的 requires_engine 必须指向本目录已声明的 engine 条目,悬空引用会被拒。
func withEngine(t *testing.T, models ...Entry) []byte {
	t.Helper()
	return mustJSON(t, append([]Entry{engineEntry()}, models...))
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
	if _, err := parseCatalog(withEngine(t, e)); err != nil {
		t.Fatalf("FileURLs 直链条目被拒: %v", err)
	}
	// 模型:既无 Repo 又缺 FileURLs 覆盖
	e2 := validEntry()
	e2.Repo, e2.FileURLs = "", nil
	if _, err := parseCatalog(withEngine(t, e2)); err == nil {
		t.Error("无 Repo 且无 FileURLs 应被拒")
	}
	// 模型:归档条目必填 ArchiveURL + ExtractFiles
	e3 := validEntry()
	e3.Archive = "tar.bz2"
	if _, err := parseCatalog(withEngine(t, e3)); err == nil {
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
	if _, err := parseCatalog(withEngine(t, e5)); err == nil {
		t.Error("requires_engine 指向不存在引擎应被拒")
	}
}

// TestParseCatalogRequiresEngineRuling 落实控制器裁定:
// asr/tts 条目 requires_engine 必填;引擎条目 requires_engine 必须为空。
func TestParseCatalogRequiresEngineRuling(t *testing.T) {
	// asr 条目缺 requires_engine → 拒
	e := validEntry()
	e.RequiresEngine = ""
	if _, err := parseCatalog(mustJSON(t, []Entry{e})); err == nil {
		t.Error("asr/tts 条目缺 requires_engine 应被拒")
	}
	// 引擎条目声明 requires_engine → 拒
	eng := engineEntry()
	eng.RequiresEngine = "audiocpp"
	if _, err := parseCatalog(mustJSON(t, []Entry{eng})); err == nil {
		t.Error("引擎条目声明 requires_engine 应被拒")
	}
	// 模型条目未经归档/直链路径却把 requires_engine 指向非引擎条目 → 拒
	e2 := validEntry()
	e2.RequiresEngine = "m1"
	if _, err := parseCatalog(mustJSON(t, []Entry{e2})); err == nil {
		t.Error("requires_engine 指向非 engine 条目应被拒")
	}
}

// TestParseCatalogArchiveSizeRuling 落实控制器裁定 4:模型归档条目必须带正的
// archive_size(下载进度与磁盘预检的基准)。
func TestParseCatalogArchiveSizeRuling(t *testing.T) {
	e := validEntry()
	e.Archive = "tar.bz2"
	e.ArchiveURL = "https://example.com/m.tar.bz2"
	e.ArchiveSize = 163002883
	e.ExtractFiles = []string{"model.int8.onnx", "tokens.txt"}
	if _, err := parseCatalog(withEngine(t, e)); err != nil {
		t.Fatalf("完整归档条目被拒: %v", err)
	}
	e.ArchiveSize = 0
	if _, err := parseCatalog(withEngine(t, e)); err == nil {
		t.Error("归档条目缺 archive_size 应被拒")
	}
}

// TestEmbeddedCatalogSenseVoiceSHA 落实控制器裁定 4:sensevoice-int8 的整包 sha256
// 必须落库(Task 1 实测留档),runArchive 据此校验下载完整性。
func TestEmbeddedCatalogSenseVoiceSHA(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "sensevoice-int8" {
			continue
		}
		const want = "7d1efa2138a65b0b488df37f8b89e3d91a60676e416f515b952358d83dfd347e"
		if e.ArchiveSHA256 != want {
			t.Fatalf("sensevoice-int8 archive_sha256 应为实测整包哈希,实际 %q", e.ArchiveSHA256)
		}
		if e.ArchiveSize != 163002883 {
			t.Errorf("sensevoice-int8 archive_size 应为实测 163002883,实际 %d", e.ArchiveSize)
		}
		return
	}
	t.Fatal("内嵌目录缺少 sensevoice-int8 条目")
}

// TestEmbeddedCatalogIndexTTS2 落实 family 规则与 Task 2 spike 实测留档:
// IndexTTS2.5 条目族值必须为 index_tts2,size_bytes 与逐文件 sha256 为本机对整文件实测,
// runFile 据此校验下载完整性。
func TestEmbeddedCatalogIndexTTS2(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "index-tts2_5-q8" {
			continue
		}
		if e.Family != "index_tts2" {
			t.Fatalf("index-tts2_5-q8 的 family 应为 index_tts2,实际 %q", e.Family)
		}
		if e.SizeBytes != 3502955328 {
			t.Errorf("index-tts2_5-q8 size_bytes 应为实测 3502955328,实际 %d", e.SizeBytes)
		}
		const want = "5e827b2072042e4a1b21ccf24a5cb4f71cb1011403067a0a9b039311d8b38628"
		if e.SHA256["index-tts2_5-q8_0.gguf"] != want {
			t.Errorf("index-tts2_5-q8 的文件 sha256 应为实测整文件哈希,实际 %q", e.SHA256["index-tts2_5-q8_0.gguf"])
		}
		if e.RequiresEngine != "audiocpp" {
			t.Errorf("index-tts2_5-q8 的 requires_engine 应为 audiocpp,实际 %q", e.RequiresEngine)
		}
		return
	}
	t.Fatal("内嵌目录缺少 index-tts2_5-q8 条目")
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
