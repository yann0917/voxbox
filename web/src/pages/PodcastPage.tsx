import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowUpRight,
  AudioLines,
  Check,
  Copy,
  Download,
  FileJson,
  FileText,
  Globe,
  MessageSquare,
  Play,
  RefreshCw,
  Shuffle,
  SlidersHorizontal,
  Type,
  Users,
} from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import { formatTime } from "../lib/player";
import type { Artifact, TaskDetail, TaskStatus, Voice } from "../lib/types";
import { useTaskEvents } from "../lib/ws";
import { ALL_FILTER, VoiceFilterSelects, matchVoice, type VoiceFilters } from "../components/VoiceFilters";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  Input,
  PageHeader,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Tabs,
  Textarea,
  WavePlayer,
  useToast,
} from "../ui";

/** 后端 podcast tool 支持的输出格式（ogg_opus 为服务端枚举值） */
const FORMATS = ["mp3", "ogg_opus", "pcm", "aac"];

type Mode = "text" | "url" | "script";

const MODES: { value: Mode; label: string; icon: React.ReactNode }[] = [
  { value: "text", label: "文本 / 主题", icon: <Type size={13} strokeWidth={1.75} /> },
  { value: "url", label: "网页 URL", icon: <Globe size={13} strokeWidth={1.75} /> },
  { value: "script", label: "对话稿 JSON", icon: <FileJson size={13} strokeWidth={1.75} /> },
];

const SCRIPT_PLACEHOLDER =
  '{"rounds":[{"speaker":"音色ID","text":"大家好，欢迎收听本期播客…"},{"speaker":"音色ID","text":"主持人好，今天我们聊聊…"}]}';

/** 对话稿示例结构（可复制）：speaker 填音色 ID 或自定义说话人名 */
const SCRIPT_SAMPLE = `{
  "rounds": [
    { "speaker": "zh_female_cancan_mars_bigtts", "text": "大家好，欢迎收听本期播客。" },
    { "speaker": "zh_male_dayixiansheng_v2_saturn_bigtts", "text": "今天我们聊聊身边的 AI 工具。" }
  ]
}`;

const STEPS = [
  { key: 1, label: "内容输入" },
  { key: 2, label: "音色搭配" },
  { key: 3, label: "生成与结果" },
];

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO */
interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

/** 对话流条目：WS 逐轮追加；产物对话稿可回填（带每轮时长） */
interface FlowItem {
  round: number;
  speaker: string;
  text: string;
  durationS?: number;
}

