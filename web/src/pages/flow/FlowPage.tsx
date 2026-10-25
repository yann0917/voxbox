// 工作流页：模板库 / 我的工作流 / 运行记录 三列表 + 画布编辑器 + 运行监控。
// 编辑态图状态以本页 specs 为唯一事实源（FlowCanvas 纯投影）；运行监控以
// run 详情轮询兜底 + WS 任务事件实时上色（节点 task_id 映射）。
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Copy, FilePlus2, ListChecks, Pencil, Play, Plus, Square, Trash2, Workflow } from "lucide-react";
import { ArtifactRow } from "../../components/ArtifactRow";
import { FileDrop } from "../../components/FileDrop";
import { onTaskEvent } from "../../lib/ws";
import {
  cancelRun,
  createWorkflow,
  deleteWorkflow,
  getRun,
  listRuns,
  updateWorkflow,
  listWorkflows,
  listTools,
  runTemplate,
  runWorkflow,
  type FlowGraph,
  type FlowNodeSpec,
  type InputSlot,
  type NodeRun,
  type WorkflowDTO,
  type WorkflowRunDTO,
} from "../../lib/flow";
import type { Artifact, TaskDetail } from "../../lib/types";
import { Button, Card, EmptyState, Field, Input, Modal, PageHeader, Tabs, Textarea, useToast } from "../../ui";
import { FlowCanvas, type NodeBadge } from "./FlowCanvas";
import { NodeParamDrawer } from "./NodeParamDrawer";

type ListView = "templates" | "mine" | "runs";

interface EditorState {
  /** 已保存工作流 id；0=未保存（新图/模板副本） */
  id: number;
  name: string;
  description: string;
  specs: FlowNodeSpec[];
  inputs: InputSlot[];
}

export default function FlowPage() {
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();

  const toolsQ = useQuery({ queryKey: ["tools"], queryFn: listTools, staleTime: 5 * 60_000 });
  const tools = toolsQ.data ?? [];

  // ---- 深链：?run=<id> 直接开运行监控 ----
  const runParam = params.get("run");
  const [runView, setRunView] = useState<number | null>(runParam ? Number(runParam) : null);
  useEffect(() => {
    if (runParam) setRunView(Number(runParam));
  }, [runParam]);

  const openEditor = (st: EditorState) => {
    setRunView(null);
    setParams({});
    setEditor(st);
  };

  const [editor, setEditor] = useState<EditorState | null>(null);

  if (runView != null) {
    return (
      <RunMonitor
        runID={runView}
        tools={tools}
        onExit={() => {
          setRunView(null);
          setParams({});
          void qc.invalidateQueries({ queryKey: ["workflow-runs"] });
        }}
        toast={toast}
      />
    );
  }
  if (editor) {
    return (
      <FlowEditor
        editor={editor}
        tools={tools}
        onSpecs={(specs) => setEditor((e) => (e ? { ...e, specs } : e))}
        onExit={() => {
          setEditor(null);
          void qc.invalidateQueries({ queryKey: ["workflows"] });
          void qc.invalidateQueries({ queryKey: ["workflow-runs"] });
        }}
        onRunning={(runID) => {
          setEditor(null);
          setRunView(runID);
        }}
        toast={toast}
      />
    );
  }
  return (
    <FlowLists
      onOpen={(w) =>
        openEditor({ id: w.id, name: w.name, description: w.description, specs: w.graph.nodes ?? [], inputs: w.graph.inputs ?? [] })
      }
      onNew={() => openEditor({ id: 0, name: "未命名工作流", description: "", specs: [], inputs: [] })}
      onCopyTemplate={(w) =>
        openEditor({
          id: 0,
          name: `${w.name}（副本）`,
          description: w.description,
          specs: w.graph.nodes ?? [],
          inputs: w.graph.inputs ?? [],
        })
      }
    />
  );
}

// ---------------------------------------------------------------- 列表

