import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  Input,
  Select,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

/** 音色特效（voice_modify.sound_effects，与后端同词表；指南「音色特效」）。 */
const SOUND_EFFECTS = [
  { value: "", label: "不启用" },
  { value: "spacious_echo", label: "空旷回音" },
  { value: "auditorium_echo", label: "礼堂广播" },
  { value: "lofi_telephone", label: "电话失真" },
  { value: "robotic", label: "电音" },
];

/** 语气词标签（官方 speech-2.8 系列文本标记，与文档同词表 19 个）。 */
const INTERJECTIONS: { tag: string; label: string }[] = [
  { tag: "laughs", label: "笑声" }, { tag: "chuckle", label: "轻笑" },
  { tag: "coughs", label: "咳嗽" }, { tag: "clear-throat", label: "清嗓子" },
  { tag: "groans", label: "呻吟" }, { tag: "breath", label: "正常换气" },
  { tag: "pant", label: "喘气" }, { tag: "inhale", label: "吸气" },
  { tag: "exhale", label: "呼气" }, { tag: "gasps", label: "倒吸气" },
  { tag: "sniffs", label: "吸鼻子" }, { tag: "sighs", label: "叹气" },
  { tag: "snorts", label: "喷鼻息" }, { tag: "burps", label: "打嗝" },
  { tag: "lip-smacking", label: "咂嘴" }, { tag: "humming", label: "哼唱" },
  { tag: "hissing", label: "嘶嘶声" }, { tag: "emm", label: "嗯" },
  { tag: "sneezes", label: "喷嚏" },
];

/** 停顿标记 <#x#> 预检（与后端 validatePauseMarkers 同规则）：数值/精度/连续/首尾。
 *  返回错误文案，合法返回空串——即时反馈，省一次注定失败的付费请求。 */
