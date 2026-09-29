import { useEffect, useRef, useState } from "react";
import { PenLine, Sparkles, Square } from "lucide-react";
import { Button, Field, Modal, Select, Textarea, useToast, WaveLoader } from "../../ui";
import { promptRef, streamApplyPrompt, usePrompts, type PromptItem } from "../../lib/prompts";

/** AI 写作：挂在各合成引擎面板「合成文本」卡的 CardHeader aside（字数徽标旁）。
 *  两个入口——AI 生成（按主题出稿）与 AI 润色（改写文本框已有内容）；都是弹窗内
 *  流式预览，确认后才改动文本框，不直接覆盖用户输入。默认大模型在设置页统一配置
 *  （「AI 默认大模型」，与悬浮助手共用），此处不出现模型选择。 */

type LengthTier = "short" | "medium" | "long";

const LENGTH_OPTIONS: { value: LengthTier; label: string }[] = [
  { value: "short", label: "短 · 150 字内" },
  { value: "medium", label: "中 · 400 字左右" },
  { value: "long", label: "长 · 800 字左右" },
];

const itemRef = (p: PromptItem) => (p.source === "builtin" ? `builtin:${p.key}` : `user:${p.id}`);

/** 生成/润色共用的流式执行钩子：run 发起请求，逐段累积 preview，可中止。 */
function useStreaming() {
  const [preview, setPreview] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState("");
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => () => abortRef.current?.abort(), []);

  const run = async (req: Parameters<typeof streamApplyPrompt>[0]) => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setPreview("");
    setError("");
    setStreaming(true);
    let acc = "";
    try {
      await streamApplyPrompt(
        req,
        (delta) => {
          acc += delta;
          setPreview(acc);
        },
        ctrl.signal,
      );
      if (!acc.trim()) setError("没有返回内容，请重试。");
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        setError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  };
  const stop = () => abortRef.current?.abort();
  return { preview, streaming, error, run, stop, reset: () => { setPreview(""); setError(""); } };
}

