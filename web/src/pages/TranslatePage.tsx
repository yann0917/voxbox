import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeftRight,
  ArrowUpRight,
  Check,
  ChevronDown,
  ChevronUp,
  Copy,
  Languages,
  RefreshCw,
  Send,
  SlidersHorizontal,
} from "lucide-react";
import { Link } from "react-router-dom";
import { fetchJSON } from "../lib/api";
import { DictFill } from "../components/DictFill";
import type { TaskDetail, TaskStatus } from "../lib/types";
import { useTaskEvents } from "../lib/ws";
import { ArtifactRow } from "../components/ArtifactRow";
import { PRICE_MT, PRICE_SNAPSHOT_DATE, MT_OUTPUT_PRICE } from "../lib/pricing";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Field,
  IconButton,
  Input,
  MicroLabel,
  PageHeader,
  ProgressBar,
  Select,
  Skeleton,
  StatusBadge,
  Textarea,
  useToast,
} from "../ui";

/** 官方语言支持：32 语种（ISO 639-1 / BCP-47） */
const MT_LANGUAGES = [
  { code: "zh", name: "中文（简体）" },
  { code: "en", name: "英语" },
  { code: "ja", name: "日语" },
  { code: "ko", name: "韩语" },
  { code: "fr", name: "法语" },
  { code: "de", name: "德语" },
  { code: "es", name: "西班牙语" },
  { code: "pt", name: "葡萄牙语" },
  { code: "ru", name: "俄语" },
  { code: "ar", name: "阿拉伯语" },
  { code: "it", name: "意大利语" },
  { code: "nl", name: "荷兰语" },
  { code: "pl", name: "波兰语" },
  { code: "ro", name: "罗马尼亚语" },
  { code: "sv", name: "瑞典语" },
  { code: "da", name: "丹麦语" },
  { code: "nb", name: "挪威语" },
  { code: "fi", name: "芬兰语" },
  { code: "hu", name: "匈牙利语" },
  { code: "cs", name: "捷克语" },
  { code: "hr", name: "克罗地亚语" },
  { code: "el", name: "希腊语" },
  { code: "he", name: "希伯来语" },
  { code: "tr", name: "土耳其语" },
  { code: "uk", name: "乌克兰语" },
  { code: "th", name: "泰语" },
  { code: "vi", name: "越南语" },
  { code: "id", name: "印度尼西亚语" },
  { code: "ms", name: "马来语" },
  { code: "tl", name: "菲律宾语" },
  { code: "hi", name: "印地语" },
  { code: "zh-Hant", name: "中文（繁体）" },
];

const langName = (code: string) => MT_LANGUAGES.find((l) => l.code === code)?.name ?? code;

/** 单条文本上限 1024 Tokens（官方）；中文字符≈token 量级，超出仅提示、不拦提交 */
const TOKEN_HINT_CHARS = 1024;

interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

