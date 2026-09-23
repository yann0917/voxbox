import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  AudioLines,
  ChevronDown,
  ChevronUp,
  Download,
  MicVocal,
  Play,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { apiBase, fetchJSON } from "../../lib/api";
import type { Artifact, TaskDetail, TaskStatus, Voice } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { VoicePicker } from "../../components/VoicePicker";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  MicroLabel,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Textarea,
  WavePlayer,
  useToast,
} from "../../ui";

/** 后端 TTS 支持的输出格式（ogg_opus 为服务端枚举值） */
const FORMATS = ["mp3", "wav", "pcm", "ogg_opus"];
const DEFAULT_VOICE = "zh_female_cancan_mars_bigtts";
/** 与后端 tts tool 的 longTextThreshold 一致：超过即分段合成，且分段仅支持 mp3 */
const LONG_TEXT_LIMIT = 1000;

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO */
interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

function formatSize(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 音频产物行：轨道标签 + 波形播放器 + 大小 + 下载 +「去闪避」（携产物为人声跳 /duck） */
function AudioRow({ a }: { a: Artifact }) {
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
function ProgressBody({ run }: { run: Run }) {
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

export default function TTSSyncPanel() {
  const [text, setText] = useState("");
  // 生效音色 ID：由 VoicePicker（筛选/分组/自定义输入）上报
  const [voice, setVoice] = useState("");
  const [format, setFormat] = useState("mp3");
  const [speed, setSpeed] = useState(1);
  const [volume, setVolume] = useState(1);
  const [advanced, setAdvanced] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* 音色列表：从 /api/voices 拉取，选择逻辑由 VoicePicker 承担。
     只重试 1 次：该字段有降级路径，失败要尽快落到手动输入，而不是长时间停在"加载中" */
  const voicesQuery = useQuery({
    queryKey: ["voices"],
    queryFn: () => fetchJSON<{ voices: Voice[] }>("/api/voices"),
    retry: 1,
  });
  const voiceList = voicesQuery.data?.voices ?? [];

  /* WS 事件驱动当前任务进度；终态拉详情拿产物与最终状态 */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setRun({ status: "running", progress: ev.progress ?? 0, note: ev.note ?? "处理中" });
      return;
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<TaskDetail>(`/api/tasks/${taskId}`)
        .then((d) => {
          setRun({
            status: d.task.status,
            progress: d.task.progress,
            note: d.task.progress_note,
            error: d.task.error,
          });
          setDetail(d);
          if (d.task.status === "failed") {
            toast({ tone: "error", title: "合成失败", description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
          toast({ tone: "error", title: "读取任务结果失败", description: e.message });
        });
    }
  }, [ev, taskId, toast]);

  const submit = useMutation({
    mutationFn: () =>
      fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({
          provider: "volcengine",
          tool: "tts",
          params: { text, voice, format, speed_ratio: speed, volume_ratio: volume },
        }),
      }),
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setSubmitError("");
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  // 与后端 splitText 一致：先 TrimSpace 再按 rune 计数，避免首尾空白造成误判
  const charCount = Array.from(text.trim()).length;
  const longText = charCount > LONG_TEXT_LIMIT;
  // 长文本分段合成只支持 mp3：非 mp3 时行内警告并拦住提交（后端必然报错）
  const blockedByLongText = longText && format !== "mp3";
  const canSubmit = text.trim() !== "" && !blockedByLongText && voice.trim() !== "";

  const artifacts = detail?.artifacts ?? [];
  const audioArtifacts = artifacts.filter((a) => a.kind === "audio");
  // 结果页脚用任务自身的字数（summary.char_count），不随输入框后续编辑变化
  const taskChars = detail?.task.summary?.char_count;

  return (
    <>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左：文本编辑区 */}
        <Card className="min-w-0">
          <CardHeader
            title="合成文本"
            icon={<AudioLines size={15} strokeWidth={1.75} />}
            aside={
              <span className="font-mono text-[11px] tabular-nums text-muted">{charCount} 字</span>
            }
          />
          <CardBody className="space-y-3">
            {/* 包裹层仅用于「去输入文本」聚焦：Textarea 组件不透传 ref */}
            <div ref={textWrapRef}>
              <Field
                label="文本内容"
                hint={longText && !blockedByLongText ? "文本超过 1000 字，将分段合成（仅 mp3）。" : "支持中英文混排，数字与标点按原文合成。"}
                error={blockedByLongText ? "长文本分段合成仅支持 mp3 格式：请把格式改为 MP3，或将文本缩短到 1000 字以内。" : undefined}
              >
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={10}
                    placeholder="输入要合成的文本…"
                    className="min-h-[220px] max-h-[46vh]"
                    {...rest}
                  />
                )}
              </Field>
            </div>
          </CardBody>
        </Card>

        {/* 右：参数面板 */}
        <Card className="lg:sticky lg:top-4 lg:self-start">
          <CardHeader
            title="合成参数"
            icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
            aside={<span className="micro">volcengine · tts</span>}
          />
          <CardBody className="space-y-4">
            <VoicePicker
              voices={voiceList}
              loading={voicesQuery.isLoading}
              defaultVoiceId={DEFAULT_VOICE}
              onEffectiveVoiceChange={setVoice}
            />

            <Field label="音频格式" aside={format === "mp3" ? "推荐" : undefined}>
              {({ id, ...rest }) => (
                <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
                  {FORMATS.map((f) => (
                    <option key={f} value={f}>
                      {f === "ogg_opus" ? "OGG Opus" : f.toUpperCase()}
                    </option>
                  ))}
                </Select>
              )}
            </Field>

            {/* 高级参数：默认收起，保持界面干净 */}
            <div className="border-t border-line pt-3">
              <button
                type="button"
                aria-expanded={advanced}
                onClick={() => setAdvanced((v) => !v)}
                className="flex w-full cursor-pointer items-center justify-between gap-2 py-1 text-left"
              >
                <MicroLabel>高级参数</MicroLabel>
                <span className="text-muted">
                  {advanced ? <ChevronUp size={14} strokeWidth={1.75} /> : <ChevronDown size={14} strokeWidth={1.75} />}
                </span>
              </button>
              {advanced && (
                <div className="space-y-4 pt-3">
                  <Field
                    label="语速"
                    aside={<span className="font-mono text-[11px] tabular-nums">{speed.toFixed(1)}×</span>}
                    hint="0.2 – 3.0，默认 1.0"
                  >
                    {({ id, ...rest }) => (
                      <input
                        id={id}
                        type="range"
                        min={0.2}
                        max={3}
                        step={0.1}
                        value={speed}
                        onChange={(e) => setSpeed(Number(e.target.value))}
                        className="w-full cursor-pointer accent-accent"
                        {...rest}
                      />
                    )}
                  </Field>
                  <Field
                    label="音量"
                    aside={<span className="font-mono text-[11px] tabular-nums">{volume.toFixed(1)}×</span>}
                    hint="0.2 – 3.0，默认 1.0"
                  >
                    {({ id, ...rest }) => (
                      <input
                        id={id}
                        type="range"
                        min={0.2}
                        max={3}
                        step={0.1}
                        value={volume}
                        onChange={(e) => setVolume(Number(e.target.value))}
                        className="w-full cursor-pointer accent-accent"
                        {...rest}
                      />
                    )}
                  </Field>
                </div>
              )}
            </div>

            <div className="border-t border-line pt-3">
              <Button
                variant="primary"
                className="w-full"
                icon={<Play size={15} strokeWidth={1.75} />}
                loading={submit.isPending}
                disabled={!canSubmit}
                onClick={() => submit.mutate()}
              >
                开始合成
              </Button>
              {submitError && (
                <p className="mt-2 flex items-start gap-1.5 text-[11px] text-danger">
                  <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                  {submitError}
                </p>
              )}
            </div>
          </CardBody>
        </Card>
      </div>

      {/* 结果区 */}
      <Card className="mt-4">
        <CardHeader
          title="合成结果"
          icon={<AudioLines size={15} strokeWidth={1.75} />}
          aside={run ? <StatusBadge status={run.status} /> : undefined}
        />
        {!run ? (
          <EmptyState
            icon={<AudioLines size={18} strokeWidth={1.75} />}
            title="还没有合成结果"
            description="输入文本、选择音色与格式后点击「开始合成」，音频会出现在这里，可试听与下载。"
            action={
              <Button variant="secondary" size="sm" onClick={focusText}>
                去输入文本
              </Button>
            }
          />
        ) : run.status === "failed" ? (
          <CardBody className="space-y-3">
            <p className="flex items-start gap-2 text-sm text-danger">
              <AlertTriangle size={15} strokeWidth={1.75} className="mt-0.5 shrink-0" />
              <span className="min-w-0 break-words">{run.error || "任务失败，请重试"}</span>
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                icon={<RefreshCw size={13} strokeWidth={1.75} />}
                loading={submit.isPending}
                onClick={() => submit.mutate()}
              >
                重试
              </Button>
              <span className="font-mono text-[11px] text-muted">{taskId?.slice(0, 8)}</span>
            </div>
          </CardBody>
        ) : run.status !== "succeeded" ? (
          <ProgressBody run={run} />
        ) : audioArtifacts.length > 0 ? (
          <CardBody className="space-y-2">
            {audioArtifacts.map((a) => (
              <AudioRow key={a.id} a={a} />
            ))}
            <p className="pt-1 font-mono text-[11px] tabular-nums text-muted">
              {taskChars != null ? `${taskChars} 字` : "合成完成"} · {audioArtifacts[0].format.toUpperCase()}
            </p>
          </CardBody>
        ) : (
          <EmptyState
            icon={<AudioLines size={18} strokeWidth={1.75} />}
            title="任务已完成，但没有音频产物"
            description="可调整参数后重试，或到历史页查看该任务的产物记录。"
            action={
              <Button
                variant="secondary"
                size="sm"
                icon={<RefreshCw size={13} strokeWidth={1.75} />}
                loading={submit.isPending}
                onClick={() => submit.mutate()}
              >
                重试
              </Button>
            }
          />
        )}
      </Card>
    </>
  );
}
