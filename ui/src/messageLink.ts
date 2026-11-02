import type { Message } from "./types";

/**
 * The `message_task` event links a posted message to the task that took it as its result. It carries only
 * {id, task_id}, so it is merged into the message held: reactions, delivery state and every other field stay.
 * The list is returned unchanged (same array) when nothing matches or the link is already there.
 */
export function applyMessageTask(messages: Message[], data: Record<string, unknown>): Message[] {
  const id = typeof data.id === "string" ? data.id : "";
  const taskId = typeof data.task_id === "string" ? data.task_id : "";
  if (!id || !taskId) return messages;
  const index = messages.findIndex(message => message.id === id);
  if (index < 0 || messages[index].task_id === taskId) return messages;
  const next = messages.slice();
  next[index] = { ...messages[index], task_id: taskId };
  return next;
}
