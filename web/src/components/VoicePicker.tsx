import { useEffect, useMemo, useState } from "react";
import { Field, Input, Select } from "../ui";
import type { Voice } from "../lib/types";
import { ALL_FILTER, VoiceFilterSelects, matchVoice, type VoiceFilters } from "./VoiceFilters";

/** 音色下拉中的「自定义」哨兵值：允许填入内置列表之外（含声音复刻）的音色 ID */
export const CUSTOM_VOICE = "__custom__";

interface VoicePickerProps {
  /** 全量音色列表（/api/voices）；拉取失败或为空时降级为自由输入 */
  voices: Voice[];
  /** 列表拉取中 */
  loading?: boolean;
  /** 限定音色代际（如 "2.0"）；不传不过滤。长文本合成仅 2.0 音色可用 */
  generation?: string;
  /** 默认/兜底音色：筛选无选中时优先回退到它，列表不可用时作为占位 */
  defaultVoiceId: string;
  /** 生效音色变化回调：列表选中值或自定义输入值（筛选无匹配时为 ""） */
  onEffectiveVoiceChange: (id: string) => void;
}

/**
 * 音色选择器（TTSPage / TTSLongPage 共用）：场景/语种筛选 + 按主场景分组渲染
 * + 自定义音色 ID 输入。内部持有筛选与选中状态，对外只上报最终生效的音色 ID。
 */
export function VoicePicker({ voices, loading, generation, defaultVoiceId, onEffectiveVoiceChange }: VoicePickerProps) {
  const [filter, setFilter] = useState<VoiceFilters>({ scene: ALL_FILTER, lang: ALL_FILTER });
  const [selected, setSelected] = useState("");
  const [customText, setCustomText] = useState("");

  const usable = voices.length > 0;
  // 代际过滤后再走场景/语种筛选；筛选词表取自过滤后的列表，选项随代际收窄
  const generationFiltered = useMemo(
    () => (generation ? voices.filter((v) => v.generation === generation) : voices),
    [voices, generation],
  );
  const filtered = useMemo(() => generationFiltered.filter((v) => matchVoice(v, filter)), [generationFiltered, filter]);
  const groups = useMemo(() => {
    const byScene = new Map<string, Voice[]>();
    for (const v of filtered) {
      const key = v.scenes[0];
      const list = byScene.get(key) ?? [];
      list.push(v);
      byScene.set(key, list);
    }
    return [...byScene.entries()].sort(([a], [b]) => a.localeCompare(b, "zh"));
  }, [filtered]);
  // 筛选结果内的兜底音色：优先默认音色，否则第一项（空筛选结果 → ""）。
  // 基于场景/语种筛选后的列表取值，保证回退选中项始终在当前筛选结果内。
  const fallbackVoiceId = filtered.find((v) => v.id === defaultVoiceId)?.id ?? filtered[0]?.id ?? "";
  const voiceVal =
    selected === CUSTOM_VOICE
      ? CUSTOM_VOICE
      : selected && filtered.some((v) => v.id === selected)
        ? selected
        : fallbackVoiceId;
  // 自定义模式下用输入框的值（不兜默认，空值由提交方拦截）；列表不可用时兜默认音色
  const effectiveVoice = !usable
    ? customText.trim() || defaultVoiceId
    : selected === CUSTOM_VOICE
      ? customText.trim()
      : voiceVal;

  useEffect(() => {
    onEffectiveVoiceChange(effectiveVoice);
  }, [effectiveVoice, onEffectiveVoiceChange]);

  const hint = loading
    ? "正在拉取音色列表…"
    : usable
      ? filtered.length === 0
        ? "当前筛选无匹配音色，请调整场景/语种。"
        : generation
          ? `${generation} 音色共 ${generationFiltered.length} 个，已按场景/语种筛选（匹配 ${filtered.length} 个）。`
          : `官方音色列表，已按场景/语种筛选（匹配 ${filtered.length} 个）。`
      : "音色接口不可用，已降级为手动输入音色 ID。";

  return (
    <Field label="音色" hint={hint}>
      {({ id, ...rest }) =>
        usable ? (
          <div className="space-y-2">
            {generationFiltered.length > 0 && <VoiceFilterSelects value={filter} onChange={setFilter} voices={generationFiltered} />}
            <Select id={id} value={voiceVal} onChange={(e) => setSelected(e.target.value)} {...rest}>
              {groups.map(([scene, list]) => (
                <optgroup key={scene} label={scene}>
                  {list.map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.name} · {v.gender} · {v.languages[0]}
                    </option>
                  ))}
                </optgroup>
              ))}
              <option value={CUSTOM_VOICE}>自定义音色 ID…（含声音复刻音色）</option>
            </Select>
            {selected === CUSTOM_VOICE ? (
              <Input
                value={customText}
                onChange={(e) => setCustomText(e.target.value)}
                placeholder="粘贴自定义 / 复刻音色 ID"
                aria-label="自定义音色 ID"
              />
            ) : (
              voiceVal !== "" && (
                <p className="truncate font-mono text-[11px] text-muted" title={voiceVal}>
                  {voiceVal}
                </p>
              )
            )}
          </div>
        ) : (
          <Input
            id={id}
            value={customText}
            onChange={(e) => setCustomText(e.target.value)}
            placeholder={defaultVoiceId}
            disabled={loading}
            {...rest}
          />
        )
      }
    </Field>
  );
}
