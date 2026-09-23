import { useSearchParams } from "react-router-dom";
import { MicVocal, Slice, SlidersHorizontal } from "lucide-react";
import { PageHeader, Tabs, type TabItem } from "../../ui";
import MixerPanel from "./MixerPanel";
import ClipPanel from "./ClipPanel";
import DuckPanel from "./DuckPanel";

type TabKey = "mixer" | "clip" | "duck";

const TAB_ITEMS: TabItem<TabKey>[] = [
  { value: "mixer", label: "混音台", icon: <SlidersHorizontal size={13} strokeWidth={1.75} /> },
  { value: "clip", label: "切高潮", icon: <Slice size={13} strokeWidth={1.75} /> },
  { value: "duck", label: "口播闪避", icon: <MicVocal size={13} strokeWidth={1.75} /> },
];

/** 音频后期聚合页（/post）：混音台 / 切高潮 / 口播闪避三个面板以 Tab 切换。
 *  三者同为 audio provider 的本地 ffmpeg 导出工具（零额度），共用「跨页带入查询参数 +
 *  WS 进度卡 + 产物行」的页面形态，聚合后侧栏一个入口；旧路由 /mixer /clip /duck
 *  在 main.tsx 带查询参数重定向至此，深链（?task= / ?artifact= / ?vocal=）不失效。 */
export default function PostPage() {
  const [params, setParams] = useSearchParams();
  const tab: TabKey = TAB_ITEMS.some((t) => t.value === params.get("tab"))
    ? (params.get("tab") as TabKey)
    : "mixer";

  const switchTab = (v: TabKey) => {
    // mixer 为默认态，不带查询参数，保持 /post 干净；其余面板的带入参数
    // （task/artifact/vocal）只在面板挂载时读一次，切 Tab 卸载后重新挂载即重读
    if (v === "mixer" && !params.get("tab")) return;
    const next = new URLSearchParams(params);
    if (v === "mixer") next.delete("tab");
    else next.set("tab", v);
    setParams(next);
  };

  return (
    <>
      <PageHeader
        title="音频后期"
        description="混音台 / 切高潮 / 口播闪避：本地 ffmpeg 导出"
        actions={<span className="micro">audio provider · 三个工具</span>}
      />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Tabs items={TAB_ITEMS} value={tab} onChange={switchTab} />
      </div>

      {tab === "mixer" ? <MixerPanel /> : tab === "clip" ? <ClipPanel /> : <DuckPanel />}
    </>
  );
}
