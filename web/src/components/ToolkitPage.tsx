// 工具集页面骨架：卡片分组（视频/音频/图片）→ 选中展开 OpTaskForm → 任务进度 → 产物列表。
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Clapperboard, Copy, Download, Film, ImageIcon, Layers, Waves } from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import type { TaskStatus } from "../lib/types";
import { useTaskEvents } from "../lib/ws";
import { OpTaskForm, type ToolEntry } from "../components/OpTaskForm";
import { Card, CardBody, CardHeader, PageHeader, ProgressBar, StatusBadge, WaveLoader, WavePlayer, useToast } from "../ui";

interface SepArtifact {
  id: string;
  kind: string;
  filename: string;
  format?: string;
  size?: number;
  meta?: { track?: string; url?: string; source?: string; tool?: string };
}
interface SepTask {
  id: string;
  provider?: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: Record<string, unknown>;
}

const GROUPS = ["视频", "音频", "图片"] as const;

function formatSize(bytes?: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function ToolkitPage({
  provider,
  title,
  description,
  hint,
  envBanner,
}: {
  provider: string;
  title: string;
  description: string;
  /** 提交按钮旁说明 */
  hint: string;
  /** 顶部依赖/服务提示（返回 null 表示不显示） */
  envBanner: () => React.ReactNode;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const { toast } = useToast();
  const ev = useTaskEvents();

  const tools = useQuery({
    queryKey: ["tools"],
    queryFn: () => fetchJSON<ToolEntry[]>("/api/tools"),
    staleTime: 5 * 60_000,
  });

  const [taskId, setTaskId] = useState<string | null>(null);
  const [task, setTask] = useState<SepTask | null>(null);
  const [artifacts, setArtifacts] = useState<SepArtifact[]>([]);

  const localTools = useMemo(
    () => (tools.data ?? []).filter((t) => t.meta.provider === provider && t.meta.name !== "separate"),
    [tools.data, provider],
  );
  const selected = searchParams.get("tool");
  const active = localTools.find((t) => t.meta.name === selected) ?? null;

  const pickTool = (name: string | null) => {
    setTaskId(null);
    setTask(null);
    setArtifacts([]);
    setSearchParams(name ? { tool: name } : {}, { replace: true });
  };

  // WS 事件驱动进度；终态拉详情取产物
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setTask((t) => ({
        ...(t ?? ({ id: taskId, status: "running" } as SepTask)),
        progress: ev.progress ?? 0,
        progress_note: ev.note ?? "",
      }));
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<{ task: SepTask; artifacts: SepArtifact[] }>(`/api/tasks/${taskId}`)
        .then((d) => {
          setTask(d.task);
          setArtifacts(d.artifacts);
        })
        .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
    }
  }, [ev, taskId, toast]);

  const submitTask = (body: Record<string, unknown>) =>
    fetchJSON<{ task_id: string }>("/api/tasks", { method: "POST", body: JSON.stringify(body) })
      .then((d) => {
        setTaskId(d.task_id);
        setTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
        setArtifacts([]);
        window.scrollTo({ top: document.body.scrollHeight, behavior: "smooth" });
      })
      .catch((e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }));

  const running = task?.status === "running" || task?.status === "pending";

  return (
    <>
      <PageHeader title={title} description={description} />
      {envBanner()}
      {GROUPS.map((g) => {
        const items = localTools.filter((t) => t.meta.group === g);
        if (items.length === 0) return null;
        const Icon = g === "视频" ? Clapperboard : g === "音频" ? Waves : ImageIcon;
        return (
          <section key={g} className="mt-4 first:mt-0">
            <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-fg-2">
              <Icon size={14} strokeWidth={1.75} />
              {g}功能
            </div>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {items.map((t) => (
                <button
                  key={t.meta.name}
                  className={`cursor-pointer rounded-[var(--radius-md)] border p-3 text-left transition-colors duration-150 ${
                    active?.meta.name === t.meta.name
                      ? "border-accent bg-accent/5"
                      : "border-line bg-raise-1 hover:border-accent/50 hover:bg-raise-2"
                  }`}
                  onClick={() => pickTool(active?.meta.name === t.meta.name ? null : t.meta.name)}
                >
                  <div className="text-sm font-medium text-fg">{t.meta.title}</div>
                  <div className="mt-1 line-clamp-2 text-[11px] leading-relaxed text-muted">{t.meta.description}</div>
                </button>
              ))}
            </div>
          </section>
        );
      })}
      {tools.isLoading && (
        <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-20 animate-pulse rounded-[var(--radius-md)] border border-line bg-raise-1" />
          ))}
        </div>
      )}

      {active && (
        <Card className="mt-4">
          <CardHeader title={active.meta.title} icon={<Waves size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-5">
            <OpTaskForm key={active.meta.name} tool={active} disabled={running} hint={hint} onSubmit={submitTask} />
          </CardBody>
        </Card>
      )}

      {task && (
        <Card className="mt-4">
          <CardHeader
            title="处理进度"
            aside={
              <>
                {task.cost_ms ? <span className="micro">{`${(task.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={task.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={task.progress} active={running} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {running && <WaveLoader label="任务处理中" className="shrink-0" />}
              <span className="min-w-0">{task.progress_note || "—"}</span>
            </p>
            {task.error && <p className="text-xs text-danger break-words">{task.error}</p>}
          </CardBody>
        </Card>
      )}

      {artifacts.length > 0 && (
        <Card className="mt-4">
          <CardHeader
            title="处理结果"
            icon={<Layers size={15} strokeWidth={1.75} />}
            aside={<span className="micro">{artifacts.length} 个</span>}
          />
          <CardBody className="space-y-2">
            {artifacts.map((a) => (
              <div
                key={a.id}
                className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3"
              >
                {a.kind === "image" ? (
                  <img
                    src={`${apiBase}/api/artifacts/${a.id}/stream`}
                    alt={a.filename}
                    loading="lazy"
                    decoding="async"
                    className="h-14 w-24 shrink-0 rounded object-cover"
                  />
                ) : a.kind === "video" ? (
                  <Film size={18} strokeWidth={1.75} className="shrink-0 text-fg-2" />
                ) : null}
                <div className="min-w-0 flex-1">
                  {a.kind === "audio" ? (
                    <WavePlayer
                      src={`${apiBase}/api/artifacts/${a.id}/stream`}
                      title={a.filename}
                      sub="音频"
                      className="min-w-0"
                    />
                  ) : (
                    <div className="truncate text-xs text-fg">{a.filename}</div>
                  )}
                </div>
                <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">{formatSize(a.size)}</span>
                <a
                  href={`${apiBase}/api/artifacts/${a.id}/download`}
                  className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                >
                  <Download size={13} strokeWidth={1.75} />
                  下载
                </a>
                {a.meta?.url && (
                  <button
                    className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                    title="复制公网直链（有时效）"
                    onClick={() =>
                      navigator.clipboard
                        .writeText(a.meta!.url!)
                        .then(() => toast({ tone: "ok", title: "直链已复制" }))
                        .catch((e: Error) => toast({ tone: "error", title: "复制失败", description: e.message }))
                    }
                  >
                    <Copy size={13} strokeWidth={1.75} />
                    复制直链
                  </button>
                )}
              </div>
            ))}
          </CardBody>
        </Card>
      )}

      {!active && !taskId && (
        <Card className="mt-4">
          <div className="p-6 text-center text-xs text-muted">
            点上方卡片选择功能。人声/伴奏分离在
            <button className="mx-1 cursor-pointer text-accent underline-offset-2 hover:underline" onClick={() => navigate("/separate")}>
              人声分离
            </button>
            页。
          </div>
        </Card>
      )}
    </>
  );
}
