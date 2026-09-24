import { useEffect, useRef, useState } from "react";
import { ChevronDown, Pause, Play, Search } from "lucide-react";
import { control } from "../ui/Field";

/** 智谱音色（/api/voices?provider=zhipu 运行时拉取）：官方 + 复刻音色，含试听 URL。 */
export interface ZhipuVoice {
  voice: string;
  voice_name: string;
  voice_type: "OFFICIAL" | "PRIVATE";
  download_url?: string;
  create_time?: string;
}

type TypeFilter = "all" | "OFFICIAL" | "PRIVATE";

/** 智谱音色菜单：搜索 + 官方/复刻筛选 + 逐音色试听（列表接口的 download_url）。
    复刻音色带「复刻」标记。 */
export default function ZhipuVoicePicker({
  voices,
  loading,
  value,
  onChange,
}: {
  voices: ZhipuVoice[];
  loading: boolean;
  value: string;
  onChange: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [type, setType] = useState<TypeFilter>("all");
  const [playingId, setPlayingId] = useState<string | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);

  /* 外点关闭 + 卸载时停止播放 */
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  useEffect(() => () => audioRef.current?.pause(), []);

  const q = query.trim().toLowerCase();
  const list = voices
    .filter(
      (v) =>
        (type === "all" || v.voice_type === type) &&
        (q === "" || v.voice.toLowerCase().includes(q) || v.voice_name.toLowerCase().includes(q)),
    )
    .sort((a, b) => {
      // 官方音色排前（接口按创建时间倒序，uuid 音色会顶在最前）；组内按可读名
      if (a.voice_type !== b.voice_type) return a.voice_type === "OFFICIAL" ? -1 : 1;
      return a.voice_name.localeCompare(b.voice_name, "zh");
    });
  // 值匹配兼容两种形态：列表返回的合成 ID（官方音色多为 uuid）与短名（如默认值 tongtong）
  const current =
    voices.find((v) => v.voice === value) ?? voices.find((v) => v.voice_name === value);

  const togglePlay = (v: ZhipuVoice) => {
    if (!v.download_url) return;
    if (playingId === v.voice) {
      audioRef.current?.pause();
      setPlayingId(null);
      return;
    }
    audioRef.current?.pause();
    const el = new Audio(v.download_url);
    el.onended = () => setPlayingId((p) => (p === v.voice ? null : p));
    el.play().catch(() => setPlayingId(null));
    audioRef.current = el;
    setPlayingId(v.voice);
  };

  const closeAndStop = () => {
    setOpen(false);
    audioRef.current?.pause();
    setPlayingId(null);
  };

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => (open ? closeAndStop() : setOpen(true))}
        className={`${control} flex min-h-9 cursor-pointer items-center justify-between gap-2 px-3 py-1.5 text-left`}
      >
        {loading ? (
          <span className="text-muted">音色加载中…</span>
        ) : (
          <span className="min-w-0 truncate text-sm text-fg">
            {current ? current.voice_name : value}
          </span>
        )}
        <ChevronDown
          size={14}
          strokeWidth={1.75}
          className={`shrink-0 text-muted transition-transform duration-150 ${open ? "rotate-180" : ""}`}
        />
      </button>

      {open && (
        <div
          role="listbox"
          aria-label="智谱音色列表"
          className="absolute left-0 right-0 top-[calc(100%+6px)] z-30 overflow-hidden rounded-[var(--radius-md)] border border-line-strong bg-panel backdrop-blur-xl shadow-[var(--shadow-3)]"
        >
          <div className="flex items-center gap-2 border-b border-line px-2.5 py-2">
            <div className="relative min-w-0 flex-1">
              <Search size={13} strokeWidth={1.75} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索音色"
                aria-label="搜索音色"
                className={`${control} h-8 pl-7 pr-2 text-xs`}
              />
            </div>
            <div className="flex shrink-0 rounded-[var(--radius-sm)] border border-line p-0.5">
              {(["all", "OFFICIAL", "PRIVATE"] as const).map((tp) => (
                <button
                  key={tp}
                  type="button"
                  onClick={() => setType(tp)}
                  aria-pressed={type === tp}
                  className={`cursor-pointer rounded-[5px] px-2 py-1 text-[11px] transition-colors duration-150 ${
                    type === tp ? "bg-raise-2 text-fg" : "text-muted hover:text-fg-2"
                  }`}
                >
                  {tp === "all" ? "全部" : tp === "OFFICIAL" ? "官方" : "复刻"}
                </button>
              ))}
            </div>
          </div>

          <div className="max-h-72 overflow-y-auto p-1">
            {list.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-muted">没有匹配的音色</p>
            ) : (
              list.map((v) => {
                const selected = v.voice === value;
                const playing = playingId === v.voice;
                return (
                  <div
                    key={v.voice}
                    role="option"
                    aria-selected={selected}
                    onClick={() => {
                      onChange(v.voice);
                      closeAndStop();
                    }}
                    className={`flex cursor-pointer items-center gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 transition-colors duration-150 ${
                      selected ? "bg-raise-2" : "hover:bg-raise-2/60"
                    }`}
                  >
                    {v.download_url ? (
                      <button
                        type="button"
                        aria-label={playing ? `停止试听 ${v.voice}` : `试听 ${v.voice}`}
                        title={playing ? "停止试听" : "试听官方样本"}
                        onClick={(e) => {
                          e.stopPropagation();
                          togglePlay(v);
                        }}
                        className={`flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-full border transition-colors duration-150 ${
                          playing
                            ? "border-accent bg-accent/15 text-accent"
                            : "border-line text-muted hover:border-accent hover:text-accent"
                        }`}
                      >
                        {playing ? <Pause size={12} strokeWidth={2} /> : <Play size={12} strokeWidth={2} />}
                      </button>
                    ) : (
                      <span className="size-7 shrink-0" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="flex items-baseline gap-1.5">
                        <span className="truncate text-sm text-fg">{v.voice_name}</span>
                        {v.voice_type === "PRIVATE" && <span className="text-[10px] text-accent">复刻</span>}
                      </span>
                      {/* 合成 ID：复刻音色展示完整值（管理/CLI 需要复制）；官方音色多为
                          uuid 形态对用户无意义，仅在与可读名不同且非 uuid 时显示短名 */}
                      {v.voice_type === "PRIVATE" && (
                        <span className="block truncate font-mono text-[11px] text-muted">{v.voice}</span>
                      )}
                    </span>
                  </div>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}
