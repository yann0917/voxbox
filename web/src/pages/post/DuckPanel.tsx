import { useEffect, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { AudioLines, Download, MicVocal, Music, SlidersHorizontal, X } from "lucide-react";
import { apiBase, fetchJSON } from "../../lib/api";
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
  IconButton,
  ProgressBar,
  Select,
  StatusBadge,
  WaveLoader,
  WavePlayer,
  useToast,
} from "../../ui";

// 口播闪避（音频后期 · 口播闪避 Tab）：人声侧链压低 BGM，audio/duck 服务端导出。
// 输入归一复用 ClipPanel 的 ClipInput 模式：?vocal=<artifact_id>（TTS 产物行「去闪避」直入）
// 或本地上传（/api/uploads → file_id）；BGM 仅页内上传（裁定：本轮不做音乐搜索联动）。
// 页面形态对齐 MixerPanel：≥1024 两栏（输入 + 参数）、WS 进度卡（P0 SeparatePage 模式）、
// 成品行 WavePlayer + 下载。无实时预览（拍板）——页内不放试听引擎，导出后成品试听。

/** 输入归一：artifact=产物通道（?vocal= 直入），fileId=本地上传通道；name 仅供展示 */
type DuckInput = { kind: "artifact" | "fileId"; id: string; name?: string };

interface DuckTask {
  id: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: {
    depth?: number;
    bgm_gain?: number;
    loudness?: number;
    format?: string;
    vocal_rms?: number;
  };
}

interface DuckArtifact {
  id: string;
  kind: string;
  filename: string;
  format?: string;
  size?: number;
  duration_ms?: number;
}

/** 上传 accept：duck 是播客/口播场景，常见音频容器即可（格式合法性由工具侧报错） */
const AUDIO_ACCEPT = ".mp3,.wav,.m4a,.aac,.flac,.ogg";

/** 产物 id 的短展示（来源标签用，不带完整 UUID 噪声） */
const shortId = (id: string) => (id.length > 8 ? id.slice(0, 8) : id);

/**
 * 产物 → 上传通道转换。后端任务契约 artifact_inputs / file_ids 三通道互斥
 * （routes.go「只能提供其一」），双输入异源（?vocal= 产物 + 页内上传 BGM）时
 * 把产物侧拉流重传为 file_id，再统一走 file_ids 提交。产物流响应头带
 * Content-Disposition 文件名，落盘扩展名由此而来（解析失败回落 .mp3：
 * ffmpeg 输入按内容探测，扩展名只影响落盘命名，不影响处理）。
 */
async function artifactToFileId(id: string): Promise<string> {
  const resp = await fetch(`${apiBase}/api/artifacts/${id}/stream`);
  if (!resp.ok) throw new Error(`拉取产物流失败（HTTP ${resp.status}）`);
  const blob = await resp.blob();
  const m = /filename="([^"]+)"/.exec(resp.headers.get("Content-Disposition") ?? "");
  const name = m?.[1] ?? `vocal_${shortId(id)}.mp3`;
  const fd = new FormData();
  fd.append("file", blob, name);
  const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
  return up.file_id;
}

