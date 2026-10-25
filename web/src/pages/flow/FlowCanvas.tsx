// 工作流画布：React Flow 拖拽编排。图的事实源是 FlowNodeSpec[]（inputs_from
// 即边集合，参数随节点）——React Flow 的 nodes/edges 全部由 spec 派生投影，
// 编辑操作（拖动/连线/删除）翻译回 spec 经 onChange 上抛，本组件零内部图状态。
import { useCallback, useMemo, useRef, useState } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  Handle,
  Position,
  applyNodeChanges,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { GripVertical, Plus, Search, X } from "lucide-react";
import { portOf, type FlowNodeSpec, type ToolInfo } from "../../lib/flow";

export interface NodeBadge {
  status: string;
  progress?: number;
  note?: string;
}

export interface FlowCanvasProps {
  nodes: FlowNodeSpec[];
  tools: ToolInfo[];
  badges?: Record<string, NodeBadge>;
  readOnly?: boolean;
  /** 图变化回传（节点增删/移动/连线/删除） */
  onChange: (nodes: FlowNodeSpec[]) => void;
  onOpenParams: (nodeKey: string) => void;
}

interface ToolNodeData {
  spec: FlowNodeSpec;
  title: string;
  slotRefs: number;
  badge?: NodeBadge;
  readOnly?: boolean;
  [key: string]: unknown; // React Flow Node<T> 泛型要求索引签名
}

const badgeStyle: Record<string, string> = {
  pending: "bg-raise-2 text-muted",
  running: "bg-accent/15 text-accent",
  done: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  failed: "bg-danger/15 text-danger",
  canceled: "bg-raise-2 text-muted",
  skipped: "bg-raise-2 text-muted",
};

const badgeLabel: Record<string, string> = {
  pending: "等待",
  running: "运行中",
  done: "完成",
  failed: "失败",
  canceled: "已取消",
  skipped: "跳过",
};

/** 工具节点卡：输入端口在左（id=引擎 Files 键），输出口在右（id=out）。 */
function ToolNode({ data }: NodeProps) {
  const d = data as unknown as ToolNodeData;
  const port = portOf(d.spec.provider, d.spec.tool);
  const inPorts = port?.in ?? [];
  return (
    <div
      className={`w-56 rounded-lg border bg-raise shadow-sm transition-shadow ${
        d.badge?.status === "running"
          ? "border-accent/60"
          : d.badge?.status === "failed"
            ? "border-danger/50"
            : "border-line"
      }`}
    >
      <div className="flex items-center gap-1.5 rounded-t-lg border-b border-line bg-raise-2 px-3 py-2">
        <GripVertical size={12} className="shrink-0 text-muted" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium">{d.title}</p>
          <p className="truncate text-[10px] text-muted">
            {d.spec.provider}.{d.spec.tool}
          </p>
        </div>
        {d.badge && (
          <span className={`shrink-0 rounded-full px-1.5 py-0.5 text-[10px] ${badgeStyle[d.badge.status] ?? badgeStyle.pending}`}>
            {d.badge.status === "running" && d.badge.progress != null ? `${d.badge.progress}%` : badgeLabel[d.badge.status] ?? d.badge.status}
          </span>
        )}
      </div>
      <div className="px-3 py-2">
        {inPorts.length === 0 && <p className="text-[10px] text-muted">参数驱动 · 无产物输入</p>}
        {inPorts.map((p, i) => {
          const ref = d.spec.inputs_from?.[p.key] ?? "";
          const isSlot = ref.startsWith("inputs.");
          return (
            <div key={p.key} className="relative flex items-center py-0.5">
              <Handle type="target" position={Position.Left} id={p.key} style={{ top: "50%", marginTop: -6 }} />
              <span className="text-[10px] text-fg-2">{p.label}</span>
              <span className={`ml-auto text-[10px] ${isSlot ? "text-accent" : "text-muted"}`}>
                {isSlot ? "输入槽" : ref ? "已连接" : inPorts.length > 1 ? `#${i + 1} 未连接` : "未连接"}
              </span>
            </div>
          );
        })}
        <div className="relative mt-1 flex items-center justify-end">
          <span className="text-[10px] text-muted">产物</span>
          <Handle type="source" position={Position.Right} id="out" style={{ top: "50%", marginTop: -6 }} />
        </div>
      </div>
    </div>
  );
}

