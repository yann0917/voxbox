/**
 * 火山引擎豆包语音刊例价快照（voxbox 各工具对应计费项）。
 *
 * 来源（抓取于 2026-09-13，价格以火山账单为准）：
 * - 计费概述 https://www.volcengine.com/docs/6561/1359369
 * - 计费说明 https://www.volcengine.com/docs/6561/1359370
 *
 * 官方文档未给出「接口 ↔ 商品」的权威映射；同步合成（tts，V1 接口）的计费商品
 * 随音色代际而异，测算时按口径选择，页面已注明「以账单为准」。
 * 机器翻译对应「豆包机器翻译模型」：输入 1.8 元/百万 token、输出 5.4 元/百万 token（1359370）。
 */

export const PRICE_SNAPSHOT_DATE = "2026-09-13";

/** 千问/小米刊例价快照日（模型市场与 Pay-As-You-Go 页抓取）。 */
export const CLOUD_PRICE_SNAPSHOT_DATE = "2026-09-25";

export const PRICING_SOURCES = [
  { label: "火山计费概述", url: "https://www.volcengine.com/docs/6561/1359369" },
  { label: "火山计费说明", url: "https://www.volcengine.com/docs/6561/1359370" },
  { label: "音频工具计费（人声分离）", url: "https://www.volcengine.com/docs/6448/2486469" },
  { label: "千问计费说明", url: "https://platform.qianwenai.com/docs/developer-guides/getting-started/pricing" },
  { label: "小米 Pay-As-You-Go", url: "https://mimo.mi.com/docs/zh-CN/price/pay-as-you-go" },
];

/* ---------------- 千问平台（platform.qianwenai.com，快照 2026-09-25） ---------------- */

/** qwen3-tts-flash / instruct-flash 单价（模型市场页：0.8 元/万字符，输出不计费）。
 *  计费口径：按输入文本字符数，汉字计 2 字符、其余 1——中文估算按 2 倍折算。 */
export const QWEN_TTS_PRICE_PER_WAN = 0.8;
/** 中文文本的计费字符倍率（汉字计 2 字符；估算口径，以账单为准）。 */
export const QWEN_TTS_CJK_RATIO = 2;

/** qwen3-asr-flash-filetrans 单价（模型市场页：0.00022 元/秒，按输入音频时长，输出不计费）。 */
export const QWEN_ASR_PRICE_PER_SECOND = 0.00022;
/** 折算小时价（×3600），供与火山/小米同口径对比。 */
export const QWEN_ASR_PRICE_PER_HOUR = 0.792;

/** qwen-audio-3.1-asr-flash-filetrans 按 token 计费（非时长口径，仅列示不做时长折算估算）。 */
export const QWEN_AUDIO_ASR = { inputPerMillion: 0.8, outputPerMillion: 2.7 };

/* ---------------- 小米 MiMo（platform.xiaomimimo.com，快照 2026-09-25） ---------------- */

/** MiMo-V2.5-TTS 全系（tts/voicedesign/voiceclone）限时免费（官方 Pay-As-You-Go 页未列单价）。 */
export const MIMO_TTS_FREE_NOTE = "限时免费（截至快照日官方未列单价）";

/** mimo-v2.5-asr 单价：按输入音频时长折算小时计费（精确到秒），国内 0.5 元/小时。 */
export const MIMO_ASR_PRICE_PER_HOUR = 0.5;

/** 资源包档位：size 为计费单位数，price 为资源包价格（元）。 */
export interface Pack {
  size: number;
  price: number;
}

/** 一个计费项：后付费单价（可阶梯）+ 资源包 + 试用额度。 */
export interface PriceItem {
  /** 展示名（官方商品名） */
  label: string;
  /** 计费单位 */
  unit: "万字符" | "千次" | "小时" | "分钟" | "百万token";
  /** 后付费单价（元/单位）；阶梯时为多段 */
  postpaid: { upTo?: number; price: number }[];
  /** 资源包档位（折算单价 = price/size） */
  packs: Pack[];
  /** 试用额度文案 */
  trial: string;
  note?: string;
}

