import type { ReactNode } from "react";

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`card-surface rounded-[var(--radius-md)] border border-line shadow-[var(--shadow-1)] ${className}`}
    >
      {children}
    </div>
  );
}

export interface CardHeaderProps {
  title: string;
  icon?: ReactNode;
  aside?: ReactNode;
  className?: string;
}

export function CardHeader({ title, icon, aside, className = "" }: CardHeaderProps) {
  return (
    <div className={`flex items-center justify-between gap-3 border-b border-line px-4 py-3 ${className}`}>
      <div className="flex items-center gap-2 min-w-0">
        {icon && <span className="text-muted shrink-0">{icon}</span>}
        <h2 className="micro truncate">{title}</h2>
      </div>
      {aside && <div className="flex items-center gap-2 shrink-0">{aside}</div>}
    </div>
  );
}

export interface CardBodyProps {
  children: ReactNode;
  /** 与默认内边距 p-4 叠加（不覆盖）：一般传 space-y-* 控制子元素纵向节奏。
   *  需要改 padding 时显式传 px-、py- 或 p- 系列覆盖（Tailwind 保证 px-、py- 优先于 p-）。 */
  className?: string;
}

/** 卡体：默认内边距 p-4（MASTER 规格「内部 16 间距」）。
 *  注意 className 是叠加而非替换——默认值写进参数默认值会被整体覆盖，曾导致全站卡片掉内边距。 */
export function CardBody({ children, className = "" }: CardBodyProps) {
  return <div className={`p-4 ${className}`}>{children}</div>;
}

export function Skeleton({ className = "" }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={`animate-pulse rounded-[var(--radius-sm)] bg-raise-2 ${className}`}
    />
  );
}

export interface EmptyStateProps {
  icon: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}

export function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-12 text-center">
      <div className="flex size-11 items-center justify-center rounded-full border border-line bg-raise-2 text-muted">
        {icon}
      </div>
      <div className="space-y-1">
        <p className="text-sm font-medium text-fg">{title}</p>
        {description && <p className="mx-auto max-w-sm text-xs text-muted">{description}</p>}
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  );
}
