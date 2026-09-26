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
