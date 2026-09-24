import { useNavigate } from "react-router-dom";
import { Download, MicVocal } from "lucide-react";
import { apiBase } from "../../lib/api";
import type { Artifact, TaskStatus } from "../../lib/types";
import { CardBody, ProgressBar, Skeleton, StatusBadge, WavePlayer } from "../../ui";

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO（qianwen/xiaomi 面板共用）。 */
export interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

export function formatSize(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 音频产物行：轨道标签 + 波形播放器 + 大小 + 下载 +「去闪避」（携产物为人声跳 /duck） */
export function AudioRow({ a }: { a: Artifact }) {
  const navigate = useNavigate();
  return (
    <div className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
      <span className="w-14 shrink-0 text-xs text-fg-2">音频</span>
      <WavePlayer
        src={`${apiBase}/api/artifacts/${a.id}/stream`}
        title={a.filename}
        sub={a.format.toUpperCase()}
        durationSec={a.duration_ms ? a.duration_ms / 1000 : undefined}
        className="min-w-0 flex-1"
      />
      <span className="hidden shrink-0 font-mono text-[11px] tabular-nums text-muted sm:inline">
        {formatSize(a.size)}
      </span>
      <button
        type="button"
        onClick={() => navigate(`/post?tab=duck&vocal=${a.id}`)}
        title="以该产物为人声，到口播闪避页压制 BGM"
        className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
      >
        <MicVocal size={13} strokeWidth={1.75} />
        去闪避
      </button>
      <a
        href={`${apiBase}/api/artifacts/${a.id}/download`}
        className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
      >
        <Download size={13} strokeWidth={1.75} />
        下载
      </a>
    </div>
  );
}

/** 运行中：状态 + 细进度条 + 备注 + 与波形同形的骨架 */
export function ProgressBody({ run }: { run: Run }) {
  return (
    <CardBody className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <StatusBadge status={run.status} />
        <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
      </div>
      <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
      <p className="text-xs text-muted">{run.note || "处理中"}</p>
      <Skeleton className="h-9 w-full" />
    </CardBody>
  );
}
