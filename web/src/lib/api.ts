// 后端 API 基址：一律同源相对路径——dev 走 vite 代理（无跨域预检），prod 页面与 API 同域部署。
// 供 fetchJSON 与页面拼接 stream/download 等资源链接共用。
export const apiBase = "";

// 后端统一包络：{code, data, message}，HTTP 一律 200，code!==0 为业务错误。
interface Envelope<T> { code: number; data: T; message: string }

export async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(apiBase + path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  let body: Envelope<T> | null = null;
  try {
    body = await resp.json();
  } catch {
    throw new Error(`响应不是 JSON：HTTP ${resp.status}`);
  }
  if (body === null || typeof body.code !== "number") {
    throw new Error(`响应格式错误：HTTP ${resp.status}`);
  }
  if (body.code !== 0) {
    throw new Error(body.message || `业务错误码 ${body.code}`);
  }
  return body.data;
}

export interface ToolMeta { provider: string; name: string; title: string; description: string; group: string }
export interface ParamSpec { key: string; label: string; type: string; required: boolean; default?: unknown; options?: { value: string; label: string }[]; placeholder?: string; group?: string }
export interface ToolInfo { meta: ToolMeta; param_specs: ParamSpec[] }
export const listTools = () => fetchJSON<ToolInfo[]>("/api/tools");
