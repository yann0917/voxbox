import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Play,
  RefreshCw,
  ScrollText,
  SlidersHorizontal,
} from "lucide-react";
import { fetchJSON } from "../../lib/api";
import type { TaskDetail, TaskStatus, Voice } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { VoicePicker } from "../../components/VoicePicker";
import { ArtifactRow } from "../../components/ArtifactRow";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  Input,
  MicroLabel,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

/** 长文本合成仅支持三种格式（无 wav） */
const FORMATS = ["mp3", "pcm", "ogg_opus"];
const SAMPLE_RATES = [8000, 16000, 22050, 24000, 32000, 44100, 48000];
/** 默认音色：2.0 音色（官方文档示例同款） */
const DEFAULT_VOICE = "zh_female_vv_uranus_bigtts";
/** 官方上限：单次 10 万字符 */
const MAX_CHARS = 100000;
/** 非法字符（除 \t \n 的 ASCII 控制字符）占比上限，超过服务端拒绝执行 */
const MAX_ILLEGAL_RATIO = 0.1;

/** 与后端 validateLongText 一致的控制字符统计（\r 算非法） */
function illegalControlChars(text: string): number {
  let n = 0;
  for (const ch of text) {
    const code = ch.codePointAt(0) ?? 0;
    if (code < 0x20 && ch !== "\t" && ch !== "\n") n++;
  }
  return n;
}

interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

