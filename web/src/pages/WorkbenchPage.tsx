import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowUpRight, AudioLines, Clock, Languages, ListOrdered, Mic, NotebookPen, Podcast, Waves, X, Scissors } from "lucide-react";
import { fetchJSON } from "../lib/api";
import type { Task, TaskStatus } from "../lib/types";
import { useStorageEnabled } from "../lib/useStorageEnabled";
import { toolLabel } from "../lib/toolNames";
import { useTaskEvents } from "../lib/ws";
import { FileDrop } from "../components/FileDrop";
import { Button, Card, CardBody, CardHeader, EmptyState, IconButton, PageHeader, Skeleton, StatusBadge, Tabs, useToast } from "../ui";

const tools = [
  { to: "/tts", name: "语音合成", desc: "同步/流式/长文本三通道，按费用选", icon: AudioLines, tool: "tts" },
  { to: "/asr", name: "语音识别", desc: "音频转文字，分句时间戳与字幕", icon: Mic, tool: "asr" },
  { to: "/podcast", name: "播客工坊", desc: "生成双人对话播客", icon: Podcast, tool: "podcast" },
  { to: "/separate", name: "人声分离", desc: "人声与背景音分轨输出", icon: Waves, tool: "separate" },
  { to: "/audio-edit", name: "音频剪辑", desc: "切割/合并/变调/调BPM查询", icon: Scissors, tool: "trim" },
  { to: "/translate", name: "机器翻译", desc: "32 语种互译，术语定制", icon: Languages, tool: "translate" },
  { to: "/minutes", name: "语音妙记", desc: "音视频转纪要：总结/待办/章节", icon: NotebookPen, tool: "minutes" },
];

/** 批量识别单批上限：防止误贴超大列表；引擎侧并发 ×2 自动排队 */
const MAX_BATCH = 20;

type AsrVersion = "standard" | "idle" | "flash";

/** 批量识别本地文件白名单：标准/闲时/极速三版本一致（与后端 checkURLFileForBridge 一致） */
const BATCH_EXTS = ["wav", "mp3", "ogg", "spx", "amr", "aac", "m4a"];
const BATCH_ACCEPT = BATCH_EXTS.map((e) => `.${e}`).join(",");

