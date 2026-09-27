import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

export type ModelStatus = "idle" | "downloading" | "verifying" | "installed" | "failed";

export interface ModelRequirements {
  device: "cuda" | "cpu";
  vram_gb?: number;
}

/** GET /api/models 的 items 元素:目录条目 + 实时状态(Entry 字段平铺)。 */
export interface ModelItem {
  id: string;
  repo: string;
  revision: string;
  name: string;
  kind: string; // asr | tts(开放枚举)
  /** tts 模型族:qwen3_tts | index_tts2(仅 tts 条目有;asr/engine 无)。 */
  family?: string;
  summary: string;
  size_bytes: number;
  requirements: ModelRequirements;
  license: string;
  license_url: string;
  status: ModelStatus;
  requires_engine?: string; // tts/asr 条目:依赖的引擎 id(engine 条目无此字段)
  has_partial: boolean;
  downloaded_bytes: number;
  total_bytes: number;
  error?: string;
}

export const listModels = () => fetchJSON<{ items: ModelItem[] }>("/api/models");
export const startModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/download`, { method: "POST" });
export const stopModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/stop`, { method: "POST" });
export const deleteModel = (id: string) =>
  fetchJSON(`/api/models/${id}`, { method: "DELETE" });
export const openModelsDir = () => fetchJSON("/api/models/open-dir", { method: "POST" });

const POLL_MS = 1000;

/** 模型目录+实时状态:存在下载/校验时 1s 轮询,否则不轮询。 */
export function useModels() {
  return useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    refetchInterval: (q) =>
      q.state.data?.items.some((m) => m.status === "downloading" || m.status === "verifying")
        ? POLL_MS
        : false,
  });
}

/** 字节数 → 人读大小(B/MB/GB,与设置页 mono 小字风格配套)。 */
export function formatSize(n: number): string {
  if (n >= 2 ** 30) return `${(n / 2 ** 30).toFixed(1)} GB`;
  if (n >= 2 ** 20) return `${(n / 2 ** 20).toFixed(0)} MB`;
  return `${n} B`;
}

export interface LocalReadyMissing {
  type: "engine" | "model";
  id: string;
  name: string;
}

export interface LocalReady {
  ready: boolean;
  missing: LocalReadyMissing[];
}

/** 本地推理就绪查询(引擎+模型依赖链一次判定);挂载即拉,下载完成后回到页面会自动刷新。 */
export function useLocalReady(tool: "tts" | "asr") {
  return useQuery({
    queryKey: ["local-ready", tool],
    queryFn: () => fetchJSON<LocalReady>(`/api/local/ready?tool=${tool}`),
  });
}

/** 本地预置音色(CustomVoice GGUF 的 9 个 speaker)。 */
export function useLocalVoices() {
  return useQuery({
    queryKey: ["voices", "local"],
    queryFn: () => fetchJSON<{ voices: { id: string; name: string }[] }>("/api/voices?provider=local"),
  });
}
