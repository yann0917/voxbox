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
	Device string `json:"device"`            // cpu | cuda | metal
	VRAMGB int    `json:"vram_gb,omitempty"` // device=cuda 时的显存下限(GB)
}

// Asset 引擎的平台归档资产。
type Asset struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Entry 目录条目:清单是唯一人工维护面,调整模型只改 catalog.json + 发版。
type Entry struct {
	ID           string            `json:"id"`   // 稳定 slug:磁盘目录名与 API 路径参数
	Repo         string            `json:"repo"` // 魔搭 repo(如 Qwen/Qwen3-TTS-12Hz-1.7B-Base);直链/引擎条目可空
	Revision     string            `json:"revision"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"` // asr | tts | engine
	Summary      string            `json:"summary"`
	SizeBytes    int64             `json:"size_bytes"` // 仅展示(「约」);真实大小以下载响应为准
	Files        []string          `json:"files"`      // 白名单:逐文件顺序下载,不运行时爬 repo
	Requirements Requirements      `json:"requirements"`
	License      string            `json:"license"`
	LicenseURL   string            `json:"license_url"` // 魔搭模型页或上游仓库/releases 页,设置页渲染为内嵌链接
	SHA256       map[string]string `json:"sha256,omitempty"`

	// —— v2:引擎分发 / 整包归档 / 直链模型 ——
	Archive        string            `json:"archive,omitempty"`     // tar.bz2 | tar.gz | zip;空=按 Files 逐文件
	ArchiveURL     string            `json:"archive_url,omitempty"` // 引擎经 Assets 按平台选择;模型归档条目直接写这里
	ArchiveSize    int64             `json:"archive_size,omitempty"`
	ArchiveSHA256  string            `json:"archive_sha256,omitempty"` // 引擎必填
	ExtractFiles   []string          `json:"extract_files,omitempty"`  // 归档解包白名单
	Binaries       []string          `json:"binaries,omitempty"`       // engine:解包内必须存在的可执行文件
	Assets         map[string]Asset  `json:"assets,omitempty"`         // engine:键 "goos/goarch"
	FileURLs       map[string]string `json:"file_urls,omitempty"`      // file → 直链;Repo 非空时可省
	RequiresEngine string            `json:"requires_engine,omitempty"`
}

// ArchiveFor 按 GOOS/GOARCH 取引擎平台资产;未声明平台返回 false(该平台不展示此引擎)。
func (e Entry) ArchiveFor(goos, goarch string) (Asset, bool) {
	a, ok := e.Assets[goos+"/"+goarch]
	return a, ok
}

