import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, AudioLines, Upload } from "lucide-react";
import { Link } from "react-router-dom";
import { fetchJSON } from "../../lib/api";
import { useLocalReady, useLocalVoices, useModels } from "../../lib/models";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  Input,
  Select,
  Textarea,
  useToast,
} from "../../ui";

const LANGS = [
  { value: "Chinese", label: "中文" },
  { value: "English", label: "英文" },
  { value: "Japanese", label: "日语" },
  { value: "Korean", label: "韩语" },
];

/** 本地语音合成面板:audio.cpp 引擎 + 已安装 Qwen3-TTS,克隆(参考音频)或预置音色。 */
export default function LocalTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState("");
  const [mode, setMode] = useState<"clone" | "preset">("clone");
  const [refFile, setRefFile] = useState<File | null>(null);
  const [refText, setRefText] = useState("");
  const [speaker, setSpeaker] = useState("");
  const [instruct, setInstruct] = useState("");
  const [language, setLanguage] = useState("Chinese");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const { toast } = useToast();
  const qc = useQueryClient();
  const ev = useTaskEvents();

  const ready = useLocalReady("tts");
  const voices = useLocalVoices();
  const models = useModels();
  const installedModels = (models.data?.items ?? []).filter(
    (m) => m.kind === "tts" && m.status === "installed",
  );
  // 已安装模型唯一时自动选中;多模型时默认第一个
  useEffect(() => {
    if (!model && installedModels.length > 0) setModel(installedModels[0].id);
  }, [installedModels, model]);

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
        text: text.trim(), model, mode, language,
      };
      if (mode === "clone" && refText.trim()) params.ref_text = refText.trim();
      if (mode === "preset") {
        params.speaker = speaker;
        if (instruct.trim()) params.instruct = instruct.trim();
      }
      // 克隆参考音频先上传拿 file_id(不设 Content-Type,让浏览器带 multipart boundary)
      let file_ids: string[] | undefined;
      if (mode === "clone" && refFile) {
        const fd = new FormData();
        fd.append("file", refFile);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        file_ids = [up.file_id];
      }
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "local", tool: "tts", params, file_ids }),
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

  const canSubmit = text.trim() !== "" && model !== "" && (mode === "preset" ? speaker !== "" : refFile !== null);
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
            <Field label="音色模式">
              {({ id, ...rest }) => (
                <Select id={id} value={mode} onChange={(e) => setMode(e.target.value as "clone" | "preset")} {...rest}>
                  <option value="clone">参考音频克隆</option>
                  <option value="preset">预置音色</option>
                </Select>
              )}
            </Field>
            {mode === "clone" ? (
              <>
                <Field label="参考音频" hint="3-60 秒清晰人声,自动转 24kHz 单声道">
                  {({ id, ...rest }) => (
                    <div className="flex items-center gap-2">
                      <Button
                        type="button"
                        size="sm"
                        variant="secondary"
                        icon={<Upload size={13} strokeWidth={1.75} />}
                        onClick={() => document.getElementById(id)?.click()}
                      >
                        {refFile ? refFile.name : "选择音频文件"}
                      </Button>
                      <input
                        id={id}
                        type="file"
                        accept="audio/*,.wav,.mp3,.m4a,.flac"
                        className="hidden"
                        onChange={(e) => setRefFile(e.target.files?.[0] ?? null)}
                        {...rest}
                      />
                    </div>
                  )}
                </Field>
                <Field label="参考音频转写" hint="选填;填了克隆更准,留空走纯音色克隆">
                  {({ id, ...rest }) => (
                    <Input id={id} value={refText} onChange={(e) => setRefText(e.target.value)} placeholder="参考音频实际说的内容" {...rest} />
                  )}
                </Field>
              </>
            ) : (
              <>
                <Field label="预置音色">
                  {({ id, ...rest }) => (
                    <Select id={id} value={speaker} onChange={(e) => setSpeaker(e.target.value)} {...rest}>
                      <option value="">选择音色…</option>
                      {(voices.data?.voices ?? []).map((v) => (
                        <option key={v.id} value={v.id}>{v.name}</option>
                      ))}
                    </Select>
                  )}
                </Field>
                <Field label="风格指令" hint="选填;自然语言描述语气,如 Very happy">
                  {({ id, ...rest }) => (
                    <Input id={id} value={instruct} onChange={(e) => setInstruct(e.target.value)} {...rest} />
                  )}
                </Field>
              </>
            )}
            <Field label="语言">
              {({ id, ...rest }) => (
                <Select id={id} value={language} onChange={(e) => setLanguage(e.target.value)} {...rest}>
                  {LANGS.map((l) => (
                    <option key={l.value} value={l.value}>{l.label}</option>
                  ))}
                </Select>
              )}
            </Field>
            <Button variant="primary" loading={submit.isPending} disabled={!canSubmit} onClick={() => submit.mutate()}>
              开始合成
            </Button>
          </CardBody>
        </Card>
      </div>
    </>
  );
}
