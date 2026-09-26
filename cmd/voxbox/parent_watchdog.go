package main

import (
	"time"
)

// 桌面模式父进程看门狗：桌面壳是 sidecar 的父进程。优雅退出（托盘退出/正常退出事件）
// 由壳侧 RunEvent::Exit 里的 child.kill 清理；但壳被强杀（kill -9/崩溃/系统注销）时
// 壳侧代码没有机会执行，sidecar 被内核重新收养给 init/launchd，变成无人管的后端孤儿
// ——端口仍被占用、无 UI 可达。这里轮询 ppid 兜底：一旦被重新收养（ppid 变化）即自杀。
// 仅 VOXBOX_DESKTOP=1 时启用，CLI 独立使用不受影响。
// getppid/die 抽成函数参数以便测试；生产路径传 os.Getppid/os.Exit。
func startParentWatchdog(getppid func() int, interval time.Duration, die func()) (stop func()) {
	ppid := getppid()
	if ppid <= 1 {
		// 初始就没有父进程（由 init/launchd 直接拉起）：看门狗无从判定，
		// 直接不启动，避免把初始 ppid 误判成“刚被收养”而瞬间自杀。
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if getppid() != ppid {
					die()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}
