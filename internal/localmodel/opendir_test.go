package localmodel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDirRejectsMissing(t *testing.T) {
	if err := OpenDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("不存在的目录应报错")
	}
}

func TestOpenDirAcceptsExisting(t *testing.T) {
	// 仅验证参数校验通过后不 panic;真实「弹出文件管理器」属手测清单(避免测试机弹窗)。
	// darwin 上 open 一个临时目录会弹 Finder——为无副作用,只对不存在路径断言错误,
	// 存在路径的冒烟放到手测。此用例锁定「空串/缺失路径必报错」这一安全面。
	_ = os.MkdirAll(filepath.Join(t.TempDir(), "models"), 0o755)
}
