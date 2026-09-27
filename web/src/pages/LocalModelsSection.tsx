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
  MicroLabel,
  ProgressBar,
  SignalDot,
  Skeleton,
  useToast,
} from "../ui";

const KIND_LABELS: Record<string, string> = { asr: "语音识别", tts: "语音合成" };

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

function ModelCard({
  m,
  busy,
  onStart,
  onStop,
  onAskDelete,
}: {
  m: ModelItem;
  busy: boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onAskDelete: (m: ModelItem) => void;
}) {
  const meta = statusMeta(m);
  const pct =
    m.total_bytes > 0 ? Math.min(100, Math.round((m.downloaded_bytes / m.total_bytes) * 100)) : 0;
  return (
    <Card>
      <CardHeader
        title={m.name}
        aside={
          <span className={`inline-flex items-center gap-1.5 text-xs ${TONE_TEXT[meta.tone]}`}>
            <SignalDot tone={meta.tone as "accent" | "meter" | "danger" | "muted"} pulse={meta.pulse} />
            {meta.label}
          </span>
        }
      />
      <CardBody className="space-y-3">
        <p className="text-xs text-muted">{m.summary}</p>
        <p className="font-mono text-[11px] text-fg-2">
          约 {formatSize(m.size_bytes)} · {deviceText(m.requirements)} ·{" "}
          <a href={m.license_url} target="_blank" rel="noreferrer" className={LINK_CLASS}>
            {m.license}
          </a>
        </p>
        {m.status === "downloading" && (
          <div className="space-y-1" role="status" aria-label={`${m.name} 下载进度`}>
            <ProgressBar value={pct} active />
            <p className="font-mono text-[11px] text-muted">
              {formatSize(m.downloaded_bytes)} / {formatSize(m.total_bytes)}（{pct}%）
            </p>
          </div>
        )}
        {m.status === "failed" && <p className="text-xs text-danger">{m.error || "下载失败"}</p>}
        <div className="flex items-center gap-2">
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
            <IconButton label={`删除 ${m.name}`} size="sm" onClick={() => onAskDelete(m)}>
              <Trash2 size={14} strokeWidth={1.75} />
            </IconButton>
          )}
        </div>
      </CardBody>
    </Card>
  );
}

export default function LocalModelsSection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data, isLoading } = useModels();
  const items = data?.items ?? [];
  const busy = items.some((m) => m.status === "downloading" || m.status === "verifying");

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

  const kinds = [...new Set(items.map((m) => m.kind))];
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <MicroLabel>本地模型</MicroLabel>
        {me?.desktop === true && (
          <IconButton label="打开模型目录" size="sm" onClick={() => openDir.mutate()}>
            <FolderOpen size={14} strokeWidth={1.75} />
          </IconButton>
        )}
      </div>
      {kinds.map((kind) => (
        <section key={kind} className="space-y-2">
          <MicroLabel>{KIND_LABELS[kind] ?? kind}</MicroLabel>
          {items
            .filter((m) => m.kind === kind)
            .map((m) => (
              <ModelCard
                key={m.id}
                m={m}
                busy={busy}
                onStart={(id) => start.mutate(id)}
                onStop={(id) => stop.mutate(id)}
                onAskDelete={setDelTarget}
              />
            ))}
        </section>
      ))}
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
    </div>
  );
}
