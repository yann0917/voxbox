import {
  Children,
  isValidElement,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import type { ButtonHTMLAttributes } from "react";
import { Check, ChevronDown } from "lucide-react";
import { control } from "./Field";

export interface SelectChangeEvent {
  target: { value: string };
}

export interface SelectProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange" | "value" | "defaultValue"> {
  value?: string;
  defaultValue?: string;
  /** 与原生 select 事件同形：onChange={(e) => set(e.target.value)} */
  onChange?: (e: SelectChangeEvent) => void;
  /** 无匹配选项时的占位文案 */
  placeholder?: string;
  children?: ReactNode;
}

interface OptionItem {
  value: string;
  label: string;
  disabled: boolean;
  group?: string;
}

function flattenText(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (isValidElement(node)) return flattenText((node.props as { children?: ReactNode }).children);
  if (Array.isArray(node)) return node.map(flattenText).join("");
  return "";
}

/** 解析 <option> / <optgroup> 子元素——保持与原生 select 一致的声明式写法。 */
function parseOptions(children: ReactNode): OptionItem[] {
  const out: OptionItem[] = [];
  Children.forEach(children, (child) => {
    if (!isValidElement(child)) return;
    const props = child.props as {
      value?: string;
      disabled?: boolean;
      label?: string;
      children?: ReactNode;
    };
    if (child.type === "optgroup") {
      const group = props.label ?? "";
      Children.forEach(props.children, (o) => {
        if (!isValidElement(o) || o.type !== "option") return;
        const op = o.props as { value?: string; disabled?: boolean; children?: ReactNode };
        out.push({ value: op.value ?? "", label: flattenText(op.children), disabled: !!op.disabled, group });
      });
    } else if (child.type === "option") {
      out.push({ value: props.value ?? "", label: flattenText(props.children), disabled: !!props.disabled });
    }
  });
  return out;
}

const PANEL_MAX = 288; // max-h-72

