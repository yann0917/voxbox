import { describe, expect, it } from "vitest";
import { buildICS } from "../src/lib/ics";

describe("buildICS", () => {
  it("renders events with dtstamp and uid", () => {
    const ics = buildICS([{ title: "评审会", start: "2026-10-07 14:00", end: "2026-10-07 15:00", description: "Q4 方案" }]);
    expect(ics).toContain("BEGIN:VCALENDAR");
    expect(ics).toContain("SUMMARY:评审会");
    expect(ics).toContain("DTSTART:20261007T140000");
    expect(ics).toContain("DTEND:20261007T150000");
  });

  it("emits one VEVENT with UID and DTSTAMP per event", () => {
    const ics = buildICS([
      { title: "评审会", start: "2026-10-07 14:00", end: "2026-10-07 15:00" },
      { title: "1:1", start: "2026-10-08 10:30" },
    ]);
    expect(ics.match(/BEGIN:VEVENT/g)).toHaveLength(2);
    expect(ics).toContain("UID:20261007T140000-0@voxbox");
    expect(ics).toContain("UID:20261008T103000-1@voxbox");
    expect(ics.match(/DTSTAMP:\d{8}T\d{6}Z/g)).toHaveLength(2);
  });

  it("omits DTEND when end is an empty string (no end time)", () => {
    const ics = buildICS([{ title: "截止", start: "2026-10-07 14:00", end: "", description: "提测" }]);
    expect(ics).toContain("DTSTART:20261007T140000");
    expect(ics).not.toContain("DTEND");
  });

  it("escapes commas semicolons and newlines in text values", () => {
    const ics = buildICS([
      { title: "同步, 议程; 下半场", start: "2026-10-07 14:00", description: "第一点,第二点;\n第三点" },
    ]);
    expect(ics).toContain("SUMMARY:同步\\, 议程\\; 下半场");
    expect(ics).toContain("DESCRIPTION:第一点\\,第二点\\;\\n第三点");
  });

  it("wraps lines with CRLF and closes the calendar", () => {
    const ics = buildICS([{ title: "评审会", start: "2026-10-07 14:00" }]);
    expect(ics.startsWith("BEGIN:VCALENDAR\r\n")).toBe(true);
    expect(ics.endsWith("END:VCALENDAR\r\n")).toBe(true);
    expect(ics).not.toContain("\n\n");
  });
});
