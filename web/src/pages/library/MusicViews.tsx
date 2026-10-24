import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ListMusic, Loader2, Music, Play, Search, X } from "lucide-react";
import {
  listPlayHistory,
  listPlaylists,
  mvHot,
  mvPlay,
  mvSearch,
  mvStreamURL,
  playlistDetail,
  playlistLines,
  playMusicTracks,
  rankBoards,
  rankSongs,
  searchMusic,
  searchMusicAll,
  sourceName,
  submitMVDownload,
  type MusicTrack,
  type MVItem,
  type MVQuality,
  type Playlist,
} from "../../lib/music";
import { Button, Card, EmptyState, useToast } from "../../ui";
import { listMusicSources } from "../../lib/music";
import { MusicRow } from "./MusicRow";

export interface MusicViewProps {
  onStems: (t: MusicTrack) => void;
  onClip: (t: MusicTrack) => void;
}

// ---------------------------------------------------------------- 聚合搜索

export function MusicSearchView({ onStems, onClip }: MusicViewProps) {
  const [keyword, setKeyword] = useState("");
  const [sourcePick, setSourcePick] = useState("");
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<MusicTrack[]>([]);
  const [srcErrors, setSrcErrors] = useState<Record<string, string>>({});
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState("");
  const pageReqRef = useRef(1);

  const { data: sourcesData } = useQuery({
    queryKey: ["music-sources"],
    queryFn: listMusicSources,
  });
  const sources = (sourcesData?.items ?? []).filter((l) => l.enabled);
  // 默认「全部」= 聚合并发搜索；选单源走直连
  const source = sourcePick || "__all__";

  const doSearch = useMutation({
    mutationFn: async (p: number) => {
      pageReqRef.current = p;
      setSearching(true);
      setSearchError("");
      try {
        if (source === "__all__") {
          const d = await searchMusicAll(keyword.trim(), p);
          return { items: d.items, errors: d.errors };
        }
        const d = await searchMusic(source, keyword.trim(), p);
        return { items: d.items, errors: {} as Record<string, string> };
      } finally {
        setSearching(false);
      }
    },
    onSuccess: (d) => {
      setItems(d.items);
      setSrcErrors(d.errors);
      setPage(pageReqRef.current);
    },
    onError: (e) => {
      setItems([]);
      setSrcErrors({});
      setSearchError(e instanceof Error ? e.message : String(e));
    },
  });

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap items-center gap-2 p-4">
          <div className="flex min-w-0 flex-1 items-center gap-2 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3">
            <Search size={15} className="shrink-0 text-muted" />
            <input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && keyword.trim()) doSearch.mutate(1);
              }}
              placeholder="搜歌名、歌手…（默认全音源并发）"
              className="min-w-0 flex-1 bg-transparent py-2.5 text-sm outline-none placeholder:text-muted"
            />
          </div>
          <Button variant="primary" disabled={!keyword.trim()} loading={searching} onClick={() => doSearch.mutate(1)} icon={<Search size={14} />}>
            搜索
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-1.5 px-4 pb-4">
          <button
            type="button"
            onClick={() => setSourcePick("")}
            className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
              source === "__all__" ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
            }`}
          >
            全部
          </button>
          {sources.map((l) => (
            <button
              key={l.id}
              type="button"
              onClick={() => setSourcePick(l.id)}
              className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                source === l.id ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
              }`}
            >
              {l.name || l.id}
            </button>
          ))}
        </div>
      </Card>

      {searchError && <p className="text-xs text-danger">{searchError}</p>}
      {Object.keys(srcErrors).length > 0 && (
        <p className="text-xs text-muted">
          {Object.entries(srcErrors)
            .map(([id, msg]) => `${sourceName(id)}：${msg}`)
            .join("；")}
        </p>
      )}
      {items.length > 0 && (
        <TrackListCard
          items={items}
          page={page}
          onStems={onStems}
          onClip={onClip}
          onPrev={() => doSearch.mutate(page - 1)}
          onNext={() => doSearch.mutate(page + 1)}
          hasPrev={page > 1}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- 列表骨架（分页行容器复用）

function TrackListCard({
  items,
  page,
  onStems,
  onClip,
  onPrev,
  onNext,
  hasPrev,
  hasNext = true,
  footer,
}: MusicViewProps & {
  items: MusicTrack[];
  page: number;
  onPrev: () => void;
  onNext: () => void;
  hasPrev?: boolean;
  hasNext?: boolean;
  footer?: React.ReactNode;
}) {
  return (
    <Card>
      <div className="flex flex-col divide-y divide-line">
        {items.map((t, i) => (
          <MusicRow key={`${t.source}-${t.rid}-${i}`} t={t} tracks={items} index={i} onStems={onStems} onClip={onClip} />
        ))}
      </div>
      <div className="flex items-center justify-end gap-3 px-4 py-3 text-xs text-fg-2">
        {footer}
        <button
          type="button"
          disabled={!hasPrev}
          onClick={onPrev}
          className="cursor-pointer disabled:cursor-not-allowed disabled:opacity-40 hover:text-accent"
        >
          上一页
        </button>
        <span className="text-muted tabular-nums">第 {page} 页</span>
        <button
          type="button"
          disabled={!hasNext || items.length === 0}
          onClick={onNext}
          className="cursor-pointer disabled:cursor-not-allowed disabled:opacity-40 hover:text-accent"
        >
          下一页
        </button>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------- 排行榜

export function MusicRanksView({ onStems, onClip }: MusicViewProps) {
  const [source, setSource] = useState("kw");
  const [boardPick, setBoardPick] = useState("");
  const [page, setPage] = useState(1);
  const [songs, setSongs] = useState<MusicTrack[]>([]);
  const [boardName, setBoardName] = useState("");

  const { data: boardsData } = useQuery({
    queryKey: ["rank-boards", source],
    queryFn: () => rankBoards(source),
  });
  const boards = boardsData?.items ?? [];

  const loadSongs = useMutation({
    mutationFn: async (p: number) => {
      const d = await rankSongs(source, boardPick, p);
      return d;
    },
    onSuccess: (d, p) => {
      setSongs((d.musicList ?? []).map((s) => ({ ...s, source })));
      setBoardName(d.name || boardName);
      setPage(p);
    },
  });

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap gap-1.5 p-4">
          {(boardsData?.sources ?? ["kw", "wy", "tx", "kg"]).map((id) => (
            <button
              key={id}
              type="button"
              onClick={() => {
                setSource(id);
                setBoardPick("");
                setSongs([]);
              }}
              className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                source === id ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
              }`}
            >
              {sourceName(id)}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap gap-1.5 px-4 pb-4">
          {boards.map((b) => (
            <button
              key={b.id}
              type="button"
              onClick={() => {
                setBoardPick(b.rankid || b.id);
                loadSongs.mutate(1);
              }}
              className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                boardPick === (b.rankid || b.id) ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
              }`}
            >
              {b.name}
            </button>
          ))}
        </div>
      </Card>
      {loadSongs.isPending && (
        <p className="flex items-center gap-2 text-xs text-muted">
          <Loader2 size={13} className="animate-spin" /> 拉取榜单…
        </p>
      )}
      {songs.length > 0 && (
        <TrackListCard
          items={songs}
          page={page}
          onStems={onStems}
          onClip={onClip}
          onPrev={() => loadSongs.mutate(page - 1)}
          onNext={() => loadSongs.mutate(page + 1)}
          hasPrev={page > 1}
          footer={<span className="mr-auto text-muted">{boardName}</span>}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- 歌单广场

export function MusicPlaylistsView({ onStems, onClip }: MusicViewProps) {
  const [source, setSource] = useState("kw");
  const [tag, setTag] = useState("");
  const [page, setPage] = useState(1);
  const [openId, setOpenId] = useState("");
  const qc = useQueryClient();

  const { data: linesData } = useQuery({ queryKey: ["playlist-lines"], queryFn: playlistLines });
  const line = (linesData?.items ?? []).find((l) => l.source === source);
  const categories = line?.categories ?? [];

  const lists = useQuery({
    queryKey: ["playlists", source, tag, page],
    queryFn: () => listPlaylists(source, tag, page),
  });
  const detail = useQuery({
    queryKey: ["playlist-detail", source, openId],
    queryFn: () => playlistDetail(source, openId),
    enabled: openId !== "",
  });

  if (openId && detail.data) {
    const d = detail.data;
    return (
      <div className="flex flex-col gap-4">
        <Card>
          <div className="flex items-center gap-3 p-4">
            <button
              type="button"
              onClick={() => setOpenId("")}
              className="flex cursor-pointer items-center gap-1 text-xs text-fg-2 hover:text-accent"
            >
              <ChevronDown size={13} /> 返回广场
            </button>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{d.name}</p>
              <p className="text-xs text-muted">
                {sourceName(d.source)} · {d.total} 首{d.user_name ? ` · ${d.user_name}` : ""}
              </p>
            </div>
            <Button variant="primary" onClick={() => playMusicTracks(d.tracks, 0)} icon={<Play size={14} />} title="整张歌单加入队列连播">
              连播全单
            </Button>
          </div>
        </Card>
        {d.tracks.length > 0 ? (
          <Card>
            <div className="flex flex-col divide-y divide-line">
              {d.tracks.map((t, i) => (
                <MusicRow key={`${t.source}-${t.rid}-${i}`} t={t} tracks={d.tracks} index={i} onStems={onStems} onClip={onClip} />
              ))}
            </div>
          </Card>
        ) : (
          <Card>
            <EmptyState icon={<Music size={20} strokeWidth={1.5} />} title="没有拉到曲目" description="该歌单详情接口上游可能受限，换个歌单试试" />
          </Card>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap gap-1.5 p-4">
          {(linesData?.items ?? []).map((l) => (
            <button
              key={l.source}
              type="button"
              onClick={() => {
                setSource(l.source);
                setTag("");
                setPage(1);
              }}
              className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                source === l.source ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
              }`}
            >
              {l.title || l.alias || sourceName(l.source)}
            </button>
          ))}
        </div>
        {categories.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-4 pb-4">
            <button
              type="button"
              onClick={() => {
                setTag("");
                setPage(1);
              }}
              className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                tag === "" ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
              }`}
            >
              推荐
            </button>
            {categories.map((cat) => (
              <button
                key={cat.id}
                type="button"
                onClick={() => {
                  setTag(cat.id);
                  setPage(1);
                }}
                className={`cursor-pointer rounded-full border px-3 py-1 text-xs transition-colors duration-150 ${
                  tag === cat.id ? "border-accent/50 bg-accent/10 text-accent" : "border-line text-fg-2 hover:border-line-strong"
                }`}
              >
                {cat.name}
              </button>
            ))}
          </div>
        )}
      </Card>
      {lists.isLoading ? (
        <p className="flex items-center gap-2 text-xs text-muted">
          <Loader2 size={13} className="animate-spin" /> 加载歌单…
        </p>
      ) : (lists.data?.items.length ?? 0) > 0 ? (
        <PlaylistGrid
          items={lists.data!.items}
          page={page}
          total={lists.data!.total}
          onOpen={(id) => {
            setOpenId(id);
            void qc.invalidateQueries({ queryKey: ["playlist-detail", source, id] });
          }}
          onPrev={() => setPage((p) => Math.max(1, p - 1))}
          onNext={() => setPage((p) => p + 1)}
        />
      ) : (
        <Card>
          <EmptyState icon={<ListMusic size={20} strokeWidth={1.5} />} title="没有歌单" description="该线路暂时拉不到歌单，换条线路或分类试试" />
        </Card>
      )}
    </div>
  );
}

function PlaylistGrid({
  items,
  page,
  total,
  onOpen,
  onPrev,
  onNext,
}: {
  items: Playlist[];
  page: number;
  total: number;
  onOpen: (id: string) => void;
  onPrev: () => void;
  onNext: () => void;
}) {
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
        {items.map((p) => (
          <button
            key={p.id}
            type="button"
            onClick={() => onOpen(p.id)}
            className="card-surface group flex cursor-pointer flex-col gap-2 rounded-[var(--radius-md)] border border-line p-3 text-left transition-colors duration-150 hover:border-line-strong"
          >
            <div className="aspect-square w-full overflow-hidden rounded-[var(--radius-sm)] bg-raise-2">
              {p.img ? <img src={p.img} alt="" loading="lazy" className="size-full object-cover" /> : null}
            </div>
            <p className="line-clamp-2 text-xs text-fg">{p.name}</p>
            <p className="text-[11px] text-muted">{p.total > 0 ? `${p.total} 首` : p.uname || ""}</p>
          </button>
        ))}
      </div>
      <div className="flex items-center justify-end gap-3 text-xs text-fg-2">
        <button type="button" disabled={page <= 1} onClick={onPrev} className="cursor-pointer disabled:cursor-not-allowed disabled:opacity-40 hover:text-accent">
          上一页
        </button>
        <span className="text-muted tabular-nums">
          第 {page} 页{total > 0 ? ` · 共 ${total}` : ""}
        </span>
        <button type="button" disabled={items.length === 0} onClick={onNext} className="cursor-pointer disabled:cursor-not-allowed disabled:opacity-40 hover:text-accent">
          下一页
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- MV

export function MusicMVView({ onStems: _onStems, onClip: _onClip }: MusicViewProps) {
  const [keyword, setKeyword] = useState("");
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<MVItem[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [playing, setPlaying] = useState<MVItem | null>(null);
  const { toast } = useToast();

  const hot = useQuery({ queryKey: ["mv-hot", page], queryFn: () => mvHot(page) });

  const doSearch = useMutation({
    mutationFn: async (p: number) => {
      setSearching(true);
      try {
        return await mvSearch("tx", keyword.trim(), p);
      } finally {
        setSearching(false);
      }
    },
    onSuccess: (d) => {
      setItems(d.items);
      setPage(d.page);
    },
    onError: (e) => toast({ tone: "error", title: "搜索失败", description: e instanceof Error ? e.message : String(e) }),
  });

  const shown = items ?? hot.data?.items ?? [];
  const shownPage = items ? page : (hot.data?.page ?? 1);
  const shownLoading = searching || hot.isLoading;

  const goPage = (p: number) => {
    if (items) doSearch.mutate(p);
    else setPage(p);
  };

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap items-center gap-2 p-4">
          <div className="flex min-w-0 flex-1 items-center gap-2 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3">
            <Search size={15} className="shrink-0 text-muted" />
            <input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && keyword.trim()) doSearch.mutate(1);
              }}
              placeholder="搜 MV（QQ 音乐线路）"
              className="min-w-0 flex-1 bg-transparent py-2.5 text-sm outline-none placeholder:text-muted"
            />
          </div>
          {items && (
            <button
              type="button"
              onClick={() => {
                setItems(null);
                setKeyword("");
              }}
              className="shrink-0 cursor-pointer rounded-[var(--radius-sm)] border border-line px-3 py-2 text-xs text-fg-2 hover:border-accent hover:text-accent"
            >
              回热门
            </button>
          )}
        </div>
      </Card>
      {shownLoading ? (
        <p className="flex items-center gap-2 text-xs text-muted">
          <Loader2 size={13} className="animate-spin" /> 加载 MV…
        </p>
      ) : shown.length > 0 ? (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
            {shown.map((mv) => (
              <MVCard key={mv.id} mv={mv} onPlay={() => setPlaying(mv)} />
            ))}
          </div>
          <div className="flex items-center justify-end gap-3 text-xs text-fg-2">
            <button
              type="button"
              disabled={shownPage <= 1}
              onClick={() => goPage(shownPage - 1)}
              className="cursor-pointer disabled:cursor-not-allowed disabled:opacity-40 hover:text-accent"
            >
              上一页
            </button>
            <span className="text-muted tabular-nums">第 {shownPage} 页</span>
            <button type="button" onClick={() => goPage(shownPage + 1)} className="cursor-pointer hover:text-accent">
              下一页
            </button>
          </div>
        </>
      ) : (
        <Card>
          <EmptyState icon={<Music size={20} strokeWidth={1.5} />} title="没有 MV" description="换个关键词，或回热门看看" />
        </Card>
      )}
      {playing && <MVPlayerModal mv={playing} onClose={() => setPlaying(null)} />}
    </div>
  );
}

function MVCard({ mv, onPlay }: { mv: MVItem; onPlay: () => void }) {
  return (
    <div className="card-surface group flex cursor-pointer flex-col gap-2 rounded-[var(--radius-md)] border border-line p-3 transition-colors duration-150 hover:border-line-strong" onClick={onPlay}>
      <div className="relative aspect-video w-full overflow-hidden rounded-[var(--radius-sm)] bg-raise-2">
        {mv.pic ? <img src={mv.pic} alt="" loading="lazy" className="size-full object-cover" /> : null}
        <span className="absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-150 group-hover:opacity-100">
          <span className="flex size-10 items-center justify-center rounded-full bg-black/60 text-white">
            <Play size={18} />
          </span>
        </span>
      </div>
      <p className="line-clamp-1 text-xs text-fg">{mv.name}</p>
      <p className="line-clamp-1 text-[11px] text-muted">
        {mv.artist}
        {mv.duration ? ` · ${mv.duration}` : ""}
      </p>
    </div>
  );
}

/** MV 播放弹层：<video> 走 /api/music/mv/stream 代理（Range 透传可拖进度），画质可切、可下载入库。 */
function MVPlayerModal({ mv, onClose }: { mv: MVItem; onClose: () => void }) {
  const [quality, setQuality] = useState("");
  const [qualities, setQualities] = useState<MVQuality[]>([]);
  const { toast } = useToast();
  const qc = useQueryClient();

  useQuery({
    queryKey: ["mv-play", mv.source, mv.id],
    queryFn: async () => {
      const d = await mvPlay(mv.source, mv.id);
      setQualities(d.items);
      return d;
    },
  });

  const submitDownload = useMutation({
    mutationFn: () => submitMVDownload(mv, quality),
    onSuccess: () => {
      toast({ tone: "ok", title: "已加入下载", description: "MV 落盘为视频产物，见最近素材或任务页" });
      void qc.invalidateQueries({ queryKey: ["recent-materials"] });
    },
    onError: (e) => toast({ tone: "error", title: "提交失败", description: e instanceof Error ? e.message : String(e) }),
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm" onClick={onClose}>
      <div className="w-full max-w-3xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-2 flex items-center gap-2">
          <p className="min-w-0 flex-1 truncate text-sm text-fg">
            {mv.name} <span className="text-fg-2">- {mv.artist}</span>
          </p>
          <select
            value={quality}
            onChange={(e) => setQuality(e.target.value)}
            className="rounded-[var(--radius-sm)] border border-line bg-panel px-2 py-1 text-xs"
          >
            <option value="">默认画质</option>
            {qualities.map((q) => (
              <option key={q.name} value={q.name}>
                {q.name} {q.size ? `· ${q.size}` : ""}
              </option>
            ))}
          </select>
          <Button variant="primary" loading={submitDownload.isPending} onClick={() => submitDownload.mutate()} icon={<DownloadIcon />}>
            下载
          </Button>
          <button type="button" onClick={onClose} className="cursor-pointer text-white/70 hover:text-white">
            <X size={16} />
          </button>
        </div>
        <video
          key={`${mv.id}-${quality}`}
          src={mvStreamURL(mv.source, mv.id, quality)}
          controls
          autoPlay
          className="max-h-[70vh] w-full rounded-[var(--radius-md)] border border-line bg-black"
        />
      </div>
    </div>
  );
}

function DownloadIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" className="size-3.5" aria-hidden>
      <path d="M12 3v12m0 0 4-4m-4 4-4-4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" strokeLinecap="round" />
    </svg>
  );
}

// ---------------------------------------------------------------- 在听（收听历史）

export function MusicHistoryView({ onStems, onClip }: MusicViewProps) {
  const [items, setItems] = useState<MusicTrack[] | null>(null);

  useQuery({
    queryKey: ["play-history"],
    queryFn: async () => {
      const d = await listPlayHistory(1, 100);
      setItems(
        d.items.map((h) => ({
          source: h.source, rid: h.rid, name: h.name, artist: h.artist,
          album: h.album, duration: h.duration, pic: h.pic,
        })),
      );
      return d;
    },
  });

  if (items === null) {
    return (
      <p className="flex items-center gap-2 text-xs text-muted">
        <Loader2 size={13} className="animate-spin" /> 加载在听历史…
      </p>
    );
  }
  if (items.length === 0) {
    return (
      <Card>
        <EmptyState icon={<Music size={20} strokeWidth={1.5} />} title="还没有收听记录" description="试听过的歌会自动记在这里" />
      </Card>
    );
  }
  return (
    <Card>
      <div className="flex flex-col divide-y divide-line">
        {items.map((t, i) => (
          <MusicRow
            key={`${t.source}-${t.rid}`}
            t={t}
            tracks={items}
            index={i}
            onStems={onStems}
            onClip={onClip}
          />
        ))}
      </div>
    </Card>
  );
}
