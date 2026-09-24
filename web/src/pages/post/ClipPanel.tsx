import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  AudioLines,
  Download,
  ListMusic,
  Music,
  Pause,
  Play,
  Slice,
  SlidersHorizontal,
} from "lucide-react";
import { apiBase, fetchJSON } from "../../lib/api";
import { formatTime, loadPeaks } from "../../lib/player";
import { RegionPlayer } from "../../lib/region";
import { useTaskEvents } from "../../lib/ws";
import type { TaskStatus } from "../../lib/types";
import { FileDrop } from "../../components/FileDrop";
import { PanelIntro } from "./PanelIntro";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  Input,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  WaveLoader,
  WavePlayer,
  useToast,
} from "../../ui";

// 切高潮（音频后期 · 切高潮 Tab）：hook 副歌候选检测 → 波形候选选区 → 选区试听 → audio/clip 切片导出。
// 输入两通道归一为 ClipInput：?artifact=<id>（音乐搜索「切高潮」prepare 落盘的产物）或
// 本地 FileDrop 上传（/api/uploads → file_id）；提交 hook/clip 任务时按 kind 选
// artifact_inputs:[id] / file_ids:[id]（任务契约两通道皆合法，互斥由后端校验）。
// 页面形态对齐 MixerPanel：WS 进度卡 + 产物行 + 引擎 dead 旗标生命周期 + 空格试听，
// 时间线交互复用 MixerTimeline 的 pointer 命中/setPointerCapture/主题 MutationObserver 模式。

/** 输入归一：artifact=产物通道（prepare/产物直入），fileId=本地上传通道 */
type ClipInput = { kind: "artifact"; id: string } | { kind: "fileId"; id: string };

interface Cand {
  start: number;
  end: number;
  score: number;
}

interface HookTask {
  id: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: { candidates?: Cand[]; duration_sec?: number; source?: string };
}

interface ClipTask {
  id: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: {
    start?: number;
    end?: number;
    fade_in?: number;
    fade_out?: number;
    loudness?: number;
    format?: string;
    duration_sec?: number;
  };
}

interface ClipArtifact {
  id: string;
  kind: string;
  filename: string;
  format?: string;
  size?: number;
  duration_ms?: number;
}

/** 媒体地址两通道：产物 stream / 上传文件 stream（同源，cookie 自动携带） */
const streamURL = (input: ClipInput) =>
  input.kind === "artifact"
    ? `${apiBase}/api/artifacts/${input.id}/stream`
    : `${apiBase}/api/uploads/${input.id}/stream`;

const mediaURL = (id: string) => `${apiBase}/api/artifacts/${id}/stream`;

/** 选区形状（秒）；空选区 end<=start */
interface Sel {
  start: number;
  end: number;
}
const EMPTY_SEL: Sel = { start: 0, end: 0 };
const hasSpan = (s: Sel) => s.end - s.start > 0;
const round2 = (v: number) => Math.round(v * 100) / 100;
const clampN = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));

/** 时长预设：预设只改选区长度、起点不动；自由=两边缘可拖 + 空白单击重摆起点 */
const PRESETS = [
  { id: "ring", label: "铃声 30s", sec: 30 },
  { id: "short", label: "短视频 15s", sec: 15 },
  { id: "s30", label: "30s", sec: 30 },
  { id: "s60", label: "60s", sec: 60 },
  { id: "free", label: "自由", sec: 0 },
] as const;
type PresetID = (typeof PRESETS)[number]["id"];

/** 自由选区长度下限（秒）；上限对齐后端 audio/clip 的 clipMaxSpan */
const MIN_SEL = 5;
const MAX_SEL = 600;
/** hook 候选检测参数（工具默认档：30s 窗口 × top3，本地 DSP 零额度） */
const HOOK_DURATION = 30;
const HOOK_COUNT = 3;

