import { useMemo, useState } from "react";
import { Calculator, Coins, ExternalLink, Info, Languages, Mic, NotebookPen, Podcast, Waves } from "lucide-react";
import {
  bestPackUnitPrice,
  CHARS_PER_AUDIO_MINUTE,
  CLOUD_PRICE_SNAPSHOT_DATE,
  estimateMinutes,
  estimateMT,
  estimatePodcast,
  MINUTES_PRICE,
  MIMO_ASR_PRICE_PER_HOUR,
  MIMO_TTS_FREE_NOTE,
  MT_OUTPUT_PRICE,
  PODCAST_PRICES,
  postpaidUnitPrice,
  PRICE_ASR_FLASH,
  PRICE_ASR_IDLE,
  PRICE_ASR_STANDARD,
  PRICE_MT,
  PRICE_SNAPSHOT_DATE,
  PRICING_SOURCES,
  PRICE_SYNC_BY_CALLS,
  PRICE_SYNC_BY_CHARS,
  PRICE_SYNC_BY_MINUTE,
  PRICE_TTS_20,
  QWEN_AUDIO_ASR,
  QWEN_ASR_PRICE_PER_HOUR,
  QWEN_TTS_CJK_RATIO,
  QWEN_TTS_PRICE_PER_WAN,
  SEPARATE_PRICE_PER_MINUTE,
  SEPARATE_TRIAL,
  SYNC_SEGMENT_CHARS,
  type PriceItem,
} from "../lib/pricing";
import { Card, CardBody, CardHeader, Field, Input, MicroLabel, PageHeader, Select } from "../ui";

/** 金额格式化：<1 元保留 3 位有效小数，否则 2 位。 */
function fmtYuan(v: number): string {
  if (v === 0) return "¥0";
  if (v < 1) return `¥${v.toFixed(v < 0.1 ? 4 : 3).replace(/0+$/, "").replace(/\.$/, "")}`;
  return `¥${v.toFixed(2)}`;
}

