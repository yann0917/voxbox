import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { useSettings } from "../lib/useStorageEnabled";
import { Card, CardBody, CardHeader, Field, Select, useToast } from "../ui";

interface AssistantPlatform {
  provider: string;
  label: string;
  enabled: boolean;
  models: { id: string; label: string }[];
}

/** AI 默认大模型：悬浮助手与语音合成页 AI 生成/润色共用的默认模型。
 *  选项即助手模型目录（按已配置凭证标注可用）；空值 = 自动回落第一个已配置平台。 */
export default function AssistantModelCard() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data: settings } = useSettings();
  const { data: platforms } = useQuery({
    queryKey: ["assistant-models"],
    queryFn: () => fetchJSON<AssistantPlatform[]>("/api/assistant/models"),
    staleTime: 60_000,
  });

  const stored = settings?.assistant?.default_model ?? "";
  const refresh = () => void qc.invalidateQueries({ queryKey: ["settings"] });

  const save = useMutation({
    mutationFn: (default_model: string) =>
      fetchJSON("/api/settings/assistant", { method: "PUT", body: JSON.stringify({ default_model }) }),
    onSuccess: () => {
      toast({ tone: "ok", title: "默认大模型已保存", description: "悬浮助手与 AI 生成/润色即时生效。" });
      refresh();
    },
    onError: (e: Error) => {
      toast({ tone: "error", title: "保存失败", description: e.message });
      refresh(); // 回显真实已存值
    },
  });

  return (
    <Card>
      <CardHeader
        title="AI 默认大模型"
        icon={<Sparkles size={15} strokeWidth={1.75} />}
        aside={<span className="micro">{stored ? "已设置" : "自动"}</span>}
      />
      <CardBody className="space-y-4">
        <p className="text-xs text-muted">
          悬浮 AI 助手与语音合成页的 AI 生成、AI 润色共用这个模型；选「自动」时使用第一个已配置凭证平台的默认模型。
        </p>
        <Field label="模型" hint={platforms?.some((p) => p.enabled) ? undefined : "先在上方配置任一平台的 API Key，才能使用 AI 对话与写作"}>
          {({ id, ...rest }) => (
            <Select id={id} value={stored} onChange={(e) => save.mutate(e.target.value)} disabled={save.isPending} {...rest}>
              <option value="">自动选择</option>
              {(platforms ?? []).map((p) => (
                <optgroup key={p.provider} label={p.label}>
                  {p.models.map((m) => (
                    <option key={m.id} value={`${p.provider}:${m.id}`} disabled={!p.enabled}>
                      {m.label}
                      {!p.enabled ? " · 未配置" : ""}
                    </option>
                  ))}
                </optgroup>
              ))}
            </Select>
          )}
        </Field>
      </CardBody>
    </Card>
  );
}
