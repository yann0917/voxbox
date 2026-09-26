package main

import (
	"bufio"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildVoxbox 编译当前包到临时目录（e2e 用真实二进制而非 go run，避免每次重新编译解释开销）。
func buildVoxbox(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "voxbox")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

// 等待就绪行，返回解析出的地址；timeout 内未出现则 Fatal。
func waitReadyLine(t *testing.T, stdout io.ReadCloser, timeout time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "VOXBOX_READY addr="); ok {
				ch <- strings.TrimSpace(rest)
				return
			}
		}
		close(ch)
	}()
	select {
	case addr := <-ch:
		if addr == "" {
			t.Fatal("stdout 先于就绪行关闭")
		}
		return addr
	case <-time.After(timeout):
		t.Fatalf("%v 内未出现 VOXBOX_READY 就绪行", timeout)
		return ""
	}
}

// 桌面壳契约：--port 0 由系统分配空闲端口，就绪行地址真实可连。
func TestServePortZeroReadyLineAndDialable(t *testing.T) {
	bin := buildVoxbox(t)
	cmd := exec.Command(bin, "serve", "--port", "0")
	cmd.Env = append(os.Environ(), "VOXBOX_HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	addr := waitReadyLine(t, stdout, 30*time.Second)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("就绪行地址 = %q, want 127.0.0.1:<port>", addr)
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("就绪行端口不可连: %v", err)
	}
	_ = conn.Close()
}

// 既有行为回归：显式 --port 覆盖配置默认端口（就绪行同样打印）。
func TestServeDefaultPortFromConfig(t *testing.T) {
	bin := buildVoxbox(t)
	cmd := exec.Command(bin, "serve", "--port", "18099")
	cmd.Env = append(os.Environ(), "VOXBOX_HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	addr := waitReadyLine(t, stdout, 30*time.Second)
	if addr != "127.0.0.1:18099" {
		t.Fatalf("就绪行地址 = %q, want 127.0.0.1:18099", addr)
	}
}

// flag 默认值改为 -1（未指定），0 的新语义是系统分配。
func TestServePortFlagDefault(t *testing.T) {
	f := newServeCommand().Flags().Lookup("port")
	if f == nil {
		t.Fatal("serve 无 --port flag")
	}
	if f.DefValue != "-1" {
		t.Fatalf("--port 默认值 = %q, want -1", f.DefValue)
	}
}

// 桌面模式首启静默引导：admin 行照常创建，但初始密码绝不外泄到输出。
func TestServeDesktopModeSilentBootstrap(t *testing.T) {
	bin := buildVoxbox(t)
	cmd := exec.Command(bin, "serve", "--port", "0")
	cmd.Env = append(os.Environ(), "VOXBOX_HOME="+t.TempDir(), "VOXBOX_DESKTOP=1")
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

	waitReadyLine(t, stdout, 30*time.Second)
	time.Sleep(2 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if s := errBuf.String(); strings.Contains(s, "初始密码") || strings.Contains(s, "首次启动已创建管理员账号") {
		t.Fatalf("桌面模式泄露引导密码输出:\n%s", s)
	}
}
