import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, FolderOpen, Pause, RotateCcw, Trash2 } from "lucide-react";
import { useMe } from "../lib/auth";
import {
  deleteModel,
  formatSize,
  openModelsDir,
  startModelDownload,
  stopModelDownload,
  useModels,
  type ModelItem,
  type ModelRequirements,
} from "../lib/models";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ConfirmDialog,
  IconButton,
  ProgressBar,
  SignalDot,
  Skeleton,
  useToast,
} from "../ui";

// 表格「类别」列的短标签(整行宽度优先给模型名与简介)
const KIND_LABELS: Record<string, string> = { asr: "识别", tts: "合成" };

// Tailwind 静态类映射:状态点色调 → 文字色(动态拼接类名不会进产物)
const TONE_TEXT: Record<string, string> = {
  accent: "text-accent",
  meter: "text-meter",
  danger: "text-danger",
  muted: "text-muted",
};

function statusMeta(m: ModelItem): { label: string; tone: string; pulse: boolean } {
  switch (m.status) {
    case "downloading":
      return { label: "下载中", tone: "accent", pulse: true };
    case "verifying":
      return { label: "校验中", tone: "accent", pulse: false };
    case "installed":
      return { label: "已安装", tone: "meter", pulse: false };
    case "failed":
      return { label: "失败", tone: "danger", pulse: false };
    default:
      return m.has_partial
        ? { label: "可续传", tone: "muted", pulse: false }
        : { label: "未下载", tone: "muted", pulse: false };
  }
}

function deviceText(r: ModelRequirements): string {
  return r.device === "cuda"
    ? `需 NVIDIA GPU${r.vram_gb ? ` · ≥${r.vram_gb}GB 显存` : ""}`
    : "CPU 可用";
}

const LINK_CLASS =
  "underline decoration-line underline-offset-2 transition-colors duration-150 hover:text-accent hover:decoration-accent";

function ModelRow({
  m,
  busy,
  engineInstalled,
  onStart,
  onStop,
  onAskDelete,
}: {
  m: ModelItem;
  busy: boolean;
  engineInstalled: (id?: string) => boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onAskDelete: (m: ModelItem) => void;
}) {
  const meta = statusMeta(m);
  const pct =
    m.total_bytes > 0 ? Math.min(100, Math.round((m.downloaded_bytes / m.total_bytes) * 100)) : 0;
  // 副行(进度/错误)接管本行的底边框,主行与副行之间不再画线,视觉上同属一条记录
  const hasDetail = m.status === "downloading" || m.status === "failed";
  return (
    <>
      <tr
        className={`transition-colors duration-150 hover:bg-raise-2 ${hasDetail ? "" : "border-b border-line/60 last:border-0"}`}
      >
        <td className="py-2 pr-3 align-top">
          <div className="text-fg">{m.name}</div>
          <div className="mt-0.5 text-[11px] leading-snug text-muted">{m.summary}</div>
          {!engineInstalled(m.requires_engine) && m.status !== "installed" && (
            <p className="mt-1 text-[11px] text-warn">
              需先下载引擎才能本地运行(模型可先行下载)
            </p>
          )}
        </td>
        <td className="py-2 pr-3 align-top text-fg-2">{KIND_LABELS[m.kind] ?? m.kind}</td>
        <td className="py-2 pr-3 align-top font-mono tabular-nums text-fg-2">
          约 {formatSize(m.size_bytes)}
        </td>
        <td className="py-2 pr-3 align-top font-mono tabular-nums text-fg-2">
          {deviceText(m.requirements)}
        </td>
        <td className="py-2 pr-3 align-top">
          <a href={m.license_url} target="_blank" rel="noreferrer" className={LINK_CLASS}>
            {m.license}
          </a>
        </td>
        <td className="py-2 pr-3 align-top">
          <span className={`inline-flex items-center gap-1.5 whitespace-nowrap ${TONE_TEXT[meta.tone]}`}>
            <SignalDot tone={meta.tone as "accent" | "meter" | "danger" | "muted"} pulse={meta.pulse} />
            {meta.label}
          </span>
        </td>
        <td className="py-2 align-top">
          <div className="flex items-center gap-1.5">
            {m.status === "idle" && (
              <Button
                size="sm"
                variant="primary"
                disabled={busy}
                onClick={() => onStart(m.id)}
                icon={<Download size={13} strokeWidth={1.75} />}
              >
                {m.has_partial ? "继续下载" : "下载"}
              </Button>
            )}
            {m.status === "downloading" && (
              <Button
                size="sm"
                variant="secondary"
                onClick={() => onStop(m.id)}
                icon={<Pause size={13} strokeWidth={1.75} />}
              >
                暂停
              </Button>
            )}
            {m.status === "failed" && (
              <Button
                size="sm"
                variant="primary"
                disabled={busy}
                onClick={() => onStart(m.id)}
                icon={<RotateCcw size={13} strokeWidth={1.75} />}
              >
                重试
              </Button>
            )}
            {m.status === "installed" && (
              <IconButton
                label={`删除 ${m.name}`}
                size="sm"
                className="hover:text-danger"
                onClick={() => onAskDelete(m)}
              >
                <Trash2 size={14} strokeWidth={1.75} />
              </IconButton>
            )}
          </div>
        </td>
      </tr>
      {m.status === "downloading" && (
        <tr className="border-b border-line/60">
          <td colSpan={7} className="pb-3 pr-3">
            <div className="space-y-1" role="status" aria-label={`${m.name} 下载进度`}>
              <ProgressBar value={pct} active />
              <p className="font-mono text-[11px] text-muted">
                {formatSize(m.downloaded_bytes)} / {formatSize(m.total_bytes)}（{pct}%）
              </p>
            </div>
          </td>
        </tr>
      )}
      {m.status === "failed" && (
        <tr className="border-b border-line/60">
          <td colSpan={7} className="pb-3 pr-3 text-xs text-danger">
            {m.error || "下载失败"}
          </td>
        </tr>
      )}
    </>
  );
}

