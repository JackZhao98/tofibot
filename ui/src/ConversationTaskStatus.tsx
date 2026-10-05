import { taskLocale, taskText } from "./taskIssuePresentation";
import type { Conversation, Run } from "./types";
import "./conversation-task-status.css";

type TaskState = NonNullable<Conversation["task_state"]>;

export function taskStateLabel(state: TaskState): string {
  switch (state.status) {
    case "finishing": return "正在收尾";
    case "expired": return "已过期";
    case "executing": return state.draft_status === "sending" ? "邮件发送中" : "执行中";
    case "needs_attention": return state.question_id ? "待你回答" : state.draft_status === "unknown" ? "发送待核实" : "待你确认";
    case "waiting": return "等待继续";
    case "failed": return "执行失败";
    case "completed": return state.draft_status === "sent" ? state.draft_demo ? "演示已完成" : "邮件已发送" : state.result_message_id ? "已交付结果" : "本轮已结束";
    case "cancelled": return state.draft_status === "declined" ? "已取消发送" : "已停止";
  }
}

export function ConversationTaskStatus({ state: snapshot, run, onView }: { state: TaskState; run?: Run; onView: () => void }) {
  // The conversation summary can precede the latest run SSE frame. Project
  // only its exact run, so a stale finishing banner cannot outlive conclusion.
  const state: TaskState = run && run.id === snapshot.run_id && run.stop_reason === "approval_expired"
    ? { ...snapshot, status:"expired", can_retry:false }
    : run && run.id === snapshot.run_id && run.finishing_reason === "approval_expired"
      ? { ...snapshot, status:"finishing", can_retry:false } : snapshot;
  // A successful result already lives in the conversation. Keep this strip
  // for states that require attention instead of repeating every success.
  if (state.status === "completed") return null;
  return <section className="conversation-task-banner" data-task-status={state.status} aria-label="任务定位">
    <button type="button" onClick={onView}>{taskText(taskLocale(), "查看任务", "View task")}</button>
  </section>;
}
