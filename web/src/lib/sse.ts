// SSE POST 消费公用封装：/api/prompts/apply（AI 写作）与 /api/refine（录音笔记加工）
// 同一套协议——预检错误回 JSON 统一包络，流开始后 data: 帧载荷为
// {"delta":...} 增量 / {"done":true} 终止 / {"error":{code,message}}，在此一处解析。
// /api/subtitles/translate（字幕翻译）帧多带 progress 进度与 done 内嵌 result，
// 走下方 streamPostSSEEvents 整帧透传。

/** 流式消费 POST SSE：逐段回调增量正文；出错（含流内 error 事件）抛 Error。 */
export async function streamPostSSE(
  url: string,
  body: unknown,
  onDelta: (delta: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) {
    // 统一包络错误（未登录/参数/凭证等预检失败）
    const payload = await resp.json().catch(() => null);
    throw new Error(payload?.message || `业务错误码 ${payload?.code ?? resp.status}`);
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

// SSE 帧载荷：delta 增量 / done 终止（可携带 result）/ error / progress 进度。
export interface SSEFrame {
  delta?: string;
  done?: boolean;
  error?: { code?: unknown; message?: string };
  progress?: { done: number; total: number };
  result?: unknown;
}

/** 流式消费 POST SSE：整帧回调（delta/done/error/progress/result 全透传）。 */
export async function streamPostSSEEvents(
  url: string,
  body: unknown,
  onEvent: (frame: SSEFrame) => void,
  signal?: AbortSignal,
): Promise<void> {
  // 解析循环与 streamPostSSE 一致，差异仅在把解析出的帧整体交给 onEvent
  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) {
    // 统一包络错误（未登录/参数/凭证等预检失败）
    const payload = await resp.json().catch(() => null);
    throw new Error(payload?.message || `业务错误码 ${payload?.code ?? resp.status}`);
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
      const payload = JSON.parse(line.slice(5).trim()) as SSEFrame;
      onEvent(payload);
      if (payload.done) return;
    }
  }
}