/** 生成弹窗：选主题 → 可选要点与篇幅 → 流式预览 → 插入/替换/追加到文本框。 */
function GenerateModal({ open, onClose, value, onChange }: { open: boolean; onClose: () => void; value: string; onChange: (v: string) => void }) {
  const { data } = usePrompts();
  const { toast } = useToast();
  const items = (data?.items ?? []).filter((p) => p.kind === "generate");
  const [pickedRaw, setPicked] = useState("");
  const [topic, setTopic] = useState("");
  const [length, setLength] = useState<LengthTier>("medium");
  const { preview, streaming, error, run, stop, reset } = useStreaming();

  // 关窗即中止未完成的流
  useEffect(() => {
    if (!open) stop();
  }, [open, stop]);

  // 默认选中第一个内置主题（选中项被删时自动回落），渲染期派生
  const picked = items.some((p) => itemRef(p) === pickedRaw) ? pickedRaw : items.length > 0 ? itemRef(items[0]) : "";

  const chosen = items.find((p) => itemRef(p) === picked);
  const req = chosen ? { ...promptRef(chosen), input: topic.trim(), length } : null;

  const insert = (mode: "replace" | "append") => {
    if (!preview.trim()) return;
    onChange(mode === "replace" ? preview : `${value ? value.replace(/\s+$/, "") + "\n\n" : ""}${preview}`);
    toast({ tone: "ok", title: mode === "replace" ? "已替换文本框内容" : "已追加到文本框末尾" });
    reset();
    setTopic("");
    onClose();
  };

  return (
    <Modal open={open} onClose={onClose} title="AI 生成文本" width={520}
      footer={
        <>
          {streaming ? (
            <Button variant="secondary" icon={<Square size={14} strokeWidth={2} />} onClick={stop}>
              停止
            </Button>
          ) : (
            <Button variant="secondary" disabled={!req || !!error} onClick={() => req && void run(req)}>
              {preview ? "重新生成" : "生成"}
            </Button>
          )}
          {preview.trim() && !streaming &&
            (value.trim() ? (
              <>
                <Button variant="secondary" onClick={() => insert("append")}>
                  追加末尾
                </Button>
                <Button variant="primary" onClick={() => insert("replace")}>
                  替换文本框
                </Button>
              </>
            ) : (
              <Button variant="primary" onClick={() => insert("replace")}>
                插入文本框
              </Button>
            ))}
        </>
      }
    >
      <div className="space-y-4">
        <Field label="主题" hint={chosen?.description}>
          {({ id, ...rest }) => (
            <Select id={id} value={picked} onChange={(e) => { setPicked(e.target.value); reset(); }} {...rest}>
              <optgroup label="内置主题">
                {items.filter((p) => p.source === "builtin").map((p) => (
                  <option key={itemRef(p)} value={itemRef(p)}>{p.name}</option>
                ))}
              </optgroup>
              {items.some((p) => p.source === "user") && (
                <optgroup label="我的提示词">
                  {items.filter((p) => p.source === "user").map((p) => (
                    <option key={itemRef(p)} value={itemRef(p)}>{p.name}</option>
                  ))}
                </optgroup>
              )}
            </Select>
          )}
        </Field>
        <Field label="主题 / 要点" aside="可选" hint="一句话说明想讲什么；留空由 AI 自拟">
          {({ id, ...rest }) => (
            <Textarea id={id} rows={2} value={topic} onChange={(e) => setTopic(e.target.value)}
              placeholder="如：画蛇添足 / 产品名、场合、想突出的点…" {...rest} />
          )}
        </Field>
        <Field label="篇幅">
          {({ id, ...rest }) => (
            <Select id={id} value={length} onChange={(e) => setLength(e.target.value as LengthTier)} {...rest}>
              {LENGTH_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </Select>
          )}
        </Field>
        <Preview preview={preview} streaming={streaming} error={error} emptyHint="点击「生成」预览，确认后再放入文本框。" />
      </div>
    </Modal>
  );
}

/** 润色弹窗：选方向 → 对文本框现有内容流式改写 → 确认替换。 */
function PolishModal({ open, onClose, value, onChange }: { open: boolean; onClose: () => void; value: string; onChange: (v: string) => void }) {
  const { data } = usePrompts();
  const { toast } = useToast();
  const items = (data?.items ?? []).filter((p) => p.kind === "polish");
  const [pickedRaw, setPicked] = useState("");
  const { preview, streaming, error, run, stop, reset } = useStreaming();

  useEffect(() => {
    if (!open) stop();
  }, [open, stop]);

  // 默认选中第一个润色方向（选中项失效时自动回落），渲染期派生
  const picked = items.some((p) => itemRef(p) === pickedRaw) ? pickedRaw : items.length > 0 ? itemRef(items[0]) : "";

  const chosen = items.find((p) => itemRef(p) === picked);
  const source = value.trim();

  const replace = () => {
    if (!preview.trim()) return;
    onChange(preview);
    toast({ tone: "ok", title: "已替换文本框内容" });
    reset();
    onClose();
  };

  return (
    <Modal open={open} onClose={onClose} title="AI 润色" width={520}
      footer={
        <>
          {streaming ? (
            <Button variant="secondary" icon={<Square size={14} strokeWidth={2} />} onClick={stop}>
              停止
            </Button>
          ) : (
            <Button variant="secondary" disabled={!chosen || !source} onClick={() => chosen && void run({ ...promptRef(chosen), input: source })}>
              {preview ? "重新润色" : "润色"}
            </Button>
          )}
          {preview.trim() && !streaming && (
            <Button variant="primary" onClick={replace}>
              替换文本框
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        <Field label="润色方向" hint={chosen?.description}>
          {({ id, ...rest }) => (
            <Select id={id} value={picked} onChange={(e) => { setPicked(e.target.value); reset(); }} {...rest}>
              {items.map((p) => (
                <option key={itemRef(p)} value={itemRef(p)}>{p.name}</option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="原文" aside={<span className="font-mono text-[11px] tabular-nums text-muted">{Array.from(source).length} 字</span>}>
          {() =>
            source ? (
              <div className="max-h-32 overflow-y-auto rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 text-xs leading-relaxed whitespace-pre-wrap break-words text-fg-2">
                {value}
              </div>
            ) : (
              <p className="rounded-[var(--radius-sm)] border border-dashed border-line px-3 py-3 text-xs text-muted">
                文本框还没有内容：先输入或生成文本，再来润色。
              </p>
            )
          }
        </Field>
        <Preview preview={preview} streaming={streaming} error={error} emptyHint="点击「润色」预览改写结果，确认后替换文本框。" />
      </div>
    </Modal>
  );
}

/** 流式预览区：生成/润色弹窗共用。 */
function Preview({ preview, streaming, error, emptyHint }: { preview: string; streaming: boolean; error: string; emptyHint: string }) {
  if (error) {
    return (
      <p className="rounded-[var(--radius-sm)] border border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-[color-mix(in_oklab,var(--danger)_8%,transparent)] px-3 py-2 text-xs break-words text-danger">
        {error}
      </p>
    );
  }
  if (streaming && !preview) {
    return (
      <div className="flex items-center gap-2 rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-3">
        <WaveLoader label="生成中" />
      </div>
    );
  }
  if (preview) {
    return (
      <div className="max-h-56 overflow-y-auto rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2.5 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-fg">
        {preview}
      </div>
    );
  }
  return <p className="text-[11px] text-muted">{emptyHint}</p>;
}

/** AI 写作入口按钮 + 两项下拉（挂在 aside，占位小）。 */
export default function AIWrite({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [mode, setMode] = useState<"generate" | "polish" | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    const onDown = (e: PointerEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onDown);
    };
  }, [menuOpen]);

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        aria-label="AI 写作"
        title="AI 生成 / 润色文本"
        onClick={() => setMenuOpen((v) => !v)}
        className="flex cursor-pointer items-center gap-1 rounded-full border border-line bg-raise-2 px-2 py-0.5 text-[11px] text-fg-2 transition-colors duration-150 hover:border-accent hover:text-accent"
      >
        <Sparkles size={11} strokeWidth={1.75} />
        AI 写作
      </button>
      {menuOpen && (
        <div
          role="menu"
          aria-label="AI 写作"
          className="rise absolute right-0 top-[calc(100%+6px)] z-30 w-36 overflow-hidden rounded-[var(--radius-md)] border border-line bg-panel shadow-[var(--shadow-2)] backdrop-blur-xl"
        >
          {(
            [
              { key: "generate", label: "AI 生成文本", icon: Sparkles },
              { key: "polish", label: "AI 润色", icon: PenLine },
            ] as const
          ).map(({ key, label, icon: Icon }) => (
            <button
              key={key}
              role="menuitem"
              onClick={() => {
                setMenuOpen(false);
                setMode(key);
              }}
              className="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-xs text-fg-2 transition-colors duration-150 hover:bg-raise-2 hover:text-fg"
            >
              <Icon size={13} strokeWidth={1.75} />
              {label}
            </button>
          ))}
        </div>
      )}
      <GenerateModal open={mode === "generate"} onClose={() => setMode(null)} value={value} onChange={onChange} />
      <PolishModal open={mode === "polish"} onClose={() => setMode(null)} value={value} onChange={onChange} />
    </div>
  );
}
