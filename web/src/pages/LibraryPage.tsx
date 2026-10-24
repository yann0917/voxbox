import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronDown,
  ChevronRight,
  Download,
  Link as LinkIcon,
  Loader2,
  MicVocal,
  Music,
  Sparkles,
  Workflow,
} from "lucide-react";
import { fetchJSON } from "../lib/api";
import { douyinNotesScenario, linkSubtitleScenario, songClipScenario, songStemsScenario } from "../lib/scenarios";
import type { Scenario } from "../lib/scenarios";
import { useScenarioRunner } from "../lib/useScenarioRunner";
import { listMusicSources, type MusicTrack } from "../lib/music";
import type { Artifact, Task, TaskDetail } from "../lib/types";
import { ArtifactRow, formatSize } from "../components/ArtifactRow";
import { Button, Card, EmptyState, PageHeader, ProgressBar, Switch, Tabs, useToast } from "../ui";
import { MusicSearchView, MusicRanksView, MusicPlaylistsView, MusicMVView, MusicHistoryView } from "./library/MusicViews";

type Tab = "music" | "link" | "scenario";
type MusicSub = "search" | "ranks" | "playlists" | "mv" | "history";

const TABS = [
  { value: "music" as Tab, label: "音乐", icon: <Music size={14} strokeWidth={1.75} /> },
  { value: "link" as Tab, label: "链接下载", icon: <LinkIcon size={14} strokeWidth={1.75} /> },
  { value: "scenario" as Tab, label: "场景", icon: <Workflow size={14} strokeWidth={1.75} /> },
];

const MUSIC_TABS = [
  { value: "search" as MusicSub, label: "搜索" },
  { value: "ranks" as MusicSub, label: "排行榜" },
  { value: "playlists" as MusicSub, label: "歌单" },
  { value: "mv" as MusicSub, label: "MV" },
  { value: "history" as MusicSub, label: "在听" },
];

export default function LibraryPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = (searchParams.get("tab") as Tab) || "music";
  const { run, start, reset, cancel } = useScenarioRunner();
  // 空态（未导入音源）时音乐 Tab 里的视图们会因搜索/榜单不可用而难看，单独用 sources 查询判空态
  const { data: sourcesData } = useQuery({ queryKey: ["music-sources"], queryFn: listMusicSources });
  const sources = (sourcesData?.items ?? []).filter((l) => l.enabled);

  const onStems = (t: MusicTrack) => start(songStemsScenario(t, "volcengine"));
  const onClip = (t: MusicTrack) => start(songClipScenario(t));

  return (
    <div>
      <PageHeader title="素材" description="搜歌、贴链接下载，或按场景一步串到成品" />
      {run && <RunnerCard run={run} onCancel={cancel} onReset={reset} />}
      <div className="mb-5">
        <Tabs items={TABS} value={TABS.some((t) => t.value === tab) ? tab : "music"} onChange={(v) => setSearchParams(v === "music" ? {} : { tab: v })} />
      </div>
      {tab === "music" &&
        (sources.length === 0 ? (
          <MusicEmptyState />
        ) : (
          <MusicSection onStems={onStems} onClip={onClip} />
        ))}
      {tab === "link" && <LinkTab />}
      {tab === "scenario" && <ScenarioTab onGoMusic={() => setSearchParams({ tab: "music" })} onStartScenario={start} />}
      <RecentMaterials />
    </div>
  );
}

// ---------------------------------------------------------------- 音乐（二级视图）

