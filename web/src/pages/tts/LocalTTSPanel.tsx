import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, AudioLines, Mic, Pencil, Square, Trash2, Upload } from "lucide-react";
import { Link } from "react-router-dom";
import { fetchJSON } from "../../lib/api";
import { useMe } from "../../lib/auth";
import { formatTime } from "../../lib/player";
import { recordingSupported, startRecording, type RecordingSession } from "../../lib/recorder";
import { useLocalReady, useLocalVoices, useModels } from "../../lib/models";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import {
  deleteVoice as deleteVoiceApi,
  renameVoice as renameVoiceApi,
  uploadVoice as uploadVoiceApi,
  useVoiceLibrary,
  voiceStreamUrl,
  type VoiceLibItem,
} from "../../lib/voiceLibrary";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ConfirmDialog,
  EmptyState,
  Field,
  IconButton,
  Input,
  Modal,
  Select,
  Textarea,
  WavePlayer,
  useToast,
} from "../../ui";

// 与后端音色库上传上限同源(internal/server/voicelib.go: maxVoiceUploadBytes = 20 << 20);
// 选文件时前端先挡一道,避免 100MB 级文件完整上传后才被后端驳回
const VOICE_MAX_BYTES = 20 * 1024 * 1024;

const LANGS = [
  { value: "Chinese", label: "中文" },
  { value: "English", label: "英文" },
  { value: "Japanese", label: "日语" },
  { value: "Korean", label: "韩语" },
];

// index_tts2 家族的语言码(与后端 provider/local/tts.go indexLanguages 同源)
const INDEX_LANGS = [
  { value: "auto", label: "自动" },
  { value: "zh", label: "中文" },
  { value: "en", label: "英文" },
  { value: "ja", label: "日语" },
  { value: "es", label: "西班牙语" },
  { value: "ar", label: "阿拉伯语" },
];

/** 毫秒 → 秒展示(下拉项 name(duration) 用)。 */
const fmtSec = (ms: number) => `${(ms / 1000).toFixed(1)}s`;

/** 文件名去扩展名作音色默认名(录音产物为 录音-HHMMSS.wav)。 */
const defaultNameFromFile = (f: File) => f.name.replace(/\.[^.]+$/, "");

/** 命名弹窗状态机:save=录制/上传完成后入库命名;rename=已入库音色改名。 */
type NameModal = { kind: "save"; file: File } | { kind: "rename"; id: string } | null;

/** 本地语音合成面板:audio.cpp 引擎 + 已安装 Qwen3-TTS / IndexTTS2.5,sherpa-onnx + Kokoro。
 *  克隆走音色库(录制/上传入库 → voice_id 提交),不再用临时文件上传;
 *  家族感知:index_tts2 锁克隆 + 六语言 + 情感参数,kokoro 锁预置音色 + 无语言/风格参数,
 *  qwen3_tts 维持预置 + 四语言。 */
