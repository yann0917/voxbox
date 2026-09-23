/**
 * 麦克风录音 → ASR 兼容 WAV 文件。
 * 浏览器 MediaRecorder 产出 webm/opus 或 mp4/aac（随平台），ASR 只收 mp3/wav/ogg/pcm，
 * 因此停止后统一解码重采样为 16kHz 单声道 16-bit PCM WAV（sauc 识别标准格式）。
 */

const MIME_CANDIDATES = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"];
const TARGET_SAMPLE_RATE = 16000;

export interface RecordingSession {
  stop: () => Promise<File>;
  cancel: () => void;
  /** 实时电平 0..1（RMS），供录音电平条用 */
  level: () => number;
}

export function recordingSupported(): boolean {
  return typeof navigator !== "undefined" && !!navigator.mediaDevices?.getUserMedia && typeof MediaRecorder !== "undefined";
}

function pickMime(): string {
  for (const m of MIME_CANDIDATES) {
    if (typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(m)) return m;
  }
  return "";
}

export async function startRecording(): Promise<RecordingSession> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { echoCancellation: true, noiseSuppression: true },
  });

  const mime = pickMime();
  const recorder = mime ? new MediaRecorder(stream, { mimeType: mime }) : new MediaRecorder(stream);
  const chunks: BlobPart[] = [];
  recorder.ondataavailable = (e) => {
    if (e.data.size > 0) chunks.push(e.data);
  };
  recorder.start(250); // 每 250ms 收一块，stop 时 onstop 前数据完整

  // 电平分析：AudioContext 只做分析不参与产物编码
  const ctx = new AudioContext();
  const source = ctx.createMediaStreamSource(stream);
  const analyser = ctx.createAnalyser();
  analyser.fftSize = 512;
  source.connect(analyser);
  const timeData = new Float32Array(analyser.fftSize);
  let rms = 0;
  const levelTimer = window.setInterval(() => {
    analyser.getFloatTimeDomainData(timeData);
    let sum = 0;
    for (let i = 0; i < timeData.length; i++) sum += timeData[i] * timeData[i];
    rms = Math.sqrt(sum / timeData.length);
  }, 100);

  const cleanup = () => {
    window.clearInterval(levelTimer);
    stream.getTracks().forEach((t) => t.stop());
    void ctx.close();
  };

  return {
    level: () => Math.min(1, rms * 4), // 增益让常温说话电平可见

    stop: () =>
      new Promise<File>((resolve, reject) => {
        recorder.onstop = async () => {
          cleanup();
          try {
            const blob = new Blob(chunks, { type: recorder.mimeType || "audio/webm" });
            const file = await toWav16k(blob);
            resolve(file);
          } catch (e) {
            reject(e instanceof Error ? e : new Error(String(e)));
          }
        };
        recorder.stop();
      }),

    cancel: () => {
      try {
        recorder.onstop = null;
        recorder.stop();
      } finally {
        cleanup();
      }
    },
  };
}

/** 解码任意录音格式 → OfflineAudioContext 重采样 16kHz 单声道 → 16-bit PCM WAV File */
async function toWav16k(blob: Blob): Promise<File> {
  const decodeCtx = new AudioContext();
  let decoded: AudioBuffer;
  try {
    decoded = await decodeCtx.decodeAudioData(await blob.arrayBuffer());
  } finally {
    void decodeCtx.close();
  }
  const frames = Math.max(1, Math.ceil(decoded.duration * TARGET_SAMPLE_RATE));
  const offline = new OfflineAudioContext(1, frames, TARGET_SAMPLE_RATE);
  const src = offline.createBufferSource();
  src.buffer = decoded;
  src.connect(offline.destination);
  src.start();
  const rendered = await offline.startRendering();

  const pcm = new Int16Array(frames);
  const data = rendered.getChannelData(0);
  for (let i = 0; i < frames; i++) {
    const s = Math.max(-1, Math.min(1, data[i] ?? 0));
    pcm[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  const wav = encodeWav(pcm, TARGET_SAMPLE_RATE);
  return new File([wav], `录音-${new Date().toISOString().slice(11, 19).replace(/:/g, "")}.wav`, { type: "audio/wav" });
}

function encodeWav(pcm: Int16Array, sampleRate: number): Blob {
  const buf = new ArrayBuffer(44 + pcm.length * 2);
  const v = new DataView(buf);
  const writeStr = (offset: number, s: string) => {
    for (let i = 0; i < s.length; i++) v.setUint8(offset + i, s.charCodeAt(i));
  };
  writeStr(0, "RIFF");
  v.setUint32(4, 36 + pcm.length * 2, true);
  writeStr(8, "WAVE");
  writeStr(12, "fmt ");
  v.setUint32(16, 16, true); // fmt 块长度
  v.setUint16(20, 1, true); // PCM
  v.setUint16(22, 1, true); // 单声道
  v.setUint32(24, sampleRate, true);
  v.setUint32(28, sampleRate * 2, true); // 字节率
  v.setUint16(32, 2, true); // 块对齐
  v.setUint16(34, 16, true); // 位深
  writeStr(36, "data");
  v.setUint32(40, pcm.length * 2, true);
  new Int16Array(buf, 44).set(pcm);
  return new Blob([buf], { type: "audio/wav" });
}
