// Package task 实现任务引擎：参数校验、并发执行、状态落库、事件通知。
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
)

type Event struct {
	Type      string              `json:"type"` // progress|done|error|canceled|task.snapshot
	TaskID    string              `json:"task_id"`
	Provider  string              `json:"provider,omitempty"` // 归属工具，前端全局通知/展示用
	Tool      string              `json:"tool,omitempty"`
	Progress  int                 `json:"progress"`
	Note      string              `json:"note"`
	Error     string              `json:"error"`
	Detail    map[string]any      `json:"detail,omitempty"` // 仅 progress 事件携带的工具自定义展示数据（如播客对话流轮次）
	Artifacts []provider.Artifact `json:"artifacts"`
}

type Engine struct {
	db      *store.DB
	reg     *provider.Registry
	dataDir string
	sem     chan struct{}
	notify  func(Event)
	mu      sync.Mutex
	cancels map[string]context.CancelFunc

	// storageMu 守护 storage 函数的替换（Web 保存存储配置热更新）：每个任务启动时
	// 调用一次取当前客户端，进行中任务不受后续替换影响。
	storageMu sync.RWMutex
	storage   func() provider.StorageClient
}

// SetStorageClient 注入对象存储客户端获取函数（nil 或返回 nil 表示未配置）。
// 供 Service 在启动与存储配置热更新时调用。
func (e *Engine) SetStorageClient(fn func() provider.StorageClient) {
	e.storageMu.Lock()
	e.storage = fn
	e.storageMu.Unlock()
}

// storageClient 当前对象存储客户端（未注入或返回 nil 均表示未配置）。
func (e *Engine) storageClient() provider.StorageClient {
	e.storageMu.RLock()
	defer e.storageMu.RUnlock()
	if e.storage == nil {
		return nil
	}
	return e.storage()
}

func New(db *store.DB, reg *provider.Registry, dataDir string, concurrency int, notify func(Event)) *Engine {
	if concurrency <= 0 {
		concurrency = 2
	}
	return &Engine{
		db: db, reg: reg, dataDir: dataDir,
		sem:     make(chan struct{}, concurrency),
		notify:  notify,
		cancels: map[string]context.CancelFunc{},
	}
}

// InputRef 记录 Web 提交边界的原始输入引用（file_ids/artifact_input/artifact_inputs
// 在进引擎前就被解析成本地路径，不落 Params）；重跑与前端回放依赖它溯源。CLI 直传本地路径，无需引用。
type InputRef struct {
	FileIDs        []string `json:"file_ids,omitempty"`
	ArtifactInput  string   `json:"artifact_input,omitempty"`
	ArtifactInputs []string `json:"artifact_inputs,omitempty"` // 多产物按序（mix：[伴奏id, 人声id]）
}

func (r *InputRef) empty() bool {
	return r == nil || (len(r.FileIDs) == 0 && r.ArtifactInput == "" && len(r.ArtifactInputs) == 0)
}

// emit 在引擎 goroutine 内同步调用 notify 回调。
// 契约：notify 由任务执行 goroutine 同步触发，订阅方必须非阻塞
// （使用带缓冲的 channel 并配合丢弃策略），不得在回调内做耗时操作或再回调引擎。
func (e *Engine) emit(ev Event) {
	if e.notify != nil {
		e.notify(ev)
	}
}

// validate 检查必填参数；返回错误消息包含缺失参数 key。
func validate(t provider.Tool, params map[string]any) error {
	for _, spec := range t.ParamSpecs() {
		if !spec.Required {
			continue
		}
		v, ok := params[spec.Key]
		if !ok || v == nil || fmt.Sprint(v) == "" {
			return fmt.Errorf("缺少必填参数: %s", spec.Key)
		}
	}
	return nil
}

