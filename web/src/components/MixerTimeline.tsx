import { useCallback, useEffect, useRef, useState } from "react";
import { envValueAt, type EnvPoint } from "../lib/mixer";

interface Props {
  duration: number;
  musicPeaks: number[];
  vocalPeaks: number[];
  env: EnvPoint[];
  onEnvChange: (pts: EnvPoint[]) => void;
  time: number;
  playing: boolean;
  onSeek: (t: number) => void;
  vocalMuted: boolean;
}

/* ---------------- 双轨几何常量（模块级：画布绘制与关键帧手柄定位共用同一坐标系） ----------------
 * wrap 高 h-44=176px（含 1px 边框 → 内高约 174px），双轨各占内高一半：
 * 上轨伴奏 [0, 0.5h)、下轨人声 [0.5h, h]。包络画在人声轨【内】：0dB 在轨顶、-60dB 在轨底、
 * ≤-60（含 -99）钳到道底。骨架稿的 22px 顶部留白会让 0dB 手柄骑进伴奏轨，与「包络在人声轨内」
 * 的裁定冲突，故弃用；用内高占比（88/176=0.5）而非写死像素表达，实测内高变化时仍逐像素对齐。
 * 手柄用 CSS 百分比定位（% 相对 wrap 内盒=padding box，与画布 inset-0 同一只盒子），
 * 画布用同一占比乘实测内高得 px——两处永系同一坐标，且渲染期无需读 ref（oxlint react/refs）。 */
const LANE_TOP = 0.5; // 人声轨顶部 = 内高之半（包络 0dB 基线）
const LANE_H = 0.5; // 包络道高度 = 内高之半（0..-60dB 纵跨全道）

/** 包络 dB → 包络道纵向占比：0dB→LANE_TOP，-60dB→LANE_TOP+LANE_H，≤-60（含 -99）钳底。
 *  手柄 top = 占比×100%（CSS），画布 = 占比×实测内高（px）——同一函数同源。 */
const envFrac = (db: number) => LANE_TOP + (Math.max(-60, Math.min(0, db)) / -60) * LANE_H;
/** 包络 dB → 轨内 y（px，入参 h=wrap 实测内高）。画布绘制用。 */
const envY = (db: number, h: number) => envFrac(db) * h;
/** 时间 → 内盒宽度占比（0..1，钳 [0,1]）：手柄 left = ×100%（CSS），画布/命中 = ×内盒宽（px）。 */
const fracOf = (t: number, duration: number) => (duration > 0 ? Math.max(0, Math.min(1, t / duration)) : 0);

/** 关键帧命中半径（px）：屏幕 x 轴 ±8px 即命中（纵向不限——手柄只有 12px，按 x 抓才稳）。 */
const HIT_R = 8;

/** 双轨时间线：上=伴奏波形，下=人声波形+音量包络。包络交互：
 *  拖点=改时间/值，双击空白=加点，点选后 Delete/退格=删点；同刻双点渲染为竖直跳变段。
 *  包络语义锚点同 lib/mixer：分段线性、首点前=首点值、末点后=末点值（internal/provider/audiotool/envelope.go）。 */
