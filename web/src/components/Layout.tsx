import { Suspense, useEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  AudioLines,
  Calculator,
  Captions,
  History,
  Info,
  Languages,
  LayoutDashboard,
  LogOut,
  Menu,
  Mic,
  Monitor,
  Moon,
  NotebookPen,
  PanelLeftClose,
  PanelLeftOpen,
  Podcast,
  Settings,
  SlidersHorizontal,
  Sun,
  Waves,
  Wrench,
  X,
  Scissors,
} from "lucide-react";
import { useTheme, type ThemePref } from "../lib/theme";
import { logout, useMe } from "../lib/auth";
import { usePlayer } from "../lib/player";
import { useWSStatus } from "../lib/ws";
import PlayerBar from "./PlayerBar";
import TaskToasts from "./TaskToasts";
import { IconButton, Skeleton, useToast, ConfirmDialog } from "../ui";

const nav = [
  { to: "/workbench", label: "工作台", desc: "工具总览与最近任务", icon: LayoutDashboard },
  { to: "/tts", label: "语音合成", desc: "同步/流式/长文本三通道", icon: AudioLines },
  { to: "/asr", label: "语音识别", desc: "音频转文字与字幕", icon: Mic },
  { to: "/podcast", label: "播客工坊", desc: "生成双人播客", icon: Podcast },
  { to: "/separate", label: "人声分离", desc: "人声与背景音分轨", icon: Waves },
  { to: "/post", label: "音频后期", desc: "混音台 · 切高潮 · 口播闪避", icon: SlidersHorizontal },
  { to: "/audio-edit", label: "音频剪辑", desc: "切割合并变调与调BPM查询", icon: Scissors },
  { to: "/gsgc", label: "格式工厂", desc: "工具集 HTTP 方案（pcg serve）", icon: Wrench },
  { to: "/translate", label: "机器翻译", desc: "32 语种互译与术语定制", icon: Languages },
  { to: "/minutes", label: "语音妙记", desc: "音视频转结构化纪要", icon: NotebookPen },
  { to: "/subtitles", label: "字幕工坊", desc: "字幕样式与 SRT/ASS 导出", icon: Captions },
  { to: "/history", label: "历史", desc: "全部任务与产物", icon: History },
  { to: "/pricing", label: "计费测算", desc: "刊例价用量估算", icon: Calculator },
  { to: "/settings", label: "设置", desc: "凭证与连接", icon: Settings },
  { to: "/about", label: "关于", desc: "产品与使用指南", icon: Info },
];

const themeOrder: ThemePref[] = ["system", "dark", "light"];

// 侧栏折叠记忆（≥md 生效；<md 一直是抽屉）。手动开关，默认展开。
const NAV_COLLAPSED_KEY = "nav.collapsed";
const readNavCollapsed = () =>
  typeof localStorage !== "undefined" && localStorage.getItem(NAV_COLLAPSED_KEY) === "1";
const themeMeta = {
  system: { icon: Monitor, label: "主题：跟随系统" },
  dark: { icon: Moon, label: "主题：暗色" },
  light: { icon: Sun, label: "主题：亮色" },
};

function HealthIndicator() {
  // 徽标跟随 WS 事件通道状态：WS 经 HTTP 升级建立，"open"即服务可达且实时通道可用，
  // 比轮询 /api/health 更强也更相关（任务进度依赖这条通道）。服务端 ping/pong 保证
  // 状态真实；断了 2 秒自动重连，无需人工处理。
  const status = useWSStatus();
  const meta = {
    open: { label: "实时连接", cls: "text-meter", title: "事件通道已连接，任务进度实时推送" },
    connecting: { label: "连接中", cls: "text-warn", title: "正在建立事件通道" },
    closed: { label: "已断开", cls: "text-danger", title: "事件通道中断，将自动重连" },
  }[status];
  return (
    <span
      className={`hidden items-center gap-1.5 text-[11px] sm:inline-flex ${meta.cls}`}
      title={meta.title}
      role="status"
    >
      <Activity size={13} strokeWidth={1.75} />
      {meta.label}
    </span>
  );
}

/** 账号菜单：头像点击弹下拉（账号信息 + 退出登录）。顶栏唯一账号入口——移动端
 *  (<sm) 不隐藏，退出功能随菜单始终可达。Esc / 点击菜单外关闭（Modal 的 Escape
 *  监听惯例 + pointerdown 外点判定），菜单内点击不关。
 *  退出走二次确认：菜单项只关菜单并回调 onAskLogout；ConfirmDialog 由 Layout 在
 *  根层渲染——顶栏 backdrop-blur 是 fixed 后代的包含块，弹窗放菜单里会被锁进
 *  56px 高的顶栏内居中（出屏裁切）。 */
