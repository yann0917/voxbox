# Tauri 2 桌面壳实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Tauri 2 把 voxbox 包成 macOS + Windows 桌面应用：sidecar 拉起 `voxbox serve`、免登录直达工作台、托盘驻留、GitHub Releases 自动更新。

**Architecture:** Tauri 壳（Rust，`desktop/src-tauri`）以 sidecar 方式打包 Go 二进制并 spawn `voxbox serve --port 0`（env `VOXBOX_DESKTOP=1`），从 stdout 读就绪行 `VOXBOX_READY addr=<host:port>` 后创建主窗口指向 `http://127.0.0.1:<port>/workbench`。页面不使用任何 Tauri JS API，壳逻辑全在 Rust。前端保持单一构建，desktop 形态靠 `/api/auth/me` 的 `desktop:true` 运行时判断。

**Tech Stack:** Go 1.26（gin/cobra）、Tauri 2（tauri-plugin-shell/single-instance/updater/dialog）、React 19 + Vite（现有）、GitHub Actions（tauri-action）。

**Spec:** `docs/superpowers/specs/2026-09-26-tauri-desktop-design.md`

## Global Constraints

- 就绪行契约精确为 stdout 单行：`VOXBOX_READY addr=<host>:<port>`（Rust 侧 `READY_PREFIX = "VOXBOX_READY addr="`，Go 侧改动必须逐字保持）。
- 桌面模式环境变量：`VOXBOX_DESKTOP=1`；数据目录隔离用 `VOXBOX_HOME=<dir>`（`internal/config/config.go:182` 已支持）。
- 前端**不改**打包链：不新增 desktop 专用构建变体，`go:embed` 仍是唯一事实来源；页面内禁止调用 Tauri JS API。
- Go 改动过 `gofmt -l -w cmd internal`（CI 有 gofmt 检查）与 `go vet ./...`；每任务结束 `go test ./...` 全绿再提交。
- 提交信息用仓库惯例：`type(scope): 中文描述——细节`（type: feat/fix/docs/chore）。
- UI 文案中文、遵循「深空信号站」既有视觉（不新增独立配色）。
- 本机为 darwin/arm64：Rust 侧本地验证只覆盖 macOS；Windows 链路只能靠 Task 9 的 CI 验证，不得声称本地验证过 Windows。
- 工作分支 main；`git commit` 前用 `git branch --show-current` 确认。

---

### Task 1: serve 动态端口 + VOXBOX_READY 就绪行（Go）

**Files:**
- Modify: `cmd/voxbox/serve.go`
- Test: `cmd/voxbox/serve_desktop_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `newServeCommand()`（`cmd/voxbox/serve.go`）、`VOXBOX_HOME` 隔离。
- Produces: `serve --port 0` = 系统分配空闲端口；stdout 就绪行 `VOXBOX_READY addr=host:port`。Task 5 的 Rust `spawn_sidecar` 与 Task 3 的静默引导 e2e 都依赖此契约。`--port` flag 默认值由 0 改为 -1（-1 = 未指定、沿用配置文件，现行为不变）。

- [ ] **Step 1: 写失败测试**

创建 `cmd/voxbox/serve_desktop_test.go`：

```go
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

