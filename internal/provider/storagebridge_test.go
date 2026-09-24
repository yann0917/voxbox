package provider

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeBridgeFile(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// nopReport 测试用空进度上报器。
func nopReport(progress int, note string, detail map[string]any) {}

// fakeStorage 桥接测试用假存储客户端：记录 Put 的 key/size，PresignGet 返回可断言的固定域名 URL。
type fakeStorage struct {
	mu       sync.Mutex
	keys     []string
	sizes    []int64
	presignT time.Duration
}

func (f *fakeStorage) Put(_ context.Context, key, _ string, _ io.Reader, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys, f.sizes = append(f.keys, key), append(f.sizes, size)
	return nil
}

func (f *fakeStorage) PresignGet(key string, ttl time.Duration) (string, error) {
	f.presignT = ttl
	return "https://bucket.tos-cn-beijing.volces.com/" + key + "?X-Tos-Algorithm=fake", nil
}

func TestEnsureURLInput(t *testing.T) {
	st := &fakeStorage{}
	file := writeBridgeFile(t, "meeting.mp3", 1024)

	t.Run("URL直用", func(t *testing.T) {
		got, err := EnsureURLInput(context.Background(), TaskInput{
			Params: map[string]any{"url": " https://cdn.example.com/a.mp3 "},
		}, "url", "音频", "缺少", nopReport)
		if err != nil || got != "https://cdn.example.com/a.mp3" {
			t.Fatalf("got=%q err=%v", got, err)
		}
	})

	t.Run("URL协议校验", func(t *testing.T) {
		if _, err := EnsureURLInput(context.Background(), TaskInput{
			Params: map[string]any{"url": "ftp://x/a.mp3"},
		}, "url", "音频", "缺少", nopReport); err == nil || !strings.Contains(err.Error(), "http(s)") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("无输入报missingErr", func(t *testing.T) {
		if _, err := EnsureURLInput(context.Background(), TaskInput{},
			"url", "音频", "缺少输入：请上传", nopReport); err == nil || !strings.Contains(err.Error(), "缺少输入") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("文件但未配置存储", func(t *testing.T) {
		_, err := EnsureURLInput(context.Background(), TaskInput{
			Files: map[string]string{"audio": file},
		}, "url", "音频", "缺少", nopReport)
		if err == nil || !strings.Contains(err.Error(), "对象存储") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("文件转存取签名URL", func(t *testing.T) {
		got, err := EnsureURLInput(context.Background(), TaskInput{
			Files:   map[string]string{"audio": file},
			Storage: st,
		}, "url", "音频", "缺少", nopReport)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.HasPrefix(got, "https://bucket.tos-cn-beijing.volces.com/") || !strings.HasSuffix(strings.Split(got, "?")[0], ".mp3") {
			t.Fatalf("签名 URL = %q", got)
		}
		if len(st.keys) != 1 || st.sizes[0] != 1024 {
			t.Fatalf("转存记录 keys=%v sizes=%v", st.keys, st.sizes)
		}
		if st.presignT != 72*time.Hour {
			t.Fatalf("预签名有效期 = %v, 期望 72h（对齐 3 天生命周期）", st.presignT)
		}
	})

	t.Run("无扩展名文件拒绝", func(t *testing.T) {
		noext := writeBridgeFile(t, "noext", 10)
		if _, err := EnsureURLInput(context.Background(), TaskInput{
			Files:   map[string]string{"audio": noext},
			Storage: st,
		}, "url", "音频", "缺少", nopReport); err == nil || !strings.Contains(err.Error(), "扩展名") {
			t.Fatalf("err=%v", err)
		}
	})
}