func (e *Engine) createTask(userID, providerName, toolName string, params map[string]any, ref *InputRef, files map[string]string) (*store.Task, provider.Tool, error) {
	tool, ok := e.reg.Get(providerName, toolName)
	if !ok {
		return nil, nil, fmt.Errorf("未知工具: %s.%s", providerName, toolName)
	}
	if err := validate(tool, params); err != nil {
		return nil, nil, err
	}
	raw, _ := json.Marshal(params)
	title := deriveTitle(params, ref)
	if title == "" {
		title = uploadTitle(files)
	}
	t := &store.Task{
		ID: uuid.NewString(), UserID: userID, Provider: providerName, Tool: toolName,
		Status: store.StatusPending, Params: string(raw),
		Title: title,
	}
	if !ref.empty() {
		refRaw, _ := json.Marshal(ref)
		t.Input = string(refRaw)
	}
	if err := e.db.CreateTask(t); err != nil {
		return nil, nil, err
	}
	return t, tool, nil
}

// titleMaxRunes 标题截断长度：列表单行展示量级，超出以省略号收尾。
const titleMaxRunes = 60

// deriveTitle 提交时派生人类可读标题，供历史列表与搜索展示/命中：
// URL 取文件名段；文本参数取首行摘要；上传文件由 uploadTitle 从落盘路径恢复原名；
// 解析不出返回空串（前端回退工具名展示）。
func deriveTitle(params map[string]any, ref *InputRef) string {
	for _, key := range []string{"url", "input_url"} {
		if u := taskParamString(params, key); u != "" {
			u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
			if i := strings.IndexAny(u, "?#"); i >= 0 {
				u = u[:i]
			}
			return clipTitle(filepath.Base(u))
		}
	}
	for _, key := range []string{"text", "input_text"} {
		if s := taskParamString(params, key); s != "" {
			return clipTitle(strings.Join(strings.Fields(s), " "))
		}
	}
	if ref != nil && ref.ArtifactInput != "" {
		short := ref.ArtifactInput
		if len(short) > 8 {
			short = short[:8]
		}
		return "产物 " + short
	}
	return ""
}

func clipTitle(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) > titleMaxRunes {
		return string(runes[:titleMaxRunes]) + "…"
	}
	return string(runes)
}

