import { useEffect, useRef, useState, type DragEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowUpRight,
  Captions,
  Download,
  FileAudio,
  FileText,
  Link2,
  Mic,
  RefreshCw,
  SlidersHorizontal,
  Square,
  Upload,
  X,
} from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import { formatTime } from "../lib/player";
import type { Artifact, TaskDetail, TaskStatus } from "../lib/types";
import { useProviderConfigured, useStorageEnabled } from "../lib/useStorageEnabled";
import { useTaskEvents } from "../lib/ws";
import { useTranscriptSync } from "../lib/useTranscriptSync";
import { recordingSupported, startRecording, type RecordingSession } from "../lib/recorder";
import { TranscriptList } from "../components/TranscriptList";
import { DictFill } from "../components/DictFill";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  IconButton,
  Input,
  PageHeader,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Tabs,
  type TabItem,
  WavePlayer,
  useToast,
} from "../ui";

/** 音频扩展名白名单（与后端一致）：一句话版与标准/闲时/极速版均为官方八种格式
    （bigmodel_nostream 文档 audio.format：wav/mp3/ogg/pcm/spx/amr/aac/m4a） */
const SENTENCE_EXTS = ["wav", "mp3", "ogg", "pcm", "spx", "amr", "aac", "m4a"];
const URL_VERSION_EXTS = ["wav", "mp3", "ogg", "spx", "amr", "aac", "m4a"];
const ACCEPT_ALL = [...new Set([...SENTENCE_EXTS, ...URL_VERSION_EXTS])]
  .map((e) => `.${e}`)
  .join(",");

type Mode = "upload" | "url" | "recording";
/** 识别版本：sentence 一句话识别（本地文件，单向流式大模型同步）；standard/idle/flash 录音文件识别（仅 URL） */
type ASRVersion = "sentence" | "standard" | "idle" | "flash";

/** URL 输入提示随版本变化（闲时/极速版 format 由 URL 扩展名推断，与后端 audioFormatFromURL 一致） */
const URL_HINT: Record<"standard" | "idle" | "flash", string> = {
  standard: "公网音频地址（wav/mp3/ogg/spx/amr/aac/m4a），异步转写",
  idle: "公网音频地址（wav/mp3/ogg/spx/amr/aac/m4a），最大 512MB / 5 小时",
  flash: "公网音频地址（wav/mp3/ogg/spx/amr/aac/m4a），最大 100MB / 2 小时",
};

/** 识别引擎：火山引擎（一句话/标准/闲时/极速） / 千问平台文件转写 / 小米 MiMo 同步转写 */
type Engine = "volcengine" | "qianwen" | "xiaomi";

/** 引擎页签：恒可点（TabItem 无 disabled），未配置凭证时切过去渲染设置引导卡（与 TTSPage 同款） */
const ENGINE_TABS: TabItem<Engine>[] = [
  { value: "volcengine", label: "火山引擎" },
  { value: "qianwen", label: "千问平台" },
  { value: "xiaomi", label: "小米 MiMo" },
];

/** 千问 filetrans 转写模型（与后端 ParamSpecs 枚举一致） */
const QW_MODELS = [
  { value: "qwen3-asr-flash-filetrans", label: "qwen3-asr-flash-filetrans（推荐）" },
  { value: "qwen-audio-3.1-asr-flash-filetrans", label: "qwen-audio-3.1-asr-flash-filetrans（说话人分离更强）" },
];

/** 千问识别语种词表（与后端 ParamSpecs 同词表）；含「自动识别」（空值 = 不限定语种） */
const QW_LANGUAGES: { value: string; label: string }[] = [
  { value: "", label: "自动识别" },
  { value: "zh", label: "中文" }, { value: "en", label: "英语" },
  { value: "ja", label: "日语" }, { value: "ko", label: "韩语" },
  { value: "de", label: "德语" }, { value: "fr", label: "法语" },
  { value: "ru", label: "俄语" }, { value: "es", label: "西班牙语" },
  { value: "pt", label: "葡萄牙语" }, { value: "it", label: "意大利语" },
];

/** 小米 mimo-v2.5-asr 识别语种（与后端 ParamSpecs 同词表） */
const MI_LANGUAGES: { value: string; label: string }[] = [
  { value: "", label: "自动识别" },
  { value: "zh", label: "中文" },
  { value: "en", label: "英语" },
];

