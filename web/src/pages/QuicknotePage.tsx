import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Mic, SlidersHorizontal, Square } from "lucide-react";
import { apiBase, fetchJSON } from "../lib/api";
import { useMe } from "../lib/auth";
import { formatTime } from "../lib/player";
import { PRICE_QUICKNOTE_SAUC, PRICE_SNAPSHOT_DATE } from "../lib/pricing";
import type { TaskDetail } from "../lib/types";
import { useProviderConfigured } from "../lib/useStorageEnabled";
import { useTaskEvents } from "../lib/ws";
import { recordingSupported, startRecording, type RecordingSession } from "../lib/recorder";
import { useTranscriptSync } from "../lib/useTranscriptSync";
import { Card, CardBody, CardHeader, Field, Input, PageHeader, useToast } from "../ui";
import { ChatPanel } from "./quicknote/ChatPanel";
import { RefinePanel } from "./quicknote/RefinePanel";
import { ResultPanel } from "./quicknote/ResultPanel";
import { failedStatusText, loadTask, replayFileId, type ResultView, type Run } from "./quicknote/model";

/** 页面状态机：待录 → 录音中 → 提交中 → 转写中 → 完成 | 出错（录音停止即自动提交） */
type Phase = "idle" | "recording" | "submitting" | "running" | "done" | "error";

