// 格式工厂（在线转换）：功能卡片与任务表单，任务提交到格式工厂云端执行，产物自动取回。
import { ToolkitPage } from "../components/ToolkitPage";

export default function GsgcPage() {
  return (
    <ToolkitPage
      provider="gsgc"
      title="格式工厂"
      description="格式工厂在线版：音视频/图片转换压缩，云端执行、免费可用；人声分离在「人声分离」页"
      hint="任务由格式工厂云端处理，产物自动取回本服务；第三方免费接口，可用性以站点为准。"
    />
  );
}
