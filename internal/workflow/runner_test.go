package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

// fakeTool 可配置的测试工具：固定输出 kind 的单产物（失败/延迟可配），
// 产物文件落在 runner 的 dataDir 下（相对路径，与真实工具同契约），并记录收到的 Files。
type fakeTool struct {
	name    string
	outKind string
	fail    bool
	delay   time.Duration
	data    string // runner 的 dataDir，产物相对路径基准
	files   map[string]string
}

func (f *fakeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: f.name, Title: f.name, Group: "测试"}
}

func (f *fakeTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{{Key: "text", Label: "文本", Type: provider.ParamText, Required: false}}
}

func (f *fakeTool) Run(ctx context.Context, in provider.TaskInput, _ provider.ProgressReporter) (provider.TaskOutput, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return provider.TaskOutput{}, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.fail {
		return provider.TaskOutput{}, fmt.Errorf("fake 失败")
	}
	f.files = in.Files
	outDir := filepath.Join(f.data, "fake-out")
	_ = os.MkdirAll(outDir, 0o755)
	abs := filepath.Join(outDir, f.name+"-"+uuid.NewString()[:8]+".txt")
	if err := os.WriteFile(abs, []byte("out"), 0o644); err != nil {
		return provider.TaskOutput{}, err
	}
	rel, err := filepath.Rel(f.data, abs)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	return provider.TaskOutput{Artifacts: []provider.Artifact{{
		Kind: f.outKind, Path: rel, Format: "txt", Size: 3,
	}}}, nil
}

// registerFake 注册 fake 工具 + 端口表项（测试结束还原端口表）。
func registerFake(t *testing.T, reg *provider.Registry, dataDir, name, outKind string) *fakeTool {
	t.Helper()
	ft := &fakeTool{name: name, outKind: outKind, data: dataDir}
	if err := reg.Register(ft); err != nil {
		t.Fatal(err)
	}
	prev, had := ports["fake."+name]
	ports["fake."+name] = ToolSpec{
		In:       []InPort{{Key: "audio", Kind: "audio", Label: "音频"}},
		OutKinds: []string{outKind}, Chainable: true,
	}
	t.Cleanup(func() {
		if had {
			ports["fake."+name] = prev
		} else {
			delete(ports, "fake."+name)
		}
	})
	return ft
}

type runnerFixture struct {
	runner  *Runner
	reg     *provider.Registry
	dataDir string
}

func newRunnerFixture(t *testing.T) runnerFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	eng := task.New(db, reg, dir, 2, nil)
	return runnerFixture{runner: NewRunner(db, eng, reg, dir), reg: reg, dataDir: dir}
}

// waitRun 轮询 run 终态。
func (f runnerFixture) waitRun(t *testing.T, id uint) *store.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := f.runner.db.GetWorkflowRunByID(id)
		if err != nil {
			t.Fatalf("查 run 失败: %v", err)
		}
		switch r.Status {
		case RunDone, RunFailed, RunCanceled:
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run 超时未达终态")
	return nil
}

func TestRunnerLinearChain(t *testing.T) {
	f := newRunnerFixture(t)
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	b := registerFake(t, f.reg, f.dataDir, "b", "audio")
	g := &Graph{Nodes: []Node{
		{Key: "n1", Provider: "fake", Tool: "a", Params: map[string]any{"text": "hello"}},
		{Key: "n2", Provider: "fake", Tool: "b", InputsFrom: map[string]string{"audio": "n1"}},
	}}
	run, err := f.runner.Start("链", g, nil, "user-1", 0, "tpl")
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunDone {
		t.Fatalf("run 应 done，得到 %s（%s）", done.Status, done.Error)
	}
	nodes := mustNodeRuns(done.Nodes)
	if len(nodes) != 2 || nodes[0].Status != NodeDone || nodes[1].Status != NodeDone {
		t.Fatalf("两节点应全 done: %+v", nodes)
	}
	if len(nodes[0].ArtifactIDs) != 1 || len(nodes[1].ArtifactIDs) != 1 {
		t.Fatalf("每节点应产出 1 个产物: %+v", nodes)
	}
	// 产物传递：b 收到的 Files["audio"] 是 a 的产物文件
	if b.files == nil || b.files["audio"] == "" {
		t.Fatalf("b 未收到上游产物文件: %v", b.files)
	}
	if _, err := os.Stat(b.files["audio"]); err != nil {
		t.Errorf("上游产物文件不存在: %v", err)
	}
}

