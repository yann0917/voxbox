import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { fetchJSON } from "./api";
import { onTaskEvent } from "./ws";
import type { Artifact, TaskDetail } from "./types";
import type { Scenario, ScenarioStep } from "./scenarios";

/** 场景执行的单步状态。taskId 关联 WS 事件；progress 仅当前步有。 */
export interface StepRun {
  label: string;
  taskId: string | null;
  status: "pending" | "running" | "done" | "failed";
  progress: number;
  note: string;
}

export interface ScenarioRunState {
  title: string;
  steps: StepRun[];
  status: "running" | "done" | "failed";
  /** 终态产物（逐步累积，含末步全部产物） */
  artifacts: Artifact[];
  error?: string;
}

/** 场景执行器：按序提交任务，WS 终态驱动下一步（产物走 artifact_input 通道）。
 *  每一步都是普通任务——历史可见、可单独重跑；页面关闭只断后链，已完成步骤不受影响。
 *  状态以 ref 为唯一事实源（WS 回调跨渲染批次读最新值），state 仅作渲染镜像——
 *  事件回调里不能走 setState(updater)：updater 需为纯函数，编排副作用必须在外面。 */
export function useScenarioRunner() {
  const [run, setRun] = useState<ScenarioRunState | null>(null);
  const stateRef = useRef<ScenarioRunState | null>(null);
  const stepsRef = useRef<ScenarioStep[]>([]);
  const currentRef = useRef<number>(-1);
  const qc = useQueryClient();
  const navigate = useNavigate();

  const commit = useCallback((next: ScenarioRunState | null) => {
    stateRef.current = next;
    setRun(next);
  }, []);

  const fail = useCallback(
    (error: string) => {
      const cur = stateRef.current;
      if (cur) commit({ ...cur, status: "failed", error });
    },
    [commit],
  );

  const submitStep = useCallback(
    async (idx: number, artifactInput?: string) => {
      const step = stepsRef.current[idx];
      const body: Record<string, unknown> = {
        provider: step.provider,
        tool: step.tool,
        params: step.params,
      };
      if (artifactInput) body.artifact_input = artifactInput;
      try {
        const d = await fetchJSON<{ task_id: string }>("/api/tasks", {
          method: "POST",
          body: JSON.stringify(body),
        });
        currentRef.current = idx;
        const cur = stateRef.current;
        if (cur) {
          commit({
            ...cur,
            steps: cur.steps.map((s, i) =>
              i === idx ? { ...s, taskId: d.task_id, status: "running", progress: 0, note: "已提交" } : s,
            ),
          });
        }
      } catch (e) {
        fail(e instanceof Error ? e.message : String(e));
      }
    },
    [commit, fail],
  );

  const start = useCallback(
    (scenario: Scenario) => {
      stepsRef.current = scenario.steps;
      currentRef.current = -1;
      commit({
        title: scenario.title,
        steps: scenario.steps.map((s) => ({ label: s.label, taskId: null, status: "pending", progress: 0, note: "" })),
        status: "running",
        artifacts: [],
      });
      void submitStep(0);
    },
    [commit, submitStep],
  );

  const reset = useCallback(() => {
    stepsRef.current = [];
    currentRef.current = -1;
    commit(null);
  }, [commit]);

  const cancel = useCallback(async () => {
    const idx = currentRef.current;
    const taskId = stateRef.current?.steps[idx]?.taskId;
    if (taskId) {
      await fetchJSON(`/api/tasks/${taskId}/cancel`, { method: "POST" }).catch(() => undefined);
    }
    const cur = stateRef.current;
    if (cur && cur.status === "running") commit({ ...cur, status: "failed", error: "已取消" });
  }, [commit]);

  useEffect(
    () =>
      onTaskEvent((ev) => {
        const idx = currentRef.current;
        const cur = stateRef.current;
        if (idx < 0 || !cur || cur.status !== "running" || !ev.task_id) return;
        const step = cur.steps[idx];
        if (!step?.taskId || step.taskId !== ev.task_id) return;

        if (ev.type === "progress") {
          commit({
            ...cur,
            steps: cur.steps.map((s, i) =>
              i === idx ? { ...s, progress: ev.progress ?? s.progress, note: ev.note ?? "" } : s,
            ),
          });
          return;
        }

        if (ev.type === "error" || ev.type === "canceled") {
          commit({
            ...cur,
            status: "failed",
            error: ev.type === "canceled" ? "已取消" : ev.error || "步骤失败",
            steps: cur.steps.map((s, i) => (i === idx ? { ...s, status: "failed", note: ev.error ?? "失败" } : s)),
          });
          return;
        }

        if (ev.type === "done") {
          // 步骤先标完成（UI 即时反馈），产物拉取在 updater 之外进行
          const doneSteps = cur.steps.map((s, i) => (i === idx ? { ...s, status: "done" as const, progress: 100, note: "完成" } : s));
          commit({ ...cur, steps: doneSteps });
          void (async () => {
            try {
              const d = await fetchJSON<TaskDetail>(`/api/tasks/${ev.task_id}`);
              const arts = d.artifacts;
              const def = stepsRef.current[idx];
              const picked = def.wants ? arts.filter((a) => a.kind === def.wants) : arts;
              const base = stateRef.current;
              if (!base) return;
              const withArts: ScenarioRunState = { ...base, artifacts: [...base.artifacts, ...arts] };
              const isLast = idx === stepsRef.current.length - 1;
              if (isLast) {
                commit({ ...withArts, status: "done" });
                const goto = def.goto?.(arts) ?? null;
                if (goto) navigate(goto);
                void qc.invalidateQueries({ queryKey: ["recent-materials"] });
                return;
              }
              const nextInput = def.wants ? (picked[0]?.id ?? "") : "";
              if (!nextInput) {
                commit({ ...withArts, status: "failed", error: `上一步没有可用产物（需要 ${def.wants}）` });
                return;
              }
              commit(withArts);
              currentRef.current = -1;
              await submitStep(idx + 1, nextInput);
            } catch (e) {
              fail(e instanceof Error ? e.message : String(e));
            }
          })();
        }
      }),
    [commit, fail, navigate, qc, submitStep],
  );

  return { run, start, reset, cancel };
}
