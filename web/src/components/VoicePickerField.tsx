import { useEffect, useState } from "react";
import { ChevronDown, PenLine } from "lucide-react";
import { Field, Input } from "../ui";
import VoicePickerModal from "./VoicePickerModal";

/** 弹框式音色选择的字段封装：触发按钮（回显当前音色）+ VoicePickerModal，
 *  可选「自定义 / 复刻音色 ID」输入（火山系克隆音色不在内置列表内时使用）。
 *  音色状态由面板持有；voiceList 仅用于回显与默认回填。 */
export default function VoicePickerField({
  provider,
  value,
  onChange,
  voices,
  loading,
  defaultVoiceId,
  allowCustomID,
  voiceFilter,
  annotate,
  hint,
}: {
  /** 引擎平台（弹框页签定位 + 可选用范围） */
  provider: string;
  /** 生效音色 ID（面板状态） */
  value: string;
  onChange: (id: string) => void;
  /** 平台音色列表（回显显示名 + 默认回填；拉取失败时触发按钮显示原始 ID） */
  voices: { id: string; label?: string; name?: string }[];
  loading?: boolean;
  /** 默认音色：列表加载后 value 为空时回填（列表内缺失则回落第一项） */
  defaultVoiceId?: string;
  /** 显示「自定义 / 复刻音色 ID」输入（火山系克隆音色场景） */
  allowCustomID?: boolean;
  /** 透传弹框：归一化音色过滤（如火山流式/长文本仅 2.0 代际） */
  voiceFilter?: (v: { raw: unknown }) => boolean;
  /** 透传弹框：行内附注（如千问音色不支持当前模型），附注不阻断选用 */
  annotate?: (v: { raw: unknown }) => string | undefined;
  hint?: string;
}) {
  const [open, setOpen] = useState(false);
  const [customMode, setCustomMode] = useState(false);
  const [customText, setCustomText] = useState("");

  /* 列表就绪后 value 仍为空 → 回填默认音色（旧下拉的自动选中语义） */
  useEffect(() => {
    if (customMode || value !== "" || voices.length === 0) return;
    const fallback = voices.find((v) => v.id === defaultVoiceId) ?? voices[0];
    if (fallback) onChange(fallback.id);
  }, [customMode, value, voices, defaultVoiceId, onChange]);

  const currentLabel = customMode
    ? customText.trim()
    : (voices.find((v) => v.id === value)?.label ??
      voices.find((v) => v.id === value)?.name ??
      value);

  return (
    <>
      <Field
        label="音色"
        hint={
          customMode
            ? "自定义模式：直接粘贴音色 ID（含复刻音色）"
            : hint ?? "点击打开音色库：按平台浏览、收藏、试听"
        }
      >
        {() => (
          <div className="space-y-2">
            <button
              type="button"
              onClick={() => setOpen(true)}
              disabled={loading}
              className="flex w-full items-center justify-between gap-2 rounded-lg border border-line bg-transparent px-3 py-2 text-left text-sm text-fg outline-none transition-colors hover:border-accent disabled:opacity-60"
            >
              <span className={`min-w-0 truncate ${currentLabel ? "" : "text-muted"}`}>
                {currentLabel || "点击选择音色…"}
              </span>
              <ChevronDown size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
            </button>
            {allowCustomID &&
              (customMode ? (
                <Input
                  value={customText}
                  onChange={(e) => {
                    setCustomText(e.target.value);
                    onChange(e.target.value.trim());
                  }}
                  placeholder="粘贴自定义 / 复刻音色 ID"
                  aria-label="自定义音色 ID"
                />
              ) : (
                <button
                  type="button"
                  className="flex items-center gap-1 text-[11px] text-muted transition-colors hover:text-accent"
                  onClick={() => {
                    setCustomMode(true);
                    setCustomText(value);
                  }}
                >
                  <PenLine size={12} strokeWidth={1.75} />
                  使用自定义 / 复刻音色 ID
                </button>
              ))}
          </div>
        )}
      </Field>
      <VoicePickerModal
        open={open}
        onClose={() => setOpen(false)}
        valueProvider={provider}
        value={customMode ? "" : value}
        onPick={(id) => {
          setCustomMode(false);
          setCustomText("");
          onChange(id);
        }}
        voiceFilter={voiceFilter}
        annotate={annotate}
      />
    </>
  );
}
