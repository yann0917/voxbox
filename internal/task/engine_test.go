package task

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
)

type echoTool struct{ fail bool }

func (echoTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: "echo", Title: "Echo"}
}
func (echoTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{{Key: "text", Label: "文本", Type: provider.ParamText, Required: true}}
}
func (t echoTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	report(10, "开始", nil)
	if t.fail {
		return provider.TaskOutput{}, errorFail{}
	}
	select {
	case <-ctx.Done():
		return provider.TaskOutput{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	report(100, "完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{Kind: "audio", Path: "echo/a.mp3", Format: "mp3", Size: 3}},
		Summary:   map[string]any{"text": in.Params["text"]},
	}, nil
}

type errorFail struct{}

func (errorFail) Error() string { return "boom" }

// detailTool 上报带 detail 的进度，用于验证引擎透传（如播客对话流轮次）。
type detailTool struct{}

func (detailTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: "detail", Title: "Detail"}
}
func (detailTool) ParamSpecs() []provider.ParamSpec { return nil }
func (detailTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	report(30, "第 1 轮", map[string]any{"round_id": 1, "text": "x"})
	return provider.TaskOutput{}, nil
}

func TestEventDetailPassthrough(t *testing.T) {
	var events []Event
	e := newTestEngine(t, detailTool{}, &events)
	if _, _, err := e.SubmitSync(context.Background(), "fake", "detail", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "progress" && ev.Detail["round_id"] == 1 && ev.Detail["text"] == "x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("progress 事件未透传 detail: %+v", events)
	}
	// 向后兼容：detail 为 nil 的事件（含终态）不应携带该字段。
	if last := events[len(events)-1]; last.Detail != nil {
		t.Errorf("终态事件不应携带 detail: %+v", last)
	}
}

func newTestEngine(t *testing.T, tool provider.Tool, events *[]Event) *Engine {
	t.Helper()
	db, err := OpenStore(t)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	return New(db, reg, t.TempDir(), 2, func(e Event) { *events = append(*events, e) })
}

func TestSubmitSyncSuccess(t *testing.T) {
	var events []Event
	e := newTestEngine(t, echoTool{}, &events)
	task, arts, err := e.SubmitSync(context.Background(), "fake", "echo",
		map[string]any{"text": "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != store.StatusSucceeded || len(arts) != 1 {
		t.Fatalf("status=%s arts=%d", task.Status, len(arts))
	}
	if arts[0].Kind != "audio" {
		t.Errorf("artifact kind = %s", arts[0].Kind)
	}
	// 计时覆盖整个 Run（echo 工具内含 10ms 延迟），取保守下限 5ms；
	// cost_ms 的真实端到端验证（--json 输出）在 Task 14 e2e 覆盖。
	if task.CostMS < 5 {
		t.Errorf("cost_ms = %d, want >= 5 (tool sleeps 10ms)", task.CostMS)
	}
	if len(events) == 0 || events[len(events)-1].Type != "done" {
		t.Errorf("last event = %+v", events)
	}
}

func TestSubmitSyncValidation(t *testing.T) {
	var events []Event
	e := newTestEngine(t, echoTool{}, &events)
	_, _, err := e.SubmitSync(context.Background(), "fake", "echo", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Errorf("err = %v, want missing required param text", err)
	}
	_, _, err = e.SubmitSync(context.Background(), "fake", "nope", map[string]any{}, nil)
	if err == nil {
		t.Error("unknown tool should fail")
	}
}

func TestSubmitSyncFailure(t *testing.T) {
	var events []Event
	e := newTestEngine(t, echoTool{fail: true}, &events)
	task, _, err := e.SubmitSync(context.Background(), "fake", "echo", map[string]any{"text": "x"}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if task.Status != store.StatusFailed || task.Error == "" {
		t.Fatalf("status=%s err=%q", task.Status, task.Error)
	}
}

// TestDeriveTitle 提交时派生人类可读标题：URL 取文件名段；文本取首行摘要；
// 解析不出为空（产物联动走 artifactTitle、上传文件名走 uploadTitle 兜底）。
func TestDeriveTitle(t *testing.T) {
	if got := deriveTitle(map[string]any{"url": "https://cdn.example.com/a/b/song file.mp3?sign=xyz"}); got != "song file.mp3" {
		t.Errorf("url title = %q", got)
	}
	if got := deriveTitle(map[string]any{"input_url": "https://example.com/x/track.flac"}); got != "track.flac" {
		t.Errorf("input_url title = %q", got)
	}
	if got := deriveTitle(map[string]any{"text": "  你好\n世界  "}); got != "你好 世界" {
		t.Errorf("text title = %q", got)
	}
	if got := deriveTitle(map[string]any{"scene": "Audio"}); got != "" {
		t.Errorf("unresolvable title = %q, want empty", got)
	}
	long := strings.Repeat("长", 70)
	got := deriveTitle(map[string]any{"text": long})
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 61 {
		t.Errorf("long title not clipped: %d runes", len([]rune(got)))
	}
}

// TestCreateTaskPersistsTitle 任务落库时标题随行（历史列表与搜索的数据来源）；
// 上传文件（file_ids 通道）在 URL/文本全解析不出时回退落盘路径里的用户原名。
func TestCreateTaskPersistsTitle(t *testing.T) {
	e := newTestEngine(t, echoTool{}, nil)
	tk, _, err := e.createTask("", "fake", "echo", map[string]any{"text": "  你好\n世界  "}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.db.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "你好 世界" {
		t.Errorf("task.Title = %q", got.Title)
	}

	// 人声分离页本地上传：params 无 url、ref 无音乐引用，标题从上传落盘名恢复。
	// echoTool 的必填 text 自身可派生标题，故用无参的 detailTool 走到兜底分支
	upload := map[string]string{
		"audio": "/data/uploads/user1/6ba7b810-9dad-11d1-80b4-00c04fd430c8-稻香 live.mp3",
	}
	e2 := newTestEngine(t, detailTool{}, nil)
	tk2, _, err := e2.createTask("", "fake", "detail", map[string]any{}, &InputRef{FileIDs: []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}}, upload)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := e2.db.GetTask(tk2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Title != "稻香 live" {
		t.Errorf("upload task.Title = %q", got2.Title)
	}
}

// TestUploadTitle 上传标题兜底：新落盘 <uuid>-<原名><ext> 剥前缀取原名；
// 分离产物（_instrumental 等轨道后缀）再加工时回源歌名；
// CLI --file 直接取文件名段；旧存量 <uuid>.<ext> 无原名返回空。
func TestUploadTitle(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"新落盘带原名", map[string]string{"audio": "/data/uploads/u1/6ba7b810-9dad-11d1-80b4-00c04fd430c8-周杰伦-稻香.flac"}, "周杰伦-稻香"},
		{"分离产物再加工", map[string]string{"audio": "/data/editor/01/6ba7b810-9dad-11d1-80b4-00c04fd430c8-稻香_instrumental.mp3"}, "稻香"},
		{"CLI本地文件", map[string]string{"audio": "/tmp/demo song.mp3"}, "demo song"},
		{"旧存量裸uuid", map[string]string{"audio": "/data/uploads/6ba7b810-9dad-11d1-80b4-00c04fd430c8.mp3"}, ""},
		{"无文件", nil, ""},
		{"假uuid前缀不剥", map[string]string{"audio": "/tmp/notauuid-12345680811234568081123456808112345678-歌.mp3"}, "notauuid-12345680811234568081123456808112345678-歌"},
	}
	for _, tc := range cases {
		if got := uploadTitle(tc.files); got != tc.want {
			t.Errorf("%s: uploadTitle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// asrTool 名为 asr 的桩工具：验证完成时识别文本前缀派生标题（引擎只对 Tool=="asr" 生效）。
type asrTool struct{ summary map[string]any }

func (asrTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: "asr", Title: "ASR"}
}
func (asrTool) ResultTitle(summaryJSON string) string {
	return provider.ASRTitleFromSummary(summaryJSON)
}
func (asrTool) ParamSpecs() []provider.ParamSpec { return nil }
func (t asrTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{Kind: "transcript", Path: "asr/a.txt", Format: "txt"}},
		Summary:   t.summary,
	}, nil
}

// TestArtifactTitle 产物联动任务标题：文件名 stem + 轨道中文标注；纯 uuid 文件名
// 回源任务标题；查无产物保持旧行为兜底；createTask 经 ref.ArtifactInput 接线。
func TestArtifactTitle(t *testing.T) {
	e := newTestEngine(t, detailTool{}, nil)
	if err := e.db.CreateTask(&store.Task{ID: "src-1", Provider: "mvsep", Tool: "separate", Status: store.StatusSucceeded, Title: "稻香 - 周杰伦"}); err != nil {
		t.Fatal(err)
	}
	seed := []struct{ id, filename, want string }{
		{"01234567-9abc-def0-1234-56789abcdef0", "稻香_instrumental.wav", "稻香 · 伴奏"},
		{"11111111-2222-3333-4444-555555555555", "6ba7b810-9dad-11d1-80b4-00c04fd430c8.wav", "稻香 - 周杰伦"},
		{"66666666-7777-8888-9999-aaaaaaaaaaaa", "6ba7b810-9dad-11d1-80b4-00c04fd430c8_vocals.wav", "稻香 - 周杰伦 · 人声"},
	}
	for _, tc := range seed {
		if err := e.db.CreateArtifact(&store.Artifact{ID: tc.id, TaskID: "src-1", UserID: "", Kind: "audio", Path: "sep/" + tc.filename, Filename: tc.filename}); err != nil {
			t.Fatal(err)
		}
		if got := e.artifactTitle(tc.id); got != tc.want {
			t.Errorf("artifactTitle(%s) = %q, want %q", tc.filename, got, tc.want)
		}
	}
	if got := e.artifactTitle("ffffffff-ffff-ffff-ffff-ffffffffffff"); got != "产物 ffffffff" {
		t.Errorf("missing artifact fallback = %q", got)
	}

	// createTask 接线：URL/文本全无时产物联动标题入库
	tk, _, err := e.createTask("", "fake", "detail", map[string]any{}, &InputRef{ArtifactInput: seed[0].id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "稻香 · 伴奏" {
		t.Errorf("createTask title = %q", tk.Title)
	}
}

// TestASRCompletionTitle ASR 完成时识别文本前缀替换机器标题；用户手改（title_edited）让位。
func TestASRCompletionTitle(t *testing.T) {
	var events []Event
	e := newTestEngine(t, asrTool{summary: map[string]any{
		"text": "大家好,欢迎来到今晚的分享会,内容很长会被截断",
	}}, &events)
	tk, _, err := e.SubmitSync(context.Background(), "fake", "asr", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.Title, "大家好") || !strings.HasSuffix(tk.Title, "…") || len([]rune(tk.Title)) != 21 {
		t.Errorf("完成时标题未按 20 字截断派生: %q", tk.Title)
	}

	// segments 形状（火山系）：取首个非空分句
	e2 := newTestEngine(t, asrTool{summary: map[string]any{
		"segments": []map[string]any{{"text": "第一句话", "start_ms": 0}, {"text": "第二句话"}},
	}}, &events)
	tk2, _, err := e2.SubmitSync(context.Background(), "fake", "asr", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tk2.Title != "第一句话第二句话" {
		t.Errorf("segments 标题 = %q, want 拼接前缀 第一句话第二句话", tk2.Title)
	}

	// 用户手改标题让位：createTask 后置 TitleEdited，直接驱动 run 走完成分支
	e3 := newTestEngine(t, asrTool{summary: map[string]any{"text": "自动派生的文本"}}, &events)
	t3, _, err := e3.createTask("", "fake", "asr", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t3.Title = "用户手改的标题"
	t3.TitleEdited = true
	tool, _ := e3.reg.Get("fake", "asr")
	if _, _, err := e3.run(context.Background(), t3, tool, map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	got3, err := e3.db.GetTask(t3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Title != "用户手改的标题" {
		t.Errorf("手改标题被覆盖: %q", got3.Title)
	}
}
