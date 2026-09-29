import type { ButtonHTMLAttributes } from "react";

export interface SwitchProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange" | "type"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
}

/** 开关：凹槽轨道 + 滑钮（开 = accent 填充 + ink 滑钮）。role=switch，可被 label 包裹做文字联动。 */
export function Switch({ checked, onChange, className = "", ...rest }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border px-0.5 transition-colors duration-150 ${
        checked
          ? "border-accent bg-accent"
          : "border-line-strong bg-inset hover:border-[color-mix(in_oklab,var(--muted)_45%,transparent)]"
      } focus:outline-none focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-50 ${className}`}
      {...rest}
    >
      <span
        aria-hidden="true"
        className={`size-4 rounded-full transition-transform duration-150 ${
          checked ? "translate-x-4 bg-accent-ink" : "translate-x-0 bg-muted"
        }`}
      />
    </button>
  );
}