// uploadTitle 从输入文件路径恢复人类可读标题（URL/音乐/文本都解析不出时的兜底）。
// 上传落盘名是 <uuid>-<净化原名><ext>（uploads.go 约定），剥掉 uuid 前缀与扩展名
// 即用户原名；分离产物（_instrumental 等轨道后缀）再加工（mix/转码）时回源歌名；
// CLI/MCP --file 等其余来源直接用文件名段（与 URL 通道取文件名一致）。
// 旧存量上传 <uuid>.<ext> 与裸 uuid 名没有可读原名，返回空串交回前端回退工具名。
func uploadTitle(files map[string]string) string {
	p := files["audio"] // 首输入文件约定键（fileIDsToFiles/orderedInputs 同一约定）
	if p == "" {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	if len(stem) > 37 {
		if _, err := uuid.Parse(stem[:36]); err == nil && stem[36] == '-' {
			stem = stem[37:]
		}
	}
	// 轨道后缀剥离：分离产物再加工（mix/转码）的标题回源歌名
	for _, suf := range []string{"_instrumental", "_background", "_vocals", "_voice"} {
		if strings.HasSuffix(stem, suf) {
			stem = strings.TrimSuffix(stem, suf)
			break
		}
	}
	if _, err := uuid.Parse(stem); err == nil {
		return ""
	}
	return clipTitle(stem)
}

func taskParamString(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := params[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func (e *Engine) run(ctx context.Context, t *store.Task, tool provider.Tool, params map[string]any, files map[string]string) (*store.Task, []store.Artifact, error) {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[t.ID] = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.cancels, t.ID)
		e.mu.Unlock()
	}()

	start := time.Now() // --json 契约要求 cost_ms 为任务总耗时

	t.Status = store.StatusRunning
	_ = e.db.UpdateTask(t)
	e.emit(Event{Type: "progress", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool, Progress: 0, Note: "任务开始"})

	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	report := func(progress int, note string, detail map[string]any) {
		t.Progress = progress
		t.ProgressNote = note
		_ = e.db.UpdateTask(t)
		// detail 可能为 nil（多数工具不传），omitempty 保证 JSON 输出向后兼容。
		e.emit(Event{Type: "progress", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool, Progress: progress, Note: note, Detail: detail})
	}

	out, runErr := tool.Run(ctx, provider.TaskInput{Params: params, Files: files, Storage: e.storageClient()}, report)

	var saved []store.Artifact
	for _, a := range out.Artifacts {
		raw, _ := json.Marshal(a.Meta)
		sa := store.Artifact{
			ID: uuid.NewString(), TaskID: t.ID, UserID: t.UserID, Kind: a.Kind, Path: a.Path,
			Filename: filepath.Base(a.Path), Format: a.Format,
			Size: a.Size, DurationMS: a.DurationMS, Meta: string(raw),
		}
		if err := e.db.CreateArtifact(&sa); err != nil {
			// 产物落库失败也必须进入终态，否则任务会永久停留在 running 且不发终态事件。
			t.Status = store.StatusFailed
			t.Error = fmt.Sprintf("保存产物失败: %v", err)
			t.CostMS = time.Since(start).Milliseconds()
			_ = e.db.UpdateTask(t)
			e.emit(Event{Type: "error", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool, Error: t.Error})
			return t, saved, fmt.Errorf("保存产物失败: %w", err)
		}
		saved = append(saved, sa)
	}

	var ev Event
	switch {
	case runErr == nil:
		t.Status = store.StatusSucceeded
		t.Progress = 100
		if out.Summary != nil {
			raw, _ := json.Marshal(out.Summary)
			t.Summary = string(raw)
		}
		ev = Event{Type: "done", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool, Progress: 100, Artifacts: out.Artifacts}
	case errors.Is(runErr, context.Canceled):
		t.Status = store.StatusCanceled
		ev = Event{Type: "canceled", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool}
	default:
		t.Status = store.StatusFailed
		t.Error = runErr.Error()
		ev = Event{Type: "error", TaskID: t.ID, Provider: t.Provider, Tool: t.Tool, Error: runErr.Error()}
	}
	// 先落库终态，再发终态事件：订阅方收到事件时 DB 状态已就绪。
	// 任务开始处的「先 UpdateTask 再 emit」与本处顺序保持一致。
	t.CostMS = time.Since(start).Milliseconds()
	_ = e.db.UpdateTask(t)
	e.emit(ev)
	return t, saved, runErr
}

func (e *Engine) Submit(providerName, toolName string, params map[string]any, files map[string]string) (string, error) {
	return e.SubmitRef(providerName, toolName, params, files, nil)
}

// SubmitRef 与 Submit 等价，额外把原始输入引用落库（Web 提交边界使用）。
func (e *Engine) SubmitRef(providerName, toolName string, params map[string]any, files map[string]string, ref *InputRef) (string, error) {
	return e.SubmitUserRef("", providerName, toolName, params, files, ref)
}

// SubmitUserRef 与 SubmitRef 等价，任务归属指定用户（公网多用户隔离；空=本地/CLI 无主，
// 无主任务仅 admin 可见）。
func (e *Engine) SubmitUserRef(userID, providerName, toolName string, params map[string]any, files map[string]string, ref *InputRef) (string, error) {
	t, tool, err := e.createTask(userID, providerName, toolName, params, ref, files)
	if err != nil {
		return "", err
	}
	go func() { _, _, _ = e.run(context.Background(), t, tool, params, files) }()
	return t.ID, nil
}

func (e *Engine) SubmitSync(ctx context.Context, providerName, toolName string, params map[string]any, files map[string]string) (*store.Task, []store.Artifact, error) {
	t, tool, err := e.createTask("", providerName, toolName, params, nil, files)
	if err != nil {
		return nil, nil, err
	}
	return e.run(ctx, t, tool, params, files)
}

func (e *Engine) Cancel(id string) error {
	e.mu.Lock()
	cancel, ok := e.cancels[id]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("任务不在运行中: %s", id)
	}
	cancel()
	return nil
}
