import { useEffect, useRef, useState } from "react";
import { ListMusic, Music, Pause, Play, SkipBack, SkipForward, Trash2, X } from "lucide-react";
import { subscribeTime, usePlayer, setPlayerHooks, type Track } from "../lib/player";
import { fetchJSON } from "../lib/api";
import { fmtDuration, listPlayHistory, deletePlayHistory, playMusicTracks, type MusicTrack, type PlayHistoryItem } from "../lib/music";
import { IconButton } from "../ui";

// 副作用接线：曲目真正出声时上报收听历史（best-effort，失败不打扰播放）。
// 本组件由 Layout 常驻挂载，模块加载即完成注册。
setPlayerHooks({
  onTrackStarted: (t: Track) => {
    if (!t.music || !t.id.includes("-")) return;
    const [source, rid] = t.id.split("-");
    void fetchJSON("/api/music/history", {
      method: "POST",
      body: JSON.stringify({ source, rid, name: t.title, artist: t.artist ?? "", album: t.album ?? "", pic: t.pic ?? "", duration: t.durationMs ?? 0 }),
    }).catch(() => {});
  },
});

const fmt = (sec: number) => {
  if (!Number.isFinite(sec) || sec < 0) sec = 0;
  return `${Math.floor(sec / 60)}:${String(Math.floor(sec % 60)).padStart(2, "0")}`;
};

/** 音乐悬浮播放条：试听走全局单实例播放器（与产物音频共用通道，天然不叠加）。
 *  控制：上一曲 / 播停 / 下一曲 + 队列面板（连播队列与收听历史）+ 关闭。
 *  队列随刷新恢复（localStorage），没在播但队列非空时也渲染，点播放接续。 */
