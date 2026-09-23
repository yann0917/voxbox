// 音频剪辑页：本地 ffmpeg 工具集（provider=audio）的专属 UI。
// 切割器用 canvas 波形 + 拖拽选区（Web Audio 解码峰值，复用 lib/player.loadPeaks）；
// 其余工具为参数表单；调/BPM 查询结果直接渲染任务 Summary。
// 任务链路与分离页同一套：POST /api/tasks → WS 进度 → 终态拉详情。
import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  AudioLines,
  Clock4,
  Download,
  Gauge,
  Layers,
  Music2,
  Pause,
  Play,
  Scissors,
  SlidersHorizontal,
  Sparkles,
  Timer,
  Undo2,
  Volume2,
} from "lucide-react";
import { fetchJSON } from "../lib/api";
import type { Artifact, TaskStatus } from "../lib/types";
import { formatTime, loadPeaks } from "../lib/player";
import { useTaskEvents } from "../lib/ws";
import { FileDrop } from "../components/FileDrop";
import { ArtifactRow } from "../components/ArtifactRow";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  Input,
  PageHeader,
  ProgressBar,
  Select,
  StatusBadge,
  Tabs,
  useToast,
} from "../ui";

const AUDIO_ACCEPT = ".mp3,.wav,.flac,.ogg,.m4a,.aac,.wma,.amr";

type TabKey = "cut" | "join" | "pitch" | "analyze" | "eq" | "volume" | "fade" | "reverse";

const TABS: { value: TabKey; label: string; icon: React.ReactNode }[] = [
  { value: "cut", label: "切割器", icon: <Scissors size={13} strokeWidth={1.75} /> },
  { value: "join", label: "合并器", icon: <Layers size={13} strokeWidth={1.75} /> },
  { value: "pitch", label: "变调变速", icon: <Music2 size={13} strokeWidth={1.75} /> },
  { value: "analyze", label: "调·BPM 查询", icon: <Sparkles size={13} strokeWidth={1.75} /> },
  { value: "eq", label: "均衡器", icon: <SlidersHorizontal size={13} strokeWidth={1.75} /> },
  { value: "volume", label: "音量·响度", icon: <Volume2 size={13} strokeWidth={1.75} /> },
  { value: "fade", label: "淡入淡出", icon: <Timer size={13} strokeWidth={1.75} /> },
  { value: "reverse", label: "倒放", icon: <Undo2 size={13} strokeWidth={1.75} /> },
];

// ---- 任务类型（与 /api/tasks/:id 契约一致） ----
interface TaskInfo {
  id: string;
  provider?: string;
  status: string;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: Record<string, unknown>;
}
interface EditorTask {
  taskId: string;
  task?: TaskInfo;
  artifacts?: Artifact[];
}

/** 每个工具 Tab 独立保留任务结果，切 Tab 不丢。 */
type TaskMap = Partial<Record<TabKey, EditorTask>>;

// ---- 输出格式/码率选项（与后端 ParamSpecs 一致） ----
const FORMAT_OPTS = [
  { value: "auto", label: "跟随源格式" },
  { value: "mp3", label: "MP3" },
  { value: "m4a", label: "M4A (AAC)" },
  { value: "wav", label: "WAV (无损)" },
  { value: "flac", label: "FLAC (无损)" },
  { value: "ogg", label: "OGG" },
];
const BITRATE_OPTS = ["128k", "192k", "256k", "320k"];

/** 宽松时间输入解析：「90.5」秒或「01:30.5」分秒。非法返回 NaN。 */
function parseTimeInput(s: string): number {
  s = s.trim();
  if (s === "") return NaN;
  if (s.includes(":")) {
    const parts = s.split(":").map(Number);
    if (parts.some((n) => Number.isNaN(n))) return NaN;
    return parts.reduce((acc, n) => acc * 60 + n, 0);
  }
  return Number(s);
}

interface SourceState {
  file: File;
  fileId: string;
  url: string;
  duration: number;
  peaks: number[] | null;
  peaksFailed: boolean;
}

/** 均衡器预设（与后端 EqPresets 一致）。 */
const EQ_PRESETS: Record<string, number[]> = {
  flat: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
  pop: [-1, -1, -1, -1, 0, 0, 2, 2, 2, 2],
  rock: [5, 5, 2, 2, -1, -1, 2, 2, 4, 4],
  jazz: [4, 4, 2, 2, -1, -1, 2, 2, 3, 3],
  classical: [4, 4, 2, 2, -1, -1, 2, 2, 3, 3],
  vocal: [-2, -2, -2, -2, 2, 2, 4, 4, 3, 3],
  bass: [8, 8, 5, 5, 1, 1, 0, 0, 0, 0],
  treble: [0, 0, 0, 0, 0, 0, 3, 3, 6, 6],
  loudness: [6, 6, 3, 3, 0, 0, 2, 2, 5, 5],
};
const EQ_BANDS = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000];

