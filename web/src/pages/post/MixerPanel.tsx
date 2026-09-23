import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { AudioLines, Download, Info, Pause, Play, SlidersHorizontal } from "lucide-react";
import { apiBase, fetchJSON } from "../../lib/api";
import { formatTime, loadPeaks } from "../../lib/player";
import { MixerEngine, type EnvPoint } from "../../lib/mixer";
import { MixerTimeline } from "../../components/MixerTimeline";
import { useTaskEvents } from "../../lib/ws";
import type { TaskStatus } from "../../lib/types";
import { PanelIntro } from "./PanelIntro";
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
  WaveLoader,
  WavePlayer,
  useToast,
} from "../../ui";

// 混音台（音频后期 · 混音台 Tab，?task=<分离任务id> 直入）：以分离产物（人声+伴奏）为原料，
// 垫音/半消音/低切/音量包络实时预览后导出 audio/mix 任务。预览走 MixerEngine（后端导出链的
// 前端孪生，语义锚点 lib/mixer.ts）；导出走 POST /api/tasks（provider=audio, tool=mix），
// 「处理进度」卡与产物行复用 SeparatePage 的模式，保证两页体验一致。

interface MixTask {
  id: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: {
    vocal_gain?: number;
    vocal_highpass?: number;
    music_gain?: number;
    master_loudness?: number;
    envelope?: number;
  };
}

interface MixArtifact {
  id: string;
  kind: string;
  filename: string;
  format?: string;
  size?: number;
  duration_ms?: number;
  meta?: { track?: string; url?: string; cached?: boolean };
}

/** 预设（spec §7 精确值）：应用=写 vocal_gain + vocal_highpass 并清空包络回恒增益语义 */
const PRESETS = [
  { id: "instrumental", label: "纯伴奏", gain: -99, hp: 0 },
  { id: "dianyin", label: "垫音 20%", gain: -14, hp: 120 },
  { id: "half", label: "半消音", gain: -6, hp: 0 },
  { id: "original", label: "原曲", gain: 0, hp: 0 },
] as const;
type Preset = (typeof PRESETS)[number];

/** 配对：人声=vocals/voice/vocal，伴奏=instrumental/background；三轨任务提示 sfx 未参与 */
function pairTracks(arts: MixArtifact[]) {
  const vocal = arts.find((a) => ["vocals", "voice", "vocal"].includes(a.meta?.track ?? ""));
  const music = arts.find((a) => ["instrumental", "background"].includes(a.meta?.track ?? ""));
  return { vocal, music, sfx: arts.some((a) => a.meta?.track === "sfx") };
}

const streamURL = (id: string) => `${apiBase}/api/artifacts/${id}/stream`;