export default function MusicPlayerBar() {
  const track = usePlayer((s) => s.track);
  const playing = usePlayer((s) => s.playing);
  const duration = usePlayer((s) => s.duration);
  const queue = usePlayer((s) => s.queue);
  const queueIndex = usePlayer((s) => s.queueIndex);
  const toggle = usePlayer((s) => s.toggle);
  const seek = usePlayer((s) => s.seek);
  const playAt = usePlayer((s) => s.playAt);
  const next = usePlayer((s) => s.next);
  const prev = usePlayer((s) => s.prev);
  const clearQueue = usePlayer((s) => s.clearQueue);

  const [panelOpen, setPanelOpen] = useState(false);
  const [panelTab, setPanelTab] = useState<"queue" | "history">("queue");

  // 时间与进度走 rAF 通道直改 DOM（与全局播放器同模式），不走 React 状态
  const fillRef = useRef<HTMLDivElement>(null);
  const timeRef = useRef<HTMLSpanElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const durOf = track ? duration : 0;

  useEffect(() => {
    const render = (t: number) => {
      if (fillRef.current && durOf > 0) {
        fillRef.current.style.width = `${Math.min(100, (t / durOf) * 100)}%`;
      }
      if (timeRef.current) timeRef.current.textContent = `${fmt(t)} / ${fmt(durOf)}`;
      barRef.current?.setAttribute("aria-valuenow", String(Math.round(t)));
    };
    const unsubscribe = subscribeTime(render);
    return () => {
      unsubscribe();
    };
  }, [durOf]);

  if (!track && queue.length === 0) return null;

  // 展示优先当前曲；刷新恢复态（有队列未加载）显示队首待播
  const shown = track ?? queue[Math.max(0, queueIndex)];
  const inQueue = !!track && queue.some((x) => x.id === track.id);
  const seekTo = (clientX: number) => {
    const rect = barRef.current?.getBoundingClientRect();
    if (!rect || durOf <= 0) return;
    seek(Math.min(1, Math.max(0, (clientX - rect.left) / rect.width)) * durOf);
  };

  return (
    <aside className="rise fixed inset-x-3 bottom-3 z-30 sm:inset-x-auto sm:right-5 sm:bottom-5 sm:w-[420px]" aria-label="音乐播放条">
      {panelOpen && (
        <div className="mb-2 max-h-[380px] overflow-y-auto rounded-[var(--radius-md)] border border-line bg-panel/95 p-3 shadow-[var(--shadow-3)] backdrop-blur">
          <div className="mb-2 flex items-center gap-1 text-xs">
            {(
              [
                { v: "queue", label: `队列 ${queue.length}` },
                { v: "history", label: "在听" },
              ] as const
            ).map((o) => (
              <button
                key={o.v}
                type="button"
                onClick={() => setPanelTab(o.v)}
                className={`cursor-pointer rounded-full px-2.5 py-1 transition-colors duration-150 ${
                  panelTab === o.v ? "bg-accent/10 text-accent" : "text-muted hover:text-fg-2"
                }`}
              >
                {o.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => setPanelOpen(false)}
              className="ml-auto cursor-pointer text-muted hover:text-fg-2"
            >
              <X size={14} />
            </button>
          </div>
          {panelTab === "queue" ? (
            <QueuePanel queue={queue} index={queueIndex} onPick={playAt} />
          ) : (
            <HistoryPanel onPlay={playMusicTracks} />
          )}
        </div>
      )}
      <div className="flex items-center gap-3 rounded-[var(--radius-md)] border border-line bg-panel/95 p-3 shadow-[var(--shadow-3)] backdrop-blur">
        <div className="flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-full border border-line bg-raise-2 text-muted">
          {shown?.pic ? <img src={shown.pic} alt="" className="size-full object-cover" /> : <Music size={18} strokeWidth={1.5} />}
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium text-fg">{shown?.title ?? "—"}</p>
          <p className="truncate text-[11px] text-muted">{shown?.sub ?? ""}</p>
          <div
            ref={barRef}
            role="slider"
            aria-label="播放进度"
            aria-valuemin={0}
            aria-valuemax={Math.round(durOf)}
            tabIndex={0}
            onClick={(e) => seekTo(e.clientX)}
            className="mt-1.5 h-1.5 cursor-pointer rounded-full bg-raise-2"
          >
            <div ref={fillRef} className="h-full rounded-full bg-accent/70" style={{ width: "0%" }} />
          </div>
          <span ref={timeRef} className="mt-0.5 block text-[10px] tabular-nums text-muted">
            0:00 / {fmt(durOf) || fmt((shown?.durationMs ?? 0) / 1000)}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <IconButton label="上一曲" onClick={prev} disabled={!inQueue || queueIndex <= 0}>
            <SkipBack size={15} strokeWidth={1.75} />
          </IconButton>
          <IconButton label={playing ? "暂停" : "播放"} onClick={toggle}>
            {playing ? <Pause size={17} strokeWidth={1.75} /> : <Play size={17} strokeWidth={1.75} />}
          </IconButton>
          <IconButton label="下一曲" onClick={next} disabled={!inQueue || queueIndex >= queue.length - 1}>
            <SkipForward size={15} strokeWidth={1.75} />
          </IconButton>
          <IconButton label="队列与在听" onClick={() => setPanelOpen((v) => !v)}>
            <ListMusic size={15} strokeWidth={1.75} />
          </IconButton>
          <IconButton label="关闭并清空队列" onClick={clearQueue}>
            <X size={15} strokeWidth={1.75} />
          </IconButton>
        </div>
      </div>
    </aside>
  );
}

function QueuePanel({ queue, index, onPick }: { queue: Track[]; index: number; onPick: (i: number) => void }) {
  if (queue.length === 0) return <p className="py-6 text-center text-xs text-muted">队列是空的</p>;
  return (
    <ul className="flex flex-col">
      {queue.map((t, i) => (
        <li key={t.id}>
          <button
            type="button"
            onClick={() => onPick(i)}
            className={`flex w-full cursor-pointer items-center gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 text-left text-xs transition-colors duration-150 hover:bg-raise-2 ${
              i === index ? "text-accent" : "text-fg-2"
            }`}
          >
            <span className="w-4 shrink-0 text-center tabular-nums text-muted">{i + 1}</span>
            <span className="min-w-0 flex-1 truncate">{t.title}</span>
            <span className="max-w-24 shrink-0 truncate text-muted">{t.sub}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function HistoryPanel({ onPlay }: { onPlay: (tracks: MusicTrack[], i: number) => void }) {
  const [items, setItems] = useState<PlayHistoryItem[] | null>(null);

  useEffect(() => {
    let alive = true;
    listPlayHistory(1, 50)
      .then((d) => {
        if (alive) setItems(d.items);
      })
      .catch(() => {
        if (alive) setItems([]);
      });
    return () => {
      alive = false;
    };
  }, []);

  if (items === null) return <p className="py-6 text-center text-xs text-muted">加载中…</p>;
  if (items.length === 0) return <p className="py-6 text-center text-xs text-muted">还没有收听记录</p>;

  const toTracks = () =>
    items.map((h) => ({ source: h.source, rid: h.rid, name: h.name, artist: h.artist, album: h.album, duration: h.duration, pic: h.pic }));

  return (
    <ul className="flex flex-col">
      {items.map((h) => (
        <li key={`${h.source}-${h.rid}`} className="group flex items-center gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 text-xs hover:bg-raise-2">
          <button
            type="button"
            title="播放"
            onClick={() => {
              const tracks = toTracks();
              onPlay(tracks, items.findIndex((x) => x.source === h.source && x.rid === h.rid));
            }}
            className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
          >
            <Play size={11} className="shrink-0 text-muted" />
            <span className="min-w-0 flex-1 truncate text-fg-2">{h.name}</span>
            <span className="max-w-20 shrink-0 truncate text-muted">{h.artist}</span>
          </button>
          <span className="shrink-0 tabular-nums text-muted" title="累计收听">
            {h.play_count}×
          </span>
          <span className="hidden shrink-0 text-[10px] text-muted sm:inline">{fmtDuration(h.duration)}</span>
          <button
            type="button"
            title="删除这条记录"
            onClick={() => {
              void deletePlayHistory(h.source, h.rid).then(() => setItems((prev) => prev?.filter((x) => x.source !== h.source || x.rid !== h.rid) ?? prev));
            }}
            className="cursor-pointer text-muted opacity-0 transition-opacity duration-150 hover:text-danger group-hover:opacity-100"
          >
            <Trash2 size={12} />
          </button>
        </li>
      ))}
    </ul>
  );
}
