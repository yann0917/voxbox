import type { ReactNode } from "react";

export interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: ReactNode;
  icon?: ReactNode;
}

/** 页面头部：标题 + 说明 + 右侧主操作。每个页面只在这里出现一次标题（顶栏不再重复）。 */
export function PageHeader({ title, description, actions, icon }: PageHeaderProps) {
  return (
    <header className="rise mb-6 flex flex-wrap items-start justify-between gap-4">
      <div className="flex min-w-0 items-start gap-3">
        {icon && (
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-[var(--radius-sm)] border border-line bg-raise text-accent">
            {icon}
          </span>
        )}
        <div className="min-w-0">
          <h1 className="text-xl font-semibold leading-tight tracking-tight">{title}</h1>
          {description && <p className="mt-1 text-xs text-muted">{description}</p>}
        </div>
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  );
}
