package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// 简报修正：原测试中 db2 相关两行已删（tempdb 无法复用），
// 本测试只留 Create/Get 断言；interrupted 语义由 TestOpenMarksRunningAsInterrupted 覆盖。
func TestTaskCRUDAndInterrupted(t *testing.T) {
	db := openTest(t)
	task := &Task{ID: "t1", Provider: "volcengine", Tool: "tts", Status: StatusRunning, Params: "{}"}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.Provider != "volcengine" || got.Tool != "tts" || got.Status != StatusRunning || got.Params != "{}" {
		t.Errorf("got = %+v, want created task t1", got)
	}
}

func TestOpenMarksRunningAsInterrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	db, _ := Open(path)
	_ = db.CreateTask(&Task{ID: "r1", Provider: "p", Tool: "t", Status: StatusRunning})
	_ = db.CreateTask(&Task{ID: "s1", Provider: "p", Tool: "t", Status: StatusSucceeded})
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db2.GetTask("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted {
		t.Errorf("r1 status = %s, want interrupted", got.Status)
	}
	got, _ = db2.GetTask("s1")
	if got.Status != StatusSucceeded {
		t.Errorf("s1 status = %s, want succeeded", got.Status)
	}
}

func TestDeleteTaskSoft(t *testing.T) {
	db := openTest(t)
	_ = db.CreateTask(&Task{ID: "t1", Provider: "volcengine", Tool: "tts", Status: StatusSucceeded})
	_ = db.CreateArtifact(&Artifact{ID: "a1", TaskID: "t1", Kind: "audio", Path: "x.mp3", CreatedAt: time.Now()})
	items, total, err := db.ListTasks("volcengine", nil, 10, 0, "")
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list: items=%d total=%d err=%v", len(items), total, err)
	}
	if err := db.DeleteTask("t1"); err != nil {
		t.Fatal(err)
	}
	// 软删后常规查询自动过滤（Get/List），产物行保留（恢复时列表完整）
	if _, err := db.GetTask("t1"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	_, total, err = db.ListTasks("volcengine", nil, 10, 0, "")
	if err != nil || total != 0 {
		t.Fatalf("list after delete: total=%d err=%v", total, err)
	}
	arts, _ := db.ListArtifacts("t1")
	if len(arts) != 1 {
		t.Errorf("artifacts 应随软删保留: %d", len(arts))
	}
}
