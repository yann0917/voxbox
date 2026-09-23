import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Play,
  Radio,
  RefreshCw,
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

/** 流式合成支持四种格式（比长文本多 wav；流式场景官方推荐 pcm） */
const FORMATS = ["mp3", "pcm", "ogg_opus", "wav"];
const SAMPLE_RATES = [8000, 16000, 22050, 24000, 32000, 44100, 48000];
/** 默认音色：2.0 音色（官方文档示例同款） */
const DEFAULT_VOICE = "zh_female_vv_uranus_bigtts";

interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

export default function TTSStreamPanel() {
  const [text, setText] = useState("");
  // 生效音色 ID：由 VoicePicker（仅 2.0 代际 + 自定义/复刻输入）上报
  const [voice, setVoice] = useState("");
  const [format, setFormat] = useState("mp3");
  const [sampleRate, setSampleRate] = useState(24000);
  const [speechRate, setSpeechRate] = useState(0);
  const [loudnessRate, setLoudnessRate] = useState(0);
  const [pitch, setPitch] = useState(0);
  const [subtitle, setSubtitle] = useState(false);
  const [resource, setResource] = useState("seed-tts-2.0");
  const [model, setModel] = useState("");
  const [explicitLanguage, setExplicitLanguage] = useState("");
  const [explicitDialect, setExplicitDialect] = useState("");
  const [bitRate, setBitRate] = useState("");
  const [silenceDuration, setSilenceDuration] = useState(0);
  const [contextText, setContextText] = useState("");
  const [toneFidelity, setToneFidelity] = useState(false);
  const [aigcWatermark, setAigcWatermark] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [chunks, setChunks] = useState(0);
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

  /* WS 事件驱动进度；detail.chunks 为已收音频分片数（流式真实进度） */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      if (ev.detail?.chunks != null) setChunks(ev.detail.chunks);
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
          tool: "tts_stream",
          params: {
            text,
            voice,
            format,
            sample_rate: format === "ogg_opus" ? 48000 : sampleRate,
            speech_rate: speechRate,
            loudness_rate: loudnessRate,
            pitch,
            subtitle,
            resource,
            model,
            explicit_language: explicitLanguage,
            explicit_dialect: explicitDialect,
            bit_rate: bitRate,
            silence_duration: silenceDuration,
            context_text: contextText.trim(),
            tone_fidelity: toneFidelity,
            aigc_watermark: aigcWatermark,
          },
        }),
      }),
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setChunks(0);
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

  const charCount = Array.from(text.trim()).length;
  const canSubmit = text.trim() !== "" && voice.trim() !== "";
  const noBitRate = format === "wav" || format === "pcm";

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
            icon={<Radio size={15} strokeWidth={1.75} />}
            aside={<span className="font-mono text-[11px] tabular-nums text-muted">{charCount} 字</span>}
          />
          <CardBody className="space-y-3">
            <div ref={textWrapRef}>
              <Field label="文本内容" hint="一次性输入、流式返回：适合短中篇的低延迟合成；10 万字长文请用「长文本合成」。">
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={12}
                    placeholder="输入要合成的文本…"
                    className="min-h-[260px] max-h-[52vh]"
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
            aside={<span className="micro">volcengine · tts_stream</span>}
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

            <Field label="字级时间戳" hint="按句末标点聚合并产出 SRT 字幕（仅中英语种）">
              {({ id }) => (
                <label htmlFor={id} className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                  <input
                    id={id}
                    type="checkbox"
                    checked={subtitle}
                    onChange={(e) => setSubtitle(e.target.checked)}
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
                    <Field label="比特率" hint={noBitRate ? "wav/pcm 不支持" : undefined}>
                      {({ id, ...rest }) => (
                        <Select
                          id={id}
                          value={bitRate}
                          onChange={(e) => setBitRate(e.target.value)}
                          disabled={noBitRate}
                          {...rest}
                        >
                          <option value="">默认</option>
                          <option value="64000">64000</option>
                          <option value="160000">160000</option>
                        </Select>
                      )}
                    </Field>
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="朗读语种">
                      {({ id, ...rest }) => (
                        <Select id={id} value={explicitLanguage} onChange={(e) => setExplicitLanguage(e.target.value)} {...rest}>
                          <option value="">不指定</option>
                          <option value="zh-cn">中文（中英混读）</option>
                          <option value="en">英语</option>
                          <option value="ja">日语</option>
                          <option value="ko">韩语</option>
                          <option value="es-mx">墨西哥语</option>
                          <option value="pt-br">巴西葡语</option>
                          <option value="pt">葡萄牙语</option>
                          <option value="id">印尼语</option>
                          <option value="it">意大利语</option>
                          <option value="de">德语</option>
                          <option value="fr">法语</option>
                          <option value="th">泰语</option>
                          <option value="vi">越南语</option>
                          <option value="ru">俄语</option>
                          <option value="fil">菲律宾语</option>
                          <option value="ms">马来语</option>
                          <option value="ar">阿拉伯语</option>
                          <option value="pl">波兰语</option>
                          <option value="tr">土耳其语</option>
                          <option value="sv">瑞典语</option>
                        </Select>
                      )}
                    </Field>
                    <Field label="方言" hint="需支持方言的音色">
                      {({ id, ...rest }) => (
                        <Select id={id} value={explicitDialect} onChange={(e) => setExplicitDialect(e.target.value)} {...rest}>
                          <option value="">不指定</option>
                          <option value="beijing">北京话</option>
                          <option value="dongbei">东北话</option>
                          <option value="henan">河南话</option>
                          <option value="shaanxi">陕西话</option>
                          <option value="shanghai">上海话</option>
                          <option value="sichuan">四川话</option>
                          <option value="tianjin">天津话</option>
                          <option value="yue">粤语</option>
                        </Select>
                      )}
                    </Field>
                  </div>
                  {resource === "seed-icl-2.0" && (
                    <Field label="复刻模型版本" hint="仅复刻音色需要指定；指定后不支持语音指令">
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
                  <Field label="语音指令" hint="自然语言描述语气风格，仅 2.0 音色支持，不参与计费">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        value={contextText}
                        onChange={(e) => setContextText(e.target.value)}
                        placeholder="如：你可以用特别痛心的语气说话吗"
                        {...rest}
                      />
                    )}
                  </Field>
                  <Field label="末尾静音 (ms)" hint="0-30000，默认不追加">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        type="number"
                        min={0}
                        max={30000}
                        step={100}
                        value={String(silenceDuration)}
                        onChange={(e) => setSilenceDuration(Number(e.target.value || 0))}
                        {...rest}
                      />
                    )}
                  </Field>
                  {resource === "seed-icl-2.0" && (
                    <Field label="还原模式" hint="尽量复刻训练音频的音色与说话风格（不支持跨语种）">
                      {({ id }) => (
                        <label htmlFor={id} className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                          <input
                            id={id}
                            type="checkbox"
                            checked={toneFidelity}
                            onChange={(e) => setToneFidelity(e.target.checked)}
                            className="size-4 cursor-pointer accent-accent"
                          />
                          tone_fidelity
                        </label>
                      )}
                    </Field>
                  )}
                  <Field label="AIGC 标识">
                    {({ id }) => (
                      <label htmlFor={id} className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
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
          icon={<Radio size={15} strokeWidth={1.75} />}
          aside={run ? <StatusBadge status={run.status} /> : undefined}
        />
        {!run ? (
          <EmptyState
            icon={<Radio size={18} strokeWidth={1.75} />}
            title="还没有合成结果"
            description="输入文本、选择音色后点击「开始合成」，音频会流式生成并出现在这里，可试听与下载。"
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
          <CardBody className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <StatusBadge status={run.status} />
              <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
            </div>
            <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
            <p className="text-xs text-muted">{run.note || "处理中"}</p>
            {chunks > 0 && (
              <p className="font-mono text-[11px] tabular-nums text-muted">已收 {chunks} 个音频分片</p>
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
              {summary?.billed_chars ? ` · 计费 ${summary.billed_chars} 字` : ""}
              {summary?.chunks ? ` · ${summary.chunks} 分片` : ""}
              {task && task.cost_ms > 0 ? ` · ${(task.cost_ms / 1000).toFixed(1)}s` : ""}
            </p>
          </CardBody>
        ) : (
          <EmptyState
            icon={<Radio size={18} strokeWidth={1.75} />}
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