export default function TranslatePage() {
  const [text, setText] = useState("");
  const [sourceLang, setSourceLang] = useState("");
  const [targetLang, setTargetLang] = useState("en");
  const [terms, setTerms] = useState("");
  const [tableId, setTableId] = useState("");
  const [tableName, setTableName] = useState("");
  const [termsOpen, setTermsOpen] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const [copied, setCopied] = useState(false);
  const textWrapRef = useRef<HTMLDivElement>(null);
  const focusText = () => textWrapRef.current?.querySelector("textarea")?.focus();
  const { toast } = useToast();
  const ev = useTaskEvents();

  /* WS 事件驱动进度；终态回读任务详情取译文与产物 */
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
          if (d.task.status === "failed") {
            toast({ tone: "error", title: "翻译失败", description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
          toast({ tone: "error", title: "读取任务结果失败", description: e.message });
        });
    }
  }, [ev, taskId, toast]);

  const submit = useMutation({    mutationFn: () =>
      fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({
          provider: "volcengine",
          tool: "translate",
          params: {
            text,
            source_language: sourceLang,
            target_language: targetLang,
            terms,
            glossary_table_id: tableId,
            glossary_table_name: tableName,
          },
        }),
      }),
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setSubmitError("");
      setCopied(false);
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  const trimmed = text.replace(/\n+$/, "");
  const charCount = Array.from(trimmed).length;
  const sameLang = sourceLang !== "" && sourceLang === targetLang;
  const textError = sameLang
    ? "源语言与目标语言相同，请修改语言或清空源语言改为自动检测。"
    : undefined;
  const canSubmit = trimmed.trim() !== "" && !sameLang;

  const swapLang = () => {
    // 源为「自动检测」时无对应目标语言可换，仅提示用户先指定源语言
    if (sourceLang === "") return;
    setSourceLang(targetLang);
    setTargetLang(sourceLang);
  };

  const artifacts = detail?.artifacts ?? [];
  const task = detail?.task;
  const summary = task?.summary;
  const translation = summary?.translation ?? "";
  const detected = summary?.detected_source_language;

  const copyTranslation = async () => {
    try {
      await navigator.clipboard.writeText(translation);
      setCopied(true);
      toast({ tone: "ok", title: "译文已复制" });
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toast({ tone: "error", title: "复制失败", description: "浏览器拒绝了剪贴板访问，可手动选中复制。" });
    }
  };

  return (
    <>
      <PageHeader
        title="机器翻译"
        description="大模型机器翻译：32 语种互译、自动检测源语言、术语定制（需开通 volc.speech.mt）"
        icon={<Languages size={16} strokeWidth={1.75} />}
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

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左：原文编辑区 */}
        <Card className="min-w-0">
          <CardHeader
            title="原文"
            icon={<Languages size={15} strokeWidth={1.75} />}
            aside={
              <span
                className={`font-mono text-[11px] tabular-nums ${charCount > TOKEN_HINT_CHARS ? "text-warn" : "text-muted"}`}
              >
                {charCount} 字
              </span>
            }
          />
          <CardBody className="space-y-3">
            <div ref={textWrapRef}>
              <Field
                label="待翻译文本"
                hint="单条文本不超过 1024 Tokens；超出会被上游拒绝（错误码 45000130），请分段提交。"
                error={textError}
              >
                {({ id, ...rest }) => (
                  <Textarea
                    id={id}
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                    rows={10}
                    placeholder="粘贴或输入要翻译的文本…"
                    className="min-h-[240px] max-h-[60vh]"
                    {...rest}
                  />
                )}
              </Field>
            </div>
            {charCount > TOKEN_HINT_CHARS && (
              <p className="flex items-start gap-1.5 text-[11px] text-warn">
                <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                当前 {charCount} 字，可能超出单条 1024 Tokens 上限（不同语言分词不同，实际以服务端判定为准）。
              </p>
            )}
          </CardBody>
        </Card>

        {/* 右：语言与术语参数 */}
        <Card className="lg:sticky lg:top-4 lg:self-start">
          <CardHeader
            title="翻译参数"
            icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
            aside={<span className="micro">volcengine · translate</span>}
          />
          <CardBody className="space-y-4">
            <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-end gap-2">
              <Field label="源语言">
                {({ id, ...rest }) => (
                  <Select id={id} value={sourceLang} onChange={(e) => setSourceLang(e.target.value)} {...rest}>
                    <option value="">自动检测</option>
                    {MT_LANGUAGES.map((l) => (
                      <option key={l.code} value={l.code}>
                        {l.name}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <IconButton
                label={sourceLang === "" ? "源语言为自动检测时无法交换" : "交换源语言与目标语言"}
                variant="secondary"
                size="sm"
                className="mb-0.5"
                disabled={sourceLang === ""}
                onClick={swapLang}
              >
                <ArrowLeftRight size={14} strokeWidth={1.75} />
              </IconButton>
              <Field label="目标语言" required>
                {({ id, ...rest }) => (
                  <Select id={id} value={targetLang} onChange={(e) => setTargetLang(e.target.value)} {...rest}>
                    {MT_LANGUAGES.map((l) => (
                      <option key={l.code} value={l.code}>
                        {l.name}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            </div>

            <p className="font-mono text-[11px] tabular-nums text-muted">
              {sourceLang === "" ? "自动检测" : langName(sourceLang)} → {langName(targetLang)}
            </p>

            {/* 术语：默认收起 */}
            <div className="border-t border-line pt-3">
              <button
                type="button"
                aria-expanded={termsOpen}
                onClick={() => setTermsOpen((v) => !v)}
                className="flex w-full cursor-pointer items-center justify-between gap-2 py-1 text-left"
              >
                <MicroLabel>术语定制</MicroLabel>
                <span className="text-muted">
                  {termsOpen ? <ChevronUp size={14} strokeWidth={1.75} /> : <ChevronDown size={14} strokeWidth={1.75} />}
                </span>
              </button>
              {termsOpen && (
                <div className="space-y-4 pt-3">
                  <div className="space-y-2">
                    <Field
                      label="直传术语"
                      hint="每行一条「原词=译词」，也可用逗号分隔；直传术语优先于术语表。"
                    >
                      {({ id, ...rest }) => (
                        <Textarea
                          id={id}
                          value={terms}
                          onChange={(e) => setTerms(e.target.value)}
                          rows={4}
                          placeholder={"Volcengine=火山引擎\nBigModel=大模型"}
                          {...rest}
                        />
                      )}
                    </Field>
                    <DictFill field="terms" onFill={setTerms} />
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <Field label="术语表 ID">
                      {({ id, ...rest }) => (
                        <Input id={id} value={tableId} onChange={(e) => setTableId(e.target.value)} placeholder="可选" {...rest} />
                      )}
                    </Field>
                    <Field label="术语表名称">
                      {({ id, ...rest }) => (
                        <Input
                          id={id}
                          value={tableName}
                          onChange={(e) => setTableName(e.target.value)}
                          placeholder="可选"
                          {...rest}
                        />
                      )}
                    </Field>
                  </div>
                </div>
              )}
            </div>

            <div className="border-t border-line pt-3">
              <Button
                variant="primary"
                className="w-full"
                icon={<Send size={15} strokeWidth={1.75} />}
                loading={submit.isPending}
                disabled={!canSubmit}
                onClick={() => submit.mutate()}
              >
                翻译
              </Button>
              {submitError && (
                <p className="mt-2 flex items-start gap-1.5 text-[11px] text-danger">
                  <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                  {submitError}
                </p>
              )}
              <p className="mt-2 text-[11px] leading-relaxed text-muted">
                计费：输入 {PRICE_MT.postpaid[0].price} 元/百万 token、输出 {MT_OUTPUT_PRICE} 元/百万 token（刊例快照{" "}
                {PRICE_SNAPSHOT_DATE}），同量对比见{" "}
                <Link to="/pricing" className="text-accent transition-colors duration-150 hover:opacity-80">
                  计费测算
                </Link>
                。
              </p>
            </div>
          </CardBody>
        </Card>
      </div>

      {/* 结果区 */}
      <Card className="mt-4">
        <CardHeader
          title="译文"
          icon={<Languages size={15} strokeWidth={1.75} />}
          aside={
            <div className="flex items-center gap-2">
              {run && <StatusBadge status={run.status} />}
              {translation !== "" && (
                <Button
                  variant="secondary"
                  size="sm"
                  icon={copied ? <Check size={13} strokeWidth={1.75} /> : <Copy size={13} strokeWidth={1.75} />}
                  onClick={() => void copyTranslation()}
                >
                  {copied ? "已复制" : "复制"}
                </Button>
              )}
            </div>
          }
        />
        {!run ? (
          <EmptyState
            icon={<Languages size={18} strokeWidth={1.75} />}
            title="还没有翻译任务"
            description="输入原文、选择目标语言后点击翻译，译文与文本文件会出现在这里，可直接复制或下载。"
            action={
              <Button variant="secondary" size="sm" onClick={focusText}>
                去输入原文
              </Button>
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
                重新提交
              </Button>
              <span className="font-mono text-[11px] text-muted">{taskId?.slice(0, 8)}</span>
            </div>
          </CardBody>
        ) : run.status !== "succeeded" ? (
          <CardBody className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <StatusBadge status={run.status} />
              <span className="font-mono text-[11px] tabular-nums text-muted">{run.progress}%</span>
            </div>
            <ProgressBar value={run.progress} active={run.status === "running" || run.status === "pending"} />
            <p className="text-xs text-muted">{run.note || "处理中"}</p>
            <Skeleton className="h-24 w-full" />
          </CardBody>
        ) : (
          <CardBody className="space-y-3">
            {translation ? (
              <div className="rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
                <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-fg">{translation}</p>
              </div>
            ) : (
              <p className="text-xs text-muted">任务已完成，但未返回译文（可在历史页查看该任务产物）。</p>
            )}

            {artifacts.map((a) => (
              <ArtifactRow key={a.id} a={a} />
            ))}

            <p className="pt-1 font-mono text-[11px] tabular-nums text-muted">
              {summary?.source_language ? langName(summary.source_language) : detected ? `自动检测 · ${langName(detected)}` : "自动检测"}
              {" → "}
              {summary?.target_language ? langName(summary.target_language) : langName(targetLang)}
              {summary?.char_count != null ? ` · ${summary.char_count} 字` : ""}
              {summary?.terms_count ? ` · 术语 ${summary.terms_count} 条` : ""}
              {summary?.total_tokens != null ? ` · ${summary.total_tokens} token` : ""}
              {task && task.cost_ms > 0 ? ` · ${(task.cost_ms / 1000).toFixed(2)}s` : ""}
            </p>
          </CardBody>
        )}
      </Card>
    </>
  );
}
