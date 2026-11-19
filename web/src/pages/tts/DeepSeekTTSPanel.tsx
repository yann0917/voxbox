import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, AudioLines, Play, RefreshCw, SlidersHorizontal } from "lucide-react";
import { fetchJSON } from "../../lib/api";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import AIWrite from "./AIWrite";
import VoicePickerField from "../../components/VoicePickerField";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

/** 官方单次上限：10,000 计费字符（汉字按 2 计，其余按 1），与后端 ttsMaxBillingChars 同口径。 */
const MAX_BILLING_CHARS = 10000;

/** 官方双音色（与后端 internal/provider/deepseek/voices.go 同词表；官方未提供试听样本）。 */
const PRESET_VOICES = [
  { id: "stella", label: "stella · 海星（女声，默认）" },
  { id: "tide", label: "tide · 暗潮（男声）" },
];

/** 按官方计费口径估算：非 ASCII 字符按 2 计，其余按 1。 */
function billingChars(text: string): number {
  let n = 0;
  for (const ch of text) n += ch.charCodeAt(0) > 0x7f ? 2 : 1;
  return n;
}

/** DeepSeek 语音合成面板：stella/tide 双官方音色，十语种按文本自动识别（含粤语）。 */
export default function DeepSeekTTSPanel() {
  const [text, setText] = useState("");
  const [voice, setVoice] = useState("stella");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

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
        body: JSON.stringify({ provider: "deepseek", tool: "tts", params: { text: text.trim(), voice } }),
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

  // 输入区显示计费字符数（官方口径），超限前端拦截不发无谓请求
  const billing = billingChars(text.trim());
  const overLimit = billing > MAX_BILLING_CHARS;
  const canSubmit = text.trim() !== "" && !overLimit;

  const artifacts = detail?.artifacts ?? [];
  const audioArtifacts = artifacts.filter((a) => a.kind === "audio");

  return (
    <>
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左：文本编辑区 */}
        <Card className="min-w-0">
          <CardHeader
            title="合成文本"
            icon={<AudioLines size={15} strokeWidth={1.75} />}
            aside={
              <div className="flex items-center gap-2">
                <AIWrite value={text} onChange={setText} />
                <span className="font-mono text-[11px] tabular-nums text-muted">{billing} 计费字符</span>
              </div>
            }
          />
          <CardBody className="space-y-3">
            {/* 包裹层仅用于「去输入文本」聚焦：Textarea 组件不透传 ref */}
            <div ref={textWrapRef}>
              <Field hint="中/粤/英/日/韩等十语种按文本自动识别，无需指定；朗读前自动去除 Markdown 格式。">
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={14}
                    aria-label="文本内容"
                    placeholder="输入要合成的文本…"
                    className="min-h-[320px] max-h-[60vh]"
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
            aside={<span className="micro">deepseek · tts</span>}
          />
          <CardBody className="space-y-4">
            <VoicePickerField
              provider="deepseek"
              value={voice}
              onChange={setVoice}
              voices={PRESET_VOICES}
              defaultVoiceId="stella"
              hint="官方双音色，返回 wav 音频"
            />

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
              {!canSubmit && (
                <p className="mt-2 text-[11px] text-muted">
                  {overLimit
                    ? `超出单次上限 10,000 计费字符（当前 ${billing}，汉字按 2 计），请拆分后分段合成`
                    : "请先输入要合成的文本"}
                </p>
              )}
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
            description="输入文本、选择音色后点击「开始合成」，音频会出现在这里，可试听与下载。"
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
            <p className="pt-1 font-mono text-[11px] tabular-nums text-muted">合成完成 · WAV</p>
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