function formatSize(bytes?: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 轨道监听静音 chip：M 常用音频设备刻印，aria-pressed 表达开关态 */
function MuteChip({ label, muted, onToggle }: { label: string; muted: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={muted}
      title={`${label}监听${muted ? "取消静音" : "静音"}（仅监听，不影响导出参数）`}
      onClick={onToggle}
      className={`inline-flex cursor-pointer items-center gap-1.5 rounded-[var(--radius-sm)] border px-2 py-1 text-[11px] transition-colors duration-150 ${
        muted
          ? "border-accent bg-raise-2 text-accent"
          : "border-line text-muted hover:border-line-strong hover:text-fg-2"
      }`}
    >
      <span className="font-medium">{label}</span>
      <span className="font-mono font-medium">M</span>
    </button>
  );
}

export default function MixerPanel() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();

  // ---- 来源分离任务（?task= 挂载时读一次；产物只读，仅用于配对出双轨） ----
  const [sourceId, setSourceId] = useState<string | null>(null);
  const [sourceLoading, setSourceLoading] = useState(false);
  const [sourceError, setSourceError] = useState<string | null>(null);
  const [sourceArts, setSourceArts] = useState<MixArtifact[]>([]);

  // ---- 引擎与波形 ----
  const engineRef = useRef<MixerEngine | null>(null);
  const [ready, setReady] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [duration, setDuration] = useState(0);
  const [musicPeaks, setMusicPeaks] = useState<number[]>([]);
  const [vocalPeaks, setVocalPeaks] = useState<number[]>([]);

  // ---- 走带与监听 ----
  const [time, setTime] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [musicMuted, setMusicMuted] = useState(false);
  const [vocalMuted, setVocalMuted] = useState(false);

  // ---- 混音参数（初始=「垫音 20%」预设语义：-14dB + 低切 120Hz + 恒增益） ----
  const [vocalGain, setVocalGain] = useState(-14);
  const [hpOn, setHpOn] = useState(true);
  const [hpHz, setHpHz] = useState(120);
  const [musicGain, setMusicGain] = useState(0);
  const [loudOn, setLoudOn] = useState(true);
  const [env, setEnv] = useState<EnvPoint[]>([]);
  const [format, setFormat] = useState("mp3");

  // ---- 导出任务（复用 SeparatePage「处理进度」卡模式：WS 驱动 + 终态拉详情） ----
  const [exportId, setExportId] = useState<string | null>(null);
  const [exportTask, setExportTask] = useState<MixTask | null>(null);
  const [exportArts, setExportArts] = useState<MixArtifact[]>([]);
  const [exportBusy, setExportBusy] = useState(false);

  const ev = useTaskEvents();

  // 跨页进入水合：拉来源任务详情取产物（失败给 EmptyState，不在标题区炸 Toast 轰炸）
  useEffect(() => {
    const id = searchParams.get("task");
    if (!id) return;
    setSourceId(id);
    setSourceLoading(true);
    fetchJSON<{ task: MixTask; artifacts: MixArtifact[] }>(`/api/tasks/${id}`)
      .then((d) => setSourceArts(d.artifacts))
      .catch((e: Error) => setSourceError(e.message))
      .finally(() => setSourceLoading(false));
    // 只在挂载时读一次查询参数（同 SeparatePage 的跨页水合模式）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const { vocal, music, sfx } = useMemo(() => pairTracks(sourceArts), [sourceArts]);
  const musicId = music?.id ?? null;
  const vocalId = vocal?.id ?? null;
  const paired = music != null && vocal != null;

  // 面板参数镜像：引擎 load 完成时按当前面板值对齐（解码期间用户可能已动过面板）
  const paramsRef = useRef({ vocalGain, hpOn, hpHz, musicGain, env });
  paramsRef.current = { vocalGain, hpOn, hpHz, musicGain, env };

  // 引擎生命周期：双轨就绪 → MixerEngine.load（fetch+解码常驻）；换轨/卸载 → dispose
  // 释放两轨 AudioBuffer（4 分钟歌约 170MB 量级，spec §6.3）。
  useEffect(() => {
    if (!musicId || !vocalId) return;
    let dead = false;
    setReady(false);
    setLoadError(null);
    setPlaying(false);
    setTime(0);
    setDuration(0);
    MixerEngine.load(streamURL(musicId), streamURL(vocalId))
      .then((eng) => {
        if (dead) {
          eng.dispose(); // 卸载竞态：load 慢于 unmount，引擎建好即拆
          return;
        }
        const p = paramsRef.current;
        eng.setVocalGain(p.vocalGain);
        eng.setMusicGain(p.musicGain);
        eng.setVocalHighpass(p.hpOn ? p.hpHz : 0);
        eng.setEnvelope(p.env.length > 0 ? p.env : null);
        eng.onTick = setTime; // 80ms 节流由引擎保证
        engineRef.current = eng;
        setDuration(eng.duration);
        setReady(true);
      })
      .catch((e: Error) => {
        if (!dead) setLoadError(e.message); // 解码失败降级：不可预览仅导出（spec §9）
      });
    return () => {
      dead = true;
      engineRef.current?.dispose();
      engineRef.current = null;
    };
  }, [musicId, vocalId]);

  // 波形峰值：与引擎加载并行（loadPeaks 缓存，试听行复用同一份解码结果）
  useEffect(() => {
    if (!musicId || !vocalId) return;
    let dead = false;
    setMusicPeaks([]);
    setVocalPeaks([]);
    // 峰值与引擎解码同源同路：引擎成功时失败近乎不可能，静默降级为空道（时间线留白）
    loadPeaks(streamURL(musicId)).then((p) => {
      if (!dead) setMusicPeaks(p);
    }).catch(() => {});
    loadPeaks(streamURL(vocalId)).then((p) => {
      if (!dead) setVocalPeaks(p);
    }).catch(() => {});
    return () => {
      dead = true;
    };
  }, [musicId, vocalId]);

  /** 播放/暂停唯一入口：play() 在 ctx.resume() 失败（自动播放策略/dispose 竞态）时
   *  会 reject——必须接住并提示再点一次（Task 6 交接点 1）。 */
  const togglePlay = useCallback(() => {
    const eng = engineRef.current;
    if (!eng) return;
    if (eng.playing) {
      eng.pause();
      setPlaying(false);
    } else {
      eng.play()
        .then(() => setPlaying(true))
        .catch(() =>
          toast({ tone: "error", title: "无法开始播放", description: "浏览器阻止了自动播放，请再点一次" }),
        );
    }
  }, [toast]);

  // 空格播放/暂停（spec §6.2）：document 级监听 + 交互元素守卫——焦点在输入框/按钮/
  // 选择器上时空格保持原生语义（输入空格、激活按钮）。选 document 监听而非页面容器
  // tabIndex：点击页面空白不产生焦点，容器方案覆盖不到「点过波形后按空格」的主路径；
  // 时间线 wrap 是非交互 div，keydown 会冒泡到这里由本守卫接管。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.repeat) return; // 长按空格的连发事件不反复切换播放（T8）
      if (e.key !== " ") return;
      const el = document.activeElement;
      if (el instanceof HTMLElement) {
        const tag = el.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "BUTTON" || el.isContentEditable) {
          return;
        }
      }
      if (!engineRef.current) return;
      e.preventDefault(); // 接管空格：阻止页面滚动
      togglePlay();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [togglePlay]);

  const seek = (t: number) => {
    const eng = engineRef.current;
    if (!eng) return;
    eng.seek(t);
    setTime(t); // 暂停中引擎 onTick 已同步；播放中下一 tick 跟上（双保险消除跳动）
  };

  const applyMute = (track: "music" | "vocal", mute: boolean) => {
    if (track === "music") setMusicMuted(mute);
    else setVocalMuted(mute);
    engineRef.current?.setMute(track, mute); // 只动监听，不改导出参数语义
  };

  // 包络变更：状态存「原样」（拖拽中允许暂态乱序，pointerup 归一化由时间线负责）；
  // 引擎 setEnvelope 内部稳定排序后整层重铺，当前播放位置无缝衔接
  const applyEnv = (pts: EnvPoint[]) => {
    setEnv(pts);
    engineRef.current?.setEnvelope(pts.length > 0 ? pts : null);
  };

  // 预设：一次写齐 vocal_gain + 低切 + 清空包络（恒增益语义），引擎三连同步
  const applyPreset = (p: Preset) => {
    setVocalGain(p.gain);
    setHpOn(p.hp > 0);
    if (p.hp > 0) setHpHz(p.hp);
    setEnv([]);
    const eng = engineRef.current;
    if (eng) {
      eng.setVocalGain(p.gain);
      eng.setVocalHighpass(p.hp); // 0=关：引擎侧 5Hz 近旁路，导出侧才是真摘滤镜
      eng.setEnvelope(null);
    }
  };

  // 活跃预设反推：面板值恰好等于某预设且无包络时点亮（手调后自动熄灭）
  const activePreset = env.length === 0
    ? PRESETS.find((p) => p.gain === vocalGain && (p.hp > 0) === hpOn && (p.hp <= 0 || p.hp === hpHz))?.id
    : undefined;

  const submitExport = () => {
    if (!music || !vocal) return;
    setExportBusy(true);
    // artifact_inputs 顺序=[伴奏,人声]（后端按序映射 files.audio=伴奏 / files.audio2=人声）；
    // vocal_env 非空时覆盖恒增益语义，序列化为原生数组（后端 parseEnvParam 双通道之一）
    fetchJSON<{ task_id: string }>("/api/tasks", {
      method: "POST",
      body: JSON.stringify({
        provider: "audio",
        tool: "mix",
        params: {
          vocal_gain: vocalGain,
          vocal_highpass: hpOn ? hpHz : 0,
          music_gain: musicGain,
          master_loudness: loudOn ? -14 : 0,
          format,
          ...(env.length > 0 ? { vocal_env: env } : {}),
        },
        artifact_inputs: [music.id, vocal.id],
      }),
    })
      .then((d) => {
        setExportId(d.task_id);
        setExportTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
        setExportArts([]);
        void qc.invalidateQueries({ queryKey: ["tasks"] });
      })
      .catch((e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }))
      .finally(() => setExportBusy(false));
  };

  // WS 事件驱动导出进度；终态拉详情取混音产物（同 SeparatePage 的任务进度卡模式）
  useEffect(() => {
    if (!ev || !exportId || ev.task_id !== exportId) return;
    if (ev.type === "progress") {
      setExportTask((t) => ({
        ...(t ?? ({ id: exportId, status: "running" } as MixTask)),
        progress: ev.progress ?? 0,
        progress_note: ev.note ?? "",
      }));
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<{ task: MixTask; artifacts: MixArtifact[] }>(`/api/tasks/${exportId}`)
        .then((d) => {
          setExportTask(d.task);
          setExportArts(d.artifacts);
        })
        .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
    }
  }, [ev, exportId, toast]);

  const exportRunning = exportTask?.status === "running" || exportTask?.status === "pending";

  return (
    <>
      <PanelIntro
        title="混音台"
        description="分离产物再加工：垫音 / 半消音 / 低切 / 音量包络实时预览，导出服务端 audio/mix 任务"
      />

      {!sourceId && (
        <Card>
          <CardBody>
            <EmptyState
              icon={<SlidersHorizontal size={18} strokeWidth={1.75} />}
              title="从分离任务进入混音台"
              description="混音台以分离产物为原料：先在人声分离页得到人声与伴奏两轨，再从产物行点「混音」进入，或在历史页分离任务详情点「进混音台」。"
              action={
                <Button variant="primary" onClick={() => navigate("/separate")}>去人声分离</Button>
              }
            />
          </CardBody>
        </Card>
      )}

      {sourceId && sourceLoading && (
        <div className="mt-4" aria-busy="true" aria-label="任务详情加载中">
          <Card>
            <CardHeader title="双轨时间线" icon={<AudioLines size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-3">
              <Skeleton className="h-44 w-full" />
              <Skeleton className="h-8 w-72 max-w-full" />
            </CardBody>
          </Card>
        </div>
      )}

      {sourceId && !sourceLoading && sourceError && (
        <Card className="mt-4">
          <CardBody>
            <EmptyState
              icon={<SlidersHorizontal size={18} strokeWidth={1.75} />}
              title="任务详情获取失败"
              description={sourceError}
              action={<Button variant="primary" onClick={() => navigate("/separate")}>去人声分离</Button>}
            />
          </CardBody>
        </Card>
      )}

      {sourceId && !sourceLoading && !sourceError && sourceArts.length === 0 && (
        <Card className="mt-4">
          <CardBody>
            <EmptyState
              icon={<AudioLines size={18} strokeWidth={1.75} />}
              title="该任务还没有产物"
              description="分离任务尚未完成或产物已被清理；等分离完成后再从产物行进入混音台。"
              action={<Button variant="primary" onClick={() => navigate("/separate")}>去人声分离</Button>}
            />
          </CardBody>
        </Card>
      )}

      {sourceArts.length > 0 && !paired && (
        <Card className="mt-4">
          <CardBody>
            <EmptyState
              icon={<SlidersHorizontal size={18} strokeWidth={1.75} />}
              title="该任务缺少可配对的音轨"
              description="混音台需要同一任务的「人声 + 伴奏/背景音」两轨产物（按轨道标签自动配对）；只提取单轨的任务无法混音，可重新分离选择双轨。"
              action={<Button variant="primary" onClick={() => navigate("/separate")}>去人声分离</Button>}
            />
          </CardBody>
        </Card>
      )}

      {paired && (
        <div className="mt-4 grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
          {/* 左主区：双轨时间线 + 走带控制（页内自带，不走全局 MiniPlayerBar） */}
          <Card>
            <CardHeader
              title="双轨时间线"
              icon={<AudioLines size={15} strokeWidth={1.75} />}
              aside={<span className="micro">上伴奏 · 下人声</span>}
            />
            <CardBody className="space-y-3">
              {sfx && (
                <p className="flex items-center gap-1.5 text-xs text-muted">
                  <Info size={13} strokeWidth={1.75} className="shrink-0" />
                  三轨任务：音效轨未参与混音，仅人声与伴奏进入混音链
                </p>
              )}
              {loadError ? (
                <div className="rounded-[var(--radius-sm)] border border-warn/40 bg-warn/10 px-3 py-2 text-xs">
                  <p className="text-fg-2">双轨预览解码失败：{loadError}</p>
                  <p className="mt-1 text-muted">
                    仍可调参并导出——导出由服务端 ffmpeg 完成，不依赖浏览器预览。
                  </p>
                </div>
              ) : !ready ? (
                <div className="space-y-2">
                  <Skeleton className="h-44 w-full" />
                  <p className="flex items-center gap-2 text-xs text-muted">
                    <WaveLoader label="正在解码双轨音频" className="shrink-0" />
                    正在拉取并解码双轨音频，歌曲越长等待越久…
                  </p>
                </div>
              ) : (
                <MixerTimeline
                  duration={duration}
                  musicPeaks={musicPeaks}
                  vocalPeaks={vocalPeaks}
                  env={env}
                  onEnvChange={applyEnv}
                  time={time}
                  playing={playing}
                  onSeek={seek}
                  vocalMuted={vocalMuted}
                />
              )}
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  size="sm"
                  variant="primary"
                  disabled={!ready}
                  onClick={togglePlay}
                  icon={playing ? <Pause size={14} strokeWidth={1.75} /> : <Play size={14} strokeWidth={1.75} />}
                >
                  {playing ? "暂停" : "播放"}
                </Button>
                <span className="font-mono text-xs tabular-nums text-fg-2">
                  {formatTime(time)} <span className="text-muted">/</span> {formatTime(duration)}
                </span>
                <div className="ml-auto flex items-center gap-1.5">
                  <MuteChip label="伴奏" muted={musicMuted} onToggle={() => applyMute("music", !musicMuted)} />
                  <MuteChip label="人声" muted={vocalMuted} onToggle={() => applyMute("vocal", !vocalMuted)} />
                </div>
              </div>
              <p className="text-[11px] text-muted">
                空白处单击定位播放头 · 双击包络道加点 · 拖点改时间/增益 · 选中后 Delete 删除 · 空格播放/暂停
              </p>
            </CardBody>
          </Card>

          {/* 右参数面板：预设 → 人声组 → 伴奏组 → 主链 → 导出（320px，<1024 落到下方） */}
          <Card>
            <CardHeader title="混音参数" icon={<SlidersHorizontal size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-5">
              <div className="space-y-2">
                <MicroLabel>预设</MicroLabel>
                <div role="radiogroup" aria-label="垫音预设" className="grid grid-cols-2 gap-2">
                  {PRESETS.map((p) => {
                    const active = activePreset === p.id;
                    return (
                      <button
                        key={p.id}
                        type="button"
                        role="radio"
                        aria-checked={active}
                        onClick={() => applyPreset(p)}
                        className={`cursor-pointer rounded-[var(--radius-sm)] border px-2.5 py-2 text-left text-xs transition-colors duration-150 ${
                          active
                            ? "border-accent bg-raise-2 text-accent"
                            : "border-line bg-raise text-fg-2 hover:border-line-strong hover:bg-raise-2 hover:text-fg"
                        }`}
                      >
                        {p.label}
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="space-y-3 border-t border-line pt-4">
                <Field
                  label="人声增益"
                  aside={<span className="font-mono text-[11px] tabular-nums">{vocalGain} dB</span>}
                  hint={
                    env.length > 0
                      ? "包络生效中：导出以包络为准，此滑杆在清除包络后生效"
                      : "-99 纯伴奏 ~ 0 原声 ~ +6 提升"
                  }
                >
                  {({ id, ...rest }) => (
                    <input
                      id={id}
                      type="range"
                      min={-99}
                      max={6}
                      step={1}
                      value={vocalGain}
                      onChange={(e) => {
                        const db = Number(e.target.value);
                        setVocalGain(db);
                        engineRef.current?.setVocalGain(db); // 有包络时引擎仅记录，清除包络后生效
                      }}
                      className="w-full cursor-pointer accent-accent"
                      {...rest}
                    />
                  )}
                </Field>
                <label className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                  <input
                    type="checkbox"
                    checked={hpOn}
                    onChange={(e) => {
                      setHpOn(e.target.checked);
                      engineRef.current?.setVocalHighpass(e.target.checked ? hpHz : 0);
                    }}
                    className="size-4 cursor-pointer accent-accent"
                  />
                  人声低切（去低频轰头）
                </label>
                {hpOn && (
                  <Field label="低切频率" aside={<span className="font-mono text-[11px] tabular-nums">{hpHz} Hz</span>} hint="垫音常用 120Hz，仅人声链生效">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        type="number"
                        min={20}
                        max={2000}
                        step={10}
                        value={hpHz}
                        onChange={(e) => {
                          const hz = Math.max(0, Math.min(2000, Math.round(Number(e.target.value) || 0)));
                          setHpHz(hz);
                          engineRef.current?.setVocalHighpass(hz);
                        }}
                        {...rest}
                      />
                    )}
                  </Field>
                )}
              </div>

              <Field
                label="伴奏增益"
                aside={<span className="font-mono text-[11px] tabular-nums">{musicGain} dB</span>}
                hint="实时预览生效；导出按当前值提交，开响度归一后以导出为准"
              >
                {({ id, ...rest }) => (
                  <input
                    id={id}
                    type="range"
                    min={-60}
                    max={12}
                    step={1}
                    value={musicGain}
                    onChange={(e) => {
                      const db = Number(e.target.value);
                      setMusicGain(db);
                      engineRef.current?.setMusicGain(db); // 实时预览（与导出同值）
                    }}
                    className="w-full cursor-pointer accent-accent"
                    {...rest}
                  />
                )}
              </Field>

              <label className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
                <input
                  type="checkbox"
                  checked={loudOn}
                  onChange={(e) => setLoudOn(e.target.checked)}
                  className="size-4 cursor-pointer accent-accent"
                />
                导出响度归一（-14 LUFS）
              </label>

              <div className="space-y-3 border-t border-line pt-4">
                <Field label="输出格式" hint="mp3 128k / wav 无损">
                  {({ id, ...rest }) => (
                    <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
                      <option value="mp3">MP3（128k）</option>
                      <option value="wav">WAV（无损）</option>
                    </Select>
                  )}
                </Field>
                <Button
                  variant="primary"
                  className="w-full"
                  loading={exportBusy}
                  disabled={!paired}
                  onClick={submitExport}
                  icon={<Download size={14} strokeWidth={1.75} />}
                >
                  导出混音
                </Button>
                <p className="text-[11px] leading-relaxed text-muted">
                  导出为服务端 audio/mix 任务：包络与推子按当前面板值提交，完成后在下方「混音结果」试听下载。
                </p>
              </div>
            </CardBody>
          </Card>
        </div>
      )}

      {exportTask && (
        <Card className="mt-4">
          <CardHeader
            title="处理进度"
            aside={
              <>
                {exportTask.cost_ms ? <span className="micro">{`${(exportTask.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={exportTask.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={exportTask.progress} active={exportRunning} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {exportRunning && <WaveLoader label="任务处理中" className="shrink-0" />}
              <span className="min-w-0">{exportTask.progress_note || "—"}</span>
            </p>
            {exportTask.error && <p className="text-xs text-danger break-words">{exportTask.error}</p>}
            {exportTask.summary && (
              <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs">
                <span className="text-muted">
                  人声 <span className="font-mono text-fg-2">{exportTask.summary.vocal_gain ?? "—"} dB</span>
                </span>
                <span className="text-muted">
                  低切{" "}
                  <span className="font-mono text-fg-2">
                    {exportTask.summary.vocal_highpass ? `${exportTask.summary.vocal_highpass} Hz` : "关"}
                  </span>
                </span>
                <span className="text-muted">
                  伴奏 <span className="font-mono text-fg-2">{exportTask.summary.music_gain ?? "—"} dB</span>
                </span>
                <span className="text-muted">
                  响度{" "}
                  <span className="font-mono text-fg-2">
                    {exportTask.summary.master_loudness ? `${exportTask.summary.master_loudness} LUFS` : "关"}
                  </span>
                </span>
                <span className="text-muted">
                  包络 <span className="font-mono text-fg-2">{exportTask.summary.envelope ?? 0} 点</span>
                </span>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {exportArts.length > 0 && (
        <Card className="mt-4">
          <CardHeader
            title="混音结果"
            icon={<AudioLines size={15} strokeWidth={1.75} />}
            aside={<span className="micro">{exportArts.length} 个</span>}
          />
          <CardBody className="space-y-2">
            {exportArts.map((a) => (
              <div
                key={a.id}
                className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3"
              >
                <span className="w-14 shrink-0 text-xs text-fg">混音</span>
                <WavePlayer
                  src={streamURL(a.id)}
                  title={a.filename}
                  sub="混音成品"
                  durationSec={a.duration_ms ? a.duration_ms / 1000 : undefined}
                  className="min-w-0 flex-1 basis-64"
                />
                <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">
                  {formatSize(a.size)}
                </span>
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