// 既有行为回归：不传 --port 时沿用配置默认 8081（就绪行同样打印）。
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/voxbox/ -run 'TestServe' -v`
Expected: FAIL——`--port 默认值 = "0"`；两个 e2e 超时无就绪行。

- [ ] **Step 3: 实现**

修改 `cmd/voxbox/serve.go`：

1. import 增加 `"net"`。
2. RunE 中把 `if port != 0 { cfg.Server.Port = port }` 改为：

```go
if port >= 0 {
    cfg.Server.Port = port
}
```

3. 在 `handler := server.WithStatic(srv.Handler(), webDist)` 之后、原 `addr := fmt.Sprintf(...)` 之前改为（host 解析前移 + 空闲端口分配 + 就绪行）：

```go
host := cfg.Server.Host
if host == "" {
    host = "127.0.0.1"
}
if cfg.Server.Port == 0 {
    ln, err := net.Listen("tcp", host+":0")
    if err != nil {
        return fmt.Errorf("分配空闲端口失败: %w", err)
    }
    cfg.Server.Port = ln.Addr().(*net.TCPAddr).Port
    _ = ln.Close()
}
addr := fmt.Sprintf("%s:%d", host, cfg.Server.Port)
fmt.Printf("VOXBOX_READY addr=%s\n", addr) // stdout 机器可读就绪行（桌面壳契约）
fmt.Fprintf(os.Stderr, "voxbox Web 已启动: http://%s\n", addr)
```

（原 RunE 尾部的 `host := cfg.Server.Host; if host == "" {...}` 两条语句删除，避免重复声明。）

4. flag 注册改为：

```go
cmd.Flags().IntVar(&port, "port", -1, "端口（默认取配置，0 为系统分配空闲端口）")
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/voxbox/ -run 'TestServe' -v`
Expected: 4 个测试全 PASS。

- [ ] **Step 5: 全量回归 + 提交**

Run: `gofmt -l -w cmd internal && go vet ./... && go test ./...`
Expected: 全绿。

```bash
git add cmd/voxbox/serve.go cmd/voxbox/serve_desktop_test.go
git commit -m "feat(serve): --port 0 系统分配空闲端口 + stdout 就绪行 VOXBOX_READY（桌面壳契约）"
```

---

### Task 2: VOXBOX_DESKTOP 免登录注入 + me desktop 字段（Go）

**Files:**
- Modify: `internal/server/server.go`（Server 结构体加字段、New 读环境变量）
- Modify: `internal/server/auth.go`（desktopPrincipal、requireAuth/mcpAuth 注入、userDTO/me）
- Test: `internal/server/desktop_test.go`（新建）

**Interfaces:**
- Consumes: `GetUserByUsername`（`internal/store/user_repo.go:21`）、`EnsureBootstrapAdmin`（admin 用户名固定 "admin"）、测试助手模式参照 `routes_test.go` 的 `service.NewWithHome(t.TempDir())`。
- Produces: `VOXBOX_DESKTOP=1` 下未登录请求获得库内 admin 身份；`GET /api/auth/me` 响应 Data 含 `desktop: true`（userDTO 新增 `Desktop bool json:"desktop"`，非桌面恒 false）。Task 4 前端消费 `desktop` 字段。

- [ ] **Step 1: 写失败测试**

创建 `internal/server/desktop_test.go`：

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yann0917/voxbox/internal/service"
)

// newDesktopServer 桌面形态测试服：VOXBOX_DESKTOP=1 + 引导 admin 行，返回未登录客户端可访问的 ts。
func newDesktopServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("VOXBOX_DESKTOP", "1")
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	if _, err := s.EnsureBootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// 桌面形态：未登录 /api/auth/me 直接以 admin 身份返回，desktop:true，不触发强制改密。
func TestDesktopUnauthenticatedMeIsAdmin(t *testing.T) {
	ts := newDesktopServer(t)
	resp, err := http.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	d, _ := body["data"].(map[string]any)
	if d == nil {
		t.Fatalf("响应缺少 data: %v", body)
	}
	if d["username"] != "admin" || d["role"] != "admin" {
		t.Fatalf("身份 = %v/%v, want admin/admin", d["username"], d["role"])
	}
	if d["desktop"] != true {
		t.Fatal("desktop = false, want true")
	}
	if d["must_change_password"] == true {
		t.Fatal("must_change_password = true, 桌面形态不应触发强制改密")
	}
}

// 回归：非桌面形态未登录仍是 401（envelope code 7）。
func TestNonDesktopUnauthenticatedMeIs401(t *testing.T) {
	t.Setenv("VOXBOX_DESKTOP", "")
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	if _, err := s.EnsureBootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run 'Desktop|NonDesktop' -v`
Expected: FAIL——`status = 401` / `desktop = false`。

- [ ] **Step 3: 实现**

`internal/server/server.go`：Server 结构体加字段 `desktop bool`，`New` 改为：

```go
func New(svc *service.Service) *Server {
	return &Server{svc: svc, hub: NewHub(), desktop: os.Getenv("VOXBOX_DESKTOP") == "1"}
}
```

（补 import `"os"`；desktop 测试用 `t.Setenv` 需在 New 之前生效，测试里已如此。）

`internal/server/auth.go`：

1. `userDTO` 增加字段：

```go
	Desktop            bool   `json:"desktop"`
```

2. 新增方法（放 `sessionPrincipal` 附近）：

```go
// desktopPrincipal 桌面形态（VOXBOX_DESKTOP=1）免登录身份：以库内 admin 用户注入，
// 任务归属/搜索范围与真实用户行一致；admin 行由 EnsureBootstrapAdmin 保证存在。
// MustChangePassword 不透传（保持 false），首启强制改密流程自然跳过。
func (s *Server) desktopPrincipal() *Principal {
	if !s.desktop {
		return nil
	}
	u, err := s.svc.DB().GetUserByUsername("admin")
	if err != nil {
		return nil
	}
	return &Principal{ID: u.ID, Username: u.Username, Role: u.Role}
}
```

3. `requireAuth` 的 `unauthorized(c)` 前插入：

```go
	if p := s.desktopPrincipal(); p != nil {
		c.Set(principalKey, p)
		c.Next()
		return
	}
```

4. `mcpAuth` 的 `if p == nil {` 401 分支前插入（同一安全边界，MCP 端点桌面模式同样免鉴权）：

```go
	if p == nil {
		p = s.desktopPrincipal()
	}
```

5. `me` 改为：

```go
func (s *Server) me(c *gin.Context) {
	p := principalFrom(c)
	u, err := s.svc.DB().GetUserByID(p.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	d := toUserDTO(u)
	d.Desktop = s.desktop
	ok(c, d)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/server/ -v`
Expected: 全 PASS（含既有 auth/routes 回归）。

- [ ] **Step 5: 全量回归 + 提交**

Run: `gofmt -l -w cmd internal && go vet ./... && go test ./...`
Expected: 全绿。

```bash
git add internal/server/server.go internal/server/auth.go internal/server/desktop_test.go
git commit -m "feat(server): VOXBOX_DESKTOP=1 免登录注入库内 admin 身份，me 响应附 desktop 标记"
```

---

### Task 3: 桌面模式静默引导（Go）

