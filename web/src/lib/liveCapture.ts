/**
 * 实时字幕麦克风采集：浏览器只负责「使用麦克风听」——getUserMedia 打开麦克风，
 * 音频图归一为 16kHz 单声道，产出 16 位小端音频分片交给上层实时会话上传。
 * 引擎连接不在这里：采集与传输完全解耦，本模块不知道服务端存在。
 *
 * 处理器选择：优先 AudioWorklet（独立线程、不占主线程）；特性检测不可用或装载
 * 失败时自动降级 ScriptProcessor（主线程回调，兼容旧内核）；两者皆不可用时报
 * 明确错误。AudioContext 声明 16kHz 采样（内核不支持时回退默认采样率，由本模块
 * 线性重采样归一），停止后彻底释放音轨与音频上下文。
 */

/** 目标采样率：识别引擎标准输入（16kHz 单声道） */
export const TARGET_RATE = 16000;
/** 上送分片时长（毫秒）：约 100ms 一片，兼顾实时性与消息开销 */
const CHUNK_MS = 100;

export interface LiveCaptureSession {
  /** 停止采集并释放麦克风与音频上下文（幂等，可重复调用） */
  stop: () => void;
  /** 实时输入电平 0..1（平滑后的 RMS），供录音指示条用 */
  level: () => number;
}

export type CaptureMode = "worklet" | "script";

/** 特性检测：getUserMedia + 音频图 + 至少一种处理器可用。
 *  非安全上下文（http 非 localhost）下没有 mediaDevices，一并归入不支持。 */
export function captureSupport(): { ok: boolean; mode: CaptureMode | null } {
  if (typeof navigator === "undefined" || !navigator.mediaDevices?.getUserMedia) {
    return { ok: false, mode: null };
  }
  if (typeof AudioContext === "undefined") return { ok: false, mode: null };
  if (typeof AudioWorkletNode !== "undefined" && AudioContext.prototype.audioWorklet) {
    return { ok: true, mode: "worklet" };
  }
  if (typeof AudioContext.prototype.createScriptProcessor === "function") {
    return { ok: true, mode: "script" };
  }
  return { ok: false, mode: null };
}

/** Float32 采样（任意源采样率）→ 16kHz s16le 单声道原始字节。
 *  源率不同走线性插值重采样；采样钳制到 [-1,1] 后按符号映射到 int16 全量程。 */
