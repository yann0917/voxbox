import { ChevronLeft, ChevronRight } from "lucide-react";

export interface PaginationProps {
  /** 当前页（1 起始） */
  page: number;
  /** 总页数（≥1） */
  pageCount: number;
  /** 总条数：提供时在页码旁展示「共 N 条」 */
  total?: number;
  onChange: (page: number) => void;
  className?: string;
}

const pageButton =
  "flex size-7 cursor-pointer items-center justify-center rounded-[5px] text-muted transition-colors duration-150 hover:bg-raise-2 hover:text-fg-2 disabled:pointer-events-none disabled:opacity-40";

/** 分页条：上一页 · 页码指示 · 下一页。单页（pageCount ≤ 1）不渲染。 */
export function Pagination({ page, pageCount, total, onChange, className = "" }: PaginationProps) {
  if (pageCount <= 1) return null;
  return (
    <nav aria-label="分页" className={`flex items-center justify-center gap-2 px-4 py-3 ${className}`}>
      <button
        type="button"
        className={pageButton}
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
        aria-label="上一页"
      >
        <ChevronLeft size={15} strokeWidth={1.75} />
      </button>
      <span className="font-mono text-xs tabular-nums text-muted">
        {page} / {pageCount}
        {total !== undefined && <span className="ml-2">共 {total} 条</span>}
      </span>
      <button
        type="button"
        className={pageButton}
        disabled={page >= pageCount}
        onClick={() => onChange(page + 1)}
        aria-label="下一页"
      >
        <ChevronRight size={15} strokeWidth={1.75} />
      </button>
    </nav>
  );
}
