import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  CalendarPlus,
  CalendarRange,
  Copy,
  Download,
  ListTodo,
  NotebookPen,
  RefreshCw,
  Sparkles,
  Square,
  type LucideIcon,
} from "lucide-react";
import { useDefaultAssistantModel } from "../../lib/assistant";
import { downloadICS, icsEventCount } from "../../lib/ics";
import type { RefineEvent, RefineTodo, Refined } from "../../lib/types";
import { Button, Card, CardBody, CardHeader, Field, IconButton, Markdown, Textarea, useToast } from "../../ui";
import {
  asEvents,
  asTodos,
  parseRefinedArray,
  REFINE_MODES,
  refinedAtText,
  safeFilename,
  streamRefine,
  type ModeValue,
  type RefineMode,
} from "./model";

export interface RefinePanelProps {
  taskId: string;
  /** 任务标题（导出文件名与文案用） */
  title: string;
  /** 持久化回显：任务 Summary.refined。父级在任务详情到达后才挂载本组件，
   *  回显直接从该 prop 派生（不做异步拉取）；本会话新跑的结果以本地覆盖优先。 */
  refined?: Refined;
}

const MODE_ICONS: Record<RefineMode, LucideIcon> = {
  summary: NotebookPen,
  todos: ListTodo,
  events: CalendarRange,
  custom: Sparkles,
};

/** 加工区：文字稿交给大模型二次加工——总结/待办/事件/自定义四种方式，
 *  SSE 流式出稿；结果按模式共存展示（同模式重跑覆盖），事件可导出 .ics。 */
export function RefinePanel({ taskId, title, refined }: RefinePanelProps) {
  const { toast } = useToast();
  // undefined=配置加载中（保守禁用）；null=未选默认模型（引导去设置页）
  const defaultModel = useDefaultAssistantModel();
  const [mode, setMode] = useState<RefineMode>("summary");
  const [instruction, setInstruction] = useState("");
  const [running, setRunning] = useState(false);
  const [streamText, setStreamText] = useState("");
  const [runError, setRunError] = useState("");
  // 本会话加工结果覆盖层：键=模式。任务详情不回拉，靠流结束时的归一结果直接上屏
  const [overrides, setOverrides] = useState<Partial<Record<RefineMode, ModeValue>>>({});
  const [runAt, setRunAt] = useState("");
  const abortRef = useRef<AbortController | null>(null);

  // 卸载即中止未完成的流
  useEffect(() => () => abortRef.current?.abort(), []);

  /** 展示值：本会话新跑结果优先，回落持久化的 Summary.refined */
  const valueOf = (m: RefineMode): ModeValue | undefined => overrides[m] ?? refined?.[m];
  const updatedAt = refinedAtText(runAt || refined?.updated_at);
  const customReady = mode !== "custom" || instruction.trim() !== "";
  const canRun = !!defaultModel && customReady && !running;

  const copyText = (text: string, what: string) =>
    navigator.clipboard
      .writeText(text)
      .then(() => toast({ tone: "ok", title: `${what}已复制` }))
      .catch((e: Error) => toast({ tone: "error", title: "复制失败", description: e.message }));

  /** 发起一次加工：流式累积上屏，结束后按模式归一（todos/events 解析失败存原文，
   *  与后端落盘兜底同口径，结果块据此给「重试」入口）。 */
  const start = async (m: RefineMode) => {
    if (!defaultModel || running) return;
    if (m === "custom" && !instruction.trim()) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setStreamText("");
    setRunError("");
    setRunning(true);
    let acc = "";
    try {
      await streamRefine(
        {
          task_id: taskId,
          mode: m,
          provider: defaultModel.provider,
          model: defaultModel.model,
          ...(m === "custom" ? { instruction: instruction.trim() } : {}),
        },
        (delta) => {
          acc += delta;
          setStreamText(acc);
        },
        ctrl.signal,
      );
      if (!acc.trim()) {
        setRunError("没有返回内容，请重试。");
      } else if (m === "todos" || m === "events") {
        const arr = parseRefinedArray(acc);
        setOverrides((cur) => ({
          ...cur,
          [m]: arr ? (m === "todos" ? asTodos(arr) : asEvents(arr)) : acc,
        }));
        setRunAt(new Date().toISOString());
      } else {
        setOverrides((cur) => ({ ...cur, [m]: acc.trim() }));
        setRunAt(new Date().toISOString());
      }
    } catch (e) {
      // 主动停止不是错误
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        setRunError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setRunning(false);
      abortRef.current = null;
    }
  };

  const stop = () => abortRef.current?.abort();

  return (
    <Card>
      <CardHeader
        title="AI 加工"
        icon={<Sparkles size={15} strokeWidth={1.75} />}
        aside={updatedAt ? <span className="micro">加工于 {updatedAt}</span> : undefined}
      />
      <CardBody className="space-y-4">
        {defaultModel === null ? (
          <p className="text-xs text-fg-2">
            还没有选择 AI 模型：先在{" "}
            <Link to="/settings" className="text-accent hover:opacity-80">
              设置页
            </Link>{" "}
            选好「AI 默认大模型」，再来加工文字稿。
          </p>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              {REFINE_MODES.map((m) => {
                const selected = mode === m.value;
                return (
                  <button
                    key={m.value}
                    type="button"
                    aria-pressed={selected}
                    disabled={running}
                    onClick={() => setMode(m.value)}
                    className={`flex cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
                      selected
                        ? "border-accent bg-accent/10 text-accent"
                        : "border-line bg-raise-2 text-fg-2 hover:border-accent/60 hover:text-fg"
                    }`}
                  >
                    {(() => {
                      const Icon = MODE_ICONS[m.value];
                      return <Icon size={12} strokeWidth={1.75} />;
                    })()}
                    {m.label}
                  </button>
                );
              })}
            </div>
            {mode === "custom" && (
              <Field label="加工指令" hint="用一句话说明想让 AI 怎么处理这篇文字稿">
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    rows={2}
                    value={instruction}
                    maxLength={500}
                    onChange={(e) => setInstruction(e.target.value)}
                    placeholder="如：把讨论整理成带负责人和截止时间的项目周报"
                    {...rest}
                  />
                )}
              </Field>
            )}
            <div className="flex items-center gap-2">
              {running ? (
                <Button variant="secondary" size="sm" icon={<Square size={13} strokeWidth={1.75} />} onClick={stop}>
                  停止
                </Button>
              ) : (
                <Button
                  variant="primary"
                  size="sm"
                  icon={<Sparkles size={13} strokeWidth={1.75} />}
                  disabled={!canRun}
                  onClick={() => void start(mode)}
                >
                  开始加工
                </Button>
              )}
              {defaultModel && !running && (
                <span className="text-[11px] text-muted">
                  使用默认模型 {defaultModel.provider} · {defaultModel.model}
                </span>
              )}
            </div>
          </>
        )}

        {running && (
          <div className="rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2.5">
            {streamText ? (
              mode === "todos" || mode === "events" ? (
                <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-fg-2">
                  {streamText}
                </pre>
              ) : (
                <div className="max-h-72 overflow-y-auto">
                  <Markdown>{streamText}</Markdown>
                </div>
              )
            ) : (
              <p className="text-xs text-muted">正在阅读文字稿并加工…</p>
            )}
          </div>
        )}

        {runError && (
          <p className="flex items-start gap-1.5 text-xs text-danger">
            <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
            <span className="min-w-0 break-words">{runError}</span>
          </p>
        )}

        {!running && !runError && REFINE_MODES.every((m) => valueOf(m.value) === undefined) && (
          <p className="text-xs text-muted">
            选一种方式，AI 会基于这篇文字稿提炼总结、待办或日程；结果保存在这条任务里，刷新后仍在。
          </p>
        )}

        <div className="space-y-3">
          {REFINE_MODES.map(({ value: m, label }) => {
            const v = valueOf(m);
            if (v === undefined) return null;
            return (
              <ResultBlock
                key={m}
                mode={m}
                label={label}
                value={v}
                docTitle={title}
                running={running}
                modelMissing={defaultModel === null}
                onRerun={() => {
                  setMode(m);
                  void start(m);
                }}
                onCopy={(text) => copyText(text, `${label}结果`)}
              />
            );
          })}
        </div>
      </CardBody>
    </Card>
  );
}