export function toPcm16k(samples: Float32Array, srcRate: number): Uint8Array<ArrayBuffer> {
  if (srcRate <= 0 || samples.length === 0) return new Uint8Array(0);
  let out = samples;
  const ratio = srcRate / TARGET_RATE;
  if (Math.abs(ratio - 1) > 1e-6) {
    const n = Math.floor(samples.length / ratio);
    const resampled = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      const pos = i * ratio;
      const i0 = Math.floor(pos);
      const i1 = Math.min(i0 + 1, samples.length - 1);
      const frac = pos - i0;
      resampled[i] = samples[i0] * (1 - frac) + samples[i1] * frac;
    }
    out = resampled;
  }
  const pcm = new Int16Array(out.length);
  for (let i = 0; i < out.length; i++) {
    const s = Math.max(-1, Math.min(1, out[i]));
    pcm[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  return new Uint8Array(pcm.buffer);
}

// AudioWorklet 处理器源码（Blob 装载，免独立资源文件）：累满 2048 帧上抛一次，
// 16kHz 下约 128ms/条消息，避免逐渲染量子（128 帧）高频 postMessage。
const WORKLET_SRC = `
class VoxLiveCapture extends AudioWorkletProcessor {
  constructor() {
    super();
    this._buf = new Float32Array(2048);
    this._fill = 0;
  }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) {
      let i = 0;
      while (i < ch.length) {
        const n = Math.min(ch.length - i, this._buf.length - this._fill);
        this._buf.set(ch.subarray(i, i + n), this._fill);
        this._fill += n;
        i += n;
        if (this._fill === this._buf.length) {
          this.port.postMessage(this._buf.slice(0));
          this._fill = 0;
        }
      }
    }
    return true;
  }
}
registerProcessor("vox-live-capture", VoxLiveCapture);
`;

/** 麦克风不可用的人类可读原因（页面直接展示；权限/占用场景给行动指引） */
export function micErrorMessage(e: unknown, desktop: boolean): string {
  const err = e as DOMException;
  switch (err?.name) {
    case "NotAllowedError":
    case "PermissionDeniedError":
      return desktop
        ? "麦克风权限被拒绝：请在系统设置的麦克风权限中允许 VoxBox 后重试"
        : "麦克风权限被拒绝：请在浏览器地址栏允许使用麦克风后重试";
    case "NotReadableError":
    case "TrackStartError":
      return "麦克风被其他应用占用：请关闭占用麦克风的程序后重试";
    default:
      return `无法开始采集：${err?.message ?? String(e)}`;
  }
}

/** 开始采集：请求麦克风 → 建音频图 → 起处理器，分片经 onChunk 交出（16kHz s16le 单声道）。
 *  抛错时调用方无需清理（本函数内部已回收已建资源）；返回的会话 stop 幂等。 */
export async function startLiveCapture(
  onChunk: (pcm: Uint8Array<ArrayBuffer>) => void,
): Promise<LiveCaptureSession> {
  const support = captureSupport();
  if (!support.ok) {
    throw new Error("当前环境不支持麦克风采集：需要在 https 或本机环境下使用");
  }

  // 1. 麦克风（权限提示在此触发；失败时后续资源尚未创建，直接抛出即可）
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
  });

  // 2. 音频上下文：声明 16kHz；内核拒绝非常规采样率时回退默认率（靠重采样归一）
  let ctx: AudioContext;
  try {
    ctx = new AudioContext({ sampleRate: TARGET_RATE });
  } catch {
    ctx = new AudioContext();
  }
  const release = () => {
    stream.getTracks().forEach((t) => t.stop());
    void ctx.close().catch(() => {});
  };

  try {
    const source = ctx.createMediaStreamSource(stream);
    // 静音汇：处理器必须接入音频图才会被拉动，经零增益避免回放外放
    const sink = ctx.createGain();
    sink.gain.value = 0;
    sink.connect(ctx.destination);

    let mode: CaptureMode = support.mode!;
    let node: AudioWorkletNode | ScriptProcessorNode | null = null;

    if (mode === "worklet") {
      try {
        const url = URL.createObjectURL(new Blob([WORKLET_SRC], { type: "text/javascript" }));
        try {
          await ctx.audioWorklet.addModule(url);
        } finally {
          URL.revokeObjectURL(url);
        }
        const worklet = new AudioWorkletNode(ctx, "vox-live-capture", {
          numberOfInputs: 1,
          numberOfOutputs: 1,
          outputChannelCount: [1],
        });
        worklet.port.onmessage = (e) => feed(e.data as Float32Array);
        source.connect(worklet);
        worklet.connect(sink);
        node = worklet;
      } catch {
        // 装载失败（内核实现差异/策略限制）：无声降级 ScriptProcessor
        mode = "script";
        node = null;
      }
    }

    if (mode === "script") {
      const processor = ctx.createScriptProcessor(4096, 1, 1);
      processor.onaudioprocess = (e) => feed(e.inputBuffer.getChannelData(0));
      source.connect(processor);
      processor.connect(sink);
      node = processor;
    }

    // 浏览器自动播放策略兜底：getUserMedia 在用户手势后调用，通常已 running
    if (ctx.state === "suspended") void ctx.resume().catch(() => {});

    // ---- 分片积累：整数倍 1600 采样（100ms@16k）成片上送，尾片在 stop 时冲刷 ----
    const CHUNK_SAMPLES = (TARGET_RATE * CHUNK_MS) / 1000;
    let queue: Int16Array[] = [];
    let queued = 0;
    let rms = 0;
    let stopped = false;

    const feed = (f32: Float32Array) => {
      if (stopped || !f32 || f32.length === 0) return;
      // 输入电平（指数平滑，增益让常温说话电平可见）
      let sum = 0;
      for (let i = 0; i < f32.length; i++) sum += f32[i] * f32[i];
      rms = rms * 0.7 + Math.sqrt(sum / f32.length) * 0.3;

      const s16 = new Int16Array(toPcm16k(f32, ctx.sampleRate).buffer);
      queue.push(s16);
      queued += s16.length;
      while (queued >= CHUNK_SAMPLES) {
        const out = new Int16Array(CHUNK_SAMPLES);
        let fill = 0;
        while (fill < CHUNK_SAMPLES) {
          const head = queue[0];
          const take = Math.min(head.length, CHUNK_SAMPLES - fill);
          out.set(head.subarray(0, take), fill);
          fill += take;
          if (take === head.length) queue.shift();
          else queue[0] = head.subarray(take);
        }
        queued -= CHUNK_SAMPLES;
        onChunk(new Uint8Array(out.buffer));
      }
    };

    return {
      level: () => Math.min(1, rms * 4),
      stop: () => {
        if (stopped) return;
        stopped = true;
        // 冲刷不足一片的尾样，避免句尾 100ms 内音频丢失
        if (queued > 0) {
          const rest = new Int16Array(queued);
          let fill = 0;
          while (fill < queued) {
            const head = queue[0];
            rest.set(head.subarray(0, head.length), fill);
            fill += head.length;
            queue.shift();
          }
          onChunk(new Uint8Array(rest.buffer));
        }
        queue = [];
        queued = 0;
        if (node) {
          node.disconnect();
          if ("port" in node) node.port.onmessage = null;
          else node.onaudioprocess = null;
        }
        source.disconnect();
        sink.disconnect();
        release();
      },
    };
  } catch (e) {
    release();
    throw e;
  }
}