export default function AudioEditorPage() {
  const [tab, setTab] = useState<TabKey>("cut");
  const { toast } = useToast();
  const qc = useQueryClient();

  // ---- 共享源文件：选中即上传，波形/时长在浏览器本地解析 ----
  const [src, setSrc] = useState<SourceState | null>(null);
  const [uploading, setUploading] = useState(false);
  const [srcErr, setSrcErr] = useState("");

  const pickFile = useCallback(
    (f: File | null) => {
      setSrcErr("");
      setSrc(null);
      if (!f) return;
      setUploading(true);
      (async () => {
        const fd = new FormData();
        fd.append("file", f);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        const url = URL.createObjectURL(f);
        const duration = await new Promise<number>((resolve) => {
          const el = new Audio();
          el.preload = "metadata";
          el.onloadedmetadata = () => resolve(Number.isFinite(el.duration) ? el.duration : 0);
          el.onerror = () => resolve(0);
          el.src = url;
        });
        let peaks: number[] | null = null;
        let peaksFailed = false;
        try {
          peaks = await loadPeaks(url);
        } catch {
          peaksFailed = true; // wma/amr 等浏览器不可解码格式：波形缺省，仍可提交任务
        }
        setUploading(false);
        setSrc({ file: f, fileId: up.file_id, url, duration, peaks, peaksFailed });
      })().catch((e: Error) => {
        setUploading(false);
        setSrcErr(e.message);
      });
    },
    [],
  );

  // ---- 任务提交与 WS 进度（每 Tab 独立结果） ----
  const [tasks, setTasks] = useState<TaskMap>({});
  const ev = useTaskEvents();

  const submitTask = useCallback(
    async (t: TabKey, body: Record<string, unknown>) => {
      const d = await fetchJSON<{ task_id: string }>("/api/tasks", { method: "POST", body: JSON.stringify(body) });
      setTasks((m) => ({ ...m, [t]: { taskId: d.task_id } }));
      void qc.invalidateQueries({ queryKey: ["tasks"] });
      return d.task_id;
    },
    [qc],
  );

  useEffect(() => {
    if (!ev) return;
    const entry = Object.entries(tasks).find(([, v]) => v?.taskId === ev.task_id);
    if (!entry) return;
    const key = entry[0] as TabKey;
    const cur = entry[1];
    if (!cur) return;
    if (ev.type === "progress") {
      setTasks((m) => ({
        ...m,
        [key]: {
          ...cur,
          task: {
            id: ev.task_id, status: "running", progress: ev.progress ?? 0,
            progress_note: ev.note ?? "", ...(cur.task?.summary ? { summary: cur.task.summary } : {}),
          },
        },
      }));
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<{ task: TaskInfo; artifacts: Artifact[] }>(`/api/tasks/${ev.task_id}`)
        .then((d) => setTasks((m) => ({ ...m, [key]: { taskId: ev.task_id, task: d.task, artifacts: d.artifacts } })))
        .catch(() => undefined);
    }
  }, [ev, tasks]);

  const running = (t: TabKey) => {
    const s = tasks[t]?.task?.status;
    return s === "running" || s === "pending";
  };

  const submit = async (t: TabKey, tool: string, params: Record<string, unknown>, fileIds: string[]) => {
    try {
      await submitTask(t, { provider: "audio", tool, params, file_ids: fileIds });
    } catch (e) {
      toast({ tone: "error", title: "提交失败", description: (e as Error).message });
    }
  };

  return (
    <>
      <PageHeader
        icon={<AudioLines size={18} strokeWidth={1.75} />}
        title="音频剪辑"
        description="本地 ffmpeg 剪辑八件套：切割 / 合并 / 变调变速 / 调与 BPM 查询 / 均衡器 / 音量响度 / 淡入淡出 / 倒放。"
      />

      <Card>
        <CardHeader title="工具" icon={<SlidersHorizontal size={15} strokeWidth={1.75} />} />
        <CardBody className="space-y-5">
          <Tabs<TabKey> items={TABS} value={tab} onChange={setTab} className="max-w-full overflow-x-auto" />

          {/* 各 Tab 常驻挂载（hidden 切换），保留各自参数与结果 */}
          <div className={tab === "join" ? "hidden" : ""}>
            <SourcePicker src={src} uploading={uploading} error={srcErr} onPick={pickFile} needWave={tab === "cut"} />
          </div>

          <div className={tab === "cut" ? "" : "hidden"}>
            {src && <CutForm src={src} disabled={running("cut")} task={tasks.cut} onSubmit={(p) => submit("cut", "trim", p, [src.fileId])} />}
          </div>
          <div className={tab === "join" ? "" : "hidden"}>
            <JoinForm disabled={running("join")} task={tasks.join} onSubmit={(p, ids) => submit("join", "merge", p, ids)} />
          </div>
          <div className={tab === "pitch" ? "" : "hidden"}>
            {src && <PitchForm disabled={running("pitch")} task={tasks.pitch} analyze={tasks.analyze} onSubmit={(p) => submit("pitch", "pitch", p, [src.fileId])} onAnalyze={() => submit("analyze", "analyze", {}, [src.fileId])} />}
          </div>
          <div className={tab === "analyze" ? "" : "hidden"}>
            {src && <AnalyzeForm disabled={running("analyze")} task={tasks.analyze} onSubmit={() => submit("analyze", "analyze", {}, [src.fileId])} />}
          </div>
          <div className={tab === "eq" ? "" : "hidden"}>
            {src && <EqForm disabled={running("eq")} task={tasks.eq} onSubmit={(p) => submit("eq", "equalizer", p, [src.fileId])} />}
          </div>
          <div className={tab === "volume" ? "" : "hidden"}>
            {src && <VolumeForm disabled={running("volume")} task={tasks.volume} onSubmit={(p) => submit("volume", "volume", p, [src.fileId])} />}
          </div>
          <div className={tab === "fade" ? "" : "hidden"}>
            {src && <FadeForm src={src} disabled={running("fade")} task={tasks.fade} onSubmit={(p) => submit("fade", "fade", p, [src.fileId])} />}
          </div>
          <div className={tab === "reverse" ? "" : "hidden"}>
            {src && <ReverseForm disabled={running("reverse")} task={tasks.reverse} onSubmit={(p) => submit("reverse", "reverse", p, [src.fileId])} />}
          </div>
        </CardBody>
      </Card>

    </>
  );
}

