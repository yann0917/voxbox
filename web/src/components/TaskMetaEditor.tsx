import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { toolLabel } from "../lib/toolNames";
import type { Task } from "../lib/types";
import { useToast } from "../ui";

/** 任务详情头部：标题正文即编辑入口（点击直接改，无独立小节）——手改置 title_edited，
 *  ASR 完成时的自动派生标题据此让位；标签整体覆盖式 PATCH（后端清洗去重，超限静默
 *  丢弃）。提交成功回读任务 DTO 直接 patch 详情缓存并失效任务列表，列表行与详情即时同步。 */
export function TaskMetaEditor({ task }: { task: Task }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [addingTag, setAddingTag] = useState(false);
  const [tagDraft, setTagDraft] = useState("");

  const meta = useMutation({
    mutationFn: (body: { title?: string; tags?: string[] }) =>
      fetchJSON<Task>(`/api/tasks/${task.id}/meta`, { method: "PATCH", body: JSON.stringify(body) }),
    onSuccess: (updated) => {
      qc.setQueryData(["task", task.id], (cur: unknown) =>
        cur ? { ...(cur as { task: Task }), task: updated } : cur,
      );
      qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });

  const saveTitle = () => {
    const title = titleDraft.trim();
    setEditingTitle(false);
    if (!title || title === task.title) return;
    meta.mutate({ title });
  };

  const addTag = () => {
    const tag = tagDraft.trim();
    setAddingTag(false);
    setTagDraft("");
    if (!tag || task.tags?.includes(tag)) return;
    meta.mutate({ tags: [...(task.tags ?? []), tag] });
  };

  const removeTag = (tag: string) => {
    meta.mutate({ tags: (task.tags ?? []).filter((x) => x !== tag) });
  };

  return (
    <div className="space-y-2">
      {editingTitle ? (
        <input
          autoFocus
          value={titleDraft}
          onChange={(e) => setTitleDraft(e.target.value)}
          onBlur={saveTitle}
          onKeyDown={(e) => {
            if (e.key === "Enter") saveTitle();
            if (e.key === "Escape") setEditingTitle(false);
          }}
          aria-label="修改任务标题"
          maxLength={60}
          className="h-8 w-full rounded-[var(--radius-sm)] border border-line-strong bg-raise-2 px-2 text-base font-medium text-fg outline-none focus:border-accent"
        />
      ) : (
        <button
          type="button"
          onClick={() => {
            setTitleDraft(task.title ?? "");
            setEditingTitle(true);
          }}
          title="点击修改标题"
          className="block max-w-full cursor-pointer truncate text-left text-base font-medium text-fg transition-colors duration-150 hover:text-accent"
        >
          {task.title || `${toolLabel(task.tool)}任务`}
        </button>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        {(task.tags ?? []).map((tag) => (
          <span
            key={tag}
            className="inline-flex items-center gap-1 rounded-full bg-raise-2 px-2 py-0.5 text-[11px] text-fg-2"
          >
            {tag}
            <button
              type="button"
              aria-label={`删除标签 ${tag}`}
              onClick={() => removeTag(tag)}
              className="cursor-pointer text-muted transition-colors duration-150 hover:text-danger"
            >
              <X size={10} strokeWidth={2} />
            </button>
          </span>
        ))}
        {addingTag ? (
          <input
            autoFocus
            value={tagDraft}
            onChange={(e) => setTagDraft(e.target.value)}
            onBlur={addTag}
            onKeyDown={(e) => {
              if (e.key === "Enter") addTag();
              if (e.key === "Escape") {
                setAddingTag(false);
                setTagDraft("");
              }
            }}
            placeholder="回车添加"
            aria-label="新标签"
            maxLength={20}
            className="h-6 w-24 rounded-full border border-line-strong bg-raise-2 px-2 text-[11px] text-fg outline-none focus:border-accent"
          />
        ) : (
          <button
            type="button"
            onClick={() => setAddingTag(true)}
            className="inline-flex cursor-pointer items-center gap-0.5 rounded-full border border-dashed border-line px-2 py-0.5 text-[11px] text-muted transition-colors duration-150 hover:border-accent hover:text-accent"
          >
            <Plus size={10} strokeWidth={2} /> 标签
          </button>
        )}
      </div>
    </div>
  );
}