function MusicSection({ onStems, onClip }: { onStems: (t: MusicTrack) => void; onClip: (t: MusicTrack) => void }) {
  const [sub, setSub] = useState<MusicSub>("search");
  const props = { onStems, onClip };
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-1 text-xs">
        {MUSIC_TABS.map((t) => (
          <button
            key={t.value}
            type="button"
            onClick={() => setSub(t.value)}
            className={`cursor-pointer rounded-full px-3 py-1.5 transition-colors duration-150 ${
              sub === t.value ? "bg-accent/10 text-accent" : "text-muted hover:text-fg-2"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      {sub === "search" && <MusicSearchView {...props} />}
      {sub === "ranks" && <MusicRanksView {...props} />}
      {sub === "playlists" && <MusicPlaylistsView {...props} />}
      {sub === "mv" && <MusicMVView {...props} />}
      {sub === "history" && <MusicHistoryView {...props} />}
    </div>
  );
}

function MusicEmptyState() {
  const syncSources = useMutation({
    mutationFn: () => fetchJSON<{ lines: number }>("/api/music/sources/sync", { method: "POST" }),
  });
  const { toast } = useToast();
  const qc = useQueryClient();
  return (
    <Card>
      <EmptyState
        icon={<Music size={20} strokeWidth={1.5} />}
        title="还没有音源"
        description="填好同步地址后，点「同步音源」拉取音源配置（地址在设置页「音乐音源」维护），之后就能搜歌下载了"
        action={
          <Button
            variant="primary"
            loading={syncSources.isPending}
            onClick={() =>
              syncSources.mutate(undefined, {
                onSuccess: (d) => {
                  toast({ tone: "ok", title: `已同步 ${d.lines} 个音源`, description: "可以搜歌了" });
                  void qc.invalidateQueries({ queryKey: ["music-sources"] });
                },
                onError: (e) =>
                  toast({
                    tone: "error",
                    title: "同步失败",
                    description: `${e instanceof Error ? e.message : String(e)}（同步地址在设置页「音乐音源」配置）`,
                  }),
              })
            }
          >
            同步音源
          </Button>
        }
      />
    </Card>
  );
}

// ---------------------------------------------------------------- 场景执行卡

function RunnerCard({
  run,
  onCancel,
  onReset,
}: {
  run: NonNullable<ReturnType<typeof useScenarioRunner>["run"]>;
  onCancel: () => void;
  onReset: () => void;
}) {
  return (
    <Card className="mb-5">
      <div className="flex items-center justify-between gap-3 p-4 pb-0">
        <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
          {run.status === "running" && <Loader2 size={15} className="shrink-0 animate-spin text-accent" />}
          <Sparkles size={15} className={run.status === "running" ? "hidden" : "shrink-0 text-accent"} />
          <span className="truncate">{run.title}</span>
        </div>
        {run.status === "running" ? (
          <button type="button" onClick={onCancel} className="cursor-pointer text-xs text-fg-2 hover:text-accent">
            取消
          </button>
        ) : (
          <button type="button" onClick={onReset} className="cursor-pointer text-xs text-fg-2 hover:text-accent">
            关闭
          </button>
        )}
      </div>
      <div className="flex flex-wrap gap-2 p-4">
        {run.steps.map((s, i) => (
          <div
            key={i}
            className={`flex items-center gap-2 rounded-[var(--radius-sm)] border px-3 py-1.5 text-xs ${
              s.status === "failed"
                ? "border-danger/40 text-danger"
                : s.status === "done"
                  ? "border-line bg-raise-2 text-fg-2"
                  : s.status === "running"
                    ? "border-accent/40 text-fg"
                    : "border-line text-muted"
            }`}
          >
            <span>{s.label}</span>
            {s.status === "running" && <span className="tabular-nums text-muted">{s.progress}%</span>}
            {s.status === "failed" && <span className="max-w-40 truncate">{run.error || s.note}</span>}
          </div>
        ))}
      </div>
      {run.status === "done" && run.artifacts.length > 0 && (
        <div className="flex flex-col gap-2 px-4 pb-4">
          {run.artifacts.map((a: Artifact) => (
            <ArtifactRow key={a.id} a={a} />
          ))}
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------- 链接下载

function LinkTab() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [probing, setProbing] = useState(false);
  const [probe, setProbe] = useState<ProbeMeta | null>(null);
  const [probeError, setProbeError] = useState("");
  const [mode, setMode] = useState<"audio" | "video">("audio");
  const [maxItems, setMaxItems] = useState(1);
  const [subs, setSubs] = useState(false);

  const doProbe = useMutation({
    mutationFn: () => fetchJSON<ProbeMeta>("/api/downloader/probe", { method: "POST", body: JSON.stringify({ url: url.trim() }) }),
    onMutate: () => {
      setProbing(true);
      setProbeError("");
      setProbe(null);
    },
    onSettled: () => setProbing(false),
    onSuccess: (d) => setProbe(d),
    onError: (e) => setProbeError(e instanceof Error ? e.message : String(e)),
  });

  const submit = useMutation({
    mutationFn: () =>
      fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({
          provider: "downloader",
          tool: "download",
          params: {
            url: url.trim(),
            backend: probe?.backend ?? "auto",
            mode,
            max_items: probe?.playlist ? maxItems : 1,
            subtitles: subs,
          },
        }),
      }),
    onSuccess: () => {
      toast({ tone: "ok", title: "已加入下载", description: "进度见下方最近素材或任务页" });
      void qc.invalidateQueries({ queryKey: ["recent-materials"] });
    },
    onError: (e) => toast({ tone: "error", title: "提交失败", description: e instanceof Error ? e.message : String(e) }),
  });

  const fmtDur = (sec?: number) => {
    if (!sec) return "";
    const s = Math.round(sec);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  };

  return (
    <Card>
      <div className="flex flex-wrap items-center gap-2 p-4">
        <div className="flex min-w-0 flex-1 items-center gap-2 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3">
          <LinkIcon size={15} className="shrink-0 text-muted" />
          <input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && url.trim()) doProbe.mutate();
            }}
            placeholder="粘贴视频 / 合集 / 抖音分享链接"
            className="min-w-0 flex-1 bg-transparent py-2.5 text-sm outline-none placeholder:text-muted"
          />
        </div>
        <Button variant="secondary" loading={probing} disabled={!url.trim()} onClick={() => doProbe.mutate()}>
          解析
        </Button>
      </div>

      {probeError && <p className="px-4 pb-3 text-xs text-danger">{probeError}</p>}

      {probe && (
        <div className="flex flex-col gap-3 border-t border-line px-4 py-4">
          <div className="min-w-0">
            <p className="truncate text-sm">{probe.title || "未知道题"}</p>
            <p className="text-xs text-muted">
              {probe.backend === "douyin"
                ? "抖音链接 · 将使用 douyin-downloader 下载"
                : [probe.site, probe.uploader, fmtDur(probe.duration)].filter(Boolean).join(" · ") || "yt-dlp 下载"}
              {probe.playlist ? ` · 合集 ${probe.n_entries} 条` : ""}
              {probe.subtitle_langs?.length ? ` · 可选字幕 ${probe.subtitle_langs.length} 种语言` : ""}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-4 text-xs">
            <div className="flex items-center gap-2">
              <span className="text-muted">产物</span>
              {(["audio", "video"] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => setMode(m)}
                  className={`cursor-pointer rounded-full border px-3 py-1 transition-colors duration-150 ${
                    mode === m ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
                  }`}
                >
                  {m === "audio" ? "仅音频" : "音视频"}
                </button>
              ))}
            </div>
            {probe.playlist && (
              <label className="flex items-center gap-2 text-fg-2">
                <span className="text-muted">合集上限</span>
                <input
                  type="number"
                  min={1}
                  value={maxItems}
                  onChange={(e) => setMaxItems(Math.max(1, Number(e.target.value) || 1))}
                  className="w-16 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2 py-1 text-xs outline-none"
                />
                <span className="text-muted">条</span>
              </label>
            )}
            {!probe.playlist && (probe.subtitle_langs?.length ?? 0) > 0 && (
              <label className="flex items-center gap-2 text-fg-2">
                <Switch checked={subs} onChange={setSubs} />
                <span className="text-muted">同时下载字幕（srt）</span>
              </label>
            )}
            <Button variant="primary" loading={submit.isPending} onClick={() => submit.mutate()} icon={<Download size={14} />} className="ml-auto">
              开始下载
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}

interface ProbeMeta {
  backend: "ytdlp" | "douyin";
  probe?: string;
  title?: string;
  duration?: number;
  uploader?: string;
  site?: string;
  playlist?: boolean;
  n_entries?: number;
  subtitle_langs?: string[];
}

// ---------------------------------------------------------------- 场景

function ScenarioTab({
  onGoMusic,
  onStartScenario,
}: {
  onGoMusic: () => void;
  onStartScenario: (s: Scenario) => void;
}) {
  const [linkUrl, setLinkUrl] = useState("");
  const [linkAsr, setLinkAsr] = useState<"local" | "volcengine">("local");
  const [dyUrl, setDyUrl] = useState("");
  const [dyAsr, setDyAsr] = useState<"local" | "volcengine">("local");

  return (
    <div className="flex flex-col gap-4">
      <p className="text-xs text-muted">
        场景把「下载 → 识别 / 分离」串成一步：每步都是普通任务，进度实时可见，中断后单步可重跑。
      </p>
      <Card>
        <div className="p-4">
          <div className="flex items-center gap-2">
            <Workflow size={15} className="text-accent" />
            <p className="text-sm font-medium">链接 → 字幕稿</p>
          </div>
          <p className="mt-1 text-xs text-muted">贴视频或合集链接，自动下载音频并转写，产出 SRT 与文稿，可去字幕工坊样式化。</p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <input
              value={linkUrl}
              onChange={(e) => setLinkUrl(e.target.value)}
              placeholder="视频链接"
              className="min-w-0 flex-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-sm outline-none placeholder:text-muted"
            />
            <AsrSelect value={linkAsr} onChange={setLinkAsr} />
            <Button variant="primary" disabled={!linkUrl.trim()} onClick={() => onStartScenario(linkSubtitleScenario(linkUrl.trim(), { engine: linkAsr }))}>
              开始
            </Button>
          </div>
        </div>
      </Card>
      <Card>
        <div className="p-4">
          <div className="flex items-center gap-2">
            <MicVocal size={15} className="text-accent" />
            <p className="text-sm font-medium">歌曲 → 伴奏人声</p>
          </div>
          <p className="mt-1 text-xs text-muted">
            在音乐搜索里选歌：行内「伴奏人声」下载后自动送人声分离；「切高潮」下载后直达剪辑页。
          </p>
          <Button variant="primary" onClick={onGoMusic} className="mt-3">
            去搜索选歌
          </Button>
        </div>
      </Card>
      <Card>
        <div className="p-4">
          <div className="flex items-center gap-2">
            <Sparkles size={15} className="text-accent" />
            <p className="text-sm font-medium">抖音链接 → 转写笔记</p>
          </div>
          <p className="mt-1 text-xs text-muted">
            抖音分享链接下载音频并转写；完成后到任务详情可 AI 提炼与问答。需在设置页配置 douyin-downloader。
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <input
              value={dyUrl}
              onChange={(e) => setDyUrl(e.target.value)}
              placeholder="抖音分享链接"
              className="min-w-0 flex-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-sm outline-none placeholder:text-muted"
            />
            <AsrSelect value={dyAsr} onChange={setDyAsr} />
            <Button variant="primary" disabled={!dyUrl.trim()} onClick={() => onStartScenario(douyinNotesScenario(dyUrl.trim(), { engine: dyAsr }))}>
              开始
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

function AsrSelect({ value, onChange }: { value: "local" | "volcengine"; onChange: (v: "local" | "volcengine") => void }) {
  return (
    <div className="flex items-center gap-1 rounded-[var(--radius-sm)] border border-line p-0.5 text-xs">
      {(
        [
          { v: "local", label: "本地识别" },
          { v: "volcengine", label: "火山（≤60s）" },
        ] as const
      ).map((o) => (
        <button
          key={o.v}
          type="button"
          onClick={() => onChange(o.v)}
          className={`cursor-pointer rounded-[calc(var(--radius-sm)-2px)] px-2.5 py-1.5 transition-colors duration-150 ${
            value === o.v ? "bg-accent/10 text-accent" : "text-fg-2 hover:text-fg"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- 最近素材

function RecentMaterials() {
  const [extra, setExtra] = useState<Map<string, { detail?: TaskDetail; open: boolean }>>(new Map());

  const { data, refetch } = useQuery({
    queryKey: ["recent-materials"],
    queryFn: async () => {
      const music = await fetchJSON<{ items: Task[] }>("/api/tasks?provider=music&size=6");
      const dl = await fetchJSON<{ items: Task[] }>("/api/tasks?provider=downloader&size=6");
      return [...music.items, ...dl.items]
        .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))
        .slice(0, 8);
    },
    refetchInterval: 5000,
  });

  const rows: { task: Task; detail: TaskDetail | null; open: boolean }[] = (data ?? []).map((t) => ({
    task: t,
    detail: extra.get(t.id)?.detail ?? null,
    open: extra.get(t.id)?.open ?? false,
  }));

  const toggle = async (id: string) => {
    setExtra((prev) => {
      const cur = prev.get(id) ?? { detail: undefined, open: false };
      const next = new Map(prev);
      next.set(id, { ...cur, open: !cur.open });
      return next;
    });
    if (!extra.get(id)?.detail) {
      try {
        const d = await fetchJSON<TaskDetail>(`/api/tasks/${id}`);
        setExtra((prev) => {
          const next = new Map(prev);
          const cur = prev.get(id) ?? { open: false };
          next.set(id, { ...cur, detail: d });
          return next;
        });
      } catch {
        // 详情拉取失败不阻塞展开
      }
    }
  };

  if (!data || data.length === 0) return null;

  return (
    <div className="mt-8">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-medium text-fg">最近素材</h2>
        <button type="button" onClick={() => void refetch()} className="cursor-pointer text-xs text-fg-2 hover:text-accent">
          刷新
        </button>
      </div>
      <Card>
        <div className="flex flex-col divide-y divide-line">
          {rows.map(({ task, detail, open }) => (
            <div key={task.id}>
              <button
                type="button"
                onClick={() => void toggle(task.id)}
                className="flex w-full cursor-pointer items-center gap-3 px-4 py-3 text-left"
              >
                {open ? <ChevronDown size={14} className="shrink-0 text-muted" /> : <ChevronRight size={14} className="shrink-0 text-muted" />}
                <span className="min-w-0 flex-1 truncate text-sm">{task.title || task.tool}</span>
                <span
                  className={`shrink-0 text-xs ${
                    task.status === "succeeded" ? "text-fg-2" : task.status === "failed" ? "text-danger" : "text-accent"
                  }`}
                >
                  {task.status === "running" || task.status === "pending"
                    ? `进行中 ${task.progress}%`
                    : task.status === "succeeded"
                      ? "已完成"
                      : task.status === "failed"
                        ? "失败"
                        : task.status === "canceled"
                          ? "已取消"
                          : task.status}
                </span>
                {(task.status === "running" || task.status === "pending") && (
                  <span className="w-24 shrink-0">
                    <ProgressBar value={task.progress} />
                  </span>
                )}
                <span className="hidden shrink-0 text-xs text-muted sm:inline">
                  {new Date(task.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}
                </span>
              </button>
              {open && (
                <div className="flex flex-col gap-2 px-4 pb-4 pl-10">
                  {detail?.artifacts.length ? (
                    detail.artifacts.map((a) => <ArtifactRow key={a.id} a={a} />)
                  ) : task.status === "succeeded" ? (
                    <span className="text-xs text-muted">没有产物</span>
                  ) : (
                    <span className="text-xs text-muted">{task.error || "尚未产出"}</span>
                  )}
                  {detail && (
                    <p className="text-[11px] text-muted">
                      {detail.artifacts.length} 个产物 · {formatSize(detail.artifacts.reduce((n, a) => n + a.size, 0))}
                    </p>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
