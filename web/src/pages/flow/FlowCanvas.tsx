// 工作流画布：React Flow 拖拽编排。图的事实源是 FlowNodeSpec[] + InputSlot[]
// （inputs_from 即产物流边集合，params 里的 {{inputs.*}} 引用即参数流边）——
// React Flow 的 nodes/edges 全部由事实源派生投影，编辑操作（拖动/连线/删除）
// 翻译回事实源经 onChange 上抛，本组件零内部图状态。
//
// 输入源节点：InputSlot 的画布形态（id=in:<key>）。file 槽连工具输入端口
// （文件通道）；track/text/string 槽连工具节点的「参数」口（自动把引用写进
// 目标节点参数位——选歌/链接/文本这类业务上下文没有文件产物，走参数流）。
import { useCallback, useMemo, useRef, useState } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  Handle,
  Position,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { FileText, GripVertical, Link2, Music, PanelLeftClose, PanelLeftOpen, Plus, Search, Type, Upload, X } from "lucide-react";
import { portOf, type FlowNodeSpec, type InputSlot, type ToolInfo } from "../../lib/flow";

export interface NodeBadge {
  status: string;
  progress?: number;
  note?: string;
}

export interface FlowCanvasProps {
  nodes: FlowNodeSpec[];
  inputs: InputSlot[];
  tools: ToolInfo[];
  badges?: Record<string, NodeBadge>;
  readOnly?: boolean;
  /** 图变化回传（工具节点/输入源节点的增删移动连线） */
  onChange: (nodes: FlowNodeSpec[], inputs: InputSlot[]) => void;
  onOpenParams: (nodeKey: string) => void;
  /** 点击工具节点（运行监控态用于查看节点执行详情；编辑态不触发） */
  onNodeSelect?: (nodeKey: string) => void;
}

interface ToolNodeData {
  spec: FlowNodeSpec;
  title: string;
  badge?: NodeBadge;
  readOnly?: boolean;
  [key: string]: unknown; // React Flow Node<T> 泛型要求索引签名
}

/** 输入源节点种类：type→图标/名称/生成 key 前缀。
 *  track=选歌（聚合搜索） string=链接 text=文本 file=文件上传 */
const INPUT_SOURCES: { type: InputSlot["type"]; label: string; prefix: string; icon: typeof Music; desc: string }[] = [
  { type: "track", label: "歌曲搜索", prefix: "track", icon: Music, desc: "搜一首歌，音源/曲目自动带入" },
  { type: "string", label: "链接", prefix: "url", icon: Link2, desc: "视频/音频链接" },
  { type: "text", label: "文本", prefix: "text", icon: Type, desc: "文案、歌词、文章" },
  { type: "file", label: "文件", prefix: "file", icon: Upload, desc: "本地音频/视频上传" },
];

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

/** 工具节点卡：输入端口与「参数」口在左，输出口在右。 */
function ToolNode({ data }: NodeProps) {
  const d = data as unknown as ToolNodeData;
  const port = portOf(d.spec.provider, d.spec.tool);
  const inPorts = port?.in ?? [];
  const paramRefs = countParamRefs(d.spec.params);
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
        {inPorts.map((p, i) => {
          const ref = d.spec.inputs_from?.[p.key] ?? "";
          const isSlot = ref.startsWith("inputs.");
          return (
            <div key={p.key} className="relative flex items-center py-0.5">
              <Handle type="target" position={Position.Left} id={p.key} style={{ top: "50%", marginTop: -6 }} />
              <span className="text-[10px] text-fg-2">{p.label}</span>
              <span className={`ml-auto text-[10px] ${isSlot ? "text-accent" : "text-muted"}`}>
                {isSlot ? "输入源" : ref ? "已连接" : inPorts.length > 1 ? `#${i + 1} 未连接` : "未连接"}
              </span>
            </div>
          );
        })}
        {inPorts.length === 0 && paramRefs === 0 && <p className="text-[10px] text-muted">参数驱动 · 双击节点填写</p>}
        <div className="relative flex items-center justify-between pt-1">
          <Handle
            type="target"
            position={Position.Left}
            id="params"
            style={{ top: "auto", bottom: 10 }}
            className="!h-2 !w-2 !border-dashed !bg-transparent"
          />
          <span className="text-[10px] text-muted">{paramRefs > 0 ? `${paramRefs} 个参数来自输入源` : ""}</span>
          <span className="text-[10px] text-muted">产物</span>
          <Handle type="source" position={Position.Right} id="out" style={{ top: "50%", marginTop: -6 }} />
        </div>
      </div>
    </div>
  );
}

