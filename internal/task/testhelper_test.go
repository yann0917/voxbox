package task

import (
	"path/filepath"
	"testing"

	"github.com/yann0917/voxbox/internal/store"
)

// OpenStore 在独立目录打开测试库。
func OpenStore(t *testing.T) (*store.DB, error) {
	t.Helper()
	return store.Open(filepath.Join(t.TempDir(), "t.db"))
}
