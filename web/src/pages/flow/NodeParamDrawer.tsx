// 节点参数抽屉：ParamSpecs 通用渲染（枚举下拉/布尔开关/数字/文本）+
// 输入端口来源编辑（上游节点连线的选择规则 / file 输入槽引用）。
import { useState } from "react";
import { X } from "lucide-react";
import { portOf, type FlowNodeSpec, type InputSlot, type ToolInfo } from "../../lib/flow";
import { Button, Field, Input, Select, Switch, Textarea } from "../../ui";

const TRACK_OPTIONS = [
  { value: "", label: "自动（第一个匹配产物）" },
  { value: "voice", label: "人声轨" },
  { value: "background", label: "伴奏/背景轨" },
  { value: "music", label: "音乐轨" },
  { value: "sfx", label: "音效轨" },
];

export interface ParamDrawerProps {
  node: FlowNodeSpec;
  tool?: ToolInfo;
  /** 画布全部节点（供端口来源选上游） */
  allNodes: FlowNodeSpec[];
  inputs: InputSlot[];
  onApply: (next: FlowNodeSpec) => void;
  onDelete: () => void;
  onClose: () => void;
}

export function NodeParamDrawer({ node, tool, allNodes, inputs, onApply, onDelete, onClose }: ParamDrawerProps) {
  // 局部草稿：确认时一次性回传（避免每次击键触发画布重投影）
  const [draft, setDraft] = useState<FlowNodeSpec>(node);
  const port = portOf(node.provider, node.tool);
  const specs = tool?.param_specs ?? [];

  const setParam = (key: string, value: unknown) =>
    setDraft((d) => ({ ...d, params: { ...d.params, [key]: value } }));

  const setUpstream = (portKey: string, value: string) =>
    setDraft((d) => ({ ...d, inputs_from: { ...d.inputs_from, [portKey]: value } }));

  const upstreamNodes = allNodes.filter((n) => n.key !== draft.key);
  const fileSlots = inputs.filter((s) => s.type === "file");

  const applyRef = (portKey: string, kind: string, mode: "node" | "slot", nodeKey: string, sel: string, slotKey: string) => {
    if (mode === "slot") {
      setUpstream(portKey, slotKey ? `inputs.${slotKey}` : "");
      return;
    }
    if (!nodeKey) {
      setUpstream(portKey, "");
      return;
    }
    // 校验：上游可能产物与端口 kind 有交集才允许（与画布连线同规则）
    const src = allNodes.find((n) => n.key === nodeKey);
    const srcPort = src ? portOf(src.provider, src.tool) : null;
    if (srcPort && !srcPort.outKinds.some((k) => kind === "file" || k === kind)) {
      return; // 类型不符不落
    }
    setUpstream(portKey, sel ? `${nodeKey}:${sel}` : nodeKey);
  };

  const title = tool?.meta.title ?? `${node.provider}.${node.tool}`;

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/20 backdrop-blur-[2px]" onClick={onClose}>
      <aside
        className="flex h-full w-full max-w-md flex-col border-l border-line bg-raise shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{title}</p>
            <p className="truncate text-xs text-muted">
              {node.provider}.{node.tool}
            </p>
          </div>
          <button type="button" onClick={onClose} className="cursor-pointer text-muted transition-colors duration-150 hover:text-fg">
            <X size={16} />
          </button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
          {/* 输入端口来源 */}
          {port && port.in.length > 0 && (
            <section className="mb-6">
              <p className="mb-2 text-xs font-medium text-fg-2">输入来源</p>
              <div className="flex flex-col gap-3">
                {port.in.map((p) => {
                  const ref = draft.inputs_from?.[p.key] ?? "";
                  const isSlot = ref.startsWith("inputs.");
                  const [upNode, upSel = ""] = isSlot ? ["", ""] : ref.split(":");
                  return (
                    <div key={p.key} className="rounded-lg border border-line bg-raise-2 p-3">
                      <p className="mb-2 text-xs">
                        {p.label}
                        <span className="ml-1.5 text-muted">（{p.kind}）</span>
                      </p>
                      <div className="flex flex-wrap items-center gap-2">
                        <Select
                          value={isSlot ? "slot" : "node"}
                          onChange={(e) => {
                            if (e.target.value === "slot") {
                              applyRef(p.key, p.kind, "slot", "", "", fileSlots[0]?.key ?? "");
                            } else {
                              applyRef(p.key, p.kind, "node", upNode, upSel, "");
                            }
                          }}
                          className="w-24"
                        >
                          <option value="node">节点</option>
                          {fileSlots.length > 0 && <option value="slot">输入槽</option>}
                        </Select>
                        {isSlot ? (
                          <Select value={ref.slice("inputs.".length)} onChange={(e) => applyRef(p.key, p.kind, "slot", "", "", e.target.value)} className="min-w-0 flex-1">
                            {fileSlots.map((s) => (
                              <option key={s.key} value={s.key}>
                                {s.label}
                              </option>
                            ))}
                          </Select>
                        ) : (
                          <>
                            <Select value={upNode} onChange={(e) => applyRef(p.key, p.kind, "node", e.target.value, upSel, "")} className="min-w-0 flex-1">
                              <option value="">未连接</option>
                              {upstreamNodes.map((n) => (
                                <option key={n.key} value={n.key}>
                                  {n.key}（{n.provider}.{n.tool}）
                                </option>
                              ))}
                            </Select>
                            {upNode && (
                              <Select value={upSel} onChange={(e) => applyRef(p.key, p.kind, "node", upNode, e.target.value, "")} className="w-44">
                                {TRACK_OPTIONS.map((o) => (
                                  <option key={o.value} value={o.value}>
                                    {o.label}
                                  </option>
                                ))}
                              </Select>
                            )}
                          </>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          )}

          {/* 参数（ParamSpecs 驱动） */}
          <section>
            <p className="mb-2 text-xs font-medium text-fg-2">参数</p>
            {specs.length === 0 && <p className="text-xs text-muted">该工具无可调参数</p>}
            <div className="flex flex-col gap-3">
              {specs.map((ps) => {
                if (ps.type === "file") {
                  return (
                    <p key={ps.key} className="text-xs text-muted">
                      「{ps.label}」为文件参数：通过连线或输入槽提供，不在此填写。
                    </p>
                  );
                }
                if (ps.type === "bool") {
                  const v = draft.params[ps.key] ?? ps.default ?? false;
                  return (
                    <Field
                      key={ps.key}
                      label={ps.label}
                      aside={ps.placeholder}
                    >
                      {() => (
                        <div className="flex items-center py-1">
                          <Switch checked={Boolean(v)} onChange={(b) => setParam(ps.key, b)} />
                        </div>
                      )}
                    </Field>
                  );
                }
                if (ps.type === "enum") {
                  const v = String(draft.params[ps.key] ?? ps.default ?? "");
                  return (
                    <Field key={ps.key} label={ps.label} aside={ps.placeholder}>
                      {({ id }) => (
                        <Select id={id} value={v} onChange={(e) => setParam(ps.key, e.target.value)}>
                          {ps.options?.map((o) => (
                            <option key={o.value} value={o.value}>
                              {o.label}
                            </option>
                          ))}
                        </Select>
                      )}
                    </Field>
                  );
                }
                if (ps.type === "text") {
                  const v = String(draft.params[ps.key] ?? "");
                  return (
                    <Field key={ps.key} label={ps.label} hint={ps.required ? "必填；支持 {{inputs.槽key}} 引用运行时输入" : undefined} aside={ps.placeholder}>
                      {({ id }) => <Textarea id={id} rows={4} value={v} onChange={(e) => setParam(ps.key, e.target.value)} placeholder={ps.placeholder} />}
                    </Field>
                  );
                }
                const v = draft.params[ps.key] ?? ps.default ?? "";
                return (
                  <Field key={ps.key} label={ps.label} hint={ps.required ? "必填；支持 {{inputs.槽key}} 引用运行时输入" : undefined} aside={ps.placeholder}>
                    {({ id }) => (
                      <Input
                        id={id}
                        type={ps.type === "int" || ps.type === "float" ? "number" : "text"}
                        value={String(v)}
                        step={ps.type === "float" ? "0.1" : undefined}
                        onChange={(e) =>
                          setParam(ps.key, ps.type === "int" || ps.type === "float" ? (e.target.value === "" ? "" : Number(e.target.value)) : e.target.value)
                        }
                        placeholder={ps.placeholder}
                      />
                    )}
                  </Field>
                );
              })}
            </div>
          </section>
        </div>

        <footer className="flex items-center gap-2 border-t border-line px-4 py-3">
          <Button variant="primary" onClick={() => onApply(draft)}>
            应用
          </Button>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="danger" className="ml-auto" onClick={onDelete}>
            删除节点
          </Button>
        </footer>
      </aside>
    </div>
  );
}
