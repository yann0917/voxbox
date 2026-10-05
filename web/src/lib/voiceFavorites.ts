import { fetchJSON } from "./api";

/** 收藏音色（按平台分类、按人隔离）：弹框式音色选择器的收藏数据源。
 *  音色元数据收藏时快照入库，列表不回源各平台。 */
export interface FavoriteVoice {
  id: number;
  provider: string;
  voice_id: string;
  name: string;
  label: string;
  lang: string;
  created_at: string;
}

/** 收藏列表（分页 + 可选平台筛选）。 */
export function listFavoriteVoices(provider: string, page: number, size: number) {
  const q = new URLSearchParams({ page: String(page), size: String(size) });
  if (provider) q.set("provider", provider);
  return fetchJSON<{ items: FavoriteVoice[]; total: number }>(`/api/voice-favorites?${q}`);
}

/** 某平台已收藏音色的引用对（星标态渲染 + 取消收藏定位行，单平台一次拉全）。 */
export function favoriteVoiceRefs(provider: string) {
  return fetchJSON<{ refs: { id: number; voice_id: string }[] }>(
    `/api/voice-favorites/ids?provider=${encodeURIComponent(provider)}`,
  );
}

/** 收藏音色（幂等：重复收藏返回既有行）。 */
export function addFavoriteVoice(input: {
  provider: string;
  voice_id: string;
  name: string;
  label: string;
  lang?: string;
}) {
  return fetchJSON<{ item: FavoriteVoice; is_new: boolean }>("/api/voice-favorites", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

/** 取消收藏。 */
export function removeFavoriteVoice(id: number) {
  return fetchJSON(`/api/voice-favorites/${id}`, { method: "DELETE" });
}
