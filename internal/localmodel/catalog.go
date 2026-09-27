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
	Device string `json:"device"`            // cuda | cpu
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
			if f == "manifest.json" {
				// 保留名:manifest.json 是安装完成标记,清单文件同名会覆盖标记,
				// restore 读到非法 manifest 会把已安装模型静默变回未安装。
				return nil, fmt.Errorf("条目 %s 文件名 manifest.json 为安装标记保留: %q", e.ID, f)
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
