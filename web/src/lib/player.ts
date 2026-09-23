import { create } from "zustand";

export interface Track {
  id: string;
  src: string;
  title: string;
  /** 副标题：如「人声轨」「第 3 轮」 */
  sub?: string;
}

interface PlayerState {
  track: Track | null;
  playing: boolean;
  loading: boolean;
  /** 当前 src 加载/播放失败（直链过期、需要特殊请求头等） */
  error: boolean;
  duration: number;
  play: (t: Track, autoplay?: boolean) => void;
  toggle: () => void;
  seek: (seconds: number) => void;
  stop: () => void;
}

/* ---------- 时间通道：用 rAF 驱动，不走 React 状态，避免 60fps 重渲染 ---------- */

let time = 0;
const timeSubs = new Set<(t: number) => void>();
let rafId = 0;

export const getTime = () => time;
export function subscribeTime(cb: (t: number) => void) {
  timeSubs.add(cb);
  return () => timeSubs.delete(cb);
}

function tick() {
  if (audio && !audio.paused) {
    time = audio.currentTime;
    timeSubs.forEach((cb) => cb(time));
  }
  rafId = requestAnimationFrame(tick);
}

/* ---------- 播放引擎：全局单实例，保证同一时刻只播一路音频 ---------- */

let audio: HTMLAudioElement | null = null;

function ensureAudio(): HTMLAudioElement {
  if (audio) return audio;
  const el = new Audio();
  el.preload = "metadata";
  el.addEventListener("loadedmetadata", () => {
    if (Number.isFinite(el.duration)) usePlayer.setState({ duration: el.duration });
  });
  el.addEventListener("ended", () => usePlayer.setState({ playing: false }));
  el.addEventListener("pause", () => usePlayer.setState({ playing: false }));
  el.addEventListener("play", () => usePlayer.setState({ playing: true }));
  el.addEventListener("error", () =>
    usePlayer.setState({ loading: false, playing: false, error: Boolean(usePlayer.getState().track) }),
  );
  audio = el;
  cancelAnimationFrame(rafId);
  rafId = requestAnimationFrame(tick);
  return el;
}

export const usePlayer = create<PlayerState>((set, get) => ({
  track: null,
  playing: false,
  loading: false,
  error: false,
  duration: 0,

  play: (t, autoplay = true) => {
    const el = ensureAudio();
    const same = get().track?.src === t.src;
    if (same) {
      if (autoplay && el.paused) void el.play();
      set({ playing: !el.paused });
      return;
    }
    el.src = t.src;
    time = 0;
    timeSubs.forEach((cb) => cb(0));
    set({ track: t, duration: 0, loading: true, error: false });
    el.load();
    if (autoplay) {
      void el
        .play()
        .then(() => set({ loading: false, playing: true }))
        .catch(() => set({ loading: false, playing: false }));
    }
  },

  toggle: () => {
    const el = ensureAudio();
    if (!get().track) return;
    if (el.paused) void el.play();
    else el.pause();
  },

  seek: (seconds) => {
    if (!audio) return;
    audio.currentTime = Math.max(0, seconds);
    time = audio.currentTime;
    timeSubs.forEach((cb) => cb(time));
  },

  stop: () => {
    if (audio) {
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    }
    time = 0;
    set({ track: null, playing: false, loading: false, error: false, duration: 0 });
  },
}));

/* ---------- 波形峰值：解码一次并缓存（同一产物反复播放不重复解码） ---------- */

const peaksCache = new Map<string, number[]>();
const PEAK_BUCKETS = 168;

export async function loadPeaks(src: string): Promise<number[]> {
  const cached = peaksCache.get(src);
  if (cached) return cached;

  const resp = await fetch(src);
  const buf = await resp.arrayBuffer();
  const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
  const ctx = new Ctx();
  try {
    const decoded = await ctx.decodeAudioData(buf);
    const data = decoded.getChannelData(0);
    const bucket = Math.max(1, Math.floor(data.length / PEAK_BUCKETS));
    const peaks: number[] = [];
    for (let i = 0; i < PEAK_BUCKETS; i++) {
      let max = 0;
      const start = i * bucket;
      const end = Math.min(data.length, start + bucket);
      for (let j = start; j < end; j++) {
        const v = Math.abs(data[j]);
        if (v > max) max = v;
      }
      peaks.push(max);
    }
    // 归一化，避免弱信号波形塌成一条线
    const top = Math.max(...peaks, 0.0001);
    const norm = peaks.map((p) => Math.max(0.06, p / top));
    peaksCache.set(src, norm);
    return norm;
  } finally {
    void ctx.close();
  }
}

/** mm:ss.d —— 音频工具的标准时间码 */
export function formatTime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) sec = 0;
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  const d = Math.floor((sec * 10) % 10);
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}.${d}`;
}