/** 小米引擎白名单与载荷上限：官方仅收 mp3/wav，base64 后 ≤10MB（原始 7.5MB） */
const MI_EXTS = ["mp3", "wav"];
const MI_MAX_BYTES = 7.5 * 1024 * 1024;

type Segment = { text: string; start_ms: number; end_ms: number };

/** 录音文件识别支持语种（与后端 ParamSpecs 同词表）；留空 = 自动识别中文/英文及常见方言 */
const ASR_LANGUAGES: { value: string; label: string }[] = [
  { value: "zh-CN", label: "中文普通话" },
  { value: "en-US", label: "英语" },
  { value: "ja-JP", label: "日语" },
  { value: "id-ID", label: "印尼语" },
  { value: "es-MX", label: "西班牙语" },
  { value: "pt-BR", label: "葡萄牙语" },
  { value: "de-DE", label: "德语" },
  { value: "fr-FR", label: "法语" },
  { value: "ko-KR", label: "韩语" },
  { value: "fil-PH", label: "菲律宾语" },
  { value: "ms-MY", label: "马来语" },
  { value: "th-TH", label: "泰语" },
  { value: "ar-SA", label: "阿拉伯语" },
  { value: "it-IT", label: "意大利语" },
  { value: "bn-BD", label: "孟加拉语" },
  { value: "el-GR", label: "希腊语" },
  { value: "nl-NL", label: "荷兰语" },
  { value: "ru-RU", label: "俄语" },
  { value: "tr-TR", label: "土耳其语" },
  { value: "vi-VN", label: "越南语" },
  { value: "pl-PL", label: "波兰语" },
  { value: "ro-RO", label: "罗马尼亚语" },
  { value: "ne-NP", label: "尼泊尔语" },
  { value: "uk-UA", label: "乌克兰语" },
  { value: "yue-CN", label: "粤语" },
];

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO */
interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

