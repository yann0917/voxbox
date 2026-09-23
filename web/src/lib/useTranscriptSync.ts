import { useEffect, useState } from "react";
import { subscribeTime, usePlayer } from "./player";

export interface SyncSegment {
  start_ms: number;
  end_ms: number;
  text: string;
}

/**
 * 音频-文稿同步：跟随全局播放通道定位当前句，点击句子跳转播放。
 * 约定（与全局播放器的两个行为对齐）：
 * - 先 play 再 seek：播放引擎未初始化时 seek 是 no-op（player.ts）；
 * - 仅当前播放轨是 playSrc 时才标记当前句，切走后高亮自动复位。
 */
export function useTranscriptSync(segments: SyncSegment[], playSrc: string | null) {
  const [activeIdx, setActiveIdx] = useState(-1);

  useEffect(() => {
    if (!playSrc || segments.length === 0) return;
    const unsub = subscribeTime((t) => {
      if (usePlayer.getState().track?.src !== playSrc) {
        setActiveIdx((prev) => (prev === -1 ? prev : -1));
        return;
      }
      const ms = t * 1000;
      let idx = -1;
      for (let i = 0; i < segments.length; i++) {
        if (ms >= segments[i].start_ms && ms < segments[i].end_ms) {
          idx = i;
          break;
        }
      }
      setActiveIdx((prev) => (prev === idx ? prev : idx));
    });
    return () => {
      unsub();
    };
  }, [segments, playSrc]);

  const seekTo = (ms: number, track?: { title: string; sub?: string }) => {
    if (!playSrc) return;
    const st = usePlayer.getState();
    if (st.track?.src !== playSrc) {
      st.play({ id: playSrc, src: playSrc, title: track?.title ?? "音频", sub: track?.sub }, true);
    }
    st.seek(ms / 1000);
  };

  return { activeIdx, seekTo };
}
