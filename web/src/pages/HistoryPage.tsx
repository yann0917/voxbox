import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Clock, RefreshCw, Search, SlidersHorizontal, Trash2 } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { formatTime } from "../lib/player";
import type { Task, TaskDetail } from "../lib/types";
import { TaskDetailPanel } from "../components/TaskDetail";
import {
  Button,
  Card,
  CardHeader,
  ConfirmDialog,
  EmptyState,
  IconButton,
  Input,
  PageHeader,
  Pagination,
  Skeleton,
  StatusBadge,
  useToast,
} from "../ui";
import { toolLabel } from "../lib/toolNames";

const filters = [
  { value: "", label: "全部" },
  { value: "tts", label: "语音合成" },
  { value: "tts_long", label: "长文本合成" },
  { value: "tts_stream", label: "流式合成" },
  { value: "asr", label: "语音识别" },
  { value: "podcast", label: "播客工坊" },
  { value: "separate", label: "人声分离" },
  { value: "translate", label: "机器翻译" },
  { value: "minutes", label: "语音妙记" },
];

interface SearchHit {
  task_id: string;
  tool: string;
  title?: string;
  created_at: string;
  matches: { text: string; start_ms: number; end_ms?: number }[];
}

/** 分离四引擎（gsgc/zhuanhuanmao/mvsep/volcengine）+ tool=separate 且产物 ≥2 轨的任务可进混音台 */
const SEP_PROVIDERS = ["gsgc", "zhuanhuanmao", "mvsep", "volcengine"];
const mixableToMixer = (d: TaskDetail) =>
  d.task.tool === "separate" && SEP_PROVIDERS.includes(d.task.provider) && d.artifacts.length >= 2;

/** 命中关键词高亮（大小写不敏感） */
function Highlight({ text, q }: { text: string; q: string }) {
  if (!q) return <>{text}</>;
  const lower = text.toLowerCase();
  const parts: ReactNode[] = [];
  let i = 0;
  for (;;) {
    const hit = lower.indexOf(q.toLowerCase(), i);
    if (hit < 0) {
      parts.push(text.slice(i));
      break;
    }
    parts.push(text.slice(i, hit));
    parts.push(
      <mark key={hit} className="rounded-[2px] bg-accent/30 px-0.5 text-fg">
        {text.slice(hit, hit + q.length)}
      </mark>,
    );
    i = hit + q.length;
  }
  return <>{parts}</>;
}

/** 历史列表每页条数（与后端 size 默认值一致） */
const PAGE_SIZE = 20;

