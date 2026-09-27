import { useQuery } from "@tanstack/react-query";
import { apiBase, fetchJSON } from "./api";

/** GET /api/voice-library 的 items 元素(与 internal/voicelib.Voice 的 JSON 同构,已核对 handler)。 */
export interface VoiceLibItem {
  id: string;
  name: string;
  /** 成品 wav 时长(毫秒);后端入库时 ffmpeg 转码后 ffprobe 探测 */
  duration_ms: number;
  created_at: string;
}

/** 音色库全量列表(后端按创建时间倒序)。 */
export function useVoiceLibrary() {
  return useQuery({
    queryKey: ["voice-library"],
    queryFn: () => fetchJSON<{ items: VoiceLibItem[] }>("/api/voice-library"),
  });
}

/** 上传音色(multipart: name + file)。后端转码 24kHz 单声道,成品需 1-60 秒,超 60 秒自动裁剪。 */
export function uploadVoice(name: string, file: File) {
  const fd = new FormData();
  fd.append("name", name);
  fd.append("file", file);
  // 不设 Content-Type,让浏览器自动带 multipart boundary
  return fetchJSON<VoiceLibItem>("/api/voice-library", { method: "POST", body: fd, headers: {} });
}

/** 改名(body {name})。 */
export function renameVoice(id: string, name: string) {
  return fetchJSON<{ ok: boolean }>(`/api/voice-library/${id}`, {
    method: "PATCH",
    body: JSON.stringify({ name }),
  });
}

/** 删除音色目录(不可恢复)。 */
export function deleteVoice(id: string) {
  return fetchJSON<{ ok: boolean }>(`/api/voice-library/${id}`, { method: "DELETE" });
}

/** 音色成品 wav 流(与产物 stream 同款,Accept-Ranges,可作 WavePlayer src)。 */
export const voiceStreamUrl = (id: string) => `${apiBase}/api/voice-library/${id}/stream`;