// isSHA256 判断是否 64 位小写十六进制摘要(与 shasum -a 256 输出同口径,避免大小写比较坑)。
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkRelPath 校验清单内的相对文件路径:绝对路径、反斜杠、上级穿越与未规范化形式一律拒绝。
func checkRelPath(entryID, f string) error {
	if f == "" || path.IsAbs(f) || strings.ContainsRune(f, '\\') ||
		strings.HasPrefix(path.Clean(f), "..") || path.Clean(f) != f {
		return fmt.Errorf("条目 %s 文件路径非法: %q", entryID, f)
	}
	if f == "manifest.json" {
		// 保留名:manifest.json 是安装完成标记,清单文件同名会覆盖标记,
		// restore 读到非法 manifest 会把已安装模型静默变回未安装。
		return fmt.Errorf("条目 %s 文件名 manifest.json 为安装标记保留: %q", entryID, f)
	}
	return nil
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
	// 两遍扫描:第一遍收集引擎 id 集,第二遍校验 requires_engine 引用。
	engines := map[string]bool{}
	for _, e := range entries {
		if e.Kind == "engine" {
			engines[e.ID] = true
		}
	}
	seen := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if e.ID == "" || e.Name == "" || e.Kind == "" || e.Summary == "" || e.License == "" || e.LicenseURL == "" {
			return nil, fmt.Errorf("条目缺少必填字段: %+v", *e)
		}
		if e.Kind != "asr" && e.Kind != "tts" && e.Kind != "engine" {
			return nil, fmt.Errorf("条目 %s 的 kind 必须是 asr|tts|engine", e.ID)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("条目 id 重复: %s", e.ID)
		}
		seen[e.ID] = true
		if e.Revision == "" {
			e.Revision = "master"
		}
		switch e.Requirements.Device {
		case "cpu", "cuda", "metal":
		default:
			return nil, fmt.Errorf("条目 %s 的 requirements.device 必须是 cpu|cuda|metal", e.ID)
		}
		if e.SizeBytes <= 0 {
			return nil, fmt.Errorf("条目 %s 的 size_bytes 必须为正(展示用)", e.ID)
		}
		if e.Kind == "engine" {
			if err := validateEngine(e); err != nil {
				return nil, err
			}
			continue
		}
		if err := validateModel(e, engines); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// validateEngine 校验引擎条目:平台资产分发,不使用 Repo/Files 逐文件下载,
// 因此跳过一期的文件检查(Files 可为空,Repo 可空)。
func validateEngine(e *Entry) error {
	if e.RequiresEngine != "" {
		// 引擎是 requires_engine 引用的一端,自身不得再挂引擎
		return fmt.Errorf("引擎条目 %s 不应声明 requires_engine", e.ID)
	}
	switch e.Archive {
	case "tar.bz2", "tar.gz", "zip":
	default:
		return fmt.Errorf("引擎条目 %s 的 archive 必须是 tar.bz2|tar.gz|zip", e.ID)
	}
	if !isSHA256(e.ArchiveSHA256) {
		return fmt.Errorf("引擎条目 %s 的 archive_sha256 必须是 64 位十六进制", e.ID)
	}
	if len(e.Binaries) == 0 {
		return fmt.Errorf("引擎条目 %s 必须声明 binaries(解包内必须存在的可执行文件)", e.ID)
	}
	for _, b := range e.Binaries {
		if b == "" {
			return fmt.Errorf("引擎条目 %s 的 binaries 含空项", e.ID)
		}
	}
	if len(e.Assets) == 0 {
		return fmt.Errorf("引擎条目 %s 必须声明平台 assets", e.ID)
	}
	for plat, a := range e.Assets {
		goos, goarch, found := strings.Cut(plat, "/")
		if !found || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return fmt.Errorf("引擎条目 %s 的资产键 %q 必须是 \"goos/goarch\"", e.ID, plat)
		}
		if a.URL == "" {
			return fmt.Errorf("引擎条目 %s 平台 %s 的资产缺 url", e.ID, plat)
		}
		if a.SizeBytes <= 0 {
			return fmt.Errorf("引擎条目 %s 平台 %s 的资产 size_bytes 必须为正", e.ID, plat)
		}
		if !isSHA256(a.SHA256) {
			return fmt.Errorf("引擎条目 %s 平台 %s 的资产 sha256 必须是 64 位十六进制", e.ID, plat)
		}
	}
	return nil
}

// validateModel 校验 asr/tts 条目:逐文件直链或整包归档;requires_engine 必填且
// 必须指向本目录已声明的 engine 条目。
func validateModel(e *Entry, engines map[string]bool) error {
	if e.RequiresEngine == "" {
		return fmt.Errorf("条目 %s(kind=%s)必须声明 requires_engine", e.ID, e.Kind)
	}
	if !engines[e.RequiresEngine] {
		return fmt.Errorf("条目 %s 的 requires_engine %q 不是本目录已声明的 engine 条目", e.ID, e.RequiresEngine)
	}
	if e.Archive != "" {
		// 归档条目:整包下载 + 解包白名单,二者缺一不可;archive_size 必须为正
		// (下载进度与磁盘预检的基准,缺失会让 installed 态进度失真)
		if e.ArchiveURL == "" {
			return fmt.Errorf("归档条目 %s 缺少 archive_url", e.ID)
		}
		if e.ArchiveSize <= 0 {
			return fmt.Errorf("归档条目 %s 的 archive_size 必须为正", e.ID)
		}
		if len(e.ExtractFiles) == 0 {
			return fmt.Errorf("归档条目 %s 缺少 extract_files(解包白名单)", e.ID)
		}
		for _, f := range e.ExtractFiles {
			if err := checkRelPath(e.ID, f); err != nil {
				return err
			}
		}
	}
	if len(e.Files) == 0 && e.Archive == "" {
		return fmt.Errorf("条目 %s 没有任何文件", e.ID)
	}
	covered := map[string]bool{}
	for _, f := range e.Files {
		if err := checkRelPath(e.ID, f); err != nil {
			return err
		}
		covered[f] = true
	}
	if e.Repo == "" {
		// 无 repo:每个文件必须给出非空直链
		for _, f := range e.Files {
			if e.FileURLs[f] == "" {
				return fmt.Errorf("条目 %s 无 repo,文件 %s 缺少 file_urls 直链", e.ID, f)
			}
		}
	}
	// file_urls 多出的键(不在 Files 白名单)一律拒绝,防止清单漂移
	for f := range e.FileURLs {
		if !covered[f] {
			return fmt.Errorf("条目 %s 的 file_urls 含未在 files 中声明的文件 %q", e.ID, f)
		}
	}
	return nil
}

// catalog 包级加载:目录是随二进制发布的静态资产,不合法直接 panic(fail-fast)。
var catalog = func() []Entry {
	entries, err := parseCatalog(embeddedCatalog)
	if err != nil {
		panic(err)
	}
	return entries
}()