function UserMenu({ username, role, onAskLogout }: { username: string; role: string; onAskLogout: () => void }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onDown = (e: PointerEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onDown);
    };
  }, [open]);

  const roleLabel = role === "admin" ? "管理员" : "用户";

  return (
    <div ref={wrapRef} className="relative">
      <button
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`账号：${username}（${roleLabel}）`}
        title={`账号：${username}（${roleLabel}）`}
        onClick={() => setOpen((v) => !v)}
        className={`flex size-8 cursor-pointer items-center justify-center rounded-full text-[11px] font-medium transition-colors duration-150 ${
          open ? "bg-accent text-accent-ink" : "bg-raise-2 text-fg-2 hover:bg-raise hover:text-fg"
        }`}
      >
        {username.slice(0, 1).toUpperCase()}
      </button>
      {open && (
        <div
          role="menu"
          aria-label="账号菜单"
          className="rise absolute right-0 top-[calc(100%+10px)] z-30 w-44 overflow-hidden rounded-[var(--radius-md)] border border-line bg-panel backdrop-blur-xl shadow-[var(--shadow-2)]"
        >
          <div className="border-b border-line px-3 py-2.5">
            <p className="truncate text-xs font-medium text-fg">{username}</p>
            <p className="micro mt-0.5">{roleLabel}</p>
          </div>
          <button
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onAskLogout();
            }}
            className="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-xs text-fg-2 transition-colors duration-150 hover:bg-raise-2 hover:text-fg"
          >
            <LogOut size={13} strokeWidth={1.75} />
            退出登录
          </button>
        </div>
      )}
    </div>
  );
}

function NavItems({ onNavigate, collapsed }: { onNavigate?: () => void; collapsed: boolean }) {
  return (
    <>
      {nav.map(({ to, label, icon: Icon }) => (
        <NavLink
          key={to}
          to={to}
          onClick={onNavigate}
          title={collapsed ? label : undefined}
          className={({ isActive }) =>
            `group relative flex items-center gap-3 rounded-[var(--radius-sm)] px-3 py-2 text-sm transition-colors duration-150 ${
              isActive ? "bg-raise text-fg" : "text-fg-2 hover:bg-raise-2 hover:text-fg"
            }`
          }
        >
          {({ isActive }) => (
            <>
              <span
                className={`absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full ${isActive ? "bg-accent" : "bg-transparent"}`}
              />
              <Icon size={18} strokeWidth={1.75} className={isActive ? "text-accent shrink-0" : "shrink-0"} />
              <span className={collapsed ? "hidden" : "hidden truncate lg:inline"}>{label}</span>
            </>
          )}
        </NavLink>
      ))}
    </>
  );
}

