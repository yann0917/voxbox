import { useCallback, useState } from "react";
import type { Task } from "../../lib/types";
import { speakerStats, type QNSegment } from "./model";
import { ChatPanel } from "./ChatPanel";
import { RefinePanel } from "./RefinePanel";
import { SpeakerSection } from "./SpeakerSection";

export interface NotebookSectionProps {
  task: Task;
  segments: QNSegment[];
  /** 引用 chip 点击跳播（接线方并入轨标题/时长副标题） */
  onSeek: (ms: number) => void;
}

/** 纪要区（历史任务详情的加工层）：说话人改名与统计 + AI 提炼 + 就稿问答。
 *  说话人改名状态内聚本层——统计区的改名即时反映到问答上下文的称呼；
 *  展示名公式（改名覆盖优先，否则「说话人N」，编号原样不 +1）只在此一份。
 *  父层以任务 id 作 key 挂载：切换任务即整体重置本区状态。 */
export function NotebookSection({ task, segments, onSeek }: NotebookSectionProps) {
  const [speakerNames, setSpeakerNames] = useState<Record<string, string>>(
    task.summary?.speaker_names ?? {},
  );
  const speakerLabel = useCallback((id: string) => speakerNames[id] ?? `说话人${id}`, [speakerNames]);
  const hasSpeakers = speakerStats(segments).length > 0;

  return (
    <div className="space-y-3">
      {hasSpeakers && (
        <SpeakerSection
          task={task}
          segments={segments}
          speakerNames={speakerNames}
          speakerLabel={speakerLabel}
          onSpeakerNamesChange={setSpeakerNames}
        />
      )}
      <RefinePanel taskId={task.id} title={task.title ?? ""} refined={task.summary?.refined} />
      <ChatPanel segments={segments} speakerLabel={speakerLabel} onSeek={onSeek} />
    </div>
  );
}
