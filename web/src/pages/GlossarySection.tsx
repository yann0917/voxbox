import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Languages, Plus, Trash2, Pencil, Check, X } from "lucide-react";
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
  Select,
  Skeleton,
  useToast,
} from "../ui";

/** 一条翻译术语:src 原文词,dst 统一译法;AI 翻译时自动沉淀,这里可人工增删改。 */
export interface GlossaryTerm {
  id: number;
  target_lang: string;
  src: string;
  dst: string;
  updated_at: string;
}

/** GET /api/subtitles/langs 行格式(后端 volcengine.MTLang) */
interface TranslateLang {
  code: string;
  name: string;
}

const emptyDraft = { src: "", dst: "", target_language: "en" };

const shortTime = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? iso
    : `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

/** 翻译词汇表(设置页独立 Tab):AI 自动沉淀 + 人工增删改查。 */
export default function GlossarySection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["glossary"],
    queryFn: () => fetchJSON<{ items: GlossaryTerm[] }>("/api/glossary"),
  });
  const { data: langs } = useQuery({
    queryKey: ["subtitle-langs"],
    queryFn: () => fetchJSON<TranslateLang[]>("/api/subtitles/langs"),
    staleTime: Infinity,
  });
  const terms = data?.items ?? [];
  const refresh = () => void qc.invalidateQueries({ queryKey: ["glossary"] });

  const [langFilter, setLangFilter] = useState("");
  const [draft, setDraft] = useState(emptyDraft);
  const [editing, setEditing] = useState<number | null>(null);

  const upsert = useMutation({
    mutationFn: (d: typeof emptyDraft) =>
      editing === null
        ? fetchJSON("/api/glossary", { method: "POST", body: JSON.stringify(d) })
        : fetchJSON(`/api/glossary/${editing}`, { method: "PUT", body: JSON.stringify(d) }),
    onSuccess: () => {
      toast({ tone: "ok", title: editing === null ? "词条已添加" : "词条已保存" });
      setDraft(emptyDraft);
      setEditing(null);
      refresh();
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });

  const remove = useMutation({
    mutationFn: (id: number) => fetchJSON(`/api/glossary/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast({ tone: "ok", title: "词条已删除" });
      refresh();
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });

  const startEdit = (t: GlossaryTerm) => {
    setEditing(t.id);
    setDraft({ src: t.src, dst: t.dst, target_language: t.target_lang });
  };

  const shown = langFilter === "" ? terms : terms.filter((t) => t.target_lang === langFilter);
  const langName = (code: string) => langs?.find((l) => l.code === code)?.name ?? code;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          title="翻译词汇表"
          icon={<Languages size={15} strokeWidth={1.75} />}
          aside={<span className="micro">{shown.length} 条</span>}
        />
        <CardBody className="space-y-4">
          <p className="text-xs text-fg-2">
            字幕 AI 翻译时自动沉淀的译名对照（人名、地名等），也可在这里手动增删改；翻译时沿用表里的译法，保持全片一致。
          </p>

          {/* —— 新增 / 编辑表单 —— */}
          <div className="flex flex-wrap items-end gap-3">
            <div className="w-40">
              <MicroLabel>原文</MicroLabel>
              <Input
                value={draft.src}
                onChange={(e) => setDraft((d) => ({ ...d, src: e.target.value }))}
                placeholder="如：张三"
              />
            </div>
            <div className="w-44">
              <MicroLabel>译为</MicroLabel>
              <Input
                value={draft.dst}
                onChange={(e) => setDraft((d) => ({ ...d, dst: e.target.value }))}
                placeholder="如：Zhang San"
              />
            </div>
            <div className="w-36">
              <MicroLabel>目标语言</MicroLabel>
              <Select
                value={draft.target_language}
                onChange={(e) => setDraft((d) => ({ ...d, target_language: e.target.value }))}
              >
                {(langs ?? []).map((l) => (
                  <option key={l.code} value={l.code}>{l.name}</option>
                ))}
              </Select>
            </div>
            {editing === null ? (
              <Button
                variant="primary"
                loading={upsert.isPending}
                icon={<Plus size={14} strokeWidth={1.75} />}
                disabled={!draft.src.trim() || !draft.dst.trim()}
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
                  disabled={!draft.src.trim() || !draft.dst.trim()}
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

          {/* —— 语言筛选 + 词条表 —— */}
          <div className="w-44">
            <Select value={langFilter} onChange={(e) => setLangFilter(e.target.value)}>
              <option value="">全部语言</option>
              {(langs ?? []).map((l) => (
                <option key={l.code} value={l.code}>{l.name}</option>
              ))}
            </Select>
          </div>
          {isLoading ? (
            <Skeleton className="h-24 w-full" />
          ) : shown.length === 0 ? (
            <EmptyState
              icon={<Languages size={18} strokeWidth={1.75} />}
              title={terms.length === 0 ? "还没有词条" : "这个语言下还没有词条"}
              description="跑一次字幕 AI 翻译会自动沉淀译名，也可以手动添加。"
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[560px] text-left text-xs">
                <thead>
                  <tr className="border-b border-line text-muted">
                    <th className="py-2 pr-3 font-normal">原文</th>
                    <th className="py-2 pr-3 font-normal">译为</th>
                    <th className="py-2 pr-3 font-normal">目标语言</th>
                    <th className="py-2 pr-3 font-normal">最近使用</th>
                    <th className="py-2 font-normal text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((t) => (
                    <tr key={t.id} className="border-b border-line/50 last:border-0">
                      <td className="py-2 pr-3 font-mono text-fg">{t.src}</td>
                      <td className="py-2 pr-3 font-mono text-fg-2">{t.dst}</td>
                      <td className="py-2 pr-3 text-fg-2">{langName(t.target_lang)}</td>
                      <td className="py-2 pr-3 text-muted">{shortTime(t.updated_at)}</td>
                      <td className="py-2 text-right">
                        <div className="inline-flex items-center gap-1">
                          <Button variant="ghost" className="px-2 py-1" onClick={() => startEdit(t)}>
                            <Pencil size={13} strokeWidth={1.75} />
                          </Button>
                          <DeleteButton onConfirm={() => remove.mutate(t.id)} />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
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
        description="删除后，之后的翻译不再沿用这条译法。"
      />
    </>
  );
}
