import { useCallback, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  FileText,
  Mic,
  Pencil,
  RefreshCw,
} from "lucide-react";
import { fetchJSON } from "../../lib/api";
import { formatTime } from "../../lib/player";
import { TranscriptList } from "../../components/TranscriptList";
import { useTranscriptSync, type SyncSegment } from "../../lib/useTranscriptSync";
import { WavePlayer } from "../../ui/WavePlayer";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  ProgressBar,
  Skeleton,
  StatusBadge,
  useToast,
} from "../../ui";
import { speakerStats, type QNSegment, type ResultView, type Run } from "./model";

export interface ResultPanelProps {
  view: ResultView;
  run: Run | null;
  taskId: string | null;
  /** 播放器与跳播的轨目标题（任务标题） */
  title: string;
  segments: QNSegment[];
  durationMs?: number;
  /** 音频回放地址：服务端上传流优先，回落录音 Blob（仅本次会话可回放） */
  playSrc: string | null;
  /** 改名初值来自任务 Summary（父级按 taskId 换 key 重挂载，无需同步 effect） */
  initialNames: Record<string, string>;
  submitError: string;
  onReset: () => void;
}

/** 结果区：进度与终态——波形回放 + 分句跟读 + 说话人改名与发言统计。
 *  父级以 taskId 作 key 重挂载本组件：新任务自动重置改名/编辑态。 */
