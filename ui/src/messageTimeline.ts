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
 * A task block (name label + Activity row) is drawn right after the item it is anchored to. When
 * the next message comes from the same Bot, the block's label already names the sender, so that
 * message must not repeat the avatar and name. Returns the Bot that labelled the block directly
 * above the next item, if any.
 */
export function precedingBlockBotId(previous: TimelineAnchor | undefined, owners: { anchor?: TimelineAnchor; botId: string }[]): string | undefined {
  if (!previous) return undefined;
  const matching = owners.filter(owner => owner.anchor?.kind === previous.kind && owner.anchor.id === previous.id);
  return matching.length ? matching[matching.length - 1].botId : undefined;
}
