import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

/** AI 输出等非受信 markdown 的安全渲染：react-markdown 产出 React 元素且默认
 *  不透传原始 HTML（无 XSS 面），GFM 补表格/删除线/任务列表。字号按聊天气泡
 *  内 13px 排，标题不做大字级、只以字重与 0.5px 级差区分；代码块横滚不出气泡。
 *  首尾元素去外边距，避免气泡上下多出空隙。 */
export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="break-words text-[13px] leading-relaxed [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: (p) => <h1 className="mt-3 mb-1.5 text-sm font-semibold" {...p} />,
          h2: (p) => <h2 className="mt-3 mb-1.5 text-sm font-semibold" {...p} />,
          h3: (p) => <h3 className="mt-2.5 mb-1 text-[13px] font-semibold" {...p} />,
          h4: (p) => <h4 className="mt-2.5 mb-1 text-[13px] font-semibold" {...p} />,
          h5: (p) => <h5 className="mt-2 mb-1 text-[13px] font-medium" {...p} />,
          h6: (p) => <h6 className="mt-2 mb-1 text-[13px] font-medium" {...p} />,
          p: (p) => <p className="my-1.5" {...p} />,
          a: (p) => (
            <a className="text-accent underline decoration-line-strong underline-offset-2 hover:text-accent-hi" target="_blank" rel="noreferrer" {...p} />
          ),
          ul: (p) => <ul className="my-1.5 list-disc space-y-1 pl-5" {...p} />,
          ol: (p) => <ol className="my-1.5 list-decimal space-y-1 pl-5" {...p} />,
          blockquote: (p) => <blockquote className="my-1.5 border-l-2 border-line-strong pl-2.5 text-fg-2" {...p} />,
          hr: (p) => <hr className="my-2 border-line" {...p} />,
          // 内联代码默认样式；代码块内的 code 由 pre 的后代选择器抹平
          code: (p) => <code className="rounded-[4px] bg-inset px-1 py-0.5 font-mono text-[12px]" {...p} />,
          pre: (p) => (
            <pre
              className="my-1.5 overflow-x-auto rounded-[var(--radius-sm)] border border-line bg-inset p-2.5 font-mono text-[12px] leading-relaxed [&>code]:bg-transparent [&>code]:p-0 [&>code]:text-[12px]"
              {...p}
            />
          ),
          table: (p) => (
            <div className="my-1.5 overflow-x-auto">
              <table className="w-full border-collapse text-[12px]" {...p} />
            </div>
          ),
          th: (p) => <th className="border border-line bg-raise px-2 py-1 text-left font-medium" {...p} />,
          td: (p) => <td className="border border-line px-2 py-1 align-top" {...p} />,
          input: (p) => <input className="mr-1.5 align-middle accent-[var(--accent)]" {...p} />,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
});
