package volcengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

func writeBridgeFile(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureURLInput(t *testing.T) {
	st := &fakeStorage{}
	file := writeBridgeFile(t, "meeting.mp3", 1024)

	t.Run("URL直用", func(t *testing.T) {
		got, err := ensureURLInput(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": " https://cdn.example.com/a.mp3 "},
		}, "url", "音频", "缺少", nopReport)
		if err != nil || got != "https://cdn.example.com/a.mp3" {
			t.Fatalf("got=%q err=%v", got, err)
		}
	})

	t.Run("URL协议校验", func(t *testing.T) {
		if _, err := ensureURLInput(context.Background(), provider.TaskInput{
			Params: map[string]any{"url": "ftp://x/a.mp3"},
		}, "url", "音频", "缺少", nopReport); err == nil || !strings.Contains(err.Error(), "http(s)") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("无输入报missingErr", func(t *testing.T) {
		if _, err := ensureURLInput(context.Background(), provider.TaskInput{},
			"url", "音频", "缺少输入：请上传", nopReport); err == nil || !strings.Contains(err.Error(), "缺少输入") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("文件但未配置存储", func(t *testing.T) {
		_, err := ensureURLInput(context.Background(), provider.TaskInput{
			Files: map[string]string{"audio": file},
		}, "url", "音频", "缺少", nopReport)
		if err == nil || !strings.Contains(err.Error(), "对象存储") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("文件转存取签名URL", func(t *testing.T) {
		got, err := ensureURLInput(context.Background(), provider.TaskInput{
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
		if _, err := ensureURLInput(context.Background(), provider.TaskInput{
			Files:   map[string]string{"audio": noext},
			Storage: st,
		}, "url", "音频", "缺少", nopReport); err == nil || !strings.Contains(err.Error(), "扩展名") {
			t.Fatalf("err=%v", err)
		}
	})
}
