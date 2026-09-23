import { Link } from "react-router-dom";
import {
  AudioLines,
  Captions,
  Cloud,
  Languages,
  Mic,
  NotebookPen,
  Podcast,
  Terminal,
  Waves,
  Wrench,
} from "lucide-react";
import { Button } from "../ui";

/** 能力矩阵（与控制台导航同序同义，公开页只做介绍不拉数据）。 */
const CAPABILITIES = [
  { icon: AudioLines, title: "语音合成", desc: "同步 / 流式 / 长文本三通道，多音色与情感参数" },
  { icon: Mic, title: "语音识别", desc: "一句话秒级转写、录音文件批量识别、分句时间戳" },
  { icon: Podcast, title: "播客工坊", desc: "双人对话稿一键合成播客节目" },
  { icon: Waves, title: "人声分离", desc: "Roformer / Demucs 多引擎，人声与伴奏分轨" },
  { icon: Languages, title: "机器翻译", desc: "32 语种互译，热词与术语表定制" },
  { icon: NotebookPen, title: "语音妙记", desc: "音视频转结构化会议纪要，可导出 Word" },
  { icon: Captions, title: "字幕工坊", desc: "SRT/ASS 解析、分句草稿与卡拉OK样式导出" },
];

const USAGE = [
  {
    icon: Cloud,
    title: "Web 控制台",
    desc: "登录即用：上传音频、提交任务、波形回放与产物下载，进度实时推送。",
    cmd: null,
  },
  {
    icon: Terminal,
    title: "命令行",
    desc: "脚本化批量处理，与 Web 共享任务引擎与历史记录。",
    cmd: `voxbox asr 会议录音.wav --version sentence
voxbox tts "你好世界" --voice zh_female_roushunvsheng`,
  },
  {
    icon: Wrench,
    title: "MCP 接入",
    desc: "标准 Streamable HTTP 端点，15 个工具直接挂进 Claude 等 MCP 客户端。",
    cmd: `"mcpServers": {
  "voxbox": { "url": "https://your-host/api/mcp",
    "headers": { "Authorization": "Bearer tbx_****" } }
}`,
  },
];

/** 产品展示页（公开路由 /）：介绍能力与接入方式，登录入口在右上角。 */
export default function LandingPage() {
  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* 顶栏 */}
      <header className="sticky top-0 z-10 border-b border-line bg-panel/80 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-[1100px] items-center gap-3 px-4 md:px-8">
          <div className="flex items-center gap-2.5">
            <div className="flex size-7 items-center justify-center rounded-[var(--radius-sm)] bg-accent text-accent-ink">
              <AudioLines size={16} strokeWidth={2} />
            </div>
            <span className="text-sm font-semibold tracking-tight">voxbox</span>
          </div>
          <span className="micro ml-2 hidden sm:inline">SELF-HOSTED AUDIO TOOLKIT</span>
          <div className="ml-auto">
            <Link to="/workbench">
              <Button variant="primary">进入控制台</Button>
            </Link>
          </div>
        </div>
      </header>

      {/* Hero */}
      <section className="relative overflow-hidden border-b border-line">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 -top-24 h-64 opacity-60"
          style={{ background: "radial-gradient(60% 100% at 50% 0%, rgba(34,211,238,0.12), transparent)" }}
        />
        <div className="mx-auto max-w-[1100px] px-4 py-20 md:px-8 md:py-28">
          <p className="micro mb-4 text-accent">一键部署 · 单二进制 · 数据自有</p>
          <h1 className="max-w-3xl text-3xl font-semibold leading-tight tracking-tight md:text-5xl md:leading-[1.15]">
            语音工具箱，
            <br className="sm:hidden" />
            像一间深空信号站一样可靠
          </h1>
          <p className="mt-5 max-w-2xl text-sm leading-relaxed text-fg-2 md:text-base">
            合成、识别、分离、翻译、妙记、字幕——火山引擎语音能力的完整自托管工作台。
            一个二进制跑在服务器或本机，任务引擎与产物全在你的磁盘上，Agent 经 MCP 直接调用。
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Link to="/workbench">
              <Button>进入控制台</Button>
            </Link>
            <a href="#usage">
              <Button variant="secondary">查看接入方式</Button>
            </a>
          </div>
          <dl className="mt-12 grid max-w-2xl grid-cols-3 gap-6">
            {[
              ["33", "内置工具"],
              ["3", "调用入口 Web / CLI / MCP"],
              ["0", "外部服务依赖*"],
            ].map(([v, k]) => (
              <div key={k}>
                <dt className="sr-only">{k}</dt>
                <dd className="font-mono text-2xl font-medium tabular-nums text-fg md:text-3xl">{v}</dd>
                <dd className="mt-1 text-[11px] leading-snug text-muted">{k}</dd>
              </div>
            ))}
          </dl>
          <p className="mt-3 text-[10px] text-muted">* SQLite 存储，纯 Go 驱动；语音能力由你配置的火山引擎凭证计费。</p>
        </div>
      </section>

      {/* 能力矩阵 */}
      <section className="mx-auto max-w-[1100px] px-4 py-16 md:px-8 md:py-20">
        <p className="micro mb-2 text-muted">CAPABILITIES</p>
        <h2 className="text-xl font-semibold tracking-tight md:text-2xl">八大能力，一套工作流</h2>
        <div className="mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {CAPABILITIES.map(({ icon: Icon, title, desc }) => (
            <div
              key={title}
              className="group rounded-[var(--radius-md)] border border-line bg-raise p-4 transition-colors duration-200 hover:border-line-strong"
            >
              <div className="flex size-9 items-center justify-center rounded-[var(--radius-sm)] bg-raise-2 text-accent">
                <Icon size={17} strokeWidth={1.75} />
              </div>
              <p className="mt-3 text-sm font-medium">{title}</p>
              <p className="mt-1.5 text-xs leading-relaxed text-fg-2">{desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* 接入方式 */}
      <section id="usage" className="border-y border-line bg-panel/40">
        <div className="mx-auto max-w-[1100px] px-4 py-16 md:px-8 md:py-20">
          <p className="micro mb-2 text-muted">INTERFACES</p>
          <h2 className="text-xl font-semibold tracking-tight md:text-2xl">三种入口，同一个引擎</h2>
          <div className="mt-8 grid gap-4 lg:grid-cols-3">
            {USAGE.map(({ icon: Icon, title, desc, cmd }) => (
              <div key={title} className="rounded-[var(--radius-md)] border border-line bg-raise p-5">
                <div className="flex items-center gap-2.5">
                  <Icon size={16} strokeWidth={1.75} className="text-accent" />
                  <p className="text-sm font-medium">{title}</p>
                </div>
                <p className="mt-2.5 min-h-[3.5rem] text-xs leading-relaxed text-fg-2">{desc}</p>
                {cmd && (
                  <code className="mt-3 block overflow-x-auto whitespace-pre rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 font-mono text-[11px] leading-relaxed text-fg-2">
                    {cmd}
                  </code>
                )}
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* 页脚 */}
      <footer className="mx-auto max-w-[1100px] px-4 py-10 md:px-8">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-[11px] text-muted">
          <span>voxbox · 自托管语音工作台</span>
          <span className="hidden sm:inline">·</span>
          <span>由火山引擎语音大模型驱动</span>
        </div>
      </footer>
    </div>
  );
}
