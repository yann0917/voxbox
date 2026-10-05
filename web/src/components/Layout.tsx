import { Suspense, useEffect, useLayoutEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  LogOut,
  Menu,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Sun,
  X,
} from "lucide-react";
import { useTheme, type ThemePref } from "../lib/theme";
import { logout, useMe } from "../lib/auth";
import { usePlayer } from "../lib/player";
import { useWSStatus } from "../lib/ws";
import { features } from "../features";
import PlayerBar from "./PlayerBar";
import TaskToasts from "./TaskToasts";
import AssistantWidget from "./AssistantWidget";
import BrandMark from "./BrandMark";
import { IconButton, Skeleton, useToast, ConfirmDialog } from "../ui";

// 侧栏即功能清单(features.ts):路由/标题/图标随清单派生,新增功能不用改本文件。
const nav = features;

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
  // 状态真实；断了 2 秒自动重连，无需人工处理。颜色即语义（信号绿=连接/黄=连接中/
  // 红=断开），文字说明只留在悬浮提示与无障碍标签里。连接灯用 meter-vivid 而非
  // 苔藓绿：状态灯需要从系统色中跳出（MASTER.md 声明的唯一豁免处）。
  const status = useWSStatus();
  const meta = {
    open: { cls: "text-meter-vivid", title: "事件通道已连接，任务进度实时推送" },
    connecting: { cls: "text-warn", title: "正在建立事件通道" },
    closed: { cls: "text-danger", title: "事件通道中断，将自动重连" },
  }[status];
  return (
    <span
      className={`hidden sm:inline-flex ${meta.cls}`}
      title={meta.title}
      aria-label={meta.title}
      role="status"
    >
      <Activity size={13} strokeWidth={1.75} />
    </span>
  );
}

/** 账号菜单：头像点击弹下拉（账号信息 + 退出登录）。顶栏唯一账号入口——移动端
 *  (<sm) 不隐藏，浏览器形态下退出功能随菜单始终可达；桌面形态下整个菜单不渲染
 *  （自动登录、无账号概念），退出项无需再按形态分支。
 *  Esc / 点击菜单外关闭（Modal 的 Escape
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

function NavItems({
  onNavigate,
  collapsed,
  activeIdx,
}: {
  onNavigate?: () => void;
  collapsed: boolean;
  activeIdx: number;
}) {
  // 选中胶囊：单个共享元素承载选中底色与左侧强调条，切换时 translateY 滑到新项。
  // 首次挂载直接插入到位（新插入元素不跑过渡），只有后续切换才产生滑动；
  // 位移量取自 NavLink 实测 offsetTop，项高固定（text-sm 行高 20px + py-2）无漂移。
  const itemRefs = useRef<(HTMLAnchorElement | null)[]>([]);
  const [pill, setPill] = useState<{ top: number; height: number } | null>(null);

  useLayoutEffect(() => {
    const el = itemRefs.current[activeIdx];
    if (el) setPill({ top: el.offsetTop, height: el.offsetHeight });
  }, [activeIdx, collapsed]);

  return (
    <div className="relative flex flex-col gap-0.5">
      {pill && (
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 top-0 rounded-[var(--radius-sm)] bg-raise transition-transform duration-[320ms] ease-out-quint"
          style={{ transform: `translateY(${pill.top}px)`, height: pill.height }}
        >
          <span className="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-accent" />
        </div>
      )}
      {nav.map(({ path, title, icon: Icon }, i) => (
        <NavLink
          key={path}
          ref={(el) => {
            itemRefs.current[i] = el;
          }}
          to={path}
          onClick={onNavigate}
          title={collapsed ? title : undefined}
          className={`relative flex items-center gap-3 rounded-[var(--radius-sm)] px-3 py-2 text-sm transition-colors duration-150 ${
            i === activeIdx ? "text-fg" : "text-fg-2 hover:bg-raise-2 hover:text-fg"
          }`}
        >
          <Icon size={18} strokeWidth={1.75} className={i === activeIdx ? "text-accent shrink-0" : "shrink-0"} />
          <span className={collapsed ? "hidden" : "hidden truncate lg:inline"}>{title}</span>
        </NavLink>
      ))}
    </div>
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
  const current = nav.find((n) => pathname === n.path || pathname.startsWith(n.path + "/")) ?? nav[0];
  const activeIdx = nav.indexOf(current);
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
            {/* 品牌标记（环形声纹）：小尺寸下按视觉权重加粗笔画（44→72/1024），与 lucide 线宽观感对齐 */}
            <BrandMark size={18} strokeWidth={72} />
          </div>
          <span
            className={`text-sm font-semibold tracking-tight ${navCollapsed ? "hidden" : "hidden lg:inline"}`}
          >
            voxbox
          </span>
        </div>
        <nav className="p-2">
          <NavItems collapsed={navCollapsed} activeIdx={activeIdx} />
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
            <nav className="p-2">
              <NavItems onNavigate={() => setNavOpen(false)} collapsed={false} activeIdx={activeIdx} />
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
            <span className="text-fg-2">{current.title}</span>
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <HealthIndicator />
            <IconButton label={themeMeta[pref].label} onClick={cycleTheme}>
              <ThemeIcon size={16} strokeWidth={1.75} />
            </IconButton>
            {/* 桌面形态自动登录、无账号概念，账号菜单整体不渲染 */}
            {me && !me.desktop && (
              <UserMenu username={me.username} role={me.role} onAskLogout={() => setLogoutConfirm(true)} />
            )}
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
      <AssistantWidget />
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
