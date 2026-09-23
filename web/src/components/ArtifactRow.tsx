import { Download, MicVocal } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { apiBase } from "../lib/api";
import type { Artifact } from "../lib/types";
import { WavePlayer } from "../ui";

const artifactLabel: Record<string, string> = {
  audio: "音频",
  transcript: "转写文本",
  subtitle: "字幕",
  dialog: "对话稿",
  translation: "译文文本",
  minutes: "纪要文件",
  archive: "压缩包",
};

const trackLabel: Record<string, string> = {
  voice: "人声轨",
  background: "背景音轨",
  music: "音乐轨",
  sfx: "音效轨",
};

export function formatSize(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 产物行：音频走波形播放器，其余显示文件名；统一提供大小与下载入口。
 *  音频产物附「去闪避」：携产物为人声跳 /duck（audio 产物皆可作闪避人声输入，
 *  不按来源工具设门——TTS/分离/历史各处行为一致）。 */
export function ArtifactRow({ a }: { a: Artifact }) {
  const navigate = useNavigate();
  const isAudio = a.kind === "audio";
  const label = a.meta?.track ? trackLabel[a.meta.track] : artifactLabel[a.kind] ?? a.kind;
  return (
    <div className="flex items-center gap-3 rounded-[var(--radius-sm)] border border-line bg-raise-2 p-3">
      <span className="w-16 shrink-0 text-xs text-fg-2">{label}</span>
      {isAudio ? (
        <WavePlayer
          src={`${apiBase}/api/artifacts/${a.id}/stream`}
          title={a.filename}
          sub={label}
          durationSec={a.duration_ms ? a.duration_ms / 1000 : undefined}
          className="min-w-0 flex-1"
        />
      ) : (
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted">{a.filename}</span>
      )}
      <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">{formatSize(a.size)}</span>
      {isAudio && (
        <button
          type="button"
          onClick={() => navigate(`/post?tab=duck&vocal=${a.id}`)}
          title="以该产物为人声，到口播闪避页压制 BGM"
          className="inline-flex shrink-0 cursor-pointer items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
        >
          <MicVocal size={13} strokeWidth={1.75} />
          去闪避
        </button>
      )}
      <a
        href={`${apiBase}/api/artifacts/${a.id}/download`}
        className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
      >
        <Download size={13} strokeWidth={1.75} />
        下载
      </a>
    </div>
  );
}