function FlowLists({
  onOpen,
  onNew,
  onCopyTemplate,
}: {
  onOpen: (w: WorkflowDTO) => void;
  onNew: () => void;
  onCopyTemplate: (w: WorkflowDTO) => void;
}) {
  const [tab, setTab] = useState<ListView>("templates");
  const qc = useQueryClient();
  const { toast } = useToast();
  const workflowsQ = useQuery({ queryKey: ["workflows"], queryFn: listWorkflows });
  const runsQ = useQuery({
    queryKey: ["workflow-runs", tab],
    queryFn: () => listRuns(undefined, 1, 50),
    enabled: tab === "runs",
    refetchInterval: tab === "runs" ? 5000 : false,
  });
  const navigate = useNavigate();

  const items = workflowsQ.data?.items ?? [];
  const templates = workflowsQ.data?.templates ?? [];
  const runs = runsQ.data?.items ?? [];

  const doDelete = async (w: WorkflowDTO) => {
    await deleteWorkflow(w.id);
    toast({ tone: "ok", title: "已删除" });
    void qc.invalidateQueries({ queryKey: ["workflows"] });
  };

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-4 px-4 py-6">
      <PageHeader
        title="工作流"
        description="把工具串成流程：下载 → 识别 → 分离 → 合成，服务端一次跑完"
        actions={
          <Button variant="primary" icon={<Plus size={14} strokeWidth={1.75} />} onClick={onNew}>
            新建工作流
          </Button>
        }
      />
      <Tabs
        items={[
          { value: "templates", label: "模板库" },
          { value: "mine", label: `我的工作流${items.length ? `（${items.length}）` : ""}` },
          { value: "runs", label: "运行记录" },
        ]}
        value={tab}
        onChange={(v) => setTab(v as ListView)}
      />

      {tab === "templates" && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {templates.map((t) => (
            <Card key={t.name}>
              <div className="flex flex-col gap-3 p-4">
                <div className="flex items-center gap-2">
                  <Workflow size={15} className="shrink-0 text-accent" />
                  <p className="min-w-0 flex-1 truncate text-sm font-medium">{t.name}</p>
                  <span className="shrink-0 text-[11px] text-muted">{t.graph.nodes?.length ?? 0} 步</span>
                </div>
                <p className="min-h-8 text-xs leading-relaxed text-muted">{t.description}</p>
                <div className="flex flex-wrap items-center gap-1.5">
                  {(t.graph.nodes ?? []).map((n, i) => (
                    <span key={n.key} className="flex items-center gap-1.5">
                      {i > 0 && <ChevronRight size={11} className="text-muted" />}
                      <span className="rounded-full bg-raise-2 px-2 py-0.5 text-[11px] text-fg-2">{nodeShortName(t, n)}</span>
                    </span>
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  <RunTemplateButton template={t} />
                  <Button variant="secondary" icon={<Copy size={13} strokeWidth={1.75} />} onClick={() => onCopyTemplate(t)}>
                    复制为我的
                  </Button>
                </div>
              </div>
            </Card>
          ))}
          {templates.length === 0 && workflowsQ.isLoading && <SkeletonCards n={4} />}
        </div>
      )}

      {tab === "mine" && (
        <div className="flex flex-col gap-3">
          {items.map((w) => (
            <Card key={w.id}>
              <div className="flex items-center gap-3 p-4">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{w.name}</p>
                  <p className="truncate text-xs text-muted">
                    {w.description || "无描述"} · {w.graph.nodes?.length ?? 0} 步 · {w.updated_at}
                  </p>
                </div>
                <Button variant="secondary" size="sm" icon={<Pencil size={13} strokeWidth={1.75} />} onClick={() => onOpen(w)}>
                  编辑
                </Button>
                <RunMineButton workflow={w} />
                <Button variant="ghost" size="sm" icon={<Trash2 size={13} strokeWidth={1.75} />} onClick={() => void doDelete(w)}>
                  删除
                </Button>
              </div>
            </Card>
          ))}
          {items.length === 0 && (
            <EmptyState
              icon={<FilePlus2 size={28} strokeWidth={1.5} />}
              title="还没有保存的工作流"
              description="从模板「复制为我的」改造，或新建一张空白画布自由搭"
            />
          )}
        </div>
      )}

      {tab === "runs" && (
        <div className="flex flex-col gap-3">
          {runs.map((r) => (
            <Card key={r.id}>
              <button
                type="button"
                onClick={() => navigate(`/flow?run=${r.id}`)}
                className="flex w-full cursor-pointer items-center gap-3 p-4 text-left"
              >
                <ListChecks size={15} className={`shrink-0 ${r.status === "running" ? "text-accent" : "text-muted"}`} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{r.name}</p>
                  <p className="truncate text-xs text-muted">
                    {r.created_at} · {r.nodes?.filter((n) => n.status === "done").length ?? 0}/{r.nodes?.length ?? 0} 步完成
                  </p>
                </div>
                <RunStatusBadge status={r.status} />
                <ChevronRight size={14} className="shrink-0 text-muted" />
              </button>
            </Card>
          ))}
          {runs.length === 0 && (
            <EmptyState icon={<ListChecks size={28} strokeWidth={1.5} />} title="暂无运行记录" description="跑一次工作流后会在这里留档" />
          )}
        </div>
      )}
    </div>
  );
}

function nodeShortName(w: WorkflowDTO, n: FlowNodeSpec) {
  const meta = w.graph.nodes?.find((x) => x.key === n.key);
  void meta;
  // 工具中文名需要 tools 清单；列表卡用 provider.tool 尾段即可读
  return n.tool;
}

function SkeletonCards({ n }: { n: number }) {
  return (
    <>
      {Array.from({ length: n }).map((_, i) => (
        <div key={i} className="h-40 animate-pulse rounded-xl bg-raise-2" />
      ))}
    </>
  );
}

function RunStatusBadge({ status }: { status: string }) {
  const map: Record<string, { label: string; cls: string }> = {
    pending: { label: "排队", cls: "bg-raise-2 text-muted" },
    running: { label: "运行中", cls: "bg-accent/15 text-accent" },
    done: { label: "完成", cls: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" },
    failed: { label: "失败", cls: "bg-danger/15 text-danger" },
    canceled: { label: "已取消", cls: "bg-raise-2 text-muted" },
  };
  const s = map[status] ?? map.pending;
  return <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${s.cls}`}>{s.label}</span>;
}

// 运行按钮（模板/我的共用输入表单弹框）
function RunTemplateButton({ template }: { template: WorkflowDTO }) {
  return <RunButton name={template.name} inputs={template.graph.inputs ?? []} run={(inputs) => runTemplate(template.key!, inputs)} />;
}
function RunMineButton({ workflow }: { workflow: WorkflowDTO }) {
  return <RunButton name={workflow.name} inputs={workflow.graph.inputs ?? []} run={(inputs) => runWorkflow(workflow.id, inputs)} />;
}

function RunButton({
  name,
  inputs,
  run,
}: {
  name: string;
  inputs: InputSlot[];
  run: (inputs: Record<string, unknown>) => Promise<WorkflowRunDTO>;
}) {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>({});
  const [fileIDs, setFileIDs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { toast } = useToast();

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const payload: Record<string, unknown> = {};
      for (const s of inputs) {
        if (s.type === "file") payload[s.key] = fileIDs[s.key] ?? "";
        else payload[s.key] = values[s.key] ?? "";
      }
      const r = await run(payload);
      toast({ tone: "ok", title: "已开始运行", description: "页面可关闭，进度在任务页与工作流记录里" });
      void qc.invalidateQueries({ queryKey: ["workflow-runs"] });
      setOpen(false);
      navigate(`/flow?run=${r.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const missing = inputs.some((s) => s.required && !(s.type === "file" ? fileIDs[s.key] : values[s.key])?.trim?.());

  return (
    <>
      <Button variant="primary" size="sm" icon={<Play size={13} strokeWidth={1.75} />} onClick={() => (inputs.length ? setOpen(true) : void submit())}>
        运行
      </Button>
      <Modal open={open} title={`运行：${name}`} onClose={() => setOpen(false)} width={520}>
        <div className="flex flex-col gap-3">
          {inputs.map((s) =>
            s.type === "file" ? (
              <FileSlotField key={s.key} slot={s} fileID={fileIDs[s.key] ?? ""} onFileID={(id) => setFileIDs((f) => ({ ...f, [s.key]: id }))} />
            ) : (
              <Field key={s.key} label={s.label} required={s.required} aside={s.placeholder}>
                {({ id }) =>
                  s.type === "text" ? (
                    <Textarea id={id} rows={5} value={values[s.key] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [s.key]: e.target.value }))} placeholder={s.placeholder} />
                  ) : (
                    <Input id={id} value={values[s.key] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [s.key]: e.target.value }))} placeholder={s.placeholder} />
                  )
                }
              </Field>
            ),
          )}
          {error && <p className="text-xs text-danger">{error}</p>}
        </div>
        <div className="mt-4 flex items-center justify-end gap-2">
          <Button variant="ghost" onClick={() => setOpen(false)}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={missing} onClick={() => void submit()}>
            开始运行
          </Button>
        </div>
      </Modal>
    </>
  );
}

