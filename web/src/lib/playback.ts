import { apiBase } from "./api";
import type { Artifact, Task } from "./types";

/**
 * 任务音频回放源，按输入可回放程度排序：
 * 跨工具产物 → 上传文件 → 输入 URL → 音频产物（TTS 等合成类）。
 * 找不到可播源返回 null（如输入文件已删除）。
 */
export function resolvePlaySrc(task: Task, artifacts: Artifact[]): string | null {
  if (task.input?.artifact_input) {
    return `${apiBase}/api/artifacts/${task.input.artifact_input}/stream`;
  }
  if (task.input?.file_ids?.length) {
    return `${apiBase}/api/uploads/${task.input.file_ids[0]}/stream`;
  }
  const url = typeof task.params?.url === "string" ? task.params.url : "";
  if (/^https?:\/\//i.test(url)) return url;
  const audio = artifacts.find((a) => a.kind === "audio");
  return audio ? `${apiBase}/api/artifacts/${audio.id}/stream` : null;
}
