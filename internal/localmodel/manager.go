package localmodel

import (
	"context"
	"encoding/json"
	"errors"
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
	ID              string `json:"-"` // 冗余记账(状态表按 id 索引);JSON 由 ModelView.Entry.id 提供,置 "-" 防平铺冲突
	Status          Status `json:"status"`
	HasPartial      bool   `json:"has_partial"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Error           string `json:"error,omitempty"`
}

// ModelView GET /api/models 的 items 元素:目录条目 + 实时状态(内嵌 Entry + ModelState,
// JSON 序列化平铺,与前端 ModelItem 一一对应)。
type ModelView struct {
	Entry
	ModelState
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
