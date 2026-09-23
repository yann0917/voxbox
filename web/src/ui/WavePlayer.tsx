import { useCallback, useEffect, useRef, useState } from "react";
import { Pause, Play } from "lucide-react";
import { formatTime, getTime, loadPeaks, subscribeTime, usePlayer} from "../lib/player";
import { Skeleton } from "./Card";

export interface WavePlayerProps {
  src: string;
  title: string;
  sub?: string;
  /** 已知时长（秒）：后端产物带 duration_ms，未播放时也能显示总时长 */
  durationSec?: number;
  className?: string;
}

/** 波形播放器：真实音频峰值 + 已播段着色 + 点击/拖拽定位。 */
export function WavePlayer({ src, title, sub, durationSec, className = "" }: WavePlayerProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const timeRef = useRef<HTMLSpanElement>(null);
  const fillRef = useRef<HTMLDivElement>(null);
  const colorsRef = useRef({ accent: "#22d3ee", idle: "rgba(255,255,255,.18)" });
  const peaksRef = useRef<number[] | null>(null);
  const draggingRef = useRef(false);

  const [peaksReady, setPeaksReady] = useState(false);
  const [failed, setFailed] = useState(false);

  const track = usePlayer((s) => s.track);
  const isCurrent = track?.src === src;
  const playing = usePlayer((s) => (isCurrent ? s.playing : false));
  const liveDuration = usePlayer((s) => (isCurrent ? s.duration : 0));
  // 未播放时用后端已知时长兜底，避免总时长显示为空
  const duration = liveDuration || durationSec || 0;
  const play = usePlayer((s) => s.play);
  const toggle = usePlayer((s) => s.toggle);
  const seek = usePlayer((s) => s.seek);

  /* 峰值加载完成后再触发一次绘制：
     canvas 要等 peaksReady 才挂载，加载回调里同步 draw() 时 canvas 还不存在 */
  useEffect(() => {
    if (peaksReady) draw();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [peaksReady]);

  /* 主题色读取：暗/亮切换时刷新（MutationObserver 监听 html class） */
  useEffect(() => {
    const read = () => {
      const cs = getComputedStyle(document.documentElement);
      colorsRef.current = {
        accent: cs.getPropertyValue("--accent").trim() || "#22d3ee",
        idle: cs.getPropertyValue("--wave-idle").trim() || cs.getPropertyValue("--line-strong").trim() || "rgba(255,255,255,.18)",
      };
      if (peaksRef.current) draw();
    };
    read();
    const ob = new MutationObserver(read);
    ob.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => ob.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /* 峰值加载 */
  useEffect(() => {
    let alive = true;
    setFailed(false);
    setPeaksReady(false);
    peaksRef.current = null;
    loadPeaks(src)
      .then((p) => {
        if (!alive) return;
        peaksRef.current = p;
        setPeaksReady(true);
        draw();
      })
      .catch(() => alive && setFailed(true));
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [src]);

  /* 绘制：几何变化与时间推进都走这里 */
  const draw = useCallback(() => {
    const c = canvasRef.current;
    const peaks = peaksRef.current;
    if (!c || !peaks) return;

    const dpr = window.devicePixelRatio || 1;
    const w = c.clientWidth;
    const h = c.clientHeight;
    if (w === 0 || h === 0) return;
    if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
      c.width = Math.round(w * dpr);
      c.height = Math.round(h * dpr);
    }
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);

    const barW = 2;
    const gap = 1;
    const step = barW + gap;
    const bars = Math.max(1, Math.floor(w / step));
    const dur = isCurrent ? duration : 0;
    const played = dur > 0 ? getTime() / dur : 0;
    const { accent, idle } = colorsRef.current;

    for (let i = 0; i < bars; i++) {
      const peak = peaks[Math.min(peaks.length - 1, Math.floor((i / bars) * peaks.length))];
      const bh = Math.max(2, peak * h * 0.92);
      const x = i * step;
      const y = (h - bh) / 2;
      ctx.fillStyle = (i + 0.5) / bars <= played ? accent : idle;
      ctx.fillRect(x, y, barW, bh);
    }

    // 播放头
    if (isCurrent && dur > 0) {
      const x = played * w;
      ctx.fillStyle = accent;
      ctx.fillRect(Math.min(w - 1, Math.max(0, x)), 0, 1, h);
    }
  }, [duration, isCurrent]);

  /* 时间推进 → 直接改 DOM 与画布，不触发 React 重渲染。
     无波形降级（跨域源拿不到峰值）时同步推进进度条填充。 */
  useEffect(() => {
    const update = (t: number) => {
      if (timeRef.current) timeRef.current.textContent = formatTime(t);
      if (fillRef.current) {
        fillRef.current.style.width = duration > 0 ? `${Math.min(100, (t / duration) * 100)}%` : "0%";
      }
      draw();
    };
    if (isCurrent) update(getTime());
    else {
      if (timeRef.current) timeRef.current.textContent = formatTime(0);
      if (fillRef.current) fillRef.current.style.width = "0%";
    }
    draw();
    const unsub = subscribeTime(update);
    return () => { unsub(); };
  }, [draw, isCurrent]);

  /* 尺寸变化重绘 */
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => draw());
    ro.observe(el);
    return () => ro.disconnect();
  }, [draw]);

  const seekFromEvent = (clientX: number) => {
    const el = wrapRef.current;
    if (!el || !isCurrent) return;
    const rect = el.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    if (duration > 0) seek(ratio * duration);
  };

  const onPointerDown = (e: React.PointerEvent) => {
    draggingRef.current = true;
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    seekFromEvent(e.clientX);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (draggingRef.current) seekFromEvent(e.clientX);
  };
  const onPointerUp = () => {
    draggingRef.current = false;
  };

  return (
    <div className={`flex items-center gap-3 ${className}`}>
      <button
        type="button"
        aria-label={playing ? `暂停 ${title}` : `播放 ${title}`}
        onClick={() => (isCurrent ? toggle() : play({ id: src, src, title, sub }))}
        className="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-full border border-line-strong bg-raise-2 text-fg transition-colors duration-150 hover:border-accent hover:text-accent"
      >
        {playing ? <Pause size={13} strokeWidth={2} fill="currentColor" /> : <Play size={13} strokeWidth={2} fill="currentColor" className="ml-0.5" />}
      </button>

      <div
        ref={wrapRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        role="slider"
        aria-label={`${title} 播放进度`}
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(isCurrent ? getTime() : 0)}
        tabIndex={0}
        onKeyDown={(e) => {
          if (!isCurrent) return;
          if (e.key === "ArrowRight") seek(getTime() + 5);
          if (e.key === "ArrowLeft") seek(Math.max(0, getTime() - 5));
        }}
        className={`h-9 min-w-0 flex-1 ${isCurrent ? "cursor-pointer" : "cursor-default"}`}
        title={isCurrent ? "点击或拖拽定位" : "点击播放"}
      >
        {peaksReady ? (
          <canvas ref={canvasRef} className="h-full w-full" />
        ) : failed ? (
          /* 无波形降级：跨域源无法解码峰值时，退为进度条式，仍可点击/拖拽定位 */
          <div className="flex h-full w-full items-center">
            <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-line-strong">
              <div ref={fillRef} className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: "0%" }} />
            </div>
          </div>
        ) : (
          <Skeleton className="h-full w-full" />
        )}
      </div>

      <span className="shrink-0 font-mono text-[11px] text-muted">
        <span ref={timeRef} className={isCurrent ? "text-fg-2" : ""}>
          00:00.0
        </span>
        <span className="mx-1 opacity-50">/</span>
        {duration > 0 ? formatTime(duration) : "--:--.-"}
      </span>
    </div>
  );
}