interface BatchRow {
  task_id: string;
  url: string;
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

function shortUrl(u: string): string {
  return u.replace(/^https?:\/\//, "").slice(0, 60);
}

/** 批量识别：多行 URL / 多个本地文件逐个提交为独立任务，本批次行内实时进度（WS 驱动）与逐行取消。
 *  本地文件通道需已配置对象存储（任务执行期服务端自动转存取签名 URL）。 */
function BatchAsrCard() {
  const [mode, setMode] = useState<"url" | "files">("url");
  const [text, setText] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [fileError, setFileError] = useState("");
  const [version, setVersion] = useState<AsrVersion>("flash");
  const [rows, setRows] = useState<BatchRow[]>([]);
  const [running, setRunning] = useState(false);
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();
  const { enabled: storageEnabled } = useStorageEnabled();

  /* WS 事件驱动行状态：progress 更新进度，终态落 StatusBadge */
  useEffect(() => {
    if (!ev) return;
    setRows((prev) => {
      const idx = prev.findIndex((r) => r.task_id === ev.task_id);
      if (idx === -1) return prev;
      const next = [...prev];
      const row = { ...next[idx] };
      if (ev.type === "progress") {
        row.status = "running";
        row.progress = ev.progress ?? row.progress;
        row.note = ev.note ?? "";
      } else if (ev.type === "done") {
        row.status = "succeeded";
        row.progress = 100;
        row.note = "完成";
      } else if (ev.type === "error") {
        row.status = "failed";
        row.note = "";
        row.error = ev.error || "任务失败";
      } else if (ev.type === "canceled") {
        row.status = "canceled";
        row.note = "已取消";
      }
      next[idx] = row;
      return next;
    });
  }, [ev]);

  const urls = [...new Set(text.split(/\n/).map((s) => s.trim()).filter(Boolean))];
  const overLimit = mode === "url" ? urls.length > MAX_BATCH : files.length > MAX_BATCH;
  const badLines = mode === "url" ? urls.filter((u) => !/^https?:\/\//i.test(u)) : [];
  const badFiles = mode === "files" ? files.filter((f) => !BATCH_EXTS.includes(f.name.split(".").pop()?.toLowerCase() ?? "")) : [];
  const canSubmit =
    !running &&
    (mode === "url"
      ? urls.length > 0 && !overLimit && badLines.length === 0
      : files.length > 0 && !overLimit && badFiles.length === 0);

  const submit = async () => {
    setRunning(true);
    // 逐条提交：单条失败不阻断后续（错误经 toast 提示），排队交给引擎并发槽
    const items: { label: string; body: Record<string, unknown> }[] =
      mode === "url"
        ? urls.map((url) => ({
            label: url,
            body: { provider: "volcengine", tool: "asr", params: { url, version, srt: true } },
          }))
        : files.map((f) => ({
            label: f.name,
            body: { provider: "volcengine", tool: "asr", params: { version, srt: true }, file_ids: [] as string[] },
          }));
    for (const item of items) {
      try {
        if (mode === "files") {
          // 本地文件：先传本服务拿 file_id，任务执行期服务端转存对象存储换取签名 URL
          const fd = new FormData();
          fd.append("file", files.find((f) => f.name === item.label)!);
          const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
          (item.body as { file_ids: string[] }).file_ids = [up.file_id];
        }
        const d = await fetchJSON<{ task_id: string }>("/api/tasks", {
          method: "POST",
          body: JSON.stringify(item.body),
        });
        setRows((prev) => [...prev, { task_id: d.task_id, url: item.label, status: "pending", progress: 0, note: "已提交" }]);
      } catch (e) {
        toast({ tone: "error", title: "提交失败", description: `${shortUrl(item.label)}：${(e as Error).message}` });
      }
    }
    setRunning(false);
    setText("");
    setFiles([]);
    void qc.invalidateQueries({ queryKey: ["tasks"] });
  };

  const cancel = async (id: string) => {
    try {
      await fetchJSON(`/api/tasks/${id}/cancel`, { method: "POST" });
    } catch (e) {
      toast({ tone: "error", title: "取消失败", description: (e as Error).message });
    }
  };

  return (
    <Card className="mt-6">
      <CardHeader
        title="批量识别"
        icon={<ListOrdered size={15} strokeWidth={1.75} />}
        aside={<span className="micro">引擎并发 ×2 自动排队</span>}
      />
      <CardBody className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Tabs<AsrVersion>
            items={[
              { value: "flash", label: "极速版" },
              { value: "idle", label: "闲时版" },
              { value: "standard", label: "标准版" },
            ]}
            value={version}
            onChange={setVersion}
          />
          {storageEnabled && (
            <Tabs<"url" | "files">
              items={[
                { value: "url" as const, label: "URL 列表" },
                { value: "files" as const, label: "本地文件" },
              ]}
              value={mode}
              onChange={(m) => {
                setMode(m);
                setFileError("");
              }}
            />
          )}
        </div>

        {mode === "files" ? (
          <div className="space-y-2">
            <FileDrop
              multiple
              files={files}
              onFiles={(fs) => {
                const bad = fs.filter((f) => !BATCH_EXTS.includes(f.name.split(".").pop()?.toLowerCase() ?? ""));
                setFileError(bad.length > 0 ? `${bad.length} 个文件格式不支持：支持 wav / mp3 / ogg / spx / amr / aac / m4a` : "");
                setFiles(fs);
              }}
              accept={BATCH_ACCEPT}
              label="选择或拖入批量音频文件"
              emptyHint="支持 wav / mp3 / ogg / spx / amr / aac / m4a，最多 20 个；自动经对象存储中转"
              error={fileError || (overLimit ? `超出上限：最多 ${MAX_BATCH} 个文件` : undefined)}
            />
            <p className="text-[11px] text-muted">
              每个文件一个独立任务：先上传到本服务，再转存对象存储取签名 URL 供识别拉取；「极速版」单个文件 ≤100MB。
            </p>
          </div>
        ) : (
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={4}
            placeholder={"每行一个音频 URL，最多 20 行\nhttps://example.com/a.mp3\nhttps://example.com/b.mp3"}
            className="w-full rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 font-mono text-xs text-fg placeholder:text-muted focus:border-accent focus:outline-none"
            aria-label="批量识别 URL 列表"
          />
        )}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-[11px] text-muted">
            {mode === "files"
              ? badFiles.length > 0
                ? `${badFiles.length} 个文件格式与所选版本不匹配`
                : `已选 ${files.length} 个文件${files.length > 0 ? `，识别版本 ${version === "flash" ? "极速" : version === "idle" ? "闲时" : "标准"}` : ""}`
              : overLimit
                ? `超出上限：最多 ${MAX_BATCH} 行`
                : badLines.length > 0
                  ? `${badLines.length} 行不是 http(s) 地址`
                  : `已录入 ${urls.length} 行${urls.length > 0 ? `，识别版本 ${version === "flash" ? "极速" : version === "idle" ? "闲时" : "标准"}` : ""}`}
          </p>
          <Button variant="primary" size="sm" loading={running} disabled={!canSubmit} onClick={() => void submit()}>
            {running ? "提交中…" : `批量提交${mode === "url" ? (urls.length > 0 ? ` ${urls.length} 个` : "") : files.length > 0 ? ` ${files.length} 个` : ""}`}
          </Button>
        </div>

        {rows.length > 0 && (
          <ul className="divide-y divide-line rounded-[var(--radius-sm)] border border-line">
            {rows.map((r, i) => (
              <li key={r.task_id} className="flex items-center gap-3 px-3 py-2">
                <span className="w-5 shrink-0 font-mono text-[11px] tabular-nums text-muted">{i + 1}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-fg-2" title={r.url}>
                  {shortUrl(r.url)}
                </span>
                <StatusBadge status={r.status} />
                <span className="w-36 shrink-0 truncate text-right text-[11px] text-muted">
                  {r.error || r.note || "—"}
                </span>
                {(r.status === "pending" || r.status === "running") && (
                  <IconButton label="取消该任务" size="sm" variant="ghost" onClick={() => void cancel(r.task_id)}>
                    <X size={13} strokeWidth={1.75} />
                  </IconButton>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardBody>
    </Card>
  );
}

function StatTile({ label, value, hint, loading }: { label: string; value: string; hint?: string; loading?: boolean }) {
  return (
    <div className="rounded-[var(--radius-md)] border border-line bg-raise px-4 py-3 shadow-[var(--shadow-1)]">
      <p className="micro">{label}</p>
      {loading ? (
        <Skeleton className="mt-2 h-7 w-16" />
      ) : (
        <p className="mt-1 font-mono text-[26px] font-medium leading-none tracking-tight">{value}</p>
      )}
      {hint && <p className="mt-1.5 text-[11px] text-muted">{hint}</p>}
    </div>
  );
}

function RecentTasks({ items, loading }: { items: Task[]; loading: boolean }) {
  if (loading) {
    return (
      <div className="space-y-2 p-4">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Clock size={18} strokeWidth={1.75} />}
        title="还没有任务记录"
        description="从上方任选一个工具开始，任务会自动记录在这里。"
      />
    );
  }
  return (
    <ul className="divide-y divide-line">
      {items.map((t) => (
        <li key={t.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
          <span className="w-20 shrink-0 text-fg-2">{tools.find((x) => x.tool === t.tool)?.name ?? toolLabel(t.tool)}</span>
          <StatusBadge status={t.status} />
          {t.title && <span className="hidden min-w-0 max-w-40 shrink truncate text-xs sm:inline">{t.title}</span>}
          <span className="min-w-0 flex-1 truncate text-xs text-muted">{t.progress_note || t.error || "—"}</span>
          <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">
            {t.cost_ms > 0 ? `${(t.cost_ms / 1000).toFixed(1)}s` : "—"}
          </span>
          <span className="shrink-0 font-mono text-[11px] text-muted">{t.created_at}</span>
        </li>
      ))}
    </ul>
  );
}

export default function WorkbenchPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["tasks", "workbench"],
    queryFn: () => fetchJSON<{ items: Task[]; total: number }>("/api/tasks?size=100"),
  });

  const items = data?.items ?? [];
  const finished = items.filter((t) => t.status === "succeeded" || t.status === "failed");
  const succeeded = items.filter((t) => t.status === "succeeded").length;
  const rate = finished.length > 0 ? Math.round((succeeded / finished.length) * 100) : null;
  const totalSeconds = items.reduce((acc, t) => acc + (t.cost_ms || 0), 0) / 1000;

  return (
    <>
      <PageHeader
        title="工作台"
        description="工具入口与运行概况"
        actions={
          <Link
            to="/history"
            className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
          >
            全部任务
            <ArrowUpRight size={13} strokeWidth={1.75} />
          </Link>
        }
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile label="任务总数" value={String(data?.total ?? 0)} loading={isLoading} />
        <StatTile
          label="成功任务"
          value={String(succeeded)}
          hint={rate === null ? undefined : `成功率 ${rate}%`}
          loading={isLoading}
        />
        <StatTile
          label="累计耗时"
          value={`${totalSeconds.toFixed(1)}s`}
          hint="近 100 条任务累计"
          loading={isLoading}
        />
        <StatTile label="工具入口" value={String(tools.length)} hint="语音与音频工具入口" loading={isLoading} />
      </div>

      <div className="mt-6 grid gap-3 sm:grid-cols-2">
        {tools.map(({ to, name, desc, icon: Icon, tool }) => {
          const ttsFamily = ["tts", "tts_long", "tts_stream"];
          const count = items.filter((t) =>
            tool === "tts" ? ttsFamily.includes(t.tool) : t.tool === tool,
          ).length;
          return (
            <Link
              key={to}
              to={to}
              className="group rounded-[var(--radius-md)] border border-line bg-raise p-4 shadow-[var(--shadow-1)] transition-colors duration-150 hover:border-line-strong hover:bg-raise-2"
            >
              <div className="flex items-start gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius-sm)] border border-line bg-raise-2 text-accent">
                  <Icon size={18} strokeWidth={1.75} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <h2 className="text-sm font-medium">{name}</h2>
                    <ArrowUpRight
                      size={13}
                      strokeWidth={1.75}
                      className="text-muted transition-colors duration-150 group-hover:text-accent"
                    />
                  </div>
                  <p className="mt-0.5 text-xs text-muted">{desc}</p>
                </div>
                <span className="shrink-0 font-mono text-[11px] text-muted">{count} 次</span>
              </div>
            </Link>
          );
        })}
      </div>

      <BatchAsrCard />

      <Card className="mt-6">
        <CardHeader
          title="最近任务"
          icon={<Clock size={15} strokeWidth={1.75} />}
          aside={<span className="micro">近 {Math.min(items.length, 8)} 条</span>}
        />
        <RecentTasks items={items.slice(0, 8)} loading={isLoading} />
      </Card>
    </>
  );
}