/** 价格表行：一个计费项的后付费 / 资源包最优 / 试用额度。 */
function PriceRows({ items }: { items: PriceItem[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] text-left text-xs">
        <thead>
          <tr className="border-b border-line text-muted">
            <th className="py-2 pr-3 font-normal">商品 / 计费项</th>
            <th className="py-2 pr-3 font-normal">后付费单价</th>
            <th className="py-2 pr-3 font-normal">资源包最低折算</th>
            <th className="py-2 pr-3 font-normal">试用额度</th>
            <th className="py-2 font-normal">备注</th>
          </tr>
        </thead>
        <tbody>
          {items.map((it) => {
            const postpaid =
              it.postpaid.length === 1
                ? `${it.postpaid[0].price} 元/${it.unit}`
                : it.postpaid.map((t) => `${t.price}（≤${t.upTo ?? "∞"}${it.unit}）`).join(" / ");
            return (
              <tr key={it.label} className="border-b border-line/60 last:border-0">
                <td className="py-2 pr-3 text-fg">{it.label}</td>
                <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">{postpaid}</td>
                <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">
                  {it.packs.length > 0 ? `${bestPackUnitPrice(it).toFixed(2)} 元/${it.unit}` : "—"}
                </td>
                <td className="py-2 pr-3 text-muted">{it.trial}</td>
                <td className="py-2 text-muted">{it.note ?? "—"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** TTS 多引擎同量对比：同一字符数走火山三通道与千问/小米的估算费用。 */
function TTSCompare() {
  const [chars, setChars] = useState(10000);
  const n = Math.max(0, chars);

  // 流式/长文本：2.0 模型按字符
  const v20Postpaid = (n / 10000) * postpaidUnitPrice(PRICE_TTS_20, n / 10000);
  const v20Pack = (n / 10000) * bestPackUnitPrice(PRICE_TTS_20);

  // 同步·按时长口径：字数折算音频分钟数（约 250 字/分钟）
  const minutes = n / CHARS_PER_AUDIO_MINUTE;
  const syncMinutePostpaid = minutes * postpaidUnitPrice(PRICE_SYNC_BY_MINUTE, minutes);
  const syncMinutePack = minutes * bestPackUnitPrice(PRICE_SYNC_BY_MINUTE);
  // 同步·按字符口径
  const wanChars = n / 10000;
  const syncCharsPostpaid = wanChars * postpaidUnitPrice(PRICE_SYNC_BY_CHARS, wanChars);
  // 同步·按次口径（基础音色）：1000 字符分段 → 多次调用
  const calls = Math.ceil(n / SYNC_SEGMENT_CHARS);
  const syncCallsPostpaid = (calls / 1000) * postpaidUnitPrice(PRICE_SYNC_BY_CALLS, calls / 1000);

  // 千问：0.8 元/万计费字符；汉字计 2 字符（中文场景按 2 倍折算，以账单为准）
  const qwCost = ((n * QWEN_TTS_CJK_RATIO) / 10000) * QWEN_TTS_PRICE_PER_WAN;

  const rows: { name: string; desc: string; postpaid: number; pack: number | null; accent?: boolean }[] = [
    {
      name: "火山 · 同步合成（按时长口径）",
      desc: `≈ ${minutes.toFixed(1)} 分钟（${CHARS_PER_AUDIO_MINUTE} 字/分钟）`,
      postpaid: syncMinutePostpaid,
      pack: syncMinutePack,
      accent: true,
    },
    { name: "火山 · 流式合成", desc: "2.0 模型按字符", postpaid: v20Postpaid, pack: v20Pack },
    { name: "火山 · 长文本合成", desc: "2.0 模型按字符，一次提交", postpaid: v20Postpaid, pack: v20Pack },
    {
      name: "千问 · qwen3-tts-flash",
      desc: "0.8 元/万计费字符，汉字计 2 字符",
      postpaid: qwCost,
      pack: null,
    },
    {
      name: "小米 · MiMo-V2.5-TTS",
      desc: "限时免费（预置音色 / voicedesign 同价）",
      postpaid: 0,
      pack: null,
    },
  ];

  return (
    <Card>
      <CardHeader
        title="语音合成多引擎对比"
        icon={<Coins size={15} strokeWidth={1.75} />}
        aside={<span className="micro">同量文本 · 火山/千问/小米</span>}
      />
      <CardBody className="space-y-3">
        <Field label="字符数" hint="拖动或输入要合成的文本量">
          {({ id }) => (
            <Input
              id={id}
              type="number"
              min={0}
              step={1000}
              value={String(chars)}
              onChange={(e) => setChars(Number(e.target.value || 0))}
            />
          )}
        </Field>
        <div className="space-y-2">
          {rows.map((r) => (
            <div
              key={r.name}
              className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2"
            >
              <span className={`text-sm ${r.accent ? "text-fg" : "text-fg"}`}>{r.name}</span>
              <span className="min-w-0 flex-1 truncate text-[11px] text-muted">{r.desc}</span>
              <span className="font-mono text-sm tabular-nums text-fg">{fmtYuan(r.postpaid)}</span>
              <span className="font-mono text-[11px] tabular-nums text-muted">
                {r.pack != null ? `包后 ≈ ${fmtYuan(r.pack)}` : ""}
              </span>
            </div>
          ))}
        </div>
        <p className="text-[11px] leading-relaxed text-muted">
          同步通道的其他口径（以 {chars} 字符计）：按字符 ≈ {fmtYuan(syncCharsPostpaid)}（大模型语音合成 5 元/万字符）；
          基础音色按次 ≈ {fmtYuan(syncCallsPostpaid)}（超 1000 字自动分段为 {calls} 次调用，5.5 元/千次起）。
          「包后」为资源包最低折算单价的理论值，实际以抵扣顺序与账单为准。
        </p>
      </CardBody>
    </Card>
  );
}



/** ASR 估算：引擎（火山三版本/千问 filetrans/小米）+ 时长，同口径按小时对比。 */
function ASREstimator() {
  type AsrEngine = "standard" | "flash" | "idle" | "qianwen" | "xiaomi";
  const [engine, setEngine] = useState<AsrEngine>("standard");
  const [hours, setHours] = useState(10);
  const h = Math.max(0, hours);

  const volcItem = engine === "standard" ? PRICE_ASR_STANDARD : engine === "flash" ? PRICE_ASR_FLASH : PRICE_ASR_IDLE;
  const isVolc = engine === "standard" || engine === "flash" || engine === "idle";
  const postpaid = isVolc ? h * postpaidUnitPrice(volcItem, h) : h * (engine === "qianwen" ? QWEN_ASR_PRICE_PER_HOUR : MIMO_ASR_PRICE_PER_HOUR);
  const engineLabel = isVolc ? volcItem.label : engine === "qianwen" ? "千问 qwen3-asr-flash-filetrans（0.00022 元/秒）" : "小米 mimo-v2.5-asr";

  return (
    <Card>
      <CardHeader title="语音识别测算" icon={<Mic size={15} strokeWidth={1.75} />} aside={<span className="micro">按语音时长</span>} />
      <CardBody className="space-y-3">
        <div className="grid grid-cols-2 gap-2">
          <Field label="识别引擎">
            {({ id }) => (
              <Select id={id} value={engine} onChange={(e) => setEngine(e.target.value as AsrEngine)}>
                <option value="standard">火山 · 标准版（2.3 元/小时）</option>
                <option value="flash">火山 · 极速版（4.5 元/小时）</option>
                <option value="idle">火山 · 闲时版（1.2 元/小时）</option>
                <option value="qianwen">千问 · filetrans（≈0.79 元/小时）</option>
                <option value="xiaomi">小米 · mimo-asr（0.5 元/小时）</option>
              </Select>
            )}
          </Field>
          <Field label="音频时长（小时）">
            {({ id }) => (
              <Input id={id} type="number" min={0} step={1} value={String(hours)} onChange={(e) => setHours(Number(e.target.value || 0))} />
            )}
          </Field>
        </div>
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <p>
            <MicroLabel>后付费</MicroLabel>
            <span className="ml-2 font-mono text-xl tabular-nums text-fg">{fmtYuan(postpaid)}</span>
          </p>
          <p className="text-[11px] text-muted">
            {engineLabel}
            {isVolc && (
              <>
                {" "}· 资源包最低折算 ≈ <span className="font-mono tabular-nums">{fmtYuan(h * bestPackUnitPrice(volcItem))}</span> · 试用额度 {volcItem.trial}
              </>
            )}
            {engine === "qianwen" && " · qwen-audio 系按 token 计费，未按时长折算"}
          </p>
        </div>
      </CardBody>
    </Card>
  );
}

/** 播客估算：输入字符 + 预计成片时长。 */
function PodcastEstimator() {
  const [inputChars, setInputChars] = useState(3000);
  const [audioMinutes, setAudioMinutes] = useState(5);
  const seconds = Math.max(0, audioMinutes) * 60;
  const cost = estimatePodcast(Math.max(0, inputChars), seconds);
  const inputCost = (Math.max(0, inputChars) * PODCAST_PRICES.inputTextPerMillion) / 1_000_000;
  const outputCost =
    ((seconds * PODCAST_PRICES.tokensPerAudioSecond) * PODCAST_PRICES.outputAudioPerMillion) / 1_000_000;

  return (
    <Card>
      <CardHeader title="播客工坊测算" icon={<Podcast size={15} strokeWidth={1.75} />} aside={<span className="micro">按 token</span>} />
      <CardBody className="space-y-3">
        <div className="grid grid-cols-2 gap-2">
          <Field label="输入文本（字符）" hint="1 字符 ≈ 1 token 估算">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={0}
                step={500}
                value={String(inputChars)}
                onChange={(e) => setInputChars(Number(e.target.value || 0))}
              />
            )}
          </Field>
          <Field label="预计成片（分钟）" hint="输出音频 1 秒 ≈ 25 token">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={0}
                step={1}
                value={String(audioMinutes)}
                onChange={(e) => setAudioMinutes(Number(e.target.value || 0))}
              />
            )}
          </Field>
        </div>
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <p>
            <MicroLabel>估算合计</MicroLabel>
            <span className="ml-2 font-mono text-xl tabular-nums text-fg">{fmtYuan(cost)}</span>
          </p>
          <p className="text-[11px] text-muted">
            输入文本 {fmtYuan(inputCost)} + 输出音频 {fmtYuan(outputCost)} · 试用额度 {PODCAST_PRICES.trial}
          </p>
        </div>
        <p className="text-[11px] text-muted">
          输入-文本 {PODCAST_PRICES.inputTextPerMillion} 元/百万 token，输出-音频 {PODCAST_PRICES.outputAudioPerMillion}{" "}
          元/百万 token；对话稿提炼由模型完成，实际 token 以账单为准。
        </p>
      </CardBody>
    </Card>
  );
}

/** 机器翻译估算：原文与译文长度（输入/输出 token 分别计价）。 */
function MTEstimator() {
  const [inputChars, setInputChars] = useState(2000);
  const [outputChars, setOutputChars] = useState(2000);
  const inputTokens = Math.max(0, inputChars);
  const outputTokens = Math.max(0, outputChars);
  const inputCost = (inputTokens * PRICE_MT.postpaid[0].price) / 1_000_000;
  const outputCost = (outputTokens * MT_OUTPUT_PRICE) / 1_000_000;
  const cost = estimateMT(inputTokens, outputTokens);

  return (
    <Card>
      <CardHeader
        title="机器翻译测算"
        icon={<Languages size={15} strokeWidth={1.75} />}
        aside={<span className="micro">按 token</span>}
      />
      <CardBody className="space-y-3">
        <div className="grid grid-cols-2 gap-2">
          <Field label="原文（字符）" hint="字符数 ≈ 输入 token">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={0}
                step={500}
                value={String(inputChars)}
                onChange={(e) => setInputChars(Number(e.target.value || 0))}
              />
            )}
          </Field>
          <Field label="译文（字符）" hint="译文长度 ≈ 输出 token">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={0}
                step={500}
                value={String(outputChars)}
                onChange={(e) => setOutputChars(Number(e.target.value || 0))}
              />
            )}
          </Field>
        </div>
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <p>
            <MicroLabel>估算合计</MicroLabel>
            <span className="ml-2 font-mono text-xl tabular-nums text-fg">{fmtYuan(cost)}</span>
          </p>
          <p className="text-[11px] text-muted">
            输入 {fmtYuan(inputCost)} + 输出 {fmtYuan(outputCost)} · 试用额度 {PRICE_MT.trial}
          </p>
        </div>
        <p className="text-[11px] text-muted">
          输入 {PRICE_MT.postpaid[0].price} 元/百万 token、输出 {MT_OUTPUT_PRICE} 元/百万 token；
          文本 token 折算类似文本大模型（字符数 ≈ token 数，不同语言分词不同会有偏差），实际以账单为准。
        </p>
      </CardBody>
    </Card>
  );
}

