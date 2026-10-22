import type { Conversation } from "./types";

export type RowIndicatorKind = "attention" | "failed" | "unread" | "working";

/**
 * The one status a sidebar row (or pinned tile) shows in its corner slot. A row never shows two.
 * Priority, highest first: waiting for you (attention) > failed > unread count > working. Everything else shows nothing
 * (completed, cancelled, expired, idle); the spoken label still carries the task state and the unread count.
 * What a lower-priority state would have said is not lost: unread keeps the row title bold, and the aria-label joins all of them.
 */
export function rowIndicator(task: Conversation["task_state"], unread: number, working: boolean): RowIndicatorKind | null {
  if (task?.status === "needs_attention" || task?.status === "waiting") return "attention";
  if (task?.status === "failed") return "failed";
  if (unread > 0) return "unread";
  if (task?.status === "executing" || task?.status === "finishing" || working) return "working";
  return null;
}
