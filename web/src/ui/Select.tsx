import {
  Children,
  isValidElement,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
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

/** 弹层 fixed 定位（相对视口）。portal 到 body 后与触发器脱钩，滚动/缩放需重算。 */
interface PanelPos {
  top: number;
  left: number;
  minWidth: number;
}

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
  const [pos, setPos] = useState<PanelPos | null>(null);
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

  // 面板定位：以下方空间判定向上翻；水平夹取避免溢出视口。
  const place = useCallback(() => {
    const trig = triggerRef.current;
    const panel = panelRef.current;
    if (!trig || !panel) return;
    const rect = trig.getBoundingClientRect();
    const height = Math.min(panel.offsetHeight, PANEL_MAX);
    const below = window.innerHeight - rect.bottom;
    const flipUp = below < height + 12 && rect.top > below;
    const top = flipUp ? Math.max(8, rect.top - height - 6) : rect.bottom + 6;
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - 8 - panel.offsetWidth));
    setPos((p) =>
      p && p.top === top && p.left === left && p.minWidth === rect.width
        ? p
        : { top, left, minWidth: rect.width },
    );
  }, []);

  // 挂载后先于首帧绘制定位（翻转不闪帧）
  useLayoutEffect(() => {
    if (open) place();
  }, [open, place]);

  // fixed 定位与触发器脱钩：页面滚动（含 main 内滚动容器）或窗口缩放时跟随重算。
  // 不用 rAF 节流——place 很便宜且 setPos 值等即跳过，直接算更可靠。
  useEffect(() => {
    if (!open) return;
    document.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      document.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, place]);

  // 活动项滚入可视区
  useEffect(() => {
    if (!open || active < 0) return;
    panelRef.current
      ?.querySelector(`[id="${CSS.escape(`${listboxId}-${active}`)}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [open, active, listboxId]);

  // 点击面板外关闭（面板已 portal 到 body，须单查 panelRef）
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!wrapRef.current?.contains(t) && !panelRef.current?.contains(t)) close();
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
      {open &&
        // portal 到 body：玻璃卡片的 backdrop-filter 会创建 stacking context 并吞掉
        // 后代 z-index——就地渲染的弹层被锁进所在卡片，DOM 在后的玻璃卡（结果区）
        // 会整层盖住它。挂在根层以视口 fixed 定位，永远在最上（同 Modal 的先例）。
        createPortal(
          <ul
            ref={panelRef}
            id={listboxId}
            role="listbox"
            aria-labelledby={id}
            className="rise fixed z-50 w-max max-h-72 overflow-y-auto overflow-x-hidden rounded-[var(--radius-md)] border border-line-strong bg-panel backdrop-blur-xl p-1 shadow-[var(--shadow-3)]"
            style={{
              top: pos?.top ?? 0,
              left: pos?.left ?? 0,
              minWidth: pos?.minWidth,
              maxWidth: "calc(100vw - 1rem)",
              visibility: pos ? "visible" : "hidden",
            }}
          >
            {nodes}
          </ul>,
          document.body,
        )}
    </div>
  );
}
