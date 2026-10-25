// 工作流编排前端数据层：/api/workflows 系列端点封装与图类型。
// 图结构与后端 internal/workflow.Graph 一一对应（JSON 直传）。
import { fetchJSON } from "./api";
import type { ToolInfo } from "./api";
export type { ToolInfo };

/** 运行时输入槽声明。track=选歌槽（值为一首歌字段集，参数按槽.字段引用） */
export interface InputSlot {
  key: string;
  label: string;
  type: "string" | "text" | "file" | "track";
  required?: boolean;
  placeholder?: string;
}

/** 选歌槽的值：/api/music 聚合搜索结果行（喂给 music_download 的字段） */
export interface TrackValue {
  source: string;
  rid: string;
  name: string;
  artist?: string;
  duration?: number;
}

/** 工作流节点（画布） */
export interface FlowNodeSpec {
  key: string;
  provider: string;
  tool: string;
  params: Record<string, unknown>;
  /** 引擎 Files 键 → 上游："节点key" / "节点key:2" / "节点key:voice" / "inputs.<槽>" */
  inputs_from: Record<string, string>;
  x?: number;
  y?: number;
}

/** 工作流编排图（与后端 workflow.Graph 同构） */
export interface FlowGraph {
  version?: number;
  inputs?: InputSlot[];
  nodes: FlowNodeSpec[];
}

export interface WorkflowDTO {
  id: number;
  /** 内置模板键（仅模板非空）；用户编排为空 */
  key?: string;
  name: string;
  description: string;
  graph: FlowGraph;
  created_at?: string;
  updated_at?: string;
}

export interface WorkflowListDTO {
  items: WorkflowDTO[];
  templates: WorkflowDTO[];
}

/** 运行记录里一个节点的编排状态（后端 workflow.NodeRun） */
export interface NodeRun {
  key: string;
  task_id?: string;
  status: "pending" | "running" | "done" | "failed" | "skipped" | "canceled";
  artifact_ids?: string[];
  error?: string;
}

export interface WorkflowRunDTO {
  id: number;
  workflow_id: number;
  workflow_key: string;
  name: string;
  status: "pending" | "running" | "done" | "failed" | "canceled";
  inputs?: unknown;
  /** 运行时图快照（监控页完整还原画布） */
  graph?: FlowGraph;
  nodes: NodeRun[];
  error?: string;
  created_at?: string;
  updated_at?: string;
}

export async function listWorkflows(): Promise<WorkflowListDTO> {
  return fetchJSON<WorkflowListDTO>("/api/workflows");
}

export async function getWorkflow(id: number): Promise<WorkflowDTO> {
  return fetchJSON<WorkflowDTO>(`/api/workflows/${id}`);
}

export async function createWorkflow(name: string, description: string, graph: FlowGraph): Promise<WorkflowDTO> {
  return fetchJSON<WorkflowDTO>("/api/workflows", {
    method: "POST",
    body: JSON.stringify({ name, description, graph }),
  });
}

export async function updateWorkflow(id: number, name: string, description: string, graph: FlowGraph): Promise<WorkflowDTO> {
  return fetchJSON<WorkflowDTO>(`/api/workflows/${id}`, {
    method: "PUT",
    body: JSON.stringify({ name, description, graph }),
  });
}

export async function deleteWorkflow(id: number): Promise<void> {
  await fetchJSON(`/api/workflows/${id}`, { method: "DELETE" });
}

export async function runWorkflow(id: number, inputs: Record<string, unknown>): Promise<WorkflowRunDTO> {
  return fetchJSON<WorkflowRunDTO>(`/api/workflows/${id}/run`, {
    method: "POST",
    body: JSON.stringify({ inputs }),
  });
}

export async function runTemplate(key: string, inputs: Record<string, unknown>): Promise<WorkflowRunDTO> {
  return fetchJSON<WorkflowRunDTO>(`/api/workflow-templates/${key}/run`, {
    method: "POST",
    body: JSON.stringify({ inputs }),
  });
}

export async function getRun(id: number): Promise<WorkflowRunDTO> {
  return fetchJSON<WorkflowRunDTO>(`/api/workflow-runs/${id}`);
}

export async function listRuns(workflowID?: number, page = 1, size = 20): Promise<{ items: WorkflowRunDTO[]; total: number }> {
  const q = new URLSearchParams({ page: String(page), size: String(size) });
  if (workflowID) q.set("workflow_id", String(workflowID));
  return fetchJSON(`/api/workflow-runs?${q}`);
}

export async function cancelRun(id: number): Promise<void> {
  await fetchJSON(`/api/workflow-runs/${id}/cancel`, { method: "POST" });
}

/** 工具清单（画布工具面板数据源；含端口表在 meta 之外由后端 /api/tools 的 param_specs 渲染表单） */
export async function listTools(): Promise<ToolInfo[]> {
  return fetchJSON<ToolInfo[]>("/api/tools");
}

