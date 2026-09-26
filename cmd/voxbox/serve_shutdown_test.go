package main

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// SIGTERM 优雅关闭契约：进程以退出码 0 正常结束（此前是默认处置直接被信号杀死），
// stderr 有退出提示，且从发信号到退出是短时排空而非挂死。Windows 无 SIGTERM 语义
// （Ctrl+C 走 os.Interrupt 同一代码路径），无法在测试里模拟，跳过。
func TestServeGracefulShutdownOnSigterm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 SIGTERM 语义，优雅关闭路径由 Ctrl+C（os.Interrupt）触发")
	}
	bin := buildVoxbox(t)
	cmd := exec.Command(bin, "serve", "--port", "0")
	cmd.Env = append(os.Environ(), "VOXBOX_HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var errBuf strings.Builder
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			mu.Lock()
			errBuf.WriteString(sc.Text())
			errBuf.WriteString("\n")
			mu.Unlock()
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	addr := waitReadyLine(t, stdout, 30*time.Second)

	// 发信号前确认服务在正常出响应（非桌面模式 / 走 index 回退应为 200）。
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("SIGTERM 前服务不可达: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SIGTERM 前 GET / status = %d, want 200", resp.StatusCode)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("发送 SIGTERM 失败: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// 退出码 0 → Wait 返回 nil；被信号杀死 → ExitError（signal: terminated）。
		if err != nil {
			t.Fatalf("SIGTERM 后进程未优雅退出（期望退出码 0）: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("SIGTERM 后 15s 未退出——优雅关闭未生效或被挂死")
	}

	mu.Lock()
	defer mu.Unlock()
	if s := errBuf.String(); !strings.Contains(s, "正在退出") {
		t.Fatalf("stderr 缺少退出提示，实际输出:\n%s", s)
	}
}
