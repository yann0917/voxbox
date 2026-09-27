//go:build windows

package localmodel

import (
	"fmt"
	"syscall"
	"unsafe"
)

// realDiskFree 目录所在卷的剩余可用字节数(Windows: GetDiskFreeSpaceExW,纯 syscall 无 cgo)。
func realDiskFree(dir string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %w", err)
	}
	var avail, total, totalFree uint64
	r1, _, e := proc.Call(uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r1 == 0 {
		return 0, fmt.Errorf("探测磁盘剩余空间失败: %v", e)
	}
	return avail, nil
}