export default function QuicknotePage() {
  const [phase, setPhase] = useState<Phase>("idle");
  const [hotwords, setHotwords] = useState("");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  // 终态任务详情（TaskDetail）：文字稿/纪要展示层在此基础上展开
  const [task, setTask] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const [recError, setRecError] = useState("");
  const [recUrl, setRecUrl] = useState<string | null>(null); // 录音 Blob 回放地址（上传流不可用时的回落）
  const [recFileId, setRecFileId] = useState<string | null>(null); // 上传拿到的 file_id（服务端回放流）
  const [elapsedMs, setElapsedMs] = useState(0);
  // 说话人改名（父级持有）：结果区的改名即时生效，问答区的上下文组装同源取称呼
  const [speakerNames, setSpeakerNames] = useState<Record<string, string>>({});

  // 桌面形态标记：麦克风授权走系统设置而非浏览器地址栏，被拒后的指引文案据此分流
  const { data: me } = useMe();
  const recSessionRef = useRef<RecordingSession | null>(null);
  const recFileRef = useRef<File | null>(null);
  const recStartRef = useRef(0);
  const recLevelRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
  const { toast } = useToast();
  const ev = useTaskEvents();
  // undefined = 设置未加载完成，与未配置同走引导卡（保守态，与语音识别页一致）
  const volcReady = useProviderConfigured("volcengine");
  // 深链恢复（/quicknote?task=<id>，来自历史页「查看笔记」）：参数保留在地址栏
  // （与语音识别页 ?artifact= 同例），会话内只消费一次；成功恢复后即使火山凭证
  // 未配置也放行工作区——回看历史笔记不依赖转写凭证
  const [searchParams] = useSearchParams();
  const deepTaskId = searchParams.get("task")?.trim() ?? "";
  const deepConsumedRef = useRef(false);
  const [deepRestored, setDeepRestored] = useState(false);

  /* 进度事件驱动当前任务：progress 刷进度条；done/error/canceled 均为收尾，拉详情进结果态 */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      // eslint-disable-next-line react/set-state-in-effect -- 与语音识别/妙记页同一事件驱动进度写法（进度推送只能落在 effect 里）
      setRun({ status: "running", progress: ev.progress ?? 0, note: ev.note ?? "处理中" });
      return;
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      loadTask(taskId)
        .then((d) => {
          setRun({
            status: d.task.status,
            progress: d.task.progress,
            note: d.task.progress_note,
            error: d.task.error,
          });
          setTask(d);
          setSpeakerNames(d.task.summary?.speaker_names ?? {});
          if (d.task.status === "succeeded") {
            setPhase("done");
          } else {
            setPhase("error");
            toast({ tone: "error", title: failedStatusText(d.task.status), description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
          setPhase("error");
          toast({ tone: "error", title: "读取任务结果失败", description: e.message });
        });
    }
  }, [ev, taskId, toast]);

  /* 深链恢复：?task=<id> → 拉任务详情，succeeded 且有分句时直接进 done 视图
     （文字稿/回放/改名/加工/问答全部可用），回放源从任务 input 的 file_ids 恢复
     （服务端上传流，刷新后仍可回放）；取不到则降级隐藏播放器。仍在转写中的任务
     接线到事件流由 WS 驱动（回放源同样预置，收尾即有播放器）；失败或无可回看
     文字稿在结果区直述提示。
     恢复过程的 setState 都落在 promise 回调（异步），不在 effect 体内同步写。 */
  useEffect(() => {
    if (!deepTaskId || deepConsumedRef.current) return;
    deepConsumedRef.current = true;
    loadTask(deepTaskId)
      .then((d) => {
        if (recSessionRef.current) return; // 用户已抢先开始录音：丢弃过期恢复，不打断录音主流程
        if (d.task.status === "pending" || d.task.status === "running") {
          // 任务仍在转写：与常规提交后同一链路，进度与收尾交给 WS 事件。
          // 回放源此刻预置——WS 收尾只写 run/task/speakerNames（常规流的 recFileId
          // 由 submit onSuccess 补，深链路径没人补），不预置则完成后没有播放器；
          // 同时放行工作区（接线已成立，回看不依赖转写凭证）
          setTaskId(d.task.id);
          setRun({ status: d.task.status, progress: d.task.progress, note: d.task.progress_note || "处理中" });
          setRecFileId(replayFileId(d.task.input));
          setDeepRestored(true);
          setPhase("running");
          return;
        }
        const segs = d.task.summary?.segments ?? [];
        if (d.task.status !== "succeeded" || segs.length === 0) {
          setRun({ status: d.task.status, progress: d.task.progress, note: d.task.progress_note, error: d.task.error });
          setSubmitError(d.task.error || "该任务没有可回看的文字稿");
          setPhase("error");
          return;
        }
        setTaskId(d.task.id);
        setTask(d);
        setRun({ status: d.task.status, progress: d.task.progress, note: d.task.progress_note });
        setSpeakerNames(d.task.summary?.speaker_names ?? {});
        setRecFileId(replayFileId(d.task.input));
        setDeepRestored(true);
        setPhase("done");
      })
      .catch((e: Error) => {
        if (recSessionRef.current) return; // 同上：录音进行中不写错误态
        setSubmitError(`读取任务失败：${e.message}`);
        setPhase("error");
      });
  }, [deepTaskId]);

  /* Blob 回放地址生命周期：换源与卸载时统一释放，防泄漏（走服务端上传流时不产生 Blob） */
  useEffect(() => {
    if (!recUrl) return;
    return () => URL.revokeObjectURL(recUrl);
  }, [recUrl]);

  /* 卸载即停录：离开页面不遗留活跃的麦克风会话 */
  useEffect(() => () => recSessionRef.current?.cancel(), []);

  /* 录音计时与电平条：计时走 state（低频），电平走 rAF 直改 DOM（不走 React 渲染） */
  useEffect(() => {
    if (phase !== "recording") return;
    const timer = window.setInterval(() => setElapsedMs(Date.now() - recStartRef.current), 250);
    return () => window.clearInterval(timer);
  }, [phase]);

  useEffect(() => {
    if (phase !== "recording") return;
    let raf = 0;
    const tick = () => {
      if (recLevelRef.current && recSessionRef.current) {
        recLevelRef.current.style.width = `${Math.round(recSessionRef.current.level() * 100)}%`;
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [phase]);

  const submit = useMutation({
    mutationFn: async () => {
      const file = recFileRef.current;
      if (!file) throw new Error("没有可提交的录音");
      // 本地上传通道：先传录音文件拿到引用，再建转写任务（与语音识别页本地上传一致）
      const fd = new FormData();
      fd.append("file", file);
      const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
      // 一句话转写：语种自动识别，说话人与中英方言识别默认开启；热词选填
      const params: Record<string, unknown> = { version: "sentence", srt: true, speaker: true, lid: true };
      if (hotwords.trim()) params.hotwords = hotwords.trim();
      const d = await fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "volcengine", tool: "asr", params, file_ids: [up.file_id] }),
      });
      return { task_id: d.task_id, file_id: up.file_id };
    },
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRecFileId(d.file_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setTask(null);
      setSpeakerNames({});
      setSubmitError("");
      setPhase("running");
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      // 清掉上一段的运行态，避免旧结果卡遮住本次失败信息
      setTaskId(null);
      setRun(null);
      setSubmitError(e.message);
      setPhase("error");
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  const startRec = async () => {
    setRecError("");
    try {
      recSessionRef.current = await startRecording();
      recStartRef.current = Date.now();
      setElapsedMs(0);
      setPhase("recording");
    } catch (e) {
      const err = e as DOMException;
      setRecError(
        err?.name === "NotAllowedError"
          ? me?.desktop === true
            ? "麦克风权限被拒绝：请在系统设置的麦克风权限中允许 VoxBox 后重试"
            : "麦克风权限被拒绝：请在浏览器地址栏允许麦克风访问后重试"
          : `无法启动录音：${err?.message ?? e}`,
      );
    }
  };

  /* 停止即提交：录音完成直接进转写（「录完即出文字稿」），失败可在结果区重录 */
  const stopAndSubmit = async () => {
    const session = recSessionRef.current;
    if (!session) return;
    recSessionRef.current = null;
    setPhase("submitting");
    try {
      const wav = await session.stop(); // 产出 16kHz 单声道 WAV，走本地上传通道
      recFileRef.current = wav;
      setRecUrl(URL.createObjectURL(wav)); // 旧 Blob 由 [recUrl] 生命周期 effect 释放
      submit.mutate();
    } catch (e) {
      // 录音未生成（无任务产生）：同样清运行态，让错误视图接管结果区
      setTaskId(null);
      setRun(null);
      setSubmitError(`录音处理失败：${(e as Error).message}`);
      setPhase("error");
    }
  };

  /** 回到待录状态：清掉上一段的录音与结果（热词保留，便于连续记录同类内容） */
  const resetToIdle = () => {
    setRecUrl(null); // 旧 Blob 由 [recUrl] 生命周期 effect 释放
    recFileRef.current = null;
    setRecFileId(null);
    setTaskId(null);
    setRun(null);
    setTask(null);
    setSpeakerNames({});
    setSubmitError("");
    setDeepRestored(false);
    setPhase("idle");
  };

  const recording = phase === "recording";
  const micBusy = phase === "submitting" || phase === "running";
  const segments = task?.task.summary?.segments ?? [];
  const durationMs = task?.task.summary?.duration_ms;
  /** 音频回放源：服务端上传流优先（刷新后仍可按 file_id 回放），回落本地 Blob */
  const playSrc = recFileId ? `${apiBase}/api/uploads/${recFileId}/stream` : recUrl;
  /** 分句跟读与跳播：父级持有，结果区的分句列表与问答区的引用 chip 共用 */
  const { activeIdx, seekTo } = useTranscriptSync(segments, playSrc);
  /** 展示名：改名覆盖优先，否则「说话人{编号}」（编号原样展示，不 +1，避免与后端编号错位） */
  const speakerLabel = useCallback((id: string) => speakerNames[id] ?? `说话人${id}`, [speakerNames]);
  /** 结果区视图：提交中 → 有运行态按状态分流 → 提交/录音失败且尚无运行态也进错误视图 */
  const resultView: ResultView =
    phase === "submitting"
      ? "submitting"
      : phase === "error" && run === null
        ? "error"
        : run === null
          ? "empty"
          : run.status === "succeeded"
            ? "done"
            : run.status === "running" || run.status === "pending"
              ? "progress"
              : "error";
  const docTitle = task?.task.title || "录音笔记";
  const durationText = durationMs ? `${(durationMs / 60000).toFixed(1)} 分钟` : undefined;

  return (
    <>
      <PageHeader
        title="录音笔记"
        description="录完即出文字稿，AI 提炼要点与待办"
        icon={<Mic size={16} strokeWidth={1.75} />}
      />

      {/* 未配置凭证（或设置未加载完）：引导卡替代整个工作区，与语音识别页一致；
          深链恢复成功时放行工作区（回看历史笔记不依赖转写凭证） */}
      {!volcReady && !deepRestored ? (
        <Card>
          <CardBody className="space-y-2">
            <p className="text-sm text-fg-2">尚未配置火山引擎凭证，录音笔记暂时无法转写。</p>
            <Link to="/settings" className="text-xs text-accent hover:opacity-80">
              去设置页配置火山引擎 API Key →
            </Link>
          </CardBody>
        </Card>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
            {/* 左：录音卡 */}
            <Card className="min-w-0">
              <CardHeader
                title="录音"
                icon={<Mic size={15} strokeWidth={1.75} />}
                aside={<span className="micro">停止后自动转写</span>}
              />
              <CardBody className="space-y-3">
                {!recordingSupported() ? (
                  <p className="text-[11px] text-muted">当前环境不支持录音（需要 https 或 localhost）</p>
                ) : (
                  <>
                    <div className="flex flex-col items-center gap-2 rounded-[var(--radius-md)] border border-dashed border-line-strong bg-raise-2/40 px-4 py-8">
                      <button
                        type="button"
                        aria-label={recording ? "停止录音" : "开始录音"}
                        disabled={micBusy}
                        onClick={() => void (recording ? stopAndSubmit() : startRec())}
                        className={`flex size-12 cursor-pointer items-center justify-center rounded-full border transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
                          recording
                            ? "border-danger bg-danger/10 text-danger"
                            : "border-line-strong bg-raise-2 text-accent hover:border-accent"
                        }`}
                      >
                        {recording ? (
                          <Square size={18} strokeWidth={1.75} fill="currentColor" />
                        ) : (
                          <Mic size={18} strokeWidth={1.75} />
                        )}
                      </button>
                      {recording && (
                        <>
                          <span className="font-mono text-sm tabular-nums text-fg-2">
                            {formatTime(elapsedMs / 1000)}
                          </span>
                          <div className="h-1 w-40 overflow-hidden rounded-full bg-line">
                            <div ref={recLevelRef} className="h-full rounded-full bg-accent" style={{ width: "0%" }} />
                          </div>
                        </>
                      )}
                      <p className="text-[11px] text-muted">
                        {phase === "recording"
                          ? "正在录音，点击方块结束并转写"
                          : phase === "submitting"
                            ? "正在提交录音…"
                            : phase === "running"
                              ? "转写进行中，进度见下方"
                              : phase === "done"
                                ? "点击麦克风录制下一段"
                                : phase === "error"
                                  ? "点击麦克风重新录制"
                                  : "点击麦克风开始录音，停止后自动转写"}
                      </p>
                    </div>
                    {recError && (
                      <p className="flex items-start gap-1.5 text-[11px] text-danger">
                        <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                        {recError}
                      </p>
                    )}
                  </>
                )}
              </CardBody>
            </Card>

            {/* 右：转写设置 */}
            <Card className="lg:sticky lg:top-4 lg:self-start">
              <CardHeader
                title="转写设置"
                icon={<SlidersHorizontal size={15} strokeWidth={1.75} />}
                aside={<span className="micro">火山引擎 · 语音转文字</span>}
              />
              <CardBody className="space-y-4">
                <Field label="热词" aside="可选" hint="提升产品名、人名等专有名词的识别率">
                  {({ id, ...rest }) => (
                    <Input
                      id={id}
                      value={hotwords}
                      onChange={(e) => setHotwords(e.target.value)}
                      placeholder="产品名、人名等，逗号分隔"
                      {...rest}
                    />
                  )}
                </Field>
                <p className="text-[11px] leading-relaxed text-muted">
                  点击麦克风开始录音，停止后自动提交转写；语种自动识别，说话人与方言识别已开启，文字稿显示在下方。
                </p>
                <p className="text-[11px] leading-relaxed text-muted">
                  计费：语音识别 {PRICE_QUICKNOTE_SAUC.postpaid[0].price} 元/小时，AI 加工按所配大模型另计，刊例快照{" "}
                  {PRICE_SNAPSHOT_DATE}，以账单为准。
                </p>
              </CardBody>
            </Card>
          </div>

          {/* 结果区：进度与终态（波形回放 + 分句跟读 + 说话人改名与统计）。
              key 带「详情已加载」标记：任务详情在 WS 收尾后经 loadTask 异步到达，
              到达时重挂载以重置改名草稿等编辑态（speakerNames 持久在父级，
              详情到达时由 loadTask 回调写入） */}
          <ResultPanel
            key={`${taskId ?? "none"}${task ? "-loaded" : ""}`}
            view={resultView}
            run={run}
            taskId={taskId}
            title={docTitle}
            segments={segments}
            durationMs={durationMs}
            playSrc={playSrc}
            speakerNames={speakerNames}
            onSpeakerNamesChange={setSpeakerNames}
            speakerLabel={speakerLabel}
            durationText={durationText}
            activeIdx={activeIdx}
            seekTo={seekTo}
            submitError={submitError}
            onReset={resetToIdle}
          />

          {/* 加工区：转写完成后出现，AI 提炼总结/待办/日程，结果持久在任务 Summary.refined。
              done 视图必以任务详情到位为前提，回显直接从 task 派生，无需再拉 */}
          {phase === "done" && taskId && (
            <RefinePanel taskId={taskId} title={docTitle} refined={task?.task.summary?.refined} />
          )}

          {/* 问答区：与加工区平级——就这段录音追问，回答里的【分:秒】可点击跳播
              （与分句跟读共用同一跳播与轨信息） */}
          {phase === "done" && taskId && segments.length > 0 && (
            <ChatPanel
              segments={segments}
              speakerLabel={speakerLabel}
              onSeek={(ms) => seekTo(ms, { title: docTitle, sub: durationText })}
            />
          )}
        </>
      )}
    </>
  );
}
