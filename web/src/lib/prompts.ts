import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";
import { streamPostSSE } from "./sse";

// 提示词库：内置条目（烤在后端二进制里）与用户自定义条目合并的只读视图。
// AI 写作（生成/润色）走 POST /api/prompts/apply 的 SSE 流，协议与助手 chat 一致：
// 预检错误为 JSON 包络，流内错误为 {"error":...} 事件，终止 {"done":true}。

export type PromptKind = "generate" | "polish" | "dialect";

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
  /** 生成=主题/要点（可空自拟）；润色=原文（必填）；方言=要改写成方言的普通话原文（必填） */
  input: string;
  /** 篇幅档位，仅生成类生效 */
  length?: "short" | "medium" | "long";
}

/** 流式调用 AI 写作，逐段回调增量正文；出错（含流内 error 事件）抛 Error。
 *  协议解析在 lib/sse.streamPostSSE（与 /api/refine 共用）。 */
export async function streamApplyPrompt(req: ApplyPromptReq, onDelta: (delta: string) => void, signal?: AbortSignal) {
  return streamPostSSE("/api/prompts/apply", req, onDelta, signal);
}

/** 提示词条目的引用负载：内置按 key，自定义按 id。 */
export function promptRef(p: PromptItem): { builtin?: string; id?: number } {
  return p.source === "builtin" ? { builtin: p.key } : { id: p.id };
}
