// 格式工厂（站点云端直连）：功能卡片与任务表单，任务走站点私有协议提交到
// z.pcgeshi.com 云端执行（与 gsgc.separate 同协议）。/api/gsgc/health 探测可达性。
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, RefreshCw, TriangleAlert } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { Button } from "../ui";
import { ToolkitPage } from "../components/ToolkitPage";

interface GsgcHealth {
  reachable: boolean;
  base_url: string;
  error?: string;
  health?: { service?: string; ops?: number; queue?: { queued?: number; running?: number } };
}

export default function GsgcPage() {
  const health = useQuery({
    queryKey: ["gsgc-health"],
    queryFn: () => fetchJSON<GsgcHealth>("/api/gsgc/health"),
    staleTime: 30_000,
  });

  const d = health.data;
  return (
    <>
      {d && !d.reachable && (
        <div className="mb-4 flex flex-wrap items-center gap-2 rounded-[var(--radius-sm)] border border-warn/40 bg-warn/10 px-3 py-2 text-xs">
          <TriangleAlert size={14} strokeWidth={1.75} className="shrink-0 text-warn" />
          <span className="text-fg-2">
            格式工厂云端暂时无法连接（{d.base_url}），请稍后重试。{d.error ? `（${d.error}）` : ""}
          </span>
          <Button size="sm" variant="secondary" icon={<RefreshCw size={12} strokeWidth={1.75} />} onClick={() => health.refetch()}>
            重新检测
          </Button>
        </div>
      )}
      {d?.reachable && (
        <div className="mb-4 flex flex-wrap items-center gap-x-5 gap-y-1 rounded-[var(--radius-sm)] border border-line bg-raise-2 px-3 py-2 text-xs text-muted">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <ExternalLink size={13} strokeWidth={1.75} />
            格式工厂云端已连接（{d.base_url}）
          </span>
          {typeof d.health?.ops === "number" && <span>功能 {d.health.ops} 项</span>}
          <button
            className="inline-flex cursor-pointer items-center gap-1 text-muted transition-colors duration-150 hover:text-accent"
            onClick={() => health.refetch()}
          >
            <RefreshCw size={12} strokeWidth={1.75} />
            刷新
          </button>
        </div>
      )}
      <ToolkitPage
        provider="gsgc"
        title="格式工厂"
        description="格式工厂在线版：音视频/图片转换压缩，云端执行、免费可用；人声分离在「人声分离」页"
        hint="任务由格式工厂云端处理，产物自动取回本服务；第三方免费接口，可用性以站点为准。"
        envBanner={() => null}
      />
    </>
  );
}
