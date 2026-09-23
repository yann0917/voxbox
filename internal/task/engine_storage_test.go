package task

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
)

// fakeStore 记录 Put 调用的假存储客户端。
type fakeStore struct {
	keys []string
}

func (f *fakeStore) Put(_ context.Context, key, _ string, _ io.Reader, _ int64) error {
	f.keys = append(f.keys, key)
	return nil
}
func (f *fakeStore) PresignGet(key string, _ time.Duration) (string, error) {
	return "https://fake.tos/" + key, nil
}

// storageProbeTool 断言引擎注入的 Storage 通道可达工具。
type storageProbeTool struct{ got provider.StorageClient }

func (t *storageProbeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: "probe", Title: "探针", Group: "测试"}
}
func (t *storageProbeTool) ParamSpecs() []provider.ParamSpec { return nil }
func (t *storageProbeTool) Run(_ context.Context, in provider.TaskInput, _ provider.ProgressReporter) (provider.TaskOutput, error) {
	t.got = in.Storage
	return provider.TaskOutput{}, nil
}

// TestEngineInjectsStorage 引擎把当前对象存储客户端注入 TaskInput（热替换 getter 语义：
// 每个任务启动时取最新客户端），未注入时为 nil。
func TestEngineInjectsStorage(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	probe := &storageProbeTool{}
	if err := reg.Register(probe); err != nil {
		t.Fatal(err)
	}
	eng := New(db, reg, dir, 1, nil)

	// 未注入：nil
	if _, _, err := eng.SubmitSync(context.Background(), "fake", "probe", nil, nil); err != nil {
		t.Fatal(err)
	}
	if probe.got != nil {
		t.Fatal("未注入存储时 TaskInput.Storage 应为 nil")
	}

	// 注入后：任务拿到客户端
	st := &fakeStore{}
	eng.SetStorageClient(func() provider.StorageClient { return st })
	if _, _, err := eng.SubmitSync(context.Background(), "fake", "probe", nil, nil); err != nil {
		t.Fatal(err)
	}
	if probe.got == nil {
		t.Fatal("注入后 TaskInput.Storage 不应为 nil")
	}

	// 返回 nil 的 getter（配置被清空）：工具侧收到 nil
	eng.SetStorageClient(func() provider.StorageClient { return nil })
	if _, _, err := eng.SubmitSync(context.Background(), "fake", "probe", nil, nil); err != nil {
		t.Fatal(err)
	}
	if probe.got != nil {
		t.Fatal("getter 返回 nil 时 TaskInput.Storage 应为 nil")
	}
	if !strings.Contains(strings.Join(st.keys, ","), "") && len(st.keys) != 0 {
		t.Fatalf("探针工具不应触发转存: %v", st.keys)
	}
}
