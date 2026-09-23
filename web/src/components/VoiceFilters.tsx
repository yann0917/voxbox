import { Field, Select } from "../ui";
import type { Voice } from "../lib/types";

export interface VoiceFilters {
  scene: string; // "all" 或场景值
  lang: string; // "all" 或语种值
}

export const ALL_FILTER = "all";

/** 场景/语种筛选词表：与后端 voices_test 锁定的词表一致，新增值需同步。 */
export function voiceFilterOptions(voices: Voice[]) {
  const zh = (a: string, b: string) => a.localeCompare(b, "zh");
  return {
    scenes: [...new Set(voices.flatMap((v) => v.scenes))].sort(zh),
    langs: [...new Set(voices.flatMap((v) => v.languages))].sort(zh),
  };
}

export function matchVoice(v: Voice, f: VoiceFilters): boolean {
  return (
    (f.scene === ALL_FILTER || v.scenes.includes(f.scene)) &&
    (f.lang === ALL_FILTER || v.languages.includes(f.lang))
  );
}

interface VoiceFilterSelectsProps {
  value: VoiceFilters;
  onChange: (f: VoiceFilters) => void;
  voices: Voice[];
  disabled?: boolean;
}

/** 场景 + 语种筛选行（TTS/播客页共用的音色筛选器）。 */
export function VoiceFilterSelects({ value, onChange, voices, disabled }: VoiceFilterSelectsProps) {
  const { scenes, langs } = voiceFilterOptions(voices);
  return (
    <div className="grid grid-cols-2 gap-2">
      <Field label="场景">
        {({ id, ...rest }) => (
          <Select
            id={id}
            value={value.scene}
            onChange={(e) => onChange({ ...value, scene: e.target.value })}
            disabled={disabled}
            aria-label="按场景筛选音色"
            {...rest}
          >
            <option value={ALL_FILTER}>全部场景</option>
            {scenes.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <Field label="语种">
        {({ id, ...rest }) => (
          <Select
            id={id}
            value={value.lang}
            onChange={(e) => onChange({ ...value, lang: e.target.value })}
            disabled={disabled}
            aria-label="按语种筛选音色"
            {...rest}
          >
            <option value={ALL_FILTER}>全部语种</option>
            {langs.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </Select>
        )}
      </Field>
    </div>
  );
}