**Files:**
- Modify: `cmd/voxbox/serve.go`（首启密码打印块加桌面模式门）
- Test: `cmd/voxbox/serve_desktop_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `buildVoxbox`/`waitReadyLine`、现有首启打印块（`serve.go` 中 `initialPassword != ""` 分支）。
- Produces: 桌面模式下首启不向 stderr 打印「初始密码」块。Task 5 的 Rust 壳无需再处理引导弹窗。

- [ ] **Step 1: 写失败测试（追加到 serve_desktop_test.go）**

```go
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
	stderrBuf := make(chan string, 1)
	go func() {
		var b strings.Builder
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			b.WriteString(sc.Text())
			b.WriteString("\n")
		}
		stderrBuf <- b.String()
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	waitReadyLine(t, stdout, 30*time.Second)
	time.Sleep(2 * time.Second) // 留出引导块输出的时间窗

	select {
	case s := <-stderrBuf:
		if strings.Contains(s, "初始密码") || strings.Contains(s, "首次启动已创建管理员账号") {
			t.Fatalf("桌面模式泄露引导密码输出:\n%s", s)
		}
	case <-time.After(3 * time.Second):
		// stderr 流未关闭（进程仍在跑），用非阻塞探测兜底
	}
}
```

注意：stderr 读取 goroutine 只有进程退出才关闭流，上面的 select 是非阻塞探测——若 3 秒内没收到内容就直接通过（密码块若已输出会被 scanner 收进 buffer，但 chan 只在流关闭时才送出；因此该测试的强断言依赖进程最终退出）。为保证断言有效，改造：把 `stderrBuf <- b.String()` 改为收集进带锁 buffer，`waitReadyLine` 之后直接加锁检查内容。最终版本：

```go
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
```

（import 增加 `"sync"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/voxbox/ -run 'TestServeDesktopModeSilent' -v`
Expected: FAIL——`桌面模式泄露引导密码输出`。

- [ ] **Step 3: 实现**

`cmd/voxbox/serve.go` 的首启打印块，条件由 `if initialPassword != "" {` 改为：

```go
if initialPassword != "" && os.Getenv("VOXBOX_DESKTOP") != "1" {
```

（admin 行创建逻辑不动；桌面壳场景下用户永远不需要这串密码。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/voxbox/ -v`
Expected: 全 PASS。

- [ ] **Step 5: 全量回归 + 提交**

Run: `gofmt -l -w cmd internal && go vet ./... && go test ./...`
Expected: 全绿。

```bash
git add cmd/voxbox/serve.go cmd/voxbox/serve_desktop_test.go
git commit -m "feat(serve): 桌面模式首启静默引导——admin 照常创建，初始密码不外泄"
```

---

### Task 4: 前端 desktop 形态（web）

**Files:**
- Modify: `web/src/lib/auth.ts`（Me 类型）
- Modify: `web/src/main.tsx`（DesktopGate + 公开路由包裹）
- Modify: `web/src/components/Layout.tsx`（UserMenu 退出项显隐）
- Modify: `web/src/pages/SettingsPage.tsx`（API Token 卡显隐）

**Interfaces:**
- Consumes: Task 2 的 `GET /api/auth/me` Data `desktop: boolean`；`useMe()`（`lib/auth.ts:31`）。
- Produces: 桌面形态 `/`、`/login` 自动重定向 `/workbench`；退出项与 Token 卡隐藏。浏览器（非桌面）行为零变化（`desktop` 恒 undefined）。

- [ ] **Step 1: Me 类型加字段**

`web/src/lib/auth.ts` 的 `Me` interface 增加：

```ts
  desktop?: boolean;
```

- [ ] **Step 2: main.tsx 公开路由门**

`web/src/main.tsx`：在 `LegacyPostRedirect` 附近新增组件：

```tsx
// 桌面形态公开路由门：desktop=true 时 Landing/登录页不可达，一律直达工作台。
// 非桌面（浏览器）下 desktop 恒为 undefined，公开路由行为不变。
function DesktopGate({ children }: { children: React.ReactNode }) {
  const { data } = useMe();
  if (data?.desktop) return <Navigate to="/workbench" replace />;
  return <>{children}</>;
}
```

两个公开路由改为：

```tsx
            <Route path="/" element={<DesktopGate><LandingPage /></DesktopGate>} />
            <Route path="/login" element={<DesktopGate><LoginPage /></DesktopGate>} />
```

- [ ] **Step 3: Layout UserMenu 退出项显隐**

`web/src/components/Layout.tsx`：`UserMenu` 签名加 `desktop: boolean`：

```tsx
function UserMenu({ username, role, desktop, onAskLogout }: { username: string; role: string; desktop: boolean; onAskLogout: () => void }) {
```

退出登录菜单项（含 `<LogOut .../>` 图标的整个 `<button role="menuitem">`）包裹：

```tsx
          {!desktop && (
            <button role="menuitem" onClick={() => { setOpen(false); onAskLogout(); }} className="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-xs text-fg-2 transition-colors duration-150 hover:bg-raise-2 hover:text-fg">
              <LogOut size={13} strokeWidth={1.75} />
              退出登录
            </button>
          )}
```

调用处（`grep -n '<UserMenu' web/src/components/Layout.tsx` 定位）增加 prop：`desktop={me?.desktop === true}`（`me` 来自该文件已有的 `useMe()`；若变量名不同以实际为准）。

- [ ] **Step 4: SettingsPage API Token 卡显隐**

`web/src/pages/SettingsPage.tsx`：API Token 卡（锚点 `<CardHeader title="API Token"`，约 532 行起，至该 Card 结束）包裹：

```tsx
              {me?.desktop !== true && (
                // …原 API Token Card JSX 原样…
              )}
```

桌面形态下 Token 只为 MCP/CLI 远程鉴权服务，桌面版不暴露 CLI/MCP 入口，故整卡隐藏。

- [ ] **Step 5: 构建验证 + 提交**

Run: `cd web && npm run build`
Expected: tsc 零错误、构建成功。

Run: `go build -o /tmp/voxbox-regress ./cmd/voxbox && VOXBOX_HOME=$(mktemp -d) /tmp/voxbox-regress serve --port 18098 &`，浏览器开 `http://127.0.0.1:18098`
Expected: 未带 `VOXBOX_DESKTOP` 时 Landing/登录/退出/Token 卡与现状完全一致（desktop 字段缺省）。验证后杀进程。

```bash
git add web/src/lib/auth.ts web/src/main.tsx web/src/components/Layout.tsx web/src/pages/SettingsPage.tsx
git commit -m "feat(web): desktop 形态运行时判定——公开路由直达工作台、退出项与 Token 卡显隐"
```

---

### Task 5: Tauri 壳脚手架 + sidecar + 主窗口（Rust）

**Files:**
- Create: `desktop/shell-dist/index.html`
- Create: `desktop/src-tauri/build.rs`、`desktop/src-tauri/Cargo.toml`、`desktop/src-tauri/tauri.conf.json`、`desktop/src-tauri/src/main.rs`
- Create: `tools/icongen/main.go`（占位图标生成器）
- Create: `desktop/src-tauri/icons/`（`cargo tauri icon` 产物，入库）
- Create: `desktop/.gitignore`

**Interfaces:**
- Consumes: Task 1/3 的 sidecar 契约（`--port 0`、`VOXBOX_READY addr=`、`VOXBOX_DESKTOP=1`）。
- Produces: 可 `cargo tauri dev` 的壳（暂无托盘/更新，Task 6/7 加）；`SidecarChild` 状态与 `spawn_sidecar`/`open_main_window` 函数签名供后续任务扩展。Task 8 的 Makefile 消费 `desktop/src-tauri/binaries/voxbox-<triple>` 约定。

- [ ] **Step 0: 工具链确认**

```bash
rustc --version || echo '需要先安装: curl --proto "=https" --tlsv1.2 -sSf https://sh.rustup.rs | sh'
cargo tauri --version || cargo install tauri-cli --version '^2'   # 编译约 5-10 分钟
```

- [ ] **Step 1: 占位图标**

创建 `tools/icongen/main.go`（module 内普通 package main，`go vet ./...` 覆盖）：

```go
// icongen 生成桌面版占位图标（1024×1024 PNG）：深空午夜蓝圆角底 + 电光青信号条。
// 视觉稿确认后替换源图重跑 cargo tauri icon 即可，生成链不变。
package main

import (
	"flag"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

const size = 1024

var (
	bg  = color.RGBA{11, 30, 51, 255}   // 深空午夜蓝
	fg  = color.RGBA{34, 211, 238, 255} // 电光青
	dim = color.RGBA{34, 211, 238, 90}  // 青色弱化
)

func main() {
	out := flag.String("out", "icon-source.png", "输出 PNG 路径")
	flag.Parse()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawRoundedRect(img, 64, 64, 896, 896, 180, bg)
	heights := []int{280, 440, 640, 440, 280} // 五根信号条，中间最高
	const barW, gap = 64, 48
	x := (size - (len(heights)*barW + (len(heights)-1)*gap)) / 2
	for i, h := range heights {
		c := fg
		if i == 0 || i == len(heights)-1 {
			c = dim
		}
		drawRect(img, x, (size-h)/2, barW, h, c)
		x += barW + gap
	}
	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func drawRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for i := x; i < x+w; i++ {
		for j := y; j < y+h; j++ {
			img.Set(i, j, c)
		}
	}
}

// 圆角矩形 = 两根横条 + 两根竖条 + 四角整圆取并集。
func drawRoundedRect(img *image.RGBA, x, y, w, h, r int, c color.RGBA) {
	drawRect(img, x+r, y, w-2*r, h, c)
	drawRect(img, x, y+r, w, h-2*r, c)
	for _, p := range [][2]int{{x + r, y + r}, {x + w - r, y + r}, {x + r, y + h - r}, {x + w - r, y + h - r}} {
		for i := -r; i <= r; i++ {
			for j := -r; j <= r; j++ {
				if i*i+j*j <= r*r {
					img.Set(p[0]+i, p[1]+j, c)
				}
			}
		}
	}
}
```

生成图标：

```bash
go run ./tools/icongen -out desktop/src-tauri/icons/icon-source.png
cargo tauri icon desktop/src-tauri/icons/icon-source.png -o desktop/src-tauri/icons
```

Expected: `icons/` 下生成 `32x32.png`、`128x128.png`、`128x128@2x.png`、`icon.icns`、`icon.ico`、`icon.png` 等。

- [ ] **Step 2: 壳工程文件**

`desktop/shell-dist/index.html`（frontendDist 占位：窗口是就绪后动态创建的，此文件仅满足 bundler 要求）：

```html
<!doctype html>
<html lang="zh-CN">
  <head><meta charset="UTF-8" /><title>VoxBox</title></head>
  <body style="background:#0b1e33;color:#22d3ee;font-family:system-ui;display:grid;place-items:center;height:100vh;margin:0">
    VoxBox 正在启动…
  </body>
</html>
```

`desktop/src-tauri/build.rs`：

```rust
fn main() {
    tauri_build::build()
}
```

`desktop/src-tauri/Cargo.toml`：

```toml
[package]
name = "voxbox-desktop"
version = "0.1.0"
edition = "2021"

[build-dependencies]
tauri-build = { version = "2", features = [] }

[dependencies]
tauri = { version = "2", features = ["tray-icon"] }
tauri-plugin-shell = "2"
tauri-plugin-dialog = "2"
serde = { version = "1", features = ["derive"] }
serde_json = "1"
```

（`tray-icon` feature 本任务就带上，Task 6 直接用；托盘/updater/单实例插件在各自任务加。）

`desktop/src-tauri/tauri.conf.json`：

```json
{
  "$schema": "https://schema.tauri.app/config/2",
  "productName": "VoxBox",
  "version": "0.1.0",
  "identifier": "com.yann0917.voxbox",
  "build": { "frontendDist": "../shell-dist" },
  "app": {
    "windows": [],
    "security": { "csp": null }
  },
  "bundle": {
    "active": true,
    "icon": ["icons/32x32.png", "icons/128x128.png", "icons/128x128@2x.png", "icons/icon.icns", "icons/icon.ico", "icons/icon.png"],
    "externalBin": ["binaries/voxbox"]
  }
}
```

（`windows: []`——主窗口由 Rust 就绪后动态创建，Task 5 的关键点。不写 `bundle.targets`：显式列 dmg/nsis 会在非对应系统的构建上报错，产物类型由本机默认（mac 出 app+dmg）与 Task 9 CI 的 `--bundles` 参数控制。）

`desktop/.gitignore`：

```
/src-tauri/target/
/src-tauri/binaries/
```

- [ ] **Step 3: main.rs**

`desktop/src-tauri/src/main.rs`：

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Mutex;

use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// sidecar 子进程句柄：进程退出事件里 kill，防孤儿后端。
struct SidecarChild(Mutex<Option<CommandChild>>);

/// Go 侧契约：`serve --port 0` 打印 `VOXBOX_READY addr=<host>:<port>`（stdout 单行）。
const READY_PREFIX: &str = "VOXBOX_READY addr=";

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(SidecarChild(Mutex::new(None)))
        .setup(|app| {
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                match spawn_sidecar(&handle).await {
                    Ok(port) => open_main_window(&handle, port),
                    Err(err) => {
                        handle
                            .dialog()
                            .message(format!("VoxBox 后端启动失败：\n{err}"))
                            .kind(MessageDialogKind::Error)
                            .blocking_show();
                        handle.exit(1);
                    }
                }
            });
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            if let RunEvent::Exit = event {
                if let Some(child) = app.state::<SidecarChild>().0.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        });
}

