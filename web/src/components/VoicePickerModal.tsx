import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pause, Play, Search, Star } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { addFavoriteVoice, favoriteVoiceRefs, listFavoriteVoices, removeFavoriteVoice } from "../lib/voiceFavorites";
import { Modal, Pagination, Tabs } from "../ui";

/** 弹框式音色选择器：点击音色字段弹出，展示当前引擎平台的音色与收藏。
 *  平台归属由外层面板决定（引擎页签），弹窗内不再区分平台——两个页签：
 *  「全部音色」（当前平台，按语种/场景分组）与「收藏」（当前平台，服务端分页）。 */

/** 收藏页每页条数（服务端分页）。 */
const FAV_PAGE_SIZE = 20;

/** 平台显示名（弹窗标题附注）。 */
const PROVIDER_LABELS: Record<string, string> = {
  minimax: "MiniMax",
  volcengine: "火山引擎",
  qianwen: "千问",
  zhipu: "智谱",
  xiaomi: "小米",
  openrouter: "OpenRouter",
};

type TabKey = "all" | "favorites";

/** 归一化后的音色条目：各平台原始字段互不相同，进弹窗前统一形态。 */
interface PickerVoice {
  id: string;
  label: string;
  /** 分组名（语种/场景/官方与复刻），空 = 不分组 */
  group?: string;
  /** 试听 URL（仅千问/智谱带） */
  preview?: string;
  /** 平台原始条目：面板钩子（代际过滤/模型兼容附注）消费 */
  raw: unknown;
}

/** 各平台音色 → 归一化条目的适配（与后端 lookups.go 的音色 JSON 形状一一对应）。 */
function adaptVoices(provider: string, raw: unknown[]): PickerVoice[] {
  switch (provider) {
    case "minimax":
      // {id, name, lang?, label}
      return raw.map((v) => {
        const r = v as { id: string; name: string; lang?: string; label: string };
        return { id: r.id, label: r.label || r.name || r.id, group: r.lang || "其他 / 复刻音色", raw: r };
      });
    case "volcengine":
      // {id, name, gender, scenes[], languages[], ...}
      return raw.map((v) => {
        const r = v as { id: string; name: string; scenes?: string[]; languages?: string[] };
        return {
          id: r.id,
          label: [r.name, (r.languages ?? []).join("/")].filter(Boolean).join(" · ") || r.id,
          group: r.scenes?.[0] || "通用场景",
          raw: r,
        };
      });
    case "qianwen":
      // {id, desc, gender, languages[], models[], preview?}
      return raw.map((v) => {
        const r = v as { id: string; desc?: string; gender?: string; preview?: string };
        return {
          id: r.id,
          label: [r.desc || r.id, r.gender].filter(Boolean).join(" · "),
          group: r.gender || "音色",
          preview: r.preview,
          raw: r,
        };
      });
    case "zhipu":
      // {voice, voice_name, voice_type, download_url?}
      return raw.map((v) => {
        const r = v as { voice: string; voice_name: string; voice_type?: string; download_url?: string };
        return {
          id: r.voice,
          label: r.voice_name || r.voice,
          group: r.voice_type === "PRIVATE" ? "复刻音色" : "官方音色",
          preview: r.download_url,
          raw: r,
        };
      });
    case "xiaomi":
    case "openrouter":
      // xiaomi/openrouter 后端直接吐 {value,label} 枚举形态
      return raw.map((v) => {
        const r = v as { value?: string; id?: string; label: string };
        return { id: r.value ?? r.id ?? "", label: r.label, raw: r };
      });
    default:
      return [];
  }
}

