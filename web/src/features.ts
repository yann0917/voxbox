import type { ComponentType } from "react";
import type { LucideIcon } from "lucide-react";
import {
  AudioLines,
  AudioWaveform,
  Calculator,
  Captions,
  History,
  Info,
  Languages,
  LayoutDashboard,
  LibraryBig,
  Mic,
  NotebookPen,
  Podcast,
  Scissors,
  Settings,
  SlidersHorizontal,
  Waves,
  Wrench,
} from "lucide-react";

/** 功能清单条目:路由 / 侧栏 / 面包屑 / 工作台卡 / 工具中文名的唯一事实源。
 *  新增功能 = 新建页面组件 + 在 features 里加一条,路由、侧栏、工作台卡、
 *  工具中文名与重跑跳转随条目自动出现——不再改 main.tsx / Layout.tsx /
 *  toolNames.ts / WorkbenchPage 四处。 */
export interface FeatureDef {
  id: string;
  path: string;
  title: string;
  desc: string;
  icon: LucideIcon;
  /** 路由级代码分割的组件工厂(main.tsx 模块级 lazy 一次) */
  component: () => Promise<{ default: ComponentType }>;
  /** 产出任务链工具的声明:历史筛选/任务通知/重跑跳转的显示名与路由(toolNames 派生源) */
  tools?: { name: string; label: string; route: string }[];
  /** 工作台快捷卡(tool 为最近任务的计数键);name/desc 缺省回落 title/desc——
   *  现有卡面文案与侧栏不同处,以显式覆盖保留 */
  workbench?: { tool: string; name?: string; desc?: string };
}

/** 应用功能清单,顺序即侧栏顺序(公开页 Landing/登录不在此列,归 main.tsx)。 */
export const features: FeatureDef[] = [
  {
    id: "workbench",
    path: "/workbench",
    title: "工作台",
    desc: "工具总览与最近任务",
    icon: LayoutDashboard,
    component: () => import("./pages/WorkbenchPage"),
  },
  {
    id: "tts",
    path: "/tts",
    title: "语音合成",
    desc: "同步/流式/长文本三通道",
    icon: AudioLines,
    component: () => import("./pages/TTSPage"),
    tools: [
      { name: "tts", label: "语音合成", route: "/tts" },
      { name: "tts_long", label: "长文本合成", route: "/tts?tab=long" },
      { name: "tts_stream", label: "流式合成", route: "/tts?tab=stream" },
    ],
    workbench: { tool: "tts", desc: "同步/流式/长文本三通道，按费用选" },
  },
  {
    id: "asr",
    path: "/asr",
    title: "语音识别",
    desc: "音频转文字与字幕",
    icon: Mic,
    component: () => import("./pages/ASRPage"),
    tools: [{ name: "asr", label: "语音识别", route: "/asr" }],
    workbench: { tool: "asr", desc: "音频转文字，分句时间戳与字幕" },
  },
  {
    id: "live",
    path: "/live",
    title: "实时语音识别",
    desc: "边说边出字与一键存纪要",
    icon: AudioWaveform,
    component: () => import("./pages/LivePage"),
  },
  {
    id: "podcast",
    path: "/podcast",
    title: "播客工坊",
    desc: "生成双人播客",
    icon: Podcast,
    component: () => import("./pages/PodcastPage"),
    tools: [{ name: "podcast", label: "播客工坊", route: "/podcast" }],
    workbench: { tool: "podcast", desc: "生成双人对话播客" },
  },
  {
    id: "separate",
    path: "/separate",
    title: "人声分离",
    desc: "人声与背景音分轨",
    icon: Waves,
    component: () => import("./pages/SeparatePage"),
    tools: [{ name: "separate", label: "人声分离", route: "/separate" }],
    workbench: { tool: "separate", desc: "人声与背景音分轨输出" },
  },
  {
    id: "post",
    path: "/post",
    title: "音频后期",
    desc: "混音台 · 切高潮 · 口播闪避",
    icon: SlidersHorizontal,
    component: () => import("./pages/post/PostPage"),
  },
  {
    id: "audio-edit",
    path: "/audio-edit",
    title: "音频剪辑",
    desc: "切割合并变调与乐调 BPM 查询",
    icon: Scissors,
    component: () => import("./pages/AudioEditorPage"),
    tools: [
      { name: "trim", label: "音频切割", route: "/audio-edit" },
      { name: "merge", label: "音频合并", route: "/audio-edit" },
      { name: "pitch", label: "变调变速", route: "/audio-edit" },
      { name: "analyze", label: "调与BPM查询", route: "/audio-edit" },
      { name: "equalizer", label: "均衡器", route: "/audio-edit" },
      { name: "volume", label: "音量与响度", route: "/audio-edit" },
      { name: "fade", label: "淡入淡出", route: "/audio-edit" },
      { name: "reverse", label: "倒放", route: "/audio-edit" },
    ],
    workbench: { tool: "trim", desc: "切割/合并/变调/调BPM查询" },
  },
  {
    id: "gsgc",
    path: "/gsgc",
    title: "格式工厂",
    desc: "音视频/图片在线转换与压缩",
    icon: Wrench,
    component: () => import("./pages/GsgcPage"),
  },
  {
    id: "translate",
    path: "/translate",
    title: "机器翻译",
    desc: "32 语种互译与术语定制",
    icon: Languages,
    component: () => import("./pages/TranslatePage"),
    tools: [{ name: "translate", label: "机器翻译", route: "/translate" }],
    workbench: { tool: "translate", desc: "32 语种互译，术语定制" },
  },
  {
    id: "minutes",
    path: "/minutes",
    title: "语音妙记",
    desc: "音视频转结构化纪要",
    icon: NotebookPen,
    component: () => import("./pages/MinutesPage"),
    tools: [{ name: "minutes", label: "语音妙记", route: "/minutes" }],
    workbench: { tool: "minutes", desc: "音视频转纪要：总结/待办/章节" },
  },
  {
    id: "subtitles",
    path: "/subtitles",
    title: "字幕工坊",
    desc: "字幕样式与 SRT/ASS 导出",
    icon: Captions,
    component: () => import("./pages/SubtitlesPage"),
  },
  {
    id: "prompts",
    path: "/prompts",
    title: "提示词库",
    desc: "AI 写作主题与自定义提示词",
    icon: LibraryBig,
    component: () => import("./pages/PromptLibraryPage"),
  },
  {
    id: "history",
    path: "/history",
    title: "任务",
    desc: "提交记录与产物",
    icon: History,
    component: () => import("./pages/HistoryPage"),
  },
  {
    id: "pricing",
    path: "/pricing",
    title: "计费测算",
    desc: "刊例价用量估算",
    icon: Calculator,
    component: () => import("./pages/PricingPage"),
  },
  {
    id: "settings",
    path: "/settings",
    title: "设置",
    desc: "凭证与连接",
    icon: Settings,
    component: () => import("./pages/SettingsPage"),
  },
  {
    id: "about",
    path: "/about",
    title: "关于",
    desc: "产品与使用指南",
    icon: Info,
    component: () => import("./pages/AboutPage"),
  },
];
