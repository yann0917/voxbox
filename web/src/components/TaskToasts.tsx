import { useEffect } from "react";
import { onTaskEvent } from "../lib/ws";
import { toolLabel } from "../lib/toolNames";
import { useToast } from "../ui";

/** 系统通知开关的 localStorage 键（设置页写入："on"/"off"）。 */
export const SYS_NOTIFY_KEY = "sysnotify";

const TERMINAL = new Set(["done", "error", "canceled"]);
const NOT_RUNNING = new Set(["succeeded", "failed", "canceled", "interrupted"]);
const BASE_TITLE = "voxbox";

/** 标题角标：非终态任务数进浏览器标签页标题——切走标签页也能瞥见任务进展。 */
function useTitleBadge() {
  useEffect(() => {
    const active = new Set<string>();
    const apply = () => {
      document.title = active.size > 0 ? `(${active.size}) ${BASE_TITLE}` : BASE_TITLE;
    };
    apply();
    return onTaskEvent((ev) => {
      if (ev.type === "task.snapshot") {
        active.clear();
        for (const t of (ev.tasks ?? []) as { id: string; status: string }[]) {
          if (!NOT_RUNNING.has(t.status)) active.add(t.id);
        }
      } else if (ev.type === "progress") {
        if (ev.task_id) active.add(ev.task_id);
      } else if (TERMINAL.has(ev.type)) {
        if (ev.task_id) active.delete(ev.task_id);
      }
      apply();
    });
  }, []);
}

/** 系统通知：开关打开（设置页授权）且页面处于后台时，任务终态发系统级通知——
 *  页面内 toast 在切走标签页/最小化时不可见，系统通知补足这一层。 */
function notifyTerminal(title: string, body: string, taskId: string) {
  if (!document.hidden) return;
  if (localStorage.getItem(SYS_NOTIFY_KEY) !== "on") return;
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  try {
    const n = new Notification(title, { body, tag: taskId });
    n.onclick = () => {
      window.focus();
      n.close();
    };
  } catch {
    // 通知构造失败（如环境限制）静默忽略：页面内 toast 始终可用
  }
}

/** 全局任务通知：订阅 WS 事件流，终态（完成/失败/取消）弹 toast + 系统通知，
 *  并把非终态任务数映射为标签页标题角标。
 *  progress 不弹——推送高频且页面内已有进度条；长任务提交后可离开页面，靠此获知结果。
 *  必须挂在 ToastProvider 内（Layout 满足）。 */
export default function TaskToasts() {
  const { toast } = useToast();
  useTitleBadge();
  useEffect(
    () =>
      onTaskEvent((ev) => {
        if (ev.type !== "done" && ev.type !== "error" && ev.type !== "canceled") return;
        const tool = ev.tool ? toolLabel(ev.tool) : "";
        const name = tool ? `${tool}任务` : "任务";
        const ref = ev.task_id ? `任务 ${ev.task_id.slice(0, 8)}` : "";
        let title: string;
        let body: string;
        switch (ev.type) {
          case "done":
            title = `${name}完成`;
            body = ref;
            toast({ tone: "ok", title, description: ref });
            break;
          case "error":
            // 描述给具体错误（可能较长，Toast 已 break-words）
            title = `${name}失败`;
            body = [ref, ev.error].filter(Boolean).join("：");
            toast({ tone: "error", title, description: body });
            break;
          default:
            title = `${name}已取消`;
            body = ref;
            toast({ tone: "warn", title, description: ref });
            break;
        }
        notifyTerminal(title, body, ev.task_id ?? "");
      }),
    [toast]
  );
  return null;
}