/** 求资源包最优折算单价（元/单位）。 */
export function bestPackUnitPrice(item: PriceItem): number {
  return Math.min(...item.packs.map((p) => p.price / p.size));
}

/** 求后付费单价（元/单位）：取用量命中的阶梯单价（阶梯 upTo 为累计用量上界）。 */
export function postpaidUnitPrice(item: PriceItem, quantity: number): number {
  for (const tier of item.postpaid) {
    if (tier.upTo == null || quantity <= tier.upTo) return tier.price;
  }
  return item.postpaid[item.postpaid.length - 1].price;
}

/* ---------------- 同步合成（tts，V1 接口）的三种计费口径 ---------------- */

/** 口径 A：豆包音频生成模型 1.0（大模型 1.0 音色，按生成音频分钟数）。 */
export const PRICE_SYNC_BY_MINUTE: PriceItem = {
  label: "豆包音频生成模型1.0（按音频时长）",
  unit: "分钟",
  postpaid: [{ price: 1 }],
  packs: [
    { size: 30, price: 30 },
    { size: 200, price: 190 },
    { size: 200000, price: 180000 },
    { size: 500000, price: 425000 },
    { size: 2000000, price: 1600000 },
  ],
  trial: "60 分钟 / 半年",
  note: "按模型生成的原始时长计费，倍速不影响计费时长",
};

/** 口径 B：大模型语音合成（按字符）。 */
export const PRICE_SYNC_BY_CHARS: PriceItem = {
  label: "大模型语音合成（按字符）",
  unit: "万字符",
  postpaid: [{ price: 5 }],
  packs: [
    { size: 10, price: 45 },
    { size: 200, price: 800 },
    { size: 2000, price: 7000 },
    { size: 20000, price: 60000 },
    { size: 100000, price: 200000 },
    { size: 300000, price: 480000 },
  ],
  trial: "20000 字符 / 半年",
};

/** 口径 C：语音合成（历史小模型，基础音色按次）。 */
export const PRICE_SYNC_BY_CALLS: PriceItem = {
  label: "语音合成（历史小模型，按次）",
  unit: "千次",
  postpaid: [
    { upTo: 1000, price: 5.5 },
    { upTo: 5000, price: 5 },
    { upTo: 10000, price: 4.5 },
    { price: 4 },
  ],
  packs: [
    { size: 12500, price: 50000 },
    { size: 30000, price: 90000 },
    { size: 100000, price: 200000 },
  ],
  trial: "20000 次 / 半年",
  note: "仅基础音色（非大模型音色）走此商品",
};

/* ---------------- 2.0 合成（流式 / 长文本共用） ---------------- */

export const PRICE_TTS_20: PriceItem = {
  label: "豆包语音合成模型2.0（流式/长文本）",
  unit: "万字符",
  postpaid: [{ price: 3 }],
  packs: [
    { size: 10, price: 28 },
    { size: 2000, price: 5400 },
    { size: 20000, price: 48000 },
    { size: 200000, price: 420000 },
  ],
  trial: "20000 字符 / 半年",
  note: "复刻音色走豆包声音复刻模型2.0，单价相同",
};

/* ---------------- ASR 三版本（大模型录音文件识别，按语音时长） ---------------- */

export const PRICE_ASR_STANDARD: PriceItem = {
  label: "大模型录音文件识别（标准版）",
  unit: "小时",
  postpaid: [{ price: 2.3 }],
  packs: [
    { size: 30, price: 66 },
    { size: 1000, price: 2000 },
    { size: 10000, price: 18000 },
    { size: 100000, price: 140000 },
  ],
  trial: "20 小时 / 半年",
};

