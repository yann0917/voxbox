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
// kind=tts 必填且 ∈{qwen3_tts, index_tts2, kokoro, chatterbox, voxcpm2};
// kind=asr 可空(sherpa 默认)或 confucius4_r2t2(audiocpp ASR);kind=engine 必须为空。
func TestParseCatalogFamilyRuling(t *testing.T) {
	// tts:合法族值都通过
	for _, family := range []string{"qwen3_tts", "index_tts2", "kokoro", "chatterbox", "voxcpm2"} {
		e := ttsEntry()
		e.Family = family
		if _, err := parseCatalog(withEngine(t, e)); err != nil {
			t.Fatalf("%s 族 tts 条目被拒: %v", family, err)
		}
	}
	// tts:缺 family → 拒
	e := ttsEntry()
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
	// asr:声明 confucius4_r2t2 → 通过(R2T2 走 audiocpp 引擎)
	e = validEntry()
	e.Family = "confucius4_r2t2"
	if _, err := parseCatalog(withEngine(t, e)); err != nil {
		t.Fatalf("asr 条目声明 confucius4_r2t2 应通过: %v", err)
	}
	// asr:声明 tts 族值 → 拒
	e = validEntry()
	e.Family = "qwen3_tts"
	if _, err := parseCatalog(withEngine(t, e)); err == nil {
		t.Error("asr 条目声明 tts 族应被拒")
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
// 断言口径(裁定 2):二期目录为 2 引擎 + 9 模型;license_url 不再统一指向魔搭模型页——
// 引擎条目指向其上游 GitHub 仓库(audiocpp / sherpa-onnx),模型条目指向魔搭模型页
// 或上游 releases/tag 页(sensevoice 指向 sherpa-onnx 的 asr-models tag 页,
// r2t2 指向 HF gguf 仓库页,同为 https:// 前缀),故按 kind 分支断言前缀,统一只要求非空 https。
func TestEmbeddedCatalog(t *testing.T) {
	wantIDs := []string{
		"sherpa-onnx", "audiocpp",
		"sensevoice-int8", "r2t2-q8_0", "qwen3-tts-base-q8", "qwen3-tts-customvoice-q8", "qwen3-tts-base-0.6b-q8",
		"index-tts2_5-q8", "kokoro-v1.0", "chatterbox-q8", "voxcpm2-q8",
	}
	if len(catalog) != len(wantIDs) {
		t.Fatalf("内嵌目录应为 %d 条(2 引擎 + 9 模型),实际 %d", len(wantIDs), len(catalog))
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
				!strings.HasPrefix(e.LicenseURL, "https://github.com/") &&
				!strings.HasPrefix(e.LicenseURL, "https://huggingface.co/") {
				t.Errorf("模型条目 %s 的 license_url 应指向魔搭模型页/GitHub releases 页/HF 模型页: %s", e.ID, e.LicenseURL)
			}
			// 裁定 1:asr/tts 条目 requires_engine 必填且指向已声明的 engine 条目
			if e.RequiresEngine == "" || !engineIDs[e.RequiresEngine] {
				t.Errorf("模型条目 %s 的 requires_engine 必须指向已声明引擎: %q", e.ID, e.RequiresEngine)
			}
			// family 规则:tts 必填族值;asr 可空(sherpa)或 confucius4_r2t2
			if e.Kind == "tts" {
				switch e.Family {
				case "qwen3_tts", "index_tts2", "kokoro", "chatterbox", "voxcpm2":
				default:
					t.Errorf("tts 条目 %s 的 family 必须是 qwen3_tts|index_tts2|kokoro|chatterbox|voxcpm2: %q", e.ID, e.Family)
				}
			} else if e.Kind == "asr" && e.Family != "" && e.Family != "confucius4_r2t2" {
				t.Errorf("asr 条目 %s 的 family 必须为空或 confucius4_r2t2: %q", e.ID, e.Family)
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

// TestEmbeddedCatalogKokoro 落实 family 规则与 kokoro 条目实测留档:
// kokoro-multi-lang-v1_0(fp32)即官方 hexgrad/Kokoro-82M 的 sherpa-onnx 导出
// (rewind.ai 同款 v1.0 经典中文音色所在包),archive_size/sha256 为本机对整包实测;
// espeak-ng-data/ 目录条目验证 checkRelPath 的目录白名单语义(尾缀 / 放行,
// 归档内嵌数据目录不能被 basename 扁平化散架)。
func TestEmbeddedCatalogKokoro(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "kokoro-v1.0" {
			continue
		}
		if e.Family != "kokoro" {
			t.Fatalf("kokoro-v1.0 的 family 应为 kokoro,实际 %q", e.Family)
		}
		if e.RequiresEngine != "sherpa-onnx" {
			t.Errorf("kokoro-v1.0 的 requires_engine 应为 sherpa-onnx,实际 %q", e.RequiresEngine)
		}
		if e.SizeBytes != 349906910 || e.ArchiveSize != 349906910 {
			t.Errorf("kokoro-v1.0 size/archive_size 应为实测 349906910,实际 %d/%d",
				e.SizeBytes, e.ArchiveSize)
		}
		const want = "c5f7e2d2caf082bc1d20fb70334a61d99d20b484500aad32e7cf84c128ea3298"
		if e.ArchiveSHA256 != want {
			t.Errorf("kokoro-v1.0 archive_sha256 应为实测整包哈希,实际 %q", e.ArchiveSHA256)
		}
		for _, want := range []string{"model.onnx", "voices.bin", "tokens.txt",
			"lexicon-us-en.txt", "lexicon-zh.txt", "date-zh.fst", "phone-zh.fst", "number-zh.fst", "espeak-ng-data/"} {
			found := false
			for _, f := range e.ExtractFiles {
				if f == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("kokoro-v1.0 的 extract_files 缺少 %q", want)
			}
		}
		return
	}
	t.Fatal("内嵌目录缺少 kokoro-v1.0 条目")
}

// TestCheckRelPathDirEntries 目录条目(尾缀 /)的放行与拒绝边界。
func TestCheckRelPathDirEntries(t *testing.T) {
	for _, ok := range []string{"espeak-ng-data/", "dict/"} {
		if err := checkRelPath("m1", ok); err != nil {
			t.Errorf("目录条目 %q 应放行: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/", "..", "../", "a/../b/", "./x/", "manifest.json/"} {
		if err := checkRelPath("m1", bad); err == nil {
			t.Errorf("非法目录条目 %q 应被拒", bad)
		}
	}
}

// TestEmbeddedCatalogChatterbox 落实 family 规则与 chatterbox 条目实测留档:
// Chatterbox-GGUF/chatterbox-q8_0.gguf 为本机两次下载实测(sha256 一致),
// file_urls 直链走魔搭 HereIsMark 仓库,MIT 许可指向 HF 官方模型页。
func TestEmbeddedCatalogChatterbox(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "chatterbox-q8" {
			continue
		}
		if e.Family != "chatterbox" {
			t.Fatalf("chatterbox-q8 的 family 应为 chatterbox,实际 %q", e.Family)
		}
		if e.RequiresEngine != "audiocpp" {
			t.Errorf("chatterbox-q8 的 requires_engine 应为 audiocpp,实际 %q", e.RequiresEngine)
		}
		if e.SizeBytes != 2088393668 {
			t.Errorf("chatterbox-q8 size_bytes 应为实测 2088393668,实际 %d", e.SizeBytes)
		}
		const want = "d586dd1aa59613cab8046176fb7ca5ba191c02a9b10ffa5b0d892ed22b470656"
		if e.SHA256["chatterbox-q8_0.gguf"] != want {
			t.Errorf("chatterbox-q8 的文件 sha256 应为实测整文件哈希,实际 %q", e.SHA256["chatterbox-q8_0.gguf"])
		}
		if e.License != "MIT" {
			t.Errorf("chatterbox-q8 许可应为 MIT,实际 %q", e.License)
		}
		return
	}
	t.Fatal("内嵌目录缺少 chatterbox-q8 条目")
}

// TestEmbeddedCatalogVoxCPM2 落实 family 规则与 voxcpm 条目实测留档:
// VoxCPM2-GGUF/voxcpm2-q8_0.gguf 为本机 spike 下载实测,魔搭 HereIsMark 直链,Apache-2.0。
func TestEmbeddedCatalogVoxCPM2(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "voxcpm2-q8" {
			continue
		}
		if e.Family != "voxcpm2" {
			t.Fatalf("voxcpm2-q8 的 family 应为 voxcpm2,实际 %q", e.Family)
		}
		if e.RequiresEngine != "audiocpp" {
			t.Errorf("voxcpm2-q8 的 requires_engine 应为 audiocpp,实际 %q", e.RequiresEngine)
		}
		if e.SizeBytes != 2955000480 {
			t.Errorf("voxcpm2-q8 size_bytes 应为实测 2955000480,实际 %d", e.SizeBytes)
		}
		const want = "c8e01ab4416011e12a28f24ede298a1aa5ce64b43f8e8aaad53b1e2fe7c96432"
		if e.SHA256["voxcpm2-q8_0.gguf"] != want {
			t.Errorf("voxcpm2-q8 的文件 sha256 应为实测整文件哈希,实际 %q", e.SHA256["voxcpm2-q8_0.gguf"])
		}
		return
	}
	t.Fatal("内嵌目录缺少 voxcpm2-q8 条目")
}

// TestEmbeddedCatalogR2T2 落实 R2T2 条目实测留档(Task 1):
// kind=asr + family=confucius4_r2t2 + requires_engine=audiocpp;直链指向 HF
// davidxifeng/Confucius4-R2T2-gguf(网易有道模型许可,直链下载非再分发),
// size_bytes 与逐文件 sha256 为本机对整文件实测(2477512064B),runFile 据此校验。
func TestEmbeddedCatalogR2T2(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "r2t2-q8_0" {
			continue
		}
		if e.Kind != "asr" {
			t.Errorf("r2t2-q8_0 的 kind 应为 asr,实际 %q", e.Kind)
		}
		if e.Family != "confucius4_r2t2" {
			t.Errorf("r2t2-q8_0 的 family 应为 confucius4_r2t2,实际 %q", e.Family)
		}
		if e.RequiresEngine != "audiocpp" {
			t.Errorf("r2t2-q8_0 的 requires_engine 应为 audiocpp,实际 %q", e.RequiresEngine)
		}
		if e.SizeBytes != 2477512064 {
			t.Errorf("r2t2-q8_0 size_bytes 应为实测 2477512064,实际 %d", e.SizeBytes)
		}
		const want = "19f5ccd624484bcb5d44301437de41560b0ecc40c430e8850dfeefefbe82ccf5"
		if e.SHA256["r2t2-q8_0.gguf"] != want {
			t.Errorf("r2t2-q8_0 的文件 sha256 应为实测整文件哈希,实际 %q", e.SHA256["r2t2-q8_0.gguf"])
		}
		wantURL := "https://huggingface.co/davidxifeng/Confucius4-R2T2-gguf/resolve/main/r2t2-q8_0.gguf"
		if e.FileURLs["r2t2-q8_0.gguf"] != wantURL {
			t.Errorf("r2t2-q8_0 的直链应为 HF 官方 gguf 仓库,实际 %q", e.FileURLs["r2t2-q8_0.gguf"])
		}
		return
	}
	t.Fatal("内嵌目录缺少 r2t2-q8_0 条目")
}

// TestEmbeddedCatalogAudiocppV090 落实引擎提版留档(Task 1):audiocpp 引擎 revision
// 显式记为 v0.9.0(v0.8.2 时代条目无 revision 缺省 master,提版不带 revision 会让
// 已装旧二进制的安装永远匹配 manifest 而不触发重装;显式 revision 让旧安装判为
// 未安装并引导重下 29MB),资产 URL 指向 v0.9.0 release,四平台资产(与 v0.8.2
// 条目选型一致)sha256 均为本机对整包实测;binaries 与 v0.9.0 解包布局一致(server/cli 在包根)。
func TestEmbeddedCatalogAudiocppV090(t *testing.T) {
	for _, e := range catalog {
		if e.ID != "audiocpp" || e.Kind != "engine" {
			continue
		}
		if e.Revision != "v0.9.0" {
			t.Errorf("audiocpp 引擎 revision 应为 v0.9.0(提版驱动旧安装重装),实际 %q", e.Revision)
		}
		if len(e.Assets) != 4 {
			t.Fatalf("audiocpp 应声明 4 个平台资产(darwin arm64/amd64 + windows amd64 + linux amd64),实际 %d", len(e.Assets))
		}
		want := map[string]struct {
			url  string
			size int64
			sha  string
		}{
			"darwin/arm64": {
				"https://github.com/0xShug0/audio.cpp/releases/download/v0.9.0/audio-v0.9.0-bin-macos-arm64-metal.tar.gz",
				29162796, "7cea9219d5f06475011c5d225d71d988cecef633ff7d098ee8a4c7b08583b1b4",
			},
			"darwin/amd64": {
				"https://github.com/0xShug0/audio.cpp/releases/download/v0.9.0/audio-v0.9.0-bin-macos-x64-metal.tar.gz",
				30897245, "f6e50c776bfe3b23cb5e420f1dd31b11661ed3dc01207cf05cf780dfdadca1c9",
			},
			"windows/amd64": {
				"https://github.com/0xShug0/audio.cpp/releases/download/v0.9.0/audio-v0.9.0-bin-windows-x64-cpu-portable.zip",
				25803809, "3ee19466a1a2b5366364ca8447a4794391dd89671e655ffd01aa421ac1668bfa",
			},
			"linux/amd64": {
				"https://github.com/0xShug0/audio.cpp/releases/download/v0.9.0/audio-v0.9.0-bin-ubuntu-x64-cpu.tar.gz",
				49587362, "d0f0db4ab13bd3de1c15b6d60256e615d2bac65892fdd4fccc9a2b678db47635",
			},
		}
		for plat, w := range want {
			a, ok := e.Assets[plat]
			if !ok {
				t.Errorf("audiocpp 缺少平台资产 %s", plat)
				continue
			}
			if a.URL != w.url || a.SizeBytes != w.size || a.SHA256 != w.sha {
				t.Errorf("audiocpp 平台 %s 资产与实测不符: %+v", plat, a)
			}
		}
		if e.SizeBytes != 29162796 {
			t.Errorf("audiocpp size_bytes 应为本机平台(darwin/arm64)资产实测 29162796,实际 %d", e.SizeBytes)
		}
		return
	}
	t.Fatal("内嵌目录缺少 audiocpp 引擎条目")
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

// TestEmbeddedCatalogQwen3SHA 落实 qwen3 三条目的整文件哈希(2026-10 完整性加固补齐):
// base/customvoice 为本机播种文件 shasum 实测,与魔搭 repo/files API 的 Sha256 一致;
// 0.6b 未播种,取同 API 的 Sha256(同源两例已实测核验,提取可信)。
// 至此 11 个条目全部具备 sha256(引擎按平台资产,模型逐文件/整包归档)。
func TestEmbeddedCatalogQwen3SHA(t *testing.T) {
	want := map[string]map[string]string{
		"qwen3-tts-base-q8":        {"qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf": "b55e06c7890d43c208d15aed8b4ed3f18215f295e47d5960e061b15bff338ab0"},
		"qwen3-tts-customvoice-q8": {"qwen3-tts-12hz-1.7b-customvoice-q8_0.gguf": "3cfaac8e9f13554f6daea3c5e0c53fede71ef5500cbaae7445e5fc3a5bb12e72"},
		"qwen3-tts-base-0.6b-q8":   {"qwen3-tts-12hz-0.6b-base-q8_0.gguf": "771420bd20ff5f35407b4fa9cf9c5461e153800d3d772ef51c9febc0a520855d"},
	}
	seen := map[string]bool{}
	for _, e := range catalog {
		w, ok := want[e.ID]
		if !ok {
			continue
		}
		seen[e.ID] = true
		for f, h := range w {
			if e.SHA256[f] != h {
				t.Errorf("%s 的 %s sha256 应为 %q,实际 %q", e.ID, f, h, e.SHA256[f])
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("内嵌目录缺少 %s 条目", id)
		}
	}
}
