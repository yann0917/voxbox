import { useEffect, useRef } from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { formatTime } from "../lib/player";
import type { SyncSegment } from "../lib/useTranscriptSync";

interface TranscriptListProps {
  segments: SyncSegment[];
  /** 当前播放句下标（-1 = 无），由 useTranscriptSync 计算 */
  activeIdx?: number;
  /** 传入即可点击跳播；不传为只读预览 */
  onSeek?: (ms: number) => void;
  /** 时间码格式：默认 mm:ss.d；长音频可传 h:mm:ss */
  formatTimecode?: (ms: number) => string;
  /** 列表可视高度上限（px），超出内部滚动 */
  maxHeight?: number;
  className?: string;
}

/** 虚拟化阈值：低于此值普通渲染（样式与既有视觉完全一致），超过则只挂可视区行 */
const VIRTUAL_THRESHOLD = 100;

/** 单条分句行（普通/虚拟两条路径共用） */
function SegmentRow({
  seg,
  active,
  onSeek,
  formatTimecode,
  bordered,
}: {
  seg: SyncSegment;
  active: boolean;
  onSeek?: (ms: number) => void;
  formatTimecode: (ms: number) => string;
  bordered: boolean;
}) {
  const inner = (
    <>
      <span
        className={`shrink-0 font-mono text-[11px] tabular-nums ${active ? "text-accent" : "text-muted"}`}
      >
        [{formatTimecode(seg.start_ms)}]
      </span>
      <span className="min-w-0 flex-1 text-sm leading-relaxed">{seg.text}</span>
    </>
  );
  const cls = `flex items-baseline gap-3 border-l-2 px-4 py-2.5 ${
    active ? "border-accent bg-raise-2" : "border-transparent"
  }${bordered ? " border-b border-line" : ""}${onSeek ? " transition-colors duration-150 hover:bg-raise-2" : ""}`;
  return onSeek ? (
    <button
      type="button"
      onClick={() => onSeek(seg.start_ms)}
      aria-current={active || undefined}
      className={`w-full cursor-pointer text-left ${cls}`}
    >
      {inner}
    </button>
  ) : (
    <div className={cls}>{inner}</div>
  );
}

/** 分句文稿列表：时间码 + 文本，当前播放句高亮（ASR / 妙记 / 历史回放共用）。
 *  长转写（>VIRTUAL_THRESHOLD）走虚拟滚动，只挂可视区行；跟读高亮滚出可视区时
 *  自动跟随居中，用户手动浏览的区间内不抢滚动。 */
export function TranscriptList({
  segments,
  activeIdx = -1,
  onSeek,
  formatTimecode = (ms) => formatTime(ms / 1000),
  maxHeight = 384,
  className = "",
}: TranscriptListProps) {
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const rangeRef = useRef({ startIndex: 0, endIndex: 0 });
  const virtual = segments.length > VIRTUAL_THRESHOLD;

  /* 跟读滚动：仅当当前句不在可视范围（被滚动出去/刚 seek 到远处）时居中跟随 */
  useEffect(() => {
    if (!virtual || activeIdx < 0) return;
    const { startIndex, endIndex } = rangeRef.current;
    if (activeIdx < startIndex || activeIdx > endIndex) {
      virtuosoRef.current?.scrollToIndex({ index: activeIdx, align: "center" });
    }
  }, [activeIdx, virtual]);

  if (segments.length === 0) return null;

  if (!virtual) {
    return (
      <div className={`overflow-hidden rounded-[var(--radius-sm)] border border-line ${className}`}>
        <ul style={{ maxHeight }} className="divide-y divide-line overflow-y-auto">
          {segments.map((seg, i) => (
            <li key={i}>
              <SegmentRow
                seg={seg}
                active={i === activeIdx}
                onSeek={onSeek}
                formatTimecode={formatTimecode}
                bordered={false}
              />
            </li>
          ))}
        </ul>
      </div>
    );
  }

  return (
    <div className={`overflow-hidden rounded-[var(--radius-sm)] border border-line ${className}`}>
      <Virtuoso
        ref={virtuosoRef}
        style={{ height: maxHeight }}
        totalCount={segments.length}
        data={segments}
        rangeChanged={(range) => (rangeRef.current = range)}
        itemContent={(i, seg) => (
          <SegmentRow
            seg={seg}
            active={i === activeIdx}
            onSeek={onSeek}
            formatTimecode={formatTimecode}
            bordered={i < segments.length - 1}
          />
        )}
      />
    </div>
  );
}