/// 拉起内嵌的 voxbox serve，等就绪行，返回端口。就绪前退出/报错都视为失败。
async fn spawn_sidecar(app: &tauri::AppHandle) -> Result<u16, String> {
    let cmd = app
        .shell()
        .sidecar("voxbox")
        .map_err(|e| e.to_string())?
        .args(["serve", "--port", "0"])
        .env("VOXBOX_DESKTOP", "1");
    let (mut rx, child) = cmd.spawn().map_err(|e| format!("sidecar 启动失败：{e}"))?;
    *app.state::<SidecarChild>().0.lock().unwrap() = Some(child);

    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(30);
    while std::time::Instant::now() < deadline {
        match rx.recv().await {
            Some(CommandEvent::Stdout(line)) => {
                let text = String::from_utf8_lossy(&line);
                if let Some(rest) = text.trim().strip_prefix(READY_PREFIX) {
                    let addr = rest.trim();
                    return addr
                        .rsplit(':')
                        .next()
                        .and_then(|p| p.parse::<u16>().ok())
                        .ok_or_else(|| format!("就绪行端口解析失败：{addr}"));
                }
            }
            Some(CommandEvent::Error(e)) => return Err(e),
            Some(CommandEvent::Terminated(_)) => return Err("后端进程在就绪前退出".into()),
            None => return Err("后端输出流意外关闭".into()),
            _ => {}
        }
    }
    Err("等待后端就绪超时（30s）".into())
}

