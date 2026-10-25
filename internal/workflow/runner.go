package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

// Runner 服务端工作流执行器：入度驱动的 DAG 调度，逐节点向任务引擎同步提交。
// 并发 run 有上限（防编排批量拉起工具把引擎占满）；run goroutine 是
// run 记录的唯一写者（取消路径经 context 级联，不并发写）。
type Runner struct {
	db      *store.DB
	engine  *task.Engine
	reg     *provider.Registry
	dataDir string

	sem     chan struct{}
	mu      sync.Mutex
	cancels map[uint]context.CancelFunc
}

// MaxConcurrentRuns 同时执行的工作流上限：节点任务本身已受引擎并发约束，
// 这里挡的是编排层批量排队。
const MaxConcurrentRuns = 3

func NewRunner(db *store.DB, engine *task.Engine, reg *provider.Registry, dataDir string) *Runner {
	return &Runner{
		db: db, engine: engine, reg: reg, dataDir: dataDir,
		sem:     make(chan struct{}, MaxConcurrentRuns),
		cancels: map[uint]context.CancelFunc{},
	}
}

// Start 校验并启动一次执行：落 run 记录（pending）后异步推进，立即返回。
// wfID/wfKey 溯源来源（用户编排或内置模板）。
func (r *Runner) Start(wfName string, g *Graph, inputs map[string]any, userID string, wfID uint, wfKey string) (*store.WorkflowRun, error) {
	if err := g.ValidateWithTools(r.reg); err != nil {
		return nil, err
	}
	expanded, fileInputs, err := g.Expand(inputs)
	if err != nil {
		return nil, err
	}
	nodeRuns := make([]NodeRun, len(g.Nodes))
	for i, n := range g.Nodes {
		nodeRuns[i] = NodeRun{Key: n.Key, Status: NodePending}
	}
	nodesJSON, _ := json.Marshal(nodeRuns)
	inputsJSON, _ := json.Marshal(inputs)
	graphJSON, _ := json.Marshal(g)
	run := &store.WorkflowRun{
		UserID: userID, WorkflowID: wfID, WorkflowKey: wfKey,
		Name: wfName, Status: RunPending,
		Inputs: string(inputsJSON), Graph: string(graphJSON), Nodes: string(nodesJSON),
	}
	if err := r.db.CreateWorkflowRun(run); err != nil {
		return nil, err
	}
	select {
	case r.sem <- struct{}{}:
	default:
		return nil, fmt.Errorf("已有 %d 个工作流在执行，请等待完成后再试", MaxConcurrentRuns)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancels[run.ID] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.cancels, run.ID)
			r.mu.Unlock()
			<-r.sem
		}()
		r.execute(ctx, run, g, expanded, fileInputs)
	}()
	return run, nil
}

// Cancel 取消执行中的 run：context 级联取消当前节点任务（节点任务自身也发
// canceled 事件，历史页状态一致）。run 不在执行中时报错。
func (r *Runner) Cancel(runID uint) error {
	r.mu.Lock()
	cancel, ok := r.cancels[runID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("工作流不在执行中")
	}
	cancel()
	return nil
}