export default function ClipPanel() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();

  // ---- 输入（?artifact= 挂载时读一次；上传/重选后变更，全部下游随 input.id 重置） ----
  const [input, setInput] = useState<ClipInput | null>(null);
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    const id = searchParams.get("artifact");
    if (id) setInput({ kind: "artifact", id });
    // 只在挂载时读一次查询参数（同 MixerPanel 的跨页水合模式）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 输入镜像与归一键：下游重活（引擎解码/任务提交）以原始键为依赖——同键的对象重建
  // 不触发重跑（StrictMode 双挂载下 ？artifact= 会 set 两次新对象，原始键挡住重复解码/提交）
  const inputRef = useRef<ClipInput | null>(null);
  inputRef.current = input;
  const inputKey = input ? `${input.kind}:${input.id}` : null;

  // ---- 引擎与波形 ----
  const engineRef = useRef<RegionPlayer | null>(null);
  const [ready, setReady] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [duration, setDuration] = useState(0);
  const [peaks, setPeaks] = useState<number[]>([]);

  // ---- 选区与试听 ----
  const [sel, setSel] = useState<Sel>(EMPTY_SEL);
  const [preset, setPreset] = useState<PresetID>("free");
  const [time, setTime] = useState(0);
  const [playing, setPlaying] = useState(false);

  // ---- 候选检测任务（audio/hook：Summary.candidates 承载候选，零产物） ----
  const [hookId, setHookId] = useState<string | null>(null);
  const [hookTask, setHookTask] = useState<HookTask | null>(null);
  const [cands, setCands] = useState<Cand[]>([]);

  // ---- 导出（audio/clip） ----
  const [fadeIn, setFadeIn] = useState(1);
  const [fadeOut, setFadeOut] = useState(0.5);
  const [loudOn, setLoudOn] = useState(true);
  const [format, setFormat] = useState("mp3");
  const [clipId, setClipId] = useState<string | null>(null);
  const [clipTask, setClipTask] = useState<ClipTask | null>(null);
  const [clipArts, setClipArts] = useState<ClipArtifact[]>([]);
  const [clipBusy, setClipBusy] = useState(false);

  const ev = useTaskEvents();

  // 引擎生命周期：input 就绪 → RegionPlayer.load（fetch+解码常驻）；换源/卸载 → dispose
  // 释放 AudioBuffer。dead 旗标防卸载竞态（load 慢于 unmount 时引擎建好即拆），
  // peaks 与引擎加载并行（loadPeaks 缓存，失败静默降级为空波形，同 MixerPanel）。
  useEffect(() => {
    const cur = inputRef.current;
    if (!cur) {
      setReady(false);
      setLoadError(null);
      setDuration(0);
      setPeaks([]);
      return;
    }
    let dead = false;
    setReady(false);
    setLoadError(null);
    setPlaying(false);
    setTime(0);
    setDuration(0);
    setPeaks([]);
    RegionPlayer.load(streamURL(cur))
      .then((eng) => {
        if (dead) {
          eng.dispose(); // 卸载竞态：load 慢于 unmount，引擎建好即拆
          return;
        }
        eng.onTick = setTime;
        eng.onEnded = () => setPlaying(false); // 自然播完落播放态（引擎 stop() 不触发）
        engineRef.current = eng;
        setDuration(eng.duration);
        setReady(true);
      })
      .catch((e: Error) => {
        if (!dead) setLoadError(e.message);
      });
    loadPeaks(streamURL(cur))
      .then((p) => {
        if (!dead) setPeaks(p);
      })
      .catch(() => {});
    return () => {
      dead = true;
      engineRef.current?.dispose();
      engineRef.current = null;
    };
  }, [inputKey]);

  // 候选检测任务：input 就绪即提交（与引擎解码/loadPeaks 并行，流程 §1.3）。
  // StrictMode 双挂载防重：同 input 键只提交一次（ref 键对齐输入身份）。
  const hookSubmittedRef = useRef<string | null>(null);
  useEffect(() => {
    const cur = inputRef.current;
    if (!cur) {
      hookSubmittedRef.current = null;
      setCands([]);
      setSel(EMPTY_SEL);
      setPreset("free");
      setHookId(null);
      setHookTask(null);
      return;
    }
    const key = `${cur.kind}:${cur.id}`;
    if (hookSubmittedRef.current === key) return;
    hookSubmittedRef.current = key;
    setHookId(null);
    setHookTask(null);
    setCands([]);
    setSel(EMPTY_SEL);
    setPreset("free");
    fetchJSON<{ task_id: string }>("/api/tasks", {
      method: "POST",
      body: JSON.stringify({
        provider: "audio",
        tool: "hook",
        params: { duration: HOOK_DURATION, count: HOOK_COUNT },
        // 输入归一：artifact → artifact_inputs，fileId → file_ids（两通道互斥，单输入按序映射 files.audio）
        ...(cur.kind === "artifact" ? { artifact_inputs: [cur.id] } : { file_ids: [cur.id] }),
      }),
    })
      .then((d) => {
        setHookId(d.task_id);
        setHookTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
        void qc.invalidateQueries({ queryKey: ["tasks"] });
      })
      .catch((e: Error) => toast({ tone: "error", title: "候选检测提交失败", description: e.message }));
  }, [inputKey, toast, qc]);

  // WS 事件驱动两条任务链；终态拉详情（候选检测读 summary.candidates，导出读产物行）
  useEffect(() => {
    if (!ev) return;
    if (hookId && ev.task_id === hookId) {
      if (ev.type === "progress") {
        setHookTask((t) => ({
          ...(t ?? ({ id: hookId, status: "running" } as HookTask)),
          progress: ev.progress ?? 0,
          progress_note: ev.note ?? "",
        }));
      }
      if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
        fetchJSON<{ task: HookTask; artifacts: ClipArtifact[] }>(`/api/tasks/${hookId}`)
          .then((d) => {
            setHookTask(d.task);
            setCands(d.task.summary?.candidates ?? []);
          })
          .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
      }
    }
    if (clipId && ev.task_id === clipId) {
      if (ev.type === "progress") {
        setClipTask((t) => ({
          ...(t ?? ({ id: clipId, status: "running" } as ClipTask)),
          progress: ev.progress ?? 0,
          progress_note: ev.note ?? "",
        }));
      }
      if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
        fetchJSON<{ task: ClipTask; artifacts: ClipArtifact[] }>(`/api/tasks/${clipId}`)
          .then((d) => {
            setClipTask(d.task);
            setClipArts(d.artifacts);
          })
          .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
      }
    }
  }, [ev, hookId, clipId, toast]);

  const hasSel = hasSpan(sel);

  /** 选区变化唯一提交口（候选带/空白单击/手柄拖完/键盘微调）：手动编辑即脱离预设档；
   *  播放中提交新选区=按新选区从起点重播（选中即重播起点）。 */
  const pickSel = useCallback(
    (s: Sel) => {
      setSel(s);
      setPreset("free");
      const eng = engineRef.current;
      if (eng?.playing) {
        eng.play(s.start, s.end)
          .then(() => setPlaying(true))
          .catch(() => {
            setPlaying(false);
            toast({ tone: "error", title: "无法开始播放", description: "浏览器阻止了自动播放，请再点一次" });
          });
      }
    },
    [toast],
  );

  /** 手柄拖拽中：只更新选区（试听已在拖拽开始时停止），松手后由用户再按播放 */
  const dragSel = useCallback((s: Sel) => {
    setSel(s);
    setPreset("free");
  }, []);

  /** 拖拽开始：停掉试听——拖拽期间选区与在播区间错位，听到的不该是旧区间 */
  const dragStarted = useCallback(() => {
    const eng = engineRef.current;
    if (eng?.playing) {
      eng.stop();
      setPlaying(false);
    }
  }, []);

  // 预设：起点不动、改长度（钳到音频时长内，上限对齐后端 clipMaxSpan）；自由=维持现状
  const applyPreset = (p: (typeof PRESETS)[number]) => {
    setPreset(p.id);
    if (p.sec <= 0) return;
    const start = hasSel ? sel.start : 0;
    if (duration <= start + 1) return; // 音频比预设起点还短，无从应用（极端小文件兜底）
    const next: Sel = { start, end: Math.min(start + p.sec, duration) };
    setSel(next);
    // 预设不算「手动编辑」：播放中同样按新选区重播，保持「在播=面板所见」
    const eng = engineRef.current;
    if (eng?.playing) {
      eng.play(next.start, next.end)
        .then(() => setPlaying(true))
        .catch(() => {
          setPlaying(false);
          toast({ tone: "error", title: "无法开始播放", description: "浏览器阻止了自动播放，请再点一次" });
        });
    }
  };

  /** 选区试听/停止唯一入口：play() 在 ctx.resume() 失败时 reject——必须接住并提示
   *  再点一次（P0 交接点）；播放按钮恒从选区起点播（RegionPlayer 停旧建新）。 */
  const togglePlay = useCallback(() => {
    const eng = engineRef.current;
    if (!eng || !hasSel) return;
    if (eng.playing) {
      eng.stop();
      setPlaying(false);
    } else {
      eng.play(sel.start, sel.end)
        .then(() => setPlaying(true))
        .catch(() =>
          toast({ tone: "error", title: "无法开始播放", description: "浏览器阻止了自动播放，请再点一次" }),
        );
    }
  }, [sel, hasSel, toast]);

  // 空格试听/停止（MixerPanel 同款）：document 级监听 + e.repeat 防连发 + 交互元素守卫——
  // 焦点在输入框/按钮/选择器上时空格保持原生语义
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.repeat) return; // 长按空格的连发事件不反复切换试听
      if (e.key !== " ") return;
      const el = document.activeElement;
      if (el instanceof HTMLElement) {
        const tag = el.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "BUTTON" || el.isContentEditable) {
          return;
        }
      }
      if (!engineRef.current || !hasSel) return;
      e.preventDefault(); // 接管空格：阻止页面滚动
      togglePlay();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [togglePlay, hasSel]);

  // 本地上传：POST /api/uploads 拿 file_id（不设 Content-Type 让浏览器带 multipart boundary），
  // 输入归一为 fileId 通道——提交 hook/clip 任务走 file_ids
  const onUpload = async (f: File | null) => {
    if (!f || uploading) return;
    setUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", f);
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      setInput({ kind: "fileId", id: up.file_id });
    } catch (e) {
      toast({ tone: "error", title: "上传失败", description: (e as Error).message });
    } finally {
      setUploading(false);
    }
  };

  const submitClip = () => {
    if (!input || !hasSel) return;
    setClipBusy(true);
    setClipArts([]);
    fetchJSON<{ task_id: string }>("/api/tasks", {
      method: "POST",
      body: JSON.stringify({
        provider: "audio",
        tool: "clip",
        params: {
          start: round2(sel.start),
          end: round2(sel.end),
          fade_in: Math.max(0, fadeIn || 0),
          fade_out: Math.max(0, fadeOut || 0),
          loudness: loudOn ? -14 : 0, // 0=关响度归一（后端 parseClipParams 语义）
          format,
        },
        ...(input.kind === "artifact" ? { artifact_inputs: [input.id] } : { file_ids: [input.id] }),
      }),
    })
      .then((d) => {
        setClipId(d.task_id);
        setClipTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
        void qc.invalidateQueries({ queryKey: ["tasks"] });
      })
      .catch((e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }))
      .finally(() => setClipBusy(false));
  };

  const hookRunning = hookTask?.status === "running" || hookTask?.status === "pending";
  const clipRunning = clipTask?.status === "running" || clipTask?.status === "pending";

  return (
    <>
      <PanelIntro
        title="切高潮"
        description="副歌候选一键切片：hook 检测候选 → 波形选区试听 → audio/clip 导出 mp3/m4a/m4r"
        actions={
          input && (
            <Button size="sm" variant="secondary" onClick={() => setInput(null)}>
              重选音频
            </Button>
          )
        }
      />

      {!input && (
        <Card>
          <CardHeader title="选择音频" icon={<Slice size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-4">
            <FileDrop
              onFile={(f) => void onUpload(f)}
              accept=".mp3,.wav,.m4a,.aac,.flac,.ogg,.m4r"
              emptyHint="mp3 / wav / m4a / flac 等常见音频，上传后自动检测副歌候选"
              label="上传音频文件"
            />
            {uploading && (
              <p className="flex items-center gap-2 text-xs text-muted">
                <WaveLoader label="上传中" className="shrink-0" />
                正在上传音频…
              </p>
            )}
            <p className="text-[11px] leading-relaxed text-muted">
              也可以在音乐搜索结果行点「切高潮」，歌曲自动落盘为本页产物输入。
            </p>
            <div>
              <Button size="sm" variant="secondary" onClick={() => navigate("/music")} icon={<Music size={14} strokeWidth={1.75} />}>
                去音乐搜索
              </Button>
            </div>
          </CardBody>
        </Card>
      )}

      {input && (
        <div className="mt-4 grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
          {/* 左主区：候选波形时间线 + 走带控制 */}
          <Card>
            <CardHeader
              title="候选波形"
              icon={<AudioLines size={15} strokeWidth={1.75} />}
              aside={<span className="micro">总长 {ready ? formatTime(duration) : "—"}</span>}
            />
            <CardBody className="space-y-3">
              {loadError ? (
                <div className="rounded-[var(--radius-sm)] border border-warn/40 bg-warn/10 px-3 py-2 text-xs">
                  <p className="text-fg-2">音频加载失败：{loadError}</p>
                  <p className="mt-1 text-muted">产物可能已被清理或链接失效，可点右上「重选音频」换一首。</p>
                </div>
              ) : !ready ? (
                <div className="space-y-2" aria-busy="true" aria-label="音频解码中">
                  <Skeleton className="h-44 w-full" />
                  <p className="flex items-center gap-2 text-xs text-muted">
                    <WaveLoader label="正在解码音频" className="shrink-0" />
                    正在拉取并解码音频，歌曲越长等待越久…
                  </p>
                </div>
              ) : (
                <ClipTimeline
                  duration={duration}
                  peaks={peaks}
                  candidates={cands}
                  sel={sel}
                  time={time}
                  playing={playing}
                  freeDrag={preset === "free"}
                  onPick={pickSel}
                  onDragMove={dragSel}
                  onDragStart={dragStarted}
                />
              )}
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  size="sm"
                  variant="primary"
                  disabled={!ready || !hasSel}
                  onClick={togglePlay}
                  icon={playing ? <Pause size={14} strokeWidth={1.75} /> : <Play size={14} strokeWidth={1.75} />}
                >
                  {playing ? "停止" : "试听选区"}
                </Button>
                <span className="font-mono text-xs tabular-nums text-fg-2">
                  {formatTime(time)} <span className="text-muted">/</span> {formatTime(duration)}
                </span>
                <span className="ml-auto font-mono text-xs tabular-nums text-muted">
                  选区 {hasSel ? `${round2(sel.end - sel.start)}s` : "—"}
                </span>
              </div>
              <div role="radiogroup" aria-label="选区时长预设" className="flex flex-wrap gap-1.5">
                {PRESETS.map((p) => {
                  const active = preset === p.id;
                  return (
                    <button
                      key={p.id}
                      type="button"
                      role="radio"
                      aria-checked={active}
                      disabled={!ready}
                      onClick={() => applyPreset(p)}
                      className={`cursor-pointer rounded-full border px-2.5 py-1 text-xs transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
                        active
                          ? "border-accent bg-accent/10 text-accent"
                          : "border-line text-fg-2 hover:border-line-strong"
                      }`}
                    >
                      {p.label}
                    </button>
                  );
                })}
              </div>
              <p className="text-[11px] text-muted">
                点击候选竖带选中 · 预设改长度起点不动 · 自由模式拖边缘调长度（最短 5s）· 空白单击重摆起点 · 空格试听/停止
              </p>
            </CardBody>
          </Card>

          {/* 右参数面板：候选列表 → 导出参数（320px，<1024 落到下方） */}
          <div className="space-y-4">
            <Card>
              <CardHeader
                title="副歌候选"
                icon={<ListMusic size={15} strokeWidth={1.75} />}
                aside={<span className="micro">{cands.length > 0 ? `${cands.length} 个` : (hookRunning ? "检测中" : "—")}</span>}
              />
              <CardBody className="space-y-2">
                {cands.length === 0 ? (
                  <p className="py-2 text-xs text-muted">
                    {hookRunning || !hookTask ? "候选检测进行中，完成后自动列出…" : "暂无候选；可重选音频再试。"}
                  </p>
                ) : (
                  <div role="listbox" aria-label="副歌候选列表" className="space-y-1.5">
                    {cands.map((c, i) => {
                      const active = round2(sel.start) === c.start && round2(sel.end) === c.end;
                      return (
                        <button
                          key={`${c.start}-${c.end}`}
                          type="button"
                          role="option"
                          aria-selected={active}
                          onClick={() => pickSel({ start: c.start, end: c.end })}
                          className={`flex w-full cursor-pointer items-center gap-2 rounded-[var(--radius-sm)] border px-2.5 py-2 text-left text-xs transition-colors duration-150 ${
                            active
                              ? "border-accent bg-raise-2 text-accent"
                              : "border-line bg-raise text-fg-2 hover:border-line-strong hover:text-fg"
                          }`}
                        >
                          <span className="font-mono tabular-nums text-muted">#{i + 1}</span>
                          <span className="font-mono tabular-nums">
                            {formatTime(c.start)}–{formatTime(c.end)}
                          </span>
                          <span className="ml-auto font-mono tabular-nums text-[11px] text-muted">
                            得分 {c.score.toFixed(1)}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader title="导出参数" icon={<SlidersHorizontal size={15} strokeWidth={1.75} />} />
              <CardBody className="space-y-4">
                <div className="grid grid-cols-2 gap-3">
                  <Field label="淡入" aside={<span className="font-mono text-[11px] tabular-nums">{fadeIn || 0}s</span>} hint="0=不淡入">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        type="number"
                        min={0}
                        step={0.5}
                        value={fadeIn}
                        onChange={(e) => setFadeIn(Number(e.target.value) || 0)}
                        {...rest}
                      />
                    )}
                  </Field>
                  <Field label="淡出" aside={<span className="font-mono text-[11px] tabular-nums">{fadeOut || 0}s</span>} hint="0=不淡出">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        type="number"
                        min={0}
                        step={0.5}
                        value={fadeOut}
                        onChange={(e) => setFadeOut(Number(e.target.value) || 0)}
                        {...rest}
                      />
                    )}
                  </Field>
                </div>
                <p className="text-[11px] text-muted">淡入/淡出超选区一半时服务端自动折半。</p>
                <label className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                  <input
                    type="checkbox"
                    checked={loudOn}
                    onChange={(e) => setLoudOn(e.target.checked)}
                    className="size-4 cursor-pointer accent-accent"
                  />
                  响度归一（-14 LUFS）
                </label>
                <Field label="输出格式" hint="m4r 即 iPhone 铃声（AAC 128k）">
                  {({ id, ...rest }) => (
                    <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
                      <option value="mp3">MP3（128k）</option>
                      <option value="m4a">M4A（AAC）</option>
                      <option value="m4r">M4R（iPhone 铃声）</option>
                    </Select>
                  )}
                </Field>
                <Button
                  variant="primary"
                  className="w-full"
                  loading={clipBusy}
                  disabled={!hasSel}
                  onClick={submitClip}
                  icon={<Download size={14} strokeWidth={1.75} />}
                >
                  导出切片
                </Button>
                <p className="text-[11px] leading-relaxed text-muted">
                  {!hasSel
                    ? "先在左侧波形点选候选或拖出选区，再导出。"
                    : `导出 audio/clip 任务：${formatTime(sel.start)}–${formatTime(sel.end)}（${round2(sel.end - sel.start)}s），完成后在下方「切片结果」试听下载。`}
                </p>
              </CardBody>
            </Card>
          </div>
        </div>
      )}

      {hookTask && (
        <Card className="mt-4">
          <CardHeader
            title="候选检测进度"
            aside={
              <>
                {hookTask.cost_ms ? <span className="micro">{`${(hookTask.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={hookTask.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={hookTask.progress} active={hookRunning} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {hookRunning && <WaveLoader label="候选检测中" className="shrink-0" />}
              <span className="min-w-0">{hookTask.progress_note || "—"}</span>
            </p>
            {hookTask.error && <p className="text-xs text-danger break-words">{hookTask.error}</p>}
            {hookTask.summary && (
              <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs">
                <span className="text-muted">
                  候选 <span className="font-mono text-fg-2">{cands.length} 个</span>
                </span>
                {hookTask.summary.duration_sec != null && (
                  <span className="text-muted">
                    音频时长 <span className="font-mono text-fg-2">{formatTime(hookTask.summary.duration_sec)}</span>
                  </span>
                )}
                {hookTask.summary.source && (
                  <span className="text-muted">
                    来源 <span className="font-mono text-fg-2">{hookTask.summary.source}</span>
                  </span>
                )}
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {clipTask && (
        <Card className="mt-4">
          <CardHeader
            title="导出进度"
            aside={
              <>
                {clipTask.cost_ms ? <span className="micro">{`${(clipTask.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={clipTask.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={clipTask.progress} active={clipRunning} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {clipRunning && <WaveLoader label="切片处理中" className="shrink-0" />}
              <span className="min-w-0">{clipTask.progress_note || "—"}</span>
            </p>
            {clipTask.error && <p className="text-xs text-danger break-words">{clipTask.error}</p>}
            {clipTask.summary && (
              <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs">
                <span className="text-muted">
                  选区{" "}
                  <span className="font-mono text-fg-2">
                    {clipTask.summary.start ?? "—"}s – {clipTask.summary.end ?? "—"}s
                  </span>
                </span>
                <span className="text-muted">
                  淡入 <span className="font-mono text-fg-2">{clipTask.summary.fade_in ?? 0}s</span>
                </span>
                <span className="text-muted">
                  淡出 <span className="font-mono text-fg-2">{clipTask.summary.fade_out ?? 0}s</span>
                </span>
                <span className="text-muted">
                  响度{" "}
                  <span className="font-mono text-fg-2">
                    {clipTask.summary.loudness ? `${clipTask.summary.loudness} LUFS` : "关"}
                  </span>
                </span>
                <span className="text-muted">
                  格式 <span className="font-mono uppercase text-fg-2">{clipTask.summary.format ?? "—"}</span>
                </span>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {clipArts.length > 0 && (
        <Card className="mt-4">
          <CardHeader
            title="切片结果"
            icon={<AudioLines size={15} strokeWidth={1.75} />}
            aside={<span className="micro">{clipArts.length} 个</span>}
          />
          <CardBody className="space-y-2">
            {clipArts.map((a) => (
              <div
                key={a.id}
                className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3"
              >
                <span className="w-14 shrink-0 text-xs text-fg">切片</span>
                <WavePlayer
                  src={mediaURL(a.id)}
                  title={a.filename}
                  sub="切片成品"
                  durationSec={a.duration_ms ? a.duration_ms / 1000 : undefined}
                  className="min-w-0 flex-1 basis-64"
                />
                <a
                  href={`${apiBase}/api/artifacts/${a.id}/download`}
                  className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                >
                  <Download size={13} strokeWidth={1.75} />
                  下载
                </a>
              </div>
            ))}
          </CardBody>
        </Card>
      )}

    </>
  );
}

/* ============================================================
 * ClipTimeline：候选波形时间线（页内子组件，交互复用 MixerTimeline 模式）
 * ============================================================ */

/** 边缘手柄命中半径（px）：屏幕 x 轴 ±8px 即命中（同 MixerTimeline 关键帧手柄）。 */
const HIT_R = 8;
/** 时间 → 内盒宽度占比（0..1，钳 [0,1]）：手柄 left = ×100%（CSS），画布/命中 = ×内盒宽（px）。 */
const fracOf = (t: number, duration: number) => (duration > 0 ? clampN(t / duration, 0, 1) : 0);

interface TimelineProps {
  duration: number;
  peaks: number[];
  candidates: Cand[];
  sel: Sel;
  time: number;
  playing: boolean;
  /** 自由模式：两边缘手柄可拖；预设模式下可见但只读（长度被预设钉住） */
  freeDrag: boolean;
  /** 单击提交（候选带/空白重摆/键盘微调）：父层据此更新选区并在播时重播 */
  onPick: (s: Sel) => void;
  /** 手柄拖拽中的持续回调（父层只更新选区，不触发重播） */
  onDragMove: (s: Sel) => void;
  /** 手柄拖拽开始（父层停掉试听） */
  onDragStart: () => void;
}

/** 切片时间线：波形 + 候选竖带（score→透明度）+ 选区高亮与两边缘手柄 + 播放头。
 *  画布按 devicePixelRatio 渲染；主题色经 MutationObserver 读 CSS 变量（--accent/--wave-idle），
 *  与 MixerTimeline/WavePlayer 同一模式——无画布外硬编码色板。 */
function ClipTimeline({
  duration,
  peaks,
  candidates,
  sel,
  time,
  playing,
  freeDrag,
  onPick,
  onDragMove,
  onDragStart,
}: TimelineProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const waveRef = useRef<HTMLCanvasElement>(null);
  const dragRef = useRef<"start" | "end" | null>(null);
  // 兜底色与 theme.css 暗色默认一致；实际值一律以 computed style 读取为准
  const colorsRef = useRef({ accent: "#e2a35a", idle: "rgba(246,236,221,.16)" });
  /** 绘制函数引用：主题切换/尺寸变化由观察器直接触发重绘，不经过 React 状态。 */
  const drawRef = useRef<() => void>(() => {});
  const maxScore = candidates.reduce((m, c) => Math.max(m, c.score), 0);

  // 主题色读取：暗/亮切换时刷新并重绘（同 MixerTimeline 的 MutationObserver 模式）
  useEffect(() => {
    const read = () => {
      const cs = getComputedStyle(document.documentElement);
      colorsRef.current = {
        accent: cs.getPropertyValue("--accent").trim() || "#e2a35a",
        idle:
          cs.getPropertyValue("--wave-idle").trim() ||
          cs.getPropertyValue("--line-strong").trim() ||
          "rgba(246,236,221,.16)",
      };
      drawRef.current();
    };
    read();
    const ob = new MutationObserver(read);
    ob.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => ob.disconnect();
  }, []);

  // 尺寸变化重绘：canvas 位图不随 CSS 自动重采样（同 MixerTimeline）
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => drawRef.current());
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // 绘制：波形（已播段 accent / 未播 idle）→ 候选竖带（score 相对最满分映射透明度）→
  // 选区高亮与边缘线 → 播放头。父组件 tick 重渲染走这里：纯 canvas 重画、无状态写入。
  useEffect(() => {
    const draw = () => {
      const c = waveRef.current;
      if (!c) return;
      const dpr = window.devicePixelRatio || 1;
      const w = c.clientWidth;
      const h = c.clientHeight;
      if (w === 0 || h === 0) return;
      if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
        c.width = Math.round(w * dpr);
        c.height = Math.round(h * dpr);
      }
      const ctx = c.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);

      const { accent, idle } = colorsRef.current;
      const barW = 2;
      const gap = 1;
      const step = barW + gap;
      const bars = Math.max(1, Math.floor(w / step));
      const xAt = (t: number) => fracOf(t, duration) * w;

      // 波形：已播段 accent、未播 idle（同 WavePlayer 的已播着色语义）
      if (peaks.length > 0) {
        for (let i = 0; i < bars; i++) {
          const peak = peaks[Math.min(peaks.length - 1, Math.floor((i / bars) * peaks.length))];
          const bh = Math.max(2, peak * h * 0.92);
          ctx.fillStyle = duration > 0 && (i + 0.5) / bars <= time / duration ? accent : idle;
          ctx.fillRect(i * step, (h - bh) / 2, barW, bh);
        }
      }

      // 候选竖带：score 相对最高分归一映射透明度（0.14~0.40），分高者更醒目
      if (duration > 0 && candidates.length > 0) {
        ctx.fillStyle = accent;
        for (const cand of candidates) {
          const rel = maxScore > 0 ? cand.score / maxScore : 1;
          ctx.globalAlpha = 0.14 + 0.26 * rel;
          ctx.fillRect(xAt(cand.start), 0, Math.max(2, xAt(cand.end) - xAt(cand.start)), h);
        }
        ctx.globalAlpha = 1;
      }

      // 选区：淡 accent 罩 + 两边缘 2px 竖线（手柄按钮叠加其上负责交互）
      if (duration > 0 && hasSpan(sel)) {
        const x0 = xAt(sel.start);
        const x1 = xAt(sel.end);
        ctx.globalAlpha = 0.1;
        ctx.fillStyle = accent;
        ctx.fillRect(x0, 0, x1 - x0, h);
        ctx.globalAlpha = 0.9;
        ctx.fillRect(x0 - 1, 0, 2, h);
        ctx.fillRect(x1 - 1, 0, 2, h);
        ctx.globalAlpha = 1;
      }

      // 播放头：暂停时加淡光晕标记驻留位置，播放中仅细线随 tick 前进
      if (duration > 0) {
        const x = xAt(time);
        if (!playing) {
          ctx.globalAlpha = 0.3;
          ctx.fillStyle = accent;
          ctx.fillRect(x - 1.5, 0, 4, h);
          ctx.globalAlpha = 1;
        }
        ctx.fillStyle = accent;
        ctx.fillRect(x, 0, 1, h);
      }
    };
    drawRef.current = draw;
    draw();
  }, [duration, peaks, candidates, sel, time, playing, maxScore]);

  /** 时间 → 屏幕 x（wrap 内盒 px）。与手柄 left（同占比的 CSS %）、命中检测同一坐标系。 */
  const xOf = useCallback(
    (t: number) => {
      const el = wrapRef.current;
      if (!el || duration <= 0) return 0;
      return fracOf(t, duration) * el.clientWidth;
    },
    [duration],
  );

  /** 屏幕 clientX → 时间（钳 [0, duration]）。原点取内盒（扣掉 1px 左边框），与 xOf 同系。 */
  const tOf = useCallback(
    (clientX: number) => {
      const el = wrapRef.current;
      if (!el || duration <= 0 || el.clientWidth === 0) return 0;
      const rect = el.getBoundingClientRect();
      const ratio = clampN((clientX - rect.left - el.clientLeft) / el.clientWidth, 0, 1);
      return ratio * duration;
    },
    [duration],
  );

  /** 屏幕 clientX → 内盒 x（命中检测用，与手柄 left 同坐标系）。 */
  const hitX = (clientX: number) => {
    const el = wrapRef.current;
    if (!el) return -Infinity;
    return clientX - el.getBoundingClientRect().left - el.clientLeft;
  };

  /* 指针交互（复用 MixerTimeline 的 pointerdown 命中 → setPointerCapture → move 持续回调模式）：
     1) 自由模式先命中两边缘手柄（±8px）→ 开始拖（父层停试听）；
     2) 命中候选竖带 → 选中该候选（start=cand.start，长度=cand 跨度）；
     3) 空白 → 保持长度重摆起点（起点=点击处，钳 [0, duration-长度]）。 */
  const onPointerDownWrap = (e: React.PointerEvent) => {
    if (duration <= 0) return;
    const x = hitX(e.clientX);
    if (freeDrag && hasSpan(sel)) {
      const dS = Math.abs(xOf(sel.start) - x);
      const dE = Math.abs(xOf(sel.end) - x);
      if (dS <= HIT_R || dE <= HIT_R) {
        // 短选区两边缘可能都落进命中半径：抓更近的那条
        dragRef.current = dS <= dE ? "start" : "end";
        (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
        onDragStart();
        return;
      }
    }
    const cand = candidates.find((c) => x >= xOf(c.start) && x <= xOf(c.end));
    if (cand) {
      onPick({ start: cand.start, end: cand.end });
      return;
    }
    const len = sel.end - sel.start;
    if (len > 0) {
      const start = clampN(tOf(e.clientX), 0, Math.max(0, duration - len));
      onPick({ start, end: start + len });
    }
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const edge = dragRef.current;
    if (!edge) return;
    const t = tOf(e.clientX); // 已钳 [0, duration]
    if (edge === "start") {
      onDragMove({ start: clampN(t, 0, sel.end - MIN_SEL), end: sel.end });
    } else {
      onDragMove({ start: sel.start, end: clampN(t, sel.start + MIN_SEL, Math.min(duration, sel.start + MAX_SEL)) });
    }
  };

  const onPointerUp = () => {
    dragRef.current = null;
  };

  /** 手柄键盘微调（自由模式）：±0.5s，钳下限/上限后按单击提交（提交语义同拖完松手）。 */
  const nudge = (edge: "start" | "end", d: number) => {
    if (!freeDrag || !hasSpan(sel)) return;
    if (edge === "start") {
      onPick({ start: clampN(sel.start + d, 0, sel.end - MIN_SEL), end: sel.end });
    } else {
      onPick({ start: sel.start, end: clampN(sel.end + d, sel.start + MIN_SEL, Math.min(duration, sel.start + MAX_SEL)) });
    }
  };

  const onKeyDown = (e: React.KeyboardEvent, edge: "start" | "end") => {
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      nudge(edge, e.key === "ArrowLeft" ? -0.5 : 0.5);
      e.preventDefault();
    }
  };

  return (
    <div
      ref={wrapRef}
      role="application"
      aria-label="切片时间线"
      tabIndex={0}
      onPointerDown={onPointerDownWrap}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      className="relative h-44 cursor-crosshair select-none rounded-[var(--radius-sm)] border border-line bg-inset"
      style={{ touchAction: "none" }}
    >
      {/* 波形/候选带/选区/播放头画布（装饰性：信息由手柄 slider 与 application 语义承载） */}
      <canvas ref={waveRef} className="absolute inset-0 h-full w-full" aria-hidden="true" />
      {/* 选区边缘手柄：绝对定位按钮（键盘可达 + 命中稳定）。left 用内盒百分比定位——
          与画布 xAt 同一占比常量（fracOf），与命中检测 xOf 同一坐标系；渲染期不读 ref。
          预设模式下只读呈现（长度被钉住），自由模式可拖/可键盘微调。 */}
      {duration > 0 && hasSpan(sel) && (["start", "end"] as const).map((edge) => (
        <button
          key={edge}
          type="button"
          role="slider"
          aria-label={edge === "start" ? "选区起点" : "选区终点"}
          aria-valuenow={Math.round(edge === "start" ? sel.start : sel.end)}
          aria-valuemin={0}
          aria-valuemax={Math.round(duration)}
          aria-disabled={!freeDrag}
          onKeyDown={(e) => onKeyDown(e, edge)}
          className={`absolute top-0 h-full w-3 -translate-x-1/2 rounded-none border-0 bg-transparent p-0 ${
            freeDrag ? "cursor-ew-resize" : "cursor-default opacity-60"
          }`}
          style={{ left: `${fracOf(edge === "start" ? sel.start : sel.end, duration) * 100}%` }}
        >
          <span
            className={`mx-auto block h-full w-[3px] rounded-full bg-accent transition-opacity duration-150 ${
              freeDrag ? "opacity-90" : "opacity-50"
            }`}
          />
        </button>
      ))}
    </div>
  );
}