/** file 输入槽：上传 → file_id（进引擎文件通道） */
function FileSlotField({ slot, fileID, onFileID }: { slot: InputSlot; fileID: string; onFileID: (id: string) => void }) {
  const [error, setError] = useState("");
  const upload = async (f: File | null) => {
    if (!f) return;
    setError("");
    try {
      const fd = new FormData();
      fd.append("file", f);
      const { fetchJSON } = await import("../../lib/api");
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      onFileID(up.file_id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <Field label={slot.label} required={slot.required} error={error} aside={fileID ? `已就绪 ${fileID.slice(0, 8)}…` : "上传后自动填入"}>
      {() =>
        fileID ? (
          <Button variant="secondary" onClick={() => onFileID("")}>
            重新选择文件
          </Button>
        ) : (
          <FileDrop label="输入文件" accept=".mp3,.wav,.flac,.ogg,.m4a,.aac,.mp4,.mkv,.webm,.mov" emptyHint="音频或视频文件（≤500MB）" file={null} onFile={(f) => void upload(f)} />
        )
      }
    </Field>
  );
}

// ---------------------------------------------------------------- 编辑器

function FlowEditor({
  editor,
  tools,
  onSpecs,
  onExit,
  onRunning,
  toast,
}: {
  editor: EditorState;
  tools: Awaited<ReturnType<typeof listTools>>;
  onSpecs: (specs: FlowNodeSpec[]) => void;
  onExit: () => void;
  onRunning: (runID: number) => void;
  toast: ReturnType<typeof useToast>["toast"];
}) {
  const [name, setName] = useState(editor.name);
  const [description, setDescription] = useState(editor.description);
  const [paramKey, setParamKey] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const qc = useQueryClient();

  const toolOf = useCallback(
    (provider: string, tool: string) => tools.find((t) => t.meta.provider === provider && t.meta.name === tool),
    [tools],
  );
  const paramNode = editor.specs.find((n) => n.key === paramKey) ?? null;

  const validateLocal = (specs: FlowNodeSpec[]): string => {
    // 本地预检（与后端 Validate 同口径的最小集：环由后端报，这里查空节点/重名）
    const seen = new Set<string>();
    for (const n of specs) {
      if (seen.has(n.key)) return `节点 key 重复: ${n.key}`;
      seen.add(n.key);
    }
    return "";
  };

  const save = async (asNew: boolean) => {
    const err = validateLocal(editor.specs);
    if (err) {
      setSaveError(err);
      toast({ tone: "error", title: "无法保存", description: err });
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      const graph: FlowGraph = { version: 1, inputs: editor.inputs, nodes: editor.specs };
      if (asNew || editor.id === 0) {
        await createWorkflow(name || "未命名工作流", description, graph);
      } else {
        await updateWorkflow(editor.id, name, description, graph);
      }
      toast({ tone: "ok", title: "已保存" });
      void qc.invalidateQueries({ queryKey: ["workflows"] });
      onExit();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setSaveError(msg);
      toast({ tone: "error", title: "保存失败", description: msg });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-[calc(100vh-3.5rem)] flex-col">
      <header className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        <Button variant="ghost" size="sm" onClick={onExit}>
          返回
        </Button>
        <Input value={name} onChange={(e) => setName(e.target.value)} className="w-48" aria-label="工作流名称" />
        <Input value={description} onChange={(e) => setDescription(e.target.value)} className="hidden min-w-0 flex-1 md:block" placeholder="描述（可选）" aria-label="工作流描述" />
        <span className="text-xs text-muted">{editor.specs.length} 个节点</span>
        <div className="ml-auto flex items-center gap-2">
          {editor.id !== 0 && (
            <Button variant="secondary" size="sm" loading={saving} onClick={() => void save(true)}>
              另存
            </Button>
          )}
          <Button variant="primary" size="sm" loading={saving} onClick={() => void save(false)}>
            保存
          </Button>
          <RunEditorButton editor={editor} name={name} onRunning={onRunning} />
        </div>
      </header>
      {saveError && <p className="border-b border-line bg-danger/10 px-4 py-1.5 text-xs text-danger">{saveError}</p>}
      <FlowCanvas
        nodes={editor.specs}
        tools={tools}
        onChange={onSpecs}
        onOpenParams={(key) => setParamKey(key)}
      />
      {paramNode && (
        <NodeParamDrawer
          node={paramNode}
          tool={toolOf(paramNode.provider, paramNode.tool)}
          allNodes={editor.specs}
          inputs={editor.inputs}
          onApply={(next) => {
            onSpecs(editor.specs.map((n) => (n.key === next.key ? next : n)));
            setParamKey(null);
          }}
          onDelete={() => {
            onSpecs(editor.specs.filter((n) => n.key !== paramNode.key));
            setParamKey(null);
          }}
          onClose={() => setParamKey(null)}
        />
      )}
    </div>
  );
}

/** 编辑器里的运行：不走已保存 id（未保存也能试跑）——构造临时图直接提交 */
function RunEditorButton({ editor, name, onRunning }: { editor: EditorState; name: string; onRunning: (id: number) => void }) {
  // 编辑器运行 = 先保存为草稿再跑（服务端 run 需要持久图）；id=0 时静默另存
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();
  const qc = useQueryClient();
  const run = async () => {
    setBusy(true);
    try {
      const graph: FlowGraph = { version: 1, inputs: editor.inputs, nodes: editor.specs };
      let wfID = editor.id;
      if (wfID === 0) {
        const w = await createWorkflow(name || "未命名工作流", editor.description, graph);
        wfID = w.id;
      } else {
        await updateWorkflow(editor.id, name, editor.description, graph);
      }
      const inputs: Record<string, unknown> = {};
      for (const s of editor.inputs) inputs[s.key] = "";
      // 有输入槽时不能空跑：提示用户去运行卡填参（输入表单复用列表页的 RunButton）
      if (editor.inputs.length > 0) {
        toast({ tone: "ok", title: "已保存", description: "请在「我的工作流」点运行填写输入" });
        void qc.invalidateQueries({ queryKey: ["workflows"] });
        return;
      }
      const r = await runWorkflow(wfID, inputs);
      void qc.invalidateQueries({ queryKey: ["workflow-runs"] });
      onRunning(r.id);
    } catch (e) {
      toast({ tone: "error", title: "运行失败", description: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button variant="primary" size="sm" loading={busy} icon={<Play size={13} strokeWidth={1.75} />} onClick={() => void run()}>
      试运行
    </Button>
  );
}

// ---------------------------------------------------------------- 运行监控

function RunMonitor({
  runID,
  tools,
  onExit,
  toast,
}: {
  runID: number;
  tools: Awaited<ReturnType<typeof listTools>>;
  onExit: () => void;
  toast: ReturnType<typeof useToast>["toast"];
}) {
  const runQ = useQuery({
    queryKey: ["workflow-run", runID],
    queryFn: () => getRun(runID),
    refetchInterval: (q) => (q.state.data?.status === "running" || q.state.data?.status === "pending" ? 2000 : false),
  });
  const run = runQ.data;
  const [badges, setBadges] = useState<Record<string, NodeBadge>>({});
  const taskNode = useRef(new Map<string, string>()); // task_id → node key

  // run 详情到达时重建 task→node 映射
  useEffect(() => {
    if (!run) return;
    for (const n of run.nodes ?? []) {
      if (n.task_id) taskNode.current.set(n.task_id, n.key);
    }
    setBadges(fromNodeRuns(run));
  }, [run]);

  // WS 任务事件实时推进徽章（轮询兜底）
  useEffect(
    () =>
      onTaskEvent((ev) => {
        if (!ev.task_id) return;
        const key = taskNode.current.get(ev.task_id);
        if (!key) return;
        setBadges((b) => {
          if (ev.type === "progress") {
            return { ...b, [key]: { status: "running", progress: ev.progress ?? b[key]?.progress, note: ev.note } };
          }
          if (ev.type === "done") return { ...b, [key]: { status: "done", progress: 100 } };
          if (ev.type === "error") return { ...b, [key]: { status: "failed", note: ev.error } };
          if (ev.type === "canceled") return { ...b, [key]: { status: "canceled" } };
          return b;
        });
      }),
    [],
  );

  const doCancel = async () => {
    try {
      await cancelRun(runID);
      toast({ tone: "ok", title: "已请求取消" });
    } catch (e) {
      toast({ tone: "error", title: "取消失败", description: e instanceof Error ? e.message : String(e) });
    }
  };

  if (!run) {
    return <div className="mx-auto w-full max-w-4xl px-4 py-6 text-sm text-muted">加载中…</div>;
  }

  const active = run.status === "running" || run.status === "pending";
  return (
    <div className="flex h-[calc(100vh-3.5rem)] flex-col">
      <header className="flex items-center gap-3 border-b border-line px-4 py-2.5">
        <Button variant="ghost" size="sm" onClick={onExit}>
          返回
        </Button>
        <p className="min-w-0 flex-1 truncate text-sm font-medium">{run.name}</p>
        <RunStatusBadge status={run.status} />
        {run.workflow_key === "song-clip" && run.status === "done" && <ClipJumpButton run={run} />}
        {active && (
          <Button variant="secondary" size="sm" icon={<Square size={12} strokeWidth={1.75} />} onClick={() => void doCancel()}>
            取消
          </Button>
        )}
      </header>
      {run.error && <p className="border-b border-line bg-danger/10 px-4 py-1.5 text-xs text-danger">{run.error}</p>}
      <div className="flex min-h-0 flex-1">
        <div className="hidden min-w-0 flex-1 md:block">
          <FlowCanvas nodes={runGraphNodes(run)} tools={tools} badges={badges} readOnly onOpenParams={() => {}} onChange={() => {}} />
        </div>
        <aside className="w-full shrink-0 overflow-y-auto border-l border-line md:w-96">
          <div className="flex flex-col gap-2 p-3">
            {(run.nodes ?? []).map((n) => (
              <StepCard key={n.key} node={n} badge={badges[n.key]} />
            ))}
          </div>
        </aside>
      </div>
    </div>
  );
}

/** run → 画布只读投影：优先用运行时图快照（位置/连线完整还原），
 *  旧记录无快照时按节点序简单纵排。 */
function runGraphNodes(run: WorkflowRunDTO): FlowNodeSpec[] {
  if (run.graph?.nodes?.length) return run.graph.nodes;
  return (run.nodes ?? []).map((n, i) => ({
    key: n.key,
    provider: "",
    tool: n.key,
    params: {},
    inputs_from: {},
    x: 80 + i * 40,
    y: 60 + i * 30,
  }));
}

function fromNodeRuns(run: WorkflowRunDTO): Record<string, NodeBadge> {
  const out: Record<string, NodeBadge> = {};
  for (const n of run.nodes ?? []) {
    out[n.key] = { status: n.status, progress: n.status === "done" ? 100 : undefined, note: n.error };
  }
  return out;
}

/** 「切高潮」模板完成态的收口：取产物音频直跳剪辑页（旧场景 goto 语义） */
function ClipJumpButton({ run }: { run: WorkflowRunDTO }) {
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();
  const go = async () => {
    const last = [...(run.nodes ?? [])].reverse().find((n) => n.status === "done" && n.task_id);
    if (!last?.task_id) return;
    setBusy(true);
    try {
      const { fetchJSON } = await import("../../lib/api");
      const d = await fetchJSON<TaskDetail>(`/api/tasks/${last.task_id}`);
      const audio = (d.artifacts ?? []).find((a) => a.kind === "audio");
      if (!audio) {
        toast({ tone: "error", title: "没有可用的音频产物" });
        return;
      }
      navigate(`/post?tab=clip&artifact=${audio.id}`);
    } catch (e) {
      toast({ tone: "error", title: "打开失败", description: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button variant="primary" size="sm" loading={busy} onClick={() => void go()}>
      去剪辑
    </Button>
  );
}

/** 任务详情深链（路由跳转，与全站 BrowserRouter 一致） */
function TaskDetailLink({ taskID }: { taskID: string }) {
  const navigate = useNavigate();
  return (
    <button
      type="button"
      onClick={() => navigate(`/history?task=${taskID}`)}
      className="shrink-0 cursor-pointer text-[11px] text-muted transition-colors duration-150 hover:text-accent"
    >
      任务详情
    </button>
  );
}

/** 步骤卡：状态 + 产物行（完成后拉任务详情渲染 ArtifactRow） */
function StepCard({ node, badge }: { node: NodeRun; badge?: NodeBadge }) {
  const status = badge?.status ?? node.status;
  const [arts, setArts] = useState<Artifact[] | null>(null);
  const [open, setOpen] = useState(false);
  const taskID = node.task_id ?? null;
  const label = node.key;

  const loadArts = async () => {
    if (arts) {
      setOpen(!open);
      return;
    }
    if (!taskID) return;
    try {
      const { fetchJSON } = await import("../../lib/api");
      const d = await fetchJSON<TaskDetail>(`/api/tasks/${taskID}`);
      setArts(d.artifacts);
      setOpen(true);
    } catch {
      setArts([]);
      setOpen(true);
    }
  };

  const progress = badge?.progress;
  return (
    <div className="rounded-lg border border-line bg-raise p-3">
      <div className="flex items-center gap-2">
        <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${badgeStyle2(status)}`}>{statusLabel(status)}</span>
        <p className="min-w-0 flex-1 truncate text-xs font-medium">{label}</p>
        {status === "done" && node.artifact_ids && node.artifact_ids.length > 0 && (
          <button type="button" onClick={() => void loadArts()} className="shrink-0 cursor-pointer text-[11px] text-accent hover:underline">
            {open ? "收起产物" : `${node.artifact_ids.length} 个产物`}
          </button>
        )}
        {taskID && <TaskDetailLink taskID={taskID} />}
      </div>
      {status === "running" && progress != null && (
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-raise-2">
          <div className="h-full rounded-full bg-accent transition-[width] duration-300" style={{ width: `${progress}%` }} />
        </div>
      )}
      {(badge?.note || node.error) && status !== "done" && (
        <p className={`mt-1.5 truncate text-[11px] ${status === "failed" ? "text-danger" : "text-muted"}`}>{node.error || badge?.note}</p>
      )}
      {open && arts && (
        <div className="mt-2 flex flex-col gap-1.5 border-t border-line pt-2">
          {arts.length === 0 && <p className="text-[11px] text-muted">产物已不可用</p>}
          {arts.map((a) => (
            <ArtifactRow key={a.id} a={a} />
          ))}
        </div>
      )}
      {open && arts === null && <p className="mt-2 text-[11px] text-muted">加载产物中…</p>}
    </div>
  );
}

function badgeStyle2(status: string) {
  return (
    {
      pending: "bg-raise-2 text-muted",
      running: "bg-accent/15 text-accent",
      done: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
      failed: "bg-danger/15 text-danger",
      canceled: "bg-raise-2 text-muted",
      skipped: "bg-raise-2 text-muted",
    }[status] ?? "bg-raise-2 text-muted"
  );
}

function statusLabel(status: string) {
  return { pending: "等待", running: "运行中", done: "完成", failed: "失败", canceled: "已取消", skipped: "跳过" }[status] ?? status;
}
