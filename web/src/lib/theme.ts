import { create } from "zustand";

export type ThemePref = "system" | "dark" | "light";
export type ResolvedTheme = "dark" | "light";

const KEY = "theme";
const mq = () => window.matchMedia("(prefers-color-scheme: dark)");

export function readPref(): ThemePref {
  const raw = localStorage.getItem(KEY);
  return raw === "dark" || raw === "light" || raw === "system" ? raw : "system";
}

function resolve(pref: ThemePref): ResolvedTheme {
  if (pref === "system") return mq().matches ? "dark" : "light";
  return pref;
}

function apply(resolved: ResolvedTheme) {
  document.documentElement.classList.toggle("light", resolved === "light");
}

interface ThemeState {
  pref: ThemePref;
  resolved: ResolvedTheme;
  setPref: (next: ThemePref) => void;
}

// 单一主题源（zustand）：类名应用与组件生命周期解耦——此前只有 Layout 挂载时才 apply，
// 退出登录到公开页后 <html> 上的 light 类没人摘，公开页停在旧主题、刷新才回默认。
// 模块加载即应用一次：任意路由（含展示页/登录页）首帧就是正确主题。
const initial = readPref();
apply(resolve(initial));

const useThemeStore = create<ThemeState>((set) => ({
  pref: initial,
  resolved: resolve(initial),
  setPref: (next) => {
    localStorage.setItem(KEY, next);
    const r = resolve(next);
    apply(r);
    set({ pref: next, resolved: r });
  },
}));

// 跟随系统时，系统切换要即时响应（与组件挂载无关，全局监听一份）
if (typeof window !== "undefined") {
  mq().addEventListener("change", () => {
    const { pref } = useThemeStore.getState();
    if (pref !== "system") return;
    const r = resolve("system");
    apply(r);
    useThemeStore.setState({ resolved: r });
  });
}

/** 主题偏好：system 跟随系统，dark/light 手动指定，localStorage 持久化。 */
export function useTheme() {
  return useThemeStore();
}
