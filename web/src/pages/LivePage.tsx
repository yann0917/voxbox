import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  AudioWaveform,
  Captions,
  CircleAlert,
  FileText,
  Mic,
  Save,
  Square,
} from "lucide-react";
import { useMe } from "../lib/auth";
import {
  captureSupport,
  micErrorMessage,
  startLiveCapture,
  type LiveCaptureSession,
} from "../lib/liveCapture";
import { LiveClient, type LiveEngine, type LiveServerMsg } from "../lib/liveClient";
import {
  applyLiveFinal,
  applyLivePartial,
  captionHasText,
  emptyLiveCaption,
  formatElapsed,
  groupConsecutiveSpeaker,
  speakerLabel,
  type LiveCaptionState,
} from "../lib/liveCaption";
import { useModels } from "../lib/models";
import { DictFill } from "../components/DictFill";
import { Button, Card, CardBody, CardHeader, EmptyState, Input, PageHeader, Select, Switch, Tabs, useToast } from "../ui";

type Phase = "idle" | "connecting" | "streaming" | "finishing" | "final";

const ENGINE_TABS = [
  { value: "volcengine" as LiveEngine, label: "火山引擎" },
  { value: "local" as LiveEngine, label: "本地识别", tag: "实验" },
];

export default function LivePage() {
  const navigate = useNavigate();
  const { toast } = useToast();
  // 桌面形态：麦克风授权走系统设置而非浏览器地址栏，被拒后的指引文案据此分流
  const { data: me } = useMe();
  const [engine, setEngine] = useState<LiveEngine>("volcengine");
  const [hotwords, setHotwords] = useState("");
  const [speaker, setSpeaker] = useState(false);
  // 本地引擎语种：空=自动检测。R2T2 自动检测在数字、短句等无上下文内容上不稳定
  // （同一句数字时中时英），固定语种经 prompt 强制后即稳。
  const [language, setLanguage] = useState("");
  const [phase, setPhase] = useState<Phase>("idle");
  const [caption, setCaption] = useState<LiveCaptionState>(emptyLiveCaption);
  const [finalInfo, setFinalInfo] = useState<{ degraded: boolean; durationMs: number } | null>(null);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [error, setError] = useState("");
  const [warn, setWarn] = useState("");
  const [saving, setSaving] = useState(false);

  const clientRef = useRef<LiveClient | null>(null);
  const captureRef = useRef<LiveCaptureSession | null>(null);
  const startedAtRef = useRef(0);
  const levelRef = useRef<HTMLDivElement>(null);
  const captionBoxRef = useRef<HTMLDivElement>(null);
  // 回调闭包只读 ref：WS 消息随时序到达，不依赖渲染期快照
  const phaseRef = useRef<Phase>("idle");
  const hasTextRef = useRef(false);
  const setPhaseSafe = (p: Phase) => {
    phaseRef.current = p;
    setPhase(p);
  };

  // 本地实时识别依赖 Confucius4-R2T2（目录 asr 条目 confucius4_r2t2 家族已安装）
  const { data: modelsData, isLoading: modelsLoading } = useModels();
  const r2t2Installed =
    modelsData?.items.some(
      (m) => m.kind === "asr" && m.family === "confucius4_r2t2" && m.status === "installed",
    ) ?? false;
  const capture = captureSupport();

  /* 计时：只在会话进行中走表，停止后冻结（火山的计费口径即此刻时长） */
  useEffect(() => {
    if (phase !== "streaming") return;
    const timer = window.setInterval(() => setElapsedMs(Date.now() - startedAtRef.current), 250);
    return () => window.clearInterval(timer);
  }, [phase]);

  /* 电平条走 rAF 直改 DOM，不进 React 渲染 */
  useEffect(() => {
    if (phase !== "streaming") return;
    let raf = 0;
    const tick = () => {
      if (levelRef.current && captureRef.current) {
        levelRef.current.style.width = `${Math.round(captureRef.current.level() * 100)}%`;
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [phase]);

  /* 字幕跟读：仅在阅读位置贴近底部（距底 <80px）时跟随贴底，回看历史内容不抢滚动 */
  useEffect(() => {
    const el = captionBoxRef.current;
    if (!el) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 80) el.scrollTop = el.scrollHeight;
  }, [caption, phase]);

  /* 离开页面：停采集、断会话通道（close 幂等） */
  useEffect(
    () => () => {
      captureRef.current?.stop();
      captureRef.current = null;
      clientRef.current?.close();
      clientRef.current = null;
    },
    [],
  );

  const resetOutputs = () => {
    setCaption(emptyLiveCaption);
    setFinalInfo(null);
    setElapsedMs(0);
    hasTextRef.current = false;
  };

  /** 会话中断收束（开始失败/连接断开）：回到初始态，保留提示文案 */
  const teardown = (message: string) => {
    captureRef.current?.stop();
    captureRef.current = null;
    clientRef.current?.close();
    clientRef.current = null;
    setWarn("");
    setPhaseSafe("idle");
    setError(message);
  };

  const handleError = (message: string) => {
    setSaving(false); // 保存被拒等场景：按钮退出加载态，提示紧随其后
    const ph = phaseRef.current;
    if ((ph === "streaming" || ph === "finishing") && hasTextRef.current) {
      // 会话中报错且已有内容（音频推送受阻一类）：会话可能仍可收束，
      // 停在原状态让用户停止后保存已说内容；连接真断由 onClose 兜底
      setWarn(message);
      return;
    }
    if (ph === "streaming" || ph === "connecting") {
      teardown(message);
      return;
    }
    if (ph === "finishing") {
      // 等待定格中：先记下提示，连接若断由 onClose 收口
      setWarn(message);
      return;
    }
    setError(message); // 初始/定格态（保存被拒等）
  };

  const handleMsg = (m: LiveServerMsg) => {
    switch (m.type) {
      case "ready":
        break; // 会话确认；音频随开始指令入队即传，无需等应答
      case "partial":
        if (phaseRef.current === "final") break; // 定格后的残余增量不覆盖定格文本（防御服务端排序回归）
        if ((m.committed ?? "").length > 0 || (m.unstable ?? "").length > 0) hasTextRef.current = true;
        setCaption((c) => applyLivePartial(c, m));
        break;
      case "final": {
        captureRef.current?.stop(); // 残余采集不再上送
        captureRef.current = null;
        if ((m.text ?? "").length > 0) hasTextRef.current = true;
        setCaption((c) => applyLiveFinal(c, m));
        setFinalInfo({ degraded: m.degraded === true, durationMs: m.duration_ms ?? 0 });
        setPhaseSafe("final");
        break;
      }
      case "saved":
        toast({ tone: "ok", title: "已存入历史", description: "正在打开任务详情" });
        clientRef.current?.close();
        clientRef.current = null;
        navigate(`/history?task=${m.task_id}`);
        break;
      case "error":
        handleError(m.message);
        break;
    }
  };

  const handleClose = () => {
    clientRef.current = null;
    const ph = phaseRef.current;
    if (ph === "idle" || ph === "final") return; // 初始态/定格后的正常断开不打扰
    captureRef.current?.stop();
    captureRef.current = null;
    setWarn("");
    setPhaseSafe("idle");
    setError((prev) => prev || "实时字幕连接已断开，请重新开始");
  };

  const startSession = async () => {
    if (!capture.ok) {
      setError("当前环境不支持使用麦克风（需要 https 或 localhost）");
      return;
    }
    if (engine === "local" && !r2t2Installed) return;
    setError("");
    setWarn("");
    resetOutputs();
    setPhaseSafe("connecting");
    // 每次会话一条新连接（一条连接一次会话；引擎失败后的重试同样走新连接）
    clientRef.current?.close();
    const client = new LiveClient();
    client.onMessage = handleMsg;
    client.onClose = handleClose;
    clientRef.current = client;
    try {
      await client.connect();
      // await 窗口期可能已卸载/断连（handleClose）/换代：延续操作前先验证本代次仍有效，
      // 无效则静默收尾——否则麦克风保持开启无人停、死 socket 永久卡在等待态
      if (clientRef.current !== client) {
        client.close();
        return;
      }
      const session = await startLiveCapture((pcm) => clientRef.current?.sendAudio(pcm));
      if (clientRef.current !== client) {
        session.stop();
        client.close();
        return;
      }
      captureRef.current = session;
      client.sendStart({
        engine,
        // 热词按当前引擎 gate：本地引擎词表口径不同，不透传（界面仅火山展示）
        hotwords: engine === "volcengine" ? hotwords.trim() || undefined : undefined,
        speaker: engine === "volcengine" ? speaker : undefined,
        // 语种仅本地引擎可选手动固定（火山自动检测够稳）；ISO 码由引擎解析为模型规范名
        language: engine === "local" && language ? language : undefined,
      });
      startedAtRef.current = Date.now();
      setElapsedMs(0);
      setPhaseSafe("streaming");
    } catch (e) {
      teardown(micErrorMessage(e, me?.desktop === true));
    }
  };

  const stopSession = () => {
    captureRef.current?.stop();
    captureRef.current = null;
    clientRef.current?.sendStop();
    setPhaseSafe("finishing");
  };

  const saveResult = () => {
    if (phase !== "final" || !caption.committed) return;
    setSaving(true);
    clientRef.current?.sendSave();
  };

  const live = phase === "streaming";
  const waitingFinal = phase === "finishing" || phase === "connecting";
  // 定格后解锁：切换引擎/改参数只影响下一次开始（startSession 恒关旧连接开新通道）
  const locked = phase !== "idle" && phase !== "final";
  const hasFinalText = phase === "final" && caption.committed.length > 0;
  const speakerMode = caption.segments.some((s) => s.speaker !== undefined);
  const lines = groupConsecutiveSpeaker(caption.segments);

  return (
    <>
      <PageHeader
        title="实时字幕"
        description="使用麦克风边说边出字，说完一键存为识别任务，继续 AI 提炼与问答"
        icon={<AudioWaveform size={16} strokeWidth={1.75} />}
      />

      {/* 引擎页签：会话进行中锁定（一条会话一条通道，中途不可换引擎） */}
      <div className={`mb-3 flex flex-wrap items-center gap-3 ${locked ? "pointer-events-none opacity-50" : ""}`}>
        <Tabs<LiveEngine>
          items={ENGINE_TABS}
          value={engine}
          onChange={(v) => {
            if (locked) return;
            setEngine(v);
            setError("");
            setWarn("");
          }}
        />
        {live && <span className="text-[11px] text-muted">会话进行中，暂不可切换引擎</span>}
        {engine === "local" && !live && r2t2Installed && (
          <span className="text-[11px] text-muted">本机识别每 60 秒分段续接，适合短会话；长会议建议用火山引擎</span>
        )}
      </div>

      {/* 本地引擎未就绪：安装引导卡（模型依赖精确到 Confucius4-R2T2） */}
      {engine === "local" && !modelsLoading && !r2t2Installed ? (
        <Card className="mb-4">
          <CardBody className="space-y-2">
            <p className="text-sm text-fg-2">
              本地实时识别需要先下载 <span className="text-fg">Confucius4-R2T2</span> 模型。
            </p>
            <p className="text-xs text-muted">
              识别在本机完成，数据不出本机；语种自动检测，可配热词提升专有名词识别率。
            </p>
            <Link to="/settings" className="text-xs text-accent hover:opacity-80">
              去设置页「本地环境」下载 →
            </Link>
          </CardBody>
        </Card>
      ) : null}

      {/* 会话控制条：开始/停止圆钮在动线起点（左上），配置项随其后，字幕流全宽铺开 */}
      <Card className="mb-4">
        <CardBody className="flex flex-wrap items-center gap-x-5 gap-y-3">
          <div className="flex shrink-0 items-center gap-3">
            <button
              type="button"
              aria-label={live ? "停止" : "开始"}
              disabled={waitingFinal || (engine === "local" && !r2t2Installed)}
              onClick={() => void (live ? stopSession() : startSession())}
              className={`flex size-11 cursor-pointer items-center justify-center rounded-full border transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
                live
                  ? "border-danger bg-danger/10 text-danger"
                  : "border-line-strong bg-raise-2 text-accent hover:border-accent"
              }`}
            >
              {live ? (
                <Square size={18} strokeWidth={1.75} fill="currentColor" />
              ) : (
                <Mic size={18} strokeWidth={1.75} />
              )}
            </button>
            {phase === "connecting" && <span className="text-[11px] text-muted">正在连接…</span>}
            {phase === "finishing" && <span className="text-[11px] text-muted">正在整理本次内容…</span>}
            {live && (
              <div className="h-1 w-24 overflow-hidden rounded-full bg-line">
                <div ref={levelRef} className="h-full rounded-full bg-accent" style={{ width: "0%" }} />
              </div>
            )}
            {engine === "volcengine" && phase === "idle" && (
              <span className="text-[11px] text-muted">约 1 元/小时</span>
            )}
          </div>
          <div
            className={`flex min-w-0 flex-1 flex-wrap items-center gap-x-5 gap-y-3 ${
              locked ? "pointer-events-none opacity-50" : ""
            }`}
          >
            {engine === "volcengine" ? (
              <>
                <Input
                  value={hotwords}
                  onChange={(e) => setHotwords(e.target.value)}
                  placeholder="热词（可选，逗号分隔）"
                  aria-label="热词"
                  disabled={locked}
                  className="min-w-44 max-w-xs flex-1"
                />
                <DictFill field="hotwords" onFill={setHotwords} />
                <label className="flex shrink-0 cursor-pointer items-center gap-2">
                  <Switch checked={speaker} onChange={setSpeaker} disabled={locked} />
                  <span className="text-sm text-fg-2">区分说话人</span>
                </label>
              </>
            ) : (
              <Select
                value={language}
                onChange={(e) => setLanguage(e.target.value)}
                disabled={locked}
                aria-label="识别语种"
                className="w-40 shrink-0"
              >
                <option value="">语种自动检测</option>
                <option value="zh">中文</option>
                <option value="en">英语</option>
                <option value="ja">日语</option>
                <option value="ko">韩语</option>
                <option value="fr">法语</option>
                <option value="de">德语</option>
                <option value="it">意大利语</option>
                <option value="es">西班牙语</option>
                <option value="pt">葡萄牙语</option>
                <option value="ru">俄语</option>
              </Select>
            )}
          </div>
        </CardBody>
      </Card>

      {/* 错误/警示：全宽横条，紧跟控制条 */}
      {(error || warn) && (
        <div className="mb-4 space-y-2">
          {error && (
            <p className="flex items-start gap-1.5 text-xs text-danger">
              <AlertTriangle size={13} strokeWidth={1.75} className="mt-0.5 shrink-0" />
              <span className="min-w-0 break-words">{error}</span>
            </p>
          )}
          {warn && (
            <p className="flex items-start gap-1.5 text-xs text-warn">
              <AlertTriangle size={13} strokeWidth={1.75} className="mt-0.5 shrink-0" />
              <span className="min-w-0 break-words">{warn}</span>
            </p>
          )}
        </div>
      )}

      {/* 字幕流：全宽主内容区 */}
      <Card className="min-w-0">
          <CardHeader
            title="字幕"
            icon={<Captions size={15} strokeWidth={1.75} />}
            aside={
              live ? (
                <span className="flex items-center gap-2">
                  {engine === "volcengine" && (
                    <span className="text-[11px] text-muted">约 1 元/小时</span>
                  )}
                  <span className="flex items-center gap-1.5 text-danger">
                    <span className="signal-dot signal-dot-pulse" />
                    <span className="font-mono text-xs tabular-nums">{formatElapsed(elapsedMs)}</span>
                  </span>
                </span>
              ) : finalInfo ? (
                <span className="font-mono text-[11px] tabular-nums text-muted">
                  时长 {formatElapsed(finalInfo.durationMs)}
                </span>
              ) : undefined
            }
          />
          <CardBody className="space-y-3">
            <div
              ref={captionBoxRef}
              aria-label="实时字幕内容"
              className="min-h-[280px] max-h-[52vh] overflow-y-auto"
            >
              {!captionHasText(caption) && !finalInfo ? (
                phase === "connecting" ? (
                  <p className="py-10 text-center text-sm text-muted">正在连接…</p>
                ) : (
                  <EmptyState
                    icon={<Mic size={18} strokeWidth={1.75} />}
                    title="还没有字幕"
                    description="点击「开始」使用麦克风说话，识别内容会实时显示在这里。"
                  />
                )
              ) : speakerMode ? (
                <div className="space-y-3 py-1">
                  {lines.map((l, i) => (
                    <p key={i} className="flex items-baseline gap-2.5 text-sm leading-relaxed">
                      {speakerLabel(l.speaker) && (
                        <span className="shrink-0 rounded-full border border-line bg-raise-2 px-1.5 py-px text-[10px] leading-4 text-muted">
                          {speakerLabel(l.speaker)}
                        </span>
                      )}
                      <span className="min-w-0 flex-1 text-fg">{l.text}</span>
                    </p>
                  ))}
                  {caption.unstable && (
                    <p className="text-sm leading-relaxed text-muted">{caption.unstable}…</p>
                  )}
                </div>
              ) : (
                <p className="whitespace-pre-wrap py-1 text-[15px] leading-8">
                  {caption.committed && <span className="text-fg">{caption.committed}</span>}
                  {caption.unstable && <span className="text-muted">{caption.unstable}</span>}
                  {live && captionHasText(caption) && <span className="signal-dot ml-1 inline-block text-accent" />}
                </p>
              )}
            </div>

            {finalInfo?.degraded && (
              <p className="flex items-start gap-1.5 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-xs text-warn">
                <AlertTriangle size={13} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                本次会话出现过中断，以上是已尽力保留的内容，可能不完整。
              </p>
            )}
            {phase === "final" && !caption.committed && (
              <p className="flex items-start gap-1.5 text-xs text-muted">
                <CircleAlert size={13} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                本次没有识别到说话内容，无可保存的结果。
              </p>
            )}
            {phase === "final" && (
              <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line pt-3">
                <Button
                  variant="primary"
                  icon={hasFinalText ? <Save size={15} strokeWidth={1.75} /> : <FileText size={15} strokeWidth={1.75} />}
                  loading={saving}
                  disabled={!hasFinalText}
                  onClick={saveResult}
                >
                  {hasFinalText ? "保存到历史" : "无可保存内容"}
                </Button>
                {hasFinalText ? (
                  <p className="text-[11px] text-muted">保存后在历史中查看文稿与字幕，可继续 AI 提炼、待办与问答。</p>
                ) : (
                  <Button variant="ghost" onClick={() => void startSession()}>
                    再试一次
                  </Button>
                )}
              </div>
            )}
          </CardBody>
        </Card>
    </>
  );
}
