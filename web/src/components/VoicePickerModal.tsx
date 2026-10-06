import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pause, Play, Search, Star } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { addFavoriteVoice, favoriteVoiceRefs, listFavoriteVoices, removeFavoriteVoice } from "../lib/voiceFavorites";
import { Modal, Pagination, Tabs } from "../ui";

/** 弹框式音色选择器：点击音色字段弹出，音色列表按平台分类（页签），支持收藏
 *  （按平台收藏/移除、收藏列表分页）。仅 valueProvider 平台的音色可选用——
 *  其他平台页签供浏览与收藏，收藏的音色到对应引擎的音色选择里使用。 */

/** 收藏页每页条数（服务端分页）。 */
const FAV_PAGE_SIZE = 20;

/** 选择器覆盖的平台：数据源 /api/voices?provider=<value>。
 *  本地推理的音色语义不同（sid/family），暂不进弹框。 */
const PICKER_PROVIDERS: { value: string; label: string }[] = [
  { value: "minimax", label: "MiniMax" },
  { value: "volcengine", label: "火山引擎" },
  { value: "qianwen", label: "千问" },
  { value: "zhipu", label: "智谱" },
  { value: "xiaomi", label: "小米" },
  { value: "openrouter", label: "OpenRouter" },
];

type TabKey = "favorites" | (typeof PICKER_PROVIDERS)[number]["value"];

/** 归一化后的音色条目：各平台原始字段互不相同，进弹框前统一形态。 */
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