export function MixerTimeline({ duration, musicPeaks, vocalPeaks, env, onEnvChange, time, playing, onSeek, vocalMuted }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const waveRef = useRef<HTMLCanvasElement>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const dragRef = useRef<number | null>(null);
  // 兜底色与 theme.css 暗色默认一致；实际值一律以 computed style 读取为准（MASTER：无画布外硬编码色板）
  const colorsRef = useRef({ accent: "#e2a35a", idle: "rgba(246,236,221,.16)", line: "rgba(255,214,165,.16)" });
  /** 绘制函数引用：主题切换/尺寸变化由观察器直接触发重绘，不经过 React 状态。 */
  const drawRef = useRef<() => void>(() => {});

  /* 主题色读取：暗/亮切换时刷新并重绘（MutationObserver 监听 html class，同 WavePlayer 的模式）。
     只写 colorsRef 不进 React 状态——播放中 80ms tick 的重绘路径上零状态开销。 */
  useEffect(() => {
    const read = () => {
      const cs = getComputedStyle(document.documentElement);
      colorsRef.current = {
        accent: cs.getPropertyValue("--accent").trim() || "#e2a35a",
        idle: cs.getPropertyValue("--wave-idle").trim() || cs.getPropertyValue("--line-strong").trim() || "rgba(246,236,221,.16)",
        line: cs.getPropertyValue("--line-strong").trim() || "rgba(255,214,165,.16)",
      };
      drawRef.current();
    };
    read();
    const ob = new MutationObserver(read);
    ob.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => ob.disconnect();
  }, []);

  /* 尺寸变化重绘：canvas 位图不随 CSS 自动重采样，观察 wrap 尺寸直接触发重绘（同 WavePlayer）。
     手柄是百分比定位，随布局自动跟随，无需经此同步。 */
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => drawRef.current());
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  /* 绘制：波形（上伴奏 idle 色/已播段 accent——同 WavePlayer 的已播着色；下人声 accent 弱化）
     + 包络（人声轨内 accent 折线+淡填充，静音时虚线压暗）+ 轨道分隔发丝线 + 贯穿两轨的播放头。
     父组件 80ms tick 重渲染走这里：纯 canvas 重画、无状态写入，开销可承受。 */
  useEffect(() => {
    const draw = () => {
      const c = waveRef.current;
      if (!c) return;
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

      const { accent, idle, line } = colorsRef.current;
      const vocalTop = LANE_TOP * h;
      const laneH = LANE_H * h;
      const barW = 2;
      const gap = 1;
      const step = barW + gap;
      const bars = Math.max(1, Math.floor(w / step));
      const xAt = (t: number) => fracOf(t, duration) * w;

      // 轨道分隔发丝线
      ctx.fillStyle = line;
      ctx.fillRect(0, vocalTop, w, 1);

      // 上轨：伴奏波形（未播 idle / 已播 accent）
      if (musicPeaks.length > 0) {
        for (let i = 0; i < bars; i++) {
          const peak = musicPeaks[Math.min(musicPeaks.length - 1, Math.floor((i / bars) * musicPeaks.length))];
          const bh = Math.max(2, peak * laneH * 0.92);
          ctx.fillStyle = duration > 0 && (i + 0.5) / bars <= time / duration ? accent : idle;
          ctx.fillRect(i * step, (vocalTop - bh) / 2, barW, bh);
        }
      }

      // 下轨：人声波形（accent 弱化；静音再压暗，静音态在画布上可读）
      if (vocalPeaks.length > 0) {
        ctx.globalAlpha = vocalMuted ? 0.15 : 0.35;
        ctx.fillStyle = accent;
        for (let i = 0; i < bars; i++) {
          const peak = vocalPeaks[Math.min(vocalPeaks.length - 1, Math.floor((i / bars) * vocalPeaks.length))];
          const bh = Math.max(2, peak * laneH * 0.92);
          ctx.fillRect(i * step, vocalTop + (laneH - bh) / 2, barW, bh);
        }
        ctx.globalAlpha = 1;
      }

      // 包络：点列稳定排序后画折线（首点前/末点后按语义平延，同 envValueAt；
      // 同刻相邻双点 x 相同 → lineTo 自然形成竖直跳变段）；折线下方淡填充示意增益区域
      if (duration > 0 && env.length > 0) {
        const pts = [...env].sort((a, b) => a[0] - b[0]); // 稳定排序：同刻双点保持先后=跳变方向
        ctx.beginPath();
        ctx.moveTo(0, envY(pts[0][1], h));
        for (const p of pts) ctx.lineTo(xAt(p[0]), envY(p[1], h));
        ctx.lineTo(w, envY(pts[pts.length - 1][1], h));
        ctx.lineTo(w, vocalTop + laneH);
        ctx.lineTo(0, vocalTop + laneH);
        ctx.closePath();
        ctx.globalAlpha = vocalMuted ? 0.05 : 0.12;
        ctx.fillStyle = accent;
        ctx.fill();

        ctx.beginPath();
        ctx.moveTo(0, envY(pts[0][1], h));
        for (const p of pts) ctx.lineTo(xAt(p[0]), envY(p[1], h));
        ctx.lineTo(w, envY(pts[pts.length - 1][1], h));
        ctx.globalAlpha = vocalMuted ? 0.4 : 1;
        ctx.strokeStyle = accent;
        ctx.lineWidth = 1.5;
        if (vocalMuted) ctx.setLineDash([4, 3]); // 静音：包络虚线弱化呈现
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.globalAlpha = 1;
      }

      // 播放头：贯穿两轨共享。暂停时加淡光晕标记驻留位置，播放中仅细线随 tick 前进
      if (duration > 0) {
        const x = xAt(time);
        if (!playing) {
          ctx.globalAlpha = 0.3;
          ctx.fillStyle = accent;
          ctx.fillRect(x - 1.5, 0, 4, h);
          ctx.globalAlpha = 1;
        }
        ctx.fillStyle = accent;
        ctx.fillRect(x, 0, 1, h);
      }
    };
    drawRef.current = draw;
    draw();
  }, [duration, musicPeaks, vocalPeaks, env, time, playing, vocalMuted]);

  /** 时间 → 屏幕 x（wrap 内盒 px）。与手柄 left（同占比的 CSS %）、命中检测同一坐标系。 */
  const xOf = useCallback((t: number) => {
    const el = wrapRef.current;
    if (!el || duration <= 0) return 0;
    return fracOf(t, duration) * el.clientWidth;
  }, [duration]);

  /** 屏幕 clientX → 时间（钳 [0, duration]）。原点取内盒（扣掉 1px 左边框），与 xOf/手柄逐像素同系。 */
  const tOf = useCallback((clientX: number) => {
    const el = wrapRef.current;
    if (!el || duration <= 0 || el.clientWidth === 0) return 0;
    const rect = el.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (clientX - rect.left - el.clientLeft) / el.clientWidth));
    return ratio * duration;
  }, [duration]);

  /** 屏幕 clientX → 内盒 x（命中检测用，与手柄 left 同坐标系：getBoundingClientRect 原点扣左边框）。 */
  const hitX = (clientX: number) => {
    const el = wrapRef.current;
    if (!el) return -Infinity;
    return clientX - el.getBoundingClientRect().left - el.clientLeft;
  };

  /** 屏幕 clientY → 包络 dB（整数、钳 [-60, 0]）：envFrac 的逆映射——同一占比常量换算，
   *  拖点上界 0dB（骑进伴奏轨即钳平）、下界 -60dB（道底）。spec §6.2「拖点=改时间/改 dB」。 */
  const dbOf = (clientY: number) => {
    const el = wrapRef.current;
    if (!el || el.clientHeight === 0) return 0;
    const rect = el.getBoundingClientRect();
    const frac = (clientY - rect.top - el.clientTop) / el.clientHeight;
    return Math.round(Math.max(-60, Math.min(0, ((frac - LANE_TOP) / LANE_H) * -60)));
  };

  /* 关键帧拖拽：pointerdown 命中（x±8px）→ 选中+开始拖；move 持续回调（时间钳 [0,duration]，
     拖成与相邻点同刻=跳变，允许）；up 归一化提交。
     排序约定：拖拽中不排序（dragRef 按索引须稳定），pointerup 归一化为升序——父态从此恒有序，
     MixerEngine.setEnvelope 与导出侧拿到的都是规范形状（稳定排序保持同刻先后=跳变方向不变）。 */
  const onPointerDownWrap = (e: React.PointerEvent) => {
    if (duration <= 0) return;
    const x = hitX(e.clientX);
    const i = env.findIndex((p) => Math.abs(xOf(p[0]) - x) <= HIT_R);
    if (i >= 0) {
      dragRef.current = i;
      setSelected(i);
      (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    } else {
      onSeek(tOf(e.clientX)); // 空白处按下=定位播放头
    }
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const i = dragRef.current;
    if (i === null) return;
    const t = tOf(e.clientX); // tOf 已钳 [0, duration]
    const db = dbOf(e.clientY); // dbOf 已钳 [-60, 0]：拖点同时改时间与增益（spec §6.2）
    onEnvChange(env.map((p, k) => (k === i ? ([t, db] as EnvPoint) : p))); // 拖拽中持续回调（父组件实时下发 engine.setEnvelope）
  };

  const onPointerUp = () => {
    const i = dragRef.current;
    dragRef.current = null;
    if (i === null || i >= env.length) return;
    const moved = env[i];
    const sorted = [...env].sort((a, b) => a[0] - b[0]);
    if (sorted.some((p, k) => p !== env[k])) {
      onEnvChange(sorted); // 拖拽可能跨点换序：结束时归一化升序（稳定排序，同刻先后不变）
    }
    const ni = sorted.indexOf(moved); // 归一化后同步选中索引，键盘续调不落空
    if (ni >= 0) setSelected(ni);
  };

  const onDoubleClick = (e: React.MouseEvent) => {
    if (duration <= 0) return;
    const x = hitX(e.clientX);
    if (env.some((p) => Math.abs(xOf(p[0]) - x) <= HIT_R)) return; // 命中已有点：交给选中/拖拽，不重复加点
    const t = tOf(e.clientX);
    // 新点取当前曲线值（envValueAt 要求升序输入——父态可能处于拖拽后的暂态未排序），避免加点即跳变
    const g = envValueAt([...env].sort((a, b) => a[0] - b[0]), t);
    const next = [...env, [t, g] as EnvPoint].sort((a, b) => a[0] - b[0]);
    onEnvChange(next);
    setSelected(next.findIndex((p) => p[0] === t && p[1] === g)); // 新点即选中，Delete/方向键立即可用
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (selected === null || selected >= env.length || duration <= 0) return;
    if (e.key === "Delete" || e.key === "Backspace") {
      const target = env[selected];
      const sorted = [...env].sort((a, b) => a[0] - b[0]);
      onEnvChange(sorted.filter((p) => p !== target)); // 按引用删选中点（防同刻同值的孪生点误删）
      setSelected(null);
      e.preventDefault();
    }
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      const d = e.key === "ArrowLeft" ? -0.5 : 0.5;
      const cur = env[selected];
      const moved: EnvPoint = [Math.max(0, Math.min(duration, cur[0] + d)), cur[1]]; // 时间钳 [0, duration]
      const next = env.map((p, k) => (k === selected ? moved : p));
      const sorted = [...next].sort((a, b) => a[0] - b[0]);
      onEnvChange(sorted); // 微调可能跨点换序：同步归一化（稳定排序保持同刻先后）
      setSelected(sorted.indexOf(moved)); // moved 是新元组（引用唯一），归一化后仍指向同一点
      e.preventDefault();
    }
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      const d = e.key === "ArrowUp" ? 2 : -2;
      const cur = env[selected];
      const moved: EnvPoint = [cur[0], Math.max(-60, Math.min(0, cur[1] + d))]; // 增益钳 [-60, 0]
      const next = env.map((p, k) => (k === selected ? moved : p));
      onEnvChange(next); // 值变不动时间：恒有序，无需归一化
      e.preventDefault();
    }
  };

  return (
    // 角色注释：application + 手柄 slider——纯键盘用户经 Tab 聚焦 wrap 或手柄，Delete/方向键编辑包络
    <div
      ref={wrapRef}
      role="application"
      aria-label="混音时间线"
      tabIndex={0}
      onPointerDown={onPointerDownWrap}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={onDoubleClick}
      onKeyDown={onKeyDown}
      className="relative h-44 cursor-crosshair select-none rounded-[var(--radius-sm)] border border-line bg-inset"
      style={{ touchAction: "none" }}
    >
      {/* 波形/包络/播放头画布（装饰性：信息由关键帧手柄与 application 语义承载） */}
      <canvas ref={waveRef} className="absolute inset-0 h-full w-full" aria-hidden="true" />
      {/* 关键帧手柄：绝对定位按钮（键盘可达 + 命中稳定）。left/top 用内盒百分比定位——
          与画布 xAt/envY 同一占比常量（fracOf/envFrac），与命中检测 xOf 同一坐标系；
          渲染期不读 ref，尺寸变化随布局自动跟随。 */}
      {env.map((p, i) => (
        <button
          key={i}
          type="button"
          role="slider"
          aria-label={`包络点 ${i}：${p[0].toFixed(1)} 秒 ${p[1]} dB`}
          aria-valuenow={Math.round(p[1])}
          aria-valuemin={-60}
          aria-valuemax={0}
          onClick={(e) => {
            e.stopPropagation();
            setSelected(i);
          }}
          // Tab 聚焦即选中：方向键/删除立即可用（keydown 自手柄冒泡到 wrap 处理）
          onFocus={() => setSelected(i)}
          className={`absolute size-3 cursor-grab rounded-full border-2 transition-colors duration-150 ${
            selected === i ? "border-fg bg-accent" : "border-inset bg-accent"
          }${vocalMuted ? " opacity-50" : ""}`}
          style={{ left: `${fracOf(p[0], duration) * 100}%`, top: `${envFrac(p[1]) * 100}%` }}
        />
      ))}
    </div>
  );
}
