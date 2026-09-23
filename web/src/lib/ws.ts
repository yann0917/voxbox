import { useEffect, useRef, useState } from "react";
import { create } from "zustand";

export interface TaskEvent {
  type: "progress" | "done" | "error" | "canceled" | "task.snapshot";
  task_id?: string;
  provider?: string;
  tool?: string;
  progress?: number;
  note?: string;
  error?: string;
  tasks?: unknown[];
  // 仅 progress 事件携带的工具自定义展示数据（后端 task.Event.Detail，如播客对话流轮次）。
  detail?: { round_id?: number; speaker?: string; text?: string; rounds_done?: number; task_id?: string; poll?: number; status?: string; chunks?: number };
}

export type WSStatus = "connecting" | "open" | "closed";

interface WSStore {
  last: TaskEvent | null;
  status: WSStatus;
}

// 全局单例连接（与播放器单实例同一模式）：所有页面与顶栏状态徽标共用一条 WS，
// 切页不再断开重连。连接本身即存活信号——WS 经 HTTP 升级建立，连着就说明服务在，
// 故顶栏不再轮询 /api/health；半开/僵死连接由服务端 ping/pong 读超时回收。
const useWSStore = create<WSStore>(() => ({ last: null, status: "connecting" }));

// 事件逐条回调订阅：store.last 只保留最新一条，快速连发的两条事件会互相覆盖
//（页面取 last 无碍，全局通知类消费方一条都不能丢），故提供逐条总线。
const eventSubs = new Set<(ev: TaskEvent) => void>();

/** 订阅每一条任务事件，返回退订函数。首次订阅会顺带建立 WS 连接。 */
export function onTaskEvent(cb: (ev: TaskEvent) => void): () => void {
  ensureConnection();
  eventSubs.add(cb);
  return () => void eventSubs.delete(cb);
}

let started = false;
function ensureConnection() {
  if (started) return;
  started = true;
  const connect = () => {
    useWSStore.setState({ status: "connecting" });
    // 同源相对连接：dev 经 vite 代理（ws: true），https 部署自动用 wss
    const wsProto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${wsProto}//${location.host}/api/ws`);
    ws.onopen = () => useWSStore.setState({ status: "open" });
    ws.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data) as TaskEvent;
        useWSStore.setState({ last: ev });
        eventSubs.forEach((cb) => cb(ev));
      } catch {
        // 忽略坏帧
      }
    };
    ws.onclose = () => {
      useWSStore.setState({ status: "closed" });
      // 公开页（展示页/登录页）上 WS 会被鉴权门 401 拒绝：不再重试并复位单例，
      // 登录后回到应用路由时由订阅方重新触发连接。
      if (location.pathname === "/" || location.pathname.startsWith("/login")) {
        started = false;
        return;
      }
      setTimeout(connect, 2000);
    };
  };
  connect();
}

export function useTaskEvents(): TaskEvent | null {
  ensureConnection();
  const last = useWSStore((s) => s.last);
  const [ev, setEv] = useState<TaskEvent | null>(null);
  // 挂载时 store 里已有的事件属"历史"不下发，保持旧 hook"只收挂载后事件"的语义
  //（消费方都以 ev.task_id === taskId 过滤，此为双保险）。
  const baseline = useRef(last);
  useEffect(() => {
    if (last && last !== baseline.current) setEv(last);
  }, [last]);
  return ev;
}

/** 事件通道连接状态，顶栏徽标消费。 */
export function useWSStatus(): WSStatus {
  ensureConnection();
  return useWSStore((s) => s.status);
}