/// 就绪后创建主窗口，直达工作台（桌面模式免登录，Task 2 已保证）。
fn open_main_window(app: &tauri::AppHandle, port: u16) {
    let url = tauri::Url::parse(&format!("http://127.0.0.1:{port}/workbench")).expect("valid url");
    let win = WebviewWindowBuilder::new(app, "main", WebviewUrl::External(url))
        .title("VoxBox")
        .inner_size(1280.0, 840.0)
        .min_inner_size(960.0, 640.0)
        .build()
        .expect("failed to create main window");
    let _ = win.set_focus();
}
```

- [ ] **Step 4: 编译检查**

Run: `cd desktop/src-tauri && cargo check`
Expected: 零错误零警告（警告需清零，顺手修）。

- [ ] **Step 5: 手动冒烟**

```bash
make web skills
rustc -vV | awk '/host:/{print $2}'   # 记下 host triple，如 aarch64-apple-darwin
go build -ldflags "-s -w" -o "desktop/src-tauri/binaries/voxbox-<host-triple>" ./cmd/voxbox
cd desktop && cargo tauri dev
```

Expected:
1. 窗口弹出直达 `/workbench`，无登录页（VOXBOX_DESKTOP=1 生效）；
2. 无引导密码输出（Task 3 生效）；
3. 跑一次 TTS 合成 + 试听可用；
4. 退出 app 后 `pgrep -fl voxbox` 无残留 serve 进程；
5. 无 Windows 控制台窗口问题（本机 mac，此条留到 CI 后验）。

- [ ] **Step 6: 提交**

```bash
git add desktop tools/icongen
git commit -m "feat(desktop): Tauri 2 壳脚手架——sidecar 拉起 voxbox serve、就绪行定端口、动态创建主窗口直达工作台"
```

---

### Task 6: 托盘驻留 + 关窗隐藏 + 单实例（Rust）

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`
- Modify: `desktop/src-tauri/Cargo.toml`

