package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateTaskMeta 标题/标签条件更新：只改传入端，title 置 title_edited，
// 属主/存在性不匹配报 ErrNotFound。
func TestUpdateTaskMeta(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CreateTask(&Task{ID: "t1", UserID: "u1", Provider: "p", Tool: "asr", Status: StatusSucceeded}); err != nil {
		t.Fatal(err)
	}

	title := "新标题"
	if err := d.UpdateTaskMeta("t1", "u1", &title, nil); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "新标题" || !got.TitleEdited {
		t.Fatalf("title=%q edited=%v", got.Title, got.TitleEdited)
	}

	tags := `["会议","粤语"]`
	if err := d.UpdateTaskMeta("t1", "u1", nil, &tags); err != nil {
		t.Fatal(err)
	}
	if got, _ = d.GetTask("t1"); got.Tags != tags || got.Title != "新标题" || !got.TitleEdited {
		t.Fatalf("tags-only 更新波及其他列: %+v", got)
	}

	if err := d.UpdateTaskMeta("t1", "u1", nil, nil); err != ErrNotFound {
		t.Fatalf("空更新 = %v, want ErrNotFound", err)
	}
	wrong := "别人的标题"
	if err := d.UpdateTaskMeta("t1", "u2", &wrong, nil); err != ErrNotFound {
		t.Fatalf("越权更新 = %v, want ErrNotFound", err)
	}
	if got, _ = d.GetTask("t1"); got.Title == wrong {
		t.Fatal("越权更新生效了")
	}
	if err := d.UpdateTaskMeta("missing", "u1", &title, nil); err != ErrNotFound {
		t.Fatalf("不存在任务 = %v, want ErrNotFound", err)
	}
}

// TestBackfillTaskTitles 存量机器标题回填：三类旧模式用识别文本重写，
// 手改/非 asr/解析不出文本的跳过；重复执行幂等。
func TestBackfillTaskTitles(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "bf.db"))
	if err != nil {
		t.Fatal(err)
	}
	seed := []*Task{
		// 命中「实时字幕+时间」：长文本 → 20 字截断
		{ID: "t1", Provider: "volcengine", Tool: "asr", Status: StatusSucceeded,
			Title: "实时字幕 2026-10-03 16:00", Summary: `{"text":"大家好,欢迎来到今晚的分享会,内容很长会被截断"}`},
		// 命中「录音-」：segments 形状取首分句
		{ID: "t2", Provider: "local", Tool: "asr", Status: StatusSucceeded,
			Title: "录音-163025", Summary: `{"segments":[{"text":"第一句话","start_ms":0},{"text":"第二句话"}]}`},
		// 命中「产物」但 Summary 解析不出文本：保留原样
		{ID: "t3", Provider: "volcengine", Tool: "asr", Status: StatusSucceeded,
			Title: "产物 01234567", Summary: `not-json`},
		// 用户手改过：保留
		{ID: "t4", Provider: "local", Tool: "asr", Status: StatusSucceeded, TitleEdited: true,
			Title: "实时字幕 2026-10-03 17:00", Summary: `{"text":"不该出现在标题里"}`},
		// 非 asr 工具：保留
		{ID: "t5", Provider: "volcengine", Tool: "tts", Status: StatusSucceeded,
			Title: "录音-999999", Summary: `{"text":"不该出现在标题里"}`},
	}
	for _, s := range seed {
		if err := d.CreateTask(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.backfillTaskTitles(); err != nil {
		t.Fatal(err)
	}

	got1, _ := d.GetTask("t1")
	if !strings.HasPrefix(got1.Title, "大家好") || !strings.HasSuffix(got1.Title, "…") || len([]rune(got1.Title)) != 21 {
		t.Errorf("t1 title = %q, want 识别文本前缀截断", got1.Title)
	}
	got2, _ := d.GetTask("t2")
	if got2.Title != "第一句话第二句话" {
		t.Errorf("t2 title = %q, want 拼接前缀 第一句话第二句话", got2.Title)
	}
	for id, want := range map[string]string{
		"t3": "产物 01234567",
		"t4": "实时字幕 2026-10-03 17:00",
		"t5": "录音-999999",
	} {
		got, _ := d.GetTask(id)
		if got.Title != want {
			t.Errorf("%s title = %q, want %q（保留原样）", id, got.Title, want)
		}
	}

	// 幂等：再跑一遍结果不变
	if err := d.backfillTaskTitles(); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetTask("t1"); !strings.HasPrefix(got.Title, "大家好") {
		t.Errorf("二次回填改变结果: %q", got.Title)
	}
}

// TestASRTitleFromSummary text 优先、segments 次之、全空/损坏返回空串。
func TestASRTitleFromSummary(t *testing.T) {
	if got := ASRTitleFromSummary(`{"text":"  多  行\n文本  "}`); got != "多 行 文本" {
		t.Errorf("text 形状 = %q", got)
	}
	if got := ASRTitleFromSummary(`{"segments":[{"text":"  "},{"text":"第二句"}]}`); got != "第二句" {
		t.Errorf("segments 跳过空分句 = %q", got)
	}
	if got := ASRTitleFromSummary(`{"segments":[{"text":""}]}`); got != "" {
		t.Errorf("全空 = %q, want 空", got)
	}
	if got := ASRTitleFromSummary(""); got != "" {
		t.Errorf("空 summary = %q", got)
	}
}