function formatSize(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 非音频产物行：图标 + 文件名 + 大小 + 下载 */
function DownloadRow({ a }: { a: Artifact }) {
  const Icon = a.kind === "subtitle" ? Captions : FileText;
  return (
    <a
      href={`${apiBase}/api/artifacts/${a.id}/download`}
      className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3 transition-colors duration-150 hover:border-line-strong"
    >
      <span className="shrink-0 text-muted">
        <Icon size={16} strokeWidth={1.75} />
      </span>
      <span className="min-w-0 flex-1 truncate text-sm text-fg">{a.filename}</span>
      <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">{formatSize(a.size)}</span>
      <Download size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
    </a>
  );
}

export default function ASRPage() {
  /* 跨工具联动：/asr?artifact=<id>（来自人声分离页「送 ASR识别」）→ 跳过输入区，
     以 artifact_input 直接提交识别任务。 */
  const [searchParams] = useSearchParams();
  const artifactId = searchParams.get("artifact")?.trim() ?? "";
  const artifactMode = artifactId !== "";

  const [engine, setEngine] = useState<Engine>("volcengine");
  const [mode, setMode] = useState<Mode>("upload");
  const [version, setVersion] = useState<ASRVersion>("sentence");
  const [file, setFile] = useState<File | null>(null);
  const [dragging, setDragging] = useState(false);
  const [url, setUrl] = useState("");
  const [language, setLanguage] = useState("");
  const [hotwords, setHotwords] = useState("");
  const [diarization, setDiarization] = useState(false);
  const [qwenModel, setQwenModel] = useState("qwen3-asr-flash-filetrans");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [segments, setSegments] = useState<Segment[]>([]);
  const [playSrc, setPlaySrc] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState("");
  const [fileError, setFileError] = useState("");
  const [recState, setRecState] = useState<"idle" | "recording" | "processing">("idle");
  const [recError, setRecError] = useState("");
  const [recPreview, setRecPreview] = useState<string | null>(null);
  const [elapsedMs, setElapsedMs] = useState(0);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const blobRef = useRef<string | null>(null); // 上传回放的对象 URL（换任务时释放）
  const recSessionRef = useRef<RecordingSession | null>(null);
  const recStartRef = useRef(0);
  const recLevelRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();
  const { enabled: storageEnabled } = useStorageEnabled();
  // undefined = 设置未加载完成，与未配置同走引导卡（保守态，与 TTSPage 一致）
  const qianwenReady = useProviderConfigured("qianwen");
  const xiaomiReady = useProviderConfigured("xiaomi");
  // 火山无需凭证卡；千问/小米未配置（或未加载完）时切过去渲染设置引导卡
  const engineReady = engine === "qianwen" ? qianwenReady : engine === "xiaomi" ? xiaomiReady : true;

  /* 本地上传白名单：火山一句话版（WS 直发）mp3/wav/ogg/pcm；标准/闲时/极速与千问（对象存储
     中转，按 URL 扩展名推断格式）白名单一致 wav/mp3/ogg/spx/amr/aac/m4a；极速版另有 100MB 上限。 */
  const isXiaomi = engine === "xiaomi";
  const sentenceFile = engine === "volcengine" && version === "sentence";
  const allowedExts = isXiaomi ? MI_EXTS : sentenceFile ? SENTENCE_EXTS : URL_VERSION_EXTS;

  /* WS 事件驱动当前任务进度；终态拉详情拿产物与 summary.segments */
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
          setSegments(d.task.summary?.segments ?? []);
          if (d.task.status === "failed") {
            toast({ tone: "error", title: "识别失败", description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
          toast({ tone: "error", title: "读取任务结果失败", description: e.message });
        });
    }
  }, [ev, taskId, toast]);

  const artifacts = detail?.artifacts ?? [];
  const downloads = artifacts.filter((a) => a.kind !== "audio");
  const durationSec = detail?.task.summary?.duration_ms ? detail.task.summary.duration_ms / 1000 : undefined;

  const playTitle = artifactMode
    ? "人声轨（分离产物）"
    : mode === "url"
      ? "远程音频 URL"
      : file?.name ?? (mode === "recording" ? "麦克风录音" : "本地上传音频");
  const playSub = artifactMode
    ? `产物 ${artifactId.slice(0, 8)}`
    : mode === "url"
      ? url.trim() || undefined
      : file
        ? formatSize(file.size)
        : undefined;

  /** 切换识别版本：一句话版吃本地文件（上传/录音）；配置对象存储后标准/闲时/极速版
      也开放本地上传（自动中转）。版本间扩展名白名单不同，切换时清空已选文件避免带非法格式提交。 */
  const changeVersion = (v: ASRVersion) => {
    setVersion(v);
    setFileError("");
    setFile(null);
    if (v === "sentence") {
      setMode((m) => (m === "url" ? "upload" : m));
      return;
    }
    if (recState === "recording") discardRec();
    if (!storageEnabled) {
      setMode("url");
    }
    // 已配置存储：保持当前 upload/url 通道
  };

  /** 切换识别引擎：两页签恒可点（未配置千问时渲染设置引导卡）。跨引擎清空输入态与语言
      （两引擎词表不同：zh-CN vs zh）；千问无录音/一句话版通道，回落到当前可用通道。 */
  const changeEngine = (v: Engine) => {
    if (v === engine) return;
    if (recState === "recording") discardRec();
    setEngine(v);
    setFile(null);
    setUrl("");
    setFileError("");
    setSubmitError("");
    setLanguage("");
    if (v === "qianwen") {
      if (!storageEnabled) setMode("url");
      else setMode((m) => (m === "recording" ? "upload" : m));
    } else if (v === "xiaomi") {
      // 小米本地上传直读、URL 直下，两通道恒可用；录音 Tab 不提供，回落上传
      setMode((m) => (m === "recording" ? "upload" : m));
    } else if (version === "sentence") {
      // 回到火山：一句话版没有 URL 通道（与 changeVersion 的规则一致）
      setMode((m) => (m === "url" ? "upload" : m));
    }
  };

  const submit = useMutation({
    mutationFn: async () => {
      // params 按引擎分支：火山 = language/version/hotwords；千问 = filetrans 四参（语言空串 = 自动识别）
      let params: Record<string, unknown>;
      if (engine === "xiaomi") {
        // 小米同步转写：仅语种（空串 = 服务端自动识别），音频经 file_ids/artifact_input 通道进 Files
        params = { language: language.trim() };
      } else if (engine === "qianwen") {
        params = {
          srt: true,
          model: qwenModel,
          language_hints: language.trim(), // 留空 = 服务端不限定语种（自动识别）
          diarization_enabled: diarization, // 恒传 bool
        };
      } else {
        params = {
          srt: true,
          language: language.trim(), // 留空 = 服务端自动识别语种/方言
          // 联动产物（分离人声轨）是本地文件，固定走一句话识别
          version: artifactMode ? "sentence" : version,
        };
        if (hotwords.trim()) params.hotwords = hotwords.trim();
      }
      if (artifactMode) {
        // artifact_input 模式：产物由服务端解析为本地文件（Files["audio"]），两引擎通用
        // （千问侧经 EnsureURLInput 走对象存储中转）；params 不带 url/file_ids。
        return fetchJSON<{ task_id: string }>("/api/tasks", {
          method: "POST",
          body: JSON.stringify({ provider: engine, tool: "asr", params, artifact_input: artifactId }),
        });
      }
      if (mode === "url") {
        params.url = url.trim();
        return fetchJSON<{ task_id: string }>("/api/tasks", {
          method: "POST",
          body: JSON.stringify({ provider: engine, tool: "asr", params }),
        });
      }
      // 本地上传：先 POST /api/uploads 拿 file_id（不设 Content-Type，让浏览器带 multipart boundary），
      // 再建任务走 file_ids 本地文件通道。
      const fd = new FormData();
      fd.append("file", file!);
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: engine, tool: "asr", params, file_ids: [up.file_id] }),
      });
    },
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setSegments([]);
      setSubmitError("");
      if (artifactMode) {
        setPlaySrc(`${apiBase}/api/artifacts/${artifactId}/stream`);
      } else if (mode !== "url" && file) {
        if (blobRef.current) URL.revokeObjectURL(blobRef.current);
        blobRef.current = URL.createObjectURL(file);
        setPlaySrc(blobRef.current);
      } else {
        setPlaySrc(url.trim());
      }
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  /** 音频-文稿同步：当前句高亮 + 点击跳播（切轨时先切播放源再 seek）。 */
  const { activeIdx, seekTo } = useTranscriptSync(segments, playSrc);

  const pickFile = (f: File) => {
    const ext = f.name.split(".").pop()?.toLowerCase() ?? "";
    if (!allowedExts.includes(ext)) {
      setFileError(
        isXiaomi
          ? `不支持的格式 .${ext || "未知"}：小米引擎仅支持 mp3 / wav`
          : sentenceFile
            ? `不支持的格式 .${ext || "未知"}：仅支持 mp3 / wav / ogg / pcm`
            : `不支持的格式 .${ext || "未知"}：支持 wav / mp3 / ogg / spx / amr / aac / m4a`,
      );
      return;
    }
    if (engine === "volcengine" && version === "flash" && f.size > 100 * 1024 * 1024) {
      setFileError(`极速版仅支持 100MB 内音频（当前 ${(f.size / 1024 / 1024).toFixed(0)}MB）`);
      return;
    }
    if (isXiaomi && f.size > MI_MAX_BYTES) {
      setFileError(`小米引擎仅支持 7.5MB 内音频（当前 ${(f.size / 1024 / 1024).toFixed(1)}MB）`);
      return;
    }
    setFileError("");
    setFile(f);
  };

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragging(false);
    const f = e.dataTransfer.files?.[0];
    if (f) pickFile(f);
  };

  /* ---- 麦克风录音：采集完成后产出 16kHz WAV File，复用本地上传通道 ---- */
  const startRec = async () => {
    setRecError("");
    try {
      recSessionRef.current = await startRecording();
      recStartRef.current = Date.now();
      setElapsedMs(0);
      setRecState("recording");
    } catch (e) {
      const err = e as DOMException;
      setRecError(
        err?.name === "NotAllowedError"
          ? "麦克风权限被拒绝：请在浏览器地址栏允许麦克风访问后重试"
          : `无法启动录音：${err?.message ?? e}`,
      );
    }
  };

  const stopRec = async () => {
    const session = recSessionRef.current;
    if (!session) return;
    setRecState("processing");
    try {
      const file = await session.stop();
      pickFile(file); // 产出即 .wav，必过扩展名校验
      if (recPreview) URL.revokeObjectURL(recPreview);
      setRecPreview(URL.createObjectURL(file));
    } catch (e) {
      setRecError(`录音处理失败：${(e as Error).message}`);
    } finally {
      recSessionRef.current = null;
      setRecState("idle");
    }
  };

  const discardRec = () => {
    recSessionRef.current?.cancel();
    recSessionRef.current = null;
    if (recPreview) URL.revokeObjectURL(recPreview);
    setRecPreview(null);
    setFile(null);
    setRecState("idle");
  };

  /* 录音计时与电平条：计时走 state（低频），电平走 rAF 直改 DOM（不走 React 渲染） */
  useEffect(() => {
    if (recState !== "recording") return;
    const timer = window.setInterval(() => setElapsedMs(Date.now() - recStartRef.current), 250);
    return () => window.clearInterval(timer);
  }, [recState]);

  useEffect(() => {
    if (recState !== "recording") return;
    let raf = 0;
    const tick = () => {
      if (recLevelRef.current && recSessionRef.current) {
        recLevelRef.current.style.width = `${Math.round(recSessionRef.current.level() * 100)}%`;
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [recState]);

  const canSubmit = artifactMode || (mode === "url" ? url.trim() !== "" : file != null);
  // URL 提示：千问为文件转写限制；火山仅标准/闲时/极速版渲染（一句话版没有 URL 输入）
  const urlHint =
    engine === "qianwen"
      ? "公网音频地址，异步转写，最大 2GB / 12 小时"
      : isXiaomi
        ? "公网音频地址（mp3/wav），同步转写，音频 ≤7.5MB"
        : version === "sentence"
          ? undefined
          : URL_HINT[version];
  const seekTrack = { title: playTitle, sub: playSub };

  return (
    <>
      <PageHeader
        title="语音识别"
        description="多引擎音频转文字（火山 / 千问 / 小米），输出分句时间戳与 SRT 字幕"
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

      {/* 引擎页签：恒可点，未配置千问时切过去渲染引导卡（与 TTSPage 同款） */}
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <Tabs<Engine> items={ENGINE_TABS} value={engine} onChange={changeEngine} />
      </div>

      {/* 千问/小米未配置（或设置未加载完）：引导卡替代输入/参数/结果区 */}
      {!engineReady ? (
        <Card>
          <CardBody className="space-y-2">
            <p className="text-sm text-fg-2">尚未配置{engine === "qianwen" ? "千问平台" : "小米 MiMo"}凭证。</p>
            <Link to="/settings" className="text-xs text-accent hover:opacity-80">
              去设置页配置{engine === "qianwen" ? "千问" : "小米"} API Key →
            </Link>
          </CardBody>
        </Card>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
            {/* 左：音频输入（artifact 联动时为来源横幅） */}
            <Card className="min-w-0">
              <CardHeader
                title={artifactMode ? "输入来源" : "音频输入"}
                icon={<FileAudio size={15} strokeWidth={1.75} />}
                aside={<span className="micro">{artifactMode ? "artifact_input" : mode === "upload" ? "本地文件" : "公网 URL"}</span>}
              />
              <CardBody className="space-y-4">
                {artifactMode ? (
                  <div className="space-y-3">
                    <div className="flex flex-wrap items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                      <span className="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-sm)] border border-line text-accent">
                        <FileAudio size={14} strokeWidth={1.75} />
                      </span>
                      <span className="min-w-0 flex-1 text-xs text-fg-2">
                        使用人声分离产物{" "}
                        <span className="font-mono text-accent">{artifactId.slice(0, 8)}</span> 直接识别，无需再上传
                      </span>
                      <Link
                        to="/asr"
                        className="shrink-0 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                      >
                        改用其他音频
                      </Link>
                    </div>
                    <p className="text-[11px] text-muted">
                      提交时以 artifact_input 通道传入该产物，提交后可在下方试听源音轨。
                      {engine === "qianwen" && " 千问引擎会将本地产物经对象存储中转后转写。"}
                      {isXiaomi && " 小米引擎直接读取本地产物转写（mp3/wav）。"}
                    </p>
                  </div>
                ) : (
                  <>
                    {engine === "volcengine" ? (
                      <Field
                        label="识别版本"
                        hint={
                          version === "idle"
                            ? "闲时任务在服务端持续等待结果，可关闭页面，完成后在历史产物查看"
                            : version === "sentence"
                              ? "单向流式大模型的整段同步模式，本地文件秒级返回"
                              : undefined
                        }
                      >
                        {() => (
                          <Tabs<ASRVersion>
                            items={[
                              { value: "sentence", label: "一句话识别" },
                              { value: "standard", label: "标准版" },
                              { value: "idle", label: "闲时版" },
                              { value: "flash", label: "极速版" },
                            ]}
                            value={version}
                            onChange={changeVersion}
                          />
                        )}
                      </Field>
                    ) : isXiaomi ? (
                      <p className="text-xs text-muted">
                        mimo-v2.5-asr 同步转写：返回纯文本（无时间戳，不产 SRT）；本地文件直读，无需对象存储。
                      </p>
                    ) : (
                      <>
                        <Field label="转写模型" hint="qwen3 通用推荐；qwen-audio 说话人分离更强（≤2GB / 12 小时）">
                          {({ id, ...rest }) => (
                            <Select
                              id={id}
                              value={qwenModel}
                              onChange={(e) => setQwenModel(e.target.value)}
                              {...rest}
                            >
                              {QW_MODELS.map((m) => (
                                <option key={m.value} value={m.value}>
                                  {m.label}
                                </option>
                              ))}
                            </Select>
                          )}
                        </Field>
                        <label className="flex cursor-pointer items-start gap-2.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2">
                          <input
                            type="checkbox"
                            checked={diarization}
                            onChange={(e) => setDiarization(e.target.checked)}
                            className="mt-0.5 size-4 shrink-0 cursor-pointer accent-accent"
                          />
                          <span className="min-w-0 space-y-0.5">
                            <span className="block text-sm text-fg">说话人分离</span>
                            <span className="block text-[11px] text-muted">区分不同说话人（≤2h 且单声道音频）</span>
                          </span>
                        </label>
                      </>
                    )}

                    <Tabs<Mode>
                      items={
                        isXiaomi
                          ? [
                              { value: "url" as const, label: "音频 URL", icon: <Link2 size={13} strokeWidth={1.75} /> },
                              { value: "upload" as const, label: "本地上传", icon: <Upload size={13} strokeWidth={1.75} /> },
                            ]
                          : sentenceFile
                            ? [
                                { value: "upload" as const, label: "本地上传", icon: <Upload size={13} strokeWidth={1.75} /> },
                                { value: "recording" as const, label: "麦克风录音", icon: <Mic size={13} strokeWidth={1.75} /> },
                              ]
                            : !storageEnabled
                              ? [{ value: "url" as const, label: "音频 URL", icon: <Link2 size={13} strokeWidth={1.75} /> }]
                              : [
                                  { value: "url" as const, label: "音频 URL", icon: <Link2 size={13} strokeWidth={1.75} /> },
                                  { value: "upload" as const, label: "本地上传", icon: <Upload size={13} strokeWidth={1.75} /> },
                                ]
                      }
                      value={mode}
                      onChange={(m) => {
                        if (mode === "recording" && m !== "recording" && recState === "recording") discardRec();
                        setMode(m);
                        setFileError("");
                      }}
                    />

                    {mode === "upload" ? (
                      <div className="space-y-2">                    <div
                          role="button"
                          tabIndex={0}
                          aria-label="选择或拖入音频文件"
                          onClick={() => fileInputRef.current?.click()}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              fileInputRef.current?.click();
                            }
                          }}
                          onDragOver={(e) => {
                            e.preventDefault();
                            setDragging(true);
                          }}
                          onDragLeave={() => setDragging(false)}
                          onDrop={onDrop}
                          className={`flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[var(--radius-md)] border border-dashed px-4 py-8 text-center transition-colors duration-150 ${
                            dragging ? "border-accent bg-raise-2" : "border-line-strong bg-raise-2/40 hover:border-accent"
                          }`}
                        >
                          <span className={`flex size-9 items-center justify-center rounded-full border border-line bg-raise ${dragging ? "text-accent" : "text-muted"}`}>
                            <Upload size={16} strokeWidth={1.75} />
                          </span>
                          {file ? (
                            <>
                              <p className="max-w-full truncate text-sm text-fg">{file.name}</p>
                              <p className="font-mono text-[11px] tabular-nums text-muted">{formatSize(file.size)}</p>
                            </>
                          ) : (
                            <>
                              <p className="text-sm text-fg-2">拖拽音频到此处，或点击选择文件</p>
                              <p className="text-[11px] text-muted">
                                {sentenceFile
                                  ? "支持 wav / mp3 / ogg / pcm / spx / amr / aac / m4a"
                                  : isXiaomi
                                    ? "仅支持 mp3 / wav，7.5MB 内（base64 直传，无需对象存储）"
                                    : "支持 wav / mp3 / ogg / spx / amr / aac / m4a；提交后自动经对象存储中转"}
                              </p>
                            </>
                          )}
                          <input
                            ref={fileInputRef}
                            type="file"
                            accept={ACCEPT_ALL}
                            aria-label="选择要识别的音频文件"
                            className="hidden"
                            onChange={(e) => {
                              const f = e.target.files?.[0];
                              if (f) pickFile(f);
                              e.target.value = "";
                            }}
                          />
                        </div>
                        {file && (
                          <div className="flex items-center gap-2">
                            <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted">{file.name}</span>
                            <IconButton
                              label="清除已选文件"
                              size="sm"
                              onClick={(e) => {
                                e.stopPropagation();
                                setFile(null);
                                setFileError("");
                              }}
                            >
                              <X size={14} strokeWidth={1.75} />
                            </IconButton>
                          </div>
                        )}
                        {fileError && (
                          <p className="flex items-start gap-1.5 text-[11px] text-danger">
                            <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                            {fileError}
                          </p>
                        )}
                      </div>
                    ) : mode === "recording" ? (
                      <div className="space-y-3">
                        {!recordingSupported() ? (
                          <p className="text-[11px] text-muted">当前环境不支持录音（需要 https 或 localhost）</p>
                        ) : (
                          <>
                            <div className="flex flex-col items-center gap-2 rounded-[var(--radius-md)] border border-dashed border-line-strong bg-raise-2/40 px-4 py-6">
                              <button
                                type="button"
                                aria-label={recState === "recording" ? "停止录音" : "开始录音"}
                                disabled={recState === "processing"}
                                onClick={() => void (recState === "recording" ? stopRec() : startRec())}
                                className={`flex size-12 cursor-pointer items-center justify-center rounded-full border transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
                                  recState === "recording"
                                    ? "border-danger bg-danger/10 text-danger"
                                    : "border-line-strong bg-raise-2 text-accent hover:border-accent"
                                }`}
                              >
                                {recState === "recording" ? (
                                  <Square size={18} strokeWidth={1.75} fill="currentColor" />
                                ) : (
                                  <Mic size={18} strokeWidth={1.75} />
                                )}
                              </button>
                              <span className="font-mono text-sm tabular-nums text-fg-2">
                                {formatTime(elapsedMs / 1000)}
                              </span>
                              {recState === "recording" && (
                                <div className="h-1 w-40 overflow-hidden rounded-full bg-line">
                                  <div ref={recLevelRef} className="h-full rounded-full bg-accent" style={{ width: "0%" }} />
                                </div>
                              )}
                              <p className="text-[11px] text-muted">
                                {recState === "recording"
                                  ? "正在录音，点击方块停止"
                                  : recState === "processing"
                                    ? "正在转码…"
                                    : "点击麦克风开始录音，产出 16kHz WAV 走标准版识别"}
                              </p>
                            </div>
                            {recError && (
                              <p className="flex items-start gap-1.5 text-[11px] text-danger">
                                <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                                {recError}
                              </p>
                            )}
                            {recPreview && (
                              <div className="space-y-2">
                                <WavePlayer src={recPreview} title="录音预览" />
                                <Button variant="ghost" size="sm" icon={<X size={13} strokeWidth={1.75} />} onClick={discardRec}>
                                  丢弃录音
                                </Button>
                              </div>
                            )}
                          </>
                        )}
                      </div>
                    ) : (
                      <Field label="音频 URL" hint={urlHint}>
                        {({ id, ...rest }) => (
                          <Input
                            id={id}
                            value={url}
                            onChange={(e) => setUrl(e.target.value)}
                            placeholder="https://example.com/audio.mp3"
                            {...rest}
                          />
                        )}
                      </Field>
                    )}
                  </>
                )}
              </CardBody>
            </Card>

            {/* 右：参数面板 */}
            <Card className="lg:sticky lg:top-4 lg:self-start">
              <CardHeader
                title="识别参数"
                icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
                aside={<span className="micro">{engine} · asr</span>}
              />
              <CardBody className="space-y-4">
                <Field
                  label="语言"
                  hint={
                    engine === "qianwen"
                      ? "留空自动识别语种"
                      : isXiaomi
                        ? "留空自动识别（官方支持中/英显式指定）"
                        : "留空自动识别：中文、英文及上海/闽南/四川/陕西/粤语方言"
                  }
                >
                  {({ id, ...rest }) => (
                    <Select
                      id={id}
                      value={language}
                      onChange={(e) => setLanguage(e.target.value)}
                      {...rest}
                    >
                      {engine === "volcengine" && <option value="">自动识别</option>}
                      {(engine === "qianwen" ? QW_LANGUAGES : isXiaomi ? MI_LANGUAGES : ASR_LANGUAGES).map((l) => (
                        <option key={l.value} value={l.value}>
                          {engine === "volcengine" ? `${l.label} ${l.value}` : l.label}
                        </option>
                      ))}
                    </Select>
                  )}
                </Field>
                {engine === "volcengine" && (
                  <div className="space-y-2">
                    <Field label="热词" aside="可选" hint="逗号分隔，用于提升专有名词识别率">
                      {({ id, ...rest }) => (
                        <Input
                          id={id}
                          value={hotwords}
                          onChange={(e) => setHotwords(e.target.value)}
                          placeholder="火山引擎,语音合成"
                          {...rest}
                        />
                      )}
                    </Field>
                    <DictFill field="hotwords" onFill={setHotwords} />
                  </div>
                )}

                <div className="border-t border-line pt-3">
                  <Button
                    variant="primary"
                    className="w-full"
                    icon={<Mic size={15} strokeWidth={1.75} />}
                    loading={submit.isPending}
                    disabled={!canSubmit}
                    onClick={() => submit.mutate()}
                  >
                    开始识别
                  </Button>
                  {!canSubmit && (
                    <p className="mt-2 text-[11px] text-muted">
                      {mode === "url" ? "请先填写音频 URL" : mode === "recording" ? "请先完成录音" : "请先选择音频文件"}
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
              title="识别结果"
              icon={<FileText size={15} strokeWidth={1.75} />}
              aside={
                run ? (
                  <StatusBadge status={run.status} />
                ) : segments.length > 0 ? (
                  <span className="font-mono text-[11px] tabular-nums text-muted">{segments.length} 句</span>
                ) : undefined
              }
            />

            {!run ? (
              <EmptyState
                icon={<Mic size={18} strokeWidth={1.75} />}
                title="还没有识别结果"
                description="上传本地音频或填写音频 URL 后开始识别，分句时间戳与字幕会显示在这里。"
                action={
                  artifactMode ? (
                    <Button variant="primary" size="sm" loading={submit.isPending} onClick={() => submit.mutate()}>
                      开始识别该音轨
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" icon={<Upload size={13} strokeWidth={1.75} />} onClick={() => fileInputRef.current?.click()}>
                      选择音频文件
                    </Button>
                  )
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
            ) : (
              <CardBody className="space-y-3">
                {playSrc && (
                  <div className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                    <span className="w-14 shrink-0 text-xs text-fg-2">音频源</span>
                    <WavePlayer
                      src={playSrc}
                      title={playTitle}
                      sub={playSub}
                      durationSec={durationSec}
                      className="min-w-0 flex-1"
                    />
                  </div>
                )}

                {run.status !== "succeeded" ? (
                  <div className="space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="text-xs text-muted">{run.note || "处理中"}</span>
                      <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
                    </div>
                    <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
                    {[0, 1, 2, 3].map((i) => (
                      <Skeleton key={i} className="h-9 w-full" />
                    ))}
                  </div>
                ) : segments.length > 0 ? (
                  <TranscriptList
                    segments={segments}
                    activeIdx={activeIdx}
                    onSeek={(ms) => seekTo(ms, seekTrack)}
                  />
                ) : (
                  <p className="py-2 text-xs text-muted">未识别到分句内容，可直接下载转写文本查看。</p>
                )}
              </CardBody>
            )}
          </Card>

          {/* 产物下载：非音频产物行式列出 */}
          {downloads.length > 0 && (
            <Card className="mt-4">
              <CardHeader
                title="产物"
                icon={<Download size={15} strokeWidth={1.75} />}
                aside={<span className="font-mono text-[11px] tabular-nums text-muted">{downloads.length} 个文件</span>}
              />
              <CardBody className="space-y-2">
                {downloads.map((a) => (
                  <DownloadRow key={a.id} a={a} />
                ))}
              </CardBody>
            </Card>
          )}
        </>
      )}
    </>
  );
}
