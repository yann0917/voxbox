import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";

export type ToastTone = "ok" | "error" | "warn" | "info";

interface ToastItem {
  id: number;
  tone: ToastTone;
  title: string;
  description?: string;
}

interface ToastApi {
  toast: (t: { tone?: ToastTone; title: string; description?: string }) => void;
}

const ToastCtx = createContext<ToastApi | null>(null);

const toneStyles: Record<ToastTone, { color: string; Icon: typeof Info }> = {
  ok: { color: "text-meter", Icon: CheckCircle2 },
  error: { color: "text-danger", Icon: XCircle },
  warn: { color: "text-warn", Icon: AlertTriangle },
  info: { color: "text-accent", Icon: Info },
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);

  const remove = useCallback((id: number) => setItems((prev) => prev.filter((t) => t.id !== id)), []);

  const api = useMemo<ToastApi>(
    () => ({
      toast: ({ tone = "info", title, description }) => {
        setItems((prev) => [...prev, { id: Date.now() + Math.random(), tone, title, description }].slice(-4));
      },
    }),
    []
  );

  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed right-4 top-4 z-50 flex w-[320px] flex-col gap-2">
        {items.map((t) => (
          <ToastCard key={t.id} item={t} onDone={() => remove(t.id)} />
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

function ToastCard({ item, onDone }: { item: ToastItem; onDone: () => void }) {
  const { color, Icon } = toneStyles[item.tone];

  useEffect(() => {
    const timer = setTimeout(onDone, 4000);
    return () => clearTimeout(timer);
  }, [onDone]);

  return (
    <div
      role={item.tone === "error" ? "alert" : "status"}
      className="rise pointer-events-auto flex items-start gap-2.5 rounded-[var(--radius-md)] border border-line bg-panel p-3 shadow-[var(--shadow-3)]"
    >
      <Icon size={16} className={`${color} mt-0.5 shrink-0`} strokeWidth={1.75} />
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="text-sm text-fg break-words">{item.title}</p>
        {item.description && <p className="text-xs text-muted break-words">{item.description}</p>}
      </div>
      <button
        onClick={onDone}
        aria-label="关闭提示"
        className="shrink-0 cursor-pointer text-muted transition-colors duration-150 hover:text-fg"
      >
        <X size={14} strokeWidth={1.75} />
      </button>
    </div>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error("useToast 必须在 ToastProvider 内使用");
  return ctx;
}
