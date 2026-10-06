import type { Conversation } from "./types";
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