func TestRunnerFanout(t *testing.T) {
	f := newRunnerFixture(t)
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	registerFake(t, f.reg, f.dataDir, "b", "audio")
	registerFake(t, f.reg, f.dataDir, "c", "transcript")
	g := &Graph{Nodes: []Node{
		{Key: "src", Provider: "fake", Tool: "a"},
		{Key: "x", Provider: "fake", Tool: "b", InputsFrom: map[string]string{"audio": "src"}},
		{Key: "y", Provider: "fake", Tool: "c", InputsFrom: map[string]string{"audio": "src"}},
	}}
	run, err := f.runner.Start("扇出", g, nil, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunDone {
		t.Fatalf("扇出 run 应 done: %s %s", done.Status, done.Error)
	}
	nodes := mustNodeRuns(done.Nodes)
	for _, n := range nodes {
		if n.Status != NodeDone {
			t.Errorf("节点 %s 应 done: %s %s", n.Key, n.Status, n.Error)
		}
	}
}

func TestRunnerFailureSkipsDownstream(t *testing.T) {
	f := newRunnerFixture(t)
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	fb := registerFake(t, f.reg, f.dataDir, "b", "audio")
	fb.fail = true
	registerFake(t, f.reg, f.dataDir, "c", "audio")
	g := &Graph{Nodes: []Node{
		{Key: "n1", Provider: "fake", Tool: "a"},
		{Key: "n2", Provider: "fake", Tool: "b", InputsFrom: map[string]string{"audio": "n1"}},
		{Key: "n3", Provider: "fake", Tool: "c", InputsFrom: map[string]string{"audio": "n2"}},
	}}
	run, err := f.runner.Start("失败链", g, nil, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunFailed {
		t.Fatalf("run 应 failed，得到 %s", done.Status)
	}
	st := map[string]string{}
	for _, n := range mustNodeRuns(done.Nodes) {
		st[n.Key] = n.Status
	}
	if st["n1"] != NodeDone || st["n2"] != NodeFailed || st["n3"] != NodeSkipped {
		t.Fatalf("状态传播不对: %v", st)
	}
	if done.Error == "" || !strings.Contains(done.Error, "n2") {
		t.Errorf("错误信息应含失败节点: %q", done.Error)
	}
}

func TestRunnerCancel(t *testing.T) {
	f := newRunnerFixture(t)
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	slow := registerFake(t, f.reg, f.dataDir, "b", "audio")
	slow.delay = 3 * time.Second
	g := &Graph{Nodes: []Node{
		{Key: "n1", Provider: "fake", Tool: "a"},
		{Key: "n2", Provider: "fake", Tool: "b", InputsFrom: map[string]string{"audio": "n1"}},
	}}
	run, err := f.runner.Start("取消", g, nil, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	// 等首节点完成、慢节点已提交运行（NodeRun 提交时才有 task_id 字段）再取消
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, _ := f.runner.db.GetWorkflowRunByID(run.ID)
		if strings.Contains(r.Nodes, `"key":"n2","task_id"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等第二节点超时")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := f.runner.Cancel(run.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunCanceled {
		t.Fatalf("run 应 canceled，得到 %s", done.Status)
	}
	for _, n := range mustNodeRuns(done.Nodes) {
		if n.Key == "n2" && n.Status != NodeCanceled && n.Status != NodeSkipped {
			t.Errorf("慢节点应 canceled/skipped，得到 %s", n.Status)
		}
	}
}

func TestRunnerFileSlot(t *testing.T) {
	f := newRunnerFixture(t)
	ft := registerFake(t, f.reg, f.dataDir, "a", "audio")
	// 伪造上传文件：<dataDir>/uploads/<user>/<uuid>.wav
	uid := uuid.NewString()
	upDir := filepath.Join(f.dataDir, "uploads", "user-9")
	if err := os.MkdirAll(upDir, 0o755); err != nil {
		t.Fatal(err)
	}
	uploaded := filepath.Join(upDir, uid+".wav")
	if err := os.WriteFile(uploaded, []byte("wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &Graph{
		Inputs: []InputSlot{{Key: "voice", Label: "人声", Type: "file", Required: true}},
		Nodes:  []Node{{Key: "n1", Provider: "fake", Tool: "a", InputsFrom: map[string]string{"audio": "inputs.voice"}}},
	}
	run, err := f.runner.Start("文件槽", g, map[string]any{"voice": uid}, "user-9", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunDone {
		t.Fatalf("file 槽 run 应 done: %s %s", done.Status, done.Error)
	}
	if ft.files == nil || ft.files["audio"] != uploaded {
		t.Errorf("应收到上传文件路径: %v", ft.files)
	}
}

func TestRunnerTrackSelect(t *testing.T) {
	f := newRunnerFixture(t)
	// 上游一次产两轨（voice/background），下游按 track 选择
	src := registerFake(t, f.reg, f.dataDir, "src", "audio")
	registerFake(t, f.reg, f.dataDir, "down", "audio")
	// 重写 Run 产双产物：包一层（注册同 key 覆盖，端口表无须变动）
	dual := &dualOutTool{fakeTool: src}
	f.reg.Replace(dual)
	g := &Graph{Nodes: []Node{
		{Key: "s", Provider: "fake", Tool: "src"},
		{Key: "v", Provider: "fake", Tool: "down", InputsFrom: map[string]string{"audio": "s:voice"}},
		{Key: "bgm", Provider: "fake", Tool: "down", InputsFrom: map[string]string{"audio": "s:background"}},
	}}
	run, err := f.runner.Start("轨道", g, nil, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunDone {
		t.Fatalf("轨道选择 run 应 done: %s %s", done.Status, done.Error)
	}
	if dual.calls == 0 {
		t.Error("双产物工具未被调用")
	}
}

// dualOutTool 双产物包装（voice/background 两轨，与分离工具同契约）。
type dualOutTool struct {
	*fakeTool
	calls int
}

func (d *dualOutTool) Run(ctx context.Context, in provider.TaskInput, r provider.ProgressReporter) (provider.TaskOutput, error) {
	d.calls++
	out, err := d.fakeTool.Run(ctx, in, r)
	if err != nil {
		return out, err
	}
	out.Artifacts[0].Meta = map[string]any{"track": "voice"}
	background := out.Artifacts[0]
	background.Meta = map[string]any{"track": "background"}
	return provider.TaskOutput{Artifacts: []provider.Artifact{out.Artifacts[0], background}}, nil
}

func TestRunnerMissingUpstreamArtifact(t *testing.T) {
	f := newRunnerFixture(t)
	// 上游静态声明可能出 audio/transcript，但实际只产 transcript——
	// 校验期（静态交集）放行，运行期按实际产物挑 kind 失败
	registerFake(t, f.reg, f.dataDir, "t", "transcript")
	ports["fake.t"] = ToolSpec{In: []InPort{}, OutKinds: []string{"audio", "transcript"}, Chainable: true}
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	g := &Graph{Nodes: []Node{
		{Key: "s", Provider: "fake", Tool: "t"},
		{Key: "d", Provider: "fake", Tool: "a", InputsFrom: map[string]string{"audio": "s"}},
	}}
	run, err := f.runner.Start("kind不符", g, nil, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	done := f.waitRun(t, run.ID)
	if done.Status != RunFailed || !strings.Contains(done.Error, "audio") {
		t.Fatalf("应因缺 audio 产物失败: %s %q", done.Status, done.Error)
	}
}

func TestRunnerInputRefForRerun(t *testing.T) {
	f := newRunnerFixture(t)
	registerFake(t, f.reg, f.dataDir, "a", "audio")
	registerFake(t, f.reg, f.dataDir, "b", "audio")
	g := &Graph{Nodes: []Node{
		{Key: "n1", Provider: "fake", Tool: "a"},
		{Key: "n2", Provider: "fake", Tool: "b", InputsFrom: map[string]string{"audio": "n1"}},
	}}
	run, _ := f.runner.Start("溯源", g, nil, "u1", 0, "")
	f.waitRun(t, run.ID)
	tasks, _, err := f.runner.db.ListTasks("fake", "", nil, 10, 0, "u1")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("应有两个节点任务: %v %v", err, len(tasks))
	}
	// 末节点（最近创建）的 Input 应携带上游产物 id（重跑溯源链）
	if tasks[0].Input == "" {
		t.Fatal("下游节点任务应落 InputRef")
	}
	var ref task.InputRef
	if err := json.Unmarshal([]byte(tasks[0].Input), &ref); err != nil {
		t.Fatalf("InputRef 解析失败: %v", err)
	}
	if len(ref.ArtifactInputs) != 1 || ref.ArtifactInputs[0] == "" {
		t.Fatalf("下游节点应引用上游产物 id: %+v", ref)
	}
}