interface InputNodeData {
  slot: InputSlot;
  [key: string]: unknown;
}

const INPUT_ICON_BY_TYPE: Record<string, typeof Music> = {
  track: Music,
  string: Link2,
  text: Type,
  file: Upload,
};

/** 输入源节点卡：虚线边框区分于工具节点，右侧一个输出口。 */
function InputNode({ data }: NodeProps) {
  const d = data as unknown as InputNodeData;
  const Icon = INPUT_ICON_BY_TYPE[d.slot.type] ?? FileText;
  return (
    <div className="w-40 rounded-lg border border-dashed border-accent/40 bg-raise shadow-sm">
      <div className="flex items-center gap-1.5 px-3 py-2">
        <Icon size={13} className="shrink-0 text-accent" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium">{d.slot.label}</p>
          <p className="text-[10px] text-muted">{inputTypeLabel(d.slot.type)}</p>
        </div>
        <Handle type="source" position={Position.Right} id="out" style={{ top: "50%", marginTop: -6 }} />
      </div>
    </div>
  );
}

function inputTypeLabel(type: string) {
  return { track: "歌曲搜索", string: "链接", text: "文本", file: "文件上传" }[type] ?? type;
}

const nodeTypes = { tool: ToolNode, source: InputNode };

function countParamRefs(params: Record<string, unknown>): number {
  return Object.values(params ?? {}).filter((v) => typeof v === "string" && v.includes("{{inputs.")).length;
}

/** 参数连线的落位规则：把输入槽引用写进目标节点的参数位。
 *  text→第一个文本参数；string→第一个字符串参数；track→music_download 全套字段。 */
function paramLinkFor(tool: ToolInfo | undefined, slot: InputSlot, slotKey: string): Record<string, unknown> | null {
  if (!tool) return null;
  const ref = (k: string) => `{{inputs.${slotKey}${k ? "." + k : ""}}}`;
  if (slot.type === "track") {
    if (tool.meta.provider === "music" && tool.meta.name === "music_download") {
      return { source: ref("source"), rid: ref("rid"), name: ref("name"), artist: ref("artist"), duration_ms: ref("duration") };
    }
    return null;
  }
  if (slot.type === "text") {
    const p = tool.param_specs?.find((s) => s.type === "text");
    return p ? { [p.key]: ref("") } : null;
  }
  if (slot.type === "string") {
    const p = tool.param_specs?.find((s) => s.type === "string" && (s.key.includes("url") || s.required)) ?? tool.param_specs?.find((s) => s.type === "string");
    return p ? { [p.key]: ref("") } : null;
  }
  return null;
}

/** 输入节点 id 前缀（与工具节点 key 空间隔离） */
const INPUT_ID_PREFIX = "in:";

export function FlowCanvas(props: FlowCanvasProps) {
  return (
    <ReactFlowProvider>
      <FlowCanvasInner {...props} />
    </ReactFlowProvider>
  );
}

