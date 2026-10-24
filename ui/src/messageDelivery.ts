import type { Message } from "./types";

export type DeliveryState = "queued" | "read";

/**
 * Applies an `inbox` stream event ({message_id, state}) to the message it names. A late "queued" never undoes a "read"
 * (the stream can replay out of order after a reconnect). The server lists only "queued" in history, so a "read" exists
 * only in a session that saw it happen; its note fades away by CSS (send-receipt.css).
 */
export const applyInbox = (messages: Message[], event: Record<string, unknown>): Message[] =>
  event.state === "queued" || event.state === "read"
    ? messages.map(message => message.id === event.message_id && message.delivery !== "read" ? { ...message, delivery: event.state as DeliveryState } : message)
    : messages;
