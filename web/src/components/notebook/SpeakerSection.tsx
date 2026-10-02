import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import { fetchJSON } from "../../lib/api";
import { formatTime } from "../../lib/player";
import type { Task } from "../../lib/types";
import { Card, CardBody, CardHeader, useToast } from "../../ui";
import { speakerStats, type QNSegment } from "./model";

export interface SpeakerSectionProps {
  /** 改名目标任务（PATCH /api/tasks/:id/speakers） */
  task: Task;
  segments: QNSegment[];
  /** 说话人改名覆盖（父层持有：问答上下文与统计区同源） */
  speakerNames: Record<string, string>;
  /** 展示名（改名覆盖优先，否则「说话人N」；公式只在 NotebookSection 一份） */
  speakerLabel: (id: string) => string;
  onSpeakerNamesChange: (update: (cur: Record<string, string>) => Record<string, string>) => void;
}

/** 说话人区：改名 + 发言统计（轮数/时长/占比条）。从录音笔记结果区原样收编——
 *  整体覆盖式 PATCH、乐观更新与函数式按 id 回滚的语义不变；改名草稿（编辑中的
 *  输入框）状态内聚本组件。父层以任务 id 作 key 重挂载：切任务即重置编辑态。 */
export function SpeakerSection({ task, segments, speakerNames, speakerLabel, onSpeakerNamesChange }: SpeakerSectionProps) {
  const { toast } = useToast();
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  // 最新改名 map（ref 同步）：渲染闭包快照可能滞后于在途乐观更新（连续快速改名），
  // 提交以推进后的 ref 为基线，PATCH body 与乐观态同源，不漏掉在途笔。
  // 渲染后经 effect 对齐父级（commitRename 里的先行推进覆盖同 tick 的连续提交）
  const namesRef = useRef(speakerNames);
  useEffect(() => {
    namesRef.current = speakerNames;
  }, [speakerNames]);

  const rename = useMutation({
    mutationFn: async (names: Record<string, string>) => {
      // 整体覆盖式：names 缺失后端拒绝，空对象即清空全部改名
      return fetchJSON<{ ok: boolean }>(`/api/tasks/${task.id}/speakers`, {
        method: "PATCH",
        body: JSON.stringify({ names }),
      });
    },
  });

  /** 提交单个改名：乐观更新与失败回滚都走函数式按 id 操作——回滚只拨回该 id 的
   *  旧值，不整体拨快照，避免在途的另一笔并发改名被连带抹掉；
   *  清空输入 = 移除覆盖（恢复默认称呼） */
  const commitRename = (id: string) => {
    const name = draft.trim();
    const prevName = namesRef.current[id]; // 该 id 的旧值（可能 undefined = 无覆盖）
    setEditingId(null);
    if (name === (prevName ?? "")) return;
    // 以 ref 为基线先行推进（不等下一次渲染）：乐观更新与 PATCH body 同取 next，
    // 消除渲染闭包快照——快照取值会让 body 漏掉在途改名、被整体覆盖式 PATCH 连带抹掉
    const next = { ...namesRef.current };
    if (name) next[id] = name;
    else delete next[id];
    namesRef.current = next;
    onSpeakerNamesChange(() => next);
    rename.mutate(next, {
      onError: (e: Error) => {
        onSpeakerNamesChange((cur) => {
          const rollback = { ...cur };
          if (prevName === undefined) delete rollback[id];
          else rollback[id] = prevName;
          return rollback;
        });
        toast({ tone: "error", title: "改名失败", description: e.message });
      },
    });
  };

  const startRename = (id: string) => {
    setDraft(speakerNames[id] ?? "");
    setEditingId(id);
  };

  const stats = speakerStats(segments);

  return (
    <Card>
      <CardHeader title="说话人" aside={<span className="micro">点击铅笔改名，统计按发言时长</span>} />
      <CardBody className="space-y-2">
        {stats.map(({ id, turns, ms, pct }) => (
          <div key={id} className="flex items-center gap-2">
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2">
                {editingId === id ? (
                  <input
                    autoFocus
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    onBlur={() => commitRename(id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") commitRename(id);
                      if (e.key === "Escape") setEditingId(null);
                    }}
                    aria-label={`修改${speakerLabel(id)}的称呼`}
                    maxLength={20}
                    className="h-6 w-28 rounded-[var(--radius-sm)] border border-line-strong bg-raise-2 px-2 text-xs text-fg outline-none focus:border-accent"
                  />
                ) : (
                  <span className="truncate text-xs text-fg">{speakerLabel(id)}</span>
                )}
                <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">
                  {turns} 句 · {formatTime(ms / 1000)} · {Math.round(pct * 100)}%
                </span>
              </div>
              <div className="mt-1 h-1 overflow-hidden rounded-full bg-line">
                <div
                  className="h-full rounded-full bg-accent"
                  style={{ width: `${Math.max(2, Math.round(pct * 100))}%` }}
                />
              </div>
            </div>
            <button
              type="button"
              aria-label={`修改${speakerLabel(id)}的称呼`}
              onClick={() => startRename(id)}
              className="shrink-0 cursor-pointer rounded-full border border-line p-1.5 text-muted transition-colors duration-150 hover:border-accent hover:text-accent"
            >
              <Pencil size={11} strokeWidth={1.75} />
            </button>
          </div>
        ))}
      </CardBody>
    </Card>
  );
}
