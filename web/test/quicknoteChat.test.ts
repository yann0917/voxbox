import { describe, expect, it } from "vitest";
import {
  buildChatContext,
  citeToMs,
  clockText,
  linkifyCitations,
  seekHrefToMs,
  timedTranscript,
  todayText,
  type QNSegment,
} from "../src/pages/quicknote/model";

describe("clockText", () => {
  it("formats ms as HH:MM:SS with zero padding", () => {
    expect(clockText(0)).toBe("00:00:00");
    expect(clockText(4000)).toBe("00:00:04");
    expect(clockText(201000)).toBe("00:03:21");
    expect(clockText(3723000)).toBe("01:02:03");
  });

  it("carries over an hour naturally and clamps negatives", () => {
    expect(clockText(3661000)).toBe("01:01:01");
    expect(clockText(-5)).toBe("00:00:00");
  });
});

describe("timedTranscript", () => {
  const segs: QNSegment[] = [
    { text: "大家好，开始吧。", start_ms: 4000, end_ms: 6000, speaker: "0" },
    { text: "（无说话人标注的一句）", start_ms: 61000, end_ms: 63000 },
    { text: "会议定在周四。", start_ms: 3723000, end_ms: 3725000, speaker: "1" },
  ];

  it("renders HH:MM:SS-prefixed lines with speaker labels", () => {
    expect(timedTranscript(segs, (id) => `说话人${id}`)).toBe(
      ["00:00:04 说话人0：大家好，开始吧。", "00:01:01 （无说话人标注的一句）", "01:02:03 说话人1：会议定在周四。"].join("\n"),
    );
  });

  it("applies rename overrides through speakerLabel", () => {
    expect(timedTranscript([segs[0]], () => "老王")).toBe("00:00:04 老王：大家好，开始吧。");
  });
});

describe("todayText", () => {
  it("formats local date as YYYY-MM-DD", () => {
    expect(todayText(new Date(2026, 9, 1))).toBe("2026-10-01");
    expect(todayText(new Date(2026, 0, 5))).toBe("2026-01-05");
  });
});

describe("buildChatContext", () => {
  it("wraps transcript with citation convention and today", () => {
    const ctx = buildChatContext("00:00:04 说话人0：大家好", "2026-10-01");
    expect(ctx).toContain("00:00:04 说话人0：大家好");
    expect(ctx).toContain("【分:秒】");
    expect(ctx).toContain("如【03:21】");
    expect(ctx).toContain("今天是 2026-10-01");
  });

  it("truncates over-budget transcripts and keeps the assembled context within the 24000 cap", () => {
    const long = "字".repeat(24000); // 旧「转写 24000 不截断」会组装出 24097，超服务端封顶
    const ctx = buildChatContext(long, "2026-10-01");
    expect(ctx).toContain("（转写过长，已截断）");
    // 截断后的正文不再含完整原文；总长恒 ≤24000（服务端 len([]rune) 上限）
    expect(ctx.includes(long)).toBe(false);
    expect(ctx.length).toBeLessThanOrEqual(24000);
  });

  it("keeps an at-budget transcript intact; over budget lands exactly at the 24000 boundary", () => {
    // 预算 = 24000 − 包装文案（空转写的组装长）− 截断提示：预算内原文完整保留
    const budget = 24000 - buildChatContext("", "2026-10-01").length - "\n（转写过长，已截断）".length;
    const exact = "字".repeat(budget);
    const ctx = buildChatContext(exact, "2026-10-01");
    expect(ctx).toContain(exact);
    expect(ctx).not.toContain("已截断");
    expect(ctx.length).toBeLessThanOrEqual(24000);
    // 超预算 1 字即截断，组装总长恰好顶到 24000（服务端边界值放行）
    const over = buildChatContext("字".repeat(budget + 1), "2026-10-01");
    expect(over).toContain("（转写过长，已截断）");
    expect(over.length).toBe(24000);
  });
});

describe("citeToMs", () => {
  it("parses mm:ss and hh:mm:ss stamps to milliseconds", () => {
    expect(citeToMs("03:21")).toBe(201000);
    expect(citeToMs("0:00")).toBe(0);
    expect(citeToMs("01:02:03")).toBe(3723000);
  });

  it("rejects malformed stamps", () => {
    expect(citeToMs("abc")).toBeNull();
    expect(citeToMs("1:2:3:4")).toBeNull();
  });
});

describe("linkifyCitations", () => {
  it("leaves text without citations untouched", () => {
    expect(linkifyCitations("没有任何标注的普通回答。")).toBe("没有任何标注的普通回答。");
  });

  it("replaces citations with internal seek links", () => {
    expect(linkifyCitations("会议定在周四【03:21】。")).toBe("会议定在周四[03:21](#seek-201000)。");
  });

  it("handles multiple and hour-form citations", () => {
    expect(linkifyCitations("开头【0:00】结尾【01:02:03】")).toBe("开头[0:00](#seek-0)结尾[01:02:03](#seek-3723000)");
  });

  it("keeps partial brackets like an unclosed stamp as plain text", () => {
    expect(linkifyCitations("流式半截【03:2")).toBe("流式半截【03:2");
  });
});

describe("seekHrefToMs", () => {
  it("round-trips linkified citations back to the same milliseconds", () => {
    // 组合回归（审查点名的盲区）：ChatPanel 拿到的是 linkifyCitations 产出的
    // href（纯毫秒整数形态），必须解析回与 citeToMs 一致的跳播点
    for (const stamp of ["0:00", "03:21", "59:59", "01:02:03"]) {
      const md = linkifyCitations(`回答里引用【${stamp}】`);
      const href = /\]\((#seek-\d+)\)/.exec(md)?.[1];
      expect(href, `linkify 应为 ${stamp} 产出 seek 锚点`).toBeTruthy();
      expect(seekHrefToMs(href)).toBe(citeToMs(stamp));
    }
  });

  it("rejects non-seek or malformed hrefs (fallback to plain link)", () => {
    expect(seekHrefToMs(undefined)).toBeNull();
    expect(seekHrefToMs("https://example.com")).toBeNull();
    expect(seekHrefToMs("#seek-")).toBeNull();
    expect(seekHrefToMs("#seek-abc")).toBeNull();
    expect(seekHrefToMs("#seek-12x34")).toBeNull();
  });
});