function FlowCanvasInner({ nodes, inputs, tools, badges, readOnly, onChange, onOpenParams, onNodeSelect }: FlowCanvasProps) {
  const seqRef = useRef(nodes.length + inputs.length);
  const [search, setSearch] = useState("");
  const [panelOpen, setPanelOpen] = useState(true);
  const { screenToFlowPosition } = useReactFlow();

  const titleOf = useCallback(
    (provider: string, tool: string) =>
      tools.find((t) => t.meta.provider === provider && t.meta.name === tool)?.meta.title ?? `${provider}.${tool}`,
    [tools],
  );

  // 事实源 → React Flow 投影
  const rfNodes: Node[] = useMemo(
    () => [
      ...inputs.map((slot) => ({
        id: `${INPUT_ID_PREFIX}${slot.key}`,
        type: "source" as const,
        position: { x: slot.x ?? 0, y: slot.y ?? 0 },
        data: { slot } satisfies InputNodeData as unknown as Record<string, unknown>,
      })),
      ...nodes.map((n) => ({
        id: n.key,
        type: "tool" as const,
        position: { x: n.x ?? 0, y: n.y ?? 0 },
        data: {
          spec: n,
          title: titleOf(n.provider, n.tool),
          badge: badges?.[n.key],
          readOnly,
        } satisfies ToolNodeData as unknown as Record<string, unknown>,
      })),
    ],
    [nodes, inputs, badges, readOnly, titleOf],
  );

  const rfEdges: Edge[] = useMemo(() => {
    const out: Edge[] = [];
    const known = new Set(nodes.map((n) => n.key));
    for (const n of nodes) {
      // 产物流边：inputs_from 端口引用（上游节点或输入源的 file 槽）
      for (const [port, ref] of Object.entries(n.inputs_from ?? {})) {
        if (!ref) continue;
        if (ref.startsWith("inputs.")) {
          const slotKey = ref.slice("inputs.".length);
          out.push({
            id: `${INPUT_ID_PREFIX}${slotKey}->${n.key}:${port}`,
            source: `${INPUT_ID_PREFIX}${slotKey}`,
            target: n.key,
            sourceHandle: "out",
            targetHandle: port,
            animated: badges?.[n.key]?.status === "running",
          });
          continue;
        }
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
      // 参数流边：params 里的 {{inputs.<key> 引用（扫前缀，track 字段引用同源）
      const seenSlots = new Set<string>();
      for (const v of Object.values(n.params ?? {})) {
        if (typeof v !== "string" || !v.includes("{{inputs.")) continue;
        for (const m of v.matchAll(/\{\{inputs\.([a-zA-Z0-9_-]+)(?:\.\w+)?\}\}/g)) {
          if (seenSlots.has(m[1])) continue;
          seenSlots.add(m[1]);
          out.push({
            id: `${INPUT_ID_PREFIX}${m[1]}->${n.key}:params`,
            source: `${INPUT_ID_PREFIX}${m[1]}`,
            target: n.key,
            sourceHandle: "out",
            targetHandle: "params",
            animated: badges?.[n.key]?.status === "running",
            style: { strokeDasharray: "4 3" },
          });
        }
      }
    }
    return out;
  }, [nodes, badges]);

  // 新节点落点：当前视口中心（顺序偏移避免叠在同一格）
  const centerPosition = useCallback(
    (seq: number) => {
      const pt = screenToFlowPosition({ x: window.innerWidth / 2, y: window.innerHeight / 2 });
      return { x: Math.max(20, pt.x - 110 + (seq % 4) * 36), y: Math.max(20, pt.y - 40 + (seq % 5) * 30) };
    },
    [screenToFlowPosition],
  );

  // 通用编辑出口：应用节点变更 → 回推事实源（工具节点/输入源节点分流）
  const onNodesChange = useCallback(
    (changes: NodeChange[]) => {
      if (readOnly || changes.length === 0) return;
      const next = applyNodeChanges(changes, rfNodes);
      const removed = new Set(changes.filter((c) => c.type === "remove").map((c) => (c as { id: string }).id));
      const movedOrRemoved = changes.some((c) => c.type === "remove" || c.type === "position");
      if (!movedOrRemoved) return;

      // 删除输入源节点：连引用（inputs_from 端口引用 + params 引用）一起清
      const removedSlots = new Set(
        [...removed].filter((id) => id.startsWith(INPUT_ID_PREFIX)).map((id) => id.slice(INPUT_ID_PREFIX.length)),
      );
      const nextInputs = inputs
        .filter((s) => !removedSlots.has(s.key))
        .map((s) => {
          const rf = next.find((n) => n.id === `${INPUT_ID_PREFIX}${s.key}`);
          return rf ? { ...s, x: rf.position.x, y: rf.position.y } : s;
        });
      const nextNodes = next.flatMap((n) => {
        if (n.id.startsWith(INPUT_ID_PREFIX) || removed.has(n.id)) return [];
        const old = nodes.find((s) => s.key === n.id);
        if (!old) return [];
        const inputsFrom: Record<string, string> = {};
        for (const [port, ref] of Object.entries(old.inputs_from ?? {})) {
          // 上游被删（节点或输入源）则清引用
          if (ref.startsWith("inputs.")) {
            if (!removedSlots.has(ref.slice("inputs.".length))) inputsFrom[port] = ref;
            continue;
          }
          if (!removed.has(ref.split(":")[0])) inputsFrom[port] = ref;
        }
        let params = old.params;
        if (removedSlots.size > 0) {
          const cleaned: Record<string, unknown> = {};
          for (const [k, v] of Object.entries(params ?? {})) {
            const hit = typeof v === "string" && [...removedSlots].some((sk) => v.includes(`{{inputs.${sk}`));
            cleaned[k] = hit ? "" : v;
          }
          params = cleaned;
        }
        return [{ ...old, x: n.position.x, y: n.position.y, inputs_from: inputsFrom, params }];
      });
      onChange(nextNodes, nextInputs);
    },
    [inputs, nodes, onChange, readOnly, rfNodes],
  );

  const onConnect = useCallback(
    (conn: Connection) => {
      if (readOnly || !conn.targetHandle) return;
      const isInputSource = conn.source.startsWith(INPUT_ID_PREFIX);
      if (isInputSource) {
        const slotKey = conn.source.slice(INPUT_ID_PREFIX.length);
        const slot = inputs.find((s) => s.key === slotKey);
        if (!slot || conn.sourceHandle !== "out") return;
        const target = nodes.find((n) => n.key === conn.target);
        const tool = tools.find((t) => t.meta.provider === target?.provider && t.meta.name === target?.tool);
        if (!target || !tool) return;
        if (conn.targetHandle === "params") {
          // 参数流连线：把引用写进目标节点参数位（file 槽没有参数语义）
          if (slot.type === "file") return;
          const link = paramLinkFor(tool, slot, slotKey);
          if (!link) return;
          onChange(
            nodes.map((n) => (n.key === target.key ? { ...n, params: { ...n.params, ...link } } : n)),
            inputs,
          );
          return;
        }
        // 文件通道连线：file 槽 → 工具输入端口（kind 校验：file 通配）
        if (slot.type !== "file") return;
        const dstPort = portOf(target.provider, target.tool);
        if (!dstPort || !dstPort.in.some((p) => p.key === conn.targetHandle)) return;
        if (target.inputs_from?.[conn.targetHandle]) return; // 端口已占用
        onChange(
          nodes.map((n) =>
            n.key === target.key ? { ...n, inputs_from: { ...(n.inputs_from ?? {}), [conn.targetHandle!]: `inputs.${slotKey}` } } : n,
          ),
          inputs,
        );
        return;
      }
      // 工具→工具产物流连线（kind 交集校验）
      if (conn.source === conn.target || conn.sourceHandle !== "out") return;
      const src = nodes.find((n) => n.key === conn.source);
      const dst = nodes.find((n) => n.key === conn.target);
      if (!src || !dst || conn.targetHandle === "params") return;
      const srcPort = portOf(src.provider, src.tool);
      const dstPort = portOf(dst.provider, dst.tool);
      if (!srcPort || !dstPort) return;
      const inPort = dstPort.in.find((p) => p.key === conn.targetHandle);
      if (!inPort) return;
      if (dst.inputs_from?.[conn.targetHandle!]) return;
      if (!srcPort.outKinds.some((k) => inPort.kind === "file" || k === inPort.kind)) return;
      onChange(
        nodes.map((n) =>
          n.key === dst.key ? { ...n, inputs_from: { ...(n.inputs_from ?? {}), [conn.targetHandle!]: src.key } } : n,
        ),
        inputs,
      );
    },
    [inputs, nodes, onChange, readOnly, tools],
  );

  const isValidConnection = useCallback(
    (conn: Connection | Edge) => {
      if (conn.source === conn.target) return false;
      const isInputSource = conn.source.startsWith(INPUT_ID_PREFIX);
      const dst = nodes.find((n) => n.key === conn.target);
      if (!dst) return false;
      if (isInputSource) {
        const slot = inputs.find((s) => s.key === conn.source.slice(INPUT_ID_PREFIX.length));
        if (!slot) return false;
        if (conn.targetHandle === "params") {
          if (slot.type === "file") return false;
          const tool = tools.find((t) => t.meta.provider === dst.provider && t.meta.name === dst.tool);
          return paramLinkFor(tool, slot, slot.key) != null;
        }
        if (slot.type !== "file") return false;
        const dstPort = portOf(dst.provider, dst.tool);
        return !!dstPort && dstPort.in.some((p) => p.key === conn.targetHandle);
      }
      if (conn.targetHandle === "params") return false;
      const src = nodes.find((n) => n.key === conn.source);
      if (!src || conn.sourceHandle !== "out") return false;
      const srcPort = portOf(src.provider, src.tool);
      const dstPort = portOf(dst.provider, dst.tool);
      if (!srcPort || !dstPort) return false;
      const inPort = dstPort.in.find((p) => p.key === conn.targetHandle);
      if (!inPort) return false;
      if (dst.inputs_from?.[conn.targetHandle!]) return false;
      return srcPort.outKinds.some((k) => inPort.kind === "file" || k === inPort.kind);
    },
    [inputs, nodes, tools],
  );

  const addTool = useCallback(
    (t: ToolInfo) => {
      seqRef.current += 1;
      const key = `${t.meta.provider}-${t.meta.name}-${seqRef.current}`;
      const pos = centerPosition(seqRef.current);
      onChange(
        [...nodes, { key, provider: t.meta.provider, tool: t.meta.name, params: {}, inputs_from: {}, x: pos.x, y: pos.y }],
        inputs,
      );
    },
    [centerPosition, inputs, nodes, onChange],
  );

  const addInput = useCallback(
    (src: (typeof INPUT_SOURCES)[number]) => {
      seqRef.current += 1;
      const key = `${src.prefix}_${seqRef.current}`;
      const pos = centerPosition(seqRef.current);
      onChange(nodes, [...inputs, { key, label: src.label, type: src.type, required: true, x: pos.x, y: pos.y }]);
    },
    [centerPosition, inputs, nodes, onChange],
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
        onNodeDoubleClick={(_, n) => !readOnly && !n.id.startsWith(INPUT_ID_PREFIX) && onOpenParams(n.id)}
        onNodeClick={(_, n) => readOnly && !n.id.startsWith(INPUT_ID_PREFIX) && onNodeSelect?.(n.id)}
        fitView
        minZoom={0.2}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        elementsSelectable={!readOnly}
        deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
      >
        <Background gap={24} />
        {/* 右下角被常驻播放条占据，缩放控件统一放左下 */}
        <Controls showInteractive={false} position="bottom-left" />
        {!readOnly && panelOpen && (
          <div className="absolute left-3 top-3 z-10 flex w-56 flex-col gap-1.5 rounded-lg border border-line bg-raise/95 p-2 shadow-sm backdrop-blur">
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
              <button
                type="button"
                title="收起面板"
                onClick={() => setPanelOpen(false)}
                className="cursor-pointer text-muted hover:text-fg"
              >
                <PanelLeftClose size={13} />
              </button>
            </div>
            <div className="max-h-[26rem] overflow-y-auto">
              <p className="px-1 pb-0.5 pt-1 text-[10px] text-muted">输入源</p>
              {INPUT_SOURCES.map((src) => (
                <button
                  key={src.type}
                  type="button"
                  onClick={() => addInput(src)}
                  className="flex w-full cursor-pointer items-center gap-2 rounded-md px-1.5 py-1.5 text-left text-xs transition-colors duration-150 hover:bg-raise-2"
                >
                  <src.icon size={12} className="shrink-0 text-accent" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{src.label}</span>
                    <span className="block truncate text-[10px] text-muted">{src.desc}</span>
                  </span>
                </button>
              ))}
              <p className="px-1 pb-0.5 pt-2 text-[10px] text-muted">工具</p>
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
        {!readOnly && !panelOpen && (
          <button
            type="button"
            title="展开工具面板"
            onClick={() => setPanelOpen(true)}
            className="absolute left-3 top-3 z-10 flex cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-raise/95 px-2.5 py-2 text-xs shadow-sm backdrop-blur transition-colors duration-150 hover:bg-raise-2"
          >
            <PanelLeftOpen size={13} />
            添加节点
          </button>
        )}
      </ReactFlow>
      {!readOnly && (
        <p className="pointer-events-none absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full bg-raise/90 px-3 py-1 text-[11px] text-muted shadow-sm">
          添加输入源与工具 · 实线连产物、虚线连参数 · 双击工具节点改参数
        </p>
      )}
    </div>
  );
}
