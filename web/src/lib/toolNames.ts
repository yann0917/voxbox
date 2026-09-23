/** 工具名 → 中文名（HistoryPage 筛选器与全局任务通知共用）。 */
export const toolName: Record<string, string> = {
  tts: "语音合成",
  tts_long: "长文本合成",
  tts_stream: "流式合成",
  asr: "语音识别",
  podcast: "播客工坊",
  separate: "人声分离",
  trim: "音频切割",
  merge: "音频合并",
  pitch: "变调变速",
  analyze: "调与BPM查询",
  equalizer: "均衡器",
  volume: "音量与响度",
  fade: "淡入淡出",
  reverse: "倒放",
  translate: "机器翻译",
  minutes: "语音妙记",
};

export const toolLabel = (t: string): string => toolName[t] ?? t;

/** 工具名 → 控制台路由（历史页重跑后跳转等跨页导航用）。 */
export const toolRoute: Record<string, string> = {
  tts: "/tts",
  tts_long: "/tts?tab=long",
  tts_stream: "/tts?tab=stream",
  asr: "/asr",
  podcast: "/podcast",
  separate: "/separate",
  trim: "/audio-edit",
  merge: "/audio-edit",
  pitch: "/audio-edit",
  analyze: "/audio-edit",
  equalizer: "/audio-edit",
  volume: "/audio-edit",
  fade: "/audio-edit",
  reverse: "/audio-edit",
  translate: "/translate",
  minutes: "/minutes",
};
