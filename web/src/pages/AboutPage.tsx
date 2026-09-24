import { Link } from "react-router-dom";
import {
  ArrowUpRight,
  BookOpenText,
  Coins,
  Cpu,
  ExternalLink,
  Info,
  KeyRound,
  Monitor,
  PlugZap,
  Terminal,
} from "lucide-react";
import { PRICE_SNAPSHOT_DATE } from "../lib/pricing";
import { Card, CardBody, CardHeader, MicroLabel, PageHeader } from "../ui";

/** 官方文档外链（产品介绍与计费口径来源） */
const OFFICIAL_DOCS = [
  { label: "豆包语音产品简介", url: "https://docs.volcengine.com/docs/6561/163032" },
  { label: "计费概述", url: "https://www.volcengine.com/docs/6561/1359369" },
  { label: "计费说明", url: "https://www.volcengine.com/docs/6561/1359370" },
];

/** 快速上手三步 */
function Step({ n, title, children }: { n: string; title: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3">
      <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border border-line bg-raise-2 font-mono text-[11px] tabular-nums text-accent">
        {n}
      </span>
      <div className="min-w-0 space-y-1.5">
        <p className="text-sm font-medium text-fg">{title}</p>
        <div className="space-y-1.5 text-[13px] leading-relaxed text-fg-2">{children}</div>
      </div>
    </div>
  );
}

function Cmd({ children }: { children: string }) {
  return (
    <code className="block overflow-x-auto whitespace-pre rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 font-mono text-xs leading-relaxed text-fg-2">
      {children}
    </code>
  );
}