/** 单模式结果块：文本模式走 markdown 渲染；todos/events 结构化列表（事件可导出
 *  日历）；持久值为原文字符串 = 上次解析失败，按原文展示并给重试入口。
 *  （mode 与 value 的配对由构造方保证：字符串仅 summary/custom/todos·events 兜底） */
function ResultBlock({
  mode,
  label,
  value,
  docTitle,
  running,
  modelMissing,
  onRerun,
  onCopy,
}: {
  mode: RefineMode;
  label: string;
  value: ModeValue;
  /** 任务标题（事件批量导出的文件名） */
  docTitle: string;
  running: boolean;
  /** 未配置默认大模型：重新加工是点了也不会有结果的死按钮，禁用并提示 */
  modelMissing: boolean;
  onRerun: () => void;
  onCopy: (text: string) => void;
}) {
  const Icon = MODE_ICONS[mode];
  const rerunTitle = modelMissing ? "先在设置页选好「AI 默认大模型」再加工" : undefined;
  return (
    <div className="space-y-2 rounded-[var(--radius-md)] border border-line bg-raise-2/30 p-3">
      <div className="flex items-center justify-between gap-2">
        <p className="micro flex items-center gap-1.5">
          <Icon size={12} strokeWidth={1.75} />
          {label}
        </p>
        <div className="flex items-center gap-0.5">
          {(mode === "summary" || mode === "custom") && typeof value === "string" && (
            <IconButton size="sm" label={`复制${label}结果`} onClick={() => onCopy(value)}>
              <Copy size={13} strokeWidth={1.75} />
            </IconButton>
          )}
          <IconButton
            size="sm"
            label={rerunTitle ?? "重新加工"}
            disabled={running || modelMissing}
            onClick={onRerun}
          >
            <RefreshCw size={13} strokeWidth={1.75} />
          </IconButton>
        </div>
      </div>
      {typeof value === "string" ? (
        mode === "todos" || mode === "events" ? (
          <RawFallback
            label={label}
            text={value}
            disabled={running}
            modelMissing={modelMissing}
            onRetry={onRerun}
          />
        ) : (
          <Markdown>{value}</Markdown>
        )
      ) : mode === "todos" ? (
        <TodoList todos={value as RefineTodo[]} />
      ) : (
        <EventList events={value as RefineEvent[]} docTitle={docTitle} />
      )}
    </div>
  );
}

