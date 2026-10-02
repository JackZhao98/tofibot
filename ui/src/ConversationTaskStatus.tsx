import type { Conversation } from "./types";
import "./conversation-task-status.css";

type TaskState = NonNullable<Conversation["task_state"]>;

export function taskStateLabel(state: TaskState): string {
  switch (state.status) {
    case "executing": return state.draft_status === "sending" ? "邮件发送中" : "执行中";
    case "needs_attention": return state.question_id ? "待你回答" : state.draft_status === "unknown" ? "发送待核实" : "待你确认";
    case "waiting": return "等待继续";
    case "failed": return "执行失败";
    case "completed": return state.draft_status === "sent" ? state.draft_demo ? "演示已完成" : "邮件已发送" : state.result_message_id ? "已交付结果" : "本轮已结束";
    case "cancelled": return state.draft_status === "declined" ? "已取消发送" : "已停止";
  }
}

export function ConversationTaskStatus({ state, onView, onRetry }: { state: TaskState; onView: () => void; onRetry?: () => void }) {
  // A successful result already lives in the conversation. Keep this strip
  // for states that require attention instead of repeating every success.
  if (state.status === "completed") return null;
  const canView = Boolean(state.question_id || state.draft_id || state.result_message_id);
  return <section className="conversation-task-banner" data-task-status={state.status} aria-label={`任务状态：${taskStateLabel(state)}`}>
    <span className="conversation-task-banner-dot" aria-hidden="true" />
    <strong>{taskStateLabel(state)}</strong>
    {state.status === "needs_attention" && <span>{state.draft_status === "unknown" ? "请先核实实际发送状态；系统不会自动重发。" : state.draft_id ? "请在草稿卡确认或取消，发送状态以卡片记录为准。" : "处理后任务会继续，批准本身不代表操作成功。"}</span>}
    {state.status === "failed" && <span>{state.failure_reason || "本轮未完成。"}</span>}
    {canView && <button type="button" onClick={onView}>{state.status === "needs_attention" && state.draft_status !== "unknown" ? "查看待处理" : "查看记录"}</button>}
    {state.status === "failed" && state.can_retry && onRetry && <button type="button" onClick={onRetry}>重试本轮</button>}
  </section>;
}
