//go:build !windows

package localmodel

import (
	"fmt"
	"syscall"
)

// realDiskFree 目录所在文件系统的剩余可用字节数(unix: statfs)。
func realDiskFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %w", err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
