import { Play } from "lucide-react";
import type { MinutesNotes } from "../../lib/types";
import { clockText } from "./model";

export interface MinutesViewProps {
  notes: MinutesNotes;
  /** 时间戳 chip 点击跳播（接线方并入轨标题/时长副标题）；无回放源不传，时间码为纯文本 */
  onSeek?: (ms: number) => void;
}

/** 结构化会议纪要渲染：概述 + 议题卡（要点带时间戳）+ 结论 + 待办 + 术语表。
 *  时间戳来自后端 evidence 转写行号解析（t 毫秒），有回放源时渲染为跳播 chip。 */
export function MinutesView({ notes, onSeek }: MinutesViewProps) {
  const topics = (notes.topics ?? []).filter(
    (t) => t.title.trim() || (t.points ?? []).some((p) => p.text.trim()),
  );
  const conclusions = (notes.conclusions ?? []).filter((c) => c.text.trim());
  const todos = (notes.todos ?? []).filter((t) => t.content.trim());
  const terms = (notes.terms ?? []).filter((t) => t.name.trim());
  const empty =
    !notes.summary?.trim() && topics.length === 0 && conclusions.length === 0 && todos.length === 0 && terms.length === 0;
  if (empty) {
    return <p className="text-xs text-muted">这次加工没有产出有效的纪要内容，可以重试一次。</p>;
  }
  return (
    <div className="space-y-4">
      {notes.title?.trim() && <p className="text-sm font-medium leading-relaxed text-fg">{notes.title.trim()}</p>}
      {notes.summary?.trim() && <p className="text-[13px] leading-relaxed text-fg-2">{notes.summary.trim()}</p>}
      {topics.length > 0 && (
        <section className="space-y-2.5">
          <p className="micro text-muted">议题</p>
          {topics.map((t, i) => (
            <div key={i} className="rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2.5">
              <p className="flex items-baseline gap-2 text-[13px] font-medium text-fg">
                <span className="min-w-0 flex-1 break-words">{t.title.trim()}</span>
                <SeekChip ms={t.t} onSeek={onSeek} />
              </p>
              {t.summary?.trim() && <p className="mt-1 break-words text-xs leading-relaxed text-muted">{t.summary.trim()}</p>}
              {(t.points ?? []).some((p) => p.text.trim()) && (
                <ul className="mt-2 space-y-1.5">
                  {(t.points ?? [])
                    .filter((p) => p.text.trim())
                    .map((p, j) => (
                      <li key={j} className="flex items-start gap-2 text-[13px] leading-relaxed">
                        <span className="mt-[7px] size-1.5 shrink-0 rounded-full bg-accent/70" aria-hidden="true" />
                        <span className="min-w-0 flex-1 break-words text-fg">
                          {p.text.trim()}
                          <SeekChip ms={p.t} onSeek={onSeek} className="ml-1.5" />
                        </span>
                      </li>
                    ))}
                </ul>
              )}
            </div>
          ))}
        </section>
      )}
      {conclusions.length > 0 && (
        <section className="space-y-1.5">
          <p className="micro text-muted">结论</p>
          <ul className="space-y-1.5">
            {conclusions.map((c, i) => (
              <li key={i} className="flex items-start gap-2 text-[13px] leading-relaxed">
                <span className="mt-[7px] size-1.5 shrink-0 rounded-full bg-accent/70" aria-hidden="true" />
                <span className="min-w-0 flex-1 break-words text-fg">
                  {c.text.trim()}
                  {(c.owner?.trim() || c.t !== undefined) && (
                    <span className="ml-1.5 text-[11px] text-muted">
                      {c.owner?.trim()}
                      {c.t !== undefined && <SeekChip ms={c.t} onSeek={onSeek} className="ml-1" />}
                    </span>
                  )}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {todos.length > 0 && (
        <section className="space-y-1.5">
          <p className="micro text-muted">待办</p>
          <ul className="space-y-1.5">
            {todos.map((t, i) => (
              <li key={i} className="flex items-start gap-2 text-[13px] leading-relaxed">
                <span className="mt-[7px] size-1.5 shrink-0 rounded-full border border-accent/70" aria-hidden="true" />
                <span className="min-w-0 flex-1 break-words text-fg">
                  {t.content.trim()}
                  {(t.owner?.trim() || t.due?.trim()) && (
                    <span className="ml-1.5 text-[11px] text-muted">
                      {[t.owner?.trim(), t.due?.trim()].filter(Boolean).join(" · ")}
                    </span>
                  )}
                  {t.t !== undefined && <SeekChip ms={t.t} onSeek={onSeek} className="ml-1.5" />}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {terms.length > 0 && (
        <section className="space-y-1.5">
          <p className="micro text-muted">术语表</p>
          <ul className="space-y-1">
            {terms.map((t, i) => (
              <li key={i} className="text-[13px] leading-relaxed break-words">
                <span className="font-medium text-fg">{t.name.trim()}</span>
                {t.note?.trim() && <span className="text-fg-2"> —— {t.note.trim()}</span>}
                {(t.heard?.length ?? 0) > 0 && (
                  <span className="text-[11px] text-muted">（听错作：{t.heard!.join("、")}）</span>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

/** 回链时间戳 chip：有 onSeek 渲染为跳播按钮（样式与问答区引用 chip 一致），
 *  无则渲染纯文本时间码（不出可点击的假按钮）；t 缺省（evidence 无法定位）不渲染。 */
function SeekChip({ ms, onSeek, className = "" }: { ms: number | undefined; onSeek?: (ms: number) => void; className?: string }) {
  if (ms === undefined) return null;
  if (!onSeek) {
    return <span className={`align-baseline font-mono text-[11px] text-muted ${className}`}>{clockText(ms)}</span>;
  }
  return (
    <button
      type="button"
      onClick={() => onSeek(ms)}
      className={`inline-flex cursor-pointer items-center gap-0.5 rounded-full border border-line bg-inset px-1.5 align-baseline font-mono text-[11px] tabular-nums leading-4 text-accent transition-colors duration-150 hover:border-accent ${className}`}
    >
      <Play size={9} strokeWidth={2} />
      {clockText(ms)}
    </button>
  );
}