// execute 入度调度主循环：ready 队列（入度 0 的节点）逐个跑完再补新 ready。
// 顺序调度（不并发跑同层节点）——产物传递与 run 更新因此单线程无竞态，
// 工具级并发由引擎 sem 统一管控。任一节点失败即终止（后续标 skipped）。
func (r *Runner) execute(ctx context.Context, run *store.WorkflowRun, g *Graph, expanded map[string]map[string]any, fileInputs map[string]string) {
	run.Status = RunRunning
	_ = r.db.UpdateWorkflowRun(run, false)

	byKey := map[string]*Node{}
	for i := range g.Nodes {
		byKey[g.Nodes[i].Key] = &g.Nodes[i]
	}
	// 入度与逆邻接（扇出：同一上游喂多个下游）；file 输入槽引用不是节点边
	indeg := map[string]int{}
	fanout := map[string][]string{}
	for _, n := range g.Nodes {
		deps := map[string]bool{} // 同一上游被多端口引用只计一次入度
		for _, ref := range n.InputsFrom {
			if strings.HasPrefix(ref, "inputs.") {
				continue
			}
			up, err := ParseUpstreamRef(ref)
			if err != nil {
				continue
			}
			deps[up.Node] = true
		}
		indeg[n.Key] = len(deps)
		for k := range deps {
			fanout[k] = append(fanout[k], n.Key)
		}
	}
	var ready []string
	for _, n := range g.Nodes {
		if indeg[n.Key] == 0 {
			ready = append(ready, n.Key)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] }) // 稳定顺序

	nodeRuns := mustNodeRuns(run.Nodes)
	nodeStatus := map[string]string{}
	for i := range nodeRuns {
		nodeStatus[nodeRuns[i].Key] = nodeRuns[i].Status
	}
	outputs := map[string][]store.Artifact{} // 节点产物（内存直传下游，路径/归属都在手）
	var runErr string

	for len(ready) > 0 {
		if ctx.Err() != nil {
			runErr = "已取消"
			break
		}
		key := ready[0]
		ready = ready[1:]
		node := byKey[key]
		idx := indexByKey(nodeRuns, key)
		nodeRuns[idx].Status = NodeRunning
		run.Nodes = marshalNodeRuns(nodeRuns)
		_ = r.db.UpdateWorkflowRun(run, false)

		taskID, err := r.submitNode(run.UserID, node, expanded[key], fileInputs, outputs)
		if err == nil {
			// 提交即落 task_id：前端凭它把 WS 任务事件映射到画布节点实时上色
			nodeRuns[idx].TaskID = taskID
			run.Nodes = marshalNodeRuns(nodeRuns)
			_ = r.db.UpdateWorkflowRun(run, false)
		}
		var arts []store.Artifact
		if err == nil {
			arts, err = r.waitNode(ctx, taskID)
		}
		if err != nil {
			if ctx.Err() != nil {
				nodeRuns[idx].Status = NodeCanceled
				run.Status = RunCanceled
				runErr = "已取消"
			} else {
				nodeRuns[idx].Status = NodeFailed
				nodeRuns[idx].Error = err.Error()
				run.Status = RunFailed
				runErr = fmt.Sprintf("节点 %s 失败: %s", key, err.Error())
			}
			break
		}
		outputs[key] = arts
		nodeRuns[idx].Status = NodeDone
		nodeRuns[idx].ArtifactIDs = artifactIDs(arts)
		for _, next := range fanout[key] {
			indeg[next]--
			if indeg[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	if runErr == "" && len(ready) == 0 && ctx.Err() != nil {
		run.Status = RunCanceled
		runErr = "已取消"
	}
	// 失败/取消后未跑到的节点标 skipped（入度不再传播）
	if run.Status == RunFailed || run.Status == RunCanceled {
		for i := range nodeRuns {
			if nodeRuns[i].Status == NodePending || nodeRuns[i].Status == NodeRunning {
				nodeRuns[i].Status = NodeSkipped
			}
		}
	}
	run.Nodes = marshalNodeRuns(nodeRuns)
	run.Error = runErr
	if run.Status != RunFailed && run.Status != RunCanceled {
		run.Status = RunDone
	}
	_ = r.db.UpdateWorkflowRun(run, false)
}

// submitNode 校验端口/拼装输入并异步提交节点任务，立即返回 task id——
// 前端凭它把 WS 进度事件映射到画布节点（终态前就要可见）。
// InputsFrom 值两种来源："节点key[:select]"（上游产物）或 "inputs.<槽key>"
// （file 型输入槽，前端 /api/uploads 上传后的 file_id）。
func (r *Runner) submitNode(userID string, n *Node, params map[string]any, fileInputs map[string]string, outputs map[string][]store.Artifact) (string, error) {
	spec := SpecOf(n.Provider, n.Tool)
	if spec == nil {
		return "", fmt.Errorf("工具 %s.%s 暂不支持编排", n.Provider, n.Tool)
	}
	files := map[string]string{}
	ref := &task.InputRef{}
	for port, refStr := range n.InputsFrom {
		inPort, ok := spec.PortOf(port)
		if !ok {
			return "", fmt.Errorf("端口 %s 不存在", port)
		}
		if slotKey, isSlot := strings.CutPrefix(refStr, "inputs."); isSlot {
			fileID := fileInputs[slotKey]
			if fileID == "" {
				return "", fmt.Errorf("输入槽 %s 未提供文件", slotKey)
			}
			mapped, err := r.uploadFiles(userID, []string{fileID})
			if err != nil {
				return "", err
			}
			files[port] = mapped["audio"]
			ref.FileIDs = append(ref.FileIDs, fileID)
			continue
		}
		up, err := ParseUpstreamRef(refStr)
		if err != nil {
			return "", err
		}
		art, err := pickArtifact(outputs[up.Node], inPort.Kind, up.Select)
		if err != nil {
			return "", err
		}
		abs, err := r.artifactAbs(art)
		if err != nil {
			return "", fmt.Errorf("上游产物 %s 文件缺失: %w", art.ID, err)
		}
		files[port] = abs
		ref.ArtifactInputs = append(ref.ArtifactInputs, art.ID)
	}
	p := cloneParams(params)
	delete(p, "_out")
	return r.engine.SubmitUserRef(userID, n.Provider, n.Tool, p, files, ref)
}

// waitNode 轮询节点任务终态（同步编排语义保持，任务状态由引擎 goroutine 落库）。
// ctx 取消时对节点任务发起 Cancel 再等其落 canceled 终态。
func (r *Runner) waitNode(ctx context.Context, taskID string) ([]store.Artifact, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = r.engine.Cancel(taskID) // 引擎把取消落为 canceled 终态，下一轮读到
		case <-ticker.C:
		}
		t, err := r.db.GetTask(taskID)
		if err != nil {
			return nil, fmt.Errorf("节点任务状态读取失败: %w", err)
		}
		switch t.Status {
		case store.StatusSucceeded:
			arts, err := r.db.ListArtifacts(taskID)
			if err != nil {
				return nil, fmt.Errorf("节点产物读取失败: %w", err)
			}
			return arts, nil
		case store.StatusFailed:
			return nil, fmt.Errorf("%s", t.Error)
		case store.StatusCanceled, store.StatusInterrupted:
			return nil, context.Canceled
		}
	}
}