/** 关于页：产品介绍、能力一览、使用方法与底层模型说明（纯内容页）。 */
export default function AboutPage() {
  return (
    <>
      <PageHeader
        title="关于"
        description="voxbox · 个人自用的多媒体 AI 工作台"
        icon={<Info size={16} strokeWidth={1.75} />}
        actions={
          <Link
            to="/history"
            className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
          >
            历史产物
            <ArrowUpRight size={13} strokeWidth={1.75} />
          </Link>
        }
      />

      {/* 产品介绍 */}
      <Card className="mb-4">
        <CardBody className="space-y-3">
          <p className="text-sm leading-relaxed text-fg">
            voxbox 是一套个人自用的多媒体 AI 工具箱：单个 Go 二进制，既是命令行工具也是 Web 控制台。
            它把火山引擎豆包语音的八项 AI 能力装进同一个任务引擎——提交任务、实时进度、产物落盘、历史可溯，
            面向配音、转写、播客、会议纪要、翻译等日常内容生产场景。
          </p>
          <p className="text-sm leading-relaxed text-fg-2">
            命令行面向脚本与 agent（<code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">--json</code> 输出机器可读结果，
            退出码区分成功 / 参数 / 失败 / 凭证）；Web 控制台按「暖调工作室」的调性设计，
            提供波形试听、计费测算与任务历史。两者共享同一份数据目录与任务记录。
          </p>
        </CardBody>
      </Card>

      {/* 快速上手 */}
      <Card className="mb-4">
        <CardHeader title="快速上手" icon={<Terminal size={15} strokeWidth={1.75} />} aside={<span className="micro">CLI 与 Web 同源</span>} />
        <CardBody className="space-y-5">
          <Step n="1" title="配置凭证">
            <p>
              在 <Link to="/settings" className="text-accent transition-colors duration-150 hover:opacity-80">设置页</Link> 填写凭证，
              或执行（配置文件位于 <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">~/.voxbox/config.yaml</code>）：
            </p>
            <Cmd>voxbox config set volc.speech.app_id &lt;APP ID&gt;&#10;voxbox config set volc.speech.access_token &lt;Token&gt;</Cmd>
            <p className="text-xs text-muted">
              <KeyRound size={12} strokeWidth={1.75} className="mr-1 inline" />
              仅播客必须 APP ID + Access Token，其余能力支持新版 API Key 单键；人声分离使用独立的 MediaKit API Key。
            </p>
          </Step>
          <Step n="2" title="命令行调用">
            <p>
              所有命令同步执行、进程退出即完成；加 <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">--json</code> 获得机器可读产物路径。
            </p>
            <Cmd>voxbox tts "你好，voxbox" --out hello.mp3 --json&#10;voxbox minutes "https://example.com/meeting.mp4" --features summary,todo --json</Cmd>
            <p className="text-xs text-muted">
              完整命令与参数见
              <a
                href="https://github.com/yann0917/voxbox/blob/main/skills/voxbox/references/cli.md"
                target="_blank"
                rel="noreferrer"
                className="mx-1 text-accent transition-colors duration-150 hover:opacity-80"
              >
                CLI 完整参考
              </a>
              ，或 <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">voxbox &lt;命令&gt; --help</code>。
            </p>
          </Step>
          <Step n="3" title="Web 控制台">
            <Cmd>voxbox serve --port 8081</Cmd>
            <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
              <Monitor size={12} strokeWidth={1.75} className="mr-1 inline" />
              浏览器打开 http://127.0.0.1:8081 —— 各工具页交互、试听与历史；同量费用对比见
              <Link to="/pricing" className="text-accent transition-colors duration-150 hover:opacity-80">
                计费测算
              </Link>
              （刊例快照 {PRICE_SNAPSHOT_DATE}，以账单为准）。
            </p>
          </Step>
        </CardBody>
      </Card>

      {/* MCP 接入 */}
      <Card className="mb-4">
        <CardHeader
          title="MCP 接入"
          icon={<PlugZap size={15} strokeWidth={1.75} />}
          aside={<span className="micro">12 个工具 · 串行排队</span>}
        />
        <CardBody className="space-y-5">
          <p className="text-sm leading-relaxed text-fg-2">
            voxbox 可作为 MCP server 接入支持 MCP 的客户端（Claude Code、Cline 等），
            与 CLI / Web 共享同一任务引擎与数据目录。
            <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">voxbox serve</code> 内嵌
            Streamable HTTP 端点（Bearer token 鉴权，可经反代安全暴露公网，部署步骤见仓库
            <code className="mx-1 rounded bg-inset px-1.5 py-0.5 font-mono text-xs">deploy/README.md</code>）；
            <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">voxbox mcp</code> 为 stdio 模式，由客户端拉起子进程。
          </p>
          <Step n="A" title="HTTP 传输（复用已运行的 serve，与 Web 共享任务引擎）">
            <p>
              先在<Link to="/settings" className="mx-1 text-accent transition-colors duration-150 hover:opacity-80">设置页</Link>
              生成 API Token（明文仅显示一次），请求头携带
              <code className="mx-1 rounded bg-inset px-1.5 py-0.5 font-mono text-xs">Authorization: Bearer tbx_…</code>：
            </p>
            <Cmd>{`{
  "mcpServers": {
    "voxbox": {
      "url": "http://127.0.0.1:8081/api/mcp",
      "headers": { "Authorization": "Bearer tbx_xxxxxxxx" }
    }
  }
}`}</Cmd>
            <p className="text-xs text-muted">
              token 缺失或无效返回 401（JSON-RPC 错误体）；重置后旧 token 立即失效。
            </p>
          </Step>
          <Step n="B" title="stdio 传输（客户端拉起子进程，最稳）">
            <Cmd>{`{
  "mcpServers": {
    "voxbox": {
      "command": "/path/to/voxbox",
      "args": ["mcp"]
    }
  }
}`}</Cmd>
            <p className="text-xs text-muted">
              同机 stdio 信任本机用户，无需 API token。同一数据目录下不要同时运行 stdio 与
              serve，会争 SQLite 写锁报 <code className="rounded bg-inset px-1.5 py-0.5 font-mono text-xs">database is locked</code>。
            </p>
          </Step>
        </CardBody>
      </Card>

      {/* 底层模型（产品介绍） */}
      <Card>
        <CardHeader
          title="底层能力：火山引擎豆包语音"
          icon={<Cpu size={15} strokeWidth={1.75} />}
          aside={<span className="micro">大模型体系</span>}
        />
        <CardBody className="space-y-3">
          <p className="text-sm leading-relaxed text-fg-2">
            全部能力由火山引擎豆包语音大模型体系驱动。官方产品线覆盖音频创作（Seed-Audio，单条 Prompt 生成影视级多轨音频）、
            语音合成（多情感高表现力 TTS）、声音复刻（少量样本克隆音色）、语音识别（高准确率转写）、
            语音播客（多角色对谈生成）、语音同传、语音妙记（会议转写与智能纪要）与机器翻译——
            voxbox 按个人工作流挑选并组合了其中八项，统一封装为本地工具；音色列表、能力边界与计费口径以官方文档为准。
          </p>
          <div className="space-y-1.5">
            <MicroLabel className="inline-flex items-center gap-1">
              <BookOpenText size={12} strokeWidth={1.75} />
              官方文档
            </MicroLabel>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
              {OFFICIAL_DOCS.map((d) => (
                <a
                  key={d.url}
                  href={d.url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
                >
                  {d.label}
                  <ExternalLink size={12} strokeWidth={1.75} />
                </a>
              ))}
              <Link
                to="/pricing"
                className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
              >
                <Coins size={12} strokeWidth={1.75} />
                本地计费测算
              </Link>
            </div>
          </div>
        </CardBody>
      </Card>
    </>
  );
}
