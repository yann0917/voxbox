import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowUp, MessageCircle, Play, Square } from "lucide-react";
import type { Components } from "react-markdown";
import { useDefaultAssistantModel } from "../../lib/assistant";
import { streamPostSSE } from "../../lib/sse";
import { Card, CardBody, CardHeader, IconButton, Markdown, Textarea, WaveLoader } from "../../ui";
import { buildChatContext, linkifyCitations, seekHrefToMs, timedTranscript, todayText, type QNSegment } from "./model";

type ChatMsg = { role: "user" | "assistant" | "error"; content: string };

/** 单轮会话保留的最近消息条数（user/assistant 合计，超出丢弃最旧，提示一次） */
const MAX_MESSAGES = 10;

export interface ChatPanelProps {
  /** 转写分句：发送时组装上下文（不缓存旧转写） */
  segments: QNSegment[];
  /** 说话人展示名（含本会话改名覆盖），上下文里的说话人称呼 */
  speakerLabel: (id: string) => string;
  /** 引用 chip 点击跳播（轨标题/时长副标题由接线方并入） */
  onSeek: (ms: number) => void;
}

/** 问答区：就这段录音的转写继续追问——发送时组装转写上下文（含说话人改名与
 *  引用约定）走 /api/assistant/chat 的 SSE 流；回答中的【分:秒】渲染成 chip，
 *  点击跳播到对应位置。会话仅存于内存，切换任务或刷新即清。 */