export const PRICE_ASR_FLASH: PriceItem = {
  label: "大模型录音文件识别（极速版）",
  unit: "小时",
  postpaid: [{ price: 4.5 }],
  packs: [
    { size: 30, price: 132 },
    { size: 1000, price: 4300 },
    { size: 10000, price: 36000 },
    { size: 100000, price: 280000 },
  ],
  trial: "20 小时 / 半年",
};

export const PRICE_ASR_IDLE: PriceItem = {
  label: "大模型录音文件识别（闲时版）",
  unit: "小时",
  postpaid: [{ price: 1.2 }],
  packs: [
    { size: 30, price: 35 },
    { size: 10000, price: 10000 },
    { size: 100000, price: 80000 },
  ],
  trial: "20 小时 / 半年",
  note: "闲时算力执行，任务 24h 内完成",
};

/* ---------------- 播客（豆包语音播客大模型，按 token） ---------------- */

export const PODCAST_PRICES = {
  /** 输入-文本：元/百万 token（官方未给折算，按 1 字符 ≈ 1 token 估算） */
  inputTextPerMillion: 120,
  /** 输出-音频：元/百万 token；官方折算 1 秒 ≈ 25 token */
  outputAudioPerMillion: 100,
  tokensPerAudioSecond: 25,
  trial: "100 万 token / 半年",
};

/** 播客估算：输入字符数 + 成片秒数 → 元（粗估，以账单为准）。 */
export function estimatePodcast(inputChars: number, audioSeconds: number): number {
  const inputTokens = inputChars;
  const outputTokens = audioSeconds * PODCAST_PRICES.tokensPerAudioSecond;
  return (
    (inputTokens * PODCAST_PRICES.inputTextPerMillion) / 1_000_000 +
    (outputTokens * PODCAST_PRICES.outputAudioPerMillion) / 1_000_000
  );
}

/* ---------------- 机器翻译（豆包机器翻译模型，按 token） ---------------- */

/** 豆包机器翻译模型：按 token 计费（官方 6561/1359370 计费说明，快照 2026-09-13）。
 *  后付费输入 1.8 元/百万 token、输出 5.4 元/百万 token；资源包折算最低 1.26。
 *  文本 token 折算类似文本大模型：字符数 ≈ token 数（分词策略不同会有偏差），
 *  故估算按「输入字符数 = 输入 token、输出按译文长度」口径，页面已注明为粗估。 */
export const PRICE_MT: PriceItem = {
  label: "豆包机器翻译模型",
  unit: "百万token",
  postpaid: [{ price: 1.8 }],
  packs: [
    { size: 20000, price: 32400 }, // 200 亿 token → 1.62 元/百万
    { size: 100000, price: 144000 }, // 1000 亿 token → 1.44 元/百万
    { size: 200000, price: 252000 }, // 2000 亿 token → 1.26 元/百万
  ],
  trial: "100 万 token（不区分输入输出）/ 半年",
  note: "输入 1.8 元/百万 token、输出 5.4 元/百万 token（后付费）；资源包仅按总量抵扣",
};

/** 输出 token 相对输入的用户体感：译文长度波动，按 1:1 粗估（官方未给固定倍率）。 */
export const MT_OUTPUT_TOKEN_RATIO = 1;

/**
 * 机器翻译费用粗估（元）：输入 + 输出分别按 token 单价计。
 * @param inputChars  原文字符数（≈输入 token 数）
 * @param outputChars 译文长度（≈输出 token 数）
 */
export function estimateMT(inputChars: number, outputChars: number): number {
  const inputTokens = inputChars;
  const outputTokens = outputChars * MT_OUTPUT_TOKEN_RATIO;
  return (inputTokens * PRICE_MT.postpaid[0].price) / 1_000_000 + (outputTokens * MT_OUTPUT_PRICE) / 1_000_000;
}

/** 输出单价（元/百万 token）：后付费与输入不同价，独立列出以免误用 PriceItem 首项。 */
export const MT_OUTPUT_PRICE = 5.4;

/* ---------------- 语音妙记（豆包语音妙记模型，按小时） ---------------- */

