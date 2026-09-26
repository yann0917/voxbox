package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// 父进程 pid 变化（被 init/launchd 重新收养）→ die 必须被触发。
func TestParentWatchdogFiresOnReparent(t *testing.T) {
	var current atomic.Int32
	current.Store(100)
	fired := make(chan struct{}, 1)
	stop := startParentWatchdog(func() int { return int(current.Load()) }, 5*time.Millisecond, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	defer stop()

	current.Store(1) // 模拟父进程死亡后被收养
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("父进程 pid 变化后看门狗未触发")
	}
}

// 父进程存活（ppid 稳定）→ 不触发。
func TestParentWatchdogQuietWhileParentAlive(t *testing.T) {
	var fired atomic.Bool
	stop := startParentWatchdog(func() int { return 4242 }, 5*time.Millisecond, func() { fired.Store(true) })
	defer stop()

	time.Sleep(60 * time.Millisecond)
	if fired.Load() {
		t.Fatal("父进程存活期间看门狗误触发")
	}
}

// 初始 ppid<=1（由 init/launchd 直接拉起）→ 看门狗不启动，绝不误杀。
func TestParentWatchdogNoParentNoStart(t *testing.T) {
	var fired atomic.Bool
	stop := startParentWatchdog(func() int { return 1 }, 5*time.Millisecond, func() { fired.Store(true) })
	defer stop()

	time.Sleep(60 * time.Millisecond)
	if fired.Load() {
		t.Fatal("初始 ppid=1 时看门狗不应启动")
	}
}
