import { RotateCw } from "lucide-react";
import { Link } from "react-router-dom";
import { Button, MicroLabel, WavePlayer } from "../ui";
import { toolLabel, toolRoute } from "../lib/toolNames";
import { formatTime } from "../lib/player";
import { resolvePlaySrc } from "../lib/playback";
import { useTranscriptSync } from "../lib/useTranscriptSync";
import { TranscriptList } from "./TranscriptList";
import { ArtifactRow } from "./ArtifactRow";
import { TaskMetaEditor } from "./TaskMetaEditor";
import { NotebookSection } from "./notebook/NotebookSection";
import type { TaskDetail as TaskDetailData } from "../lib/types";

interface TaskDetailPanelProps {
  d: TaskDetailData;
  rerunPending: boolean;
  onRerun: () => void;
}

/** 参数值展示：字符串原样，其余 JSON 序列化；空值显示 — */
function fmtParam(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "string") return v;
  return JSON.stringify(v);
}

/** 分句时长超过 1 小时改用 h:mm:ss，避免 120 分钟读数不直观 */
function pickTimecode(segments: { start_ms: number; end_ms: number }[]): (ms: number) => string {
  const last = segments[segments.length - 1];
  if (last && last.end_ms > 3_600_000) {
    return (ms) => {
      const total = Math.max(0, Math.floor(ms / 1000));
      const h = Math.floor(total / 3600);
      const m = String(Math.floor((total % 3600) / 60)).padStart(2, "0");
      const s = String(total % 60).padStart(2, "0");
      return `${h}:${m}:${s}`;
    };
  }
  return (ms) => formatTime(ms / 1000);
}

/** 历史任务详情：参数回显 + 输入可播时分句同步回放 + 产物列表 + 重跑。 */
export function TaskDetailPanel({ d, rerunPending, onRerun }: TaskDetailPanelProps) {
  const { task, artifacts } = d;
  const segments = task.summary?.segments ?? [];
  // 无 segments 的纯文本识别（本地 R2T2 / 智谱）：纪要区以 Summary.text 兜底——
  // 以单段全文喂问答上下文（无时间戳，引用 chip 最多回跳起点），说话人区自动隐藏
  const fullText = task.summary?.text?.trim() ?? "";
  const notebookSegments =
    segments.length > 0 ? segments : fullText ? [{ text: fullText, start_ms: 0, end_ms: 0 }] : [];
  const playSrc = resolvePlaySrc(task, artifacts);
  const { activeIdx, seekTo } = useTranscriptSync(segments, playSrc);
  const params = Object.entries(task.params ?? {});
  const timecode = pickTimecode(segments);
  const track = { title: `${toolLabel(task.tool)} 回放`, sub: task.id.slice(0, 8) };

  return (
    <div className="space-y-3">
      {/* 标题与标签（内联编辑）：手改置 title_edited，ASR 完成时的自动派生让位 */}
      <TaskMetaEditor task={task} />

      {/* 参数回显 */}
      {params.length > 0 && (
        <div className="space-y-1">
          <MicroLabel>参数</MicroLabel>
          <div className="grid gap-x-6 gap-y-1 sm:grid-cols-2">
            {params.map(([k, v]) => (
              <div key={k} className="flex min-w-0 items-baseline gap-2 text-xs">
                <span className="shrink-0 font-mono text-[11px] text-muted">{k}</span>
                <span className="min-w-0 truncate font-mono text-[11px] tabular-nums text-fg-2" title={fmtParam(v)}>
                  {fmtParam(v)}
                </span>
              </div>
            ))}
            {task.input?.file_ids?.length ? (
              <div className="flex min-w-0 items-baseline gap-2 text-xs">
                <span className="shrink-0 font-mono text-[11px] text-muted">上传文件</span>
                <span className="font-mono text-[11px] tabular-nums text-fg-2">{task.input.file_ids.length} 个</span>
              </div>
            ) : null}
          </div>
        </div>
      )}

      {/* 分句同步回放：与 ASR/妙记页同一套点句跳播 + 跟读高亮 */}
      {task.status === "succeeded" && segments.length > 0 && (
        <div className="space-y-2">
          {playSrc && (
            <div className="rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
              <WavePlayer src={playSrc} title={track.title} sub={track.sub} className="min-w-0" />
            </div>
          )}
          <TranscriptList
            segments={segments}
            activeIdx={playSrc ? activeIdx : -1}
            onSeek={playSrc ? (ms) => seekTo(ms, track) : undefined}
            formatTimecode={timecode}
            maxHeight={256}
          />
        </div>
      )}

      {/* 纪要区（ASR 且有转写内容时）：说话人改名统计 + AI 提炼 + 就稿问答，引用 chip 复用同一跳播。
          无 segments 的纯文本任务以 Summary.text 兜底成单段。key=任务 id：切换任务即重置改名/加工/问答的本会话状态 */}
      {task.tool === "asr" && notebookSegments.length > 0 && (
        <NotebookSection key={task.id} task={task} segments={notebookSegments} onSeek={playSrc ? (ms) => seekTo(ms, track) : undefined} />
      )}

      {/* 产物 */}
      {artifacts.length > 0 && (
        <div className="space-y-2">
          {artifacts.map((a) => (
            <ArtifactRow key={a.id} a={a} />
          ))}
        </div>
      )}

      {/* 操作 */}
      <div className="flex flex-wrap items-center gap-2">
        {/* live 任务无输入文件,重跑必失败——隐藏重跑入口 */}
        {task.params?.version !== "live" && (
          <Button variant="secondary" size="sm" icon={<RotateCw size={13} strokeWidth={1.75} />} loading={rerunPending} onClick={onRerun}>
            重跑
          </Button>
        )}
        {toolRoute[task.tool] && (
          <Link
            to={toolRoute[task.tool]}
            className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
          >
            打开工具页
          </Link>
        )}
        {task.error && <span className="min-w-0 truncate text-[11px] text-danger">{task.error}</span>}
      </div>
    </div>
  );
}
