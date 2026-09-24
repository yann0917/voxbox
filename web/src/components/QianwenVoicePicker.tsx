import { useEffect, useRef, useState } from "react";
import { ChevronDown, Pause, Play, Search } from "lucide-react";
import { control } from "../ui/Field";

/** 千问非实时音色（/api/voices?provider=qianwen）：含官方试听 URL 与模型支持矩阵。 */
export interface QianwenVoice {
  id: string;
  desc: string;
  gender: "男" | "女";
  languages: string[];
  models: string[];
  preview?: string;
}

type GenderFilter = "all" | "女" | "男";

/** 千问音色菜单：搜索 + 性别筛选 + 逐音色试听（官方样本，公开 CDN 免费直播）。
    不支持当前模型的音色置灰仍可选（供先选音色再切模型的路径）。 */
export default function QianwenVoicePicker({
  voices,
  loading,
  value,
  model,
  onChange,
}: {
  voices: QianwenVoice[];
  loading: boolean;
  value: string;
  model: string;
  onChange: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [gender, setGender] = useState<GenderFilter>("all");
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
  const list = voices.filter(
    (v) =>
      (gender === "all" || v.gender === gender) &&
      (q === "" || v.id.toLowerCase().includes(q) || v.desc.toLowerCase().includes(q)),
  );
  const current = voices.find((v) => v.id === value);

  const togglePlay = (v: QianwenVoice) => {
    if (!v.preview) return;
    if (playingId === v.id) {
      audioRef.current?.pause();
      setPlayingId(null);
      return;
    }
    audioRef.current?.pause();
    const el = new Audio(v.preview);
    el.onended = () => setPlayingId((p) => (p === v.id ? null : p));
    el.play().catch(() => setPlayingId(null));
    audioRef.current = el;
    setPlayingId(v.id);
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
            {current ? `${current.id} · ${current.desc}（${current.gender}）` : value}
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
          aria-label="千问音色列表"
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
              {(["all", "女", "男"] as const).map((g) => (
                <button
                  key={g}
                  type="button"
                  onClick={() => setGender(g)}
                  aria-pressed={gender === g}
                  className={`cursor-pointer rounded-[5px] px-2 py-1 text-[11px] transition-colors duration-150 ${
                    gender === g ? "bg-raise-2 text-fg" : "text-muted hover:text-fg-2"
                  }`}
                >
                  {g === "all" ? "全部" : `${g}声`}
                </button>
              ))}
            </div>
          </div>

          <div className="max-h-72 overflow-y-auto p-1">
            {list.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-muted">没有匹配的音色</p>
            ) : (
              list.map((v) => {
                const supported = v.models.includes(model);
                const selected = v.id === value;
                const playing = playingId === v.id;
                return (
                  <div
                    key={v.id}
                    role="option"
                    aria-selected={selected}
                    onClick={() => {
                      onChange(v.id);
                      closeAndStop();
                    }}
                    className={`flex cursor-pointer items-center gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 transition-colors duration-150 ${
                      selected ? "bg-raise-2" : "hover:bg-raise-2/60"
                    }`}
                  >
                    {v.preview ? (
                      <button
                        type="button"
                        aria-label={playing ? `停止试听 ${v.id}` : `试听 ${v.id}`}
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
                        <span className="truncate text-sm text-fg">{v.id}</span>
                        <span className="text-[10px] text-muted">{v.gender}</span>
                        {!supported && <span className="text-[10px] text-warn">不支持当前模型</span>}
                      </span>
                      <span className="block truncate text-[11px] text-fg-2">{v.desc}</span>
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
