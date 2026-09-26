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
        // 单实例必须最先注册（官方要求）：二启进程立即退出，回调里唤起已有主窗口。
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.unminimize();
                let _ = w.set_focus();
            }
        }))
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .manage(SidecarChild(Mutex::new(None)))
        .on_window_event(|window, event| {
            // 关窗 = 隐藏到托盘：长文本合成/播客等后台任务继续跑。
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .setup(|app| {
            // 托盘要在 spawn 之前就位：后端失败弹窗期间用户也有可操作的驻留入口。
            build_tray(app.handle())?;
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                match spawn_sidecar(&handle).await {
                    Ok(port) => {
                        open_main_window(&handle, port);
                        let h = handle.clone();
                        tauri::async_runtime::spawn(async move {
                            check_for_updates(&h).await;
                        });
                    }
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

    // 就绪等待必须有整体 deadline：挂死的后端（无输出也不退出）会让 recv() 无限期阻塞，
    // 靠循环条件自己查时钟永远轮不到——超时要用 tokio 定时器从外部打断，而不是循环内自查。
    let wait_ready = async move {
        loop {
            match rx.recv().await {
                Some(CommandEvent::Stdout(line)) => {
                    let text = String::from_utf8_lossy(&line);
                    if let Some(rest) = text.trim().strip_prefix(READY_PREFIX) {
                        let addr = rest.trim();
                        break addr
                            .rsplit(':')
                            .next()
                            .and_then(|p| p.parse::<u16>().ok())
                            .ok_or_else(|| format!("就绪行端口解析失败：{addr}"));
                    }
                }
                Some(CommandEvent::Error(e)) => break Err(e),
                Some(CommandEvent::Terminated(_)) => break Err("后端进程在就绪前退出".into()),
                None => break Err("后端输出流意外关闭".into()),
                _ => {}
            }
        }
    };
    match tokio::time::timeout(std::time::Duration::from_secs(30), wait_ready).await {
        Ok(result) => result,
        // 超时与其它失败同路：错误对话框 + exit(1)，不留静默白屏。
        Err(_) => Err("等待后端就绪超时（30s）".into()),
    }
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

/// 启动后静默检查更新；有新版弹确认框，同意后下载安装并重启。
/// 检查失败静默忽略（离线/仓库无 release 时不应打扰使用）；下载/安装失败弹窗反馈，
/// 留在当前版本继续可用。
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
        .buttons(MessageDialogButtons::OkCancelCustom(
            "立即更新".into(),
            "稍后".into(),
        ))
        .blocking_show();
    if !confirmed {
        return;
    }
    if let Err(e) = update.download_and_install(|_, _| {}, || {}).await {
        // 失败必须有可见反馈（stderr 用户看不到）：弹窗告知后停在当前版本继续可用。
        win.dialog()
            .message(format!("更新下载/安装失败：\n{e}"))
            .title("VoxBox 更新")
            .kind(MessageDialogKind::Error)
            .blocking_show();
        return;
    }
    // app.restart() 必须在非主线程调用才会先触发 RunEvent::Exit（回调里 kill sidecar）
    // 再重启进程——check_for_updates 跑在 async task 即满足，勿挪到主线程（会跳过 Exit 事件）。
    app.restart();
}