/** providerLabel 平台显示名（收藏行的平台徽标）。 */
const providerLabel = (p: string) => PICKER_PROVIDERS.find((x) => x.value === p)?.label ?? p;

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
  /** 当前引擎的平台：仅该平台的音色可选用 */
  valueProvider: string;
  /** 当前已选音色 ID */
  value: string;
  onPick: (voiceID: string) => void;
  /** 可选：归一化后的音色过滤（如火山流式/长文本仅 2.0 代际音色可用） */
  voiceFilter?: (v: PickerVoice) => boolean;
  /** 可选：行内附注（如千问音色不支持当前模型）；附注不阻断选用 */
  annotate?: (v: PickerVoice) => string | undefined;
}) {
  const [tab, setTab] = useState<TabKey>(valueProvider);
  const [query, setQuery] = useState("");
  const [favPage, setFavPage] = useState(1);
  const [favProvider, setFavProvider] = useState("");
  const [playingID, setPlayingID] = useState<string | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const qc = useQueryClient();

  /* 打开即回到当前引擎页签并清搜索（每次打开都是挑选音色的新起点） */
  useEffect(() => {
    if (open) {
      setTab(valueProvider);
      setQuery("");
      setFavPage(1);
      setFavProvider("");
    }
  }, [open, valueProvider]);

  /* 关闭时停止试听 */
  useEffect(() => {
    if (!open) {
      audioRef.current?.pause();
      setPlayingID(null);
    }
  }, [open]);

  /* 平台音色列表（当前页签） */
  const voicesQuery = useQuery({
    queryKey: ["voices", tab],
    queryFn: () => fetchJSON<{ voices: unknown[] }>(`/api/voices?provider=${tab}`),
    enabled: open && tab !== "favorites",
    retry: 1,
  });
  const voices = useMemo(
    () => (tab === "favorites" ? [] : adaptVoices(tab, voicesQuery.data?.voices ?? [])),
    [tab, voicesQuery.data],
  );

  /* 当前平台收藏引用对：voice_id → 收藏行 id（星标态 + 取消收藏定位行） */
  const favRefsQuery = useQuery({
    queryKey: ["voice-favorite-ids", tab],
    queryFn: () => favoriteVoiceRefs(tab),
    enabled: open && tab !== "favorites",
  });
  const favRefMap = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of favRefsQuery.data?.refs ?? []) m.set(r.voice_id, r.id);
    return m;
  }, [favRefsQuery.data]);

  /* 收藏列表（收藏页签，服务端分页） */
  const favsQuery = useQuery({
    queryKey: ["voice-favorites", favPage, favProvider],
    queryFn: () => listFavoriteVoices(favProvider, favPage, FAV_PAGE_SIZE),
    enabled: open && tab === "favorites",
  });
  const favs = favsQuery.data?.items ?? [];
  const favTotal = favsQuery.data?.total ?? 0;
  const favPageCount = Math.max(1, Math.ceil(favTotal / FAV_PAGE_SIZE));

  /* 收藏/移除后同时失效两组键（平台页签星标态 + 收藏列表） */
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["voice-favorites"] });
    void qc.invalidateQueries({ queryKey: ["voice-favorite-ids"] });
  };
  const addFav = useMutation({
    mutationFn: (v: { provider: string; id: string; label: string; group?: string }) =>
      addFavoriteVoice({
        provider: v.provider,
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

  /** togglePlay 单例试听（千问/智谱带试听 URL，与 ZhipuVoicePicker 同款单例播放）。 */
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

  /** pick 选用音色并关闭弹框。 */
  const pick = (voiceID: string) => {
    onPick(voiceID);
    onClose();
  };

  /* 平台页签：按分组归并 + 搜索过滤（客户端过滤，列表量 ≤ 数百）。
     分组排序：主语种优先（中文/粤语/英文/日文/韩文），其余按中文序，
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

  /* 收藏页签：客户端过滤当前页 + 平台筛选 */
  const filteredFavs = useMemo(() => {
    const kw = query.trim().toLowerCase();
    return favs.filter(
      (f) =>
        (!favProvider || f.provider === favProvider) &&
        (!kw ||
          f.label.toLowerCase().includes(kw) ||
          f.voice_id.toLowerCase().includes(kw) ||
          providerLabel(f.provider).toLowerCase().includes(kw)),
    );
  }, [favs, favProvider, query]);

  const isFavTab = tab === "favorites";
  const canPickHere = (provider: string) => provider === valueProvider;

  return (
    <Modal open={open} onClose={onClose} title="选择音色" width={640}>
      <div className="space-y-3">
        {/* 平台页签 + 收藏页签 */}
        <Tabs<TabKey>
          items={[
            { value: "favorites", label: `收藏${favTotal ? ` (${favTotal})` : ""}` },
            ...PICKER_PROVIDERS.map((p) => ({ value: p.value, label: p.label })),
          ]}
          value={tab}
          onChange={(v) => {
            setTab(v);
            setQuery("");
            setFavPage(1);
          }}
        />

        {/* 搜索 + 收藏页平台筛选 */}
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <Search size={13} strokeWidth={1.75} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={isFavTab ? "搜索收藏的音色…" : "搜索音色名称或 ID…"}
              className="w-full rounded-lg border border-line bg-transparent py-1.5 pl-8 pr-3 text-sm text-fg outline-none transition-colors placeholder:text-muted focus:border-accent"
            />
          </div>
          {isFavTab && (
            <select
              value={favProvider}
              onChange={(e) => {
                setFavProvider(e.target.value);
                setFavPage(1);
              }}
              aria-label="按平台筛选收藏"
              className="rounded-lg border border-line bg-transparent px-2 py-1.5 text-sm text-fg outline-none"
            >
              <option value="">全部平台</option>
              {PICKER_PROVIDERS.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          )}
        </div>

        {/* 列表区 */}
        <div className="max-h-[46vh] min-h-[240px] space-y-4 overflow-y-auto pr-1">
          {isFavTab ? (
            favsQuery.isLoading ? (
              <p className="py-8 text-center text-xs text-muted">加载中…</p>
            ) : filteredFavs.length === 0 ? (
              <p className="py-8 text-center text-xs text-muted">
                {favTotal === 0 && !favProvider
                  ? "还没有收藏音色：在各平台页签点星标收藏，常用音色一找即达。"
                  : "没有匹配的收藏。"}
              </p>
            ) : (
              <>
                <div className="space-y-1">
                  {filteredFavs.map((f) => {
                    const usable = canPickHere(f.provider);
                    return (
                      <div
                        key={f.id}
                        className={`flex items-center gap-2 rounded-lg border border-line px-3 py-2 ${
                          usable ? "cursor-pointer transition-colors hover:border-accent" : ""
                        }`}
                        onClick={() => usable && pick(f.voice_id)}
                        title={usable ? undefined : "该音色属于其他引擎，请到对应引擎的音色选择里使用"}
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
                          <p className="truncate text-sm text-fg">{f.label || f.voice_id}</p>
                          <p className="truncate font-mono text-[11px] text-muted">
                            {providerLabel(f.provider)} · {f.voice_id}
                          </p>
                        </div>
                        {usable &&
                          (f.voice_id === value ? (
                            <span className="shrink-0 text-[11px] text-accent">当前</span>
                          ) : (
                            <span className="shrink-0 text-[11px] text-fg-2">点击使用</span>
                          ))}
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
                    const isCurrent = canPickHere(tab) && value === v.id;
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
                              : addFav.mutate({ provider: tab, id: v.id, label: v.label, group: v.group })
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
                        <div
                          className={`min-w-0 flex-1 ${canPickHere(tab) ? "cursor-pointer" : ""}`}
                          onClick={() => canPickHere(tab) && pick(v.id)}
                          title={canPickHere(tab) ? undefined : "该音色属于其他引擎，收藏后到对应引擎使用"}
                        >
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