/** 语音妙记估算：时长 ×（转写必选 + 结构按打包/按功能数）。 */
function MinutesEstimator() {
  const [minutes, setMinutes] = useState(60);
  const [featureCount, setFeatureCount] = useState(2);
  const [allActivate, setAllActivate] = useState(true);
  const cost = estimateMinutes(minutes, featureCount, allActivate);
  const structureCost = estimateMinutes(minutes, featureCount, allActivate) - (Math.max(0, minutes) / 60) * MINUTES_PRICE.transcriptionPerHour;

  return (
    <Card>
      <CardHeader
        title="语音妙记测算"
        icon={<NotebookPen size={15} strokeWidth={1.75} />}
        aside={<span className="micro">按小时</span>}
      />
      <CardBody className="space-y-3">
        <div className="grid grid-cols-2 gap-2">
          <Field label="音视频时长（分钟）">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={0}
                step={10}
                value={String(minutes)}
                onChange={(e) => setMinutes(Number(e.target.value || 0))}
              />
            )}
          </Field>
          <Field label="附加功能数" hint="总结/待办/问答/章节/翻译">
            {({ id }) => (
              <Input
                id={id}
                type="number"
                min={1}
                max={5}
                value={String(featureCount)}
                onChange={(e) => setFeatureCount(Number(e.target.value || 1))}
              />
            )}
          </Field>
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-fg-2">
          <input
            type="checkbox"
            checked={allActivate}
            onChange={(e) => setAllActivate(e.target.checked)}
            className="size-4 cursor-pointer accent-accent"
          />
          打包计费（结构按集合价）
        </label>
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <p>
            <MicroLabel>估算合计</MicroLabel>
            <span className="ml-2 font-mono text-xl tabular-nums text-fg">{fmtYuan(cost)}</span>
          </p>
          <p className="text-[11px] text-muted">
            转写 {fmtYuan(cost - structureCost)} + 结构 {fmtYuan(structureCost)}
          </p>
        </div>
        <p className="text-[11px] text-muted">
          转写 {MINUTES_PRICE.transcriptionPerHour} 元/小时（必选）+ 结构集合 {MINUTES_PRICE.structureBundlePerHour} 元/小时
          或单功能 {MINUTES_PRICE.structureSinglePerHour} 元/小时 × N（功能 ≥2 时打包更划算）；
          视频价格官方未单列，按音频口径估算，以账单为准。
        </p>
      </CardBody>
    </Card>
  );
}

