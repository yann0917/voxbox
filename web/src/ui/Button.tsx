import type { ButtonHTMLAttributes, ReactNode } from "react";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

const base =
  "inline-flex items-center justify-center gap-2 rounded-[var(--radius-sm)] font-medium " +
  "transition-colors duration-150 cursor-pointer select-none whitespace-nowrap " +
  "disabled:opacity-45 disabled:cursor-not-allowed disabled:pointer-events-none";

const variants: Record<Variant, string> = {
  // 主操作 = 电光青透光：渐变 + 顶缘高光 + 青光晕（.btn-primary 定义于 theme.css）
  primary: "btn-primary text-accent-ink",
  secondary:
    "border border-line-strong text-fg hover:bg-raise-2 hover:border-[color-mix(in_oklab,var(--accent)_45%,transparent)]",
  ghost: "text-fg-2 hover:bg-raise-2 hover:text-fg",
  danger:
    "border border-[color-mix(in_oklab,var(--danger)_40%,transparent)] text-danger hover:bg-[color-mix(in_oklab,var(--danger)_12%,transparent)]",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-xs",
  md: "h-9 px-4 text-sm",
};

function Spinner() {
  return (
    <svg viewBox="0 0 24 24" className="size-3.5 animate-spin" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path d="M21 12a9 9 0 0 0-9-9" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
}

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  icon,
  className = "",
  children,
  disabled,
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={`${base} ${variants[variant]} ${sizes[size]} ${className}`}
    >
      {loading ? <Spinner /> : icon}
      {children}
    </button>
  );
}

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  variant?: Variant;
  size?: Size;
}

export function IconButton({
  label,
  variant = "ghost",
  size = "md",
  className = "",
  children,
  ...rest
}: IconButtonProps) {
  const box = size === "sm" ? "size-8" : "size-9";
  return (
    <button
      {...rest}
      title={label}
      aria-label={label}
      className={`${base} ${variants[variant]} ${box} shrink-0 ${className}`}
    >
      {children}
    </button>
  );
}
