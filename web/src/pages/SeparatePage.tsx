import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  AudioLines,
  ChevronDown,
  Copy,
  Download,
  ExternalLink,
  RefreshCw,
  SlidersHorizontal,
  SplitSquareHorizontal,
  Waves,
} from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import type { TaskStatus } from "../lib/types";
import { useStorageEnabled } from "../lib/useStorageEnabled";
import { useTaskEvents } from "../lib/ws";
import { FileDrop } from "../components/FileDrop";
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
  WaveLoader,
  WavePlayer,
  useToast,
} from "../ui";

// ---- 火山 MediaKit 引擎常量（保持原有行为） ----

interface SepTask {
  id: string;
  provider?: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms?: number;
  summary?: {
    scene?: string;
    algorithm?: string;
    duration_s?: number;
    tracks?: string[];
    model?: string;
    cached?: boolean;
  };
}
interface SepArtifact {
  id: string;
  kind: string;
  filename: string;
  format?: string;
  size?: number;
  duration_ms?: number;
  meta?: { track?: string; url?: string; cached?: boolean };
}

/** 场景与格式白名单与后端 SeparateTool 的 ParamSpecs 保持一致 */
const SCENES = [
  { value: "Audio", name: "通用", tracks: 2, desc: "通用音视频，输出人声与背景音" },
  { value: "Music", name: "音乐", tracks: 2, desc: "含背景音乐的素材，输出人声与背景音" },
  { value: "Drama", name: "短剧", tracks: 3, desc: "影视剧对白，输出人声 / 音乐 / 音效" },
  { value: "Narrate", name: "口播", tracks: 3, desc: "口播讲解，输出人声 / 音乐 / 音效" },
];
const FORMATS = ["mp3", "aac", "wav", "m4a", "flac"];
const TRACK_LABELS: Record<string, string> = {
  voice: "人声",
  vocals: "人声",
  vocal: "人声",
  background: "背景音",
  instrumental: "伴奏",
  instrum: "伴奏",
  music: "音乐",
  sfx: "音效",
  other: "其他",
};
const trackLabel = (t?: string) => (t ? TRACK_LABELS[t] ?? t : "音轨");
/** 人声类音轨（MediaKit voice / MVSep vocals 等）可送 ASR */
const isVoiceTrack = (t?: string) => !!t && ["voice", "vocals", "vocal"].includes(t);
/** 分离产物送 ASR 的格式门控：语音识别白名单（wav/mp3/ogg/pcm/spx/amr/aac/m4a）
 *  与分离输出格式（aac/mp3/wav/m4a/flac）的交集，flac 不支持 */
const asrCompatible = (fmt?: string) => !!fmt && ["mp3", "wav", "aac", "m4a"].includes(fmt);

/** 本地上传通道接受的音视频扩展名（MediaKit 按扩展名分流 audio_url/video_url） */
const SEP_ACCEPT = ".mp3,.wav,.flac,.m4a,.aac,.ogg,.mp4,.mov,.avi,.mkv,.webm";
const SEP_EXTS = ["mp3", "wav", "flac", "m4a", "aac", "ogg", "mp4", "mov", "avi", "mkv", "webm"];

/** MVSep 上传通道接受的音频扩展名（上游 create 接口白名单：MP3/WAV/FLAC/OGG/WEBM/M4A/AAC） */
const MVSEP_ACCEPT = ".mp3,.wav,.flac,.ogg,.webm,.m4a,.aac";
const MVSEP_EXTS = ["mp3", "wav", "flac", "ogg", "webm", "m4a", "aac"];

// ---- MVSep 类型（与后端 /api/mvsep/* 契约一致） ----

interface MVSepField {
  name: string;
  text: string;
  options?: Record<string, string>;
  default_key: string;
  required: boolean;
}
interface MVSepAlgorithm {
  sep_type: number;
  name: string;
  group_id: number;
  group_name: string;
  price_coefficient: number;
  orientation: number;
  is_active: boolean;
  description: string;
  fields: MVSepField[];
}
interface MVSepStatus {
  user?: { name: string; email: string };
  queue?: { plan: string; free_left: number; free_max: number; in_process: number } | null;
}
interface MVSepHistoryItem {
  id: number;
  hash: string;
  created_at: string;
  job_exists: boolean;
  algorithm: string;
}
interface MVSepRemoteResult {
  status: string;
  error?: string;
  result?: {
    algorithm: string;
    output_format: string;
    files: { name: string; link: string; size: number }[];
  };
}

