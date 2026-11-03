import { streamPostSSE } from "../../lib/sse";
import type { MinutesNotes, RefineEvent, RefineTodo } from "../../lib/types";

/** 转写分句：speaker 为上游说话人编号（"0"/"1"… 字符串，未启用分离时缺省） */
export type QNSegment = { text: string; start_ms: number; end_ms: number; speaker?: string };

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

/* ---------- 加工区（/api/refine）：模式、请求与结果归一 ---------- */

export type RefineMode = "minutes" | "todos" | "events" | "mindmap" | "custom";

export const REFINE_MODES: { value: RefineMode; label: string }[] = [
  { value: "minutes", label: "纪要" },
  { value: "mindmap", label: "导图" },
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

/** 与后端 parseJSONArray/parseJSONObject 同规则的宽容解析：容忍 markdown 代码栅栏
 *  包裹，仅接受数组/对象。加工完成流后本地归一 todos/events/minutes（结果以后端
 *  落盘为准，此处保持同一判定口径）。 */
export function parseRefinedArray(raw: string): unknown[] | null {
  const s = stripJSONFence(raw);
  if (!s) return null;
  try {
    const v: unknown = JSON.parse(s);
    return Array.isArray(v) ? v : null;
  } catch {
    return null;
  }
}

export function parseRefinedObject(raw: string): Record<string, unknown> | null {
  const s = stripJSONFence(raw);
  if (!s) return null;
  try {
    const v: unknown = JSON.parse(s);
    return v !== null && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/** 剥掉大模型输出外层的 markdown 代码栅栏（与后端 stripJSONFence 同规则）。 */
function stripJSONFence(raw: string): string {
  let s = raw.trim();
  if (s.startsWith("```")) {
    const i = s.indexOf("\n");
    if (i < 0) return "";
    s = s
      .slice(i + 1)
      .trim()
      .replace(/```$/, "")
      .trim();
  }
  return s;
}

/** 单模式的加工值形态：minutes 为结构化纪要对象、todos/events 为结构化数组、
 *  mindmap/custom 为 markdown 文本（mindmap 由 Mindmap 组件渲染成导图树）；
 *  解析失败时持久层存原文字符串，回显直接当文本展示并给重试入口。 */
export type ModeValue = string | RefineTodo[] | RefineEvent[] | MinutesNotes;

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

/** 毫秒时间戳宽容归一：非正整数按缺省处理（evidence 无法定位的条目无 t）。 */
function asT(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) && v > 0 ? v : undefined;
}

/** 宽容归一 minutes：LLM 输出字段缺失/类型漂移按空值兜底（与 todos/events 同思路），
 *  t 由后端 evidence 解析注入，此处只透传。 */
export function asMinutes(o: Record<string, unknown>): MinutesNotes {
  const arr = (v: unknown) => (Array.isArray(v) ? v : []);
  const str = (v: unknown) => (typeof v === "string" ? v : undefined);
  return {
    title: str(o.title) || undefined,
    summary: str(o.summary) || undefined,
    topics: arr(o.topics).map((x) => {
      const t = (x ?? {}) as Record<string, unknown>;
      return {
        title: String(t.title ?? ""),
        summary: str(t.summary),
        t: asT(t.t),
        points: arr(t.points).map((p) => {
          const pm = (p ?? {}) as Record<string, unknown>;
          return { text: String(pm.text ?? ""), t: asT(pm.t) };
        }),
      };
    }),
    conclusions: arr(o.conclusions).map((x) => {
      const c = (x ?? {}) as Record<string, unknown>;
      return { text: String(c.text ?? ""), owner: str(c.owner), t: asT(c.t) };
    }),
    todos: arr(o.todos).map((x) => {
      const td = (x ?? {}) as Record<string, unknown>;
      return { content: String(td.content ?? ""), owner: str(td.owner), due: str(td.due), t: asT(td.t) };
    }),
    terms: arr(o.terms).map((x) => {
      const tm = (x ?? {}) as Record<string, unknown>;
      return {
        name: String(tm.name ?? ""),
        note: str(tm.note),
        heard: Array.isArray(tm.heard) ? tm.heard.map((h) => String(h ?? "")).filter(Boolean) : undefined,
      };
    }),
  };
}

/** 纪要导出 Markdown：标题/概述/议题/结论/待办/术语（含听错变体，供搜索与人工
 *  核对兜底），时间码取 HH:MM:SS（回链 t 为空则不显）。 */
export function buildMinutesMarkdown(notes: MinutesNotes, fallbackTitle: string): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const clock = (ms?: number) =>
    ms === undefined ? "" : `${pad(Math.floor(ms / 3600000))}:${pad(Math.floor(ms / 60000) % 60)}:${pad(Math.floor(ms / 1000) % 60)}`;
  const stamp = (ms?: number) => {
    const c = clock(ms);
    return c ? ` \`[${c}]\`` : "";
  };
  const lines: string[] = [`# ${notes.title?.trim() || fallbackTitle || "会议纪要"}`, ""];
  if (notes.summary?.trim()) lines.push(notes.summary.trim(), "");
  const topics = (notes.topics ?? []).filter((t) => t.title.trim() || (t.points ?? []).length > 0);
  if (topics.length > 0) {
    lines.push("## 议题", "");
    for (const t of topics) {
      lines.push(`### ${t.title}${stamp(t.t)}`.trimEnd());
      if (t.summary?.trim()) lines.push("", t.summary.trim());
      for (const p of t.points ?? []) {
        if (p.text.trim()) lines.push(`- ${p.text.trim()}${stamp(p.t)}`);
      }
      lines.push("");
    }
  }
  const conclusions = (notes.conclusions ?? []).filter((c) => c.text.trim());
  if (conclusions.length > 0) {
    lines.push("## 结论", "");
    for (const c of conclusions) {
      const who = c.owner?.trim() ? `（${c.owner.trim()}）` : "";
      lines.push(`- ${c.text.trim()}${who}${stamp(c.t)}`);
    }
    lines.push("");
  }
  const todos = (notes.todos ?? []).filter((t) => t.content.trim());
  if (todos.length > 0) {
    lines.push("## 待办", "");
    for (const t of todos) {
      const meta = [t.owner?.trim(), t.due?.trim()].filter(Boolean).join(" · ");
      lines.push(`- [ ] ${t.content.trim()}${meta ? `（${meta}）` : ""}${stamp(t.t)}`);
    }
    lines.push("");
  }
  const terms = (notes.terms ?? []).filter((t) => t.name.trim());
  if (terms.length > 0) {
    lines.push("## 术语表", "");
    for (const t of terms) {
      const heard = t.heard?.length ? `（听错作：${t.heard.join("、")}）` : "";
      lines.push(`- **${t.name.trim()}**${t.note?.trim() ? ` —— ${t.note.trim()}` : ""}${heard}`);
    }
    lines.push("");
  }
  return lines.join("\n").replace(/\n{3,}/g, "\n\n").trimEnd() + "\n";
}

/** RFC3339 → 「YYYY-MM-DD HH:mm」（本地展示截断，不引入时区换算）。 */
export function refinedAtText(rfc3339: string | undefined): string {
  return rfc3339 ? rfc3339.slice(0, 16).replace("T", " ") : "";
}

/** 文件名安全化：路径与系统保留字符替换为连字符（事件名/任务标题直接做下载名）。 */
export function safeFilename(name: string): string {
  return name.replace(/[/\\:*?"<>|]/g, "-").trim() || "语音识别";
}

/* ---------- 问答区（/api/assistant/chat + context）：上下文组装与时间戳引用 ---------- */

/** 毫秒时间码 → HH:MM:SS（与后端 clockMS 同规则，超一小时自然进位）。 */
export function clockText(ms: number): string {
  const sec = Math.max(0, Math.floor(ms / 1000));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(Math.floor(sec / 3600))}:${pad(Math.floor(sec / 60) % 60)}:${pad(sec % 60)}`;
}

/** 带时间戳的转写全文：「HH:MM:SS 说话人N：文本」逐行（说话人展示名含改名覆盖），
 *  与加工区后端 transcriptFromSummary 同格式，供问答上下文。 */
export function timedTranscript(segs: QNSegment[], speakerLabel: (id: string) => string): string {
  return segs.map((s) => `${clockText(s.start_ms)} ${s.speaker ? `${speakerLabel(s.speaker)}：` : ""}${s.text}`).join("\n");
}

/** 本地日期「YYYY-MM-DD」（context 里的「今天」）。 */
export function todayText(now: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

/** 问答上下文（组装后）的总 rune 上限：与后端服务端封顶一致（24000，恰好放行）。
 *  包装文案与截断提示计入总额，转写只取剩余预算——组装结果恒不超上限。 */
const CONTEXT_MAX_RUNES = 24000;

/** 转写超预算截断时附加的提示（含换行，计入总预算）。 */
const TRUNCATED_NOTICE = "\n（转写过长，已截断）";

/** 引用约定（context 末尾附给模型）：句末【分:秒】，前端解析为可点击跳播点。 */
const CITE_RULE = "回答中引用转写内容时，请在对应句子的句末标注它在录音中的开始时间，格式为【分:秒】，如【03:21】。";

/** 问答上下文：转写全文（超预算截断）+ 引用约定 + 今天日期。发送时才组装，不缓存旧转写。
 *  首尾自行 trim（后端 ChatSystem 不做 TrimSpace，context 原样拼进 system）。
 *  预算按 JS string.length（UTF-16 码元）计：增补平面字符只多算不算少，偏保守方向安全。 */
export function buildChatContext(transcript: string, today: string): string {
  const prefix = `以下是这段录音的文字稿，行首是每句在录音中的开始时间：\n\n`;
  const suffix = `\n\n${CITE_RULE}\n今天是 ${today}。`;
  const budget = CONTEXT_MAX_RUNES - prefix.length - suffix.length - TRUNCATED_NOTICE.length;
  let t = transcript.trim();
  if (t.length > budget) t = t.slice(0, budget) + TRUNCATED_NOTICE;
  return prefix + t + suffix;
}

/** 引用标注：句末【分:秒】或【时:分:秒】（与模型的约定，前端据此渲染跳播 chip）。 */
const CITE_RE = /【(\d{1,2}:\d{2}(?::\d{2})?)】/g;

/** 引用时间戳 → 毫秒：「03:21」→ 201000，「01:02:03」→ 3723000；异常形态返回 null。 */
export function citeToMs(stamp: string): number | null {
  const parts = stamp.split(":").map((n) => Number(n));
  if (parts.some((n) => !Number.isInteger(n) || n < 0)) return null;
  if (parts.length === 2) return (parts[0] * 60 + parts[1]) * 1000;
  if (parts.length === 3) return (parts[0] * 3600 + parts[1] * 60 + parts[2]) * 1000;
  return null;
}

/** 助手回答里的【mm:ss】替换为内部锚点链接（#seek-毫秒），经 Markdown 的链接
 *  定制渲染成可点击 chip——保持正文一段连续的 markdown 流，chip 落在行内。 */
export function linkifyCitations(text: string): string {
  return text.replace(CITE_RE, (m, stamp: string) => {
    const ms = citeToMs(stamp);
    return ms === null ? m : `[${stamp}](#seek-${ms})`;
  });
}

const SEEK_HREF_RE = /^#seek-(\d+)$/;

/** 内部跳播锚点（#seek-毫秒，linkifyCitations 的产物）→ 毫秒；非跳播锚点或
 *  异常形态返回 null。chip 分支据此判定——href 里是纯毫秒整数，勿再喂给
 *  citeToMs（那按 mm:ss 解析，必返 null）。 */
export function seekHrefToMs(href: string | undefined): number | null {
  const m = SEEK_HREF_RE.exec(href ?? "");
  return m ? Number(m[1]) : null;
}
