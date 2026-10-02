import { useState } from "react";
import { TofiIcon } from "./icons";
import { isDesktop } from "./desktop";
import type { Message, Run, Schedule } from "./types";
import type { ScheduleOccurrence } from "./scheduleOccurrences";
import { ScheduledRun } from "./ScheduledRun";
import { scheduledRunMetadata } from "./scheduledRunMetadata";
import "./scheduled-task-row.css";

export function ScheduledTaskRow({ message, run, occurrence, schedule, timezone, onRetry }: { message: Message; run?: Run; occurrence?: ScheduleOccurrence; schedule?: Schedule; timezone?: string; onRetry?: (id: string) => Promise<void> }) {
  const [retrying, setRetrying] = useState(false);
  const owningRun = message.run_id && run?.id === message.run_id ? run : undefined;
  const exactOccurrence = message.run_id && occurrence?.root_run_id === message.run_id ? occurrence : undefined;
  const state = exactOccurrence?.execution_status ?? owningRun?.status;
  // A whole-family execution outcome does not certify result quality. Only a
  // completed receipt plus a persisted final reply in this conversation can
  // justify the narrower publication label (not a notification/read claim).
  const status = state === "waiting" ? "等待回复" : exactOccurrence
    ? state === "queued" ? "排队中" : state === "running" ? "执行中" : state === "done" ? exactOccurrence.result_in_conversation ? "结果已写入对话" : "执行已结束" : state === "failed" ? "执行失败" : state === "cancelled" ? "已停止" : state === "interrupted" ? "已中断" : "状态未知"
    : state === "queued" ? "排队中" : state === "running" ? "本轮执行中" : state === "done" ? "本轮已结束" : state === "failed" ? "本轮失败" : state === "cancelled" ? "本轮已停止" : state === "interrupted" ? "本轮已中断" : "状态未知";
  const isFailure = state === "failed" || state === "interrupted";
  // The family may have failed in another Bot's hidden conversation. Its
  // selected diagnostic comes from the same snapshot as the aggregate, not a
  // stale ancestor or the schedule's most recent occurrence.
  const error = isFailure ? exactOccurrence?.status_error ?? (
    owningRun?.error && (!exactOccurrence || exactOccurrence.status_run_id === owningRun.id) ? owningRun.error : undefined
  ) : undefined;
  const retryId = exactOccurrence?.status_run_id ?? owningRun?.id;
  const receiptMissing = error?.includes("scheduled task returned without confirming a completed result");
  if (!isDesktop) return <ScheduledRun
    {...scheduledRunMetadata(message, run, occurrence, schedule, timezone)}
    content={message.content}
    state={state}
    statusLabel={status}
    resultPublished={exactOccurrence?.result_in_conversation}
    failureSummary={receiptMissing ? "本轮没有确认完成结果，未算作已交付。" : "本轮未完成，任务记录已保留。"}
    error={error}
    notes={(!exactOccurrence || (exactOccurrence.execution_status === "done" && !exactOccurrence.result_in_conversation)) ? <>
      {!exactOccurrence && <p>暂未取得完整执行状态；这里只显示主运行状态。</p>}
      {exactOccurrence?.execution_status === "done" && !exactOccurrence.result_in_conversation && <p>未确认本次最终回复已写入当前对话；运行结束不代表结果已交付。</p>}
    </> : undefined}
    onRetry={onRetry && retryId ? () => onRetry(retryId) : undefined}
  />;
  // Preserve the native desktop row and its existing disclosure/retry behavior.
  return <article className={`scheduled-task-row scheduled-task-${state ?? "unknown"}`}>
    <details>
      <summary>
        <span className="scheduled-task-dot" aria-hidden="true" />
        <TofiIcon className="scheduled-task-clock" name="clock" size={13} aria-hidden="true" />
        <span className="scheduled-task-label" title={message.content}>{message.content}</span>
        <span className="scheduled-task-status">{status}</span>
        <TofiIcon className="scheduled-task-chevron" name="chevron-right" size={14} />
      </summary>
      <div className="scheduled-task-details"><strong>任务内容</strong><p>{message.content}</p>{!exactOccurrence && <p className="field-note">暂未取得完整执行状态；这里只显示主运行状态。</p>}{exactOccurrence?.execution_status === "done" && !exactOccurrence.result_in_conversation && <p className="field-note">未确认本次最终回复已写入当前对话；运行结束不代表结果已交付。</p>}{isFailure && <div className="scheduled-task-recovery"><p>{receiptMissing ? "本轮没有确认完成结果，未算作已交付。" : "本轮未完成，任务记录已保留。"}为避免重复执行外部操作，请确认后重试。</p>{error && <details><summary>查看技术原因</summary><p className="error-text">{error}</p></details>}{onRetry && retryId && <button type="button" className="text-button" disabled={retrying} onClick={() => { setRetrying(true); void onRetry(retryId).finally(() => setRetrying(false)); }}>{retrying ? "正在重新执行…" : "重新执行本轮"}</button>}</div>}</div>
    </details>
  </article>;
}