export function Select({
  value,
  defaultValue,
  onChange,
  children,
  className = "",
  id,
  onKeyDown,
  disabled,
  placeholder = "请选择",
  ...rest
}: SelectProps) {
  const [open, setOpen] = useState(false);
  const [uncontrolled, setUncontrolled] = useState(defaultValue ?? "");
  const [active, setActive] = useState(-1);
  const [flipUp, setFlipUp] = useState(false);
  const [shiftX, setShiftX] = useState(0);
  const current = value !== undefined ? value : uncontrolled;

  const opts = useMemo(() => parseOptions(children), [children]);
  const selected = opts.find((o) => o.value === current);

  const listboxId = useId();
  const wrapRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLUListElement>(null);
  const typeahead = useRef({ buf: "", timer: 0 });

  const firstEnabled = () => opts.findIndex((o) => !o.disabled);
  const lastEnabled = () => {
    for (let i = opts.length - 1; i >= 0; i--) if (!opts[i].disabled) return i;
    return -1;
  };
  /** 从 from 沿 dir 环绕查找下一个可用项。 */
  const step = (from: number, dir: 1 | -1) => {
    const n = opts.length;
    for (let i = 1; i <= n; i++) {
      const idx = (((from + dir * i) % n) + n) % n;
      if (!opts[idx].disabled) return idx;
    }
    return from;
  };

  const openMenu = () => {
    if (disabled || opts.length === 0) return;
    const idx = opts.findIndex((o) => o.value === current);
    setActive(idx >= 0 && !opts[idx].disabled ? idx : firstEnabled());
    setFlipUp(false);
    setOpen(true);
  };

  const commit = (v: string) => {
    if (value === undefined) setUncontrolled(v);
    onChange?.({ target: { value: v } });
    setOpen(false);
  };

  const close = () => {
    setOpen(false);
    setActive(-1);
  };

  // 面板定位：下方放不下且上方更宽裕时向上翻；内容更宽时自适应宽度并避免溢出视口
  useLayoutEffect(() => {
    if (!open) return;
    const trig = triggerRef.current;
    const panel = panelRef.current;
    if (!trig || !panel) return;
    const rect = trig.getBoundingClientRect();
    const below = window.innerHeight - rect.bottom;
    const need = Math.min(panel.scrollHeight, PANEL_MAX) + 12;
    setFlipUp(below < need && rect.top > below);
    const box = panel.getBoundingClientRect();
    setShiftX(Math.min(0, window.innerWidth - 8 - box.right));
  }, [open]);

  // 活动项滚入可视区
  useEffect(() => {
    if (!open || active < 0) return;
    panelRef.current
      ?.querySelector(`[id="${CSS.escape(`${listboxId}-${active}`)}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [open, active, listboxId]);

  // 点击面板外关闭
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) close();
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);

  useEffect(() => () => window.clearTimeout(typeahead.current.timer), []);

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLButtonElement>) => {
    if (disabled) return;
    if (!open) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        openMenu();
      }
      return;
    }
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActive((a) => step(a < 0 ? firstEnabled() : a, 1));
        break;
      case "ArrowUp":
        e.preventDefault();
        setActive((a) => step(a < 0 ? firstEnabled() : a, -1));
        break;
      case "Home":
        e.preventDefault();
        setActive(firstEnabled());
        break;
      case "End":
        e.preventDefault();
        setActive(lastEnabled());
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        if (active >= 0 && !opts[active].disabled) commit(opts[active].value);
        break;
      case "Escape":
        // 只关面板，不冒泡给底层 Modal
        e.preventDefault();
        e.stopPropagation();
        close();
        break;
      case "Tab":
        close();
        break;
      default: {
        // 首字跳转：600ms 内连续按键累积匹配
        if (e.key.length !== 1 || e.metaKey || e.ctrlKey || e.altKey) return;
        e.preventDefault();
        const t = typeahead.current;
        window.clearTimeout(t.timer);
        t.buf = (t.buf + e.key.toLowerCase()).slice(-32);
        t.timer = window.setTimeout(() => (t.buf = ""), 600);
        const n = opts.length;
        for (let i = 1; i <= n; i++) {
          const idx = (((active + i) % n) + n) % n;
          const o = opts[idx];
          if (o.disabled) continue;
          const lbl = o.label.toLowerCase();
          if (lbl.startsWith(t.buf) || lbl.includes(t.buf)) {
            setActive(idx);
            break;
          }
        }
      }
    }
  };

  // 分组渲染：组头（micro 刻印微标签）+ 选项行
  const nodes: ReactNode[] = [];
  let lastGroup: string | undefined;
  opts.forEach((o, i) => {
    if (o.group !== lastGroup) {
      nodes.push(
        <li key={`g-${i}`} role="presentation" className="micro px-2.5 pb-1 pt-2">
          {o.group}
        </li>,
      );
      lastGroup = o.group;
    }
    const isSelected = o.value === current;
    nodes.push(
      <li
        key={`o-${i}-${o.value}`}
        id={`${listboxId}-${i}`}
        role="option"
        aria-selected={isSelected}
        aria-disabled={o.disabled || undefined}
        onMouseDown={(e) => e.preventDefault()}
        onMouseEnter={() => setActive(i)}
        onClick={() => {
          if (!o.disabled) commit(o.value);
        }}
        className={`flex h-8 cursor-pointer items-center justify-between gap-2 rounded-[var(--radius-sm)] px-2.5 text-sm transition-colors duration-150 hover:bg-raise-2 aria-disabled:cursor-not-allowed aria-disabled:hover:bg-transparent ${
          active === i ? "bg-raise-2" : ""
        } ${o.disabled ? "opacity-50" : ""}`}
      >
        <span className="truncate text-fg">{o.label}</span>
        {isSelected && <Check size={14} strokeWidth={2} className="shrink-0 text-accent" aria-hidden />}
      </li>,
    );
  });

  return (
    <div ref={wrapRef} className={`relative ${className}`}>
      <button
        type="button"
        ref={triggerRef}
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-controls={open ? listboxId : undefined}
        aria-activedescendant={open && active >= 0 ? `${listboxId}-${active}` : undefined}
        disabled={disabled}
        onClick={() => (open ? close() : openMenu())}
        onKeyDown={(e) => {
          handleKeyDown(e);
          onKeyDown?.(e);
        }}
        className={`${control} flex h-9 cursor-pointer items-center justify-between gap-2 pl-3 pr-2.5 text-left text-sm ${
          open ? "border-accent ring-2 ring-accent/30" : ""
        }`}
        {...rest}
      >
        <span className={`truncate ${selected ? "text-fg" : "text-muted"}`}>
          {selected ? selected.label : placeholder}
        </span>
        <ChevronDown
          size={16}
          strokeWidth={1.75}
          aria-hidden
          className={`shrink-0 text-muted transition-transform duration-200 ${open ? "rotate-180" : ""}`}
        />
      </button>
      {open && (
        <ul
          ref={panelRef}
          id={listboxId}
          role="listbox"
          aria-labelledby={id}
          className={`rise absolute left-0 z-40 w-max min-w-full max-h-72 overflow-y-auto overflow-x-hidden rounded-[var(--radius-md)] border border-line-strong bg-panel p-1 shadow-[var(--shadow-3)] ${
            flipUp ? "bottom-[calc(100%+6px)]" : "top-[calc(100%+6px)]"
          }`}
          style={{ transform: `translateX(${shiftX}px)`, maxWidth: "calc(100vw - 1rem)" }}
        >
          {nodes}
        </ul>
      )}
    </div>
  );
}
