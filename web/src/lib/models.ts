import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchJSON } from "./api";
import { onTaskEvent } from "./ws";

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
  /** tts 模型族:qwen3_tts | index_tts2 | kokoro(仅 tts 条目有;asr/engine 无)。 */
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
  /** 目录换版检测:盘面 manifest 与条目 revision 不一致(旧安装仍在盘)→ 可一键更新。 */
  update_available: boolean;
  /** update_available=true 时盘面旧安装的 revision(展示「已装 x → 新版 y」)。 */
  installed_revision?: string;
}

export const listModels = () => fetchJSON<{ items: ModelItem[] }>("/api/models");
export const startModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/download`, { method: "POST" });
export const stopModelDownload = (id: string) =>
  fetchJSON(`/api/models/${id}/stop`, { method: "POST" });
export const deleteModel = (id: string) =>
  fetchJSON(`/api/models/${id}`, { method: "DELETE" });
export const openModelsDir = () => fetchJSON("/api/models/open-dir", { method: "POST" });

/** 模型目录+实时状态:进度由 WS model 事件驱动(setQueryData 直接 patch 缓存),
 *  不再轮询;快照消息(model.snapshot)在连接建立/重连时补发在途状态自愈,
 *  staleTime 压窗口聚焦重取频率作离线兜底。 */
export function useModels() {
  const qc = useQueryClient();
  useEffect(
    () =>
      onTaskEvent((ev) => {
        const apply = (m: ModelItem, e: { status: ModelStatus; downloaded_bytes: number; total_bytes: number; error?: string }): ModelItem => ({
          ...m,
          status: e.status,
          downloaded_bytes: e.downloaded_bytes,
          total_bytes: e.total_bytes,
          error: e.error,
        });
        if (ev.type === "model" && ev.model_id) {
          const e = {
            status: (ev.status ?? "idle") as ModelStatus,
            downloaded_bytes: ev.downloaded_bytes ?? 0,
            total_bytes: ev.total_bytes ?? 0,
            error: ev.error,
          };
          qc.setQueryData<{ items: ModelItem[] }>(["models"], (d) =>
            d ? { items: d.items.map((m) => (m.id === ev.model_id ? apply(m, e) : m)) } : d,
          );
        } else if (ev.type === "model.snapshot" && ev.models) {
          const byId = new Map(ev.models.map((x) => [x.model_id, x]));
          qc.setQueryData<{ items: ModelItem[] }>(["models"], (d) =>
            d
              ? {
                  items: d.items.map((m) => {
                    const e = byId.get(m.id);
                    return e ? apply(m, e) : m;
                  }),
                }
              : d,
          );
        }
      }),
    [qc],
  );
  return useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    staleTime: 30_000,
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

/** 本地预置音色:qwen3 CustomVoice 的 9 个 speaker;family=kokoro 时为 103 个内置音色
 *  (含 voices.bin 的 sid,后端据此归一)。 */
export function useLocalVoices(family?: string) {
  return useQuery({
    queryKey: ["voices", "local", family ?? ""],
    queryFn: () =>
      fetchJSON<{ voices: { id: string; name: string; sid?: number }[] }>(
        family ? `/api/voices?provider=local&family=${family}` : "/api/voices?provider=local",
      ),
  });
}
