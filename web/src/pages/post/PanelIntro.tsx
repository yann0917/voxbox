import type { ReactNode } from "react";

export interface PanelIntroProps {
  title: string;
  description?: string;
  actions?: ReactNode;
}

/** 面板引言行：聚合页（PageHeader + Tabs）之下、面板内容之上的轻量页头——
 *  小一号的标题 + 说明 + 面板级动作（如「清空重选」），替代单页时代的 PageHeader。 */
export function PanelIntro({ title, description, actions }: PanelIntroProps) {
  return (
    <div className="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
      <div className="min-w-0">
        <h2 className="text-sm font-semibold leading-tight tracking-tight">{title}</h2>
        {description && <p className="mt-1 text-xs text-muted">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}
