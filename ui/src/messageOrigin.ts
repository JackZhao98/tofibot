import type { Message } from "./types";

type MessageOrigin = Pick<Message, "role" | "kind" | "sender_bot_id">;

export function isWebhookMessage(message: MessageOrigin): boolean {
  return message.kind === "webhook_event";
}

export function messageSourceLabel(message: MessageOrigin, botName?: string): string {
  if (isWebhookMessage(message)) return "Webhook";
  return botName ?? (message.role === "user" ? "你" : message.role === "tool" ? "工具" : "Bot");
}

/** Keep externally supplied events separate from human message grouping. */
export function messageOriginKey(message: MessageOrigin): string {
  if (isWebhookMessage(message)) return "webhook_event";
  return message.role === "user" ? "user" : message.sender_bot_id ?? message.role;
}

/** Retry's existing boundary changes only for the new external provenance. */
export function isRetryBoundaryMessage(message: MessageOrigin): boolean {
  return message.role === "user" && !isWebhookMessage(message);
}
