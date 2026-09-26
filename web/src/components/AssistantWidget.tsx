import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowUp, Sparkles, Square, X } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { usePlayer } from "../lib/player";
import { IconButton, Select, Textarea, useToast, WaveLoader } from "../ui";

interface AssistantPlatform {
  provider: string;
  label: string;
  enabled: boolean;
  models: { id: string; label: string }[];
}

type ChatMsg = { role: "user" | "assistant" | "error"; content: string };

/** 悬浮 AI 助手：登录后全页面常驻（Layout 挂载）。模型目录来自 /api/assistant/models
 *  （后端单一事实来源，未配置凭证的平台组禁用）；对话走 POST /api/assistant/chat 的
 *  SSE 流——预检错误响应 application/json 统一包络，流内错误为 {"error":...} 事件，
 *  正常终止为 {"done":true}。会话仅存于内存（切页不丢，刷新即清）。 */
export default function AssistantWidget() {
  const [open, setOpen] = useState(false);
  const [msgs, setMsgs] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [picked, setPicked] = useState(""); // 用户显式选择（"provider:modelId"）；空 = 回落默认
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickBottom = useRef(true);
  const { toast } = useToast();
  const hasTrack = usePlayer((s) => Boolean(s.track));

  const { data: platforms } = useQuery({
    queryKey: ["assistant-models"],
    queryFn: () => fetchJSON<AssistantPlatform[]>("/api/assistant/models"),
    staleTime: 60_000,
  });

  // 默认模型：首个已配置平台的第一个模型，渲染期派生（目录未到位为空串 → 显示占位）
  const model =
    picked ||
    (() => {
      const first =
        platforms?.find((p) => p.enabled && p.models.length > 0) ?? platforms?.find((p) => p.models.length > 0);
      return first ? `${first.provider}:${first.models[0].id}` : "";
    })();

  // 流式输出贴底滚动；用户上翻（距底 > 64px）则停止跟随
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickBottom.current) el.scrollTop = el.scrollHeight;
  }, [msgs]);

  // Esc 关闭面板（Modal 惯例；Select 打开时其内部监听先行拦截）
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  const selected = (() => {
    for (const p of platforms ?? []) {
      for (const m of p.models) {
        if (`${p.provider}:${m.id}` === model) return { provider: p.provider, model: m.id };
      }
    }
    return null;
  })();

  const onScroll = () => {
    const el = scrollRef.current;
    if (el) stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 64;
  };

  async function send() {
    const text = input.trim();
    if (!text || streaming) return;
    if (!selected) {
      toast({ tone: "warn", title: "请先在设置页配置任一平台的 API Key" });
      return;
    }
    const history = msgs.filter((m) => m.role !== "error");
    // outgoing = 历史 + 本次消息（assistant 占位气泡只进 UI,不发给后端）
    const outgoing: Pick<ChatMsg, "role" | "content">[] = [...history, { role: "user", content: text }];
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
      const resp = await fetch("/api/assistant/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: selected.provider,
          model: selected.model,
          messages: outgoing,
        }),
        signal: ctrl.signal,
      });
      const ct = resp.headers.get("content-type") ?? "";
      if (ct.includes("application/json")) {
        // 统一包络错误（未登录/参数/凭证等预检失败）
        const body = await resp.json().catch(() => null);
        throw new Error(body?.message || `业务错误码 ${body?.code ?? resp.status}`);
      }
      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`);
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      stream: for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        for (;;) {
          const sep = buf.indexOf("\n\n");
          if (sep < 0) break;
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          const line = frame.split("\n").find((l) => l.startsWith("data:"));
          if (!line) continue;
          const payload = JSON.parse(line.slice(5).trim()) as {
            delta?: string;
            done?: boolean;
            error?: { message: string };
          };
          if (payload.error) throw new Error(payload.error.message);
          if (payload.delta) {
            acc += payload.delta;
            patchLast(acc);
          }
          if (payload.done) break stream;
        }
      }
      if (!acc.trim()) patchLast("（上游没有返回内容）");
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
  }

  // PlayerBar 常驻底部时整体上抬让位；面板贴着按钮上方展开
  const bottom = hasTrack ? 96 : 24;

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="打开 AI 助手"
        title="AI 助手"
        style={{ bottom }}
        className="rise fixed right-5 z-40 flex size-12 cursor-pointer items-center justify-center rounded-full border border-line-strong bg-panel text-accent shadow-[var(--shadow-2)] backdrop-blur-xl transition-colors duration-150 hover:border-accent hover:text-accent-hi"
      >
        <Sparkles size={20} strokeWidth={1.75} />
      </button>
    );
  }

  return (
    <div
      role="dialog"
      aria-label="AI 助手"
      style={{ bottom: bottom + 60 }}
      className="rise fixed right-5 z-40 flex h-[min(560px,calc(100vh-11rem))] w-[380px] max-w-[calc(100vw-2.5rem)] flex-col overflow-hidden rounded-[var(--radius-lg)] border border-line bg-panel shadow-[var(--shadow-3)] backdrop-blur-xl"
    >
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <Sparkles size={15} strokeWidth={1.75} className="shrink-0 text-accent" />
        <span className="text-sm font-medium">AI 助手</span>
        <div className="ml-auto w-40">
          <Select value={model} onChange={(e) => setPicked(e.target.value)} placeholder="选择模型">
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
        </div>
        <IconButton label="关闭助手" size="sm" onClick={() => setOpen(false)}>
          <X size={16} strokeWidth={1.75} />
        </IconButton>
      </header>

      <div ref={scrollRef} onScroll={onScroll} className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-3 py-3">
        {msgs.length === 0 ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-1.5 text-center">
            <Sparkles size={18} strokeWidth={1.5} className="text-muted" />
            <p className="text-sm text-fg-2">问问 AI 助手</p>
            <p className="micro">回答由所选大模型生成</p>
          </div>
        ) : (
          msgs.map((m, i) =>
            m.role === "error" ? (
              <p
                key={i}
                className="rounded-[var(--radius-sm)] border border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-[color-mix(in_oklab,var(--danger)_8%,transparent)] px-2.5 py-1.5 text-xs break-words text-danger"
              >
                {m.content}
              </p>
            ) : (
              <div
                key={i}
                className={
                  m.role === "user"
                    ? "self-end max-w-[85%] rounded-[var(--radius-md)] rounded-br-[4px] bg-raise-2 px-3 py-2 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-fg"
                    : "self-start max-w-[92%] rounded-[var(--radius-md)] rounded-bl-[4px] bg-raise px-3 py-2 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-fg"
                }
              >
                {m.content}
              </div>
            ),
          )
        )}
        {streaming && msgs[msgs.length - 1]?.content === "" && (
          <div className="self-start rounded-[var(--radius-md)] bg-raise px-3 py-2">
            <WaveLoader label="生成中" />
          </div>
        )}
      </div>

      <footer className="shrink-0 border-t border-line p-2.5">
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
            placeholder="输入消息，Enter 发送，Shift+Enter 换行"
            className="pr-12 text-[13px]"
          />
          <div className="absolute right-2 bottom-2.5">
            {streaming ? (
              <IconButton label="停止生成" onClick={() => abortRef.current?.abort()}>
                <Square size={14} strokeWidth={2} />
              </IconButton>
            ) : (
              <IconButton label="发送" disabled={!input.trim()} onClick={() => void send()}>
                <ArrowUp size={16} strokeWidth={2} />
              </IconButton>
            )}
          </div>
        </div>
      </footer>
    </div>
  );
}