/** 画布连线用的端口元数据：后端不单独暴露端口表，前端按工具名约定映射
 *  （与后端 ports.go 同源维护；测试保证 registry 与后端表对齐）。
 *  这里仅声明「哪些工具可入画布」的判定所需的最小信息——端口 kind 由
 *  端口键名推导：audio→audio、audio2→audio、其余 file 通配。 */
export const PORT_KIND_BY_KEY: Record<string, string> = {
  audio: "audio",
  audio2: "audio",
  audio3: "audio",
};

/** 前端静态端口表：与后端 internal/workflow/ports.go 对齐（仅画布渲染需要
 *  输入端口名与输出 kind 集合；连线校验以此为准）。 */
export interface ToolPort {
  in: { key: string; kind: string; label: string }[];
  outKinds: string[];
  chainable: boolean;
}

export function portOf(provider: string, tool: string): ToolPort | null {
  const spec = FLOW_PORTS[`${provider}.${tool}`];
  if (!spec || !spec.chainable) return null;
  return spec;
}

export const FLOW_PORTS: Record<string, ToolPort> = {
  "downloader.download": { in: [], outKinds: ["audio", "video", "subtitle", "image", "archive"], chainable: true },
  "music.music_download": { in: [], outKinds: ["audio"], chainable: true },
  "music.music_mv_download": { in: [], outKinds: ["video"], chainable: true },
  "volcengine.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript", "subtitle"], chainable: true },
  "volcengine.minutes": { in: [{ key: "audio", kind: "audio", label: "音视频" }], outKinds: ["minutes", "transcript"], chainable: true },
  "volcengine.separate": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "volcengine.translate": { in: [], outKinds: ["translation"], chainable: true },
  "volcengine.podcast": { in: [], outKinds: ["audio"], chainable: true },
  "volcengine.tts": { in: [], outKinds: ["audio"], chainable: true },
  "volcengine.tts_stream": { in: [], outKinds: ["audio"], chainable: true },
  "volcengine.tts_long": { in: [], outKinds: ["audio"], chainable: true },
  "qianwen.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript", "subtitle"], chainable: true },
  "qianwen.tts": { in: [], outKinds: ["audio"], chainable: true },
  "xiaomi.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript"], chainable: true },
  "xiaomi.tts": { in: [], outKinds: ["audio"], chainable: true },
  "zhipu.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript"], chainable: true },
  "zhipu.tts": { in: [], outKinds: ["audio"], chainable: true },
  "openrouter.tts": { in: [], outKinds: ["audio"], chainable: true },
  "minimax.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript", "subtitle"], chainable: true },
  "minimax.tts": { in: [], outKinds: ["audio"], chainable: true },
  "minimax.tts_long": { in: [], outKinds: ["audio"], chainable: true },
  "local.asr": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["transcript", "subtitle"], chainable: true },
  "local.tts": { in: [], outKinds: ["audio"], chainable: true },
  "mvsep.separate": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "zhuanhuanmao.separate": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "gsgc.separate": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.trim": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.clip": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.merge": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.pitch": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.equalizer": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.volume": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.fade": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.reverse": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "audio.mix": {
    in: [
      { key: "audio", kind: "audio", label: "伴奏" },
      { key: "audio2", kind: "audio", label: "人声" },
    ],
    outKinds: ["audio"], chainable: true,
  },
  "audio.duck": {
    in: [
      { key: "audio", kind: "audio", label: "人声" },
      { key: "audio2", kind: "audio", label: "BGM" },
    ],
    outKinds: ["audio"], chainable: true,
  },
  "gsgc.video-format-convert": { in: [{ key: "audio", kind: "video", label: "视频" }], outKinds: ["video"], chainable: true },
  "gsgc.video-compression": { in: [{ key: "audio", kind: "video", label: "视频" }], outKinds: ["video"], chainable: true },
  "gsgc.video-extract-audio": { in: [{ key: "audio", kind: "video", label: "视频" }], outKinds: ["audio"], chainable: true },
  "gsgc.video-volume-adjust": { in: [{ key: "audio", kind: "video", label: "视频" }], outKinds: ["video"], chainable: true },
  "gsgc.video-speed": { in: [{ key: "audio", kind: "video", label: "视频" }], outKinds: ["video"], chainable: true },
  "gsgc.audio-format-convert": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "gsgc.audio-compression": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "gsgc.audio-denoise": { in: [{ key: "audio", kind: "audio", label: "音频" }], outKinds: ["audio"], chainable: true },
  "gsgc.image-format-convert": { in: [{ key: "audio", kind: "image", label: "图片" }], outKinds: ["image"], chainable: true },
  "gsgc.image-compression": { in: [{ key: "audio", kind: "image", label: "图片" }], outKinds: ["image"], chainable: true },
};

/** 图 → 后端校验可用的纯数据（去 React 侧附加字段） */
export function toGraphPayload(nodes: FlowNodeSpec[], inputs: InputSlot[]): FlowGraph {
  return { version: 1, inputs, nodes };
}
