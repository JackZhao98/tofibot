import type { Message } from "./types";

// A reply quote only earns its space when it adds information. When the quoted
// message is the user message directly above the reply (only progress/notice items
// or the same Bot's own earlier segments in between), the quote just repeats it.
export function shouldShowReplyQuote(timeline: Message[], index: number, target: Message | undefined): boolean {
  const message = timeline[index];
  if (!message || !target) return false;
  if (target.role !== "user") return true;
  const targetIndex = timeline.findIndex(item => item.id === target.id);
  if (targetIndex < 0 || targetIndex >= index) return true;
  for (let i = targetIndex + 1; i < index; i++) {
    const between = timeline[i];
    if (between.kind === "progress" || between.kind === "notice" || between.kind === "message_ref") continue;
    if (between.role === "user") return true;
    if ((between.sender_bot_id ?? "") !== (message.sender_bot_id ?? "")) return true;
  }
  return false;
}