export default function TTSLongPanel() {
  const [text, setText] = useState("");
  // 生效音色 ID：由 VoicePicker（仅 2.0 代际 + 自定义/复刻输入）上报
  const [voice, setVoice] = useState("");
  const [format, setFormat] = useState("mp3");
  const [sampleRate, setSampleRate] = useState(24000);
  const [speechRate, setSpeechRate] = useState(0);
  const [loudnessRate, setLoudnessRate] = useState(0);
  const [pitch, setPitch] = useState(0);
  const [timestamps, setTimestamps] = useState(false);
  const [resource, setResource] = useState("seed-tts-2.0");
  const [model, setModel] = useState("");
  const [explicitLanguage, setExplicitLanguage] = useState("");
  const [bitRate, setBitRate] = useState("");
  const [aigcWatermark, setAigcWatermark] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [upstreamTaskId, setUpstreamTaskId] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  const voicesQuery = useQuery({
    queryKey: ["voices"],
    queryFn: () => fetchJSON<{ voices: Voice[] }>("/api/voices"),
    retry: 1,
  });
  const voiceList = voicesQuery.data?.voices ?? [];

  /* WS 事件驱动进度；progress detail 里的 task_id 是上游合成任务 ID（可在火山控制台追踪） */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      if (ev.detail?.task_id) setUpstreamTaskId(ev.detail.task_id);
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
          tool: "tts_long",
          params: {
            text,
            voice,
            format,
            sample_rate: format === "ogg_opus" ? 48000 : sampleRate,
            speech_rate: speechRate,
            loudness_rate: loudnessRate,
            pitch,
            timestamps,
            resource,
            model,
            explicit_language: explicitLanguage,
            bit_rate: bitRate,
            aigc_watermark: aigcWatermark,
          },
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

  // 与后端一致：容忍结尾换行，先裁掉再计数
  const trimmed = text.replace(/\n+$/, "");
  const charCount = Array.from(trimmed).length;
  const illegalCount = illegalControlChars(trimmed);
  const overLimit = charCount > MAX_CHARS;
  const illegalRatio = charCount > 0 ? illegalCount / charCount : 0;
  const textError = overLimit
    ? `超出长度限制：${charCount} / ${MAX_CHARS} 字符，请分段提交。`
    : illegalRatio > MAX_ILLEGAL_RATIO
      ? `非法字符占比 ${(illegalRatio * 100).toFixed(0)}%（除制表符与换行外的控制字符共 ${illegalCount} 个），请清理后提交。`
      : undefined;
  const canSubmit = trimmed !== "" && !textError && voice.trim() !== "";

  const artifacts = detail?.artifacts ?? [];
  const task = detail?.task;
  const summary = task?.summary;

  return (
    <>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左：文本编辑区 */}
        <Card className="min-w-0">
          <CardHeader
            title="合成文本"
            icon={<ScrollText size={15} strokeWidth={1.75} />}
            aside={
              <span className={`font-mono text-[11px] tabular-nums ${overLimit ? "text-danger" : "text-muted"}`}>
                {charCount} / {MAX_CHARS} 字
              </span>
            }
          />
          <CardBody className="space-y-3">
            <div ref={textWrapRef}>
              <Field
                label="文本内容"
                hint="异步任务模式：提交后轮询产出，合成耗时与文本量正相关（分钟级）。支持从文件粘贴大段文本。"
                error={textError}
              >
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={16}
                    placeholder="粘贴或输入要合成的长文本…"
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
            aside={<span className="micro">volcengine · tts_long</span>}
          />
          <CardBody className="space-y-4">
            <VoicePicker
              voices={voiceList}
              loading={voicesQuery.isLoading}
              generation="2.0"
              defaultVoiceId={DEFAULT_VOICE}
              onEffectiveVoiceChange={setVoice}
            />

            <div className="grid grid-cols-2 gap-2">
              <Field label="音频格式">
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
              <Field label="采样率" hint={format === "ogg_opus" ? "OGG Opus 仅 48000 Hz" : undefined}>
                {({ id, ...rest }) => (
                  <Select
                    id={id}
                    value={format === "ogg_opus" ? "48000" : String(sampleRate)}
                    onChange={(e) => setSampleRate(Number(e.target.value))}
                    disabled={format === "ogg_opus"}
                    {...rest}
                  >
                    {SAMPLE_RATES.map((r) => (
                      <option key={r} value={String(r)}>
                        {r} Hz
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            </div>

            <Field label="时间戳字幕" hint="开启后按分句时间戳产出 SRT 字幕文件">
              {({ id }) => (
                <label htmlFor={id} className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                  <input
                    id={id}
                    type="checkbox"
                    checked={timestamps}
                    onChange={(e) => setTimestamps(e.target.checked)}
                    className="size-4 cursor-pointer accent-accent"
                  />
                  生成 SRT 字幕
                </label>
              )}
            </Field>

            {/* 高级参数：默认收起 */}
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
                    aside={<span className="font-mono text-[11px] tabular-nums">{speechRate > 0 ? `+${speechRate}` : speechRate}</span>}
                    hint="-50 ~ 100，100 = 2.0 倍速"
                  >
                    {({ id, ...rest }) => (
                      <input
                        id={id}
                        type="range"
                        min={-50}
                        max={100}
                        step={5}
                        value={speechRate}
                        onChange={(e) => setSpeechRate(Number(e.target.value))}
                        className="w-full cursor-pointer accent-accent"
                        {...rest}
                      />
                    )}
                  </Field>
                  <Field
                    label="音量"
                    aside={<span className="font-mono text-[11px] tabular-nums">{loudnessRate > 0 ? `+${loudnessRate}` : loudnessRate}</span>}
                    hint="-50 ~ 100，100 = 2.0 倍音量"
                  >
                    {({ id, ...rest }) => (
                      <input
                        id={id}
                        type="range"
                        min={-50}
                        max={100}
                        step={5}
                        value={loudnessRate}
                        onChange={(e) => setLoudnessRate(Number(e.target.value))}
                        className="w-full cursor-pointer accent-accent"
                        {...rest}
                      />
                    )}
                  </Field>
                  <Field
                    label="音调"
                    aside={<span className="font-mono text-[11px] tabular-nums">{pitch > 0 ? `+${pitch}` : pitch}</span>}
                    hint="-12 ~ 12，越大越明亮"
                  >
                    {({ id, ...rest }) => (
                      <input
                        id={id}
                        type="range"
                        min={-12}
                        max={12}
                        step={1}
                        value={pitch}
                        onChange={(e) => setPitch(Number(e.target.value))}
                        className="w-full cursor-pointer accent-accent"
                        {...rest}
                      />
                    )}
                  </Field>
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="资源" hint="复刻音色选 icl">
                      {({ id, ...rest }) => (
                        <Select id={id} value={resource} onChange={(e) => setResource(e.target.value)} {...rest}>
                          <option value="seed-tts-2.0">seed-tts-2.0</option>
                          <option value="seed-icl-2.0">seed-icl-2.0</option>
                        </Select>
                      )}
                    </Field>
                    <Field label="朗读语种">
                      {({ id, ...rest }) => (
                        <Select id={id} value={explicitLanguage} onChange={(e) => setExplicitLanguage(e.target.value)} {...rest}>
                          <option value="">不指定</option>
                          <option value="zh-cn">中文（中英混读）</option>
                          <option value="en">英语</option>
                          <option value="es-mx">墨西哥语</option>
                          <option value="id">印尼语</option>
                          <option value="pt-br">巴西葡语</option>
                        </Select>
                      )}
                    </Field>
                  </div>
                  {resource === "seed-icl-2.0" && (
                    <Field label="复刻模型版本" hint="仅复刻音色需要指定">
                      {({ id, ...rest }) => (
                        <Input
                          id={id}
                          value={model}
                          onChange={(e) => setModel(e.target.value)}
                          placeholder="复刻音色对应的模型版本"
                          {...rest}
                        />
                      )}
                    </Field>
                  )}
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="比特率">
                      {({ id, ...rest }) => (
                        <Select
                          id={id}
                          value={bitRate}
                          onChange={(e) => setBitRate(e.target.value)}
                          disabled={format === "pcm"}
                          {...rest}
                        >
                          <option value="">默认</option>
                          <option value="64000">64000</option>
                          <option value="160000">160000</option>
                        </Select>
                      )}
                    </Field>
                    <Field label="AIGC 标识">
                      {({ id }) => (
                        <label htmlFor={id} className="flex h-10 cursor-pointer items-center gap-2 text-sm text-fg-2">
                          <input
                            id={id}
                            type="checkbox"
                            checked={aigcWatermark}
                            onChange={(e) => setAigcWatermark(e.target.checked)}
                            className="size-4 cursor-pointer accent-accent"
                          />
                          结尾节奏标识
                        </label>
                      )}
                    </Field>
                  </div>
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
                提交合成任务
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
          icon={<ScrollText size={15} strokeWidth={1.75} />}
          aside={run ? <StatusBadge status={run.status} /> : undefined}
        />
        {!run ? (
          <EmptyState
            icon={<ScrollText size={18} strokeWidth={1.75} />}
            title="还没有合成任务"
            description="输入文本、选择音色后提交任务，音频与字幕会出现在这里，可试听与下载。"
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
                重新提交
              </Button>
              <span className="font-mono text-[11px] text-muted">{taskId?.slice(0, 8)}</span>
            </div>
          </CardBody>
        ) : run.status !== "succeeded" ? (
          <CardBody className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <StatusBadge status={run.status} />
              <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
            </div>
            <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
            <p className="text-xs text-muted">{run.note || "处理中"}</p>
            {upstreamTaskId && (
              <p className="font-mono text-[11px] tabular-nums text-muted" title="上游合成任务 ID">
                任务 {upstreamTaskId}
              </p>
            )}
            <Skeleton className="h-9 w-full" />
          </CardBody>
        ) : artifacts.length > 0 ? (
          <CardBody className="space-y-2">
            {artifacts.map((a) => (
              <ArtifactRow key={a.id} a={a} />
            ))}
            <p className="pt-1 font-mono text-[11px] tabular-nums text-muted">
              {summary?.char_count != null ? `${summary.char_count} 字` : "合成完成"}
              {summary?.sentence_count ? ` · ${summary.sentence_count} 句` : ""}
              {task && task.cost_ms > 0 ? ` · ${(task.cost_ms / 1000).toFixed(1)}s` : ""}
              {upstreamTaskId ? ` · ${upstreamTaskId}` : ""}
            </p>
          </CardBody>
        ) : (
          <EmptyState
            icon={<ScrollText size={18} strokeWidth={1.75} />}
            title="任务已完成，但没有产物"
            description="可调整参数后重新提交，或到历史页查看该任务的产物记录。"
            action={
              <Button
                variant="secondary"
                size="sm"
                icon={<RefreshCw size={13} strokeWidth={1.75} />}
                loading={submit.isPending}
                onClick={() => submit.mutate()}
              >
                重新提交
              </Button>
            }
          />
        )}
      </Card>
    </>
  );
}
