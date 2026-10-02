/**
 * 实时字幕文本层纯函数：两层文本（已提交 + 进行中尾随）的快照合并、说话人分句聚合
 * 与计时格式化。与引擎/传输解耦，全部可单测。
 *
 * 协议要点（与 /api/ws/live 对齐）：
 *  - partial 为 REPLACE 快照：整层覆盖，绝不与上一层拼接；
 *  - final 为定格全量（stop 后二遍修正/本地 done 全量）；
 *  - 引擎差异按键存在性分流：火山 committed/unstable/segments 三层齐全，
 *    本地引擎只有 committed（无 unstable/segments 键）。
 */

export interface LiveSegment {
  text: string;
  start_ms: number;
  end_ms: number;
  /** 上游说话人编号（"0"/"1"…字符串），未启用分离或缺省时不存在 */
  speaker?: string;
}

/** 字幕渲染态：已提交正文 + 进行中尾随 + 分句（说话人视图用） */
export interface LiveCaptionState {
  committed: string;
  unstable: string;
  segments: LiveSegment[];
}

export const emptyLiveCaption: LiveCaptionState = { committed: "", unstable: "", segments: [] };

export interface LivePartialPayload {
  committed?: string;
  unstable?: string;
  segments?: LiveSegment[];
}

export interface LiveFinalPayload {
  text?: string;
  segments?: LiveSegment[];
  degraded?: boolean;
  duration_ms?: number;
}

/** 增量快照入态：REPLACE 语义，直接以服务端快照覆盖两层文本。
 *  segments 键缺失（本地引擎/早期快照）视为「本引擎无分句层」，清空而不是沿用。 */
export function applyLivePartial(_prev: LiveCaptionState, p: LivePartialPayload): LiveCaptionState {
  return { committed: p.committed ?? "", unstable: p.unstable ?? "", segments: p.segments ?? [] };
}

/** 定格全量入态：文本以 final 为准、进行中层清空；final 未带分句时保留快照期的分句。 */
export function applyLiveFinal(prev: LiveCaptionState, f: LiveFinalPayload): LiveCaptionState {
  return { committed: f.text ?? "", unstable: "", segments: f.segments ?? prev.segments };
}

/** 字幕区是否有可展示内容（决定空态与保存可用性） */
export function captionHasText(s: LiveCaptionState): boolean {
  return s.committed.length > 0 || s.unstable.length > 0;
}

export interface SpeakerLine {
  /** undefined = 无说话人标记（未启用分离/本地引擎） */
  speaker?: string;
  text: string;
}

/** 分句聚合：连续同说话人的句子合并为一行（字幕流按人分段）；无标记时归并为纯文本流。 */
export function groupConsecutiveSpeaker(segments: LiveSegment[]): SpeakerLine[] {
  const lines: SpeakerLine[] = [];
  for (const s of segments) {
    if (!s.text) continue;
    const last = lines[lines.length - 1];
    if (last && last.speaker === s.speaker) last.text += s.text;
    else lines.push({ speaker: s.speaker, text: s.text });
  }
  return lines;
}

/** 说话人展示名：上游编号 "0" 起 → 「说话人 1」；非编号原样透出；无标记不展示。 */
export function speakerLabel(speaker: string | undefined): string | undefined {
  if (speaker === undefined || speaker === "") return undefined;
  const n = Number(speaker);
  return Number.isFinite(n) ? `说话人 ${n + 1}` : speaker;
}

/** 会话计时：不足 1 小时 mm:ss，超过后 h:mm:ss。 */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`;
}