// ============================================================
// 源文件选择（共享）
// ============================================================

function SourcePicker({ src, uploading, error, onPick, needWave }: {
  src: SourceState | null;
  uploading: boolean;
  error: string;
  onPick: (f: File | null) => void;
  needWave: boolean;
}) {
  return (
    <div className="space-y-2">
      <FileDrop
        file={src?.file ?? null}
        onFile={onPick}
        accept={AUDIO_ACCEPT}
        emptyHint="mp3 / wav / flac / ogg / m4a / aac / wma / amr"
        label="选择音频文件"
        error={error}
      />
      {uploading && <p className="text-xs text-muted">正在上传文件…</p>}
      {src && !uploading && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
          <span className="text-fg-2">{src.file.name}</span>
          {src.duration > 0 && <span>时长 {formatTime(src.duration)}</span>}
          {needWave && src.peaksFailed && <span className="text-warn">浏览器无法解码该格式波形，仍可提交任务</span>}
        </div>
      )}
    </div>
  );
}

// ============================================================
// 输出格式共用字段
// ============================================================

function OutputFields({ format, bitrate, onChange }: {
  format: string;
  bitrate: string;
  onChange: (patch: { format?: string; bitrate?: string }) => void;
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field label="输出格式" aside="默认跟随源">
        {({ id, ...rest }) => (
          <Select id={id} value={format} onChange={(e) => onChange({ format: e.target.value })} {...rest}>
            {FORMAT_OPTS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </Select>
        )}
      </Field>
      <Field label="码率" aside="无损格式忽略">
        {({ id, ...rest }) => (
          <Select id={id} value={bitrate} onChange={(e) => onChange({ bitrate: e.target.value })} {...rest}>
            {BITRATE_OPTS.map((b) => (
              <option key={b} value={b}>{b}</option>
            ))}
          </Select>
        )}
      </Field>
    </div>
  );
}

// ============================================================
// 切割器：波形 + 选区
// ============================================================

interface Selection { start: number; end: number }

function CutForm({ src, disabled, task, onSubmit }: {
  src: SourceState;
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
}) {
  const dur = src.duration;
  const [sel, setSel] = useState<Selection>({ start: 0, end: dur });
  const [cutout, setCutout] = useState(false);
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");

  useEffect(() => {
    setSel({ start: 0, end: src.duration });
  }, [src]);

  const submit = () => {
    const start = Math.max(0, Math.min(sel.start, sel.end));
    const end = Math.max(sel.start, sel.end);
    onSubmit({
      start: Math.round(start * 1000) / 1000,
      end: end >= dur - 0.01 && !cutout ? 0 : Math.round(end * 1000) / 1000,
      cutout, format, bitrate,
    });
  };

  const len = Math.max(0, Math.min(sel.end, dur) - sel.start);

  return (
    <div className="space-y-4">
      <WaveEditor src={src} sel={sel} onSelChange={setSel} />

      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="起点（秒）" aside="可拖波形手柄">
          {({ id }) => (
            <Input
              id={id}
              value={formatTime(sel.start)}
              onChange={(e) => {
                const v = parseTimeInput(e.target.value);
                if (!Number.isNaN(v)) setSel((s) => ({ ...s, start: Math.max(0, Math.min(v, dur)) }));
              }}
            />
          )}
        </Field>
        <Field label="终点（秒）">
          {({ id }) => (
            <Input
              id={id}
              value={formatTime(Math.min(sel.end, dur))}
              onChange={(e) => {
                const v = parseTimeInput(e.target.value);
                if (!Number.isNaN(v)) setSel((s) => ({ ...s, end: Math.min(dur, Math.max(0, v)) }));
              }}
            />
          )}
        </Field>
        <Field label="选区时长">
          {({ id }) => <Input id={id} value={formatTime(len)} disabled readOnly />}
        </Field>
      </div>

      <label className="flex cursor-pointer items-center gap-2 text-xs text-fg-2">
        <input type="checkbox" checked={cutout} onChange={(e) => setCutout(e.target.checked)} className="accent-[var(--accent)]" />
        挖除选区（删除选中段，保留其余部分——去广告 / 去口误）
      </label>

      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />

      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={disabled} onClick={submit}>开始切割</Button>
        <span className="text-xs text-muted">{cutout ? "将删除选区内的部分" : `将保留选区（${formatTime(len)}）`}</span>
      </div>

      <TaskResult task={task} emptyHint="切割结果会显示在这里" />
    </div>
  );
}

/** 波形编辑器：canvas 峰值渲染 + 拖拽选区 + 播放头 + 选区试听。 */
function WaveEditor({ src, sel, onSelChange }: {
  src: SourceState;
  sel: Selection;
  onSelChange: (s: Selection) => void;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const rafRef = useRef(0);
  const dragRef = useRef<null | { mode: "start" | "end" | "move"; grabOffset: number; anchor: number; orig: Selection }>(null);
  const [playing, setPlaying] = useState(false);
  const [loop, setLoop] = useState(false);
  const [playhead, setPlayhead] = useState(0);
  const dur = src.duration || 0;

  // 选区试听：本地 audio 元素，不与全局播放器抢轨道
  const playSel = () => {
    if (!audioRef.current) audioRef.current = new Audio();
    const el = audioRef.current;
    el.src = src.url;
    el.currentTime = sel.start;
    el.play().then(() => setPlaying(true)).catch(() => undefined);
  };
  const stopSel = () => {
    audioRef.current?.pause();
    setPlaying(false);
    setPlayhead(0);
  };

  useEffect(() => {
    const el = audioRef.current;
    return () => {
      cancelAnimationFrame(rafRef.current);
      el?.pause();
    };
  }, []);

  // 播放推进：rAF 更新播放头，越过选区终点按需暂停/循环
  useEffect(() => {
    if (!playing) return;
    const tick = () => {
      const el = audioRef.current;
      if (el) {
        if (el.currentTime >= sel.end - 0.01 || el.ended) {
          if (loop) {
            el.currentTime = sel.start;
          } else {
            el.pause();
            setPlaying(false);
            setPlayhead(0);
            return;
          }
        }
        setPlayhead(el.currentTime);
      }
      rafRef.current = requestAnimationFrame(tick);
    };
    rafRef.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(rafRef.current);
  }, [playing, sel.start, sel.end, loop]);

  // 绘制
  useEffect(() => {
    const canvas = canvasRef.current;
    const wrap = wrapRef.current;
    if (!canvas || !wrap) return;
    const draw = () => {
      const dpr = window.devicePixelRatio || 1;
      const w = wrap.clientWidth;
      const h = 116;
      canvas.width = Math.max(1, Math.round(w * dpr));
      canvas.height = Math.round(h * dpr);
      canvas.style.width = `${w}px`;
      canvas.style.height = `${h}px`;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);

      const css = getComputedStyle(canvas);
      const idle = css.getPropertyValue("--wave-idle").trim() || "rgba(240,234,222,.16)";
      const accent = css.getPropertyValue("--accent").trim() || "#22d3ee";
      const x0 = dur > 0 ? (Math.min(sel.start, dur) / dur) * w : 0;
      const x1 = dur > 0 ? (Math.min(sel.end, dur) / dur) * w : w;

      const peaks = src.peaks;
      const mid = h / 2;
      const barW = 2;
      const gap = 1;
      const n = Math.floor(w / (barW + gap));
      for (let i = 0; i < n; i++) {
        const x = i * (barW + gap);
        const p = peaks ? (peaks[Math.min(peaks.length - 1, Math.floor((i / n) * peaks.length))] ?? 0) : 0.04;
        const half = Math.max(1, p * (mid - 6));
        ctx.fillStyle = !peaks ? idle : x >= x0 && x <= x1 ? accent : idle;
        ctx.globalAlpha = !peaks ? 0.5 : 1;
        ctx.fillRect(x, mid - half, barW, half * 2);
      }
      ctx.globalAlpha = 1;

      if (peaks) {
        // 选区边界手柄
        ctx.fillStyle = accent;
        ctx.fillRect(x0 - 1, 0, 2, h);
        ctx.fillRect(x1 - 1, 0, 2, h);
        // 选区外遮罩
        ctx.fillStyle = "rgba(0,0,0,.35)";
        ctx.fillRect(0, 0, x0, h);
        ctx.fillRect(x1, 0, w - x1, h);
        // 播放头
        if (playhead > 0) {
          const px = (playhead / dur) * w;
          ctx.fillStyle = css.getPropertyValue("--meter").trim() || "#4cd487";
          ctx.fillRect(px - 0.5, 0, 1.5, h);
        }
      }
    };
    draw();
    const ro = new ResizeObserver(draw);
    ro.observe(wrap);
    return () => ro.disconnect();
  }, [src.peaks, sel, playhead, dur]);

  const timeAt = (clientX: number) => {
    const wrap = wrapRef.current;
    if (!wrap || dur <= 0) return 0;
    const rect = wrap.getBoundingClientRect();
    return Math.max(0, Math.min(dur, ((clientX - rect.left) / rect.width) * dur));
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (dur <= 0 || !src.peaks) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    const t = timeAt(e.clientX);
    const wrap = wrapRef.current;
    const pxPerSec = wrap ? wrap.clientWidth / dur : 0;
    const nearStart = Math.abs(t - sel.start) * pxPerSec < 7;
    const nearEnd = Math.abs(t - sel.end) * pxPerSec < 7;
    if (nearStart) dragRef.current = { mode: "start", grabOffset: 0, anchor: t, orig: sel };
    else if (nearEnd) dragRef.current = { mode: "end", grabOffset: 0, anchor: t, orig: sel };
    else if (t > sel.start && t < sel.end) dragRef.current = { mode: "move", grabOffset: t - sel.start, anchor: t, orig: sel };
    else dragRef.current = { mode: "end", grabOffset: 0, anchor: t, orig: { start: t, end: t } };
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag) return;
    const t = timeAt(e.clientX);
    if (drag.mode === "start") onSelChange({ ...sel, start: Math.min(t, sel.end) });
    else if (drag.mode === "end") onSelChange({ ...sel, end: Math.max(t, sel.start) });
    else {
      const len = drag.orig.end - drag.orig.start;
      const start = Math.max(0, Math.min(dur - len, t - drag.grabOffset));
      onSelChange({ start, end: start + len });
    }
  };

  const onPointerUp = () => {
    dragRef.current = null;
  };

  return (
    <div className="space-y-2">
      <div
        ref={wrapRef}
        className="relative touch-none select-none rounded-[var(--radius-sm)] border border-line bg-inset p-0"
        style={{ boxShadow: "var(--inset-shadow)" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        role="slider"
        aria-label="波形选区"
        aria-valuemin={0}
        aria-valuemax={Math.round(dur * 10) / 10}
        aria-valuenow={Math.round(sel.start * 10) / 10}
        tabIndex={0}
      >
        <canvas ref={canvasRef} className="block h-[116px] w-full cursor-ew-resize rounded-[var(--radius-sm)]" />
        {!src.peaks && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-xs text-muted">
            {src.peaksFailed ? "该格式无法在浏览器解码波形" : "波形加载中…"}
          </div>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant={playing ? "secondary" : "ghost"} onClick={playing ? stopSel : playSel} disabled={!src.peaks}>
          {playing ? <Pause size={13} strokeWidth={1.75} /> : <Play size={13} strokeWidth={1.75} />}
          {playing ? "停止" : "试听选区"}
        </Button>
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-fg-2">
          <input type="checkbox" checked={loop} onChange={(e) => setLoop(e.target.checked)} className="accent-[var(--accent)]" />
          循环
        </label>
        <span className="mx-1 text-line-strong">|</span>
        <Button size="sm" variant="ghost" onClick={() => onSelChange({ start: 0, end: dur })}>全选</Button>
        <Button size="sm" variant="ghost" onClick={() => onSelChange({ start: 0, end: dur / 2 })}>前半</Button>
        <Button size="sm" variant="ghost" onClick={() => onSelChange({ start: dur / 2, end: dur })}>后半</Button>
        <span className="ml-auto font-mono text-[11px] text-muted">
          {formatTime(sel.start)} → {formatTime(Math.min(sel.end, dur))}
        </span>
      </div>
    </div>
  );
}

// ============================================================
// 合并器
// ============================================================

interface JoinItem { file: File; fileId: string }

function JoinForm({ disabled, task, onSubmit }: {
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>, fileIds: string[]) => void;
}) {
  const { toast } = useToast();
  const [items, setItems] = useState<JoinItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [crossfade, setCrossfade] = useState("0");
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");

  const addFiles = async (fs: File[]) => {
    setBusy(true);
    try {
      for (const f of fs) {
        const fd = new FormData();
        fd.append("file", f);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        setItems((cur) => [...cur, { file: f, fileId: up.file_id }]);
      }
    } catch (e) {
      toast({ tone: "error", title: "上传失败", description: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };

  const move = (i: number, dir: -1 | 1) => {
    setItems((cur) => {
      const j = i + dir;
      if (j < 0 || j >= cur.length) return cur;
      const next = [...cur];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  };

  return (
    <div className="space-y-4">
      <FileDrop
        multiple
        files={[]}
        onFiles={(fs) => void addFiles(fs)}
        accept={AUDIO_ACCEPT}
        emptyHint="选择 2 个及以上音频，按顺序合并；可重复追加"
        label="添加要合并的音频文件"
      />
      {items.length > 0 && (
        <ol className="space-y-1.5">
          {items.map((it, i) => (
            <li key={`${it.fileId}`} className="flex items-center gap-2 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-xs">
              <span className="w-5 text-center font-mono text-muted">{i + 1}</span>
              <span className="min-w-0 flex-1 truncate text-fg-2">{it.file.name}</span>
              <button className="cursor-pointer text-muted hover:text-fg" onClick={() => move(i, -1)} aria-label="上移">↑</button>
              <button className="cursor-pointer text-muted hover:text-fg" onClick={() => move(i, 1)} aria-label="下移">↓</button>
              <button className="cursor-pointer text-muted hover:text-danger" onClick={() => setItems((cur) => cur.filter((_, j) => j !== i))} aria-label="移除">✕</button>
            </li>
          ))}
        </ol>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="交叉淡化（秒）" aside="0 = 直接拼接">
          {({ id }) => (
            <Input id={id} value={crossfade} onChange={(e) => setCrossfade(e.target.value)} placeholder="如 2：每个接缝 2 秒过渡" />
          )}
        </Field>
        <Field label="输出格式" aside="默认跟随第一个文件">
          {({ id, ...rest }) => (
            <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
              {FORMAT_OPTS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
            </Select>
          )}
        </Field>
        <Field label="码率">
          {({ id, ...rest }) => (
            <Select id={id} value={bitrate} onChange={(e) => setBitrate(e.target.value)} {...rest}>
              {BITRATE_OPTS.map((b) => (
              <option key={b} value={b}>{b}</option>
            ))}
            </Select>
          )}
        </Field>
      </div>
      <div className="flex items-center gap-3">
        <Button
          variant="primary"
          disabled={disabled || busy || items.length < 2}
          onClick={() =>
            onSubmit(
              { crossfade: Number(crossfade) || 0, format, bitrate },
              items.map((it) => it.fileId),
            )
          }
        >
          合并 {items.length >= 2 ? `${items.length} 个文件` : ""}
        </Button>
        {busy && <span className="text-xs text-muted">上传中…</span>}
      </div>
      <TaskResult task={task} emptyHint="合并结果会显示在这里" />
    </div>
  );
}

// ============================================================
// 变调变速
// ============================================================

const KEY_LABELS = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];

function PitchForm({ disabled, task, analyze, onSubmit, onAnalyze }: {
  disabled: boolean;
  task?: EditorTask;
  analyze?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
  onAnalyze: () => void;
}) {
  const [semitones, setSemitones] = useState(0);
  const [tempo, setTempo] = useState(1);
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");

  const ana = analyze?.task?.status === "succeeded" ? analyze.task.summary : null;
  const srcBpm = typeof ana?.bpm === "number" ? ana.bpm : 0;
  const srcKey = ana ? `${ana.key} ${ana.scale === "minor" ? "minor" : "major"}` : null;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-3 text-xs text-muted">
        {srcKey ? (
          <span className="rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2.5 py-1 text-fg-2">
            源：<span className="text-accent">{srcKey}</span>
            {srcBpm > 0 && <> · <span className="text-accent">{srcBpm} BPM</span></>}
          </span>
        ) : (
          <span>先识别源曲调与 BPM，滑条效果更直观</span>
        )}
        <Button size="sm" variant="ghost" disabled={disabled} onClick={onAnalyze}>查询调与 BPM</Button>
      </div>

      <div className="grid gap-5 sm:grid-cols-2">
        <div className="space-y-2">
          <div className="flex items-baseline justify-between">
            <span className="text-xs text-fg-2">乐调（音高）</span>
            <span className="font-mono text-sm text-accent">
              {semitones > 0 ? `+${semitones}` : semitones} 半音
              {srcKey && semitones !== 0 && typeof ana?.key === "string" && KEY_LABELS.includes(ana.key) && (
                <span className="ml-2 text-[11px] text-muted">
                  {ana.key} → {KEY_LABELS[(KEY_LABELS.indexOf(ana.key) + semitones + 72) % 12]}
                </span>
              )}
            </span>
          </div>
          <input
            type="range" min={-12} max={12} step={1} value={semitones}
            onChange={(e) => setSemitones(Number(e.target.value))}
            className="w-full accent-[var(--accent)]"
            aria-label="变调半音数"
          />
          <div className="flex justify-between font-mono text-[10px] text-muted"><span>-12</span><span>0</span><span>+12</span></div>
        </div>
        <div className="space-y-2">
          <div className="flex items-baseline justify-between">
            <span className="text-xs text-fg-2">BPM（节奏）</span>
            <span className="font-mono text-sm text-accent">
              ×{tempo.toFixed(2)}
              {srcBpm > 0 && tempo !== 1 && <span className="ml-2 text-[11px] text-muted">{srcBpm} → {Math.round(srcBpm * tempo * 10) / 10} BPM</span>}
            </span>
          </div>
          <input
            type="range" min={0.5} max={2} step={0.05} value={tempo}
            onChange={(e) => setTempo(Number(e.target.value))}
            className="w-full accent-[var(--accent)]"
            aria-label="速度倍率"
          />
          <div className="flex justify-between font-mono text-[10px] text-muted"><span>0.5×</span><span>1×</span><span>2×</span></div>
        </div>
      </div>

      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />

      <div className="flex items-center gap-3">
        <Button
          variant="primary"
          disabled={disabled || (semitones === 0 && tempo === 1)}
          onClick={() => onSubmit({ semitones, tempo, format, bitrate })}
        >
          应用变调变速
        </Button>
        {semitones === 0 && tempo === 1 && <span className="text-xs text-muted">拖动任一滑条后可提交</span>}
      </div>
      <TaskResult task={task} emptyHint="处理结果会显示在这里" />
    </div>
  );
}

// ============================================================
// 调与 BPM 查询
// ============================================================

function AnalyzeForm({ disabled, task, onSubmit }: {
  disabled: boolean;
  task?: EditorTask;
  onSubmit: () => void;
}) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={disabled} onClick={onSubmit}>开始分析</Button>
        <span className="text-xs text-muted">识别调（key）、音阶、Camelot 编码与 BPM，本地 DSP 数秒完成</span>
      </div>
      <AnalyzeResult task={task} />
      <TaskResult task={task} emptyHint="" />
    </div>
  );
}

function AnalyzeResult({ task }: { task?: EditorTask }) {
  if (!task || task.task?.status !== "succeeded") return null;
  const s = task.task.summary ?? {};
  const bpm = typeof s.bpm === "number" ? s.bpm : 0;
  const alts = Array.isArray(s.bpm_alts) ? (s.bpm_alts as number[]) : [];
  const score = typeof s.key_score === "number" ? s.key_score : 0;
  const conf = score > 0.85 ? "高" : score > 0.65 ? "中" : score > 0.4 ? "较低" : "低";
  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <div className="rounded-[var(--radius-md)] border border-line bg-raise-2 p-4">
        <div className="flex items-center gap-1.5 text-xs text-muted"><Gauge size={13} strokeWidth={1.75} /> BPM</div>
        <div className="mt-1 font-mono text-3xl text-accent">{bpm > 0 ? bpm : "—"}</div>
        <div className="mt-1 text-[11px] text-muted">
          {bpm > 0 ? <>备选 {alts.length > 0 ? alts.join(" / ") : "无"}</> : "未检测到明显节奏"}
        </div>
      </div>
      <div className="rounded-[var(--radius-md)] border border-line bg-raise-2 p-4">
        <div className="flex items-center gap-1.5 text-xs text-muted"><Music2 size={13} strokeWidth={1.75} /> 调（Key）</div>
        <div className="mt-1 font-mono text-3xl text-accent">{String(s.key ?? "—")} <span className="text-base">{s.scale === "minor" ? "minor" : "major"}</span></div>
        <div className="mt-1 text-[11px] text-muted">{s.scale === "minor" ? "小调" : "大调"} · 置信度{conf}（r={score.toFixed(2)}）</div>
      </div>
      <div className="rounded-[var(--radius-md)] border border-line bg-raise-2 p-4">
        <div className="flex items-center gap-1.5 text-xs text-muted"><Clock4 size={13} strokeWidth={1.75} /> Camelot</div>
        <div className="mt-1 font-mono text-3xl text-accent">{String(s.camelot ?? "—")}</div>
        <div className="mt-1 text-[11px] text-muted">
          {typeof s.duration_sec === "number" && <>时长 {formatTime(s.duration_sec)} · </>}
          {typeof s.sample_rate === "number" && <>{Math.round(s.sample_rate / 1000)} kHz</>}
        </div>
      </div>
    </div>
  );
}

// ============================================================
// 均衡器
// ============================================================

function EqForm({ disabled, task, onSubmit }: {
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
}) {
  const [preset, setPreset] = useState("flat");
  const [gains, setGains] = useState<number[]>(EQ_PRESETS.flat);
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");
  const dirty = gains.some((g, i) => g !== EQ_PRESETS[preset][i]);

  const applyPreset = (name: string) => {
    setPreset(name);
    setGains([...EQ_PRESETS[name]]);
  };
  const setGain = (i: number, v: number) => {
    const next = [...gains];
    next[i] = v;
    setGains(next);
    setPreset("custom");
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-1.5">
        {[...Object.keys(EQ_PRESETS), "custom"].map((name) => (
          <button
            key={name}
            onClick={() => name !== "custom" && applyPreset(name)}
            disabled={name === "custom" && !dirty}
            className={`cursor-pointer rounded-[5px] border px-2.5 py-1 text-xs transition-colors duration-150 ${
              preset === name
                ? "border-accent bg-accent/10 text-accent"
                : "border-line text-muted hover:text-fg-2 disabled:opacity-40"
            }`}
          >
            {name}
          </button>
        ))}
      </div>
      <div className="grid grid-cols-2 gap-x-5 gap-y-3 sm:grid-cols-5">
        {EQ_BANDS.map((f, i) => (
          <div key={f} className="space-y-1">
            <div className="flex items-baseline justify-between">
              <span className="font-mono text-[11px] text-muted">{f >= 1000 ? `${f / 1000}k` : f}</span>
              <span className="font-mono text-[11px] text-fg-2">{gains[i] > 0 ? `+${gains[i]}` : gains[i]}</span>
            </div>
            <input
              type="range" min={-12} max={12} step={1} value={gains[i]}
              onChange={(e) => setGain(i, Number(e.target.value))}
              className="w-full accent-[var(--accent)]"
              aria-label={`${f}Hz 增益`}
            />
          </div>
        ))}
      </div>
      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={disabled} onClick={() => onSubmit({ gains: gains.join(","), preset: "flat", format, bitrate })}>
          应用均衡
        </Button>
        <span className="text-xs text-muted">{gains.every((g) => g === 0) ? "全 0 段位 = 原样输出" : "自定义曲线"}</span>
      </div>
      <TaskResult task={task} emptyHint="均衡结果会显示在这里" />
    </div>
  );
}

// ============================================================
// 音量与响度
// ============================================================

function VolumeForm({ disabled, task, onSubmit }: {
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
}) {
  const [gain, setGain] = useState(0);
  const [normalize, setNormalize] = useState(false);
  const [lufs, setLufs] = useState("-14");
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <div className="flex items-baseline justify-between">
          <span className="text-xs text-fg-2">增益</span>
          <span className="font-mono text-sm text-accent">{gain > 0 ? `+${gain}` : gain} dB</span>
        </div>
        <input
          type="range" min={-30} max={12} step={1} value={gain}
          onChange={(e) => setGain(Number(e.target.value))}
          disabled={normalize}
          className="w-full accent-[var(--accent)] disabled:opacity-40"
          aria-label="增益 dB"
        />
      </div>
      <div className="flex flex-wrap items-center gap-4">
        <label className="flex cursor-pointer items-center gap-2 text-xs text-fg-2">
          <input type="checkbox" checked={normalize} onChange={(e) => setNormalize(e.target.checked)} className="accent-[var(--accent)]" />
          响度归一化（EBU R128，覆盖增益滑条）
        </label>
        {normalize && (
          <Select value={lufs} onChange={(e) => setLufs(e.target.value)} className="w-52">
            <option value="-14">-14 LUFS（流媒体）</option>
            <option value="-16">-16 LUFS（播客/语音）</option>
            <option value="-23">-23 LUFS（广播）</option>
          </Select>
        )}
      </div>
      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />
      <div className="flex items-center gap-3">
        <Button
          variant="primary"
          disabled={disabled || (!normalize && gain === 0)}
          onClick={() => onSubmit({ gain_db: gain, normalize, lufs: Number(lufs), format, bitrate })}
        >
          应用
        </Button>
        {!normalize && gain === 0 && <span className="text-xs text-muted">调整增益或勾选归一化</span>}
      </div>
      <TaskResult task={task} emptyHint="处理结果会显示在这里" />
    </div>
  );
}

// ============================================================
// 淡入淡出 / 倒放
// ============================================================

function FadeForm({ src, disabled, task, onSubmit }: {
  src: SourceState;
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
}) {
  const [fadeIn, setFadeIn] = useState("0");
  const [fadeOut, setFadeOut] = useState("3");
  const [curve, setCurve] = useState("linear");
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");
  const inV = Number(fadeIn) || 0;
  const outV = Number(fadeOut) || 0;

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="淡入（秒）" aside="0 = 不淡入">
          {({ id }) => <Input id={id} value={fadeIn} onChange={(e) => setFadeIn(e.target.value)} />}
        </Field>
        <Field label="淡出（秒）" aside={`总时长 ${formatTime(src.duration)}`}>
          {({ id }) => <Input id={id} value={fadeOut} onChange={(e) => setFadeOut(e.target.value)} />}
        </Field>
        <Field label="曲线">
          {({ id, ...rest }) => (
            <Select id={id} value={curve} onChange={(e) => setCurve(e.target.value)} {...rest}>
              <option value="linear">线性</option>
              <option value="log">对数（先快后慢）</option>
              <option value="exp">指数（先慢后快）</option>
            </Select>
          )}
        </Field>
      </div>
      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />
      <Button variant="primary" disabled={disabled || (inV === 0 && outV === 0)} onClick={() => onSubmit({ fade_in: inV, fade_out: outV, curve, format, bitrate })}>
        应用淡入淡出
      </Button>
      <TaskResult task={task} emptyHint="处理结果会显示在这里" />
    </div>
  );
}

function ReverseForm({ disabled, task, onSubmit }: {
  disabled: boolean;
  task?: EditorTask;
  onSubmit: (params: Record<string, unknown>) => void;
}) {
  const [format, setFormat] = useState("auto");
  const [bitrate, setBitrate] = useState("192k");
  return (
    <div className="space-y-4">
      <OutputFields format={format} bitrate={bitrate} onChange={(p) => { if (p.format) setFormat(p.format); if (p.bitrate) setBitrate(p.bitrate); }} />
      <Button variant="primary" disabled={disabled} onClick={() => onSubmit({ format, bitrate })}>整段倒放</Button>
      <TaskResult task={task} emptyHint="倒放结果会显示在这里" />
    </div>
  );
}

// ============================================================
// 任务结果面板
// ============================================================

function TaskResult({ task, emptyHint }: { task?: EditorTask; emptyHint: string }) {
  if (!task) {
    return emptyHint ? <p className="text-xs text-muted">{emptyHint}</p> : null;
  }
  const t = task.task;
  const st = t?.status ?? "pending";
  const running = st === "running" || st === "pending";
  return (
    <div className="space-y-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
      <div className="flex items-center gap-3">
        <StatusBadge status={st as TaskStatus} />
        {running && (
          <div className="min-w-0 flex-1">
            <ProgressBar value={t?.progress ?? 0} />
          </div>
        )}
        <span className="min-w-0 flex-1 truncate text-xs text-muted">{t?.progress_note ?? "已提交"}</span>
        {typeof t?.cost_ms === "number" && !running && <span className="font-mono text-[11px] text-muted">{(t.cost_ms / 1000).toFixed(1)}s</span>}
      </div>
      {st === "failed" && t?.error && <p className="text-xs text-danger">{t.error}</p>}
      {(task.artifacts ?? []).map((a) => (
        <ArtifactRow key={a.id} a={a} />
      ))}
      {st === "succeeded" && (task.artifacts ?? []).length === 0 && emptyHint && (
        <p className="flex items-center gap-1.5 text-xs text-muted"><Download size={12} /> 任务完成（无产物，见上方分析结果）</p>
      )}
    </div>
  );
}
