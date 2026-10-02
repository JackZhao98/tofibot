import type { Message, StreamDraft } from "./types";

/** A draft keeps the slot reserved when its first text became visible. */
export function mergeMessageTimeline(records: Message[], drafts: StreamDraft[]): Message[] {
  const persisted = new Set(records.map(message => message.id));
  const streaming: Message[] = drafts
    .filter(draft => draft.status === "active" && draft.content.trim() && !persisted.has(draft.message_id))
    .map(draft => ({
      id: draft.message_id,
      conversation_id: draft.conversation_id,
      seq: draft.seq && draft.seq > 0 ? draft.seq : Number.MAX_SAFE_INTEGER,
      role: "assistant",
      sender_bot_id: draft.bot_id,
      run_id: draft.run_id,
      content: draft.content,
      created_at: draft.created_at,
    }));
  return [...records, ...streaming].sort((a, b) => a.seq - b.seq);
}
