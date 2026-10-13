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

type TimelineAnchor = { kind: "message" | "question" | "draft"; id: string };

/**
 * A task block (name label + Activity row) is drawn right after the item it is anchored to; for a
 * run triggered by a message that item is the trigger, so several Bots answering one message stack
 * several blocks there. Returns every Bot labelled by a block directly above the next item: a
 * message from any of them must not repeat the avatar and name.
 */
export function precedingBlockBotIds(previous: TimelineAnchor | undefined, owners: { anchor?: TimelineAnchor; botId: string }[]): string[] {
  if (!previous) return [];
  return owners.filter(owner => owner.anchor?.kind === previous.kind && owner.anchor.id === previous.id).map(owner => owner.botId);
}
