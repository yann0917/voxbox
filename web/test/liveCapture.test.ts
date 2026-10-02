import { afterEach, describe, expect, it } from "vitest";
import { captureSupport } from "../src/lib/liveCapture";

/** captureSupport 的浏览器特性检测。重点回归：AudioContext.prototype.audioWorklet
 *  是带 brand check 的 IDL 访问器，Chrome/Edge 对非实例取值抛 Illegal invocation——
 *  探针写法会让页面在所有现代浏览器上打不开（2026-10-02 线上事故）。
 *  jsdom 没有 AudioWorkletNode，这里用全局桩模拟 Chrome 形态。 */
describe("captureSupport", () => {
  const g = globalThis as Record<string, unknown>;
  let savedWorklet: unknown;
  let savedCtx: unknown;
  const installChromeShape = () => {
    savedWorklet = g.AudioWorkletNode;
    savedCtx = g.AudioContext;
    g.AudioWorkletNode = class {};
    // brand-check 访问器：原型取值即抛（Chrome 对 IDL 访问器的实际行为）
    class FakeAudioContext {}
    Object.defineProperty(FakeAudioContext.prototype, "audioWorklet", {
      get() {
        throw new TypeError("Illegal invocation");
      },
    });
    Object.defineProperty(FakeAudioContext.prototype, "createScriptProcessor", {
      value: function () {},
    });
    g.AudioContext = FakeAudioContext;
  };

  afterEach(() => {
    if (savedWorklet === undefined) delete g.AudioWorkletNode;
    else g.AudioWorkletNode = savedWorklet;
    if (savedCtx === undefined) delete g.AudioContext;
    else g.AudioContext = savedCtx;
  });

  it("does not throw when the worklet getter brand-checks (Chrome shape)", () => {
    installChromeShape();
    // 有 mediaDevices（jsdom navigator 缺省无——补桩）
    Object.defineProperty(navigator, "mediaDevices", {
      value: { getUserMedia: () => {} },
      configurable: true,
    });
    try {
      expect(captureSupport()).toEqual({ ok: true, mode: "worklet" });
    } finally {
      delete (navigator as { mediaDevices?: unknown }).mediaDevices;
    }
  });

  it("falls back to script processor when worklet is absent", () => {
    delete g.AudioWorkletNode;
    installChromeShape();
    g.AudioWorkletNode = undefined;
    Object.defineProperty(navigator, "mediaDevices", {
      value: { getUserMedia: () => {} },
      configurable: true,
    });
    try {
      expect(captureSupport()).toEqual({ ok: true, mode: "script" });
    } finally {
      delete (navigator as { mediaDevices?: unknown }).mediaDevices;
    }
  });
});
