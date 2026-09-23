// 格式工厂工具集的功能表单（provider=gsgc，站点云端直连）。文件通道走
// /api/uploads → /api/tasks(file_ids)；参数渲染由后端 ParamSpecs 驱动
//（枚举下拉/布尔开关/数字输入）。
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { SlidersHorizontal, TriangleAlert } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { FileDrop } from "./FileDrop";
import { Button, Field, Input, useToast } from "../ui";

export interface ToolMeta {
  provider: string;
  name: string;
  title: string;
  description: string;
  group: string;
}
export interface ToolEntry {
  meta: ToolMeta;
  param_specs: ParamSpec[];
}
export interface ParamSpec {
  key: string;
  label: string;
  type: "string" | "text" | "int" | "float" | "bool" | "enum" | "file";
  required?: boolean;
  default?: unknown;
  options?: { value: string; label: string }[];
  placeholder?: string;
}

/** 按分组定上传 accept（输入都是「该类别的源文件」） */
export const ACCEPT_BY_GROUP: Record<string, string> = {
  视频: ".mp4,.mkv,.webm,.mov,.avi,.flv,.ts",
  音频: ".mp3,.wav,.flac,.ogg,.m4a,.aac,.wma,.amr",
  图片: ".jpg,.jpeg,.png,.webp,.bmp,.gif,.tiff",
};
export const HINT_BY_GROUP: Record<string, string> = {
  视频: "mp4 / mkv / webm / mov / avi / flv / ts",
  音频: "mp3 / wav / flac / ogg / m4a / aac / wma / amr",
  图片: "jpg / png / webp / bmp / gif / tiff",
};

export function OpTaskForm({
  tool,
  disabled,
  warning,
  hint,
  onSubmit,
}: {
  tool: ToolEntry;
  disabled: boolean;
  /** 顶部依赖警告（如缺 ffmpeg/服务未启动） */
  warning?: string;
  /** 提交按钮旁的说明文字 */
  hint?: string;
  onSubmit: (body: Record<string, unknown>) => Promise<void>;
}) {
  const { toast } = useToast();
  const group = tool.meta.group;
  const [files, setFiles] = useState<File[]>([]);
  const [fileError, setFileError] = useState("");
  const [values, setValues] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const sp of tool.param_specs) {
      if (sp.default != null) init[sp.key] = String(sp.default);
    }
    return init;
  });

  const accept = ACCEPT_BY_GROUP[group] ?? "";
  const exts = accept.split(",").map((e) => e.replace(".", "").trim());

  const pickFiles = (fs: File[]) => {
    const bad = fs.find((f) => !exts.includes(f.name.split(".").pop()?.toLowerCase() ?? ""));
    if (bad) {
      setFileError(`不支持的格式：${bad.name}（可选 ${HINT_BY_GROUP[group]}）`);
      return;
    }
    setFileError("");
    setFiles(fs.slice(0, 1));
  };

  const submit = useMutation({
    mutationFn: async () => {
      if (files.length === 0) throw new Error("请先选择文件");
      const params: Record<string, unknown> = {};
      for (const sp of tool.param_specs) {
        const v = values[sp.key];
        if (v == null || v === "") continue;
        if (sp.type === "int") params[sp.key] = Math.trunc(Number(v));
        else if (sp.type === "float") params[sp.key] = Number(v);
        else if (sp.type === "bool") params[sp.key] = v === "true";
        else params[sp.key] = v;
      }
      const fd = new FormData();
      fd.append("file", files[0]);
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      await onSubmit({ provider: tool.meta.provider, tool: tool.meta.name, params, file_ids: [up.file_id] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "提交失败", description: e.message }),
  });

  return (
    <>
      {warning && (
        <div className="flex items-center gap-2 rounded-[var(--radius-sm)] border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-fg-2">
          <TriangleAlert size={13} strokeWidth={1.75} className="shrink-0 text-warn" />
          {warning}
        </div>
      )}

      <FileDrop
        file={files[0] ?? null}
        onFile={(f) => pickFiles(f ? [f] : [])}
        accept={accept}
        label="选择或拖入文件"
        emptyHint={HINT_BY_GROUP[group]}
        error={fileError}
      />

      {tool.param_specs.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2">
          {tool.param_specs.map((sp) => {
            const val = values[sp.key] ?? "";
            const set = (v: string) => setValues((m) => ({ ...m, [sp.key]: v }));
            const numberish = sp.type === "int" || sp.type === "float";
            return (
              <Field key={sp.key} label={sp.label + (sp.required ? " *" : "")} hint={sp.placeholder}>
                {({ id }) =>
                  sp.type === "enum" ? (
                    <select
                      id={id}
                      value={val}
                      onChange={(e) => set(e.target.value)}
                      className="h-9 w-full rounded-[var(--radius-sm)] border border-line bg-raise-1 px-2 text-sm text-fg"
                    >
                      <option value="">（默认）</option>
                      {(sp.options ?? []).map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  ) : sp.type === "bool" ? (
                    <select
                      id={id}
                      value={val}
                      onChange={(e) => set(e.target.value)}
                      className="h-9 w-full rounded-[var(--radius-sm)] border border-line bg-raise-1 px-2 text-sm text-fg"
                    >
                      <option value="">（默认）</option>
                      <option value="true">开启</option>
                      <option value="false">关闭</option>
                    </select>
                  ) : (
                    <Input
                      id={id}
                      value={val}
                      inputMode={numberish ? "decimal" : undefined}
                      type={numberish ? "number" : "text"}
                      step={sp.type === "float" ? "any" : undefined}
                      onChange={(e) => set(e.target.value)}
                      placeholder={sp.placeholder}
                    />
                  )
                }
              </Field>
            );
          })}
        </div>
      )}

      <div className="flex items-center justify-between gap-3">
        <p className="text-[11px] leading-relaxed text-muted">{hint}</p>
        <Button variant="primary" loading={submit.isPending} disabled={disabled} onClick={() => submit.mutate()} icon={<SlidersHorizontal size={14} strokeWidth={1.75} />}>
          {disabled ? "处理中…" : "开始处理"}
        </Button>
      </div>
    </>
  );
}
