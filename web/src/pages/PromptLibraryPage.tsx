import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, LibraryBig, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { usePrompts, type PromptItem } from "../lib/prompts";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ConfirmDialog,
  EmptyState,
  Field,
  Input,
  Modal,
  Select,
  Skeleton,
  Textarea,
  useToast,
} from "../ui";

/** 提示词库：内置写作主题（AI 生成/润色的内容来源）与用户自定义提示词的统一管理。
 *  这里的条目就是各合成引擎面板「AI 写作」弹窗里的主题/方向选项——一处维护，处处可用。
 *  内置条目不可改，需要调整时复制正文新建一条自己的。 */

const KIND_LABEL: Record<string, string> = { generate: "生成", polish: "润色" };

/** 编辑器草稿。 */
interface Draft {
  name: string;
  category: string;
  description: string;
  kind: "generate" | "polish";
  content: string;
}

const emptyDraft: Draft = { name: "", category: "", description: "", kind: "generate", content: "" };

export default function PromptLibraryPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = usePrompts();
  const items = data?.items ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["prompts"] });

  // —— 筛选：分类 chips + 关键词（名称/说明/正文） ——
  const [category, setCategory] = useState("");
  const [keyword, setKeyword] = useState("");
  // 条目量级几十条，分类与过滤直接渲染期派生，不值得 memo
  const categories = [...new Set(items.map((p) => p.category))];
  const filtered = items.filter((p) => {
    if (category && p.category !== category) return false;
    const kw = keyword.trim().toLowerCase();
    if (!kw) return true;
    return [p.name, p.description, p.content].some((s) => s.toLowerCase().includes(kw));
  });

  // —— 新建/编辑 ——
  const [editing, setEditing] = useState<PromptItem | null>(null); // null+open = 新建
  const [editorOpen, setEditorOpen] = useState(false);
  const [deleting, setDeleting] = useState<PromptItem | null>(null);

  const remove = useMutation({
    mutationFn: (p: PromptItem) => fetchJSON(`/api/prompts/${p.id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast({ tone: "ok", title: "提示词已删除" });
      setDeleting(null);
      refresh();
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });

  const copy = async (p: PromptItem) => {
    try {
      await navigator.clipboard.writeText(p.content);
      toast({ tone: "ok", title: "提示词正文已复制" });
    } catch {
      toast({ tone: "error", title: "复制失败", description: "浏览器未授权剪贴板访问" });
    }
  };

  return (
    <>
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-fg">提示词库</h1>
          <p className="mt-1 text-xs text-muted">
            AI 生成与润色使用的主题和提示词：内置的直接可用，自己的随时新建；语音合成页「AI 写作」的选项就来自这里。
          </p>
        </div>
        <Button
          variant="primary"
          icon={<Plus size={14} strokeWidth={1.75} />}
          onClick={() => {
            setEditing(null);
            setEditorOpen(true);
          }}
        >
          新建提示词
        </Button>
      </div>

      <Card>
        <CardHeader
          title="全部条目"
          icon={<LibraryBig size={15} strokeWidth={1.75} />}
          aside={<span className="micro">{filtered.length} 条</span>}
        />
        <CardBody className="space-y-4">
          {/* 筛选行 */}
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-52">
              <Search size={13} strokeWidth={1.75} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
              <Input
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                placeholder="搜索名称、说明、正文…"
                className="pl-8"
                aria-label="搜索提示词"
              />
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              <Chip active={category === ""} onClick={() => setCategory("")}>全部</Chip>
              {categories.map((c) => (
                <Chip key={c} active={category === c} onClick={() => setCategory(category === c ? "" : c)}>{c}</Chip>
              ))}
            </div>
          </div>

          {/* 条目列表 */}
          {isLoading ? (
            <Skeleton className="h-40 w-full" />
          ) : filtered.length === 0 ? (
            <EmptyState
              icon={<LibraryBig size={18} strokeWidth={1.75} />}
              title={keyword || category ? "没有匹配的条目" : "还没有自定义提示词"}
              description={
                keyword || category
                  ? "换个关键词或分类试试。"
                  : "点右上角「新建提示词」写一条自己的；内置主题已覆盖常见朗读场景。"
              }
            />
          ) : (
            <ul className="divide-y divide-line/60">
              {filtered.map((p) => (
                <li key={p.source === "builtin" ? p.key : `u${p.id}`} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-fg">{p.name}</span>
                      <span className="rounded-full border border-line px-1.5 py-px text-[10px] leading-4 text-fg-2">{p.category}</span>
                      <span className="rounded-full border border-line px-1.5 py-px text-[10px] leading-4 text-muted">
                        {KIND_LABEL[p.kind] ?? p.kind}
                      </span>
                      {p.source === "user" && (
                        <span className="rounded-full border border-line px-1.5 py-px text-[10px] leading-4 text-muted">自定义</span>
                      )}
                    </div>
                    {p.description && <p className="mt-0.5 text-xs text-fg-2">{p.description}</p>}
                    <p className="mt-1 line-clamp-2 text-xs leading-relaxed break-words text-muted">{p.content}</p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1 pt-0.5">
                    <Button variant="ghost" className="px-2 py-1" aria-label={`复制「${p.name}」正文`} onClick={() => void copy(p)}>
                      <Copy size={13} strokeWidth={1.75} />
                    </Button>
                    {p.source === "user" && (
                      <>
                        <Button
                          variant="ghost"
                          className="px-2 py-1"
                          aria-label={`编辑「${p.name}」`}
                          onClick={() => {
                            setEditing(p);
                            setEditorOpen(true);
                          }}
                        >
                          <Pencil size={13} strokeWidth={1.75} />
                        </Button>
                        <Button variant="ghost" className="px-2 py-1 text-danger" aria-label={`删除「${p.name}」`} onClick={() => setDeleting(p)}>
                          <Trash2 size={13} strokeWidth={1.75} />
                        </Button>
                      </>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          <p className="text-[11px] text-muted">
            内置条目随应用更新；想微调时复制正文新建一条即可。提示词在调用时会自动附加「朗读友好」约束（纯文本、无符号），不必写进正文。
          </p>
        </CardBody>
      </Card>

      {/* 打开时才挂载：草稿总是从 editing（或空）初始化，不残留上次未保存的输入 */}
      {editorOpen && (
        <PromptEditor
          open
          editing={editing}
          categories={categories}
          onClose={() => setEditorOpen(false)}
          onSaved={() => {
            setEditorOpen(false);
            refresh();
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        title={`删除「${deleting?.name ?? ""}」`}
        description="删除后语音合成页的 AI 写作里将不再出现这条提示词。"
        confirmLabel="删除"
        tone="danger"
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
        onCancel={() => setDeleting(null)}
      />
    </>
  );
}

/** 分类筛选小胶囊。 */
function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`cursor-pointer rounded-full border px-2.5 py-1 text-xs transition-colors duration-150 ${
        active ? "border-accent bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong hover:text-fg"
      }`}
    >
      {children}
    </button>
  );
}

/** 新建/编辑弹窗：内置条目不进编辑器（不可改），只有自定义条目与新建走这里。 */
function PromptEditor({
  open,
  editing,
  categories,
  onClose,
  onSaved,
}: {
  open: boolean;
  editing: PromptItem | null;
  categories: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [draft, setDraft] = useState<Draft>(() =>
    editing
      ? { name: editing.name, category: editing.category, description: editing.description, kind: editing.kind, content: editing.content }
      : emptyDraft,
  );

  const save = useMutation({
    mutationFn: (d: Draft) =>
      editing
        ? fetchJSON(`/api/prompts/${editing.id}`, { method: "PUT", body: JSON.stringify(d) })
        : fetchJSON("/api/prompts", { method: "POST", body: JSON.stringify(d) }),
    onSuccess: () => {
      toast({ tone: "ok", title: editing ? "提示词已更新" : "提示词已创建" });
      onSaved();
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });

  const canSave = draft.name.trim() !== "" && draft.category.trim() !== "" && draft.content.trim() !== "";
  const suggestions = categories.filter((c) => c !== draft.category.trim());

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? `编辑「${editing.name}」` : "新建提示词"}
      width={560}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={save.isPending}>
            取消
          </Button>
          <Button variant="primary" loading={save.isPending} disabled={!canSave} onClick={() => save.mutate(draft)}>
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="名称" required>
            {({ id, ...rest }) => (
              <Input id={id} value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="如：产品介绍" {...rest} />
            )}
          </Field>
          <Field label="分组" required hint="同一分组的条目在筛选与弹窗里相邻">
            {({ id, ...rest }) => (
              <Input id={id} value={draft.category} onChange={(e) => setDraft((d) => ({ ...d, category: e.target.value }))} placeholder="如：文案" {...rest} />
            )}
          </Field>
        </div>
        {suggestions.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {suggestions.slice(0, 8).map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setDraft((d) => ({ ...d, category: c }))}
                className="cursor-pointer rounded-full border border-line px-2 py-0.5 text-[11px] text-fg-2 transition-colors duration-150 hover:border-accent hover:text-accent"
              >
                {c}
              </button>
            ))}
          </div>
        )}
        <Field label="用途" hint="生成=按主题写新文本；润色=改写文本框里已有的内容">
          {({ id, ...rest }) => (
            <Select id={id} value={draft.kind} onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value as Draft["kind"] }))} {...rest}>
              <option value="generate">生成新文本</option>
              <option value="polish">改写已有文本</option>
            </Select>
          )}
        </Field>
        <Field label="说明" aside="可选" hint="一句话描述这条提示词的用途，显示在列表与选择器里">
          {({ id, ...rest }) => (
            <Input id={id} value={draft.description} onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))} placeholder="如：正式场合的活动开场稿" {...rest} />
          )}
        </Field>
        <Field label="提示词正文" required hint="写给 AI 的指令：任务、口吻、结构都交代清楚；输出约束由系统自动附加">
          {({ id, ...rest }) => (
            <Textarea
              id={id}
              rows={7}
              value={draft.content}
              onChange={(e) => setDraft((d) => ({ ...d, content: e.target.value }))}
              placeholder="你是一位…请根据给出的主题，写一段…开头…中间…结尾…"
              {...rest}
            />
          )}
        </Field>
      </div>
    </Modal>
  );
}
