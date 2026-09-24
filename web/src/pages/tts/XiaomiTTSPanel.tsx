import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, AudioLines, Play, RefreshCw, SlidersHorizontal } from "lucide-react";
import { fetchJSON } from "../../lib/api";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  Select,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

/** 合成模型：voicedesign 由音色描述定制音色（instructions 为必填音色描述，无预置音色） */
type MimoModel = "mimo-v2.5-tts" | "mimo-v2.5-tts-voicedesign";
const DEFAULT_MODEL: MimoModel = "mimo-v2.5-tts";

/** 预置音色（与后端 internal/provider/xiaomi/voices.go 同词表；官方未提供试听样本）。 */
const PRESET_VOICES: { id: string; desc: string }[] = [
  { id: "mimo_default", desc: "默认音色（中文集群为冰糖）" },
  { id: "冰糖", desc: "中文女声" },
  { id: "茉莉", desc: "中文女声" },
  { id: "苏打", desc: "中文男声" },
  { id: "白桦", desc: "中文男声" },
  { id: "Mia", desc: "英文女声" },
  { id: "Chloe", desc: "英文女声" },
  { id: "Milo", desc: "英文男声" },
  { id: "Dean", desc: "英文男声" },
];

/** 小米 MiMo 非流式语音合成面板：OpenAI 兼容协议，预置音色 / 文本描述定制音色双模型。 */
export default function XiaomiTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState<MimoModel>(DEFAULT_MODEL);
  const [voice, setVoice] = useState("mimo_default");
  const [instructions, setInstructions] = useState("");
  const [format, setFormat] = useState("wav");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  const isVoiceDesign = model === "mimo-v2.5-tts-voicedesign";

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
      // 任务参数空值键不传：instructions 留空即无风格指令（voicedesign 缺描述由后端拦截）；
      // voicedesign 无预置音色，voice 参数不随请求携带
      const params: Record<string, unknown> = { text: text.trim(), model, format };
      if (isVoiceDesign) {
        params.instructions = instructions.trim();
      } else {
        params.voice = voice;
        if (instructions.trim()) params.instructions = instructions.trim();
      }
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "xiaomi", tool: "tts", params }),
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

  // 与后端一致：TrimSpace 后按 rune 计数（xiaomi tts summary.char_count 同口径）
  const charCount = Array.from(text.trim()).length;
  const canSubmit = text.trim() !== "" && (!isVoiceDesign || instructions.trim() !== "");

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
            aside={<span className="font-mono text-[11px] tabular-nums text-muted">{charCount} 字</span>}
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
            aside={<span className="micro">xiaomi · tts</span>}
          />
          <CardBody className="space-y-4">
            <Field label="模型" hint={isVoiceDesign ? "音色由文本描述定制，无预置音色" : "默认模型，预置音色合成"}>
              {({ id, ...rest }) => (
                <Select id={id} value={model} onChange={(e) => setModel(e.target.value as MimoModel)} {...rest}>
                  <option value="mimo-v2.5-tts">mimo-v2.5-tts（预置音色）</option>
                  <option value="mimo-v2.5-tts-voicedesign">mimo-v2.5-tts-voicedesign（描述定制音色）</option>
                </Select>
              )}
            </Field>

            {!isVoiceDesign && (
              <Field label="音色" hint="9 官方预置音色，支持中英文">
                {({ id, ...rest }) => (
                  <Select id={id} value={voice} onChange={(e) => setVoice(e.target.value)} {...rest}>
                    {PRESET_VOICES.map((v) => (
                      <option key={v.id} value={v.id}>
                        {v.id} · {v.desc}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            )}

            <Field
              label={isVoiceDesign ? "音色描述" : "风格指令"}
              aside={isVoiceDesign ? "必填" : "可选"}
              hint={
                isVoiceDesign
                  ? "用自然语言描述想要的音色，如「低沉的青年男声」"
                  : "用自然语言描述语速、情感与风格"
              }
            >
              {({ id, ...rest }) => (
                <Textarea
                  id={id}
                  value={instructions}
                  onChange={(e) => setInstructions(e.target.value)}
                  rows={3}
                  placeholder={
                    isVoiceDesign
                      ? "如「沉稳的中年男声，吐字清晰，略带沙哑」"
                      : "如「低沉缓慢，带叹气感」；留空不指定"
                  }
                  {...rest}
                />
              )}
            </Field>

            <Field label="音频格式" hint="wav 无损 / mp3 通用">
              {({ id, ...rest }) => (
                <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
                  <option value="wav">wav</option>
                  <option value="mp3">mp3</option>
                </Select>
              )}
            </Field>

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
                  {isVoiceDesign && !text.trim() ? "请先输入要合成的文本" : "请先完成必填项"}
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
