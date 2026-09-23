import type { ReactNode } from "react";

export interface TabItem<T extends string> {
  value: T;
  label: string;
  icon?: ReactNode;
}

export interface TabsProps<T extends string> {
  items: TabItem<T>[];
  value: T;
  onChange: (value: T) => void;
  className?: string;
}

/** 分段控件：整块底槽 + 激活项提亮，键盘方向键可切换。 */
export function Tabs<T extends string>({ items, value, onChange, className = "" }: TabsProps<T>) {
  const onKeyDown = (e: React.KeyboardEvent) => {
    const idx = items.findIndex((i) => i.value === value);
    if (idx < 0) return;
    if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      e.preventDefault();
      const next = e.key === "ArrowRight" ? (idx + 1) % items.length : (idx - 1 + items.length) % items.length;
      onChange(items[next].value);
    }
  };

  return (
    <div
      role="tablist"
      onKeyDown={onKeyDown}
      className={`inline-flex items-center gap-0.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-0.5 ${className}`}
    >
      {items.map((it) => {
        const active = it.value === value;
        return (
          <button
            key={it.value}
            role="tab"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(it.value)}
            className={`inline-flex cursor-pointer items-center gap-1.5 rounded-[5px] px-3 py-1.5 text-xs transition-colors duration-150 ${
              active ? "bg-raise text-fg shadow-[var(--shadow-1)]" : "text-muted hover:text-fg-2"
            }`}
          >
            {it.icon}
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