/** todos 列表：事项 + 负责人/截止（原文字段，未提到则不显） */
function TodoList({ todos }: { todos: RefineTodo[] }) {
  const items = todos.filter((t) => t.content.trim());
  if (items.length === 0) return <p className="text-xs text-muted">这段录音里没有提取到待办。</p>;
  return (
    <ul className="space-y-1.5">
      {items.map((t, i) => (
        <li key={i} className="flex items-start gap-2 text-[13px] leading-relaxed">
          <span className="mt-[7px] size-1.5 shrink-0 rounded-full bg-accent/70" aria-hidden="true" />
          <span className="min-w-0 flex-1 break-words text-fg">
            {t.content}
            {(t.owner || t.due) && (
              <span className="ml-1.5 text-[11px] text-muted">
                {t.owner}
                {t.owner && t.due ? " · " : ""}
                {t.due}
              </span>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** events 列表：时间 + 标题 + 上下文，逐条「加入日历」，整体「全部导出」 */
function EventList({ events, docTitle }: { events: RefineEvent[]; docTitle: string }) {
  const { toast } = useToast();
  const items = events.filter((e) => e.title.trim() && e.start.trim());
  if (items.length === 0) return <p className="text-xs text-muted">这段录音里没有提取到日程安排。</p>;
  const exportAll = () => {
    downloadICS(items, `${safeFilename(docTitle || "日程")}.ics`);
    // 对账：start 无法解析的事件不会写进文件（非法 DTSTART 会被导入端整份拒绝）
    const missing = items.length - icsEventCount(items);
    if (missing > 0) {
      toast({ tone: "warn", title: "日历文件已下载", description: `${missing} 条时间无法解析，未包含在日历文件中` });
    } else {
      toast({ tone: "ok", title: "日历文件已下载", description: "导入系统日历即可查看这些安排" });
    }
  };
  const exportOne = (ev: RefineEvent) => {
    downloadICS([ev], `${safeFilename(ev.title)}.ics`);
    toast({ tone: "ok", title: "日历文件已下载", description: "导入系统日历即可查看这条安排" });
  };
  return (
    <div className="space-y-2">
      <ul className="space-y-1.5">
        {items.map((ev, i) => (
          <li
            key={i}
            className="flex items-center justify-between gap-2 rounded-[var(--radius-sm)] border border-line bg-inset px-2.5 py-2"
          >
            <div className="min-w-0">
              <p className="truncate text-[13px] text-fg">{ev.title}</p>
              <p className="mt-0.5 truncate font-mono text-[11px] tabular-nums text-muted">
                {ev.start}
                {ev.end ? ` ~ ${ev.end}` : ""}
                {ev.description ? ` · ${ev.description}` : ""}
              </p>
            </div>
            <Button
              size="sm"
              variant="ghost"
              className="shrink-0"
              icon={<CalendarPlus size={13} strokeWidth={1.75} />}
              onClick={() => exportOne(ev)}
            >
              加入日历
            </Button>
          </li>
        ))}
      </ul>
      <Button size="sm" variant="secondary" icon={<Download size={13} strokeWidth={1.75} />} onClick={exportAll}>
        全部导出日历
      </Button>
    </div>
  );
}

/** 解析失败兜底：持久层存的是原文字符串——按原文展示，给重试入口 */
function RawFallback({
  label,
  text,
  disabled,
  modelMissing,
  onRetry,
}: {
  label: string;
  text: string;
  disabled: boolean;
  /** 未配置默认大模型：重试同样禁用并提示 */
  modelMissing: boolean;
  onRetry: () => void;
}) {
  return (
    <div className="space-y-2">
      <p className="flex items-start gap-1.5 text-[11px] text-muted">
        <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
        上次「{label}」的结果没能解析成列表，先按原文展示，可以重试一次。
      </p>
      <pre className="max-h-64 overflow-y-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 font-mono text-[12px] leading-relaxed text-fg-2">
        {text}
      </pre>
      <Button
        size="sm"
        variant="secondary"
        icon={<RefreshCw size={13} strokeWidth={1.75} />}
        disabled={disabled || modelMissing}
        title={modelMissing ? "先在设置页选好「AI 默认大模型」再加工" : undefined}
        onClick={onRetry}
      >
        重试
      </Button>
    </div>
  );
}
