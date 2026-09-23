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
            格式工厂站点未连接（{d.base_url}）：请在 pcgeshi-go 项目启动
            <code className="mx-1 rounded bg-raise px-1 py-0.5 font-mono text-[11px]">pcg serve --addr :8090</code>
            ，或在设置页修改服务地址。{d.error ? `（${d.error}）` : ""}
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
            格式工厂站点已连接（{d.base_url}）
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
        description="格式工厂在线版云端直连：任务提交到 z.pcgeshi.com 云端执行（与官网前端同协议，免费匿名），含视频/音频转换压缩与图片处理；人声分离在「人声分离」页"
        hint="任务直传格式工厂云端（z.pcgeshi.com，免费匿名），产物自动取回本服务；站点接口为其私有协议，改版风险自担。"
        envBanner={() => null}
      />
    </>
  );
}