function pauseMarkerError(text: string): string {
  const trimmed = text.trim();
  if (!trimmed.includes("<#")) return "";
  if (/<#[^#\n]*#>\s*<#[^#\n]*#>/.test(trimmed)) {
    return "停顿标记不可连续使用（两个 <#x#> 之间需要有可发音的文本）";
  }
  const matches = [...trimmed.matchAll(/<#([^#\n]*)#>/g)];
  if (matches.length > 0) {
    const first = matches[0].index ?? 0;
    const lastEnd = (matches[matches.length - 1].index ?? 0) + matches[matches.length - 1][0].length;
    if (first === 0 || lastEnd === trimmed.length) {
      return "停顿标记不能位于文本开头或结尾（需夹在两段可发音文本之间）";
    }
  }
  for (const m of matches) {
    const v = m[1];
    const f = parseFloat(v);
    if (Number.isNaN(f) || f < 0.01 || f > 99.99 || (v.includes(".") && v.split(".")[1].length > 2)) {
      return `停顿标记 <#${v}#> 数值需在 0.01-99.99 之间（最多两位小数）`;
    }
  }
  return "";
}

/** MiniMax 音色（/api/voices?provider=minimax）：有凭证拉运行时接口（系统+复刻+文生），
 *  未配置回落 327 个系统音色静态表。两形态同构：label 为显示名，lang 仅静态表携带。 */
interface MinimaxVoice {
  id: string;
  name: string;
  lang?: string;
  label: string;
}

/** 合成模型（与后端 tts_tool.go 同词表）：speech-2.8 当前计费代际。 */
const TTS_MODELS = [
  { value: "speech-2.8-hd", label: "speech-2.8-hd（高清）" },
  { value: "speech-2.8-turbo", label: "speech-2.8-turbo（提速降本）" },
];

/** 情绪枚举（与后端同词表）：空 = 模型按文本自动匹配。 */
const EMOTIONS = [
  { value: "", label: "自动（按文本匹配）" },
  { value: "happy", label: "高兴" }, { value: "sad", label: "悲伤" },
  { value: "angry", label: "愤怒" }, { value: "fearful", label: "害怕" },
  { value: "disgusted", label: "厌恶" }, { value: "surprised", label: "惊讶" },
  { value: "calm", label: "中性" }, { value: "fluent", label: "生动" },
  { value: "whisper", label: "低语" },
];

/** 语种增强（与后端同词表）：粤语音色需 Chinese,Yue 才能出效果。 */
const LANGUAGE_BOOSTS = [
  { value: "", label: "不启用" },
  { value: "auto", label: "auto（自动判断语种）" },
  { value: "Chinese", label: "Chinese（中文增强）" },
  { value: "Chinese,Yue", label: "Chinese,Yue（粤语增强）" },
  { value: "English", label: "English" },
  { value: "Japanese", label: "Japanese" },
  { value: "Korean", label: "Korean" },
];

/** MiniMax 语音合成面板：speech-2.8 同步合成（长文本服务端自动分段拼接），响应为 wav。 */
export default function MinimaxTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState(TTS_MODELS[0].value);
  const [voice, setVoice] = useState("male-qn-qingse");
  const [speed, setSpeed] = useState("");
  const [volume, setVolume] = useState("");
  const [pitch, setPitch] = useState("");
  const [emotion, setEmotion] = useState("");
  const [languageBoost, setLanguageBoost] = useState("");
  const [soundEffects, setSoundEffects] = useState("");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const [interjectionOpen, setInterjectionOpen] = useState(false);
  const interjectionRef = useRef<HTMLDivElement>(null);

  /* 语气词弹层：点外关闭 */
  useEffect(() => {
    if (!interjectionOpen) return;
    const onDown = (e: MouseEvent) => {
      if (!interjectionRef.current?.contains(e.target as Node)) setInterjectionOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setInterjectionOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [interjectionOpen]);

  /** insertAtCursor 在文本框光标处插入标记并保持焦点（选区被替换时同理）。 */
  const insertAtCursor = (snippet: string) => {
    const ta = textWrapRef.current?.querySelector("textarea");
    if (!ta) {
      setText((t) => t + snippet);
      return;
    }
    const start = ta.selectionStart ?? ta.value.length;
    const end = ta.selectionEnd ?? start;
    setText(ta.value.slice(0, start) + snippet + ta.value.slice(end));
    requestAnimationFrame(() => {
      ta.focus();
      const pos = start + snippet.length;
      ta.setSelectionRange(pos, pos);
    });
  };
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* 音色列表：/api/voices?provider=minimax（运行时接口优先，静态表兜底）——
     弹框选择器内浏览/收藏/选用，字段触发器只回显当前音色显示名 */
  const voicesQuery = useQuery({
    queryKey: ["voices", "minimax"],
    queryFn: () => fetchJSON<{ voices: MinimaxVoice[] }>("/api/voices?provider=minimax"),
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
      // 参数空值键不传：speed/volume/pitch 留空即上游默认，emotion/语种增强留空即自动
      const params: Record<string, unknown> = { text: text.trim(), model, voice };
      if (parseFloat(speed) > 0) params.speed = parseFloat(speed);
      if (parseFloat(volume) > 0) params.volume = parseFloat(volume);
      if (pitch.trim() !== "" && Number.isFinite(parseInt(pitch, 10))) params.pitch = parseInt(pitch, 10);
      if (emotion) params.emotion = emotion;
      if (languageBoost) params.language_boost = languageBoost;
      if (soundEffects) params.sound_effects = soundEffects;
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "minimax", tool: "tts", params }),
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

  // 与后端同口径：TrimSpace 后按 rune 计数（summary.char_count）
  const charCount = Array.from(text.trim()).length;
  const speedNum = parseFloat(speed);
  const volumeNum = parseFloat(volume);
  const pitchNum = parseInt(pitch, 10);
  const speedInvalid = speed.trim() !== "" && (!Number.isFinite(speedNum) || speedNum < 0.5 || speedNum > 2);
  const volumeInvalid = volume.trim() !== "" && (!Number.isFinite(volumeNum) || volumeNum <= 0 || volumeNum > 10);
  const pitchInvalid = pitch.trim() !== "" && (!Number.isFinite(pitchNum) || pitchNum < -12 || pitchNum > 12);
  const hasInvalid = speedInvalid || volumeInvalid || pitchInvalid;
  const markerError = pauseMarkerError(text.trim());
  const canSubmit = text.trim() !== "" && !hasInvalid && !markerError;

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
              <div className="flex items-center gap-2">
                <AIWrite value={text} onChange={setText} />
                <span className="font-mono text-[11px] tabular-nums text-muted">{charCount} 字</span>
              </div>
            }
          />
          <CardBody className="space-y-3">
            {/* 包裹层仅用于「去输入文本」聚焦与光标插入：Textarea 组件不透传 ref */}
            <div ref={textWrapRef}>
              <Field hint="长文本由服务端自动分段合成后拼接（wav）。">
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
            {/* 文本标记辅助：官方标记语法（停顿/语气词）一键插入，行内发音与换行分段见提示 */}
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => insertAtCursor("<#1#>")}
                className="rounded-lg border border-line px-2.5 py-1 text-xs text-fg-2 transition-colors hover:border-accent hover:text-accent"
              >
                插入停顿 &lt;#1#&gt;
              </button>
              <div className="relative" ref={interjectionRef}>
                <button
                  type="button"
                  onClick={() => setInterjectionOpen((o) => !o)}
                  className="rounded-lg border border-line px-2.5 py-1 text-xs text-fg-2 transition-colors hover:border-accent hover:text-accent"
                >
                  语气词（19 种）
                </button>
                {interjectionOpen && (
                  <div className="absolute left-0 top-full z-10 mt-1.5 w-64 rounded-xl border border-line bg-panel p-2 shadow-lg">
                    <div className="grid grid-cols-3 gap-1">
                      {INTERJECTIONS.map((it) => (
                        <button
                          key={it.tag}
                          type="button"
                          title={`插入 (${it.tag})`}
                          onClick={() => {
                            insertAtCursor(`(${it.tag})`);
                            setInterjectionOpen(false);
                          }}
                          className="rounded-lg px-2 py-1.5 text-left text-xs text-fg-2 transition-colors hover:bg-raise-2 hover:text-fg"
                        >
                          {it.label}
                          <span className="ml-1 font-mono text-[10px] text-muted">{it.tag}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
              <span className="text-[11px] leading-relaxed text-muted">
                换行分段；停顿 &lt;#秒#&gt;；行内发音 (he2)/(lɪv)；语气词 (laughs) 等
              </span>
            </div>
          </CardBody>
        </Card>

        {/* 右：参数面板 */}
        <Card className="lg:sticky lg:top-4 lg:self-start">
          <CardHeader
            title="合成参数"
            icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
            aside={<span className="micro">minimax · tts</span>}
          />
          <CardBody className="space-y-4">
            <Field label="模型">
              {() => (
                <Select id="minimax-model" value={model} onChange={(e) => setModel(e.target.value)}>
                  {TTS_MODELS.map((m) => (
                    <option key={m.value} value={m.value}>
                      {m.label}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <VoicePickerField
              provider="minimax"
              value={voice}
              onChange={setVoice}
              voices={voiceList}
              defaultVoiceId="male-qn-qingse"
              hint="点击打开音色库：327 系统音色（24 语种），配置凭证后含复刻音色"
            />
            <Field label="音色特效">
              {() => (
                <Select
                  id="minimax-sound-effects"
                  value={soundEffects}
                  onChange={(e) => setSoundEffects(e.target.value)}
                >
                  {SOUND_EFFECTS.map((e) => (
                    <option key={e.value} value={e.value}>
                      {e.label}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="语速">
                {() => (
                  <Input
                    id="minimax-speed"
                    type="number"
                    step="0.1"
                    min="0.5"
                    max="2"
                    placeholder="1.0"
                    value={speed}
                    onChange={(e) => setSpeed(e.target.value)}
                  />
                )}
              </Field>
              <Field label="音量">
                {() => (
                  <Input
                    id="minimax-volume"
                    type="number"
                    step="0.5"
                    min="0.1"
                    max="10"
                    placeholder="1.0"
                    value={volume}
                    onChange={(e) => setVolume(e.target.value)}
                  />
                )}
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="音调">
                {() => (
                  <Input
                    id="minimax-pitch"
                    type="number"
                    step="1"
                    min="-12"
                    max="12"
                    placeholder="0"
                    value={pitch}
                    onChange={(e) => setPitch(e.target.value)}
                  />
                )}
              </Field>
              <Field label="情绪">
                {() => (
                  <Select id="minimax-emotion" value={emotion} onChange={(e) => setEmotion(e.target.value)}>
                    {EMOTIONS.map((e) => (
                      <option key={e.value} value={e.value}>
                        {e.label}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            </div>
            <Field label="语种增强" hint="合成粤语请选 Chinese,Yue">
              {() => (
                <Select
                  id="minimax-langboost"
                  value={languageBoost}
                  onChange={(e) => setLanguageBoost(e.target.value)}
                >
                  {LANGUAGE_BOOSTS.map((l) => (
                    <option key={l.value} value={l.value}>
                      {l.label}
                    </option>
                  ))}
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
              {!canSubmit && text.trim() !== "" && hasInvalid && (
                <p className="mt-2 text-[11px] text-muted">参数超出范围：语速 0.5-2、音量 0.1-10、音调 -12 到 12</p>
              )}
              {!canSubmit && markerError && (
                <p className="mt-2 text-[11px] text-warn">{markerError}</p>
              )}
              {!canSubmit && text.trim() === "" && (
                <p className="mt-2 text-[11px] text-muted">请先输入要合成的文本</p>
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