/** 妙记计费（官方 6561/1359370，快照 2026-09-13）：
 *  音频文件转写为必选功能 1.8 元/小时；音频结构（总结/待办/章节等附加功能）
 *  单功能 0.11 元/小时可叠加，或集合 0.5 元/小时二选一。
 *  AllActivate=true 按打包价（对应结构集合口径，页面注明为推断）；视频价格官方未单列，按音频口径估算。 */
export const MINUTES_PRICE = {
  /** 转写（必选）：元/小时 */
  transcriptionPerHour: 1.8,
  /** 音频结构-单功能：元/小时/功能 */
  structureSinglePerHour: 0.11,
  /** 音频结构-集合（打包）：元/小时 */
  structureBundlePerHour: 0.5,
  trial: "以控制台为准",
};

/** 妙记估算（元）：时长（分钟）×（转写 + 结构计费口径）。
 * @param minutes    音视频时长
 * @param featureCount 附加功能数
 * @param allActivate   是否打包计费（true 按结构集合价） */
export function estimateMinutes(minutes: number, featureCount: number, allActivate: boolean): number {
  const h = Math.max(0, minutes) / 60;
  const structure = allActivate
    ? MINUTES_PRICE.structureBundlePerHour
    : Math.max(1, featureCount) * MINUTES_PRICE.structureSinglePerHour;
  return h * (MINUTES_PRICE.transcriptionPerHour + structure);
}

/* ---------------- 人声分离（AI MediaKit 音频工具） ---------------- */

/** 人声背景音分离：按输入文件时长计费（官方 6448/2486469，快照 2026-09-13）。
 *  无阶梯与资源包；计费与输出轨道数无关，公式为 输入时长（分钟）× 0.07。 */
export const SEPARATE_PRICE_PER_MINUTE = 0.07;
export const SEPARATE_TRIAL = "无公开试用额度，以控制台为准";

/* ---------------- TTS 三通道（合并页 Tab 说明与对比共用） ---------------- */

export interface TTSChannelInfo {
  key: "sync" | "stream" | "long";
  tab: string;
  tool: string;
  tagline: string;
  /** 计费说明（一句话，显示在 Tab 说明条） */
  pricing: string;
  /** 适用建议 */
  advice: string;
}

/** 中文 TTS 平均语速约 250 字/分钟，用于「按时长」口径的粗估（可在测算页调整）。 */
export const CHARS_PER_AUDIO_MINUTE = 250;

/** 同步通道按 1000 字符本地分段：分段会产生多次调用（按次/按时长口径均受影响）。 */
export const SYNC_SEGMENT_CHARS = 1000;

export const TTS_CHANNELS: TTSChannelInfo[] = [
  {
    key: "sync",
    tab: "同步合成",
    tool: "tts",
    tagline: "短文本秒级返回，1.0 音色为主，支持 WAV",
    pricing: "计费随音色代际：大模型音色按音频时长（约 1 元/分钟）或按字符（5 元/万字符）；基础音色按次（5.5 元/千次起）。超过 1000 字自动分段，分段会多次计费。以账单为准。",
    advice: "适合几句话的即时配音；长文本费用与耗时都不划算，请改用流式/长文本通道。",
  },
  {
    key: "stream",
    tab: "流式合成",
    tool: "tts_stream",
    tagline: "低延迟流式返回，20 语种、8 方言、字级字幕、语音指令",
    pricing: "豆包语音合成模型2.0：3 元/万字符（复刻音色同价）；试用额度 2 万字符。",
    advice: "适合交互感场景与多语种/方言需求；同为 2.0 模型，单价与长文本一致。",
  },
  {
    key: "long",
    tab: "长文本合成",
    tool: "tts_long",
    tagline: "10 万字以内异步合成，分句时间戳出 SRT",
    pricing: "豆包语音合成模型2.0：3 元/万字符（复刻音色同价）；一次任务一次提交，无分段损耗。",
    advice: "适合书稿/文章转有声内容；异步分钟级完成，价格与流式一致。",
  },
];