export default function LocalModelsSection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data, isLoading } = useModels();
  const items = data?.items ?? [];
  const busy = items.some((m) => m.status === "downloading" || m.status === "verifying");
  // 引擎依赖判定:同表条目按 id 查状态;无依赖条目视作满足
  const engineInstalled = (id?: string) => {
    if (!id) return true; // 无依赖条目视作满足
    return items.some((m) => m.id === id && m.status === "installed");
  };

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["models"] });
  const start = useMutation({
    mutationFn: startModelDownload,
    onSuccess: invalidate,
    onError: (e: Error) => toast({ tone: "error", title: "下载启动失败", description: e.message }),
  });
  const stop = useMutation({
    mutationFn: stopModelDownload,
    onSuccess: invalidate,
    onError: (e: Error) => toast({ tone: "error", title: "暂停失败", description: e.message }),
  });
  const del = useMutation({
    mutationFn: deleteModel,
    onSuccess: () => {
      toast({ tone: "ok", title: "模型已删除" });
      invalidate();
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });
  const openDir = useMutation({
    mutationFn: openModelsDir,
    onError: (e: Error) => toast({ tone: "error", title: "打开模型目录失败", description: e.message }),
  });

  // 完成边沿 Toast:轮询驱动的状态迁移(downloading/verifying → installed)与操作解耦
  const prevStatuses = useRef<Record<string, string>>({});
  useEffect(() => {
    for (const m of items) {
      const prev = prevStatuses.current[m.id];
      if ((prev === "downloading" || prev === "verifying") && m.status === "installed") {
        toast({ tone: "ok", title: `${m.name} 已就绪` });
      }
      prevStatuses.current[m.id] = m.status;
    }
  }, [items, toast]);

  const [delTarget, setDelTarget] = useState<ModelItem | null>(null);

  if (isLoading) return <Skeleton className="h-24 w-full" />;
  if (items.length === 0) return null;

  return (
    <>
      <Card>
        <CardHeader
          title="本地模型"
          aside={
            me?.desktop === true ? (
              <IconButton label="打开模型目录" size="sm" onClick={() => openDir.mutate()}>
                <FolderOpen size={14} strokeWidth={1.75} />
              </IconButton>
            ) : undefined
          }
        />
        <CardBody>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[680px] text-left text-xs">
              <thead>
                <tr className="border-b border-line text-muted">
                  <th className="py-2 pr-3 font-normal">模型</th>
                  <th className="py-2 pr-3 font-normal">类别</th>
                  <th className="py-2 pr-3 font-normal">大小(约)</th>
                  <th className="py-2 pr-3 font-normal">设备</th>
                  <th className="py-2 pr-3 font-normal">许可证</th>
                  <th className="py-2 pr-3 font-normal">状态</th>
                  <th className="py-2 font-normal">操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((m) => (
                  <ModelRow
                    key={m.id}
                    m={m}
                    busy={busy}
                    engineInstalled={engineInstalled}
                    onStart={(id) => start.mutate(id)}
                    onStop={(id) => stop.mutate(id)}
                    onAskDelete={setDelTarget}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </CardBody>
      </Card>
      <ConfirmDialog
        open={!!delTarget}
        title={`删除 ${delTarget?.name ?? ""}`}
        description={
          delTarget
            ? `将删除模型文件并释放约 ${formatSize(delTarget.downloaded_bytes)} 空间，此操作不可撤销。`
            : ""
        }
        confirmLabel="删除"
        loading={del.isPending}
        onConfirm={() => {
          if (delTarget) del.mutate(delTarget.id);
          setDelTarget(null);
        }}
        onCancel={() => setDelTarget(null)}
      />
    </>
  );
}
