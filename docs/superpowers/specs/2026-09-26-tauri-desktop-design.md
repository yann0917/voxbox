# Tauri 2 桌面壳设计

日期：2026-09-26
状态：已确认（用户已批准各节设计）

## 背景与目标

voxbox 目前是单 Go 二进制（CLI + `voxbox serve` Web 控制台），前端产物 go:embed 内嵌、由同一进程伺服（默认 `127.0.0.1:8081`），纯 Go SQLite、零外部依赖。

目标：用 Tauri 2 包装为桌面应用，支持 **macOS + Windows**，**小范围分发**（朋友/同事）。桌面形态做到：

- 双击启动即用，免登录直达工作台（无 Landing 页、无登录页）
- 系统托盘驻留：关窗不退出，后台任务（长文本合成/播客等异步任务池）继续跑
- 自动更新（tauri-plugin-updater + GitHub Releases）

## 非目标

- 桌面版暴露 CLI / MCP / skill install 入口（继续走原发行包，互不干扰）
- Apple Developer ID 签名与公证（可选后续，$99/年）
- Linux 打包
- 桌面专用前端构建变体（**保持单一前端构建**，go:embed 仍是唯一事实来源）

## 总体架构（方案 A：sidecar + localhost）

新增 `desktop/src-tauri` 目录（Tauri 2 标准结构：Rust 壳 + `tauri.conf.json`）。Go 二进制以 sidecar 形式打进应用包（`bundle.externalBin`，按 target-triple 命名，如 `voxbox-aarch64-apple-darwin` / `voxbox-x86_64-pc-windows-msvc.exe`）。

启动顺序：

1. Rust 壳 spawn sidecar：`voxbox serve --port 0`（环境变量 `VOXBOX_DESKTOP=1`）
2. 从 stdout 读机器可读就绪行 `VOXBOX_READY addr=127.0.0.1:NNNN`
3. 创建主窗口，指向 `http://127.0.0.1:NNNN/workbench`

页面内**不使用任何 Tauri JS API**；壳级逻辑（进程管理、托盘、更新检查）全部在 Rust 侧。Web 端与浏览器打开时的行为完全一致（同源 WebSocket/上传/下载/试听）。

落选方案备查：B（前端同时打包进 Tauri，tauri:// 加载 + 跨源 API——前端两份来源、CORS/WS 改造面大，无收益）；C（Wails v3——点名要求 Tauri，且 v3 beta、生态弱）。

## Go 侧改动（约 20 行 + 测试）

文件：`cmd/voxbox/serve.go`、`internal/server/auth.go`（或新增 desktop 中间件）、`internal/server/routes.go`。

1. **`--port` 语义**：flag 默认值 0 → -1（-1 = 未指定、沿用配置文件，现有行为不变）；`--port 0` 赋予新语义「系统分配空闲端口」。避免与手动运行的 `voxbox serve`（8081）撞端口。
2. **就绪行**：监听绑定成功后向 stdout 打一行 `VOXBOX_READY addr=<host:port>`；现有人类可读日志全部不动。
3. **`VOXBOX_DESKTOP=1` 桌面模式**：
   - 鉴权中间件对未登录请求**以库内 admin 用户身份**注入 Principal（`MustChangePassword=false`），首启强制改密流程自然跳过——桌面版创建的任务归属、搜索范围与 admin 用户行一致；
   - `EnsureBootstrapAdmin` 仍创建 admin 用户行（任务归属、搜索范围依赖真实用户 ID），但不再向 stderr 打印初始密码；
   - `/api/auth/me` 响应附 `desktop: true` 字段，供前端运行时判断形态。

单测：

- `--port 0` 启动后就绪行可解析、解析出的端口真实可连；
- 桌面模式下未登录请求 `/api/auth/me` 返回 admin 身份 + `desktop:true`；
- 非桌面模式（默认）行为回归不变。

**安全边界说明**：服务仅绑 `127.0.0.1`；本机任意进程本可直读 SQLite 与配置文件，放开 localhost 鉴权无新增攻击面。MCP 端点（同端口）在桌面模式下同样免鉴权，同一边界内。

## 前端改动（web/src，单一构建）

- `/api/auth/me` 返回 `desktop:true` 时：`/` 与 `/login` 路由重定向到 `/workbench`（LandingPage、LoginPage 在桌面形态不可达）；隐藏退出按钮与设置页账号区（Layout / SettingsPage 相关 UI 按能力显隐）。
- 其余页面、构建链、字体/主题完全不动。

## 壳行为（Rust 侧）

- **单实例**：`tauri-plugin-single-instance`，二次启动唤起已有窗口而非开新进程（sidecar 不会双开）。
- **托盘**：关闭窗口 = 隐藏到托盘，任务池继续运行；托盘菜单：显示主窗口 / 退出。
- **退出**：托盘退出或应用退出时可靠终止 sidecar（含 panic/异常退出路径，进程组清理）。
- **sidecar 意外退出**：弹原生错误框提示（不静默白屏）。
- **更新**：`tauri-plugin-updater`，启动后静默检查 GitHub Releases 的 `latest.json`，有新版弹确认框，用户同意后下载安装。

## 打包与分发

- **macOS**：DMG（ad-hoc 签名）。未签名包首次打开需「右键 → 打开」绕过 Gatekeeper，分发说明写清。
- **Windows**：NSIS 安装器。未签名有 SmartScreen 警告，说明「更多信息 → 仍要运行」。
- **CI（必做）**：GitHub Actions 矩阵（`macos-latest` + `windows-latest`），tag 触发：构建 Go sidecar → `tauri build` → minisign 签名 → 发布 GitHub Release + `latest.json`。`TAURI_SIGNING_PRIVATE_KEY` 只存 GitHub Secrets，仓库与产物不含私钥。
- **图标**：一张 1024px PNG 经 `tauri icon` 生成全平台尺寸；视觉按「深空信号站」调性（午夜蓝底 + 电光青信号元素），**待用户确认视觉稿后实施**。

## 构建与开发流

- `make desktop`：`web + skills` → 按 host triple 编 Go sidecar 到 `src-tauri/binaries/` → `cargo tauri build`。
- `make desktop-dev`：`tauri dev`（用本机构建的 sidecar），验证壳行为（窗口/托盘/更新/进程）。
- 日常前端迭代完全不变：vite dev + 浏览器（已有 `/api` → `localhost:8081` 代理）。

## 测试

- Go 单测见上；Rust 侧以手动冒烟为主（GUI 自动化不在范围）。
- 手动冒烟清单：
  1. 首启免登录直达工作台，无密码对话框；
  2. TTS / ASR / 试听 / 上传全链路可用；
  3. 关窗驻留，长任务继续，托盘唤回窗口；
  4. 退出后 sidecar 进程确实终止（活动监视器/任务管理器确认）；
  5. 双开唤起已有实例；
  6. updater 用低版本号假 release 演练一次完整升级。

## 已确认决策记录

| 决策点 | 结论 |
|---|---|
| 用途/分发 | 小范围分发（朋友/同事） |
| 平台 | macOS + Windows |
| 能力边界 | 仅 Web 控制台，CLI/MCP 走原发行包 |
| 集成方案 | A：sidecar + localhost（弃 B 双份打包、C Wails） |
| 托盘 / updater | 必做（用户明确要求） |
| 登录 | 桌面形态免登录直达工作台，无 Landing 页 |