export function ResultPanel({
  view,
  run,
  taskId,
  title,
  segments,
  durationMs,
  playSrc,
  initialNames,
  submitError,
  onReset,
}: ResultPanelProps) {
  const { toast } = useToast();
  const [speakerNames, setSpeakerNames] = useState<Record<string, string>>(initialNames);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const { activeIdx, seekTo } = useTranscriptSync(segments, playSrc);

  /** 展示名：改名覆盖优先，否则「说话人{编号}」（编号原样展示，不 +1，避免与后端编号错位） */
  const speakerLabel = useCallback(
    (id: string) => speakerNames[id] ?? `说话人${id}`,
    [speakerNames],
  );

  const rename = useMutation({
    mutationFn: async (names: Record<string, string>) => {
      if (!taskId) throw new Error("任务不存在");
      // 整体覆盖式：names 缺失后端拒绝，空对象即清空全部改名
      return fetchJSON<{ ok: boolean }>(`/api/tasks/${taskId}/speakers`, {
        method: "PATCH",
        body: JSON.stringify({ names }),
      });
    },
  });

  /** 提交单个改名：乐观更新，失败回滚；清空输入 = 移除覆盖（恢复默认称呼） */
  const commitRename = (id: string) => {
    const prev = speakerNames;
    const name = draft.trim();
    if (name === (prev[id] ?? "")) {
      setEditingId(null);
      return;
    }
    const next = { ...prev };
    if (name) next[id] = name;
    else delete next[id];
    setEditingId(null);
    setSpeakerNames(next);
    rename.mutate(next, {
      onError: (e: Error) => {
        setSpeakerNames(prev);
        toast({ tone: "error", title: "改名失败", description: e.message });
      },
    });
  };

  const startRename = (id: string) => {
    setDraft(speakerNames[id] ?? "");
    setEditingId(id);
  };

  /** 分句行首的说话人胶囊（无标注的句子不渲染） */
  const speakerOf = useCallback(
    (seg: SyncSegment) => {
      const sp = (seg as QNSegment).speaker;
      return sp ? speakerLabel(sp) : undefined;
    },
    [speakerLabel],
  );

  const durationText = durationMs ? `${(durationMs / 60000).toFixed(1)} 分钟` : undefined;
  // Blob 回放 = 上传流不可用时的回落，音频随页面会话失效
  const localOnlyAudio = !!playSrc && playSrc.startsWith("blob:");
  const stats = speakerStats(segments);

  return (
    <Card className="mt-4">
      <CardHeader
        title="文字稿"
        icon={<FileText size={15} strokeWidth={1.75} />}
        aside={run ? <StatusBadge status={run.status} /> : undefined}
      />
      {view === "empty" && (
        <EmptyState
          icon={<Mic size={18} strokeWidth={1.75} />}
          title="还没有录音笔记"
          description="点击上方麦克风说一段话，停止后自动转写，文字稿会显示在这里。"
        />
      )}
      {view === "submitting" && (
        <CardBody className="space-y-3">
          <span className="text-xs text-muted">正在提交录音…</span>
          <ProgressBar value={6} active />
          <Skeleton className="h-9 w-full" />
        </CardBody>
      )}
      {view === "progress" && run && (
        <CardBody className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs text-muted">{run.note || "处理中"}</span>
            <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
          </div>
          <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </CardBody>
      )}
      {view === "done" && (
        <CardBody className="space-y-4">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <p className="flex items-center gap-2 text-sm text-fg">
              <CheckCircle2 size={15} strokeWidth={1.75} className="shrink-0 text-meter" />
              识别完成
            </p>
            <span className="font-mono text-[11px] tabular-nums text-muted">
              {segments.length > 0 && `${segments.length} 句`}
              {durationText && `${segments.length > 0 ? " · " : ""}${durationText}`}
            </span>
          </div>
          {playSrc && (
            <div className="space-y-1">
              <WavePlayer
                src={playSrc}
                title={title}
                sub={durationText}
                durationSec={durationMs ? durationMs / 1000 : undefined}
              />
              {localOnlyAudio && (
                <p className="text-[11px] text-muted">音频仅本次会话可回放</p>
              )}
            </div>
          )}
          {segments.length > 0 && (
            <TranscriptList
              segments={segments}
              activeIdx={activeIdx}
              onSeek={(ms) => seekTo(ms, { title, sub: durationText })}
              speakerOf={speakerOf}
            />
          )}
          {stats.length > 0 && (
            <div className="space-y-2">
              <p className="micro">说话人</p>
              {stats.map(({ id, turns, ms, pct }) => (
                <div key={id} className="flex items-center gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2">
                      {editingId === id ? (
                        <input
                          autoFocus
                          value={draft}
                          onChange={(e) => setDraft(e.target.value)}
                          onBlur={() => commitRename(id)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") commitRename(id);
                            if (e.key === "Escape") setEditingId(null);
                          }}
                          aria-label={`修改${speakerLabel(id)}的称呼`}
                          maxLength={20}
                          className="h-6 w-28 rounded-[var(--radius-sm)] border border-line-strong bg-raise-2 px-2 text-xs text-fg outline-none focus:border-accent"
                        />
                      ) : (
                        <span className="truncate text-xs text-fg">{speakerLabel(id)}</span>
                      )}
                      <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">
                        {turns} 句 · {formatTime(ms / 1000)} · {Math.round(pct * 100)}%
                      </span>
                    </div>
                    <div className="mt-1 h-1 overflow-hidden rounded-full bg-line">
                      <div
                        className="h-full rounded-full bg-accent"
                        style={{ width: `${Math.max(2, Math.round(pct * 100))}%` }}
                      />
                    </div>
                  </div>
                  <button
                    type="button"
                    aria-label={`修改${speakerLabel(id)}的称呼`}
                    onClick={() => startRename(id)}
                    className="shrink-0 cursor-pointer rounded-full border border-line p-1.5 text-muted transition-colors duration-150 hover:border-accent hover:text-accent"
                  >
                    <Pencil size={11} strokeWidth={1.75} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </CardBody>
      )}
      {view === "error" && (
        <CardBody className="space-y-3">
          <p className="flex items-start gap-2 text-sm text-danger">
            <AlertTriangle size={15} strokeWidth={1.75} className="mt-0.5 shrink-0" />
            <span className="min-w-0 break-words">{submitError || run?.error || "转写失败，请重试"}</span>
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              icon={<RefreshCw size={13} strokeWidth={1.75} />}
              onClick={onReset}
            >
              重新录一段
            </Button>
            <span className="font-mono text-[11px] text-muted">{taskId?.slice(0, 8)}</span>
          </div>
        </CardBody>
      )}
    </Card>
  );
}
