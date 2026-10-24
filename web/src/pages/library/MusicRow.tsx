import { Download, MicVocal, Pause, Play, Scissors } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { usePlayer } from "../../lib/player";
import { fmtDuration, playMusicTracks, sourceName, submitMusicDownload, type MusicTrack } from "../../lib/music";
import { useToast } from "../../ui";

/** 音乐结果行：试听（全局播放器单实例）/ 下载入库 / 伴奏人声 / 切高潮。
 *  搜索、榜单、歌单、在听四个视图共用；串链动作由页面级场景执行器承接。 */
export function MusicRow({
  t,
  tracks,
  index,
  onStems,
  onClip,
}: {
  t: MusicTrack;
  tracks: MusicTrack[];
  index: number;
  onStems: (t: MusicTrack) => void;
  onClip: (t: MusicTrack) => void;
}) {
  const { toast } = useToast();
  const qc = useQueryClient();
  const track = usePlayer((s) => s.track);
  const playing = usePlayer((s) => s.playing);
  const key = `${t.source}-${t.rid}`;
  const isCurrent = track?.id === key;
  const isPlaying = isCurrent && playing;

  const submitDownload = useMutation({
    mutationFn: () => submitMusicDownload(t),
    onSuccess: () => {
      toast({ tone: "ok", title: "已加入下载", description: "进度见下方最近素材或任务页" });
      void qc.invalidateQueries({ queryKey: ["recent-materials"] });
    },
    onError: (e) => toast({ tone: "error", title: "提交失败", description: e instanceof Error ? e.message : String(e) }),
  });

  return (
    <div className="flex items-center gap-3 px-4 py-2.5">
      <button
        type="button"
        onClick={() => {
          if (isCurrent) usePlayer.getState().toggle();
          else playMusicTracks(tracks, index);
        }}
        title={isPlaying ? "暂停" : "播放"}
        className={`flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-full border transition-colors duration-150 ${
          isCurrent ? "border-accent/50 text-accent" : "border-line text-fg-2 hover:border-accent hover:text-accent"
        }`}
      >
        {isPlaying ? <Pause size={14} /> : <Play size={14} />}
      </button>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm">
          {t.name} <span className="text-fg-2">- {t.artist}</span>
        </p>
        <p className="truncate text-xs text-muted">
          <span className="rounded-full border border-line px-1.5 py-px text-[10px]">{sourceName(t.source)}</span>
          {" "}
          {t.album || "未知专辑"} · {fmtDuration(t.duration)}
        </p>
      </div>
      <button
        type="button"
        onClick={() => submitDownload.mutate()}
        disabled={submitDownload.isPending}
        title="下载入库（standard 档，写标签封面）"
        className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Download size={13} strokeWidth={1.75} />
        下载入库
      </button>
      <button
        type="button"
        onClick={() => onStems(t)}
        title="下载后自动送人声分离"
        className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
      >
        <MicVocal size={13} strokeWidth={1.75} />
        伴奏人声
      </button>
      <button
        type="button"
        onClick={() => onClip(t)}
        title="下载后直达「切高潮」"
        className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
      >
        <Scissors size={13} strokeWidth={1.75} />
        切高潮
      </button>
    </div>
  );
}