// pickArtifact 端口产物选择：select 空=第一个 kind 匹配；"N"=第 N 个产物
// （1 起，不筛 kind）；"voice"/"background"=按产物 meta.track 匹配（分离链）。
func pickArtifact(arts []store.Artifact, kind, selectFlag string) (store.Artifact, error) {
	if len(arts) == 0 {
		return store.Artifact{}, fmt.Errorf("上游没有可用产物")
	}
	switch {
	case selectFlag == "":
		for _, a := range arts {
			if acceptsKind(kind, a.Kind) {
				return a, nil
			}
		}
		return store.Artifact{}, fmt.Errorf("上游没有 %s 类产物", kind)
	case isNumeric(selectFlag):
		idx := 0
		fmt.Sscanf(selectFlag, "%d", &idx)
		if idx < 1 || idx > len(arts) {
			return store.Artifact{}, fmt.Errorf("产物序号 %s 超出范围（共 %d 个）", selectFlag, len(arts))
		}
		return arts[idx-1], nil
	default:
		for _, a := range arts {
			if trackOf(a) == selectFlag {
				return a, nil
			}
		}
		return store.Artifact{}, fmt.Errorf("上游没有轨道 %s 的产物", selectFlag)
	}
}

// trackOf 产物轨道标注（分离产物 meta.track=voice/background/music/sfx）。
func trackOf(a store.Artifact) string {
	var meta struct {
		Track string `json:"track"`
	}
	_ = json.Unmarshal([]byte(a.Meta), &meta)
	return meta.Track
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// artifactAbs 产物相对路径 → 绝对路径（data 目录 jail 防御，与 server 同口径；
// _out 契约的绝对路径产物原样放行）。
func (r *Runner) artifactAbs(a store.Artifact) (string, error) {
	if filepath.IsAbs(a.Path) {
		if _, err := os.Stat(a.Path); err != nil {
			return "", err
		}
		return a.Path, nil
	}
	abs := filepath.Join(r.dataDir, a.Path)
	root := filepath.Clean(r.dataDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(abs)+string(os.PathSeparator), root) {
		return "", fmt.Errorf("非法产物路径: %s", a.Path)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// uploadFiles 上传 file_id → 绝对路径（uploads 目录按 <uuid>.<ext> Glob，
// 与 server.fileIDsToFiles 同口径；目录范围=run 归属者分桶+部署前存量根目录）。
func (r *Runner) uploadFiles(userID string, fileIDs []string) (map[string]string, error) {
	dirs := []string{}
	if userID != "" {
		dirs = append(dirs, filepath.Join(r.dataDir, "uploads", userID))
	}
	dirs = append(dirs, filepath.Join(r.dataDir, "uploads"))
	files := map[string]string{}
	for i, id := range fileIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("非法 file_id: %s", id)
		}
		found := ""
		for _, dir := range dirs {
			// 与 server.fileIDsToFiles 同口径：新形态 <uuid>-原名.ext，旧存量 <uuid>.ext
			matches, _ := filepath.Glob(filepath.Join(dir, id+"-*"))
			if len(matches) == 0 {
				matches, _ = filepath.Glob(filepath.Join(dir, id+".*"))
			}
			if len(matches) > 0 {
				found = matches[0]
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("上传文件不存在: %s", id)
		}
		key := "audio"
		if i > 0 {
			key = fmt.Sprintf("audio%d", i+1)
		}
		files[key] = found
	}
	return files, nil
}

func cloneParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}

func artifactIDs(arts []store.Artifact) []string {
	ids := make([]string, 0, len(arts))
	for _, a := range arts {
		ids = append(ids, a.ID)
	}
	return ids
}

func indexByKey(runs []NodeRun, key string) int {
	for i := range runs {
		if runs[i].Key == key {
			return i
		}
	}
	return 0
}

func mustNodeRuns(s string) []NodeRun {
	var runs []NodeRun
	if err := json.Unmarshal([]byte(s), &runs); err != nil {
		return []NodeRun{}
	}
	return runs
}

func marshalNodeRuns(runs []NodeRun) string {
	b, _ := json.Marshal(runs)
	return string(b)
}
