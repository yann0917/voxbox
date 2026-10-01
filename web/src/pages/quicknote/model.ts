import { fetchJSON } from "../../lib/api";
import { streamPostSSE } from "../../lib/sse";
import type { RefineEvent, RefineTodo, TaskDetail, TaskStatus } from "../../lib/types";

/** 转写分句：speaker 为上游说话人编号（"0"/"1"… 字符串，未启用分离时缺省） */
export type QNSegment = { text: string; start_ms: number; end_ms: number; speaker?: string };

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO（与语音识别/妙记页同一结构） */
export interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

/** 结果区视图：空闲 / 提交中 / 转写中 / 完成 / 出错 */
export type ResultView = "empty" | "submitting" | "progress" | "done" | "error";

/** 终态回读：进度通道收到收尾事件后拉任务详情（分句文字稿在 task.summary.segments） */
export function loadTask(id: string): Promise<TaskDetail> {
  return fetchJSON<TaskDetail>(`/api/tasks/${id}`);
}

/** 说话人统计：每人的发言轮数 / 时长 / 占比（无说话人标注的分句不计入） */
export function speakerStats(segs: QNSegment[]) {
  const m = new Map<string, { turns: number; ms: number }>();
  for (const s of segs) {
    if (!s.speaker) continue;
    const e = m.get(s.speaker) ?? { turns: 0, ms: 0 };
    e.turns++;
    e.ms += Math.max(0, s.end_ms - s.start_ms);
    m.set(s.speaker, e);
  }
  const total = [...m.values()].reduce((a, b) => a + b.ms, 0) || 1;
  return [...m].map(([id, v]) => ({ id, ...v, pct: v.ms / total }));
}

/** 文稿全文：逐行「说话人N：文本」（无说话人标注的句子只有文本），供复制与 .md 导出。 */
export function transcriptText(segs: QNSegment[]): string {
  return segs.map((s) => (s.speaker ? `说话人${s.speaker}：${s.text}` : s.text)).join("\n");
}

/* ---------- 加工区（/api/refine）：模式、请求与结果归一 ---------- */

export type RefineMode = "summary" | "todos" | "events" | "custom";

export const REFINE_MODES: { value: RefineMode; label: string }[] = [
  { value: "summary", label: "总结" },
  { value: "todos", label: "待办" },
  { value: "events", label: "事件" },
  { value: "custom", label: "自定义" },
];

export interface RefineReq {
  task_id: string;
  mode: RefineMode;
  /** mode=custom 必填（后端校验非空），其余模式不带 */
  instruction?: string;
  provider: string;
  model: string;
}

/** 流式加工：SSE 协议与提示词 apply 一致（预检 JSON 包络、流内 error 事件、done 终止），
 *  解析复用 lib/sse.streamPostSSE。 */
export function streamRefine(req: RefineReq, onDelta: (delta: string) => void, signal?: AbortSignal) {
  return streamPostSSE("/api/refine", req, onDelta, signal);
}

/** 与后端 parseJSONArray 同规则的宽容解析：容忍 markdown 代码栅栏包裹，仅接受数组。
 *  加工完成流后本地归一 todos/events（结果以后端落盘为准，此处保持同一判定口径）。 */
export function parseRefinedArray(raw: string): unknown[] | null {
  let s = raw.trim();
  if (s.startsWith("```")) {
    const i = s.indexOf("\n");
    if (i < 0) return null;
    s = s.slice(i + 1).trim().replace(/```$/, "").trim();
  }
  if (!s) return null;
  try {
    const v: unknown = JSON.parse(s);
    return Array.isArray(v) ? v : null;
  } catch {
    return null;
  }
}

/** 单模式的加工值形态：summary/custom 为 markdown 文本，todos/events 为结构化数组
 *  （解析失败时持久层存原文字符串，回显直接当文本展示并给重试入口）。 */
export type ModeValue = string | RefineTodo[] | RefineEvent[];

/** 宽容归一 todos：字段缺失/类型漂移按空串兜底（LLM 输出不保证字段齐整）。 */
export function asTodos(v: unknown[]): RefineTodo[] {
  return v.map((x) => {
    const o = (x ?? {}) as Record<string, unknown>;
    return {
      content: String(o.content ?? ""),
      owner: String(o.owner ?? ""),
      due: String(o.due ?? ""),
    };
  });
}

/** 宽容归一 events：同上；end 空串表示无结束时间（导出 ICS 时省略 DTEND）。 */
export function asEvents(v: unknown[]): RefineEvent[] {
  return v.map((x) => {
    const o = (x ?? {}) as Record<string, unknown>;
    return {
      title: String(o.title ?? ""),
      start: String(o.start ?? ""),
      end: String(o.end ?? ""),
      description: String(o.description ?? ""),
    };
  });
}

/** RFC3339 → 「YYYY-MM-DD HH:mm」（本地展示截断，不引入时区换算）。 */
export function refinedAtText(rfc3339: string | undefined): string {
  return rfc3339 ? rfc3339.slice(0, 16).replace("T", " ") : "";
}

/** 文件名安全化：路径与系统保留字符替换为连字符（事件名/任务标题直接做下载名）。 */
export function safeFilename(name: string): string {
  return name.replace(/[/\\:*?"<>|]/g, "-").trim() || "录音笔记";
}
