import { describe, expect, it } from "vitest";
import { failedStatusText, replayFileId } from "../src/pages/quicknote/model";

describe("replayFileId", () => {
  it("takes the first upload file id from the task input (file_ids channel)", () => {
    // 录音笔记创建走 file_ids 通道，GET /api/tasks/:id 的 task.input 原样回传 InputRef JSON
    expect(replayFileId({ file_ids: ["up-1", "up-2"] })).toBe("up-1");
  });

  it("skips blank entries and returns null on an empty list", () => {
    expect(replayFileId({ file_ids: ["", "  ", "up-3"] })).toBe("up-3");
    expect(replayFileId({ file_ids: [] })).toBeNull();
  });

  it("returns null for artifact/url channels or malformed input", () => {
    expect(replayFileId({ artifact_input: "art-1" })).toBeNull();
    expect(replayFileId({ artifact_inputs: ["art-1"] })).toBeNull();
    expect(replayFileId({ file_ids: "up-1" })).toBeNull();
    expect(replayFileId({})).toBeNull();
    expect(replayFileId(null)).toBeNull();
    expect(replayFileId(undefined)).toBeNull();
    expect(replayFileId("file_ids")).toBeNull();
  });
});

describe("failedStatusText", () => {
  it("maps canceled/interrupted to their own words and other failures to 转写失败", () => {
    expect(failedStatusText("canceled")).toBe("已取消");
    expect(failedStatusText("interrupted")).toBe("已中断");
    expect(failedStatusText("failed")).toBe("转写失败");
  });
});
