import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FolderOpen } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { useMe } from "../lib/auth";
import { useSettings } from "../lib/useStorageEnabled";
import { Button, Card, CardBody, CardHeader, Field, Input, useToast } from "../ui";

/** 数据保存位置：语音模型、任务与产物、任务数据库共用的 dataDir。
 *  修改落 config.yaml 后重启生效（运行期不换 DB 连接）；旧数据不自动迁移，卡内提示搬移口径。 */
export default function DataDirCard() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data: settings } = useSettings();
  const current = settings?.data_dir ?? "";
  // null = 跟随已存值；非空 = 未保存草稿。空值/同值禁用保存（后端对同值也会拒绝）。
  const [draft, setDraft] = useState<string | null>(null);
  // 已保存、待重启生效的新目录：重启前 GET 回显的仍是当前生效值，卡内立牌提示以免误判。
  const [pendingDir, setPendingDir] = useState("");

  const save = useMutation({
    mutationFn: (dir: string) =>
      fetchJSON<{ dir: string; restart_required: boolean }>("/api/settings/data-dir", {
        method: "PUT",
        body: JSON.stringify({ dir }),
      }),
    onSuccess: (res) => {
      setDraft(null);
      setPendingDir(res.dir);
      toast({
        tone: "ok",
        title: "数据保存位置已保存",
        description: me?.desktop ? "重启应用后生效。" : "重启服务后生效。",
      });
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });

  const value = draft ?? pendingDir ?? current;
  // 保存后 GET 未热应用（重启生效），输入框改显刚保存的 pendingDir 而非跳回旧生效值；
  // 已保存值与当前生效值都不再算可保存改动（后者后端也会拒绝）。
  const dirty =
    draft !== null && draft.trim() !== "" && draft.trim() !== current && draft.trim() !== pendingDir;
  const isWin = typeof navigator !== "undefined" && /Win/i.test(navigator?.userAgent ?? "");
  const placeholder = isWin ? "如 C:\\Users\\you\\.voxbox\\data" : "如 ~/.voxbox/data";

  return (
    <Card>
      <CardHeader title="数据保存位置" icon={<FolderOpen size={15} strokeWidth={1.75} />} />
      <CardBody className="space-y-4">
        <p className="text-xs text-muted">
          语音模型、任务与产物、任务数据库都保存在此目录。切换后原有目录的数据不会自动迁移：
          模型可删除后重新下载，任务与产物需在停止服务后手动搬移。
        </p>
        <Field label="数据目录" hint="必须是绝对路径；也可以直接编辑 ~/.voxbox/config.yaml 的 data_dir 后重启">
          {({ id, ...rest }) => (
            <Input
              id={id}
              value={value}
              placeholder={placeholder}
              className="font-mono"
              onChange={(e) => setDraft(e.target.value)}
              {...rest}
            />
          )}
        </Field>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            variant="primary"
            loading={save.isPending}
            disabled={!dirty}
            onClick={() => save.mutate(value.trim())}
          >
            保存
          </Button>
          {pendingDir && pendingDir !== current && (
            <span className="text-xs text-warn">
              已保存为 <code className="font-mono">{pendingDir}</code>，重启后生效
            </span>
          )}
        </div>
      </CardBody>
    </Card>
  );
}
