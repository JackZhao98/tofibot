import { useEffect, useRef, useState } from "react";
import { ScheduledRun } from "../../../ScheduledRun";
import { useSeenOnce } from "../../lib/hooks";
import "./schedule-row-demo.css";

/** All task records and timings below are synthetic, and labeled in the Lab. */
export function ScheduleRowDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const [state, setState] = useState<"running" | "done" | "failed">("running");
  const [startedAt, setStartedAt] = useState(() => new Date().toISOString());
  const attempt = useRef(0);

  useEffect(() => {
    if (!seen || state !== "running") return;
    setStartedAt(new Date().toISOString());
    const finish = window.setTimeout(() => {
      attempt.current += 1;
      setState(attempt.current % 2 === 1 ? "done" : "failed");
    }, 8000);
    return () => window.clearTimeout(finish);
  }, [seen, state]);

  function replay() {
    setStartedAt(new Date().toISOString());
    setState("running");
  }

  return <div className="scheduled-run-demo" ref={rootRef}>
    <ScheduledRun
      title="探索测试提醒"
      content="在这个对话里只发送一次提醒，原文：探索测试提醒已到。不要外部通知，也不要重复。"
      plannedTime="12:30"
      plannedTimeDescription="模拟计划时间：12:30"
      metadata="只一次 · 9月28日创建"
      state="done"
      statusLabel="结果已写入对话"
      statusTime="12:31"
      statusTimeDescription="模拟状态更新时间：12:31"
      resultPublished
      defaultExpanded
    />
    <ScheduledRun
      title="每日价格巡检"
      content="整理今天的价格变化，把可核实的变化与来源写入这个对话。"
      plannedTime="09:00"
      plannedTimeDescription="模拟计划时间：09:00"
      metadata="每天 09:00"
      state={state}
      statusLabel={state === "running" ? "执行中" : state === "done" ? "结果已写入对话" : "执行失败"}
      statusTime={state === "done" ? "09:01" : undefined}
      statusTimeDescription={state === "done" ? "模拟状态更新时间：09:01" : undefined}
      runningSince={startedAt}
      resultPublished={state === "done"}
      failureSummary="信息源超时，本轮未完成。"
      error="模拟错误：信息源请求超时，尚未生成完整结果。"
      onRetry={async () => replay()}
    />
    <ScheduledRun
      title="周报汇总"
      content="汇总本周已完成的任务、待办和需要跟进的问题，写入当前对话。"
      plannedTime="10:00"
      plannedTimeDescription="模拟计划时间：10:00"
      metadata="每 7 天"
      state="failed"
      statusLabel="执行失败"
      failureSummary="会话已归档，结果没有写入对话。"
      error="模拟错误：目标会话已归档。本示例不连接真实会话。"
      onRetry={async () => { throw new Error("模拟任务：会话仍已归档，请先恢复会话。"); }}
    />
    <div className="scheduled-run-demo-controls">
      <p>模拟数据 · 与对话共用 ScheduledRun。展开查看任务；第二次演示执行会失败。</p>
      <button type="button" onClick={replay} disabled={state === "running"}>再运行一次</button>
    </div>
  </div>;
}
