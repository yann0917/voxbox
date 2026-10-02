import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowUpRight, AudioLines, Clock, Languages, Mic, NotebookPen, Podcast, Waves, Scissors } from "lucide-react";
import { fetchJSON } from "../lib/api";
import type { Task } from "../lib/types";
import { toolLabel } from "../lib/toolNames";
import { Card, CardHeader, EmptyState, PageHeader, Skeleton, StatusBadge } from "../ui";

const tools = [
  { to: "/tts", name: "语音合成", desc: "同步/流式/长文本三通道，按费用选", icon: AudioLines, tool: "tts" },
  { to: "/asr", name: "语音识别", desc: "音频转文字，分句时间戳与字幕", icon: Mic, tool: "asr" },
  { to: "/podcast", name: "播客工坊", desc: "生成双人对话播客", icon: Podcast, tool: "podcast" },
  { to: "/separate", name: "人声分离", desc: "人声与背景音分轨输出", icon: Waves, tool: "separate" },
  { to: "/audio-edit", name: "音频剪辑", desc: "切割/合并/变调/调BPM查询", icon: Scissors, tool: "trim" },
  { to: "/translate", name: "机器翻译", desc: "32 语种互译，术语定制", icon: Languages, tool: "translate" },
  { to: "/minutes", name: "语音妙记", desc: "音视频转纪要：总结/待办/章节", icon: NotebookPen, tool: "minutes" },
];

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