/** 人声分离估算：输入文件时长 × 0.07 元/分钟（AI MediaKit 音频工具）。 */
function SeparateEstimator() {
  const [minutes, setMinutes] = useState(10);
  const m = Math.max(0, minutes);
  const cost = m * SEPARATE_PRICE_PER_MINUTE;

  return (
    <Card>
      <CardHeader title="人声分离测算" icon={<Waves size={15} strokeWidth={1.75} />} aside={<span className="micro">AI MediaKit</span>} />
      <CardBody className="space-y-3">
        <Field label="输入音频时长（分钟）" hint="按输入文件时长计费，与输出轨道数无关">
          {({ id }) => (
            <Input
              id={id}
              type="number"
              min={0}
              step={1}
              value={String(minutes)}
              onChange={(e) => setMinutes(Number(e.target.value || 0))}
            />
          )}
        </Field>
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <p>
            <MicroLabel>估算费用</MicroLabel>
            <span className="ml-2 font-mono text-xl tabular-nums text-fg">{fmtYuan(cost)}</span>
          </p>
          <p className="text-[11px] text-muted">
            {SEPARATE_PRICE_PER_MINUTE} 元/分钟 · {SEPARATE_TRIAL}
          </p>
        </div>
        <p className="text-[11px] text-muted">
          走 AI MediaKit（<span className="font-mono">volc.mediakit.api_key</span>）独立产品体系；
          官方示例：10 分钟 × 0.07 = 0.7 元。
        </p>
      </CardBody>
    </Card>
  );
}

