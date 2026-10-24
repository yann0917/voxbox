import type { Artifact } from "./types";

/** 场景的一步：一次任务提交。wants 声明本步完成时取哪类产物喂给下一步
 *  （artifact_input 通道）；goto 声明全链完成后的跳转（如「切高潮」直达剪辑页）。 */
export interface ScenarioStep {
  key: string;
  label: string;
  provider: string;
  tool: string;
  params: Record<string, unknown>;
  wants?: string;
  goto?: (arts: Artifact[]) => string | null;
}

export interface Scenario {
  title: string;
  steps: ScenarioStep[];
  /** 末步为识别类任务时，完成后自动跑一次 AI 总结（SSE 流进执行卡） */
  autoSummary?: boolean;
}

/** 识别步骤的引擎口径：本地识别无凭证离线可用（任意时长）；
 *  火山一句话版吃本地文件但有 60s 上限；更长的云端识别需对象存储中转。 */
export interface AsrChoice {
  engine: "local" | "volcengine";
}

function asrStep({ engine }: AsrChoice): ScenarioStep {
  return engine === "local"
    ? {
        key: "asr",
        label: "本地识别",
        provider: "local",
        tool: "asr",
        params: { model: "sensevoice", language: "auto", itn: true },
        wants: "subtitle",
      }
    : {
        key: "asr",
        label: "语音识别",
        provider: "volcengine",
        tool: "asr",
        params: { srt: true, language: "", version: "sentence" },
        wants: "subtitle",
      };
}

/** 链接 → 字幕稿：下载音频 → 识别出字幕/文稿 → 自动 AI 总结。 */
export function linkSubtitleScenario(url: string, asr: AsrChoice, autoSummary = true): Scenario {
  return {
    title: "链接 → 字幕稿",
    autoSummary,
    steps: [
      {
        key: "download",
        label: "下载音频",
        provider: "downloader",
        tool: "download",
        params: { url, backend: "auto", mode: "audio", max_items: 1, subtitles: false },
        wants: "audio",
      },
      asrStep(asr),
    ],
  };
}

/** 歌曲 → 伴奏人声：下载歌曲 → 人声分离（人声+伴奏双轨）。 */
export function songStemsScenario(track: { source: string; rid: string; name: string; artist: string; duration: number }, engine: string): Scenario {
  return {
    title: `歌曲 → 伴奏人声（${track.name}）`,
    steps: [
      {
        key: "download",
        label: "下载歌曲",
        provider: "music",
        tool: "music_download",
        params: {
          source: track.source,
          rid: track.rid,
          name: track.name,
          artist: track.artist,
          duration_ms: track.duration,
        },
        wants: "audio",
      },
      {
        key: "separate",
        label: "人声分离",
        provider: engine,
        tool: "separate",
        params: { scene: "Music", output_format: "mp3" },
        wants: "audio",
      },
    ],
  };
}

/** 歌曲切高潮：下载后直跳音频剪辑「切高潮」页（旧音乐搜索 prepare 流程的复活）。 */
export function songClipScenario(track: { source: string; rid: string; name: string; artist: string; duration: number }): Scenario {
  return {
    title: `切高潮（${track.name}）`,
    steps: [
      {
        key: "download",
        label: "下载歌曲",
        provider: "music",
        tool: "music_download",
        params: {
          source: track.source,
          rid: track.rid,
          name: track.name,
          artist: track.artist,
          duration_ms: track.duration,
        },
        wants: "audio",
        goto: (arts) => {
          const audio = arts.find((a) => a.kind === "audio");
          return audio ? `/post?tab=clip&artifact=${audio.id}` : null;
        },
      },
    ],
  };
}

/** 抖音链接 → 转写笔记：下载音频 → 识别 → 自动 AI 总结。 */
export function douyinNotesScenario(url: string, asr: AsrChoice, autoSummary = true): Scenario {
  return {
    title: "抖音链接 → 转写笔记",
    autoSummary,
    steps: [
      {
        key: "download",
        label: "下载音频",
        provider: "downloader",
        tool: "download",
        params: { url, backend: "douyin", mode: "audio", max_items: 1 },
        wants: "audio",
      },
      asrStep(asr),
    ],
  };
}
