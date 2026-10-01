/** 与后端 DTO 对齐的共享类型（internal/server/dto.go）。 */

export type TaskStatus = "pending" | "running" | "succeeded" | "failed" | "canceled" | "interrupted";

export interface Task {
  id: string;
  provider: string;
  tool: string;
  /** 人类可读标题（URL/文本摘要），列表与搜索展示 */
  title?: string;
  status: TaskStatus;
  progress: number;
  progress_note: string;
  error?: string;
  cost_ms: number;
  created_at: string;
  /** 提交参数 JSON（重跑与回放溯源用） */
  params?: Record<string, unknown>;
  /** 原始输入引用：上传文件 / 跨工具产物（URL 输入直接在 params.url） */
  input?: {
    file_ids?: string[];
    artifact_input?: string;
  };
  /** 仅任务详情接口返回：provider.TaskOutput.Summary 的 JSON */
  summary?: {
    /** 语音识别（asr）：speaker 为上游说话人编号（"0"/"1"… 字符串，未启用分离时缺省） */
    segments?: { text: string; start_ms: number; end_ms: number; speaker?: string }[];
    /** 说话人改名覆盖层（PATCH /api/tasks/:id/speakers 写入，speaker 编号→展示名） */
    speaker_names?: Record<string, string>;
    rounds?: number;
    duration_s?: number;
    duration_ms?: number;
    tracks?: string[];
    scene?: string;
    speakers?: string[];
    source?: string;
    char_count?: number;
    sentence_count?: number;
    synthesized_chars?: number;
    upstream_task_id?: string;
    billed_chars?: number;
    chunks?: number;
    audio_url_fallback?: boolean;
    /** 机器翻译（translate） */
    translation?: string;
    source_language?: string;
    target_language?: string;
    detected_source_language?: string;
    terms_count?: number;
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
    /** 语音妙记（minutes）；segments/duration_ms/upstream_task_id 与上方共用 */
    minutes_title?: string;
    summary_text?: string;
    translation_text?: string;
    features?: string[];
    sentences?: number;
    speakers_count?: number;
    todos?: { content: string; executor: string[]; start_time: number }[];
    chapters?: { title: string; summary: string; start_time: number; end_time: number }[];
    /** 本地识别（local asr）：sherpa-onnx 只回整段文本，无分句时间戳 */
    text?: string;
  };
}

export type ArtifactKind = "audio" | "transcript" | "dialog" | "subtitle" | "translation" | "minutes";

export interface Artifact {
  id: string;
  kind: ArtifactKind;
  filename: string;
  format: string;
  size: number;
  duration_ms: number;
  meta?: { track?: "voice" | "background" | "music" | "sfx"; [k: string]: unknown };
}

export interface TaskDetail {
  task: Task;
  artifacts: Artifact[];
}

export interface Voice {
  id: string;
  name: string;
  gender: string; // 男 | 女
  scenes: string[];
  languages: string[];
  dialects?: string[]; // 中文方言
  tags?: string[]; // 特殊标签（抖音同款/豆包同款…）
  emotions?: string[]; // 1.0 多情感音色支持的情感
  generation: string; // 2.0 | 1.0
  note?: string;
}
