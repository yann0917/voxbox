import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookA, Plus, Trash2, Pencil, Check, X } from "lucide-react";
import { fetchJSON } from "../lib/api";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ConfirmDialog,
  EmptyState,
  Input,
  MicroLabel,
  Pagination,
  Select,
  Skeleton,
  Textarea,
  useToast,
} from "../ui";

/** 一条发音词条：term 为原文写法，replacement 为希望引擎读出的写法（respelling）。 */
export interface PronunciationEntry {
  id: string;
  term: string;
  replacement: string;
  language: string; // "*" 全语言；二字母码按请求语言匹配
  enabled: boolean;
  created_at: string;
}

const LANG_OPTIONS = [
  { value: "*", label: "通用（所有语言）" },
  { value: "zh", label: "中文" },
  { value: "en", label: "英语" },
  { value: "ja", label: "日语" },
  { value: "ko", label: "韩语" },
  { value: "yue", label: "粤语" },
];

const langLabel = (v: string) => LANG_OPTIONS.find((o) => o.value === v)?.label ?? v;

interface EntryDraft {
  term: string;
  replacement: string;
  language: string;
}

const emptyDraft: EntryDraft = { term: "", replacement: "", language: "*" };

/** 发音词典（设置页独立 Tab）：词条管理 + 试听干跑。写操作要求管理员。 */
export default function PronunciationSection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const PAGE_SIZE = 20;
  const [page, setPage] = useState(1);
  const { data, isLoading } = useQuery({
    queryKey: ["pronunciation", page],
    queryFn: () =>
      fetchJSON<{ entries: PronunciationEntry[]; total: number }>(`/api/pronunciation?page=${page}&size=${PAGE_SIZE}`),
  });
  const entries = data?.entries ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  // 删除后当前页可能超界:落在空页上时回退到最后一页
  useEffect(() => {
    if (!isLoading && page > 1 && entries.length === 0 && total > 0) {
      setPage(pageCount);
    }
  }, [isLoading, page, entries.length, total, pageCount]);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["pronunciation"] });

  // —— 新增/编辑：editing 为正在编辑的词条 id（null = 新增模式）——
  const [draft, setDraft] = useState<EntryDraft>(emptyDraft);
  const [editing, setEditing] = useState<string | null>(null);

  const upsert = useMutation({
    mutationFn: (e: EntryDraft) =>
      editing === null
        ? fetchJSON("/api/pronunciation", { method: "POST", body: JSON.stringify({ ...e, enabled: true }) })
        : fetchJSON(`/api/pronunciation/${editing}`, { method: "PUT", body: JSON.stringify({ ...e, enabled: true }) }),
    onSuccess: () => {
      toast({ tone: "ok", title: editing === null ? "词条已添加" : "词条已更新" });
      setDraft(emptyDraft);
      setEditing(null);
      refresh();
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });

  const remove = useMutation({
    mutationFn: (id: string) => fetchJSON(`/api/pronunciation/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast({ tone: "ok", title: "词条已删除" });
      refresh();
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });

  const toggle = useMutation({
    mutationFn: (e: PronunciationEntry) =>
      fetchJSON(`/api/pronunciation/${e.id}`, {
        method: "PUT",
        body: JSON.stringify({ term: e.term, replacement: e.replacement, language: e.language, enabled: !e.enabled }),
      }),
    onSuccess: refresh,
    onError: (e: Error) => toast({ tone: "error", title: "更新失败", description: e.message }),
  });

  // —— 试听干跑：只做文本替换，不发起合成 ——
  const [sample, setSample] = useState("");
  const [sampleLang, setSampleLang] = useState("*");
  const test = useMutation({
    mutationFn: () =>
      fetchJSON<{ text: string; replaced: boolean }>("/api/pronunciation/test", {
        method: "POST",
        body: JSON.stringify({ text: sample, language: sampleLang }),
      }),
    onError: (e: Error) => toast({ tone: "error", title: "试听失败", description: e.message }),
  });

  const startEdit = (e: PronunciationEntry) => {
    setEditing(e.id);
    setDraft({ term: e.term, replacement: e.replacement, language: e.language });
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          title="发音词典"
          icon={<BookA size={15} strokeWidth={1.75} />}
          aside={<span className="micro">共 {total} 条</span>}
        />
        <CardBody className="space-y-4">
          <p className="text-xs text-fg-2">
            合成前把词条替换为对应写法，让各引擎读准多音字、品牌名与人名；对所有合成通道（云端与本地）通用。
            行内临时标注：在合成文本里写「[[词|读音]]」，只对那一处生效。
          </p>

          {/* —— 新增 / 编辑表单 —— */}
          <div className="flex flex-wrap items-end gap-3">
            <div className="w-44">
              <MicroLabel>词条</MicroLabel>
              <Input
                value={draft.term}
                onChange={(e) => setDraft((d) => ({ ...d, term: e.target.value }))}
                placeholder="如：重庆"
              />
            </div>
            <div className="w-52">
              <MicroLabel>读作</MicroLabel>
              <Input
                value={draft.replacement}
                onChange={(e) => setDraft((d) => ({ ...d, replacement: e.target.value }))}
                placeholder="如：chóngqìng"
              />
            </div>
            <div className="w-44">
              <MicroLabel>语言范围</MicroLabel>
              <Select
                value={draft.language}
                onChange={(e) => setDraft((d) => ({ ...d, language: e.target.value }))}
              >
                {LANG_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </Select>
            </div>
            {editing === null ? (
              <Button
                variant="primary"
                loading={upsert.isPending}
                icon={<Plus size={14} strokeWidth={1.75} />}
                disabled={!draft.term.trim() || !draft.replacement.trim()}
                onClick={() => upsert.mutate(draft)}
              >
                添加词条
              </Button>
            ) : (
              <div className="flex items-center gap-2">
                <Button
                  variant="primary"
                  loading={upsert.isPending}
                  icon={<Check size={14} strokeWidth={1.75} />}
                  disabled={!draft.term.trim() || !draft.replacement.trim()}
                  onClick={() => upsert.mutate(draft)}
                >
                  保存修改
                </Button>
                <Button
                  variant="secondary"
                  icon={<X size={14} strokeWidth={1.75} />}
                  onClick={() => {
                    setEditing(null);
                    setDraft(emptyDraft);
                  }}
                >
                  取消
                </Button>
              </div>
            )}
          </div>

          {/* —— 词条表 —— */}
          {isLoading ? (
            <Skeleton className="h-24 w-full" />
          ) : entries.length === 0 ? (
            <EmptyState
              icon={<BookA size={18} strokeWidth={1.75} />}
              title="还没有词条"
              description="添加一条试试：词条「GIF」读作「jiff」，合成时会自动替换。"
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[560px] text-left text-xs">
                <thead>
                  <tr className="border-b border-line text-muted">
                    <th className="py-2 pr-3 font-normal">词条</th>
                    <th className="py-2 pr-3 font-normal">读作</th>
                    <th className="py-2 pr-3 font-normal">语言范围</th>
                    <th className="py-2 pr-3 font-normal">状态</th>
                    <th className="py-2 font-normal text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e) => (
                    <tr key={e.id} className="border-b border-line/50 last:border-0">
                      <td className="py-2 pr-3 font-mono text-fg">{e.term}</td>
                      <td className="py-2 pr-3 font-mono text-fg-2">{e.replacement}</td>
                      <td className="py-2 pr-3 text-fg-2">{langLabel(e.language)}</td>
                      <td className="py-2 pr-3">
                        <span className={e.enabled ? "text-ok" : "text-muted"}>{e.enabled ? "启用" : "停用"}</span>
                      </td>
                      <td className="py-2 text-right">
                        <div className="inline-flex items-center gap-1">
                          <Button
                            variant="ghost"
                            className="px-2 py-1"
                            onClick={() => toggle.mutate(e)}
                            disabled={toggle.isPending}
                          >
                            {e.enabled ? "停用" : "启用"}
                          </Button>
                          <Button variant="ghost" className="px-2 py-1" onClick={() => startEdit(e)}>
                            <Pencil size={13} strokeWidth={1.75} />
                          </Button>
                          <DeleteButton onConfirm={() => remove.mutate(e.id)} />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pagination page={page} pageCount={pageCount} total={total} onChange={setPage} />
        </CardBody>
      </Card>

      <Card>
        <CardHeader title="试一试" icon={<BookA size={15} strokeWidth={1.75} />} />
        <CardBody className="space-y-3">
          <div>
            <MicroLabel>输入一段文本，查看替换后的效果（不会真的合成）</MicroLabel>
            <Textarea
              value={sample}
              onChange={(e) => setSample(e.target.value)}
              rows={3}
              placeholder="支持行内标注：[[GIF|jiff]] 是一种图片格式"
            />
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <div className="w-44">
              <Select value={sampleLang} onChange={(e) => setSampleLang(e.target.value)}>
                {LANG_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </Select>
            </div>
            <Button variant="secondary" loading={test.isPending} disabled={!sample.trim()} onClick={() => test.mutate()}>
              预览替换
            </Button>
          </div>
          {test.data && (
            <div className="rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 text-xs">
              <MicroLabel>替换后</MicroLabel>
              <p className="whitespace-pre-wrap text-fg">{test.data.text || "（空）"}</p>
              {!test.data.replaced && (
                <p className="mt-1 text-muted">
                  没有词条被应用：当前语言范围下没有命中的词条（「通用」词条始终生效）。
                </p>
              )}
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  );
}

/** 删除按钮 + 确认弹窗（复用 ConfirmDialog）。 */
function DeleteButton({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" className="px-2 py-1 text-danger" onClick={() => setOpen(true)}>
        <Trash2 size={13} strokeWidth={1.75} />
      </Button>
      <ConfirmDialog
        open={open}
        onCancel={() => setOpen(false)}
        onConfirm={() => {
          setOpen(false);
          onConfirm();
        }}
        title="删除词条"
        description="删除后已提交的任务不受影响，之后的合成不再应用这条替换。"
      />
    </>
  );
}
