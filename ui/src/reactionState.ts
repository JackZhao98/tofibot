import type { Reaction } from "./types";

/** The reader's own reaction applied to a message's list before the server answers; the inverse call rolls it back. */
export function applyUserReaction(reactions: Reaction[] | undefined, emoji: string, present: boolean, now = new Date().toISOString()): Reaction[] {
  const current = reactions ?? [];
  const mine = current.some(reaction => reaction.emoji === emoji && reaction.actor_type === "user");
  if (present) return mine ? current : [...current, { emoji, actor_type: "user", created_at: now } as Reaction];
  return mine ? current.filter(reaction => !(reaction.emoji === emoji && reaction.actor_type === "user")) : current;
}
