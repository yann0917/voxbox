import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  AudioLines,
  Download,
  MicVocal,
  Play,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { apiBase, fetchJSON } from "../../lib/api";
import type { Artifact, TaskDetail, TaskStatus } from "../../lib/types";
import QianwenVoicePicker, { type QianwenVoice } from "../../components/QianwenVoicePicker";
import { useTaskEvents } from "../../lib/ws";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  Input,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Textarea,
  WavePlayer,
  useToast,
} from "../../ui";

/** 千问 qwen3-tts 非流式支持的输出格式（与后端 ParamSpecs 同词表） */
/** 合成模型：instruct 版额外支持自然语言风格指令（instructions 仅其生效） */
type QianwenModel = "qwen3-tts-flash" | "qwen3-tts-instruct-flash";
const DEFAULT_MODEL: QianwenModel = "qwen3-tts-flash";

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

/** 千问非流式语音合成面板：单请求整段返回（无分段/流式），instruct 模型可带风格指令。 */
export default function QianwenTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState<QianwenModel>(DEFAULT_MODEL);
  const [voice, setVoice] = useState("Cherry");
  const [languageType, setLanguageType] = useState("");
  const [instructions, setInstructions] = useState("");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* 音色列表：/api/voices?provider=qianwen（含官方试听 URL 与模型支持矩阵） */
  const voicesQuery = useQuery({
    queryKey: ["voices", "qianwen"],
    queryFn: () => fetchJSON<{ voices: QianwenVoice[] }>("/api/voices?provider=qianwen"),
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
    mutationFn: () => {
      // 任务参数空值键不传：language_type 留空即不指定；instructions 仅 instruct 模型携带
      const params: Record<string, unknown> = { text: text.trim(), model, voice };
      if (languageType.trim()) params.language_type = languageType.trim();
      if (model === "qwen3-tts-instruct-flash" && instructions.trim()) {
        params.instructions = instructions.trim();
      }
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "qianwen", tool: "tts", params }),
      });
    },
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

  // 与后端一致：TrimSpace 后按 rune 计数（qianwen tts summary.char_count 同口径）
  const charCount = Array.from(text.trim()).length;
  const isInstruct = model === "qwen3-tts-instruct-flash";
  const canSubmit = text.trim() !== "";

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
              <Field label="文本内容" hint="支持中英文混排，数字与标点按原文合成。">
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
            aside={<span className="micro">qianwen · tts</span>}
          />
          <CardBody className="space-y-4">
            <Field label="模型" hint={isInstruct ? "instruct 版支持自然语言风格指令" : "默认模型，性价比高"}>
              {({ id, ...rest }) => (
                <Select id={id} value={model} onChange={(e) => setModel(e.target.value as QianwenModel)} {...rest}>
                  <option value="qwen3-tts-flash">qwen3-tts-flash</option>
                  <option value="qwen3-tts-instruct-flash">qwen3-tts-instruct-flash（支持风格指令）</option>
                </Select>
              )}
            </Field>

            <Field label="音色" hint="点击喇叭可试听官方样本">
              {() => (
                <QianwenVoicePicker
                  voices={voiceList}
                  loading={voicesQuery.isLoading}
                  value={voice}
                  model={model}
                  onChange={setVoice}
                />
              )}
            </Field>

            <Field label="语言" aside="可选" hint="主要发音语种，方言音色可留空">
              {({ id, ...rest }) => (
                <Input
                  id={id}
                  value={languageType}
                  onChange={(e) => setLanguageType(e.target.value)}
                  placeholder="如 Chinese / English；留空不指定"
                  {...rest}
                />
              )}
            </Field>

            {isInstruct && (
              <Field label="风格指令" aside="可选" hint="仅 instruct 模型生效">
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={instructions}
                    onChange={(e) => setInstructions(e.target.value)}
                    rows={3}
                    placeholder="用自然语言描述语速、情感与风格，如「低沉缓慢，带叹气感」"
                    {...rest}
                  />
                )}
              </Field>
            )}

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
              {!canSubmit && <p className="mt-2 text-[11px] text-muted">请先输入要合成的文本</p>}
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
            description="输入文本、选择模型与音色后点击「开始合成」，音频会出现在这里，可试听与下载。"
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
