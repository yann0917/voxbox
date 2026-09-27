/** voxbox 品牌标记：环形声纹——16 根圆头放射声纹，几何与 tools/icongen 完全同源
 *  （内径 215 / 步宽 44 / 振幅表 amps，1024 设计基准）。stroke 用 currentColor，
 *  随所在容器文字色走（如 accent 底上的 text-accent-ink），深浅主题免配置。 */
const AMPS = [420, 360, 300, 340, 400, 440, 370, 310, 310, 370, 440, 400, 340, 300, 360, 420];

export default function BrandMark({
  size = 16,
  strokeWidth = 44,
  className = "",
}: {
  size?: number;
  strokeWidth?: number;
  className?: string;
}) {
  const c = 512;
  const lines = AMPS.map((amp, i) => {
    const t = (i * 2 * Math.PI) / AMPS.length;
    return {
      x1: c + 215 * Math.sin(t),
      y1: c - 215 * Math.cos(t),
      x2: c + amp * Math.sin(t),
      y2: c - amp * Math.cos(t),
    };
  });
  return (
    <svg width={size} height={size} viewBox="0 0 1024 1024" fill="none" className={className} aria-hidden="true">
      {lines.map((l, i) => (
        <line key={i} {...l} stroke="currentColor" strokeWidth={strokeWidth} strokeLinecap="round" />
      ))}
    </svg>
  );
}
