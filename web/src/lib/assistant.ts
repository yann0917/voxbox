import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";
import { useSettings } from "./useStorageEnabled";

// 助手大模型共用读取层：「AI 默认大模型」设置键 assistant.default_model（"provider:model"）
// 与模型目录 /api/assistant/models（后端单一事实来源）。悬浮助手、AI 写作与录音笔记
// 加工共用同一口径——目录决定平台可用性，默认键决定选中项。

export interface AssistantPlatform {
  provider: string;
  label: string;
  enabled: boolean;
  models: { id: string; label: string }[];
}

/** 模型目录：未配置凭证的平台 enabled=false 且组内模型不可选。 */
export function useAssistantModels() {
  return useQuery({
    queryKey: ["assistant-models"],
    queryFn: () => fetchJSON<AssistantPlatform[]>("/api/assistant/models"),
    staleTime: 60_000,
  });
}

/** 默认大模型解析：设置键存在且平台仍可用（目录内 enabled + 模型在列）时返回
 *  provider/model；未配置或已失效返回 null（调用方禁用并引导去设置页）；
 *  设置/目录尚未加载完返回 undefined（保守禁用态，避免闪现可用）。 */
export function useDefaultAssistantModel(): { provider: string; model: string } | null | undefined {
  const { data: platforms, isLoading: modelsLoading } = useAssistantModels();
  const { data: settings, isLoading: settingsLoading } = useSettings();
  if (modelsLoading || settingsLoading) return undefined;
  const stored = settings?.assistant?.default_model ?? "";
  const usable =
    stored !== "" &&
    (platforms ?? []).some((p) => p.enabled && p.models.some((m) => `${p.provider}:${m.id}` === stored));
  if (!usable) return null;
  const i = stored.indexOf(":");
  return { provider: stored.slice(0, i), model: stored.slice(i + 1) };
}