export default function HistoryPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { toast } = useToast();
  // 深链直达任务详情（?task=<id>）：批量下载等任务提交后跳转过来即选中
  const [params, setParams] = useSearchParams();
  const [selected, setSelected] = useState<string | null>(params.get("task"));
  const selectTask = (id: string | null) => {
    setSelected(id);
    setParams(id ? { task: id } : {}, { replace: true });
  };
  const [pendingDelete, setPendingDelete] = useState<Task | null>(null);
  const [toolFilter, setToolFilterState] = useState("");
  const [page, setPage] = useState(1);
  // 筛选变化回第一页：服务端过滤后的页码与全量页码不可混用
  const setToolFilter = (v: string) => {
    setToolFilterState(v);
    setPage(1);
  };
  const [searchQ, setSearchQ] = useState("");
  const [searched, setSearched] = useState("");

  const list = useQuery({
    queryKey: ["tasks", "history", toolFilter, page],
    // 翻页/切筛选期间保留上一页数据，列表不闪骨架屏
    placeholderData: (prev) => prev,
    queryFn: () =>
      fetchJSON<{ items: Task[]; total: number }>(
        `/api/tasks?page=${page}&size=${PAGE_SIZE}${toolFilter ? `&tool=${toolFilter}` : ""}`,
      ),
  });
  const total = list.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));

  /* 总数收缩（如末页仅剩的任务被删）把页码夹回有效范围：渲染期参照上次总数调整，
   * 晚于数据到达一拍的状态修正不进 effect（同步 setState-in-effect 会被编译器拒优化） */
  const [prevTotal, setPrevTotal] = useState(total);
  if (prevTotal !== total) {
    setPrevTotal(total);
    if (page > pageCount) setPage(pageCount);
  }

  const detail = useQuery({
    queryKey: ["task", selected],
    enabled: Boolean(selected),
    queryFn: () => fetchJSON<TaskDetail>(`/api/tasks/${selected}`),
  });

  const search = useQuery({
    queryKey: ["search", searched],
    enabled: searched.trim() !== "",
    queryFn: () =>
      fetchJSON<{ items: SearchHit[]; total: number }>(`/api/search?q=${encodeURIComponent(searched.trim())}`),
  });

  const del = useMutation({
    mutationFn: (id: string) => fetchJSON(`/api/tasks/${id}`, { method: "DELETE" }),
    onSuccess: (_d, id) => {
      toast({ tone: "ok", title: "任务已删除" });
      if (selected === id) selectTask(null);
      setPendingDelete(null);
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });

  /* 重跑 = 后端克隆原任务（params 与输入引用原样回传）；上传文件/产物已被清理时后端报可读错误 */
  const rerun = useMutation({
    mutationFn: (id: string) =>
      fetchJSON<{ task_id: string }>(`/api/tasks/${id}/rerun`, { method: "POST" }),
    onSuccess: (d) => {
      toast({ tone: "ok", title: "已重新提交", description: `新任务 ${d.task_id.slice(0, 8)}，进度见工作台或工具页` });
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "重跑失败", description: e.message }),
  });

  // 类型筛选已下推服务端（/api/tasks?tool=），列表即筛选后的分页切片
  const items = list.data?.items ?? [];
  // 搜索命中但不在当前页的任务：点开后把详情任务置顶补进列表
  const visibleItems =
    selected && !items.some((t) => t.id === selected) && detail.data?.task?.id === selected
      ? [detail.data.task, ...items]
      : items;

  return (
    <>
      <PageHeader
        title="任务记录"
        description="全部任务与产物，可回放、下载、删除"
        actions={
          <>
            <div className="hidden items-center gap-0.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-0.5 sm:flex">
              {filters.map((f) => (
                <button
                  key={f.value || "all"}
                  onClick={() => setToolFilter(f.value)}
                  className={`cursor-pointer rounded-[5px] px-2.5 py-1 text-xs transition-colors duration-150 ${
                    toolFilter === f.value ? "bg-raise text-fg" : "text-muted hover:text-fg-2"
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>
            <IconButton label="刷新" onClick={() => void qc.invalidateQueries({ queryKey: ["tasks"] })}>
              <RefreshCw size={15} strokeWidth={1.75} />
            </IconButton>
          </>
        }
      />

      {/* 转写全文搜索 */}
      <div className="mt-4 flex items-center gap-2">
        <div className="relative max-w-md min-w-0 flex-1">
          <Search size={14} strokeWidth={1.75} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
          <Input
            value={searchQ}
            onChange={(e) => setSearchQ(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && setSearched(searchQ.trim())}
            placeholder="搜索任务记录：转写内容、歌曲名…，回车搜索"
            aria-label="搜索任务记录"
            className="pl-8"
          />
        </div>
        {searched !== "" && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setSearched("");
              setSearchQ("");
            }}
          >
            清除搜索
          </Button>
        )}
      </div>

      {searched !== "" && (
        <Card className="mt-4">
          <CardHeader
            title={`「${searched}」的命中`}
            icon={<Search size={15} strokeWidth={1.75} />}
            aside={
              search.isLoading ? undefined : (
                <span className="micro">{search.data?.total ?? 0} 个任务命中 · 点击查看详情</span>
              )
            }
          />
          {search.isLoading ? (
            <div className="space-y-2 p-4">
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (search.data?.items ?? []).length === 0 ? (
            <EmptyState
              icon={<Search size={18} strokeWidth={1.75} />}
              title="没有命中的任务"
              description="试试其他关键词，或确认任务已完成。"
            />
          ) : (
            <ul className="divide-y divide-line">
              {(search.data?.items ?? []).map((hit) => (
                <li key={hit.task_id}>
                  <button
                    type="button"
                    onClick={() => selectTask(hit.task_id)}
                    className="w-full cursor-pointer px-4 py-3 text-left transition-colors duration-150 hover:bg-raise-2"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm text-fg-2">{toolLabel(hit.tool)}</span>
                      {hit.title && <span className="text-sm">{hit.title}</span>}
                      <span className="font-mono text-[11px] text-muted">{hit.created_at}</span>
                      <span className="font-mono text-[11px] text-muted">{hit.task_id.slice(0, 8)}</span>
                    </div>
                    <ul className="mt-1.5 space-y-1">
                      {hit.matches.map((m, i) => (
                        <li key={i} className="flex items-baseline gap-2 text-xs text-fg-2">
                          {m.end_ms !== undefined && m.end_ms > 0 && (
                            <span className="shrink-0 font-mono text-[11px] tabular-nums text-accent">
                              [{formatTime(m.start_ms / 1000)}]
                            </span>
                          )}
                          <span className="min-w-0 flex-1 truncate">
                            <Highlight text={m.text} q={searched} />
                          </span>
                        </li>
                      ))}
                    </ul>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      <Card className="mt-4">
        <CardHeader
          title="任务列表"
          icon={<Clock size={15} strokeWidth={1.75} />}
          aside={<span className="micro">共 {total} 条</span>}
        />
        {list.isLoading ? (
          <div className="space-y-2 p-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : visibleItems.length === 0 ? (
          <EmptyState
            icon={<Clock size={18} strokeWidth={1.75} />}
            title="没有匹配的任务"
            description="换个筛选条件，或先去工具页发起一个任务。"
          />
        ) : (
          <ul className="divide-y divide-line">
            {visibleItems.map((t) => (
              <li key={t.id}>
                <div className="flex items-center gap-3 px-4 py-2.5 text-sm transition-colors duration-150 hover:bg-raise-2">
                  <button
                    onClick={() => selectTask(selected === t.id ? null : t.id)}
                    className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-left"
                  >
                    <span className="w-20 shrink-0 text-fg-2">{toolLabel(t.tool)}</span>
                    {t.title && (
                      <span className="hidden min-w-0 max-w-48 shrink truncate text-sm sm:inline">{t.title}</span>
                    )}
                    <StatusBadge status={t.status} />
                    <span className="min-w-0 flex-1 truncate text-xs text-muted">
                      {t.progress_note || t.error || "—"}
                    </span>
                  </button>
                  <span className="hidden shrink-0 font-mono text-[11px] text-muted md:inline">
                    {t.cost_ms > 0 ? `${(t.cost_ms / 1000).toFixed(1)}s` : "—"}
                  </span>
                  <span className="shrink-0 font-mono text-[11px] text-muted">{t.created_at}</span>
                  <IconButton
                    label="删除任务"
                    size="sm"
                    variant="ghost"
                    className="hover:text-danger"
                    onClick={() => setPendingDelete(t)}
                  >
                    <Trash2 size={14} strokeWidth={1.75} />
                  </IconButton>
                </div>

                {selected === t.id && (
                  <div className="rise border-t border-line bg-panel px-4 py-3">
                    {detail.isLoading ? (
                      <Skeleton className="h-12 w-full" />
                    ) : detail.data ? (
                      <div className="space-y-3">
                        <TaskDetailPanel
                          d={detail.data}
                          rerunPending={rerun.isPending && rerun.variables === t.id}
                          onRerun={() => rerun.mutate(t.id)}
                        />
                        {/* 分离任务的任务级动作：带两轨以上产物进混音台再加工（与重跑同区，读作任务动作） */}
                        {mixableToMixer(detail.data) && (
                          <div className="flex flex-wrap items-center gap-2">
                            <Button
                              size="sm"
                              variant="secondary"
                              icon={<SlidersHorizontal size={13} strokeWidth={1.75} />}
                              onClick={() => navigate(`/post?tab=mixer&task=${t.id}`)}
                            >
                              进混音台
                            </Button>
                            <span className="text-[11px] text-muted">
                              用人声 + 伴奏两轨垫音 / 半消音 / 包络混音导出
                            </span>
                          </div>
                        )}
                      </div>
                    ) : (
                      <p className="py-2 text-xs text-muted">
                        {t.error ? `失败原因：${t.error}` : "该任务没有产物"}
                      </p>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
        <Pagination page={page} pageCount={pageCount} total={total} onChange={setPage} className="border-t border-line" />
      </Card>

      <ConfirmDialog
        open={Boolean(pendingDelete)}
        title="删除任务"
        description={`将删除「${
          pendingDelete ? toolLabel(pendingDelete.tool) : ""
        }」任务记录及其产物文件，且不可恢复。`}
        confirmLabel="删除"
        loading={del.isPending}
        onConfirm={() => pendingDelete && del.mutate(pendingDelete.id)}
        onCancel={() => setPendingDelete(null)}
      />
    </>
  );
}
