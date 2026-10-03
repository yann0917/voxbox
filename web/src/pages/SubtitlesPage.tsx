import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Captions, Download, FileVideo, Languages, ListTree, Plus, SlidersHorizontal, Trash2, Upload } from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import { streamPostSSEEvents } from "../lib/sse";
import type { TaskDetail } from "../lib/types";
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
  Select,
  Switch,
  Tabs,
  Textarea,
  useToast,
} from "../ui";

/** 与后端 internal/subtitle.Segment / subtitle.Style 对齐 */
interface SubSeg {
  text: string;
  start_ms: number;
  end_ms: number;
  translation?: string;
}

/** GET /api/subtitles/langs 行格式（后端 volcengine.MTLang） */
interface TranslateLang {
  code: string;
  name: string;
}

/** 翻译终帧 stats（echo_checked 仅 AI 源逐条核对计数，UI 不展示） */
interface TranslateStats {
  total: number;
  translated: number;
  repaired: number;
  untranslated: number;
  echo_checked: number;
  source: string;
}

interface PresetStyle {
  name: string;
  font_name: string;
  font_size: number;
  bold: boolean;
  primary: string;
  secondary: string;
  outline: string;
  outline_w: number;
  margin_v: number;
  karaoke: boolean;
}

/** 预设接口未返回时的兜底（与后端第一个预设「琥珀」一致） */
const FALLBACK_STYLE: PresetStyle = {
  name: "琥珀",
  font_name: "Fira Sans",
  font_size: 64,
  bold: true,
  primary: "#FF8A3D",
  secondary: "#F0EAE2",
  outline: "#141210",
  outline_w: 2,
  margin_v: 72,
  karaoke: true,
};

function fmtClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

function parseClockToMs(s: string): number {
  const [m, rest] = s.split(":");
  const sec = Number.parseFloat(rest);
  if (!Number.isFinite(sec)) return 0;
  return Math.round((Number.parseInt(m || "0", 10) * 60 + sec) * 1000);
}

/** 导出：二进制端点不套包络，JSON 响应即业务错误 */
async function downloadExport(segs: SubSeg[], format: "srt" | "ass", style: Record<string, unknown>, toast: (t: { tone: "ok" | "error"; title: string; description?: string }) => void) {
  const resp = await fetch(`${apiBase}/api/subtitles/export`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ segments: segs, format, style }),
  });
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) {
    const j = await resp.json();
    throw new Error(j.message || "导出失败");
  }
  const blob = await resp.blob();
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = format === "ass" ? "subtitles.ass" : "subtitles.srt";
  a.click();
  URL.revokeObjectURL(a.href);
  toast({ tone: "ok", title: "已导出", description: a.download });
}

