import { describe, expect, it } from "vitest";
import { toPcm16k } from "../src/lib/liveCapture";
import {
  applyLiveFinal,
  applyLivePartial,
  captionHasText,
  emptyLiveCaption,
  formatElapsed,
  groupConsecutiveSpeaker,
  speakerLabel,
  type LiveCaptionState,
} from "../src/lib/liveCaption";

const prev: LiveCaptionState = {
  committed: "上一层的已提交文本",
  unstable: "上一层进行中",
  segments: [{ text: "旧分句", start_ms: 0, end_ms: 10, speaker: "0" }],
};

describe("applyLivePartial（REPLACE 快照语义）", () => {
  it("整层覆盖：不与上一层拼接", () => {
    const s = applyLivePartial(prev, {
      committed: "我们下周三下午",
      unstable: "三点开",
      segments: [{ text: "我们下周三下午", start_ms: 0, end_ms: 2000, speaker: "0" }],
    });
    expect(s.committed).toBe("我们下周三下午");
    expect(s.unstable).toBe("三点开");
    expect(s.segments).toHaveLength(1);
  });

  it("空快照也是覆盖：不会残留上一层文本", () => {
    const s = applyLivePartial(prev, {});
    expect(s).toEqual({ committed: "", unstable: "", segments: [] });
  });

  it("本地引擎无 unstable/segments 键：两层归零，仅保留 committed", () => {
    const s = applyLivePartial(emptyLiveCaption, { committed: "本地已识别全文" });
    expect(s.committed).toBe("本地已识别全文");
    expect(s.unstable).toBe("");
    expect(s.segments).toEqual([]);
  });
});

describe("applyLiveFinal（定格全量）", () => {
  it("以 final 文本为准，进行中层清空", () => {
    const s = applyLiveFinal(
      { committed: "我们下周三下午", unstable: "三点开", segments: [] },
      { text: "我们下周三下午3点开会。", duration_ms: 5477, segments: [{ text: "我们下周三下午3点开会。", start_ms: 0, end_ms: 5477 }] },
    );
    expect(s.committed).toBe("我们下周三下午3点开会。");
    expect(s.unstable).toBe("");
    expect(s.segments).toHaveLength(1);
  });

  it("final 未带 segments 时沿用快照期分句", () => {
    const oldSegs = [{ text: "旧", start_ms: 0, end_ms: 1 }];
    const s = applyLiveFinal({ committed: "a", unstable: "", segments: oldSegs }, { text: "全量" });
    expect(s.segments).toBe(oldSegs);
  });

  it("零文本 final：committed 为空串（正常空会话，保存由服务端拒绝）", () => {
    const s = applyLiveFinal(prev, { text: "", duration_ms: 0 });
    expect(s.committed).toBe("");
    expect(s.unstable).toBe("");
  });
});

describe("captionHasText", () => {
  it("已提交或进行中任一非空即为有内容", () => {
    expect(captionHasText(emptyLiveCaption)).toBe(false);
    expect(captionHasText({ committed: "", unstable: "嗯", segments: [] })).toBe(true);
    expect(captionHasText({ committed: "你好", unstable: "", segments: [] })).toBe(true);
  });
});

describe("groupConsecutiveSpeaker（说话人分句聚合）", () => {
  it("连续同说话人合并为一行，换人即分段", () => {
    const lines = groupConsecutiveSpeaker([
      { text: "你好", start_ms: 0, end_ms: 500, speaker: "0" },
      { text: "，世界。", start_ms: 500, end_ms: 1000, speaker: "0" },
      { text: "好的，我记下。", start_ms: 1200, end_ms: 2000, speaker: "1" },
    ]);
    expect(lines).toEqual([
      { speaker: "0", text: "你好，世界。" },
      { speaker: "1", text: "好的，我记下。" },
    ]);
  });

  it("无说话人标记（本地引擎）归并为纯文本流", () => {
    const lines = groupConsecutiveSpeaker([
      { text: "第一句。", start_ms: 0, end_ms: 500 },
      { text: "第二句。", start_ms: 600, end_ms: 900 },
    ]);
    expect(lines).toEqual([{ speaker: undefined, text: "第一句。第二句。" }]);
  });

  it("跳过空文本分句", () => {
    expect(groupConsecutiveSpeaker([{ text: "", start_ms: 0, end_ms: 0 }])).toEqual([]);
  });
});

describe("speakerLabel", () => {
  it("上游编号从 0 起映射为「说话人 N」", () => {
    expect(speakerLabel("0")).toBe("说话人 1");
    expect(speakerLabel("11")).toBe("说话人 12");
  });
  it("无标记/空串不展示，非编号原样透出", () => {
    expect(speakerLabel(undefined)).toBeUndefined();
    expect(speakerLabel("")).toBeUndefined();
    expect(speakerLabel("主持人")).toBe("主持人");
  });
});

describe("formatElapsed（会话计时）", () => {
  it("不足 1 小时为 mm:ss，超过后 h:mm:ss", () => {
    expect(formatElapsed(0)).toBe("00:00");
    expect(formatElapsed(5477)).toBe("00:05");
    expect(formatElapsed(65_000)).toBe("01:05");
    expect(formatElapsed(3_661_000)).toBe("1:01:01");
  });
  it("负数按 0 处理", () => {
    expect(formatElapsed(-1)).toBe("00:00");
  });
});

describe("toPcm16k（Float32 → 16kHz s16le 单声道字节）", () => {
  it("同采样率直转：小端 int16 全量程钳制", () => {
    const bytes = toPcm16k(new Float32Array([0, 0.5, -1, 2, -2]), 16000);
    const view = new DataView(bytes.buffer);
    expect(bytes.byteLength).toBe(10);
    expect(view.getInt16(0, true)).toBe(0);
    expect(view.getInt16(2, true)).toBe(Math.floor(0.5 * 0x7fff)); // 16383
    expect(view.getInt16(4, true)).toBe(-0x8000);
    expect(view.getInt16(6, true)).toBe(0x7fff); // 2 → 钳到 +1
    expect(view.getInt16(8, true)).toBe(-0x8000); // -2 → 钳到 -1
  });

  it("异采样率线性重采样：48k→16k 长度三分之一，采样点对齐", () => {
    const src = new Float32Array(4800);
    for (let i = 0; i < src.length; i++) src[i] = i / src.length;
    const bytes = toPcm16k(src, 48000);
    expect(bytes.byteLength).toBe(1600 * 2);
    const view = new DataView(bytes.buffer);
    expect(view.getInt16(0, true)).toBe(0); // 首点即 src[0]
    expect(view.getInt16(2, true)).toBe(Math.floor((3 / 4800) * 0x7fff)); // 第 2 点=src[3]
  });

  it("空输入产出空字节", () => {
    expect(toPcm16k(new Float32Array(0), 16000).byteLength).toBe(0);
  });
});
