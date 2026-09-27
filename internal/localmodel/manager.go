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
	"path"
	"path/filepath"
	"runtime"
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
	Binary      string         `json:"binary,omitempty"` // 引擎 server 二进制的安装目录相对路径(如 pkg/bin/sherpa-onnx-offline)
	CompletedAt string         `json:"completed_at"`
}

type manifestFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Manager 下载管理器:全局同时 1 个下载;状态内存即时更新(不落库),
// 磁盘是安装态权威;进度无节流(API 轮询即节流)。
type Manager struct {
	mu         sync.Mutex
	entries    []Entry
	byID       map[string]Entry
	baseDir    string // <dataDir>/models(模型条目安装根)
	enginesDir string // <dataDir>/engines(引擎条目安装根)
	baseURL    string // 魔搭 API 基址(测试注入 httptest)
	states     map[string]*ModelState
	active     string             // 正在下载的模型 id(""=无)
	cancel     context.CancelFunc // 活动下载的取消函数
}

// NewManager 构造管理器并扫盘恢复(服务启动期调用一次)。
func NewManager(dataDir string) *Manager {
	return newManager(filepath.Join(dataDir, "models"), filepath.Join(dataDir, "engines"), DefaultBaseURL, catalog)
}

// newManager 包内构造:模型与引擎安装根分离,测试注入 entries 与 baseURL。
func newManager(modelsDir, enginesDir, baseURL string, entries []Entry) *Manager {
	byID := make(map[string]Entry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	m := &Manager{entries: entries, byID: byID, baseDir: modelsDir, enginesDir: enginesDir, baseURL: baseURL, states: map[string]*ModelState{}}
	m.restore()
	return m
}

// Dir 模型存储根目录(桌面「打开模型目录」消费;引擎目录在同级 engines/,不在此视图)。
func (m *Manager) Dir() string { return m.baseDir }

// modelDir 条目安装目录:引擎分流到 engines/<id>,其余(模型)留在 models/<id>。
func (m *Manager) modelDir(id string) string {
	if e, ok := m.byID[id]; ok && e.Kind == "engine" {
		return filepath.Join(m.enginesDir, id)
	}
	return filepath.Join(m.baseDir, id)
}

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

// installedManifest 盘面安装裁定(engine 与模型同口径):manifest 可解析且 id/revision
// 与目录条目一致才返回,否则 ok=false——与 restore 的恢复判定完全一致。磁盘即真相:
// 消费者(推理会话)不依赖异步收敛的内存状态,安装/播种完成后立即可见。
func (m *Manager) installedManifest(e Entry) (*manifest, bool) {
	mf, err := readManifest(m.modelDir(e.ID))
	if err != nil || mf.ID != e.ID || mf.Revision != e.Revision {
		return nil, false
	}
	return mf, true
}

// GetEntry 目录条目只读访问。
func (m *Manager) GetEntry(id string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	return e, ok
}

// Installed 该条目是否已安装(engine 与模型同语义:盘面 manifest 合法即已安装)。
func (m *Manager) Installed(id string) bool {
	m.mu.Lock()
	e, ok := m.byID[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	_, installed := m.installedManifest(e)
	return installed
}

// EngineBinary 已安装引擎的 server 二进制绝对路径(manifest.Binary 记录,一期语义:Binaries[0])。
// 未安装提示到设置页下载;manifest 在而 Binary 缺失按安装记录损坏提示删除重装。
func (m *Manager) EngineBinary(id string) (string, error) {
	m.mu.Lock()
	e, ok := m.byID[id]
	m.mu.Unlock()
	if !ok || e.Kind != "engine" {
		return "", fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	mf, installed := m.installedManifest(e)
	if !installed {
		return "", fmt.Errorf("引擎未安装: %s,请到设置页下载", e.Name)
	}
	if mf.Binary == "" {
		return "", fmt.Errorf("引擎安装记录损坏: %s,请到设置页删除后重装", e.Name)
	}
	return filepath.Join(m.modelDir(id), filepath.FromSlash(mf.Binary)), nil
}

// InstalledModelFile 已安装「裸单文件」条目(如 GGUF)的唯一文件绝对路径。
// 引擎走 EngineBinary,多文件条目无「唯一文件」可言,均显式报错。
func (m *Manager) InstalledModelFile(id string) (string, error) {
	m.mu.Lock()
	e, ok := m.byID[id]
	m.mu.Unlock()
	if !ok || e.Kind == "engine" {
		return "", fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	if _, installed := m.installedManifest(e); !installed {
		return "", fmt.Errorf("模型未安装: %s,请到设置页下载", e.Name)
	}
	if len(e.Files) != 1 {
		return "", fmt.Errorf("条目 %s 不是单文件模型", id)
	}
	return filepath.Join(m.modelDir(id), filepath.FromSlash(e.Files[0])), nil
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
	// verifying 期间 active 仍为该 id,校验态检查必须在前,否则误报「正在下载」。
	if m.states[id].Status == StatusVerifying {
		return fmt.Errorf("模型校验中,请稍后再删除")
	}
	if m.active == id {
		return fmt.Errorf("模型正在下载,请先暂停再删除")
	}
	if err := os.RemoveAll(m.modelDir(id)); err != nil {
		return fmt.Errorf("删除模型目录失败: %w", err)
	}
	m.states[id] = &ModelState{ID: id, Status: StatusIdle, TotalBytes: e.SizeBytes}
	return nil
}

// run 下载主循环:归档条目(引擎/整包模型)分流 runArchive;逐文件条目走
// probe(真实大小+Range 支持+404 前置发现)→ 流式下载(.part+续传)→ 校验 → 写 manifest。
// 取消(Stop)回 idle+可续传,真实错误进 failed。
func (m *Manager) run(ctx context.Context, e Entry) {
	// 安全网:终态写入点已各自同临界区清 active,此处仅在异常路径兜底。
	defer func() {
		m.mu.Lock()
		if m.active == e.ID {
			m.active, m.cancel = "", nil
		}
		m.mu.Unlock()
	}()
	if e.Archive != "" {
		// 引擎条目与整包归档条目:单归档下载 → 解包,不走逐文件管线
		m.runArchive(ctx, e)
		return
	}
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

// fileURLFor 单文件下载地址:FileURLs 直链覆盖 → modelscope 模板。
// GGUF 直链条目(Repo 可空)与归档直链均经此统一取址。
func (m *Manager) fileURLFor(e Entry, file string) string {
	if u, ok := e.FileURLs[file]; ok {
		return u
	}
	return m.fileURL(e, file)
}

// pickAssetURL 归档条目的下载源与解包格式(控制器裁定):engine 按当前 GOOS/GOARCH
// 选平台资产,解包格式以资产 URL 扩展名为准(条目级 archive 降级为提示字段);
// 模型归档条目用条目直属 ArchiveURL/ArchiveSize/ArchiveSHA256,格式即条目级 archive。
func pickAssetURL(e Entry) (url string, size int64, sha string, format string, err error) {
	if e.Kind == "engine" {
		a, ok := e.ArchiveFor(runtime.GOOS, runtime.GOARCH)
		if !ok {
			return "", 0, "", "", fmt.Errorf("当前平台 %s/%s 暂不提供该引擎", runtime.GOOS, runtime.GOARCH)
		}
		return a.URL, a.SizeBytes, a.SHA256, archiveFormatFromURL(a.URL, e.Archive), nil
	}
	return e.ArchiveURL, e.ArchiveSize, e.ArchiveSHA256, e.Archive, nil
}

// archiveFormatFromURL 以资产 URL 路径段的扩展名判定解包格式(忽略查询串);
// 扩展名未识别时回退条目级 archive 提示字段,仍无法判定则交由 extractArchive 报不支持。
func archiveFormatFromURL(u, hint string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	low := strings.ToLower(u)
	switch {
	case strings.HasSuffix(low, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(low, ".tar.bz2"):
		return "tar.bz2"
	case strings.HasSuffix(low, ".zip"):
		return "zip"
	}
	return hint
}

// runArchive 归档条目下载管线:单归档下载(复用逐文件管线,直链 + 可选 sha256)
// → sha256 校验(引擎强制,模型归档声明了哈希同样校验)→ 解包(模型到条目根
// models/<id>/,引擎到 engines/<id>/pkg/)→ (引擎)定位二进制 → manifest → installed。
func (m *Manager) runArchive(ctx context.Context, e Entry) {
	dir := m.modelDir(e.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.setFailed(e.ID, "创建目录失败: "+err.Error())
		return
	}
	url, _, wantSHA, format, err := pickAssetURL(e)
	if err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	// 归档文件名取 URL 路径段(忽略查询串);无有效文件名无法落盘,前置报错
	u := url
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	archiveName := path.Base(u)
	if archiveName == "" || archiveName == "/" || archiveName == "." {
		m.setFailed(e.ID, "归档 URL 无文件名: "+url)
		return
	}
	// 复用逐文件管线下载归档本体:构造单文件直链视图(直链 + 可选 sha256)
	single := e
	single.Files = []string{archiveName}
	single.FileURLs = map[string]string{archiveName: url}
	if wantSHA != "" {
		single.SHA256 = map[string]string{archiveName: wantSHA}
	}
	sz, canRange, err := m.probe(ctx, single, archiveName)
	if err != nil {
		m.finish(ctx, e.ID, err)
		return
	}
	_ = canRange
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
	// 解包目标分流(控制器裁定):模型归档解到条目根 models/<id>/(白名单扁平化落根,
	// 如 models/sensevoice-int8/model.int8.onnx);引擎发布包自包含 rpath 布局不可挪,
	// 整体解到 engines/<id>/pkg/。重装清理:引擎清 pkg(归档本体在上级目录不受影响);
	// 模型条目 dest=dir 且归档同在 dir,不能整目录删(会连同归档删掉),白名单成员靠解包覆盖写。
	dest := dir
	if e.Kind == "engine" {
		dest = filepath.Join(dir, "pkg")
		if err := os.RemoveAll(dest); err != nil {
			m.setFailed(e.ID, err.Error())
			return
		}
	}
	if err := extractArchive(format, archivePath, dest, e.ExtractFiles); err != nil {
		m.setFailed(e.ID, "解包失败: "+err.Error())
		return
	}
	if e.Kind != "engine" {
		// 白名单成员必须全部在位(引擎由 findBinaries 强制):模型归档在写 manifest
		// 前逐个核对,缺任一即失败且不产出 manifest。校验放在删除归档之前——
		// 重试可直接复用盘上归档,不必整包重下。
		for _, f := range e.ExtractFiles {
			if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f))); err != nil {
				m.setFailed(e.ID, "解包不完整:缺少 "+f+"(归档可能损坏),请删除后重试")
				return
			}
		}
	}
	if err := os.Remove(archivePath); err != nil {
		m.setFailed(e.ID, err.Error())
		return
	}
	mf := manifest{ID: e.ID, Repo: e.Repo, Revision: e.Revision, CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	if e.Kind == "engine" {
		if len(e.Binaries) == 0 { // 校验本应拦截,防御 findBinaries 空名单取首元素 panic
			m.setFailed(e.ID, "引擎条目未声明 binaries")
			return
		}
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
		mf.Binary = filepath.ToSlash(rel)
	}
	// 记录解包产物(盘面走一遍:manifest Files 相对路径 + installed 字节数)
	var total int64
	_ = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		total += fi.Size()
		mf.Files = append(mf.Files, manifestFile{Path: filepath.ToSlash(rel), Size: fi.Size()})
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
	if m.active == e.ID { // installed 终态与 active 清除同临界区(与 run 一致)
		m.active, m.cancel = "", nil
	}
	m.mu.Unlock()
}

// contentRangeStart 解析 Content-Range: bytes S-E/N 头,返回起始偏移 S 与总大小 N;
// 缺失/畸形(start 或 total 非数字、start 为负、total 非正)返回 false。
// fetchOne 的 206 起始偏移校验与 probe 的总长解析(contentRangeTotal)共用。
func contentRangeStart(cr string) (start, total int64, ok bool) {
	const prefix = "bytes "
	if !strings.HasPrefix(cr, prefix) {
		return 0, 0, false
	}
	i := strings.LastIndexByte(cr, '/')
	if i < 0 {
		return 0, 0, false
	}
	head := cr[len(prefix):i] // S-E
	dash := strings.IndexByte(head, '-')
	if dash < 0 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(head[:dash], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	total, err = strconv.ParseInt(cr[i+1:], 10, 64)
	if err != nil || total <= 0 {
		return 0, 0, false
	}
	return start, total, true
}

// contentRangeTotal 解析 Content-Range: bytes S-E/N 头,返回总大小 N;缺失/畸形/非正返回 false。
// probe 的 206 路径与「200 怪癖」路径共用(魔搭对非 LFS 小文件的探测回 200 却带真实大小的该头)。
func contentRangeTotal(cr string) (int64, bool) {
	_, total, ok := contentRangeStart(cr)
	return total, ok
}

// probe 用 Range: bytes=0-0 探测单文件:取真实大小与 Range 支持,同时前置发现 404/下架。
func (m *Manager) probe(ctx context.Context, e Entry, file string) (int64, bool, error) {
	fileURL := m.fileURLFor(e, file)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
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
		if e.Repo == "" {
			// 直链条目(Repo 为空)没有 repo 段可渲染:直述失败的具体文件地址
			return 0, false, fmt.Errorf("模型文件不存在或已下架:%s(可打开 %s 确认)", fileURL, e.LicenseURL)
		}
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

// regetFromZero 丢弃当前响应(排水后关闭),从零重发不带 Range 的普通 GET。
// 续传被降级(非 206)与 206 起始偏移不符共用此降级路径;调用方负责把 offset 归零。
func (m *Manager) regetFromZero(ctx context.Context, e Entry, file string, resp *http.Response) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.fileURLFor(e, file), nil)
	if err != nil {
		return nil, err
	}
	r, err := downloadClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络错误: %w", err)
	}
	return r, nil
}

// fetchOne 单文件下载:全部写入 .part,完成后按声明校验字节数(+可选 sha256)再原子改名。
// 最终文件已存在且大小吻合 → 跳过(跨重启续传);.part 大于远端(远端变更)→ 废弃重下;
// Range 不可用/续传被降级(200)/206 起始偏移不符 → 从零重写。
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.fileURLFor(e, file), nil)
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
		degrade := false
		if resp.StatusCode == http.StatusPartialContent {
			// 206 必须核对起始偏移与总长:损坏代理可能回起点错误的 206,事后字节数校验
			// (offset+=downloaded)挡不住「长度恰好、起点错位」的内容入库安装。
			start, total, ok := contentRangeStart(resp.Header.Get("Content-Range"))
			degrade = !ok || start != offset || total != size
		} else {
			degrade = offset > 0 // 续传被降级(200 等):从零重写
		}
		if degrade {
			// 从零重写:drain 后重发普通 GET
			resp, err = m.regetFromZero(ctx, e, file, resp)
			offset = 0
			if err != nil {
				return err
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