export function ChatPanel({ segments, speakerLabel, onSeek }: ChatPanelProps) {
  // undefined=配置加载中（保守禁用）；null=未选默认模型（引导去设置页）
  const defaultModel = useDefaultAssistantModel();
  const [msgs, setMsgs] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [truncated, setTruncated] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickBottom = useRef(true);

  // 卸载即中止未完成的流（切换任务/离开页面）
  useEffect(() => () => abortRef.current?.abort(), []);

  // 流式输出贴底滚动；用户上翻（距底 > 64px）则停止跟随
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickBottom.current) el.scrollTop = el.scrollHeight;
  }, [msgs]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (el) stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 64;
  };

  const send = async () => {
    const text = input.trim();
    if (!text || streaming || !defaultModel) return;
    const history = msgs.filter((m) => m.role !== "error").map(({ role, content }) => ({ role, content }));
    let outgoing = [...history, { role: "user" as const, content: text }];
    if (outgoing.length > MAX_MESSAGES) {
      outgoing = outgoing.slice(-MAX_MESSAGES);
      setTruncated(true); // 只在首次截断时提示，之后的常规裁剪不再重复打扰
    }
    // 上下文发送时才组装：当前转写全文 + 引用约定 + 今天
    const context = buildChatContext(timedTranscript(segments, speakerLabel), todayText());
    setMsgs([...outgoing, { role: "assistant", content: "" }]);
    setInput("");
    setStreaming(true);
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const patchLast = (content: string) =>
      setMsgs((cur) => {
        const copy = cur.slice();
        copy[copy.length - 1] = { role: "assistant", content };
        return copy;
      });
    let acc = "";
    try {
      await streamPostSSE(
        "/api/assistant/chat",
        { provider: defaultModel.provider, model: defaultModel.model, messages: outgoing, context },
        (delta) => {
          acc += delta;
          patchLast(acc);
        },
        ctrl.signal,
      );
      if (!acc.trim()) patchLast("（没有返回内容）");
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") {
        if (!acc.trim()) setMsgs((cur) => cur.slice(0, -1)); // 未产出内容的中止不留空气泡
      } else {
        const message = e instanceof Error ? e.message : String(e);
        setMsgs((cur) => {
          const copy = cur.slice();
          const last = copy[copy.length - 1];
          if (last?.role === "assistant" && last.content === "") copy.pop(); // 失败不留空气泡
          return [...copy, { role: "error", content: message }];
        });
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  };

  /** 内部锚点链接（#seek-毫秒）→ 跳播 chip（点击不产生地址栏跳转）；其余链接
   *  保持默认外链样式。chip 不透传锚点属性（锚点的 type/onClick 与按钮类型相斥）。
   *  useMemo 稳定引用：键入改写 input 不再击穿 Markdown 的 memo 全量重渲气泡。 */
  const citeComponents = useMemo<Components>(() => {
    return {
      a: ({ href, children }) => {
        const ms = seekHrefToMs(href);
        if (ms !== null) {
          return (
            <button
              type="button"
              onClick={(e) => {
                e.preventDefault();
                onSeek(ms);
              }}
              className="inline-flex cursor-pointer items-center gap-0.5 rounded-full border border-line bg-inset px-1.5 align-baseline font-mono text-[11px] tabular-nums leading-4 text-accent transition-colors duration-150 hover:border-accent"
            >
              <Play size={9} strokeWidth={2} />
              {children}
            </button>
          );
        }
        return (
          <a className="text-accent underline decoration-line-strong underline-offset-2 hover:text-accent-hi" target="_blank" rel="noreferrer" href={href}>
            {children}
          </a>
        );
      },
    };
  }, [onSeek]);

  return (
    <Card>
      <CardHeader
        title="追问这段录音"
        icon={<MessageCircle size={15} strokeWidth={1.75} />}
        aside={defaultModel ? (
          <span className="micro">
            {defaultModel.provider} · {defaultModel.model}
          </span>
        ) : undefined}
      />
      <CardBody className="space-y-3">
        {defaultModel === null ? (
          <p className="text-xs text-fg-2">
            还没有选择 AI 模型：先在{" "}
            <Link to="/settings" className="text-accent hover:opacity-80">
              设置页
            </Link>{" "}
            选好「AI 默认大模型」，再来提问。
          </p>
        ) : (
          <>
            <div ref={scrollRef} onScroll={onScroll} className="flex max-h-80 min-h-20 flex-col gap-2.5 overflow-y-auto">
              {msgs.length === 0 && !streaming ? (
                <p className="text-xs text-muted">
                  就这段录音的内容提问，回答中的时间戳可以点击，跳到对应的位置播放。
                </p>
              ) : (
                <>
                  {truncated && (
                    <p className="micro border-b border-line pb-1.5 text-center text-muted">已截断早期对话</p>
                  )}
                  {msgs.map((m, i) =>
                    m.role === "error" ? (
                      <p
                        key={i}
                        className="rounded-[var(--radius-sm)] border border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-[color-mix(in_oklab,var(--danger)_8%,transparent)] px-2.5 py-1.5 text-xs break-words text-danger"
                      >
                        {m.content}
                      </p>
                    ) : m.role === "user" ? (
                      // 用户气泡保持纯文本：字面输入不该被 markdown 语法解释
                      <div
                        key={i}
                        className="self-end max-w-[85%] rounded-[var(--radius-md)] rounded-br-[4px] bg-raise-2 px-3 py-2 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-fg"
                      >
                        {m.content}
                      </div>
                    ) : (
                      // 助手气泡走 markdown 渲染，【分:秒】引用经 linkify 渲染为跳播 chip
                      <div
                        key={i}
                        className="self-start max-w-[92%] rounded-[var(--radius-md)] rounded-bl-[4px] bg-raise px-3 py-2 text-[13px] leading-relaxed text-fg"
                      >
                        <Markdown components={citeComponents}>{linkifyCitations(m.content)}</Markdown>
                      </div>
                    ),
                  )}
                  {streaming && msgs[msgs.length - 1]?.content === "" && (
                    <div className="self-start rounded-[var(--radius-md)] bg-raise px-3 py-2">
                      <WaveLoader label="思考中" />
                    </div>
                  )}
                </>
              )}
            </div>
            <div className="relative">
              <Textarea
                rows={2}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  // Enter 发送；Shift+Enter 换行；中文输入法组词中的 Enter 不触发
                  if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    void send();
                  }
                }}
                placeholder="问问这段录音的内容，Enter 发送，Shift+Enter 换行"
                className="pr-12 text-[13px]"
              />
              <div className="absolute right-2 bottom-2.5">
                {streaming ? (
                  <IconButton label="停止生成" onClick={() => abortRef.current?.abort()}>
                    <Square size={14} strokeWidth={2} />
                  </IconButton>
                ) : (
                  <IconButton label="发送" disabled={!input.trim() || !defaultModel} onClick={() => void send()}>
                    <ArrowUp size={16} strokeWidth={2} />
                  </IconButton>
                )}
              </div>
            </div>
          </>
        )}
      </CardBody>
    </Card>
  );
}