export default function SubtitlesPage() {
  const [source, setSource] = useState<"task" | "srt" | "text">("task");
  const [segments, setSegments] = useState<SubSeg[]>([]);
  const [preset, setPreset] = useState(FALLBACK_STYLE.name);
  const [fontSize, setFontSize] = useState(64);
  const [marginV, setMarginV] = useState(72);
  const [karaoke, setKaraoke] = useState(true);
  // 文稿草稿输入
  const [draftText, setDraftText] = useState("");
  const [draftMinutes, setDraftMinutes] = useState("3");
  // SRT 粘贴/文件内容
  const [srtContent, setSrtContent] = useState("");
  const [importError, setImportError] = useState("");
  // 翻译：源（auto=按可用性选择）、目标语言、进度与结果统计
  const [trSource, setTrSource] = useState("auto");
  const [trTarget, setTrTarget] = useState("en");
  const [translating, setTranslating] = useState(false);
  const [trProgress, setTrProgress] = useState<{ done: number; total: number } | null>(null);
  const [trStats, setTrStats] = useState<TranslateStats | null>(null);
  const { toast } = useToast();

  const tasks = useQuery({
    queryKey: ["tasks", "subtitles"],
    queryFn: () => fetchJSON<{ items: import("../lib/types").Task[] }>("/api/tasks?size=100"),
  });
  const presets = useQuery({
    queryKey: ["subtitle-presets"],
    queryFn: () => fetchJSON<PresetStyle[]>("/api/subtitles/presets"),
    staleTime: Infinity,
  });
  // 目标语言清单（火山 32 语种，三源统一口径）
  const langs = useQuery({
    queryKey: ["subtitle-langs"],
    queryFn: () => fetchJSON<TranslateLang[]>("/api/subtitles/langs"),
    staleTime: Infinity,
  });
  const hasTranslations = segments.some((s) => (s.translation ?? "").trim() !== "");
  // 当前生效样式：预设接口为单一事实来源（预览颜色/字重/导出覆盖均取自它）
  const style = presets.data?.find((p) => p.name === preset) ?? presets.data?.[0] ?? FALLBACK_STYLE;
  const applyPreset = (name: string) => {
    setPreset(name);
    const p = presets.data?.find((x) => x.name === name);
    if (p) {
      setFontSize(p.font_size);
      setMarginV(p.margin_v);
      setKaraoke(p.karaoke);
    }
  };
  // 配音即字幕：长文本 TTS 勾选时间戳后同样带分句时间戳，与识别/妙记任务同源可导
  const candidates = (tasks.data?.items ?? []).filter(
    (t) =>
      t.status === "succeeded" &&
      (t.tool === "asr" || t.tool === "minutes" || t.tool === "tts_long" || t.tool === "tts_stream"),
  );

  const importFromTask = useMutation({
    mutationFn: async (taskId: string) => {
      const d = await fetchJSON<TaskDetail>(`/api/tasks/${taskId}`);
      const segs = d.task.summary?.segments ?? [];
      if (segs.length === 0) throw new Error("该任务没有分句时间戳");
      return segs;
    },
    onSuccess: (segs) => {
      setSegments(segs);
      setImportError("");
    },
    onError: (e: Error) => setImportError(e.message),
  });

  const prepare = useMutation({
    mutationFn: async () => {
      if (source === "srt") {
        return (
          await fetchJSON<{ segments: SubSeg[] }>("/api/subtitles/prepare", {
            method: "POST",
            body: JSON.stringify({ mode: "srt", content: srtContent }),
          })
        ).segments;
      }
      const durationMS = Math.round(Number.parseFloat(draftMinutes || "0") * 60000);
      return (
        await fetchJSON<{ segments: SubSeg[] }>("/api/subtitles/prepare", {
          method: "POST",
          body: JSON.stringify({ mode: "text", content: draftText, duration_ms: durationMS }),
        })
      ).segments;
    },
    onSuccess: (segs) => {
      setSegments(segs);
      setImportError("");
    },
    onError: (e: Error) => setImportError(e.message),
  });

  const onSrtFile = async (f: File) => {
    setSrtContent(await f.text());
  };

  const updateRow = (i: number, patch: Partial<SubSeg>) => {
    setSegments((prev) => prev.map((s, idx) => (idx === i ? { ...s, ...patch } : s)));
  };
  const removeRow = (i: number) => setSegments((prev) => prev.filter((_, idx) => idx !== i));
  const addRow = () => {
    const last = segments[segments.length - 1];
    const start = last ? last.end_ms : 0;
    setSegments((prev) => [...prev, { text: "", start_ms: start, end_ms: start + 2000 }]);
  };

  const doExport = (format: "srt" | "ass") => {
    downloadExport(segments, format, { name: preset, font_size: fontSize, margin_v: marginV, karaoke }, toast).catch(
      (e: Error) => toast({ tone: "error", title: "导出失败", description: e.message }),
    );
  };

  const doTranslate = async () => {
    if (segments.length === 0) return;
    setTranslating(true);
    setTrProgress(null);
    setTrStats(null);
    try {
      let result: { segments: SubSeg[]; stats: TranslateStats } | undefined;
      await streamPostSSEEvents(
        `${apiBase}/api/subtitles/translate`,
        {
          segments: segments.map((s) => ({ text: s.text, start_ms: s.start_ms, end_ms: s.end_ms })),
          target_language: trTarget,
          source: trSource === "auto" ? undefined : trSource,
        },
        (f) => {
          if (f.progress) setTrProgress(f.progress);
          if (f.error) throw new Error(f.error.message || "翻译失败");
          if (f.done && f.result) result = f.result as { segments: SubSeg[]; stats: TranslateStats };
        },
      );
      if (!result) throw new Error("翻译未返回结果");
      setSegments(result.segments);
      setTrStats(result.stats);
      toast({
        tone: "ok",
        title: "翻译完成",
        description: `共 ${result.stats.total} 条 · ${
          result.stats.source === "ai" ? "AI 翻译" : result.stats.source === "volcengine" ? "火山翻译" : "免费翻译"
        }`,
      });
    } catch (e) {
      toast({ tone: "error", title: "翻译失败", description: e instanceof Error ? e.message : String(e) });
    } finally {
      setTranslating(false);
      setTrProgress(null);
    }
  };

  // 双语 SRT：前端把原文与译文拼为上下两行，复用既有 SRT 导出端点
  const doExportBilingualSRT = () => {
    const merged = segments.map((s) => ({
      ...s,
      text: (s.translation ?? "").trim() !== "" ? `${s.text}\n${s.translation!.trim()}` : s.text,
    }));
    downloadExport(merged, "srt", { name: preset, font_size: fontSize, margin_v: marginV, karaoke }, toast).catch(
      (e: Error) => toast({ tone: "error", title: "导出失败", description: e.message }),
    );
  };

  /* 预览：按 ASS PlayRes 1080p 等比缩放的近似渲染，颜色/字重取自当前预设 */
  const previewSeg = segments.find((s) => s.text.trim() !== "");
  const scale = 0.34; // 预览容器 ≈ 720p 视觉
  const previewChars = previewSeg ? [...previewSeg.text.replace(/\n/g, "")] : [];
  const sungCount = Math.ceil(previewChars.length / 2);
  const previewLine = useMemo(() => {
    if (previewChars.length === 0) return null;
    const shadow = `0 0 2px ${style.outline}, 1px 1px 0 ${style.outline}, -1px -1px 0 ${style.outline}, 1px -1px 0 ${style.outline}, -1px 1px 0 ${style.outline}`;
    return (
      <p
        className="m-0 whitespace-pre-wrap text-center leading-snug"
        style={{
          fontFamily: "var(--font-display, sans-serif)",
          fontWeight: style.bold ? 600 : 400,
          fontSize: `${Math.max(13, Math.round(fontSize * scale * 0.5))}px`,
          marginBottom: `${Math.max(10, Math.round(marginV * scale * 0.5))}px`,
        }}
      >
        {previewChars.map((c, i) => (
          <span
            key={i}
            style={{
              // ASS 语义：非卡拉 OK 字幕可见色=Primary；卡拉 OK 已唱=Primary、未唱=Secondary
              color: karaoke ? (i < sungCount ? style.primary : style.secondary) : style.primary,
              textShadow: shadow,
            }}
          >
            {c}
          </span>
        ))}
      </p>
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewSeg, fontSize, marginV, karaoke, style]);

  return (
    <>
      <PageHeader
        title="字幕工坊"
        description="分句时间戳转样式化字幕：SRT/ASS 导出、卡拉 OK 逐字渲染（本地转换，不消耗额度）"
      />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左：导入 */}
        <Card className="min-w-0">
          <CardHeader title="导入字幕" icon={<Upload size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-3">
            <Tabs<"task" | "srt" | "text">
              items={[
                { value: "task", label: "历史任务", icon: <ListTree size={13} strokeWidth={1.75} /> },
                { value: "srt", label: "SRT 文件", icon: <Upload size={13} strokeWidth={1.75} /> },
                { value: "text", label: "文稿草稿", icon: <FileVideo size={13} strokeWidth={1.75} /> },
              ]}
              value={source}
              onChange={setSource}
            />

            {source === "task" && (
              <div className="space-y-2">
                <Field label="选择已完成的识别/配音任务" hint="取分句时间戳作为字幕轴（火山配音需勾选时间戳输出）">
                  {({ id, ...rest }) => (
                    <Select
                      id={id}
                      value=""
                      onChange={(e) => importFromTask.mutate(e.target.value)}
                      {...rest}
                    >
                      <option value="">{candidates.length > 0 ? "选择任务导入…" : "暂无已完成的识别/配音任务"}</option>
                      {candidates.map((t) => (
                        <option key={t.id} value={t.id}>
                          {t.created_at} · {t.id.slice(0, 8)}
                        </option>
                      ))}
                    </Select>
                  )}
                </Field>
                {importFromTask.isPending && <p className="text-[11px] text-muted">读取任务结果…</p>}
              </div>
            )}

            {source === "srt" && (
              <div className="space-y-2">
                <Field label="粘贴 SRT 内容或选择文件" hint="标准 SRT（00:00:01,000 --> 00:00:02,000），坏块自动跳过">
                  {({ id, ...rest }) => (
                    <Textarea
                      id={id}
                      rows={5}
                      value={srtContent}
                      onChange={(e) => setSrtContent(e.target.value)}
                      placeholder={"1\n00:00:01,000 --> 00:00:03,000\n第一句台词"}
                      {...rest}
                    />
                  )}
                </Field>
                <div className="flex items-center justify-between gap-2">
                  <label className="cursor-pointer text-xs text-fg-2 transition-colors duration-150 hover:text-accent">
                    选择 .srt 文件
                    <input
                      type="file"
                      accept=".srt"
                      className="hidden"
                      onChange={(e) => {
                        const f = e.target.files?.[0];
                        if (f) void onSrtFile(f);
                        e.target.value = "";
                      }}
                    />
                  </label>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={srtContent.trim() === ""}
                    loading={prepare.isPending}
                    onClick={() => prepare.mutate()}
                  >
                    解析 SRT
                  </Button>
                </div>
              </div>
            )}

            {source === "text" && (
              <div className="space-y-2">
                <Field label="粘贴文稿" hint="按句末标点分句，各句按字数比例在总时长内分配时间轴">
                  {({ id, ...rest }) => (
                    <Textarea
                      id={id}
                      rows={5}
                      value={draftText}
                      onChange={(e) => setDraftText(e.target.value)}
                      placeholder="粘贴整段文稿，自动分句生成字幕草稿…"
                      {...rest}
                    />
                  )}
                </Field>
                <div className="flex items-end justify-between gap-2">
                  <Field label="总时长（分钟）" hint="对齐音频实际时长，可在编辑区微调">
                    {({ id, ...rest }) => (
                      <Input
                        id={id}
                        type="number"
                        min={0.1}
                        step={0.1}
                        value={draftMinutes}
                        onChange={(e) => setDraftMinutes(e.target.value)}
                        className="w-32"
                        {...rest}
                      />
                    )}
                  </Field>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={draftText.trim() === ""}
                    loading={prepare.isPending}
                    onClick={() => prepare.mutate()}
                  >
                    生成草稿
                  </Button>
                </div>
              </div>
            )}

            {importError && <p className="text-[11px] text-danger">{importError}</p>}
          </CardBody>
        </Card>

        {/* 右：样式 + 导出 */}
        <Card className="lg:sticky lg:top-4 lg:self-start">
          <CardHeader
            title="样式与导出"
            icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
            aside={<span className="micro">ASS v4+ · 1080p</span>}
          />
          <CardBody className="space-y-4">
            {/* 预览 */}
            <div className="relative flex h-32 items-end justify-center overflow-hidden rounded-[var(--radius-sm)] border border-line bg-gradient-to-br from-[#1c2a3a] via-[#243447] to-[#141210]">
              {previewLine ?? <p className="pb-3 text-[11px] text-muted">导入字幕后在此预览样式</p>}
            </div>

            <Field label="样式预设">
              {({ id, ...rest }) => (
                <Select id={id} value={style.name} onChange={(e) => applyPreset(e.target.value)} {...rest}>
                  {(presets.data ?? []).map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="字号（1080p）">
                {({ id, ...rest }) => (
                  <Input id={id} type="number" min={20} max={120} value={String(fontSize)} onChange={(e) => setFontSize(Number(e.target.value || 64))} {...rest} />
                )}
              </Field>
              <Field label="底边距">
                {({ id, ...rest }) => (
                  <Input id={id} type="number" min={0} max={400} value={String(marginV)} onChange={(e) => setMarginV(Number(e.target.value || 72))} {...rest} />
                )}
              </Field>
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
              <Switch checked={karaoke} onChange={setKaraoke} />
              卡拉 OK 逐字渲染（行内均匀分布）
            </label>

            <div className="space-y-2 border-t border-line pt-3">
              <Button
                variant="secondary"
                className="w-full"
                icon={<Download size={15} strokeWidth={1.75} />}
                disabled={!hasTranslations}
                onClick={doExportBilingualSRT}
              >
                导出双语 SRT
              </Button>
              <Button
                variant="primary"
                className="w-full"
                icon={<Download size={15} strokeWidth={1.75} />}
                disabled={segments.length === 0}
                onClick={() => doExport("ass")}
              >
                导出 ASS
              </Button>
              <Button
                variant="secondary"
                className="w-full"
                icon={<Download size={15} strokeWidth={1.75} />}
                disabled={segments.length === 0}
                onClick={() => doExport("srt")}
              >
                导出 SRT
              </Button>
            </div>
          </CardBody>
        </Card>
      </div>

      {/* 翻译 */}
      <Card className="mt-4">
        <CardHeader
          title="翻译"
          icon={<Languages size={15} strokeWidth={1.75} />}
          aside={<span className="micro">{hasTranslations ? "已有译文，再次翻译将覆盖" : "AI / 火山 / 免费源自动选择"}</span>}
        />
        <CardBody className="space-y-3">
          <div className="flex flex-wrap items-end gap-2">
            <Field label="翻译源" hint="自动 = 按可用性选择（AI → 火山 → 免费）">
              {({ id, ...rest }) => (
                <Select id={id} value={trSource} onChange={(e) => setTrSource(e.target.value)} className="w-40" {...rest}>
                  <option value="auto">自动</option>
                  <option value="ai">AI 翻译</option>
                  <option value="volcengine">火山翻译</option>
                  <option value="free">免费翻译</option>
                </Select>
              )}
            </Field>
            <Field label="目标语言">
              {({ id, ...rest }) => (
                <Select id={id} value={trTarget} onChange={(e) => setTrTarget(e.target.value)} className="w-44" {...rest}>
                  {(langs.data ?? []).map((l) => (
                    <option key={l.code} value={l.code}>
                      {l.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Button
              variant="primary"
              size="sm"
              icon={<Languages size={14} strokeWidth={1.75} />}
              loading={translating}
              disabled={segments.length === 0}
              onClick={doTranslate}
            >
              翻译字幕
            </Button>
            {trProgress && (
              <span className="text-xs text-muted tabular-nums">
                {trProgress.done}/{trProgress.total}
              </span>
            )}
            {trStats && (
              <span className="text-xs text-muted">
                补翻 {trStats.repaired}
                {trStats.untranslated > 0 ? ` · ${trStats.untranslated} 条未按译文返回` : ""}
              </span>
            )}
          </div>
        </CardBody>
      </Card>

      {/* 编辑区 */}
      <Card className="mt-4">
        <CardHeader
          title="字幕编辑"
          icon={<Captions size={15} strokeWidth={1.75} />}
          aside={
            segments.length > 0 ? (
              <span className="font-mono text-[11px] tabular-nums text-muted">{segments.length} 条</span>
            ) : undefined
          }
        />
        {segments.length === 0 ? (
          <EmptyState
            icon={<Captions size={18} strokeWidth={1.75} />}
            title="还没有字幕内容"
            description="从历史任务导入分句、解析 SRT 文件，或粘贴文稿生成草稿。"
          />
        ) : (
          <CardBody className="space-y-2">
            <div className="flex items-center gap-2 text-[11px] text-muted">
              <span className="w-10 shrink-0 text-center">序号</span>
              <span className="w-24 shrink-0 text-center">开始 (mm:ss.d)</span>
              <span className="w-24 shrink-0 text-center">结束 (mm:ss.d)</span>
              <span className="min-w-0 flex-1 pl-3">字幕文本</span>
              {hasTranslations && <span className="min-w-0 flex-1 pl-3">译文</span>}
              <span className="w-8 shrink-0" />
            </div>
            <div className="max-h-[26rem] space-y-1.5 overflow-y-auto">
              {segments.map((seg, i) => (
                <div key={i} className="flex items-center gap-2">
                  <span className="w-10 shrink-0 text-center font-mono text-[11px] tabular-nums text-muted">{i + 1}</span>
                  <input
                    type="text"
                    value={fmtClock(seg.start_ms)}
                    onChange={(e) => updateRow(i, { start_ms: parseClockToMs(e.target.value) })}
                    aria-label={`第 ${i + 1} 条开始时间`}
                    className="w-24 shrink-0 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2 py-1.5 text-center font-mono text-[11px] tabular-nums text-fg focus:border-accent focus:outline-none"
                  />
                  <input
                    type="text"
                    value={fmtClock(seg.end_ms)}
                    onChange={(e) => updateRow(i, { end_ms: parseClockToMs(e.target.value) })}
                    aria-label={`第 ${i + 1} 条结束时间`}
                    className="w-24 shrink-0 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2 py-1.5 text-center font-mono text-[11px] tabular-nums text-fg focus:border-accent focus:outline-none"
                  />
                  <input
                    type="text"
                    value={seg.text}
                    onChange={(e) => updateRow(i, { text: e.target.value })}
                    aria-label={`第 ${i + 1} 条字幕文本`}
                    className="min-w-0 flex-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2 py-1.5 text-sm text-fg focus:border-accent focus:outline-none"
                  />
                  {hasTranslations && (
                    <input
                      type="text"
                      value={seg.translation ?? ""}
                      onChange={(e) => updateRow(i, { translation: e.target.value })}
                      aria-label={`第 ${i + 1} 条译文`}
                      className="min-w-0 flex-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-2 py-1.5 text-sm text-fg-2 focus:border-accent focus:outline-none"
                    />
                  )}
                  <IconButton label={`删除第 ${i + 1} 条`} size="sm" variant="ghost" className="hover:text-danger" onClick={() => removeRow(i)}>
                    <Trash2 size={13} strokeWidth={1.75} />
                  </IconButton>
                </div>
              ))}
            </div>
            <div>
              <Button variant="ghost" size="sm" icon={<Plus size={13} strokeWidth={1.75} />} onClick={addRow}>
                添加一行
              </Button>
            </div>
          </CardBody>
        )}
      </Card>
    </>
  );
}
