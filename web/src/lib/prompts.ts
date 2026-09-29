import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

// 提示词库：内置条目（烤在后端二进制里）与用户自定义条目合并的只读视图。
// AI 写作（生成/润色）走 POST /api/prompts/apply 的 SSE 流，协议与助手 chat 一致：
// 预检错误为 JSON 包络，流内错误为 {"error":...} 事件，终止 {"done":true}。

export type PromptKind = "generate" | "polish";

export interface PromptItem {
  source: "builtin" | "user";
  key?: string;
  id?: number;
  name: string;
  category: string;
  description: string;
  kind: PromptKind;
  content: string;
}

export function usePrompts() {
  return useQuery({
    queryKey: ["prompts"],
    queryFn: () => fetchJSON<{ items: PromptItem[] }>("/api/prompts"),
    staleTime: 30_000,
  });
}

export interface ApplyPromptReq {
  /** 内置条目 key（与 id 二选一） */
  builtin?: string;
  /** 自定义条目 id（与 builtin 二选一） */
  id?: number;
  /** 生成=主题/要点（可空自拟）；润色=原文（必填） */
  input: string;
  /** 篇幅档位，仅生成类生效 */
  length?: "short" | "medium" | "long";
}

/** 流式调用 AI 写作，逐段回调增量正文；出错（含流内 error 事件）抛 Error。 */
export async function streamApplyPrompt(req: ApplyPromptReq, onDelta: (delta: string) => void, signal?: AbortSignal) {
  const resp = await fetch("/api/prompts/apply", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
    signal,
  });
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) {
    // 统一包络错误（未登录/参数/凭证等预检失败）
    const body = await resp.json().catch(() => null);
    throw new Error(body?.message || `业务错误码 ${body?.code ?? resp.status}`);
  }
  if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`);
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    for (;;) {
      const sep = buf.indexOf("\n\n");
      if (sep < 0) break;
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      const line = frame.split("\n").find((l) => l.startsWith("data:"));
      if (!line) continue;
      const payload = JSON.parse(line.slice(5).trim()) as { delta?: string; done?: boolean; error?: { message: string } };
      if (payload.error) throw new Error(payload.error.message);
      if (payload.delta) onDelta(payload.delta);
      if (payload.done) return;
    }
  }
}

/** 提示词条目的引用负载：内置按 key，自定义按 id。 */
export function promptRef(p: PromptItem): { builtin?: string; id?: number } {
  return p.source === "builtin" ? { builtin: p.key } : { id: p.id };
}
