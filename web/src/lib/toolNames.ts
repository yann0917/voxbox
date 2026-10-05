import { features } from "../features";

// 工具名映射自功能清单(features.ts 的 tools 声明)派生,新增工具在清单条目里声明。

/** 工具名 → 中文名（HistoryPage 筛选器与全局任务通知共用）。 */
export const toolName: Record<string, string> = Object.fromEntries(
  features.flatMap((f) => f.tools?.map((t) => [t.name, t.label] as const) ?? []),
);

export const toolLabel = (t: string): string => toolName[t] ?? t;

/** 工具名 → 控制台路由（历史页重跑后跳转等跨页导航用）。 */
export const toolRoute: Record<string, string> = Object.fromEntries(
  features.flatMap((f) => f.tools?.map((t) => [t.name, t.route] as const) ?? []),
);