function formatSize(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function fmtDuration(s?: number): string {
  if (s == null) return "—";
  const total = Math.round(s);
  return total >= 60 ? `${Math.floor(total / 60)} 分 ${total % 60} 秒` : `${total} 秒`;
}

/** 音色短名：剥去「语种_性别_」前缀后取第一段；自定义说话人（如「张三」）原样保留 */
function speakerShort(s: string): string {
  const head = s.replace(/^[a-z]+_(?:male|female)_/, "").split("_")[0];
  return head || s;
}

/**
 * 对话稿 JSON 本地校验：与后端 parsePodScript 同判据
 * （JSON 合法 → rounds 为非空数组 → 每轮含非空 speaker/text）。返回空串表示合法。
 */
function validateScript(raw: string): string {
  const s = raw.trim();
  if (!s) return "请输入对话稿 JSON";
  let parsed: unknown;
  try {
    parsed = JSON.parse(s);
  } catch (e) {
    return `JSON 语法错误：${(e as Error).message}`;
  }
  const rounds = (parsed as { rounds?: unknown } | null)?.rounds;
  if (!Array.isArray(rounds)) return "对话稿缺少 rounds 数组（结构：{ rounds: [{ speaker, text }] }）";
  if (rounds.length === 0) return "rounds 为空：至少需要一轮对话";
  for (let i = 0; i < rounds.length; i++) {
    const r = rounds[i] as { speaker?: unknown; text?: unknown } | null;
    const speaker = typeof r?.speaker === "string" ? r.speaker.trim() : "";
    const text = typeof r?.text === "string" ? r.text.trim() : "";
    if (!speaker || !text) return `第 ${i + 1} 轮缺少 speaker 或 text`;
  }
  return "";
}

/** 步骤指示：序号用 mono 数字，已完成打勾，点击跳到对应分区 */
function StepRail({
  current,
  done,
  onJump,
}: {
  current: number;
  done: Record<number, boolean>;
  onJump: (step: number) => void;
}) {
  return (
    <ol className="rise mb-4 flex flex-wrap items-center gap-x-3 gap-y-1.5">
      {STEPS.map((s, i) => {
        const isDone = done[s.key];
        const isCurrent = !isDone && s.key === current;
        return (
          <li key={s.key} className="flex items-center gap-3">
            {i > 0 && <span className="h-px w-4 bg-line-strong" aria-hidden="true" />}
            <button
              type="button"
              onClick={() => onJump(s.key)}
              aria-current={isCurrent ? "step" : undefined}
              className={`flex cursor-pointer items-center gap-1.5 text-xs transition-colors duration-150 ${
                isDone ? "text-fg-2 hover:text-accent" : isCurrent ? "text-accent" : "text-muted hover:text-fg-2"
              }`}
            >
              <span className="font-mono text-[11px] tabular-nums">{`0${s.key}`}</span>
              {s.label}
              {isDone && <Check size={11} strokeWidth={2} />}
            </button>
          </li>
        );
      })}
    </ol>
  );
}

/** 产物行：图标 + 文件名 + 大小 + 下载（对话稿等非音频产物） */
function DownloadRow({ a }: { a: Artifact }) {
  return (
    <a
      href={`${apiBase}/api/artifacts/${a.id}/download`}
      className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3 transition-colors duration-150 hover:border-line-strong"
    >
      <span className="shrink-0 text-muted">
        <FileJson size={16} strokeWidth={1.75} />
      </span>
      <span className="min-w-0 flex-1 truncate text-sm text-fg">{a.filename}</span>
      <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">{formatSize(a.size)}</span>
      <Download size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
    </a>
  );
}

export default function PodcastPage() {
  const [mode, setMode] = useState<Mode>("text");
  const [text, setText] = useState("");
  const [url, setUrl] = useState("");
  const [script, setScript] = useState("");
  const [voiceA, setVoiceA] = useState("");
  const [voiceB, setVoiceB] = useState("");
  // 音色筛选：播客默认聚焦中文音色，减少 500+ 音色的翻找
  const [voiceFilter, setVoiceFilter] = useState<VoiceFilters>({ scene: ALL_FILTER, lang: "中文" });
  const [format, setFormat] = useState("mp3");
  const [headMusic, setHeadMusic] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [flow, setFlow] = useState<FlowItem[]>([]);
  const [submitError, setSubmitError] = useState("");
  const contentRef = useRef<HTMLDivElement>(null);
  const voiceRef = useRef<HTMLDivElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const flowBoxRef = useRef<HTMLUListElement>(null);
  const fetchedDialogRef = useRef<string | null>(null);
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* 音色列表：A 默认取列表第 1 个、B 取第 2 个（speakers 顺序即 A/B）。
     只重试 1 次：本页音色只能来自接口，失败要尽快落到明确错误态而非长时间加载 */
  const voicesQuery = useQuery({
    queryKey: ["voices"],
    queryFn: () => fetchJSON<{ voices: Voice[] }>("/api/voices"),
    retry: 1,
  });
  const voiceList = useMemo(() => voicesQuery.data?.voices ?? [], [voicesQuery.data]);
  // 场景/语种筛选后的音色按主场景分组渲染
  const filteredVoices = useMemo(() => voiceList.filter((v) => matchVoice(v, voiceFilter)), [voiceList, voiceFilter]);
  const voiceGroups = useMemo(() => {
    const byScene = new Map<string, Voice[]>();
    for (const v of filteredVoices) {
      const key = v.scenes[0];
      const list = byScene.get(key) ?? [];
      list.push(v);
      byScene.set(key, list);
    }
    return [...byScene.entries()].sort(([a], [b]) => a.localeCompare(b, "zh"));
  }, [filteredVoices]);
  // 默认人选：筛选结果内优先一女一男（对话感最强），缺失时退前两席
  const fallbackA = filteredVoices.find((v) => v.gender === "女")?.id ?? filteredVoices[0]?.id ?? "";
  const fallbackB =
    filteredVoices.find((v) => v.gender === "男")?.id ?? filteredVoices[1]?.id ?? filteredVoices[0]?.id ?? "";
  const voiceAVal = voiceA || fallbackA;
  const voiceBVal = voiceB || fallbackB;
  const filteredEmpty = voiceList.length > 0 && filteredVoices.length === 0;

  /* 筛选变化后，被筛掉的已选音色回到筛选结果首位（提交值与下拉所见一致） */
  useEffect(() => {
    if (voiceA && !filteredVoices.some((v) => v.id === voiceA)) setVoiceA("");
    if (voiceB && !filteredVoices.some((v) => v.id === voiceB)) setVoiceB("");
  }, [filteredVoices, voiceA, voiceB]);

  /* 快速搭配：筛选结果内优先「一女 + 一男」，否则退回前两个不同音色 */
  const quickPair = useMemo<[string, string] | null>(() => {
    const f = filteredVoices.find((v) => v.gender === "女");
    const m = filteredVoices.find((v) => v.gender === "男" && v.id !== f?.id);
    if (f && m) return [f.id, m.id];
    const uniq = [...new Set(filteredVoices.map((v) => v.id))];
    return uniq.length >= 2 ? [uniq[0], uniq[1]] : null;
  }, [filteredVoices]);

  /* WS 事件驱动当前任务进度与对话流（detail.text 非空即一轮对话）；终态拉详情拿产物与 summary */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setRun({ status: "running", progress: ev.progress ?? 0, note: ev.note ?? "处理中" });
      const d = ev.detail;
      if (d?.text) {
        setFlow((items) => [
          ...items,
          { round: d.rounds_done ?? d.round_id ?? items.length + 1, speaker: d.speaker ?? "", text: d.text ?? "" },
        ]);
      }
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
            toast({ tone: "error", title: "播客生成失败", description: d.task.error || undefined });
          }
          /* 对话稿产物是完整事实来源：WS 断线重连会丢进度事件，用产物回填对话流 */
          const dialog = d.artifacts.find((a) => a.kind === "dialog");
          if (dialog && fetchedDialogRef.current !== dialog.id) {
            fetchedDialogRef.current = dialog.id;
            fetch(`${apiBase}/api/artifacts/${dialog.id}/stream`)
              .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
              .then((j: { rounds?: { round_id?: number; speaker?: string; text?: string; duration_s?: number }[] }) => {
                const rounds = (j.rounds ?? [])
                  .filter((r) => r.text)
                  .map((r, i) => ({
                    round: r.round_id ?? i + 1,
                    speaker: r.speaker ?? "",
                    text: r.text ?? "",
                    durationS: r.duration_s,
                  }));
                // 只在产物比已流式收到的更完整时替换，避免覆盖实时流的先后顺序
                setFlow((prev) => (rounds.length > prev.length ? rounds : prev));
              })
              .catch(() => {
                /* 回填失败不打断结果展示：下载行仍可用 */
              });
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
      const params: Record<string, unknown> = {
        speakers: `${voiceAVal},${voiceBVal}`,
        format,
        head_music: headMusic,
      };
      if (mode === "text") params.input_text = text.trim();
      else if (mode === "url") params.url = url.trim();
      else params.script = script; // 对话稿 JSON 原文直传（本地已校验同判据）
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "volcengine", tool: "podcast", params }),
      });
    },
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setFlow([]);
      setSubmitError("");
      fetchedDialogRef.current = null;
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  /* 对话流自动滚到底：新条目 / 新增轮次时跟随 */
  useEffect(() => {
    const el = flowBoxRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [flow.length]);

  const contentEmpty = mode === "text" ? !text.trim() : mode === "url" ? !url.trim() : !script.trim();
  // 对话稿：边输入边校验（非空才报错，避免刚切模式就红一片）
  const scriptError = mode === "script" && !contentEmpty ? validateScript(script) : "";
  const scriptRounds = useMemo(() => {
    try {
      const parsed = JSON.parse(script || "null") as { rounds?: unknown } | null;
      return Array.isArray(parsed?.rounds) ? parsed.rounds.length : 0;
    } catch {
      return 0;
    }
  }, [script]);
  const urlHint = mode === "url" && url.trim() !== "" && !/^https?:\/\//i.test(url.trim())
    ? "链接需以 http:// 或 https:// 开头，否则抓取正文会失败。"
    : "";
  const voicesUsable = voiceAVal !== "" && voiceBVal !== "" && !filteredEmpty;
  const canSubmit = !contentEmpty && !scriptError && voicesUsable;
  const charCount = Array.from(text).length;

  const artifacts = detail?.artifacts ?? [];
  const audioArtifact = artifacts.find((a) => a.kind === "audio");
  const dialogArtifact = artifacts.find((a) => a.kind === "dialog");
  const summary = detail?.task.summary;
  const audioSec = summary?.duration_s ?? (audioArtifact?.duration_ms ? audioArtifact.duration_ms / 1000 : undefined);
  const succeeded = detail?.task.status === "succeeded";

  const shortA = speakerShort(voiceAVal);
  const shortB = speakerShort(voiceBVal);
  const roleOf = (speaker: string): string | null => {
    if (!speaker) return null;
    if (speaker === voiceAVal || speaker === shortA) return "说话人 A";
    if (speaker === voiceBVal || speaker === shortB) return "说话人 B";
    return null;
  };

  const jump = useCallback((step: number) => {
    const el = step === 1 ? contentRef.current : step === 2 ? voiceRef.current : resultRef.current;
    if (!el) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollIntoView({ behavior: reduced ? "auto" : "smooth", block: "start" });
    el.querySelector<HTMLElement>("textarea, input, select")?.focus();
  }, []);

  const copySample = () => {
    navigator.clipboard
      .writeText(SCRIPT_SAMPLE)
      .then(() => toast({ tone: "ok", title: "示例结构已复制", description: "替换 speaker 与 text 后即可提交。" }))
      .catch(() =>
        toast({ tone: "error", title: "复制失败", description: "浏览器拒绝访问剪贴板，请手动选中示例文本复制。" })
      );
  };

  const applyQuickPair = () => {
    if (!quickPair) return;
    setVoiceA(quickPair[0]);
    setVoiceB(quickPair[1]);
    toast({
      tone: "ok",
      title: "已应用快速搭配",
      description: `说话人 A：${quickPair[0]} · 说话人 B：${quickPair[1]}`,
    });
  };

  const currentStep = !contentEmpty && !scriptError ? (voicesUsable ? 3 : 2) : 1;
  const stepDone = { 1: !contentEmpty && !scriptError, 2: voicesUsable, 3: succeeded };

  return (
    <>
      <PageHeader
        title="播客工坊"
        icon={<AudioLines size={16} strokeWidth={1.75} />}
        description="文本 / 网页 / 对话稿三选一，双音色逐轮生成播客音频"
        actions={
          <Link
            to="/history"
            className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
          >
            历史产物
            <ArrowUpRight size={13} strokeWidth={1.75} />
          </Link>
        }
      />

      <StepRail current={currentStep} done={stepDone} onJump={jump} />

      {/* STEP 01 · 内容输入 */}
      <div ref={contentRef} className="scroll-mt-4">
        <Card>
          <CardHeader
            title="内容输入"
            icon={<FileText size={15} strokeWidth={1.75} />}
            aside={
              <span className="flex items-center gap-2">
                {mode === "text" && (
                  <span className="font-mono text-[11px] tabular-nums text-muted">{charCount} 字</span>
                )}
                {mode === "script" && scriptRounds > 0 && (
                  <span className="font-mono text-[11px] tabular-nums text-muted">{scriptRounds} 轮</span>
                )}
                <span className="font-mono text-[11px] text-muted">STEP 01</span>
              </span>
            }
          />
          <CardBody className="space-y-4">
            <Tabs<Mode>
              items={MODES}
              value={mode}
              onChange={(m) => {
                setMode(m);
                setSubmitError("");
              }}
            />

            {mode === "text" && (
              <Field
                label="播客主题或长文本"
                aside="≤ 12000 字"
                hint="可以是一句话主题（由服务端扩写为对谈稿），也可以是成稿长文本。"
              >
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={8}
                    placeholder="播客主题或长文本，如「聊聊身边的 AI 工具」…"
                    className="min-h-[180px] max-h-[46vh]"
                    {...rest}
                  />
                )}
              </Field>
            )}

            {mode === "url" && (
              <Field
                label="网页链接"
                hint="抓取网页正文后生成对谈稿，需公网可访问。"
                error={urlHint || undefined}
              >
                {({ id, ...rest }) => (
                  <Input
                    id={id}
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                    placeholder="https://example.com/article"
                    {...rest}
                  />
                )}
              </Field>
            )}

            {mode === "script" && (
              <div className="space-y-3">
                <Field
                  label="对话稿 JSON"
                  aside="原文直传"
                  hint="结构：{ rounds: [{ speaker, text }] }；speaker 填音色 ID 或自定义说话人名，两条轮次就会交替播报。"
                  error={scriptError || undefined}
                >
                  {({ id, ...rest }) => (
                    <Textarea
                      id={id}
                      value={script}
                      onChange={(e) => setScript(e.target.value)}
                      rows={8}
                      placeholder={SCRIPT_PLACEHOLDER}
                      className="font-mono"
                      {...rest}
                    />
                  )}
                </Field>
                <div className="space-y-2 rounded-[var(--radius-sm)] border border-line bg-raise-2/40 p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="micro">示例结构</span>
                    <Button size="sm" variant="ghost" icon={<Copy size={13} strokeWidth={1.75} />} onClick={copySample}>
                      复制示例
                    </Button>
                  </div>
                  <pre className="overflow-x-auto font-mono text-[11px] leading-relaxed text-fg-2">{SCRIPT_SAMPLE}</pre>
                </div>
              </div>
            )}
          </CardBody>
        </Card>
      </div>

      {/* STEP 02 · 音色搭配 */}
      <div ref={voiceRef} className="mt-4 scroll-mt-4">
        <Card>
          <CardHeader
            title="音色搭配"
            icon={<Users size={15} strokeWidth={1.75} />}
            aside={
              <span className="flex items-center gap-2">
                <span className="font-mono text-[11px] text-muted">STEP 02</span>
                <Button
                  size="sm"
                  variant="secondary"
                  icon={<Shuffle size={13} strokeWidth={1.75} />}
                  disabled={!quickPair}
                  onClick={applyQuickPair}
                >
                  快速搭配
                </Button>
              </span>
            }
          />
          <CardBody className="space-y-4">
            {voicesQuery.isLoading ? (
              <div className="grid gap-3 sm:grid-cols-2">
                {[0, 1].map((i) => (
                  <Skeleton key={i} className="h-28 w-full" />
                ))}
              </div>
            ) : voicesQuery.isError || voiceList.length === 0 ? (
              <div className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                <AlertTriangle size={15} strokeWidth={1.75} className="shrink-0 text-warn" />
                <span className="min-w-0 flex-1 text-xs text-fg-2">
                  音色列表不可用{voicesQuery.isError ? `（${(voicesQuery.error as Error).message}）` : "（列表为空）"}，
                  无法选择说话人音色。
                </span>
                <Button size="sm" variant="secondary" icon={<RefreshCw size={13} strokeWidth={1.75} />} loading={voicesQuery.isFetching} onClick={() => void voicesQuery.refetch()}>
                  重试
                </Button>
              </div>
            ) : (
              <>
                <div className="space-y-3">
                  <VoiceFilterSelects value={voiceFilter} onChange={setVoiceFilter} voices={voiceList} />
                  {filteredEmpty && (
                    <p className="text-[11px] text-warn">当前筛选无匹配音色，请调整场景/语种。</p>
                  )}
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  {([
                    { role: "A", val: voiceAVal, set: setVoiceA, desc: "speakers[0]" },
                    { role: "B", val: voiceBVal, set: setVoiceB, desc: "speakers[1]" },
                  ] as const).map((s) => (
                    <div key={s.role} className="space-y-3 rounded-[var(--radius-sm)] border border-line bg-raise-2/40 p-3">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="micro">说话人 {s.role}</span>
                        <span className="font-mono text-[11px] text-muted">{s.desc}</span>
                      </div>
                      <Field label={`${s.role} 音色`} hint={`当前：${s.val}`}>
                        {({ id, ...rest }) => (
                          <Select id={id} value={s.val} onChange={(e) => s.set(e.target.value)} {...rest} disabled={filteredEmpty}>
                            {voiceGroups.map(([scene, list]) => (
                              <optgroup key={scene} label={scene}>
                                {list.map((v) => (
                                  <option key={v.id} value={v.id}>
                                    {v.name} · {v.gender} · {v.languages[0]}
                                  </option>
                                ))}
                              </optgroup>
                            ))}
                          </Select>
                        )}
                      </Field>
                    </div>
                  ))}
                </div>
                <p className="text-[11px] text-muted">
                  A / B 顺序即 <span className="font-mono">speakers</span> 参数顺序（A → speakers[0]，B → speakers[1]）；
                  每轮实际用哪个音色由服务端按对话稿分配，可在下方对话流核对。
                </p>
              </>
            )}
          </CardBody>
        </Card>
      </div>

      {/* STEP 03 · 生成与结果 */}
      <div ref={resultRef} className="mt-4 scroll-mt-4">
        <Card>
          <CardHeader
            title="生成与结果"
            icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
            aside={
              <span className="flex items-center gap-2">
                {run && <StatusBadge status={run.status} />}
                <span className="font-mono text-[11px] text-muted">STEP 03</span>
              </span>
            }
          />
          <CardBody className="space-y-4">
            {/* 输出参数 + 提交 */}
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="音频格式" aside={format === "mp3" ? "推荐" : undefined} hint="对话稿逐轮合成后按此格式封装。">
                {({ id, ...rest }) => (
                  <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
                    {FORMATS.map((f) => (
                      <option key={f} value={f}>
                        {f === "ogg_opus" ? "OGG OPUS" : f.toUpperCase()}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <div className="flex flex-col justify-end gap-1.5">
                <span className="micro">开头音乐</span>
                <label className="flex cursor-pointer items-start gap-2.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2">
                  <input
                    type="checkbox"
                    checked={headMusic}
                    onChange={(e) => setHeadMusic(e.target.checked)}
                    className="mt-0.5 size-4 shrink-0 cursor-pointer accent-accent"
                  />
                  <span className="min-w-0 space-y-0.5">
                    <span className="block text-sm text-fg">叠加开头音乐</span>
                    <span className="block text-[11px] text-muted">在成品音频前加一段音乐前奏</span>
                  </span>
                </label>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3 border-t border-line pt-3">
              <Button
                variant="primary"
                icon={<Play size={15} strokeWidth={1.75} />}
                loading={submit.isPending}
                disabled={!canSubmit}
                onClick={() => submit.mutate()}
              >
                生成播客
              </Button>
              {!canSubmit && (
                <span className="text-[11px] text-muted">
                  {contentEmpty
                    ? mode === "text"
                      ? "请先填写播客主题或长文本"
                      : mode === "url"
                        ? "请先填写网页链接"
                        : "请先填写对话稿 JSON"
                    : scriptError || "音色列表不可用，无法选择说话人音色"}
                </span>
              )}
              {submitError && (
                <span className="flex items-start gap-1.5 text-[11px] text-danger">
                  <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                  {submitError}
                </span>
              )}
            </div>

            {/* 进度：运行中显示状态徽标 + 细进度条 + 备注 */}
            {run && !succeeded && run.status !== "failed" && (
              <div className="space-y-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <StatusBadge status={run.status} />
                  <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
                </div>
                <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
                <p className="text-xs text-muted">{run.note || "处理中"}</p>
              </div>
            )}

            {/* 失败：行内错误 + 重试 */}
            {run?.status === "failed" && (
              <div className="space-y-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
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
              </div>
            )}

            {/* 对话流：逐轮追加，新条目自动滚到底 */}
            {flow.length > 0 && (
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5">
                    <MessageSquare size={14} strokeWidth={1.75} className="text-muted" />
                    <span className="micro">对话流</span>
                  </span>
                  <span className="font-mono text-[11px] tabular-nums text-muted">
                    {flow.length} 轮{summary?.rounds != null ? ` / 共 ${summary.rounds} 轮` : ""}
                  </span>
                </div>
                <ul
                  ref={flowBoxRef}
                  aria-live="polite"
                  className="max-h-72 divide-y divide-line overflow-y-auto rounded-[var(--radius-sm)] border border-line"
                >
                  {flow.map((it, i) => {
                    const role = roleOf(it.speaker);
                    return (
                      <li key={`${it.round}-${i}`} className="flex items-baseline gap-3 px-3 py-2">
                        <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">
                          [第 {it.round} 轮]
                        </span>
                        <span className="shrink-0 text-xs text-fg-2" title={it.speaker}>
                          {role ?? speakerShort(it.speaker) ?? "说话人"}
                        </span>
                        <span className="min-w-0 flex-1 break-words text-sm leading-relaxed">{it.text}</span>
                        {it.durationS != null && (
                          <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">
                            {formatTime(it.durationS)}
                          </span>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </div>
            )}

            {/* 成品：音频 + 对话稿下载 + summary 读数 */}
            {succeeded && (
              <div className="space-y-2">
                {audioArtifact ? (
                  <div className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                    <span className="w-14 shrink-0 text-xs text-fg-2">音频</span>
                    <WavePlayer
                      src={`${apiBase}/api/artifacts/${audioArtifact.id}/stream`}
                      title={audioArtifact.filename}
                      sub={audioArtifact.format.toUpperCase()}
                      durationSec={audioSec}
                      className="min-w-0 flex-1"
                    />
                    <a
                      href={`${apiBase}/api/artifacts/${audioArtifact.id}/download`}
                      className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                    >
                      <Download size={13} strokeWidth={1.75} />
                      下载
                    </a>
                  </div>
                ) : (
                  <p className="rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3 text-xs text-muted">
                    任务已完成，但没有音频产物，可到历史页查看该任务的记录。
                  </p>
                )}

                {dialogArtifact && <DownloadRow a={dialogArtifact} />}

                <p className="flex flex-wrap items-center gap-x-4 gap-y-1 pt-1 text-[11px] text-muted">
                  <span>
                    轮次 <span className="font-mono tabular-nums text-fg-2">{summary?.rounds ?? flow.length}</span>
                  </span>
                  <span>
                    总时长 <span className="font-mono tabular-nums text-fg-2">{fmtDuration(summary?.duration_s ?? audioSec)}</span>
                  </span>
                  {audioArtifact && (
                    <span>
                      格式 <span className="font-mono text-fg-2">{audioArtifact.format.toUpperCase()}</span>
                    </span>
                  )}
                  <span>
                    说话人{" "}
                    <span className="font-mono text-fg-2">
                      {summary?.speakers?.join(" , ") || `${shortA} , ${shortB}`}
                    </span>
                  </span>
                </p>
              </div>
            )}

            {/* 空态：还没有提交过任务 */}
            {!run && (
              <EmptyState
                icon={<AudioLines size={18} strokeWidth={1.75} />}
                title="还没有生成结果"
                description="填写内容、搭配两位说话人后点击「生成播客」，逐轮对话会实时显示在这里，成品可试听与下载。"
                action={
                  <Button variant="secondary" size="sm" onClick={() => jump(contentEmpty ? 1 : 2)}>
                    {contentEmpty ? "去填写内容" : "去搭配音色"}
                  </Button>
                }
              />
            )}
          </CardBody>
        </Card>
      </div>
    </>
  );
}
