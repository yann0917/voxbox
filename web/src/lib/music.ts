import { fetchJSON } from "./api";
import { usePlayer, type Track as PlayerTrack } from "./player";

// 音乐板块共享类型与 API：与 qingmusic/service DTO 对齐。

export interface MusicSourceLine {
  id: string;
  name: string;
  enabled: boolean;
  levels: string[];
}

export interface MusicTrack {
  source: string;
  rid: string;
  name: string;
  artist: string;
  album: string;
  duration: number; // 毫秒
  pic?: string;
}

export interface SearchAllResult {
  items: MusicTrack[];
  page: number;
  errors: Record<string, string>;
}

export interface RankBoardInfo {
  source: string;
  id: string;
  name: string;
  rankid: string;
}

export interface RankBoard {
  code: number;
  name: string;
  pic: string;
  total: number;
  updateTime: string;
  musicList: MusicTrack[];
  error?: string;
}

export interface PlaylistLine {
  source: string;
  alias: string;
  title: string;
  categories: { id: string; name: string; hot?: boolean }[];
}

export interface Playlist {
  id: string;
  name: string;
  img?: string;
  pic?: string;
  uname?: string;
  total: number;
  source: string;
}

export interface PlaylistPage {
  source: string;
  tag?: string;
  page: number;
  total: number;
  items: Playlist[];
}

export interface PlaylistDetail {
  source: string;
  id: string;
  name: string;
  img?: string;
  user_name?: string;
  total: number;
  tracks: MusicTrack[];
}

export interface MVItem {
  id: string;
  name: string;
  artist: string;
  pic: string;
  duration: string;
  playcount: number;
  source: string;
  line: string;
}

export interface MVQuality {
  name: string;
  size: string;
  url?: string;
}

export interface PlayHistoryItem {
  source: string;
  rid: string;
  name: string;
  artist: string;
  album: string;
  pic: string;
  duration: number;
  play_count: number;
  played_at: string;
}

export const SOURCE_NAMES: Record<string, string> = {
  kw: "酷我", kg: "酷狗", wy: "网易云", tx: "QQ 音乐", mg: "咪咕", bili: "B 站",
};

export const sourceName = (id: string) => SOURCE_NAMES[id] ?? id;

export const fmtDuration = (ms?: number) => {
  if (!ms || ms <= 0) return "—";
  const total = Math.round(ms / 1000);
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
};

export const musicStreamURL = (t: Pick<MusicTrack, "source" | "rid">) =>
  `/api/music/stream?source=${encodeURIComponent(t.source)}&rid=${encodeURIComponent(t.rid)}&level=`;

/** toPlayerTracks 音乐曲目 → 全局播放器 Track（队列承载）。 */
export const toPlayerTracks = (tracks: MusicTrack[]): PlayerTrack[] =>
  tracks.map((t) => ({
    id: `${t.source}-${t.rid}`,
    src: musicStreamURL(t),
    title: t.name,
    sub: t.artist,
    music: true,
    artist: t.artist,
    album: t.album,
    pic: t.pic,
    durationMs: t.duration,
  }));

/** 试听统一走全局播放器（单实例，与产物音频共用一条通道，天然不叠加）。 */
export const playMusicTracks = (tracks: MusicTrack[], index: number) => {
  if (tracks.length === 0) return;
  usePlayer.getState().playQueue(toPlayerTracks(tracks), index);
};

export const listMusicSources = () =>
  fetchJSON<{ items: MusicSourceLine[]; configured: boolean; sources_url: string }>("/api/music/sources");

export const searchMusic = (source: string, keyword: string, page: number) =>
  fetchJSON<{ items: MusicTrack[]; page: number }>(
    `/api/music/search?source=${encodeURIComponent(source)}&keyword=${encodeURIComponent(keyword)}&page=${page}`,
  );

export const searchMusicAll = (keyword: string, page: number) =>
  fetchJSON<SearchAllResult>(`/api/music/search/all?keyword=${encodeURIComponent(keyword)}&page=${page}`);

export const rankBoards = (source: string) =>
  fetchJSON<{ sources: string[]; items: RankBoardInfo[] }>(`/api/music/ranks/boards?source=${encodeURIComponent(source)}`);

export const rankSongs = (source: string, board: string, page: number) =>
  fetchJSON<RankBoard>(`/api/music/ranks/songs?source=${encodeURIComponent(source)}&board=${encodeURIComponent(board)}&page=${page}`);

export const playlistLines = () => fetchJSON<{ items: PlaylistLine[] }>("/api/music/playlists/lines");

export const listPlaylists = (source: string, tag: string, page: number) =>
  fetchJSON<PlaylistPage>(
    `/api/music/playlists?source=${encodeURIComponent(source)}&tag=${encodeURIComponent(tag)}&page=${page}`,
  );

export const playlistDetail = (source: string, id: string) =>
  fetchJSON<PlaylistDetail>(`/api/music/playlists/detail?source=${encodeURIComponent(source)}&id=${encodeURIComponent(id)}`);

export const mvHot = (page: number, size = 30) =>
  fetchJSON<{ items: MVItem[]; page: number }>(`/api/music/mv/hot?page=${page}&size=${size}`);

export const mvSearch = (source: string, keyword: string, page: number) =>
  fetchJSON<{ items: MVItem[]; page: number }>(
    `/api/music/mv/search?source=${encodeURIComponent(source)}&keyword=${encodeURIComponent(keyword)}&page=${page}`,
  );

export const mvPlay = (source: string, vid: string) =>
  fetchJSON<{ items: MVQuality[] }>(`/api/music/mv/play?source=${encodeURIComponent(source)}&vid=${encodeURIComponent(vid)}`);

export const mvStreamURL = (source: string, vid: string, quality = "") =>
  `/api/music/mv/stream?source=${encodeURIComponent(source)}&vid=${encodeURIComponent(vid)}&quality=${encodeURIComponent(quality)}`;

export const submitMusicDownload = (t: MusicTrack) =>
  fetchJSON<{ task_id: string }>("/api/tasks", {
    method: "POST",
    body: JSON.stringify({
      provider: "music",
      tool: "music_download",
      params: { source: t.source, rid: t.rid, name: t.name, artist: t.artist, duration_ms: t.duration },
    }),
  });

export const submitMVDownload = (mv: MVItem, quality: string) =>
  fetchJSON<{ task_id: string }>("/api/tasks", {
    method: "POST",
    body: JSON.stringify({
      provider: "music",
      tool: "music_mv_download",
      params: { source: mv.source, vid: mv.id, name: mv.name, artist: mv.artist, quality },
    }),
  });

export const listPlayHistory = (page: number, size = 50) =>
  fetchJSON<{ items: PlayHistoryItem[]; total: number; page: number }>(`/api/music/history?page=${page}&size=${size}`);

export const deletePlayHistory = (source: string, rid: string) =>
  fetchJSON<{ ok: boolean }>(`/api/music/history?source=${encodeURIComponent(source)}&rid=${encodeURIComponent(rid)}`, {
    method: "DELETE",
  });
