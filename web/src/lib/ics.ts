// 日历导出（录音笔记事件模式）：把 LLM 抽取的事件拼成最小 ICS（RFC 5545）。
// 时间为本地浮游时间（无 TZID/无 Z）——录音笔记面向单机使用，导入端按本地时区
// 解释；行不做 75 字节折叠（主流客户端容忍，长描述兼容性记账在任务报告）。

/** 日历事件：start/end 为 "YYYY-MM-DD HH:mm"（refine events 模式的输出格式）；
 *  end 空串/缺省 = 无结束时间（省略 DTEND，导入端按无 DTEND 事件处理）。 */
export interface ICSEvent {
  title: string;
  start: string;
  end?: string;
  description?: string;
}

const pad2 = (n: number) => String(n).padStart(2, "0");

/** "2026-10-07 14:00"（或带秒/T 分隔）→ "20261007T140000" 本地浮游时间；解析失败回空串。 */
function localStamp(s: string): string {
  const m = /^\s*(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?/.exec(s);
  if (!m) return "";
  const [, y, mo, d, h, mi, sec] = m;
  return `${y}${mo}${d}T${h}${mi}${sec ?? "00"}`;
}

/** TEXT 值转义：反斜杠/分号/逗号按 RFC 5545 加反斜杠，换行折叠成字面 \n。 */
function escapeText(s: string): string {
  return s.replace(/\\/g, "\\\\").replace(/;/g, "\\;").replace(/,/g, "\\,").replace(/\r?\n/g, "\\n");
}

/** 拼最小 VCALENDAR：每个可解析事件一个 VEVENT（UID 含序号保证稳定可去重），
 *  start 无法解析的事件跳过（非法 DTSTART 会让整份文件被导入端拒绝）。 */
export function buildICS(events: ICSEvent[]): string {
  const now = new Date();
  const dtstamp =
    `${now.getUTCFullYear()}${pad2(now.getUTCMonth() + 1)}${pad2(now.getUTCDate())}` +
    `T${pad2(now.getUTCHours())}${pad2(now.getUTCMinutes())}${pad2(now.getUTCSeconds())}Z`;
  const lines = [
    "BEGIN:VCALENDAR",
    "VERSION:2.0",
    "PRODID:-//voxbox//VoxBox//CN",
    "CALSCALE:GREGORIAN",
  ];
  events.forEach((e, i) => {
    const start = localStamp(e.start);
    if (!start) return;
    lines.push(
      "BEGIN:VEVENT",
      `UID:${start}-${i}@voxbox`,
      `DTSTAMP:${dtstamp}`,
      `DTSTART:${start}`,
    );
    const end = e.end ? localStamp(e.end) : "";
    if (end) lines.push(`DTEND:${end}`);
    lines.push(`SUMMARY:${escapeText(e.title)}`);
    if (e.description && e.description.trim()) lines.push(`DESCRIPTION:${escapeText(e.description)}`);
    lines.push("END:VEVENT");
  });
  lines.push("END:VCALENDAR");
  return lines.join("\r\n") + "\r\n";
}

/** 可落为 VEVENT 的事件数：start 无法解析的会被 buildICS 跳过（非法 DTSTART 会让
 *  整份文件被导入端拒绝）。「全部导出」据此对账——实际写入数少于选中数时给出警示。 */
export function icsEventCount(events: ICSEvent[]): number {
  return events.filter((e) => localStamp(e.start) !== "").length;
}

/** 前端直出 .ics 文件：Blob + 临时 <a download> 触发保存（无需服务端往返）。 */
export function downloadICS(events: ICSEvent[], filename: string): void {
  const url = URL.createObjectURL(new Blob([buildICS(events)], { type: "text/calendar;charset=utf-8" }));
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}