export default function PricingPage() {
  const allItems = useMemo(
    () => [PRICE_SYNC_BY_MINUTE, PRICE_SYNC_BY_CHARS, PRICE_SYNC_BY_CALLS, PRICE_TTS_20, PRICE_ASR_STANDARD, PRICE_ASR_FLASH, PRICE_ASR_IDLE, PRICE_MT],
    [],
  );

  return (
    <>
      <PageHeader
        title="计费测算"
        description="火山 / 千问 / 小米刊例价快照估算，帮助按费用选择引擎；实际以账单为准"
        actions={
          <span className="inline-flex items-center gap-3">
            {PRICING_SOURCES.map((s) => (
              <a
                key={s.url}
                href={s.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-xs text-fg-2 transition-colors duration-150 hover:text-accent"
              >
                {s.label}
                <ExternalLink size={12} strokeWidth={1.75} />
              </a>
            ))}
          </span>
        }
      />

      <div className="space-y-4">
        <TTSCompare />

        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <ASREstimator />
          <PodcastEstimator />
          <MTEstimator />
          <MinutesEstimator />
          <SeparateEstimator />
        </div>

        {/* 价格快照总表 */}
        <Card>
          <CardHeader
            title="价格快照"
            icon={<Calculator size={15} strokeWidth={1.75} />}
            aside={<span className="micro">快照 {PRICE_SNAPSHOT_DATE}</span>}
          />
          <CardBody className="space-y-3">
            <PriceRows items={allItems} />
            <div className="overflow-x-auto">
              <table className="w-full min-w-[560px] text-left text-xs">
                <thead>
                  <tr className="border-b border-line text-muted">
                    <th className="py-2 pr-3 font-normal">MediaKit 商品 / 计费项</th>
                    <th className="py-2 pr-3 font-normal">单价</th>
                    <th className="py-2 pr-3 font-normal">资源包</th>
                    <th className="py-2 font-normal">备注</th>
                  </tr>
                </thead>
                <tbody>
                  <tr className="border-b border-line/60 last:border-0">
                    <td className="py-2 pr-3 text-fg">音频工具-人声背景音分离-计费时长</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">0.07 元/分钟</td>
                    <td className="py-2 pr-3 text-muted">—</td>
                    <td className="py-2 text-muted">按输入文件时长计费，凭证与豆包语音相互独立</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[560px] text-left text-xs">
                <thead>
                  <tr className="border-b border-line text-muted">
                    <th className="py-2 pr-3 font-normal">千问 / 小米计费项</th>
                    <th className="py-2 pr-3 font-normal">单价</th>
                    <th className="py-2 pr-3 font-normal">计费口径</th>
                    <th className="py-2 font-normal">备注</th>
                  </tr>
                </thead>
                <tbody>
                  <tr className="border-b border-line/60">
                    <td className="py-2 pr-3 text-fg">千问 qwen3-tts-flash / instruct-flash</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">{QWEN_TTS_PRICE_PER_WAN} 元/万字符</td>
                    <td className="py-2 pr-3 text-muted">按输入文本字符，输出不计费</td>
                    <td className="py-2 text-muted">汉字计 2 字符、其余 1 字符</td>
                  </tr>
                  <tr className="border-b border-line/60">
                    <td className="py-2 pr-3 text-fg">千问 qwen3-asr-flash-filetrans</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">{QWEN_ASR_PRICE_PER_HOUR} 元/小时</td>
                    <td className="py-2 pr-3 text-muted">按输入音频秒数（0.00022 元/秒）</td>
                    <td className="py-2 text-muted">输出不计费</td>
                  </tr>
                  <tr className="border-b border-line/60">
                    <td className="py-2 pr-3 text-fg">千问 qwen-audio-3.1-asr-flash-filetrans</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">
                      输入 {QWEN_AUDIO_ASR.inputPerMillion} / 输出 {QWEN_AUDIO_ASR.outputPerMillion} 元/百万token
                    </td>
                    <td className="py-2 pr-3 text-muted">按 token</td>
                    <td className="py-2 text-muted">音频折算 token 口径官方未单列，未做时长估算</td>
                  </tr>
                  <tr className="border-b border-line/60">
                    <td className="py-2 pr-3 text-fg">小米 MiMo-V2.5-TTS（tts/voicedesign/voiceclone）</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">{MIMO_TTS_FREE_NOTE}</td>
                    <td className="py-2 pr-3 text-muted">—</td>
                    <td className="py-2 text-muted">正式定价以官方后续公告为准</td>
                  </tr>
                  <tr className="border-b border-line/60 last:border-0">
                    <td className="py-2 pr-3 text-fg">小米 mimo-v2.5-asr</td>
                    <td className="py-2 pr-3 font-mono tabular-nums text-fg-2">{MIMO_ASR_PRICE_PER_HOUR} 元/小时</td>
                    <td className="py-2 pr-3 text-muted">按输入音频时长折算小时，精确到秒</td>
                    <td className="py-2 text-muted">仅收 mp3/wav，base64 后 ≤10MB</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p className="flex items-start gap-1.5 text-[11px] leading-relaxed text-muted">
              <Info size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
              字符口径：1 个汉字/字母/标点/空格均算 1 字符（UTF-8 字节数不影响计费；千问例外——汉字计 2 字符）；时长口径：累加每次调用语音时长精确至毫秒折算小时。
              本页估算不含资源包抵扣顺序、试用额度与并发增购，后付费按小时出账；官方未给出「接口 ↔ 商品」映射，
              同步合成（V1 接口）的计费商品随音色代际而异，测算已按口径拆分并以账单为准；
              机器翻译按 token 计费（输入/输出分别计价，资源包按总量抵扣），
              人声分离属 AI MediaKit 音频工具计费体系，随文档更新于 2026.07；
              千问/小米价格取自模型市场与 Pay-As-You-Go 页（快照 {CLOUD_PRICE_SNAPSHOT_DATE}）。
            </p>
          </CardBody>
        </Card>
      </div>
    </>
  );
}