export default function Layout() {
  const { pref, setPref } = useTheme();
  const [navOpen, setNavOpen] = useState(false);
  const [navCollapsed, setNavCollapsed] = useState(readNavCollapsed);
  const [logoutConfirm, setLogoutConfirm] = useState(false);
  const hasTrack = usePlayer((s) => Boolean(s.track));
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { toast } = useToast();
  const { data: me } = useMe();
  // 子页面按前缀归属父导航项，面包屑不落到「工作台」
  const current = nav.find((n) => pathname === n.to || pathname.startsWith(n.to + "/")) ?? nav[0];
  const ThemeIcon = themeMeta[pref].icon;

  const cycleTheme = () => setPref(themeOrder[(themeOrder.indexOf(pref) + 1) % themeOrder.length]);

  const toggleCollapsed = () =>
    setNavCollapsed((v) => {
      localStorage.setItem(NAV_COLLAPSED_KEY, v ? "0" : "1");
      return !v;
    });

  const doLogout = async () => {
    try {
      await logout();
    } finally {
      qc.setQueryData(["me"], undefined);
      void qc.removeQueries({ queryKey: ["me"] });
      toast({ tone: "ok", title: "已退出登录" });
      navigate("/login", { replace: true });
    }
  };

  return (
    <div className="flex h-screen">
      {/* 左侧导轨：lg 全宽（可手动收起），md 图标态 */}
      <aside
        className={`hidden shrink-0 flex-col border-r border-line bg-panel backdrop-blur-xl md:flex ${
          navCollapsed ? "md:w-16" : "md:w-16 lg:w-[232px]"
        }`}
      >
        <div className="flex h-14 items-center gap-2.5 border-b border-line px-3 lg:px-4">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-sm)] bg-accent text-accent-ink">
            <AudioLines size={16} strokeWidth={2} />
          </div>
          <span
            className={`text-sm font-semibold tracking-tight ${navCollapsed ? "hidden" : "hidden lg:inline"}`}
          >
            voxbox
          </span>
        </div>
        <nav className="flex flex-col gap-0.5 p-2">
          <NavItems collapsed={navCollapsed} />
        </nav>
        <div className={`mt-auto p-4 ${navCollapsed ? "hidden" : "hidden lg:block"}`}>
          <p className="micro leading-relaxed opacity-70">
            多引擎
            <br />
            语音工作台
          </p>
        </div>
      </aside>

      {/* 窄屏抽屉 */}
      {navOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-[2px]" onClick={() => setNavOpen(false)} />
          <aside className="rise absolute left-0 top-0 h-full w-[248px] border-r border-line bg-panel backdrop-blur-xl">
            <div className="flex h-14 items-center justify-between border-b border-line px-3">
              <span className="text-sm font-semibold">voxbox</span>
              <IconButton label="关闭导航" onClick={() => setNavOpen(false)}>
                <X size={16} strokeWidth={1.75} />
              </IconButton>
            </div>
            <nav className="flex flex-col gap-0.5 p-2">
              <NavItems onNavigate={() => setNavOpen(false)} collapsed={false} />
            </nav>
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 顶栏：只放全局控件与当前位置，页面标题由各页 PageHeader 承担（避免重复）。
            relative z-20：头像下拉菜单溢出顶栏，须整体压过 DOM 在后的 main 内容 */}
        <header className="relative z-20 flex h-14 shrink-0 items-center gap-3 border-b border-line bg-panel px-3 backdrop-blur md:px-6">
          <IconButton label="打开导航" className="md:hidden" onClick={() => setNavOpen(true)}>
            <Menu size={18} strokeWidth={1.75} />
          </IconButton>
          <IconButton
            label={navCollapsed ? "展开侧栏" : "收起侧栏"}
            className="hidden md:inline-flex"
            onClick={toggleCollapsed}
          >
            {navCollapsed ? <PanelLeftOpen size={18} strokeWidth={1.75} /> : <PanelLeftClose size={18} strokeWidth={1.75} />}
          </IconButton>
          <nav aria-label="当前位置" className="micro flex min-w-0 items-center gap-1.5 truncate">
            <span>voxbox</span>
            <span className="text-line-strong">/</span>
            <span className="text-fg-2">{current.label}</span>
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <HealthIndicator />
            <IconButton label={themeMeta[pref].label} onClick={cycleTheme}>
              <ThemeIcon size={16} strokeWidth={1.75} />
            </IconButton>
            {me && <UserMenu username={me.username} role={me.role} onAskLogout={() => setLogoutConfirm(true)} />}
          </div>
        </header>

        <main className={`min-h-0 flex-1 overflow-y-auto ${hasTrack ? "pb-24" : ""}`}>
          <div className="mx-auto max-w-[1100px] px-4 py-6 md:px-8 md:py-8">
            {/* 路由级代码分割的加载边界：Suspense 必须包住 Outlet（只在内容区占位，
                导航壳不闪）；懒页面 chunk 本地加载极快，骨架只在首跳一闪即过 */}
            <Suspense fallback={<PageFallback />}>
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>

      <PlayerBar />
      {me && (
        <ConfirmDialog
          open={logoutConfirm}
          title="退出登录"
          description={`确定要退出 ${me.username} 吗？退出后需要重新登录才能继续使用。`}
          confirmLabel="退出登录"
          tone="danger"
          onConfirm={() => {
            setLogoutConfirm(false);
            doLogout();
          }}
          onCancel={() => setLogoutConfirm(false)}
        />
      )}
      <TaskToasts />
    </div>
  );
}

// PageFallback 懒加载路由的骨架占位：模仿页头+卡片的版式轮廓，
// 高度接近真实页面首屏，避免 chunk 加载瞬间内容区塌陷跳动。
function PageFallback() {
  return (
    <div className="space-y-6" aria-busy="true" aria-label="页面加载中">
      <div className="space-y-2">
        <Skeleton className="h-8 w-56" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
      <Skeleton className="h-72 w-full" />
    </div>
  );
}
