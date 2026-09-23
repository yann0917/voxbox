import type { ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ArrowUpRight, AudioLines, Radio, ScrollText } from "lucide-react";
import { Card, CardBody, MicroLabel, PageHeader, Tabs, type TabItem } from "../ui";
import { PRICE_SNAPSHOT_DATE, TTS_CHANNELS } from "../lib/pricing";
import TTSSyncPanel from "./tts/TTSSyncPanel";
import TTSStreamPanel from "./tts/TTSStreamPanel";
import TTSLongPanel from "./tts/TTSLongPanel";

type TabKey = "sync" | "stream" | "long";

const CHANNEL_ICONS: Record<TabKey, ReactNode> = {
  sync: <AudioLines size={13} strokeWidth={1.75} />,
  stream: <Radio size={13} strokeWidth={1.75} />,
  long: <ScrollText size={13} strokeWidth={1.75} />,
};

const TAB_ITEMS: TabItem<TabKey>[] = TTS_CHANNELS.map((c) => ({
  value: c.key,
  label: c.tab,
  icon: CHANNEL_ICONS[c.key],
}));

/** 语音合成聚合页：同步 / 流式 / 长文本三通道以 Tab 切换。
 *  三条通道接口代际与计费方式不同（详见各 Tab 说明条与「计费测算」页），
 *  按官方接口差异拆分实现，页面层仅做组织。 */
export default function TTSPage() {
  const [params, setParams] = useSearchParams();
  const tab: TabKey = TTS_CHANNELS.some((c) => c.key === params.get("tab"))
    ? (params.get("tab") as TabKey)
    : "sync";
  const info = TTS_CHANNELS.find((c) => c.key === tab)!;

  const switchTab = (v: TabKey) => {
    // sync 为默认态，不带查询参数，保持 /tts 干净
    if (v === "sync") setParams({});
    else setParams({ tab: v });
  };

  return (
    <>
      <PageHeader
        title="语音合成"
        description="同步 / 流式 / 长文本三个通道，按场景与费用选择"
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

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Tabs items={TAB_ITEMS} value={tab} onChange={switchTab} />
      </div>

      {/* 通道说明条：定位 + 计费方式 + 适用建议（费用视角选通道） */}
      <Card className="mb-4">
        <CardBody className="space-y-1.5 py-3">
          <div className="flex flex-wrap items-baseline gap-2">
            <MicroLabel>{info.tab}</MicroLabel>
            <span className="text-sm text-fg">{info.tagline}</span>
          </div>
          <p className="text-xs text-fg-2">
            <span className="mr-1 text-muted">计费：</span>
            {info.pricing}
          </p>
          <p className="text-xs text-muted">
            <span className="mr-1">适用：</span>
            {info.advice} 同量文本的费用对比见
            <Link to="/pricing" className="mx-0.5 text-accent transition-colors duration-150 hover:opacity-80">
              计费测算
            </Link>
            （刊例快照 {PRICE_SNAPSHOT_DATE}，以账单为准）。
          </p>
        </CardBody>
      </Card>

      {tab === "sync" ? <TTSSyncPanel /> : tab === "stream" ? <TTSStreamPanel /> : <TTSLongPanel />}
    </>
  );
}