export default function LocalTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState("");
  const [mode, setMode] = useState<"clone" | "preset">("clone");
  const [voiceId, setVoiceId] = useState("");
  const [speaker, setSpeaker] = useState("");
  const [instruct, setInstruct] = useState("");
  const [language, setLanguage] = useState("Chinese");
  const [emotionText, setEmotionText] = useState("");
  const [emotionAlpha, setEmotionAlpha] = useState("");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const { toast } = useToast();
  // 桌面形态标记：麦克风授权走系统设置而非浏览器地址栏，被拒后的指引文案据此分流
  const { data: me } = useMe();
  const qc = useQueryClient();
  const ev = useTaskEvents();

  const ready = useLocalReady("tts");
  const models = useModels();
  const installedModels = (models.data?.items ?? []).filter(
    (m) => m.kind === "tts" && m.status === "installed",
  );
  // 已安装模型唯一时自动选中;多模型时默认第一个
  useEffect(() => {
    if (!model && installedModels.length > 0) setModel(installedModels[0].id);
  }, [installedModels, model]);

  // —— 家族感知:选中模型的 family 决定克隆/预置可用性与语言集(后端 family 平铺在 items 上) ——
  const selectedModelItem = installedModels.find((m) => m.id === model);
  const isIndex = selectedModelItem?.family === "index_tts2";
  const isKokoro = selectedModelItem?.family === "kokoro";
  // 家族强制:effectiveMode 决定渲染与提交,index 锁克隆、kokoro 锁预置,
  // 切回 qwen3 时用户先前的选择自动恢复
  const effectiveMode: "clone" | "preset" = isKokoro ? "preset" : isIndex ? "clone" : mode;
  // 预置音色列表按家族拉取:kokoro 103 个内置音色,qwen3 九个 CustomVoice speaker
  const presetVoices = useLocalVoices(isKokoro ? "kokoro" : undefined);
  // 家族切换时把语言校正到当前家族支持集内(qwen3 全名 / index 语言码;kokoro 不发语言)
  useEffect(() => {
    const langSet = (isIndex ? INDEX_LANGS : LANGS).map((l) => l.value);
    setLanguage((l) => (langSet.includes(l) ? l : isIndex ? "auto" : "Chinese"));
  }, [isIndex]);
  // kokoro 音色表与 qwen3 不通用:进入/离开 kokoro 家族时清掉失配的选中音色
  useEffect(() => {
    setSpeaker("");
  }, [isKokoro]);

  // —— 音色库 ——
  const vlib = useVoiceLibrary();
  const libVoices = vlib.data?.items ?? [];
  const selectedVoice = libVoices.find((v) => v.id === voiceId);
  // 选中项被删除/列表刷新后失效时清空选择,避免预览与提交指向不存在的音色
  useEffect(() => {
    if (!voiceId || !vlib.data) return;
    if (!vlib.data.items.some((v) => v.id === voiceId)) setVoiceId("");
  }, [vlib.data, voiceId]);

  // —— 命名弹窗(入库保存 / 改名共用)与删除确认 ——
  const [nameModal, setNameModal] = useState<NameModal>(null);
  const [nameValue, setNameValue] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<VoiceLibItem | null>(null);
  const invalidateVoices = () => void qc.invalidateQueries({ queryKey: ["voice-library"] });

  const uploadMut = useMutation({
    mutationFn: ({ name, file }: { name: string; file: File }) => uploadVoiceApi(name, file),
    onSuccess: async (v) => {
      setNameModal(null);
      toast({ tone: "ok", title: "音色已入库", description: v.name });
      // 「入库即选中」必须等重取完成后再设:invalidateQueries 的 Promise 在活动查询
      // 重取结束后才 resolve;若先行 setVoiceId,守卫 effect 会拿旧列表判「不存在」
      // 把刚设的选中竞态清掉,预览不出现、canSubmit 恒 false
      await qc.invalidateQueries({ queryKey: ["voice-library"] });
      setVoiceId(v.id);
    },
    onError: (e: Error) => toast({ tone: "error", title: "音色入库失败", description: e.message }),
  });
  const renameMut = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => renameVoiceApi(id, name),
    onSuccess: () => {
      invalidateVoices();
      setNameModal(null);
      toast({ tone: "ok", title: "已重命名" });
    },
    onError: (e: Error) => toast({ tone: "error", title: "重命名失败", description: e.message }),
  });
  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteVoiceApi(id),
    onSuccess: (_, id) => {
      invalidateVoices();
      setDeleteTarget(null);
      if (voiceId === id) setVoiceId("");
      toast({ tone: "ok", title: "音色已删除" });
    },
    onError: (e: Error) => toast({ tone: "error", title: "删除失败", description: e.message }),
  });

  const confirmName = () => {
    const name = nameValue.trim();
    if (!name) return;
    if (nameModal?.kind === "save") uploadMut.mutate({ name, file: nameModal.file });
    else if (nameModal?.kind === "rename") renameMut.mutate({ id: nameModal.id, name });
  };
  const namePending = uploadMut.isPending || renameMut.isPending;

  // —— 录音:RecordingSession 启停 + 秒数显示(与 ASRPage 同款会话管理) ——
  const recSupported = recordingSupported();
  const [recState, setRecState] = useState<"idle" | "recording" | "processing">("idle");
  const [recError, setRecError] = useState("");
  const [elapsedMs, setElapsedMs] = useState(0);
  const recSessionRef = useRef<RecordingSession | null>(null);
  const recStartRef = useRef(0);

  const startRec = async () => {
    setRecError("");
    try {
      recSessionRef.current = await startRecording();
      recStartRef.current = Date.now();
      setElapsedMs(0);
      setRecState("recording");
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

  const stopRec = async () => {
    const session = recSessionRef.current;
    if (!session) return;
    setRecState("processing");
    try {
      const file = await session.stop(); // 产出 16kHz WAV,直接入库
      setNameValue(defaultNameFromFile(file));
      setNameModal({ kind: "save", file }); // 弹命名;取消即丢弃,不占音色库
    } catch (e) {
      setRecError(`录音处理失败：${(e as Error).message}`);
    } finally {
      recSessionRef.current = null;
      setRecState("idle");
    }
  };

  /* 录音计时:250ms 步进(低频 state,与 ASRPage 一致) */
  useEffect(() => {
    if (recState !== "recording") return;
    const timer = window.setInterval(() => setElapsedMs(Date.now() - recStartRef.current), 250);
    return () => window.clearInterval(timer);
  }, [recState]);

  // —— 上传文件入库:20MB 前端预检 → 命名弹窗 ——
  const uploadInputRef = useRef<HTMLInputElement>(null);
  const pickVoiceFile = (f: File | null) => {
    if (!f) return;
    if (f.size > VOICE_MAX_BYTES) {
      toast({ tone: "error", title: "音频文件过大", description: "请选择 20MB 以内的音频" });
      return;
    }
    setNameValue(defaultNameFromFile(f));
    setNameModal({ kind: "save", file: f });
  };

  /* WS 事件驱动进度;终态拉详情(与 QianwenTTSPanel 同款) */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setRun({ status: "running", progress: ev.progress ?? 0, note: ev.note ?? "处理中" });
      return;
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<TaskDetail>(`/api/tasks/${taskId}`)
        .then((d) => {
          setRun({ status: d.task.status, progress: d.task.progress, note: d.task.progress_note, error: d.task.error });
          setDetail(d);
          if (d.task.status === "failed") {
            toast({ tone: "error", title: "合成失败", description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
        });
    }
  }, [ev, taskId, toast]);

  const submit = useMutation({
    mutationFn: async () => {
      const params: Record<string, unknown> = {
        text: text.trim(), model, mode: effectiveMode,
      };
      // 语言仅 qwen3/index 透传(kokoro 中英混读由文本驱动,后端缺省 auto)
      if (!isKokoro) params.language = language;
      // 克隆:音色库 voice_id(库内成品已 24kHz 单声道,后端不经转码直用)
      if (effectiveMode === "clone") params.voice_id = voiceId;
      if (effectiveMode === "preset") {
        params.speaker = speaker;
        if (instruct.trim()) params.instruct = instruct.trim();
      }
      // 情感参数仅 index_tts2 家族透传(后端对 qwen3 直接忽略);留空不下发
      if (isIndex) {
        if (emotionText.trim()) params.emotion_text = emotionText.trim();
        const raw = emotionAlpha.trim();
        if (raw !== "") {
          const alpha = Number(raw);
          if (!Number.isNaN(alpha)) params.emotion_alpha = alpha;
        }
      }
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "local", tool: "tts", params }),
      });
    },
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setSubmitError("");
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  // —— 未就绪引导卡 ——
  if (ready.isLoading) return null;
  if (ready.data && !ready.data.ready) {
    return (
      <Card>
        <CardBody className="space-y-2">
          <p className="text-sm text-fg-2">本地推理引擎或模型尚未安装:</p>
          <ul className="space-y-1 text-xs text-muted">
            {(ready.data.missing ?? []).map((m) => (
              <li key={`${m.type}-${m.id}`}>
                · {m.type === "engine" ? "引擎" : "模型"}:{m.name}
              </li>
            ))}
          </ul>
          <Link to="/settings" className="text-xs text-accent hover:opacity-80">
            去设置页「本地环境」下载 →
          </Link>
        </CardBody>
      </Card>
    );
  }

  const canSubmit =
    text.trim() !== "" &&
    model !== "" &&
    (effectiveMode === "preset" ? speaker !== "" : voiceId !== "");
  const artifacts = detail?.artifacts ?? [];
  const audioArtifacts = artifacts.filter((a) => a.kind === "audio");

  return (
    <>
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左:文本编辑区 + 运行态 + 结果 */}
        <Card className="min-w-0">
          <CardHeader title="合成文本" icon={<AudioLines size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-3">
            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={8}
              placeholder="输入要合成的文本…"
            />
            {/* ProgressBody 是运行中组件(带波形骨架):仅非终态显示,完成/失败由产物行与错误行接管 */}
            {taskId && run && run.status !== "succeeded" && run.status !== "failed" && (
              <ProgressBody run={run} />
            )}
            {run?.status === "failed" && (
              <p className="flex items-start gap-1.5 text-xs text-danger">
                <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
                <span className="min-w-0 break-words">{run.error || "任务失败，请重试"}</span>
              </p>
            )}
            {audioArtifacts.map((a) => (
              <AudioRow key={a.id} a={a} />
            ))}
            {submitError && <p className="text-xs text-danger">{submitError}</p>}
          </CardBody>
        </Card>
        {/* 右:参数面板 */}
        <Card>
          <CardHeader title="本地推理参数" />
          <CardBody className="space-y-4">
            <Field label="本地模型">
              {({ id, ...rest }) => (
                <Select id={id} value={model} onChange={(e) => setModel(e.target.value)} {...rest}>
                  {installedModels.map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </Select>
              )}
            </Field>
            <Field
              label="音色模式"
              hint={isIndex ? "IndexTTS2.5 仅支持参考音频克隆" : isKokoro ? "Kokoro 仅支持预置音色" : undefined}
            >
              {({ id, ...rest }) => (
                <Select
                  id={id}
                  value={effectiveMode}
                  onChange={(e) => setMode(e.target.value as "clone" | "preset")}
                  {...rest}
                >
                  {!isKokoro && <option value="clone">参考音频克隆</option>}
                  {!isIndex && <option value="preset">预置音色</option>}
                </Select>
              )}
            </Field>
            {effectiveMode === "clone" ? (
              <>
                <Field label="克隆音色" hint="1-60 秒清晰人声,合成时作参考音频">
                  {({ id, ...rest }) => (
                    <div className="space-y-2">
                      {libVoices.length === 0 ? (
                        <EmptyState
                          icon={<AudioLines size={18} strokeWidth={1.75} />}
                          title="音色库还是空的"
                          description="先在下方录制或上传一个音色，再回来选择"
                        />
                      ) : (
                        <div className="flex items-center gap-1.5">
                          <Select id={id} value={voiceId} onChange={(e) => setVoiceId(e.target.value)} {...rest}>
                            <option value="">选择音色…</option>
                            {libVoices.map((v) => (
                              <option key={v.id} value={v.id}>
                                {v.name}（{fmtSec(v.duration_ms)}）
                              </option>
                            ))}
                          </Select>
                          <IconButton
                            label="重命名"
                            size="sm"
                            disabled={!selectedVoice || namePending}
                            onClick={() => {
                              if (!selectedVoice) return;
                              setNameValue(selectedVoice.name);
                              setNameModal({ kind: "rename", id: selectedVoice.id });
                            }}
                          >
                            <Pencil size={13} strokeWidth={1.75} />
                          </IconButton>
                          <IconButton
                            label="删除"
                            size="sm"
                            variant="danger"
                            disabled={!selectedVoice || deleteMut.isPending}
                            onClick={() => selectedVoice && setDeleteTarget(selectedVoice)}
                          >
                            <Trash2 size={13} strokeWidth={1.75} />
                          </IconButton>
                        </div>
                      )}
                      {/* 选中即预览:直接播音色库成品流,播放前就能确认选对了音色 */}
                      {selectedVoice && (
                        <WavePlayer
                          src={voiceStreamUrl(selectedVoice.id)}
                          title={selectedVoice.name}
                          durationSec={selectedVoice.duration_ms / 1000}
                        />
                      )}
                    </div>
                  )}
                </Field>
                <Field
                  label="录入新音色"
                  hint={recSupported ? "录制或上传 1-60 秒清晰人声,超 60 秒自动裁剪" : "当前浏览器不支持录音,请使用上传文件"}
                >
                  {() => (
                    <div className="space-y-2">
                      <div className="flex items-center gap-2">
                        <Button
                          type="button"
                          size="sm"
                          variant={recState === "recording" ? "danger" : "secondary"}
                          icon={
                            recState === "recording" ? (
                              <Square size={12} strokeWidth={1.75} fill="currentColor" />
                            ) : (
                              <Mic size={13} strokeWidth={1.75} />
                            )
                          }
                          disabled={!recSupported || recState === "processing"}
                          onClick={() => (recState === "recording" ? void stopRec() : void startRec())}
                        >
                          {recState === "recording"
                            ? `停止 ${formatTime(elapsedMs / 1000)}`
                            : recState === "processing"
                              ? "处理中…"
                              : "录音"}
                        </Button>
                        <Button
                          type="button"
                          size="sm"
                          variant="secondary"
                          icon={<Upload size={13} strokeWidth={1.75} />}
                          disabled={recState !== "idle"}
                          onClick={() => uploadInputRef.current?.click()}
                        >
                          上传文件
                        </Button>
                        <input
                          ref={uploadInputRef}
                          type="file"
                          accept="audio/*,.wav,.mp3,.m4a,.flac"
                          className="hidden"
                          onChange={(e) => {
                            pickVoiceFile(e.target.files?.[0] ?? null);
                            e.target.value = ""; // 允许再次选同一个文件时仍触发校验
                          }}
                        />
                      </div>
                      {recState === "recording" && (
                        <p className="text-[11px] text-muted">录音中…对麦克风说话,点「停止」结束</p>
                      )}
                      {recError && <p className="text-[11px] text-danger">{recError}</p>}
                    </div>
                  )}
                </Field>
              </>
            ) : (
              <>
                <Field label="预置音色">
                  {({ id, ...rest }) => (
                    <Select id={id} value={speaker} onChange={(e) => setSpeaker(e.target.value)} {...rest}>
                      <option value="">选择音色…</option>
                      {(presetVoices.data?.voices ?? []).map((v) => (
                        <option key={v.id} value={v.id}>{v.name}</option>
                      ))}
                    </Select>
                  )}
                </Field>
                {!isKokoro && (
                  <Field label="风格指令" hint="选填;自然语言描述语气,如 Very happy">
                    {({ id, ...rest }) => (
                      <Input id={id} value={instruct} onChange={(e) => setInstruct(e.target.value)} {...rest} />
                    )}
                  </Field>
                )}
              </>
            )}
            {!isKokoro && (
              <Field label="语言">
                {({ id, ...rest }) => (
                  <Select id={id} value={language} onChange={(e) => setLanguage(e.target.value)} {...rest}>
                    {(isIndex ? INDEX_LANGS : LANGS).map((l) => (
                      <option key={l.value} value={l.value}>{l.label}</option>
                    ))}
                  </Select>
                )}
              </Field>
            )}
            {isIndex && (
              <>
                <Field label="情感文本" hint="选填;描述朗读情感,如:用开心的语气朗读">
                  {({ id, ...rest }) => (
                    <Input
                      id={id}
                      value={emotionText}
                      onChange={(e) => setEmotionText(e.target.value)}
                      placeholder="如:用开心的语气朗读"
                      {...rest}
                    />
                  )}
                </Field>
                <Field label="情感强度" hint="选填;0-1,步进 0.1,留空使用引擎默认">
                  {({ id, ...rest }) => (
                    <Input
                      id={id}
                      type="number"
                      min={0}
                      max={1}
                      step={0.1}
                      value={emotionAlpha}
                      onChange={(e) => setEmotionAlpha(e.target.value)}
                      placeholder="0 - 1"
                      {...rest}
                    />
                  )}
                </Field>
              </>
            )}
            <Button variant="primary" loading={submit.isPending} disabled={!canSubmit} onClick={() => submit.mutate()}>
              开始合成
            </Button>
          </CardBody>
        </Card>
      </div>

      {/* 命名弹窗:入库保存(录制/上传完成)与改名共用 */}
      <Modal
        open={nameModal !== null}
        title={nameModal?.kind === "rename" ? "重命名音色" : "保存音色到音色库"}
        onClose={() => {
          if (!namePending) setNameModal(null);
        }}
        footer={
          <>
            <Button variant="ghost" onClick={() => setNameModal(null)} disabled={namePending}>
              取消
            </Button>
            <Button variant="primary" loading={namePending} disabled={!nameValue.trim()} onClick={confirmName}>
              保存
            </Button>
          </>
        }
      >
        <Field
          label="音色名称"
          hint={nameModal?.kind === "save" ? "入库时转码 24kHz 单声道;成品需 1-60 秒" : undefined}
        >
          {({ id }) => (
            <Input
              id={id}
              value={nameValue}
              onChange={(e) => setNameValue(e.target.value)}
              placeholder="如:我的播音音色"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === "Enter" && nameValue.trim() && !namePending) confirmName();
              }}
            />
          )}
        </Field>
      </Modal>

      {/* 删除确认:文案含音色名,防误删 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除音色"
        description={`确定删除音色「${deleteTarget?.name ?? ""}」?音色文件将被移除,删除后不可恢复。`}
        confirmLabel="删除"
        loading={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