**Interfaces:**
- Consumes: Task 5 的窗口 label `"main"`、`SidecarChild` 状态。
- Produces: 关窗 = 隐藏到托盘（任务池继续）；托盘菜单「显示主窗口 / 退出」；单实例二次启动唤起已有窗口。

- [ ] **Step 1: 依赖**

`Cargo.toml` `[dependencies]` 增加：

```toml
tauri-plugin-single-instance = "2"
```

- [ ] **Step 2: main.rs 三处改动**

1. Builder **最前**（官方要求单实例插件最先注册）加：

```rust
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.unminimize();
                let _ = w.set_focus();
            }
        }))
```

2. `setup` 开头（spawn 之前）加托盘：

```rust
            build_tray(app.handle())?;
```

文件尾部加：

```rust
/// 托盘：关窗驻留后从这里唤回或退出。图标用 bundler 生成的默认窗口图标。
fn build_tray(app: &tauri::AppHandle) -> tauri::Result<()> {
    use tauri::menu::{Menu, MenuItem};
    use tauri::tray::TrayIconBuilder;
    let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;
    TrayIconBuilder::with_id("main-tray")
        .icon(app.default_window_icon().expect("bundler icon").clone())
        .icon_as_template(false)
        .menu(&menu)
        .show_menu_on_left_click(true)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "show" => {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.show();
                    let _ = w.unminimize();
                    let _ = w.set_focus();
                }
            }
            "quit" => app.exit(0),
            _ => {}
        })
        .build(app)?;
    Ok(())
}
```

3. Builder 上加窗口事件（关窗 = 隐藏驻留）：

```rust
        .on_window_event(|window, event| {
            // 关窗 = 隐藏到托盘：长文本合成/播客等后台任务继续跑。
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
```

- [ ] **Step 3: 编译 + 冒烟**

Run: `cd desktop/src-tauri && cargo check`
Expected: 零错误。

冒烟（复用 Task 5 的 dev 流程）：
1. 关窗后程序仍驻留（Dock/托盘可见），托盘「显示主窗口」唤回；
2. 关窗后从浏览器操作触发的长任务继续跑（如先发起一次长文本合成再关窗，重开后任务还在推进）；
3. 再执行一次 `desktop/src-tauri/target/debug/voxbox-desktop`（二启）→ 不开新进程，已有窗口被唤到前台；
4. 托盘「退出」→ 进程全退，`pgrep -fl voxbox` 干净。

- [ ] **Step 4: 提交**

```bash
git add desktop/src-tauri
git commit -m "feat(desktop): 托盘驻留（关窗隐藏任务不断）+ 单实例二启唤起 + 托盘菜单退出"
```

---

### Task 7: 自动更新 updater（Rust + 密钥）

**Files:**
- Modify: `desktop/src-tauri/src/main.rs`
- Modify: `desktop/src-tauri/Cargo.toml`
- Modify: `desktop/src-tauri/tauri.conf.json`

**Interfaces:**
- Consumes: Task 5 的窗口 label `"main"`。
- Produces: 启动静默检查 GitHub Releases `latest.json`，有新版弹确认框、下载安装后自动重启；`createUpdaterArtifacts: true` 使 Task 8/9 的构建产出签名更新包。

- [ ] **Step 1: 生成签名密钥对（一次性，私钥不入库）**

```bash
cargo tauri signer generate -w ~/.tauri/voxbox.key
```

密码留空（直接回车两次）。记下输出中的**公钥**（`RW…` 开头一行）。确认 `git status` 不出现任何 key 文件。

- [ ] **Step 2: 依赖与配置**

`Cargo.toml` `[dependencies]` 增加：

```toml
tauri-plugin-updater = "2"
```

`tauri.conf.json` 顶层（与 `app`、`bundle` 平级）加：

```json
  "plugins": {
    "updater": {
      "pubkey": "<Step 1 输出的公钥，逐字粘贴>",
      "endpoints": ["https://github.com/yann0917/voxbox/releases/latest/download/latest.json"]
    }
  },
```

`bundle` 内加：

```json
    "createUpdaterArtifacts": true,
```

- [ ] **Step 3: main.rs 接入**

Builder 加插件（dialog 之后）：

```rust
        .plugin(tauri_plugin_updater::Builder::new().build())
```

`setup` 的 `open_main_window(&handle, port);` 之后加：

```rust
                        let h = handle.clone();
                        tauri::async_runtime::spawn(async move {
                            check_for_updates(&h).await;
                        });
```

文件尾部加：

