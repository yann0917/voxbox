package localmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

// ErrBusy 全局单飞行冲突。
var ErrBusy = errors.New("已有模型在下载")

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
	m.setFailedLocked(id, msg)
}

// setFailedLocked 已持锁的失败态写入(diskResidue 做盘 IO,Start 预检复用)。
// 终态与 active 清除同临界区,消除「状态已 failed 而 Start 仍被 ErrBusy 误拒」的窗口;
// 按 id 条件清除,避免 Start 预检失败误伤其他在途下载。未知 id 无状态可写,直接返回(防御 nil 解引用)。
func (m *Manager) setFailedLocked(id, msg string) {
	st := m.states[id]
	if st == nil {
		return
	}
	st.Status, st.Error = StatusFailed, msg
	if m.active == id {
		m.active, m.cancel = "", nil
	}
	has, downloaded := diskResidue(m.modelDir(id))
	st.HasPartial, st.DownloadedBytes = has, downloaded
}

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
	_, have := diskResidue(m.modelDir(id)) // 已在盘上的字节(半程文件+ .part)
	if err := checkDiskFree(m.baseDir, e.SizeBytes-have); err != nil {
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
	// 安全网:终态写入点已各自同临界区清 active,此处仅在异常路径兜底。
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
	if m.active == e.ID { // installed 终态与 active 清除同临界区
		m.active, m.cancel = "", nil
	}
	m.mu.Unlock()
}

// finish 区分取消与真实错误:取消(Stop)→ idle+可续传(错误清空);其余 → failed。
// 两分支的终态写入都与 active 清除同临界区,保证「idle/failed 可见 ⇒ 可立即 Start」。
func (m *Manager) finish(ctx context.Context, id string, err error) {
	if ctx.Err() != nil {
		m.mu.Lock()
		st := m.states[id]
		has, downloaded := diskResidue(m.modelDir(id))
		st.Status, st.HasPartial, st.DownloadedBytes, st.Error = StatusIdle, has, downloaded, ""
		if m.active == id {
			m.active, m.cancel = "", nil
		}
		m.mu.Unlock()
		return
	}
	m.setFailed(id, err.Error())
}

// fileURL 魔搭单文件直链:FilePath 经查询串编码(路径分隔符 %2F),公开模型免 token。
func (m *Manager) fileURL(e Entry, file string) string {
	q := url.Values{}
	q.Set("Revision", e.Revision)
	q.Set("FilePath", file)
	return fmt.Sprintf("%s/api/v1/models/%s/repo?%s", m.baseURL, e.Repo, q.Encode())
}

// contentRangeTotal 解析 Content-Range: bytes S-E/N 头,返回总大小 N;缺失/畸形/非正返回 false。
// probe 的 206 路径与「200 怪癖」路径共用(魔搭对非 LFS 小文件的探测回 200 却带真实大小的该头)。
func contentRangeTotal(cr string) (int64, bool) {
	i := strings.LastIndexByte(cr, '/')
	if !strings.HasPrefix(cr, "bytes ") || i < 0 {
		return 0, false
	}
	total, err := strconv.ParseInt(cr[i+1:], 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
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
		total, ok := contentRangeTotal(cr)
		if !ok {
			return 0, false, fmt.Errorf("响应 Content-Range 异常: %q", cr)
		}
		return total, true, nil
	case http.StatusOK:
		// 魔搭怪癖:非 LFS 小文件(直链无 302)对 bytes=0-0 回 200 + Content-Range: bytes 0-0/N
		// (N 为真实大小)+ Content-Length: 1(单字节 body)。此时大小必须取 N——采信
		// Content-Length 会把小文件记成 1 字节,既污染 total_bytes 又拖垮 rangeOK 触发续传回零。
		// 真实续传请求(bytes=S-)实测走 302 → CDN 206,Range 有效,故 canRange=true。
		if total, ok := contentRangeTotal(resp.Header.Get("Content-Range")); ok {
			return total, true, nil
		}
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
			// 目录条目允许子目录文件(如 speech_tokenizer/model.safetensors),落盘前补父目录
			if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
				resp.Body.Close()
				return fmt.Errorf("创建文件目录失败: %w", err)
			}
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