export default function DuckPanel() {
  const [searchParams] = useSearchParams();
  const qc = useQueryClient();
  const { toast } = useToast();

  // ---- 双输入（?vocal= 挂载时读一次；上传/清除后变更，两卡独立） ----
  const [vocal, setVocal] = useState<DuckInput | null>(null);
  const [bgm, setBgm] = useState<DuckInput | null>(null);
  const [uploadingVocal, setUploadingVocal] = useState(false);
  const [uploadingBgm, setUploadingBgm] = useState(false);

  useEffect(() => {
    const id = searchParams.get("vocal");
    if (id) setVocal({ kind: "artifact", id });
    // 只在挂载时读一次查询参数（同 ClipPanel 的跨页水合模式）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- 导出参数（默认=工具默认档：depth -12 / bgm_gain -6 / 响度开 / mp3） ----
  const [depth, setDepth] = useState(-12);
  const [bgmGain, setBgmGain] = useState(-6);
  const [loudOn, setLoudOn] = useState(true);
  const [format, setFormat] = useState("mp3");

  // ---- 导出任务（复用 MixerPanel「处理进度」卡模式：WS 驱动 + 终态拉详情） ----
  const [duckId, setDuckId] = useState<string | null>(null);
  const [duckTask, setDuckTask] = useState<DuckTask | null>(null);
  const [duckArts, setDuckArts] = useState<DuckArtifact[]>([]);
  const [duckBusy, setDuckBusy] = useState(false);
  const [busyNote, setBusyNote] = useState("");

  const ev = useTaskEvents();

  // WS 事件驱动导出进度；终态拉详情取产物行（同 MixerPanel 的任务进度卡模式）
  useEffect(() => {
    if (!ev || !duckId || ev.task_id !== duckId) return;
    if (ev.type === "progress") {
      setDuckTask((t) => ({
        ...(t ?? ({ id: duckId, status: "running" } as DuckTask)),
        progress: ev.progress ?? 0,
        progress_note: ev.note ?? "",
      }));
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<{ task: DuckTask; artifacts: DuckArtifact[] }>(`/api/tasks/${duckId}`)
        .then((d) => {
          setDuckTask(d.task);
          setDuckArts(d.artifacts);
        })
        .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
    }
  }, [ev, duckId, toast]);

  // 本地上传：POST /api/uploads 拿 file_id（不设 Content-Type 让浏览器带 multipart
  // boundary，ClipPanel 同款）；两卡共用一套流程，仅目标 setter 与忙态不同
  const uploadTo = async (
    f: File | null,
    inFlight: boolean,
    apply: (i: DuckInput) => void,
    busy: (b: boolean) => void,
  ) => {
    if (!f || inFlight) return;
    busy(true);
    try {
      const fd = new FormData();
      fd.append("file", f);
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      apply({ kind: "fileId", id: up.file_id, name: f.name });
    } catch (e) {
      toast({ tone: "error", title: "上传失败", description: (e as Error).message });
    } finally {
      busy(false);
    }
  };

  const submitDuck = () => {
    if (!bgm || !vocal || duckBusy) return;
    setDuckBusy(true);
    setDuckArts([]);
    // 异源归一：产物/上传两通道在输入侧可混（?vocal= 产物 + 上传 BGM），但任务契约
    // 互斥——先归一到同一通道再拼 body（同源零开销直通，异源多一次产物重传）。
    const normalized = (async (): Promise<[DuckInput, DuckInput]> => {
      if (bgm.kind === vocal.kind) return [bgm, vocal];
      setBusyNote("正在把产物输入转为上传通道…");
      const b: DuckInput = bgm.kind === "artifact" ? { kind: "fileId", id: await artifactToFileId(bgm.id) } : bgm;
      const v: DuckInput = vocal.kind === "artifact" ? { kind: "fileId", id: await artifactToFileId(vocal.id) } : vocal;
      return [b, v];
    })();
    // **通道序 [BGM, 人声] 是负载序**：后端按序映射 files.audio=BGM（被压主轨）/
    // files.audio2=人声（侧链控制信号），滤镜图 [0:a][1:a] 颠倒即压错方向（duck.go 输入序铁律）。
    normalized
      .then(([b, v]) => {
        setBusyNote("");
        const channel =
          b.kind === "artifact"
            ? { artifact_inputs: [b.id, v.id] }
            : { file_ids: [b.id, v.id] };
        return fetchJSON<{ task_id: string }>("/api/tasks", {
          method: "POST",
          body: JSON.stringify({
            provider: "audio",
            tool: "duck",
            params: {
              depth,
              bgm_gain: bgmGain,
              loudness: loudOn ? -14 : 0, // 0=关响度归一（后端 parseDuckOpts 语义）
              format,
            },
            ...channel,
          }),
        }).then((d) => {
          setDuckId(d.task_id);
          setDuckTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
          void qc.invalidateQueries({ queryKey: ["tasks"] });
        });
      })
      .catch((e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }))
      .finally(() => {
        setDuckBusy(false);
        setBusyNote("");
      });
  };

  const duckRunning = duckTask?.status === "running" || duckTask?.status === "pending";
  const readyToDuck = Boolean(bgm && vocal);

  /** 导出按钮下的引导文案：缺哪路补哪路（两路齐了给动作预告） */
  const exportHint = !readyToDuck
    ? !vocal && !bgm
      ? "先备齐两路输入：人声可从 TTS 产物行点「去闪避」带入，BGM 在左侧上传。"
      : !vocal
        ? "还差人声：上传口播音频，或从 TTS 产物行点「去闪避」进入本页。"
        : "还差 BGM：上传背景音乐/伴奏（本轮不支持从音乐搜索带入）。"
    : `导出 audio/duck 任务：人声开口时 BGM ${depth === 0 ? "直通不压" : `约压低 ${Math.abs(depth)} dB（实际随语音密度过冲，可再深 4~8dB）`}，完成后在下方「闪避成品」试听下载。`;

  return (
    <>
      <PanelIntro
        title="口播闪避"
        description="说话时 BGM 自动压低：人声侧链压缩 + 响度归一，导出服务端 audio/duck 任务"
        actions={
          (vocal || bgm) && (
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                setVocal(null);
                setBgm(null);
              }}
            >
              清空重选
            </Button>
          )
        }
      />

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左主区：双输入卡（人声=侧链+混入 / BGM=被压低的主轨） */}
        <div className="grid gap-4 sm:grid-cols-2">
          <Card>
            <CardHeader
              title="人声"
              icon={<MicVocal size={15} strokeWidth={1.75} />}
              aside={
                vocal ? (
                  <span className="micro">{vocal.kind === "artifact" ? `产物直入 ${shortId(vocal.id)}…` : "本地上传"}</span>
                ) : (
                  <span className="micro">未就绪</span>
                )
              }
            />
            <CardBody className="space-y-3">
              {vocal ? (
                <LoadedInput
                  input={vocal}
                  icon={<MicVocal size={14} strokeWidth={1.75} />}
                  label="清除人声"
                  onClear={() => setVocal(null)}
                />
              ) : (
                <FileDrop
                  onFile={(f) => void uploadTo(f, uploadingVocal, setVocal, setUploadingVocal)}
                  accept={AUDIO_ACCEPT}
                  emptyHint="口播 / 旁白音频；TTS 产物可从产物行「去闪避」直接带入"
                  label="上传人声文件"
                />
              )}
              {uploadingVocal && (
                <p className="flex items-center gap-2 text-xs text-muted">
                  <WaveLoader label="上传中" className="shrink-0" />
                  正在上传人声…
                </p>
              )}
              <p className="text-[11px] leading-relaxed text-muted">
                参与方式：侧链信号（人声开口触发 BGM 压低），本体经响度归一混入成品。
              </p>
            </CardBody>
          </Card>

          <Card>
            <CardHeader
              title="BGM"
              icon={<Music size={15} strokeWidth={1.75} />}
              aside={
                bgm ? (
                  <span className="micro">{bgm.kind === "artifact" ? `产物直入 ${shortId(bgm.id)}…` : "本地上传"}</span>
                ) : (
                  <span className="micro">未就绪</span>
                )
              }
            />
            <CardBody className="space-y-3">
              {bgm ? (
                <LoadedInput
                  input={bgm}
                  icon={<Music size={14} strokeWidth={1.75} />}
                  label="清除 BGM"
                  onClear={() => setBgm(null)}
                />
              ) : (
                <FileDrop
                  onFile={(f) => void uploadTo(f, uploadingBgm, setBgm, setUploadingBgm)}
                  accept={AUDIO_ACCEPT}
                  emptyHint="背景音乐 / 伴奏，时长建议不短于人声"
                  label="上传 BGM 文件"
                />
              )}
              {uploadingBgm && (
                <p className="flex items-center gap-2 text-xs text-muted">
                  <WaveLoader label="上传中" className="shrink-0" />
                  正在上传 BGM…
                </p>
              )}
              <p className="text-[11px] leading-relaxed text-muted">
                参与方式：主轨——人声开口时按深度压低，闭口后回弹；未被压低时按基线增益混入。
              </p>
            </CardBody>
          </Card>
        </div>

        {/* 右参数面板：深度 → BGM 基线 → 母带 → 导出（320px，<1024 落到下方） */}
        <Card>
          <CardHeader title="闪避参数" icon={<SlidersHorizontal size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-4">
            <Field
              label="闪避深度"
              aside={
                <span className="font-mono text-[11px] tabular-nums">{depth === 0 ? "不压直通" : `${depth} dB`}</span>
              }
              hint="0=直通不压；绝对值越大说话时 BGM 压得越低；实际压深随语音密度过冲，约再加深 4~8dB（宁深勿浅）"
            >
              {({ id, ...rest }) => (
                <input
                  id={id}
                  type="range"
                  min={-40}
                  max={0}
                  step={1}
                  value={depth}
                  onChange={(e) => setDepth(Number(e.target.value))}
                  className="w-full cursor-pointer accent-accent"
                  {...rest}
                />
              )}
            </Field>
            <Field
              label="BGM 基线增益"
              aside={<span className="font-mono text-[11px] tabular-nums">{bgmGain} dB</span>}
              hint="未被压低时的 BGM 音量，垫底常用 -6 dB"
            >
              {({ id, ...rest }) => (
                <input
                  id={id}
                  type="range"
                  min={-40}
                  max={6}
                  step={1}
                  value={bgmGain}
                  onChange={(e) => setBgmGain(Number(e.target.value))}
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
              响度归一（-14 LUFS）
            </label>
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
              loading={duckBusy}
              disabled={!readyToDuck}
              onClick={submitDuck}
              icon={<Download size={14} strokeWidth={1.75} />}
            >
              导出闪避成品
            </Button>
            <p className="text-[11px] leading-relaxed text-muted">{busyNote || exportHint}</p>
          </CardBody>
        </Card>
      </div>

      {duckTask && (
        <Card className="mt-4">
          <CardHeader
            title="导出进度"
            aside={
              <>
                {duckTask.cost_ms ? <span className="micro">{`${(duckTask.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={duckTask.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={duckTask.progress} active={duckRunning} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {duckRunning && <WaveLoader label="闪避处理中" className="shrink-0" />}
              <span className="min-w-0">{duckTask.progress_note || "—"}</span>
            </p>
            {duckTask.error && <p className="text-xs text-danger break-words">{duckTask.error}</p>}
            {duckTask.summary && (
              <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs">
                <span className="text-muted">
                  深度{" "}
                  <span className="font-mono text-fg-2">
                    {duckTask.summary.depth === 0 ? "直通" : `${duckTask.summary.depth ?? "—"} dB`}
                  </span>
                </span>
                <span className="text-muted">
                  BGM 基线 <span className="font-mono text-fg-2">{duckTask.summary.bgm_gain ?? "—"} dB</span>
                </span>
                <span className="text-muted">
                  响度{" "}
                  <span className="font-mono text-fg-2">
                    {duckTask.summary.loudness ? `${duckTask.summary.loudness} LUFS` : "关"}
                  </span>
                </span>
                <span className="text-muted">
                  人声 RMS <span className="font-mono text-fg-2">{duckTask.summary.vocal_rms ?? "—"} dB</span>
                </span>
                <span className="text-muted">
                  格式 <span className="font-mono uppercase text-fg-2">{duckTask.summary.format ?? "—"}</span>
                </span>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {duckArts.length > 0 && (
        <Card className="mt-4">
          <CardHeader
            title="闪避成品"
            icon={<AudioLines size={15} strokeWidth={1.75} />}
            aside={<span className="micro">{duckArts.length} 个</span>}
          />
          <CardBody className="space-y-2">
            {duckArts.map((a) => (
              <div
                key={a.id}
                className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3"
              >
                <span className="w-14 shrink-0 text-xs text-fg">成品</span>
                <WavePlayer
                  src={`${apiBase}/api/artifacts/${a.id}/stream`}
                  title={a.filename}
                  sub="闪避成品"
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

/** 已就绪输入行：来源标签 + 展示名 + 清除按钮（替换 FileDrop 的紧凑态） */
function LoadedInput({ input, icon, label, onClear }: { input: DuckInput; icon: ReactNode; label: string; onClear: () => void }) {
  return (
    <div className="flex items-center gap-2.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2.5">
      <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-raise text-accent">{icon}</span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-xs text-fg">{input.name ?? `产物 ${shortId(input.id)}…`}</p>
        <p className="text-[11px] text-muted">
          来源：{input.kind === "artifact" ? "产物直入（跨页带入）" : "本地上传"}
        </p>
      </div>
      <IconButton label={label} size="sm" onClick={onClear}>
        <X size={14} strokeWidth={1.75} />
      </IconButton>
    </div>
  );
}