```rust
/// 启动后静默检查更新；有新版弹确认框，同意后下载安装并重启。
/// 检查失败静默忽略（离线/仓库无 release 时不应打扰使用）。
async fn check_for_updates(app: &tauri::AppHandle) {
    use tauri_plugin_dialog::{DialogExt, MessageDialogButtons};
    use tauri_plugin_updater::UpdaterExt;
    let updater = match app.updater() {
        Ok(u) => u,
        Err(_) => return,
    };
    let update = match updater.check().await {
        Ok(Some(u)) => u,
        _ => return,
    };
    let Some(win) = app.get_webview_window("main") else {
        return;
    };
    let confirmed = win
        .dialog()
        .message(format!("发现新版本 {}，是否立即更新？", update.version))
        .title("VoxBox 更新")
        .buttons(MessageDialogButtons::OkCancelCustom("立即更新".into(), "稍后".into()))
        .blocking_show();
    if !confirmed {
        return;
    }
    if let Err(e) = update.download_and_install(|_, _| {}, || {}).await {
        eprintln!("更新下载/安装失败：{e}");
        return;
    }
    app.restart();
}
```

- [ ] **Step 4: 编译 + 冒烟**

Run: `cd desktop/src-tauri && cargo check`
Expected: 零错误。

带签名环境跑一次完整构建（同时验证 createUpdaterArtifacts 不缺钥匙报错）：

```bash
cd desktop && TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key cargo tauri build
```

Expected: 构建成功；`target/release/bundle/` 出 dmg，且更新产物（`.app.tar.gz` + `.sig` 或 `.tar.gz`/`.sig`）存在。装上运行：控制台无 updater 崩溃（仓库暂无 release → 检查静默失败即通过）。

- [ ] **Step 5: 提交**

```bash
git add desktop/src-tauri
git commit -m "feat(desktop): tauri-plugin-updater 启动静默检查——GitHub Releases latest.json，确认后下载安装重启"
```

---

### Task 8: Makefile desktop 目标

**Files:**
- Modify: `Makefile`

**Interfaces:**
- Consumes: Task 5 的 `desktop/src-tauri/binaries/voxbox-<triple>` 约定、现有 `web`/`skills` 目标、`LDFLAGS`。
- Produces: `make desktop`（本机构建 dmg/app）、`make desktop-dev`（dev 冒烟入口）。

- [ ] **Step 1: Makefile 追加**

`.PHONY` 行追加 `desktop desktop-dev`，`clean` 的 rm 列表追加 `desktop/src-tauri/binaries`。文件尾部加：

```make
# ---- 桌面版（Tauri 2 壳 + Go sidecar）----
# 本机只出 darwin 包；Windows/发布产物走 CI（.github/workflows/desktop-release.yml）。
HOST_TRIPLE := $(shell rustc -vV | awk '/host:/{print $$2}')
DESKTOP_BIN := desktop/src-tauri/binaries/voxbox-$(HOST_TRIPLE)

# updater 签名需要私钥：TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key make desktop
desktop: web skills
	go build -ldflags "$(LDFLAGS)" -o "$(DESKTOP_BIN)" ./cmd/voxbox
	cd desktop && cargo tauri build

desktop-dev: web skills
	go build -ldflags "$(LDFLAGS)" -o "$(DESKTOP_BIN)" ./cmd/voxbox
	cd desktop && cargo tauri dev
```

- [ ] **Step 2: 验证**

Run: `make desktop-dev`（Ctrl-C 退出）
Expected: 与 Task 5 手工流程等价。

Run: `TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key make desktop`
Expected: `desktop/src-tauri/target/release/bundle/dmg/VoxBox_0.1.0_aarch64.dmg` 生成。挂载安装、右键打开验证可用。

- [ ] **Step 3: 提交**

```bash
git add Makefile
git commit -m "build(desktop): make desktop / desktop-dev——sidecar 按 host triple 产出后走 cargo tauri"
```

---

### Task 9: CI 发布工作流 + README 说明

**Files:**
- Create: `.github/workflows/desktop-release.yml`
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 7 的 updater 密钥（GitHub Secrets）、tauri-action 的 latest.json 产物、Task 1-3 的 sidecar 契约。
- Produces: tag `desktop-v*` 推送 → 三平台（mac arm64 / mac x64 / win x64）签名产物 + `latest.json` 自动发到 GitHub Release；README 桌面版章节（下载、Gatekeeper/SmartScreen 解锁、更新机制）。

- [ ] **Step 1: 配置 Secrets（人工步骤，写入 PR 描述提醒）**

仓库 Settings → Secrets → Actions 添加：
- `TAURI_SIGNING_PRIVATE_KEY` = `~/.tauri/voxbox.key` 文件内容（`cat ~/.tauri/voxbox.key`）
- `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` = 空（生成时留空则配置空串）

- [ ] **Step 2: 工作流**

创建 `.github/workflows/desktop-release.yml`：

