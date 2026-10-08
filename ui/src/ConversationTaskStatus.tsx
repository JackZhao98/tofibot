import type { Conversation } from "./types";
import { i18n } from "./i18n";
import "./conversation-task-status.css";

type TaskState = NonNullable<Conversation["task_state"]>;

export function taskStateLabel(state: TaskState): string {
  const t = i18n.getFixedT(null, "tasks");
  switch (state.status) {
    case "finishing": return t("conversation_state.finishing");
    case "expired": return t("conversation_state.expired");
    case "executing": return state.draft_status === "sending" ? t("conversation_state.mail_sending") : t("conversation_state.executing");
    case "needs_attention": return state.question_id ? t("conversation_state.needs_answer") : state.draft_status === "unknown" ? t("conversation_state.send_unconfirmed") : t("conversation_state.needs_confirmation");
    case "waiting": return t("conversation_state.waiting");
    case "failed": return t("conversation_state.failed");
    case "completed": return state.draft_status === "sent" ? state.draft_demo ? t("conversation_state.demo_done") : t("conversation_state.mail_sent") : state.result_message_id ? t("conversation_state.delivered") : t("conversation_state.turn_ended");
    case "cancelled": return state.draft_status === "declined" ? t("conversation_state.send_cancelled") : t("conversation_state.stopped");
  }
}