const nodeTypes = { tool: ToolNode };

export function FlowCanvas({ nodes, tools, badges, readOnly, onChange, onOpenParams }: FlowCanvasProps) {
  const seqRef = useRef(nodes.length);
  const [search, setSearch] = useState("");

  const titleOf = useCallback(
    (provider: string, tool: string) =>
      tools.find((t) => t.meta.provider === provider && t.meta.name === tool)?.meta.title ?? `${provider}.${tool}`,
    [tools],
  );

  // spec → React Flow 投影（纯派生，含徽章/连线动画态）
  const rfNodes: Node[] = useMemo(
    () =>
      nodes.map((n) => ({
        id: n.key,
        type: "tool" as const,
        position: { x: n.x ?? 0, y: n.y ?? 0 },
        data: {
          spec: n,
          title: titleOf(n.provider, n.tool),
          slotRefs: Object.values(n.inputs_from ?? {}).filter((v) => v.startsWith("inputs.")).length,
          badge: badges?.[n.key],
          readOnly,
        } satisfies ToolNodeData as unknown as Record<string, unknown>,
      })),
    [nodes, badges, readOnly, titleOf],
  );

  const rfEdges: Edge[] = useMemo(() => {
    const out: Edge[] = [];
    const known = new Set(nodes.map((n) => n.key));
    for (const n of nodes) {
      for (const [port, ref] of Object.entries(n.inputs_from ?? {})) {
        if (ref.startsWith("inputs.")) continue; // 输入槽引用不画边，节点卡上有标签
        const [srcKey, sel] = ref.split(":");
        if (!known.has(srcKey)) continue;
        out.push({
          id: `${srcKey}->${n.key}:${port}`,
          source: srcKey,
          target: n.key,
          sourceHandle: "out",
          targetHandle: port,
          label: sel ? ({ voice: "人声轨", background: "伴奏轨", music: "音乐轨", sfx: "音效轨" }[sel] ?? sel) : undefined,
          animated: badges?.[n.key]?.status === "running",
        });
      }
    }
    return out;
  }, [nodes, badges]);

  // 编辑操作统一出口：应用变更 → 回推 spec → onChange
  const onNodesChange = useCallback(
    (changes: NodeChange[]) => {
      if (readOnly || changes.length === 0) return;
      const next = applyNodeChanges(changes, rfNodes);
      if (changes.some((c) => c.type === "remove")) {
        // 删除节点：连同所有引用它的连线（inputs_from）一起清
        const removed = new Set(changes.filter((c) => c.type === "remove").map((c) => (c as { id: string }).id));
        onChange(
          next.flatMap((n) => {
            const old = nodes.find((s) => s.key === n.id);
            if (!old) return [];
            const inputsFrom: Record<string, string> = {};
            for (const [port, ref] of Object.entries(old.inputs_from ?? {})) {
              if (ref.startsWith("inputs.") || upstreamKept(ref, removed)) inputsFrom[port] = ref;
            }
            return [{ ...old, x: n.position.x, y: n.position.y, inputs_from: inputsFrom }];
          }),
        );
        return;
      }
      if (!changes.some((c) => c.type === "position" || c.type === "dimensions")) return;
      onChange(
        next.flatMap((n) => {
          const old = nodes.find((s) => s.key === n.id);
          return old ? [{ ...old, x: n.position.x, y: n.position.y }] : [];
        }),
      );
    },
    [nodes, onChange, readOnly, rfNodes],
  );

  const isValidConnection = useCallback(
    (conn: Connection | Edge) => {
      if (conn.source === conn.target || conn.sourceHandle !== "out" || !conn.targetHandle) return false;
      const src = nodes.find((n) => n.key === conn.source);
      const dst = nodes.find((n) => n.key === conn.target);
      if (!src || !dst) return false;
      const srcPort = portOf(src.provider, src.tool);
      const dstPort = portOf(dst.provider, dst.tool);
      if (!srcPort || !dstPort) return false;
      const inPort = dstPort.in.find((p) => p.key === conn.targetHandle);
      if (!inPort) return false;
      // 目标端口已有连线则拒绝（先删旧边再连）
      if (nodes.some((n) => n.key === dst.key && n.inputs_from?.[conn.targetHandle!])) return false;
      return srcPort.outKinds.some((k) => inPort.kind === "file" || k === inPort.kind);
    },
    [nodes],
  );

  const onConnect = useCallback(
    (conn: Connection) => {
      if (readOnly || !isValidConnection(conn) || !conn.targetHandle) return;
      onChange(
        nodes.map((n) =>
          n.key === conn.target
            ? { ...n, inputs_from: { ...(n.inputs_from ?? {}), [conn.targetHandle!]: conn.source } }
            : n,
        ),
      );
    },
    [isValidConnection, nodes, onChange, readOnly],
  );

  const addTool = useCallback(
    (t: ToolInfo) => {
      seqRef.current += 1;
      const key = `${t.meta.provider}-${t.meta.name}-${seqRef.current}`;
      onChange([
        ...nodes,
        {
          key,
          provider: t.meta.provider,
          tool: t.meta.name,
          params: {},
          inputs_from: {},
          x: 120 + (seqRef.current % 4) * 40,
          y: 60 + (seqRef.current % 5) * 36,
        },
      ]);
    },
    [nodes, onChange],
  );

  const groups = useMemo(() => {
    const byGroup = new Map<string, ToolInfo[]>();
    for (const t of tools) {
      if (!portOf(t.meta.provider, t.meta.name)) continue;
      const q = search.trim().toLowerCase();
      if (q && !(t.meta.title.toLowerCase().includes(q) || `${t.meta.provider}.${t.meta.name}`.includes(q))) continue;
      const g = t.meta.group || "其他";
      if (!byGroup.has(g)) byGroup.set(g, []);
      byGroup.get(g)!.push(t);
    }
    return [...byGroup.entries()];
  }, [tools, search]);

  return (
    <div className="relative h-full min-h-0 flex-1">
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onConnect={onConnect}
        isValidConnection={isValidConnection}
        onNodeDoubleClick={(_, n) => !readOnly && onOpenParams(n.id)}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        elementsSelectable={!readOnly}
        deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
      >
        <Background gap={24} />
        <Controls showInteractive={false} position="bottom-right" />
        {!readOnly && (
          <div className="absolute left-3 top-3 z-10 flex w-52 flex-col gap-1.5 rounded-lg border border-line bg-raise/95 p-2 shadow-sm backdrop-blur">
            <div className="flex items-center gap-1.5 rounded-md border border-line bg-raise-2 px-2">
              <Search size={13} className="shrink-0 text-muted" />
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索工具"
                className="min-w-0 flex-1 bg-transparent py-1.5 text-xs outline-none placeholder:text-muted"
              />
              {search && (
                <button type="button" onClick={() => setSearch("")} className="cursor-pointer text-muted hover:text-fg">
                  <X size={12} />
                </button>
              )}
            </div>
            <div className="max-h-72 overflow-y-auto">
              {groups.map(([g, list]) => (
                <div key={g} className="mb-1">
                  <p className="px-1 py-0.5 text-[10px] text-muted">{g}</p>
                  {list.map((t) => (
                    <button
                      key={`${t.meta.provider}.${t.meta.name}`}
                      type="button"
                      onClick={() => addTool(t)}
                      className="flex w-full cursor-pointer items-center gap-1.5 rounded-md px-1.5 py-1.5 text-left text-xs transition-colors duration-150 hover:bg-raise-2"
                    >
                      <Plus size={11} className="shrink-0 text-muted" />
                      <span className="truncate">{t.meta.title}</span>
                    </button>
                  ))}
                </div>
              ))}
              {groups.length === 0 && <p className="px-1 py-2 text-xs text-muted">没有匹配的工具</p>}
            </div>
          </div>
        )}
      </ReactFlow>
      {!readOnly && (
        <p className="pointer-events-none absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full bg-raise/90 px-3 py-1 text-[11px] text-muted shadow-sm">
          点击左侧工具添加节点 · 拖动连线 · 双击节点改参数
        </p>
      )}
    </div>
  );
}

/** 被删上游的连线要一并清掉（inputs_from 引用悬空会导致保存校验失败）：
 *  上游节点不在 removed 集合（仍存活）时保留该引用。 */
function upstreamKept(ref: string, removed: Set<string>) {
  const src = ref.split(":")[0];
  return !removed.has(src);
}