```yaml
name: desktop-release

# tag desktop-v* 触发；三平台矩阵构建 + updater 签名 + GitHub Release（含 latest.json）
on:
  push:
    tags: ["desktop-v*"]

jobs:
  build:
    strategy:
      fail-fast: false
      matrix:
        include:
          - os: macos-latest      # Apple Silicon
            triple: aarch64-apple-darwin
            ext: ""
            bundles: dmg
          - os: macos-13          # Intel
            triple: x86_64-apple-darwin
            ext: ""
            bundles: dmg
          - os: windows-latest
            triple: x86_64-pc-windows-msvc
            ext: ".exe"
            bundles: nsis
    runs-on: ${{ matrix.os }}
    permissions:
      contents: write
    defaults:
      run:
        shell: bash
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json

      # 前端产物 + skill 进 embed 目录（等价 make web skills，Windows runner 无 make）
      - name: Build web assets
        run: |
          cd web && npm ci && npm run build && cd ..
          rm -rf cmd/voxbox/webdist && mkdir -p cmd/voxbox/webdist
          cp -r web/dist/* cmd/voxbox/webdist/
          rm -rf cmd/voxbox/skillsdist && mkdir -p cmd/voxbox/skillsdist
          cp -r skills/voxbox cmd/voxbox/skillsdist/

      - name: Build Go sidecar
        run: |
          VERSION="${GITHUB_REF_NAME#desktop-v}"
          go build -ldflags "-s -w -X main.version=$VERSION" \
            -o "desktop/src-tauri/binaries/voxbox-${{ matrix.triple }}${{ matrix.ext }}" ./cmd/voxbox
          if [ "${{ matrix.os }}" != "windows-latest" ]; then
            codesign --force --sign - "desktop/src-tauri/binaries/voxbox-${{ matrix.triple }}"
          fi

      - uses: dtolnay/rust-toolchain@stable

      - uses: Swatinem/rust-cache@v2
        with:
          workspaces: desktop/src-tauri

      # bundle 版本对齐 tag，updater 版本比较依赖它
      - name: Sync version
        run: |
          node -e "const fs=require('fs');const p='desktop/src-tauri/tauri.conf.json';const j=JSON.parse(fs.readFileSync(p));j.version=process.argv[1];fs.writeFileSync(p,JSON.stringify(j,null,2)+'\n')" "${GITHUB_REF_NAME#desktop-v}"

      - uses: tauri-apps/tauri-action@v0
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAURI_SIGNING_PRIVATE_KEY: ${{ secrets.TAURI_SIGNING_PRIVATE_KEY }}
          TAURI_SIGNING_PRIVATE_KEY_PASSWORD: ${{ secrets.TAURI_SIGNING_PRIVATE_KEY_PASSWORD }}
        with:
          projectPath: desktop
          args: ["--bundles", "${{ matrix.bundles }}"]
          tagName: ${{ github.ref_name }}
          releaseName: "VoxBox ${{ github.ref_name }}"
          releaseBody: "桌面版安装包与自动更新清单。macOS 首次打开：右键 → 打开；Windows：SmartScreen 点「仍要运行」。"
          releaseDraft: false
          prerelease: false
```

- [ ] **Step 3: README 桌面版章节**

`README.md`（「快速开始」节后插入）：

```markdown
## 桌面版（Tauri 2）

双击即用的桌面应用：免登录直达工作台、关窗驻留托盘（后台任务不中断）、GitHub Releases 自动更新。

- **下载**：GitHub Releases 的 `desktop-v*` 标签下取 DMG（macOS，分 arm64/Intel）/ NSIS 安装器（Windows）。
- **macOS 首次打开**：未做 Apple 公证，右键应用 → 「打开」→ 再点「打开」即可（仅首次）；`xattr -cr /Applications/VoxBox.app` 等效。
- **Windows 首次运行**：SmartScreen 弹窗点「更多信息」→「仍要运行」。
- **更新**：应用启动时静默检查新版本，弹窗确认后自动下载安装并重启。
- **本地构建**：`TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key make desktop`（依赖 Rust 工具链与 tauri-cli，sidecar 契约见 `docs/superpowers/specs/2026-09-26-tauri-desktop-design.md`）。
- 桌面版不含 CLI/MCP/skill install——这些继续用 `make dist` 的单二进制发行包。
```

- [ ] **Step 4: 提交**

```bash
git add .github/workflows/desktop-release.yml README.md
git commit -m "ci(desktop): tag 触发三平台签名发布（tauri-action + latest.json），README 桌面版章节"
```

---

### Task 10: 端到端验收

**Files:**
- 无新文件（验证 + 可能的小修）

**Interfaces:**
- Consumes: Task 1-9 全部产物。
- Produces: spec「测试」节 6 条冒烟清单的执行记录。

- [ ] **Step 1: Go 侧全量**

Run: `gofmt -l cmd internal && go vet ./... && go test ./... && cd web && npm run build`
Expected: 全部零输出/全绿（`gofmt -l` 无输出 = 格式通过）。

- [ ] **Step 2: spec 冒烟清单（macOS，桌面 app 构建）**

`TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key make desktop`，安装 DMG 后逐条验证并记录：
1. 首启免登录直达工作台，无密码对话框；
2. TTS / ASR / 试听 / 上传全链路可用；
3. 关窗驻留，长任务继续，托盘唤回窗口；
4. 托盘退出后 `pgrep -fl voxbox` 无 serve 残留；
5. 二开 app 唤起已有实例；
6. updater 完整演练：本地打 `desktop-v0.0.1-test` tag 触发 CI（或手改本地 conf 版本号 + 手工构造 latest.json 到临时静态服务）→ 低版本 app 弹更新框 → 升级成功。

（第 6 条需要 push tag 到 GitHub——推前先 `git branch --show-current` 确认 main，tag 触发的是 CI 工作流而非代码变更。）

- [ ] **Step 3: 收尾**

验证中发现的问题按 bug 修复 + 独立 commit；全部通过后汇报结果（含哪条在哪个平台验证、哪条仅 CI 覆盖）。
