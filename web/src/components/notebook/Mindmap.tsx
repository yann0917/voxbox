import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Copy } from "lucide-react";
import { IconButton, useToast } from "../../ui";

/** 思维导图：解析 mindmap 模式的嵌套无序列表 markdown（"- " 行，两空格一级），
 *  渲染为可折叠的右向分支树（CSS 连接线）；根节点为任务主题。 */

export interface MindmapNode {
  text: string;
  children: MindmapNode[];
}

/** 宽容解析：跳过非列表行（模型偶尔夹带的空行/栅栏），按前导空格数定层级
 *  （2 空格一级，容忍 tab 与 1 空格）；比上一行更深视为其子级，更浅回栈。 */
export function parseMindmap(markdown: string): MindmapNode[] {
  const roots: MindmapNode[] = [];
  const stack: { level: number; node: MindmapNode }[] = [];
  for (const raw of markdown.split("\n")) {
    const m = /^(\s*)-\s+(.+)$/.exec(raw.replace(/\t/g, "  "));
    if (!m) continue;
    const level = Math.floor(m[1].replace(/ /g, "  ").length / 2);
    const node: MindmapNode = { text: m[2].trim(), children: [] };
    while (stack.length > 0 && stack[stack.length - 1].level >= level) stack.pop();
    if (stack.length === 0) roots.push(node);
    else stack[stack.length - 1].node.children.push(node);
    stack.push({ level, node });
  }
  return roots;
}

/** 思维导图视图：折叠/展开分支，右上角复制原始 markdown。 */
export function Mindmap({ markdown }: { markdown: string }) {
  const { toast } = useToast();
  const roots = useMemo(() => parseMindmap(markdown), [markdown]);
  if (roots.length === 0) {
    return <p className="text-xs text-muted">导图为空，可以重新生成。</p>;
  }
  return (
    <div className="overflow-x-auto rounded-[var(--radius-sm)] border border-line bg-inset p-3">
      <div className="mb-1 flex justify-end">
        <IconButton
          size="sm"
          label="复制导图内容（markdown）"
          onClick={() =>
            navigator.clipboard
              .writeText(markdown)
              .then(() => toast({ tone: "ok", title: "导图内容已复制" }))
              .catch((e: Error) => toast({ tone: "error", title: "复制失败", description: e.message }))
          }
        >
          <Copy size={13} strokeWidth={1.75} />
        </IconButton>
      </div>
      <ul className="min-w-max space-y-1.5">
        {roots.map((n, i) => (
          <MindmapBranch key={i} node={n} depth={0} />
        ))}
      </ul>
    </div>
  );
}

function MindmapBranch({ node, depth }: { node: MindmapNode; depth: number }) {
  const [open, setOpen] = useState(depth < 2);
  const hasChildren = node.children.length > 0;
  return (
    <li className="relative">
      <div className="flex items-center gap-1">
        {hasChildren ? (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="flex size-4 shrink-0 cursor-pointer items-center justify-center rounded text-muted transition-colors duration-150 hover:bg-raise-2 hover:text-fg-2"
            aria-label={open ? "收起分支" : "展开分支"}
          >
            {open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
          </button>
        ) : (
          <span className="inline-flex size-4 shrink-0 items-center justify-center">
            <span className="size-1.5 rounded-full bg-accent/60" aria-hidden />
          </span>
        )}
        <span
          className={`inline-block max-w-proc rounded-[var(--radius-sm)] border px-2 py-0.5 text-[12px] leading-snug ${
            depth === 0
              ? "border-accent/40 bg-accent/10 font-medium text-fg"
              : "border-line bg-raise-2 text-fg-2"
          }`}
        >
          {node.text}
        </span>
      </div>
      {hasChildren && open && (
        <ul className="ml-[7px] space-y-1 border-l border-line pl-4 pt-1">
          {node.children.map((c, i) => (
            <MindmapBranch key={i} node={c} depth={depth + 1} />
          ))}
        </ul>
      )}
    </li>
  );
}