const MVSEP_FORMATS = [
  { value: "0", label: "MP3（小体积）" },
  { value: "1", label: "WAV 16bit" },
  { value: "2", label: "WAV 24bit" },
  { value: "3", label: "WAV 32bit float" },
  { value: "4", label: "WAV 32bit" },
  { value: "5", label: "FLAC（无损）" },
];

function formatSize(bytes?: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export default function SeparatePage() {
  const [engine, setEngine] = useState<"mediakit" | "mvsep" | "gsgc" | "zhuanhuanmao">("gsgc");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [task, setTask] = useState<SepTask | null>(null);
  const [artifacts, setArtifacts] = useState<SepArtifact[]>([]);
  const [searchParams] = useSearchParams();

  const qc = useQueryClient();
  const navigate = useNavigate();
  const { toast } = useToast();
  const ev = useTaskEvents();

  // 复制上游公网直链：链接有时效（格式工厂 TOS 签名约 1 小时、火山 24 小时），仅作分享/极速下载用
  const copyURL = (u: string) =>
    navigator.clipboard
      .writeText(u)
      .then(() =>
        toast({
          tone: "ok",
          title: "公网直链已复制",
          description: "上游链接有时效（转换猫/格式工厂约 1 小时、火山 24 小时），请尽快使用",
        }),
      )
      .catch((e: Error) => toast({ tone: "error", title: "复制失败", description: e.message }));

  // 跨页跳转水合（如音乐搜索页提交分离后带 ?task= 进入）：拉任务详情恢复
  // 进度/产物视图，并按任务 provider 对齐引擎 Tab，后续 WS 事件照常驱动。
  useEffect(() => {
    const id = searchParams.get("task");
    if (!id) return;
    setTaskId(id);
    fetchJSON<{ task: SepTask; artifacts: SepArtifact[] }>(`/api/tasks/${id}`)
      .then((d) => {
        setTask(d.task);
        setArtifacts(d.artifacts);
        if (d.task.provider === "volcengine") setEngine("mediakit");
        if (d.task.provider === "mvsep") setEngine("mvsep");
        if (d.task.provider === "gsgc") setEngine("gsgc");
        if (d.task.provider === "zhuanhuanmao") setEngine("zhuanhuanmao");
      })
      .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
    // 只在挂载时读一次查询参数，避免任务运行中 URL 变更重复触发
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // WS 事件驱动进度；终态拉详情取每轨产物与 summary
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setTask((t) => ({
        ...(t ?? ({ id: taskId, status: "running" } as SepTask)),
        progress: ev.progress ?? 0,
        progress_note: ev.note ?? "",
      }));
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<{ task: SepTask; artifacts: SepArtifact[] }>(`/api/tasks/${taskId}`)
        .then((d) => {
          setTask(d.task);
          setArtifacts(d.artifacts);
        })
        .catch((e: Error) => toast({ tone: "error", title: "获取任务详情失败", description: e.message }));
    }
  }, [ev, taskId, toast]);

  const submitTask = (body: Record<string, unknown>) =>
    fetchJSON<{ task_id: string }>("/api/tasks", { method: "POST", body: JSON.stringify(body) })
      .then((d) => {
        setTaskId(d.task_id);
        setTask({ id: d.task_id, status: "pending", progress: 0, progress_note: "已提交" });
        setArtifacts([]);
        void qc.invalidateQueries({ queryKey: ["tasks"] });
      })
      .catch((e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }));

  const running = task?.status === "running" || task?.status === "pending";

  return (
    <>
      <PageHeader
        title="人声分离"
        description="音频源分离四引擎：格式工厂 / 转换猫（免费直连，同后端双线路）、MVSep（120+ 算法，每日 50 次免费）、火山 MediaKit（人声/背景，计费）"
      />

      <Card>
        <CardHeader title="输入与参数" icon={<Waves size={15} strokeWidth={1.75} />} />
        <CardBody className="space-y-5">
          <Tabs<"mediakit" | "mvsep" | "gsgc" | "zhuanhuanmao">
            items={[
              { value: "gsgc" as const, label: "格式工厂 · 免费" },
              { value: "zhuanhuanmao" as const, label: "转换猫 · 免费" },
              { value: "mvsep" as const, label: "MVSep · 120+ 算法" },
              { value: "mediakit" as const, label: "火山 MediaKit" },
            ]}
            value={engine}
            onChange={(v) => {
              setEngine(v);
              setTaskId(null);
              setTask(null);
              setArtifacts([]);
            }}
          />
          {/* 块级容器兜底：Tabs 是 inline-flex（行内级），两个 Tabs 直接相邻会拼到同一行——
              火山模式输入方式 Tabs 必须另起一行，与引擎选择保持上下两排 */}
          <div className="space-y-5">
            {engine === "mvsep" ? (
              <MVSepForm disabled={running} onSubmit={submitTask} />
            ) : engine === "gsgc" || engine === "zhuanhuanmao" ? (
              <CloudSepForm provider={engine} disabled={running} onSubmit={submitTask} />
            ) : (
              <MediaKitForm disabled={running} onSubmit={submitTask} />
            )}
          </div>
        </CardBody>
      </Card>

      {task && (
        <Card className="mt-4">
          <CardHeader
            title="处理进度"
            aside={
              <>
                {task.cost_ms ? <span className="micro">{`${(task.cost_ms / 1000).toFixed(1)}s`}</span> : null}
                <StatusBadge status={task.status} />
              </>
            }
          />
          <CardBody className="space-y-2.5">
            <ProgressBar value={task.progress} active={running} />
            <p className="flex items-center gap-2 text-xs text-muted">
              {running && <WaveLoader label="任务处理中" className="shrink-0" />}
              <span className="min-w-0">{task.progress_note || "—"}</span>
            </p>
            {task.error && <p className="text-xs text-danger break-words">{task.error}</p>}
            {task.summary && (
              <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs">
                {(engine === "gsgc" || engine === "zhuanhuanmao") && !task.summary.algorithm ? (
                  <span className="text-muted">
                    通道{" "}
                    <span className="font-mono text-fg-2">
                      {engine === "zhuanhuanmao" ? "转换猫" : "格式工厂"}
                      {task.summary.model ? ` · model ${task.summary.model}` : ""}
                      {task.summary.cached ? " · 缓存命中" : ""}
                    </span>
                  </span>
                ) : engine === "mvsep" || task.summary.algorithm ? (
                  <span className="text-muted">
                    算法 <span className="font-mono text-fg-2">{task.summary.algorithm ?? "—"}</span>
                  </span>
                ) : (
                  <span className="text-muted">
                    场景{" "}
                    <span className="font-mono text-fg-2">
                      {SCENES.find((s) => s.value === task.summary?.scene)?.name ?? task.summary.scene ?? "—"}
                    </span>
                  </span>
                )}
                {task.summary.duration_s != null && (
                  <span className="text-muted">
                    时长 <span className="font-mono text-fg-2">{task.summary.duration_s.toFixed(1)}s</span>
                  </span>
                )}
                <span className="text-muted">
                  轨道{" "}
                  <span className="font-mono text-fg-2">
                    {task.summary.tracks?.map(trackLabel).join(" / ") || "—"}
                  </span>
                </span>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {artifacts.length > 0 && (
        <Card className="mt-4">
          <CardHeader
            title="分离结果"
            icon={<Waves size={15} strokeWidth={1.75} />}
            aside={<span className="micro">{artifacts.length} 轨</span>}
          />
          <CardBody className="space-y-2">
            {artifacts.map((a) => {
              const track = a.meta?.track;
              return (
                <div
                  key={a.id}
                  className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3"
                >
                  <span className="w-14 shrink-0 text-xs text-fg">{trackLabel(track)}</span>
                  <WavePlayer
                    src={`${apiBase}/api/artifacts/${a.id}/stream`}
                    title={a.filename}
                    sub={trackLabel(track)}
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
                  <button
                    className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                    title="用两轨产物进混音台（垫音/半消音/包络）"
                    onClick={() => navigate(`/post?tab=mixer&task=${task!.id}`)}
                  >
                    <SlidersHorizontal size={13} strokeWidth={1.75} />
                    混音
                  </button>
                  {a.meta?.url && (
                    <button
                      className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                      title="复制上游公网直链（有时效，可直接分享/外部下载）"
                      onClick={() => copyURL(a.meta!.url!)}
                    >
                      <Copy size={13} strokeWidth={1.75} />
                      复制直链
                    </button>
                  )}
                  {isVoiceTrack(track) &&
                    (asrCompatible(a.format) ? (
                      <Button
                        size="sm"
                        onClick={() => navigate(`/asr?artifact=${a.id}`)}
                        className="shrink-0"
                      >
                        送 ASR 识别
                      </Button>
                    ) : (
                      <span
                        className="shrink-0 text-[11px] text-muted"
                        title="flac 格式语音识别暂不支持，可重新分离并选择 mp3 / wav / aac / m4a"
                      >
                        格式暂不支持识别
                      </span>
                    ))}
                </div>
              );
            })}
          </CardBody>
        </Card>
      )}

      {!task && engine === "mvsep" && <MVSepHistory className="mt-4" />}

      {!task && (engine === "mediakit" || engine === "gsgc" || engine === "zhuanhuanmao") && (
        <Card className="mt-4">
          <EmptyState
            icon={<Waves size={18} strokeWidth={1.75} />}
            title="还没有分离结果"
            description="填入公网音视频地址或上传本地文件、选择场景后点击「开始分离」，双轨或三轨音频会出现在这里，可试听、下载，人声轨可一键送语音识别。"
          />
        </Card>
      )}
    </>
  );
}

// ---- 火山 MediaKit 表单（原逻辑原样保留） ----

function MediaKitForm({
  disabled,
  onSubmit,
}: {
  disabled: boolean;
  onSubmit: (body: Record<string, unknown>) => Promise<void>;
}) {
  const [mode, setMode] = useState<"url" | "upload">("url");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState("");
  const [scene, setScene] = useState("Audio");
  const [format, setFormat] = useState("mp3");
  const { enabled: storageEnabled } = useStorageEnabled();

  const pickFile = (f: File | null) => {
    if (!f) {
      setFile(null);
      setFileError("");
      return;
    }
    const ext = f.name.split(".").pop()?.toLowerCase() ?? "";
    if (!SEP_EXTS.includes(ext)) {
      setFileError(`不支持的格式 .${ext || "未知"}：请选择常见音频/视频文件`);
      return;
    }
    setFileError("");
    setFile(f);
  };

  const submit = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {
        provider: "volcengine",
        tool: "separate",
        params: { url: mode === "url" ? url.trim() : "", scene, output_format: format },
      };
      if (mode === "upload") {
        // 本地上传：先拿 file_id，任务执行期由服务端转存对象存储换取签名 URL
        const fd = new FormData();
        fd.append("file", file!);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        body.file_ids = [up.file_id];
      }
      await onSubmit(body);
    },
  });

  const canSubmit = disabled ? false : mode === "url" ? !!url.trim() : !!file;

  return (
    <>
      <Tabs<"url" | "upload">
        items={
          storageEnabled
            ? [
                { value: "url" as const, label: "音视频 URL" },
                { value: "upload" as const, label: "本地上传" },
              ]
            : [{ value: "url" as const, label: "音视频 URL" }]
        }
        value={mode}
        onChange={setMode}
      />

      {mode === "upload" ? (
        <div className="space-y-2">
          <FileDrop
            file={file}
            onFile={pickFile}
            accept={SEP_ACCEPT}
            label="选择或拖入音视频文件"
            emptyHint="音频/视频均可；提交后自动经对象存储中转"
            error={fileError}
          />
          <p className="text-[11px] text-muted">
            提交时文件先上传到本服务，再转存对象存储取签名 URL 供 MediaKit 拉取；「处理进度」会显示转存状态。
          </p>
        </div>
      ) : (
        <Field
          label="音视频 URL"
          required
          hint="MediaKit 仅接受公网可访问地址；本地文件请切换到「本地上传」（需在设置页启用对象存储）。"
        >
          {({ id, ...rest }) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/media.mp4"
              {...rest}
            />
          )}
        </Field>
      )}

      <div className="space-y-2">
        <span className="micro">分离场景</span>
        <div role="radiogroup" aria-label="分离场景" className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
          {SCENES.map((s) => {
            const active = scene === s.value;
            return (
              <button
                key={s.value}
                role="radio"
                aria-checked={active}
                onClick={() => setScene(s.value)}
                className={`cursor-pointer rounded-[var(--radius-md)] border p-3 text-left transition-colors duration-150 ${
                  active
                    ? "border-accent bg-raise-2"
                    : "border-line bg-raise hover:border-line-strong hover:bg-raise-2"
                }`}
              >
                <div className="flex items-baseline justify-between gap-2">
                  <span className={`text-sm font-medium ${active ? "text-accent" : "text-fg"}`}>{s.name}</span>
                  <span className="font-mono text-[11px] text-muted">{s.tracks} 轨</span>
                </div>
                <p className="mt-1 text-[11px] leading-relaxed text-muted">{s.desc}</p>
              </button>
            );
          })}
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="输出格式" hint="默认 mp3；送识别支持 mp3 / wav / aac / m4a">
          {({ id, ...rest }) => (
            <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
              {FORMATS.map((f) => (
                <option key={f} value={f}>
                  {f.toUpperCase()}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <div className="flex items-end">
          <Button
            variant="primary"
            className="w-full"
            loading={submit.isPending}
            disabled={!canSubmit}
            onClick={() => submit.mutate()}
            icon={<SplitSquareHorizontal size={14} strokeWidth={1.75} />}
          >
            {disabled ? "分离中…" : "开始分离"}
          </Button>
        </div>
      </div>
      {submit.isError && (
        <p className="text-xs text-danger">{(submit.error as Error).message}</p>
      )}
    </>
  );
}

// ---- MVSep 表单：动态算法（分组下拉）+ 附加选项 + 云端额度 ----

function MVSepForm({
  disabled,
  onSubmit,
}: {
  disabled: boolean;
  onSubmit: (body: Record<string, unknown>) => Promise<void>;
}) {
  const { toast } = useToast();
  const navigate = useNavigate();
  const [mode, setMode] = useState<"url" | "upload">("url");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState("");
  const [sepType, setSepType] = useState("");
  const [addOpts, setAddOpts] = useState<Record<string, string>>({});
  const [format, setFormat] = useState("0");

  const status = useQuery({
    queryKey: ["mvsep-status"],
    queryFn: () => fetchJSON<MVSepStatus>("/api/mvsep/status"),
    retry: 0,
    staleTime: 60_000,
  });

  const algorithms = useQuery({
    queryKey: ["mvsep-algorithms"],
    queryFn: () => fetchJSON<MVSepAlgorithm[]>("/api/mvsep/algorithms"),
    staleTime: 10 * 60_000,
  });

  const algoList = algorithms.data ?? [];
  // 分组保序（上游已按组排序）：optgroup 一次给全，单框内滚动选择
  const groups: { name: string; algos: MVSepAlgorithm[] }[] = [];
  for (const a of algoList) {
    const g = groups[groups.length - 1];
    if (g && g.name === a.group_name) g.algos.push(a);
    else groups.push({ name: a.group_name || "其他", algos: [a] });
  }
  const algo = algoList.find((a) => String(a.sep_type) === sepType);

  // 切换算法：附加选项重置为该算法默认值（缺省不提交，留空走上游 default_key）
  const pickAlgo = (v: string) => {
    setSepType(v);
    const next = algoList.find((a) => String(a.sep_type) === v);
    const defaults: Record<string, string> = {};
    for (const f of next?.fields ?? []) {
      if (f.default_key !== "") defaults[f.name] = f.default_key;
    }
    setAddOpts(defaults);
  };

  const pickFile = (f: File | null) => {
    if (!f) {
      setFile(null);
      setFileError("");
      return;
    }
    const ext = f.name.split(".").pop()?.toLowerCase() ?? "";
    if (!MVSEP_EXTS.includes(ext)) {
      setFileError(`不支持的格式 .${ext || "未知"}：可选 ${MVSEP_ACCEPT.replaceAll(".", "")}`);
      return;
    }
    if (f.size > 50 * 1024 * 1024) {
      setFileError(`文件 ${formatSize(f.size)} 超过免费账号 50MB 上限，请压缩或转码后上传`);
      return;
    }
    setFileError("");
    setFile(f);
  };

  const submit = useMutation({
    mutationFn: async () => {
      const params: Record<string, unknown> = {
        url: mode === "url" ? url.trim() : "",
        sep_type: sepType,
        output_format: format,
      };
      for (const [k, v] of Object.entries(addOpts)) {
        if (v !== "") params[k] = v;
      }
      const body: Record<string, unknown> = { provider: "mvsep", tool: "separate", params };
      if (mode === "upload") {
        // MVSep 直收文件上传：本地文件由服务端 multipart 直传，无需对象存储中转
        const fd = new FormData();
        fd.append("file", file!);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        body.file_ids = [up.file_id];
      }
      await onSubmit(body);
    },
    onError: (e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }),
  });

  const canSubmit = disabled
    ? false
    : !!sepType && (mode === "url" ? !!url.trim() : !!file);

  return (
    <>
      {/* 账户与免费额度条 */}
      {status.isLoading ? (
        <Skeleton className="h-9 w-72" />
      ) : status.isError ? (
        <div className="flex flex-wrap items-center gap-2 rounded-[var(--radius-sm)] border border-warn/40 bg-warn/10 px-3 py-2 text-xs">
          <span className="text-fg-2">
            MVSep 未就绪：{(status.error as Error).message}
          </span>
          <Button size="sm" variant="secondary" onClick={() => navigate("/settings")}>
            去设置
          </Button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-xs text-muted">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <AudioLines size={13} strokeWidth={1.75} />
            {status.data?.user?.name ?? "MVSep"}
          </span>
          {status.data?.queue?.free_max ? (
            <span>
              今日免费分离余量{" "}
              <span className="font-mono text-fg-2">
                {status.data.queue.free_left}/{status.data.queue.free_max}
              </span>
            </span>
          ) : null}
          {status.data?.queue ? <span>云端处理中 {status.data.queue.in_process} 单</span> : null}
          <button
            className="inline-flex cursor-pointer items-center gap-1 text-muted transition-colors duration-150 hover:text-accent"
            onClick={() => status.refetch()}
          >
            <RefreshCw size={12} strokeWidth={1.75} />
            刷新
          </button>
        </div>
      )}

      <Tabs<"url" | "upload">
        items={[
          { value: "url" as const, label: "音频 URL" },
          { value: "upload" as const, label: "本地上传" },
        ]}
        value={mode}
        onChange={setMode}
      />

      {mode === "upload" ? (
        <div className="space-y-2">
          <FileDrop
            file={file}
            onFile={pickFile}
            accept={MVSEP_ACCEPT}
            label="选择或拖入音频文件"
            emptyHint="mp3 / wav / flac / ogg / m4a / aac；免费账号单文件 ≤ 50MB，服务端直传 MVSep"
            error={fileError}
          />
          <p className="text-[11px] text-muted">
            本地上传不依赖对象存储：提交时文件先传到本服务，再由服务端直传 MVSep。
          </p>
        </div>
      ) : (
        <Field label="音频 URL" required hint="公网可访问的音频直链；本服务会先下载再上传给 MVSep。">
          {({ id, ...rest }) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/song.mp3"
              {...rest}
            />
          )}
        </Field>
      )}

      <Field
        label="分离类型"
        required
        hint={
          algo
            ? undefined
            : "按分组选择算法；常用：人声/伴奏分离 → Ensemble 或 BS Roformer 系列"
        }
      >
        {({ id }) =>
          algorithms.isLoading ? (
            <Skeleton className="h-9 w-full" />
          ) : algorithms.isError ? (
            <div className="flex items-center gap-2 text-xs">
              <span className="text-danger">{(algorithms.error as Error).message}</span>
              <Button
                size="sm"
                variant="secondary"
                icon={<RefreshCw size={12} strokeWidth={1.75} />}
                onClick={() => algorithms.refetch()}
              >
                重试
              </Button>
            </div>
          ) : (
            <Select id={id} value={sepType} onChange={(e) => pickAlgo(e.target.value)} placeholder="选择分离类型">
              {groups.map((g) => (
                <optgroup key={g.name} label={g.name}>
                  {g.algos.map((a) => (
                    <option key={a.sep_type} value={String(a.sep_type)}>
                      {a.name}
                      {a.orientation > 0 ? "（高级）" : ""}
                    </option>
                  ))}
                </optgroup>
              ))}
            </Select>
          )
        }
      </Field>

      {algo && (
        <div className="space-y-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
          {algo.description && (
            <p className="line-clamp-2 text-[11px] leading-relaxed text-muted">{algo.description}</p>
          )}
          <div className="flex flex-wrap gap-2 text-[11px] text-muted">
            <span className="rounded-full border border-line px-2 py-0.5 font-mono">
              sep_type {algo.sep_type}
            </span>
            {algo.price_coefficient !== 1 && (
              <span className="rounded-full border border-line px-2 py-0.5">
                积分系数 ×{algo.price_coefficient}
              </span>
            )}
            {algo.orientation > 0 && (
              <span className="rounded-full border border-warn/50 bg-warn/10 px-2 py-0.5 text-warn">
                高级算法，可能消耗积分
              </span>
            )}
          </div>
          {algo.fields
            .filter((f) => f.options && Object.keys(f.options).length > 0)
            .map((f) => (
              <Field key={f.name} label={f.text || f.name} hint={f.required ? "必选" : undefined}>
                {({ id }) => (
                  <Select
                    id={id}
                    value={addOpts[f.name] ?? ""}
                    onChange={(e) => setAddOpts((m) => ({ ...m, [f.name]: e.target.value }))}
                  >
                    {Object.entries(f.options!).map(([k, label]) => (
                      <option key={k} value={k}>
                        {label}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            ))}
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="输出格式" hint="免费额度按次数计，与格式无关；送识别选 mp3 / wav">
          {({ id, ...rest }) => (
            <Select id={id} value={format} onChange={(e) => setFormat(e.target.value)} {...rest}>
              {MVSEP_FORMATS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <div className="flex items-end">
          <Button
            variant="primary"
            className="w-full"
            loading={submit.isPending}
            disabled={!canSubmit}
            onClick={() => submit.mutate()}
            icon={<SplitSquareHorizontal size={14} strokeWidth={1.75} />}
          >
            {disabled ? "分离中…" : "开始分离"}
          </Button>
        </div>
      </div>
    </>
  );
}

// ---- 云端直连表单（转换猫/格式工厂同后端双线路，免费无凭证） ----

const CLOUDSEP_ACCEPT = ".mp3,.wav,.flac,.ogg,.webm,.m4a,.aac";
const CLOUDSEP_EXTS = ["mp3", "wav", "flac", "ogg", "webm", "m4a", "aac"];
const CLOUDSEP_STEMS = [
  { value: "both", label: "人声 + 伴奏（双轨）" },
  { value: "vocals", label: "只提取人声" },
  { value: "instrumental", label: "只提取伴奏" },
];

const CLOUDSEP_LINES: Record<
  "gsgc" | "zhuanhuanmao",
  { name: string; host: string; mirror: string }
> = {
  gsgc: { name: "格式工厂", host: "z.pcgeshi.com", mirror: "转换猫" },
  zhuanhuanmao: { name: "转换猫", host: "www.zhuanhuanmao.com", mirror: "格式工厂" },
};

function CloudSepForm({
  provider,
  disabled,
  onSubmit,
}: {
  provider: "gsgc" | "zhuanhuanmao";
  disabled: boolean;
  onSubmit: (body: Record<string, unknown>) => Promise<void>;
}) {
  const { toast } = useToast();
  const [mode, setMode] = useState<"url" | "upload">("url");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState("");
  const [stems, setStems] = useState("both");
  const line = CLOUDSEP_LINES[provider];

  const pickFile = (f: File | null) => {
    if (!f) {
      setFile(null);
      setFileError("");
      return;
    }
    const ext = f.name.split(".").pop()?.toLowerCase() ?? "";
    if (!CLOUDSEP_EXTS.includes(ext)) {
      setFileError(`不支持的格式 .${ext || "未知"}：可选 ${CLOUDSEP_ACCEPT.replaceAll(".", "")}`);
      return;
    }
    if (f.size > 100 * 1024 * 1024) {
      setFileError(`文件 ${formatSize(f.size)} 超过 100MB，免费通道对大文件可能拒绝处理，请压缩后上传`);
      return;
    }
    setFileError("");
    setFile(f);
  };

  const submit = useMutation({
    mutationFn: async () => {
      const params: Record<string, unknown> = {
        url: mode === "url" ? url.trim() : "",
        stems,
      };
      const body: Record<string, unknown> = { provider, tool: "separate", params };
      if (mode === "upload") {
        // 与 MVSep 同款：本地文件先传到本服务，再由服务端直传上游对象存储
        const fd = new FormData();
        fd.append("file", file!);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        body.file_ids = [up.file_id];
      }
      await onSubmit(body);
    },
    onError: (e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }),
  });

  const canSubmit = disabled
    ? false
    : (mode === "url" ? !!url.trim() : !!file);

  return (
    <>
      <Tabs<"url" | "upload">
        items={[
          { value: "url" as const, label: "音频 URL" },
          { value: "upload" as const, label: "本地上传" },
        ]}
        value={mode}
        onChange={setMode}
      />

      {mode === "upload" ? (
        <div className="space-y-2">
          <FileDrop
            file={file}
            onFile={pickFile}
            accept={CLOUDSEP_ACCEPT}
            label="选择或拖入音频文件"
            emptyHint="mp3 / wav / flac / ogg / m4a / aac；服务端直传云端通道"
            error={fileError}
          />
          <p className="text-[11px] text-muted">
            本地上传不依赖对象存储：提交时文件先传到本服务，再直传到通道的对象存储。
          </p>
        </div>
      ) : (
        <Field label="音频 URL" required hint="公网可访问的音频直链；本服务会先下载再上传给云端通道。">
          {({ id, ...rest }) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/song.mp3"
              {...rest}
            />
          )}
        </Field>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="提取轨道">
          {({ id, ...rest }) => (
            <Select id={id} value={stems} onChange={(e) => setStems(e.target.value)} {...rest}>
              {CLOUDSEP_STEMS.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <div className="flex items-end">
          <Button
            variant="primary"
            className="w-full"
            loading={submit.isPending}
            disabled={!canSubmit}
            onClick={() => submit.mutate()}
            icon={<SplitSquareHorizontal size={14} strokeWidth={1.75} />}
          >
            {disabled ? "分离中…" : "开始分离"}
          </Button>
        </div>
      </div>

      <p className="text-[11px] text-muted">
        {line.name}线路（{line.host}，免费直连，{line.mirror}为同后端镜像线路、可切换互为备用），无需凭证与对象存储；
        产物自动转码为标准 MP3（128k）。通道为站点私有接口，作为免费备用通道，上游限流或改版时请切换 MVSep。
      </p>
    </>
  );
}

// ---- MVSep 云端历史：任务超时/清理后按 hash 补拉产物直链 ----

function MVSepHistory({ className }: { className?: string }) {
  const { toast } = useToast();
  const [openHash, setOpenHash] = useState<string | null>(null);
  const history = useQuery({
    queryKey: ["mvsep-history"],
    queryFn: () => fetchJSON<{ items: MVSepHistoryItem[] }>("/api/mvsep/history?start=0&limit=5"),
    retry: 0,
    staleTime: 30_000,
  });
  const probe = useMutation({
    mutationFn: (hash: string) =>
      fetchJSON<MVSepRemoteResult>(`/api/mvsep/separation?hash=${encodeURIComponent(hash)}`),
    onSuccess: (d) => {
      if (d.error) toast({ tone: "error", title: `任务 ${d.status}`, description: d.error });
    },
  });

  if (history.isLoading || history.isError || !history.data?.items?.length) return null;
  const items = history.data.items;

  return (
    <Card className={className}>
      <CardHeader
        title="MVSep 云端最近任务"
        icon={<ExternalLink size={15} strokeWidth={1.75} />}
        aside={<span className="micro">{items.length} 条</span>}
      />
      <CardBody className="space-y-2">
        <p className="text-[11px] text-muted">
          云端任务在 MVSep 侧独立保留；本服务任务超时或记录清理后，可在此按 hash 补拉结果直链。
        </p>
        {items.map((it) => {
          const open = openHash === it.hash;
          const probed = open && probe.data && probe.variables === it.hash ? probe.data : null;
          return (
            <div key={it.id} className="rounded-[var(--radius-sm)] border border-line bg-raise-2">
              <button
                className="flex w-full cursor-pointer items-center gap-3 p-2.5 text-left"
                onClick={() => {
                  const next = open ? null : it.hash;
                  setOpenHash(next);
                  if (next) probe.mutate(next);
                }}
              >
                <ChevronDown
                  size={14}
                  strokeWidth={1.75}
                  className={`shrink-0 text-muted transition-transform duration-200 ${open ? "rotate-180" : ""}`}
                />
                <span className="min-w-0 flex-1 truncate text-xs text-fg-2">
                  {it.algorithm || "未知算法"}
                  <span className="ml-2 font-mono text-[11px] text-muted">{it.created_at}</span>
                </span>
                <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">
                  {it.hash.slice(0, 20)}…
                </span>
              </button>
              {open && (
                <div className="border-t border-line px-3 py-2">
                  {probe.isPending && probe.variables === it.hash ? (
                    <Skeleton className="h-6 w-48" />
                  ) : probed ? (
                    probed.result ? (
                      <div className="space-y-1.5">
                        {probed.result.files.map((f) => (
                          <div key={f.link} className="flex items-center gap-3 text-xs">
                            <span className="min-w-0 flex-1 truncate text-fg-2">{f.name}</span>
                            <span className="shrink-0 font-mono text-[11px] text-muted">
                              {formatSize(f.size)}
                            </span>
                            <a
                              href={f.link}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex shrink-0 items-center gap-1 text-fg-2 transition-colors duration-150 hover:text-accent"
                            >
                              <Download size={13} strokeWidth={1.75} />
                              下载
                            </a>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <p className={`text-xs ${probed.error ? "text-danger" : "text-muted"}`}>
                        {probed.error ?? `状态：${probed.status}`}
                      </p>
                    )
                  ) : (
                    <p className="text-xs text-muted">点击行展开拉取状态…</p>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </CardBody>
    </Card>
  );
}
