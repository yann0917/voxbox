import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, AudioLines, Play, RefreshCw, SlidersHorizontal } from "lucide-react";
import { fetchJSON } from "../../lib/api";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import ZhipuVoicePicker, { type ZhipuVoice } from "../../components/ZhipuVoicePicker";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  Input,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

/** 智谱 glm-tts 非流式合成：单请求整段返回 wav；input ≤1024 字符（与后端同口径）。 */
const MAX_CHARS = 1024;

/** 智谱语音合成面板：官方/复刻音色（运行时列表，含试听），语速/音量可选。 */
export default function ZhipuTTSPanel() {
  const [text, setText] = useState("");
  const [voice, setVoice] = useState("tongtong");
  const [speed, setSpeed] = useState("");
  const [volume, setVolume] = useState("");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* 音色列表：/api/voices?provider=zhipu（官方 + 复刻，运行时拉取，未配置凭证回落官方表） */
  const voicesQuery = useQuery({
    queryKey: ["voices", "zhipu"],
    queryFn: () => fetchJSON<{ voices: ZhipuVoice[] }>("/api/voices?provider=zhipu"),
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
      // 任务参数空值键不传：speed/volume 留空即上游默认 1.0
      const params: Record<string, unknown> = { text: text.trim(), voice };
      if (parseFloat(speed) > 0) params.speed = parseFloat(speed);
      if (parseFloat(volume) > 0) params.volume = parseFloat(volume);
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "zhipu", tool: "tts", params }),
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

  // 与后端一致：TrimSpace 后按 rune 计数（zhipu tts summary.char_count 同口径）
  const charCount = Array.from(text.trim()).length;
  const canSubmit = text.trim() !== "";
  const speedNum = parseFloat(speed);
  const volumeNum = parseFloat(volume);
  const speedInvalid = speed.trim() !== "" && (!Number.isFinite(speedNum) || speedNum < 0.5 || speedNum > 2);
  const volumeInvalid = volume.trim() !== "" && (!Number.isFinite(volumeNum) || volumeNum <= 0 || volumeNum > 10);
  const hasInvalid = speedInvalid || volumeInvalid;

  const artifacts = detail?.artifacts ?? [];
  const audioArtifacts = artifacts.filter((a) => a.kind === "audio");
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
              <span className={`font-mono text-[11px] tabular-nums ${charCount > MAX_CHARS ? "text-danger" : "text-muted"}`}>
                {charCount} / {MAX_CHARS} 字
              </span>
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
            aside={<span className="micro">zhipu · tts</span>}
          />
          <CardBody className="space-y-4">
            <Field
              label="音色"
              hint="官方音色与复刻音色（复刻为 PRIVATE 标记）；点击喇叭可试听"
              aside={
                <button
                  type="button"
                  onClick={() => void voicesQuery.refetch()}
                  className="text-[11px] text-muted transition-colors duration-150 hover:text-accent"
                >
                  刷新
                </button>
              }
            >
              {() => (
                <ZhipuVoicePicker
                  voices={voiceList}
                  loading={voicesQuery.isLoading}
                  value={voice}
                  onChange={setVoice}
                />
              )}
            </Field>

            <div className="grid grid-cols-2 gap-2">
              <Field label="语速" aside="可选" hint="0.5-2.0，默认 1.0">
                {({ id, ...rest }) => (
                  <Input
                    id={id}
                    type="number"
                    step={0.1}
                    min={0.5}
                    max={2}
                    value={speed}
                    onChange={(e) => setSpeed(e.target.value)}
                    placeholder="1.0"
                    {...rest}
                  />
                )}
              </Field>
              <Field label="音量" aside="可选" hint="(0, 10]，默认 1.0">
                {({ id, ...rest }) => (
                  <Input
                    id={id}
                    type="number"
                    step={0.5}
                    min={0.1}
                    max={10}
                    value={volume}
                    onChange={(e) => setVolume(e.target.value)}
                    placeholder="1.0"
                    {...rest}
                  />
                )}
              </Field>
            </div>
            {hasInvalid && (
              <p className="flex items-start gap-1.5 text-[11px] text-danger">
                <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                {speedInvalid ? "语速须在 0.5-2.0 之间" : "音量须在 (0, 10] 之间"}
              </p>
            )}

            <div className="border-t border-line pt-3">
              <Button
                variant="primary"
                className="w-full"
                icon={<Play size={15} strokeWidth={1.75} />}
                loading={submit.isPending}
                disabled={!canSubmit || hasInvalid}
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