export default function VoicePickerModal({
  open,
  onClose,
  valueProvider,
  value,
  onPick,
  voiceFilter,
  annotate,
}: {
  open: boolean;
  onClose: () => void;
  /** 当前引擎的平台：弹窗内容与收藏均限定于此平台 */
  valueProvider: string;
  /** 当前已选音色 ID */
  value: string;
  onPick: (voiceID: string) => void;
  /** 可选：归一化后的音色过滤（如火山流式/长文本仅 2.0 代际音色可用） */
  voiceFilter?: (v: PickerVoice) => boolean;
  /** 可选：行内附注（如千问音色不支持当前模型）；附注不阻断选用 */
  annotate?: (v: PickerVoice) => string | undefined;
}) {
  const [tab, setTab] = useState<TabKey>("all");
  const [query, setQuery] = useState("");
  const [favPage, setFavPage] = useState(1);
  const [playingID, setPlayingID] = useState<string | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const qc = useQueryClient();

  /* 每次打开回到全部音色并清搜索 */
  useEffect(() => {
    if (open) {
      setTab("all");
      setQuery("");
      setFavPage(1);
    }
  }, [open]);

  /* 关闭时停止试听 */
  useEffect(() => {
    if (!open) {
      audioRef.current?.pause();
      setPlayingID(null);
    }
  }, [open]);

  /* 当前平台音色列表 */
  const voicesQuery = useQuery({
    queryKey: ["voices", valueProvider],
    queryFn: () => fetchJSON<{ voices: unknown[] }>(`/api/voices?provider=${valueProvider}`),
    enabled: open,
    retry: 1,
  });
  const voices = useMemo(
    () => adaptVoices(valueProvider, voicesQuery.data?.voices ?? []),
    [valueProvider, voicesQuery.data],
  );

  /* 当前平台收藏引用对：voice_id → 收藏行 id（星标态 + 取消收藏定位行） */
  const favRefsQuery = useQuery({
    queryKey: ["voice-favorite-ids", valueProvider],
    queryFn: () => favoriteVoiceRefs(valueProvider),
    enabled: open,
  });
  const favRefMap = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of favRefsQuery.data?.refs ?? []) m.set(r.voice_id, r.id);
    return m;
  }, [favRefsQuery.data]);

  /* 收藏列表（收藏页签，服务端分页，限定当前平台） */
  const favsQuery = useQuery({
    queryKey: ["voice-favorites", valueProvider, favPage],
    queryFn: () => listFavoriteVoices(valueProvider, favPage, FAV_PAGE_SIZE),
    enabled: open && tab === "favorites",
  });
  const favs = favsQuery.data?.items ?? [];
  const favTotal = favsQuery.data?.total ?? 0;
  const favPageCount = Math.max(1, Math.ceil(favTotal / FAV_PAGE_SIZE));

  /* 收藏/移除后同时失效两组键（星标态 + 收藏列表） */
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["voice-favorites"] });
    void qc.invalidateQueries({ queryKey: ["voice-favorite-ids"] });
  };
  const addFav = useMutation({
    mutationFn: (v: PickerVoice) =>
      addFavoriteVoice({
        provider: valueProvider,
        voice_id: v.id,
        name: v.label,
        label: v.label,
        lang: v.group && v.group !== "其他 / 复刻音色" ? v.group : "",
      }),
    onSuccess: invalidate,
  });
  const removeFav = useMutation({
    mutationFn: (id: number) => removeFavoriteVoice(id),
    onSuccess: invalidate,
  });

  /** togglePlay 单例试听（千问/智谱带试听 URL，单例播放）。 */
  const togglePlay = (id: string, url: string) => {
    if (playingID === id) {
      audioRef.current?.pause();
      setPlayingID(null);
      return;
    }
    if (!audioRef.current) audioRef.current = new Audio();
    audioRef.current.src = url;
    audioRef.current.onended = () => setPlayingID(null);
    audioRef.current.play().catch(() => setPlayingID(null));
    setPlayingID(id);
  };

  /** pick 选用音色并关闭弹窗。 */
  const pick = (voiceID: string) => {
    onPick(voiceID);
    onClose();
  };

  /* 全部音色页签：按分组归并 + 过滤 + 搜索（客户端，列表量 ≤ 数百）。
     分组排序：主语种优先（中文/粤语/英文/日文/韩文），其余中文序，
     「其他 / 复刻音色」恒垫底——不依赖上游返回顺序。 */
  const grouped = useMemo(() => {
    const kw = query.trim().toLowerCase();
    const filtered = voices
      .filter((v) => !voiceFilter || voiceFilter(v))
      .filter((v) => !kw || v.label.toLowerCase().includes(kw) || v.id.toLowerCase().includes(kw));
    const groups = new Map<string, PickerVoice[]>();
    for (const v of filtered) {
      const g = v.group ?? "";
      const list = groups.get(g) ?? [];
      list.push(v);
      groups.set(g, list);
    }
    const preferred = ["中文", "中文 (粤语)", "英文", "日文", "韩文"];
    return [...groups.entries()].sort(([a], [b]) => {
      if (a === "其他 / 复刻音色") return 1;
      if (b === "其他 / 复刻音色") return -1;
      const ia = preferred.indexOf(a);
      const ib = preferred.indexOf(b);
      return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib) || a.localeCompare(b, "zh");
    });
  }, [voices, query, voiceFilter]);

  /* 收藏页签：客户端过滤当前页；通道限制（voiceFilter）与模型兼容附注
     仅对能在当前平台音色表里对上号的条目生效（自定义/复刻 ID 不在表内，原样展示） */
  const voiceByID = useMemo(() => new Map(voices.map((v) => [v.id, v])), [voices]);
  const filteredFavs = useMemo(() => {
    const kw = query.trim().toLowerCase();
    return favs.filter((f) => {
      const v = voiceByID.get(f.voice_id);
      if (v && voiceFilter && !voiceFilter(v)) return false;
      return !kw || f.label.toLowerCase().includes(kw) || f.voice_id.toLowerCase().includes(kw);
    });
  }, [favs, voiceByID, voiceFilter, query]);

  const isFavTab = tab === "favorites";
  const platformLabel = PROVIDER_LABELS[valueProvider] ?? valueProvider;

  return (
    <Modal open={open} onClose={onClose} title={`选择音色 · ${platformLabel}`} width={640}>
      <div className="space-y-3">
        <Tabs<TabKey>
          items={[
            { value: "all", label: "全部音色" },
            { value: "favorites", label: `收藏${favTotal ? ` (${favTotal})` : ""}` },
          ]}
          value={tab}
          onChange={(v) => {
            setTab(v);
            setQuery("");
            setFavPage(1);
          }}
        />

        {/* 搜索 */}
        <div className="relative">
          <Search size={13} strokeWidth={1.75} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={isFavTab ? "搜索收藏的音色…" : "搜索音色名称或 ID…"}
            className="w-full rounded-lg border border-line bg-transparent py-1.5 pl-8 pr-3 text-sm text-fg outline-none transition-colors placeholder:text-muted focus:border-accent"
          />
        </div>

        {/* 列表区 */}
        <div className="max-h-[46vh] min-h-[240px] space-y-4 overflow-y-auto pr-1">
          {isFavTab ? (
            favsQuery.isLoading ? (
              <p className="py-8 text-center text-xs text-muted">加载中…</p>
            ) : filteredFavs.length === 0 ? (
              <p className="py-8 text-center text-xs text-muted">
                {favTotal === 0
                  ? "还没有收藏音色：在「全部音色」里点星标收藏，常用音色一找即达。"
                  : "没有匹配的收藏。"}
              </p>
            ) : (
              <>
                <div className="space-y-1">
                  {filteredFavs.map((f) => {
                    const v = voiceByID.get(f.voice_id);
                    return (
                      <div
                        key={f.id}
                        className="flex cursor-pointer items-center gap-2 rounded-lg border border-line px-3 py-2 transition-colors hover:border-accent"
                        onClick={() => pick(f.voice_id)}
                      >
                        <button
                          type="button"
                          aria-label={`取消收藏 ${f.label}`}
                          className="shrink-0 text-warn"
                          onClick={(e) => {
                            e.stopPropagation();
                            removeFav.mutate(f.id);
                          }}
                        >
                          <Star size={15} strokeWidth={1.75} fill="currentColor" />
                        </button>
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm text-fg">
                            {f.label || f.voice_id}
                            {f.voice_id === value && <span className="ml-1.5 text-[11px] text-accent">当前</span>}
                            {v && annotate && (
                              <span className="ml-1.5 text-[11px] text-warn">{annotate(v)}</span>
                            )}
                          </p>
                          <p className="truncate font-mono text-[11px] text-muted">{f.voice_id}</p>
                        </div>
                        <span className="shrink-0 text-[11px] text-fg-2">点击使用</span>
                      </div>
                    );
                  })}
                </div>
                {favPageCount > 1 && (
                  <Pagination page={favPage} pageCount={favPageCount} total={favTotal} onChange={setFavPage} />
                )}
              </>
            )
          ) : voicesQuery.isLoading ? (
            <p className="py-8 text-center text-xs text-muted">加载中…</p>
          ) : voicesQuery.isError ? (
            <p className="py-8 text-center text-xs text-danger">
              音色列表加载失败：{voicesQuery.error instanceof Error ? voicesQuery.error.message : "请检查凭证配置"}
            </p>
          ) : grouped.length === 0 ? (
            <p className="py-8 text-center text-xs text-muted">没有匹配的音色。</p>
          ) : (
            grouped.map(([group, list]) => (
              <div key={group}>
                {group && <p className="mb-1.5 text-[11px] font-medium text-muted">{group}</p>}
                <div className="space-y-1">
                  {list.map((v) => {
                    const favorited = favRefMap.has(v.id);
                    const isCurrent = value === v.id;
                    return (
                      <div
                        key={v.id}
                        className={`group flex items-center gap-2 rounded-lg px-3 py-1.5 transition-colors hover:bg-raise-2/60 ${
                          isCurrent ? "bg-raise-2" : ""
                        }`}
                      >
                        <button
                          type="button"
                          aria-label={favorited ? `取消收藏 ${v.label}` : `收藏 ${v.label}`}
                          className={`shrink-0 ${
                            favorited
                              ? "text-warn"
                              : "text-muted opacity-0 transition-opacity group-hover:opacity-100"
                          }`}
                          onClick={() =>
                            favorited
                              ? removeFav.mutate(favRefMap.get(v.id)!)
                              : addFav.mutate(v)
                          }
                        >
                          <Star size={15} strokeWidth={1.75} fill={favorited ? "currentColor" : "none"} />
                        </button>
                        {v.preview && (
                          <button
                            type="button"
                            aria-label={`试听 ${v.label}`}
                            className="shrink-0 text-muted hover:text-accent"
                            onClick={() => togglePlay(v.id, v.preview!)}
                          >
                            {playingID === v.id ? (
                              <Pause size={14} strokeWidth={1.75} />
                            ) : (
                              <Play size={14} strokeWidth={1.75} />
                            )}
                          </button>
                        )}
                        <div className="min-w-0 flex-1 cursor-pointer" onClick={() => pick(v.id)}>
                          <p className="truncate text-sm text-fg">
                            {v.label}
                            {isCurrent && <span className="ml-1.5 text-[11px] text-accent">当前</span>}
                            {annotate && (
                              <span className="ml-1.5 text-[11px] text-warn">{annotate(v)}</span>
                            )}
                          </p>
                          {v.label !== v.id && (
                            <p className="truncate font-mono text-[11px] text-muted">{v.id}</p>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </Modal>
  );
}
