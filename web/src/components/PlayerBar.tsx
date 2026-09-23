import { X } from "lucide-react";
import { usePlayer } from "../lib/player";
import { IconButton, WavePlayer } from "../ui";

/** 全局播放条：跨页面常驻，保证任何页面的音频都在同一条通道上播放。 */
export default function PlayerBar() {
  const track = usePlayer((s) => s.track);
  const stop = usePlayer((s) => s.stop);

  if (!track) return null;

  return (
    <footer className="rise fixed inset-x-0 bottom-0 z-30 border-t border-line bg-panel/95 backdrop-blur">
      <div className="mx-auto flex max-w-[1100px] items-center gap-3 px-4 py-2 md:px-8">
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="micro">正在播放</span>
            <span className="truncate text-xs text-fg">{track.title}</span>
            {track.sub && <span className="truncate text-[11px] text-muted">· {track.sub}</span>}
          </div>
          <WavePlayer src={track.src} title={track.title} sub={track.sub} />
        </div>
        <IconButton label="停止并关闭播放条" onClick={stop}>
          <X size={16} strokeWidth={1.75} />
        </IconButton>
      </div>
    </footer>
  );
}
