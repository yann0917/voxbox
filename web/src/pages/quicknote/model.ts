import { fetchJSON } from "../../lib/api";
import type { TaskDetail, TaskStatus } from "../../lib/types";

/** 转写分句：speaker 为上游说话人编号（"0"/"1"… 字符串，未启用分离时缺省） */
export type QNSegment = { text: string; start_ms: number; end_ms: number; speaker?: string };

/** 任务运行态：只保留界面需要的字段，不伪造完整 Task DTO（与语音识别/妙记页同一结构） */
export interface Run {
  status: TaskStatus;
  progress: number;
  note: string;
  error?: string;
}

/** 结果区视图：空闲 / 提交中 / 转写中 / 完成 / 出错 */
export type ResultView = "empty" | "submitting" | "progress" | "done" | "error";

/** 终态回读：进度通道收到收尾事件后拉任务详情（分句文字稿在 task.summary.segments） */
export function loadTask(id: string): Promise<TaskDetail> {
  return fetchJSON<TaskDetail>(`/api/tasks/${id}`);
}

/** 说话人统计：每人的发言轮数 / 时长 / 占比（无说话人标注的分句不计入） */
export function speakerStats(segs: QNSegment[]) {
  const m = new Map<string, { turns: number; ms: number }>();
  for (const s of segs) {
    if (!s.speaker) continue;
    const e = m.get(s.speaker) ?? { turns: 0, ms: 0 };
    e.turns++;
    e.ms += Math.max(0, s.end_ms - s.start_ms);
    m.set(s.speaker, e);
  }
  const total = [...m.values()].reduce((a, b) => a + b.ms, 0) || 1;
  return [...m].map(([id, v]) => ({ id, ...v, pct: v.ms / total }));
}
