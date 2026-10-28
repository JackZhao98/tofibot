import type { Conversation } from "./types";

/**
 * Sidebar ordering.
 *
 * Pinned conversations sit in the tile dock in pin_order, the person's own order. The rest of the list is recency,
 * as in every chat app: a message sent or received moves its conversation to the top. A stored manual sort_order
 * only breaks ties between rows with the same time.
 */
const recency = (conversation: Conversation) => +new Date(conversation.last_message?.created_at ?? conversation.updated_at);

export function pinnedOf(list: Conversation[]): Conversation[] {
  return list.filter(conversation => conversation.pinned).sort((a, b) => (a.pin_order ?? 0) - (b.pin_order ?? 0) || a.id.localeCompare(b.id));
}

export function listOrder(list: Conversation[]): Conversation[] {
  const rest = list.filter(conversation => !conversation.pinned);
  return rest.sort((a, b) => recency(b) - recency(a) || (a.sort_order || Infinity) - (b.sort_order || Infinity) || a.id.localeCompare(b.id));
}

export type SidebarIds = { pinned: string[]; order: string[] };

/** Where `moved` lands when it leaves slot `from` for slot `to` (indexes into the array without it, `to` = final index). */
export function moveItem<T>(items: readonly T[], from: number, to: number): T[] {
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(Math.max(0, Math.min(next.length, to)), 0, item);
  return next;
}

/** Put `next` (a reordering of `slotIds`) back into the slots `slotIds` occupy inside `full`; other entries keep their place. */
export function fillSlots(full: readonly string[], slotIds: readonly string[], next: readonly string[]): string[] {
  const slots = new Set(slotIds);
  let cursor = 0;
  return full.map(id => (slots.has(id) ? next[cursor++] ?? id : id));
}

/** The optimistic local copy of a layout the server will store: renumbers pins and the manual order. */
export function applyLayout(list: Conversation[], layout: SidebarIds): Conversation[] {
  const pin = new Map(layout.pinned.map((id, index) => [id, index + 1]));
  const place = new Map(layout.order.map((id, index) => [id, index + 1]));
  return list.map(conversation => {
    const pinOrder = pin.get(conversation.id) ?? 0;
    const sortOrder = place.get(conversation.id) ?? conversation.sort_order ?? 0;
    if ((conversation.pin_order ?? 0) === pinOrder && Boolean(conversation.pinned) === pinOrder > 0 && (conversation.sort_order ?? 0) === sortOrder) return conversation;
    return { ...conversation, pinned: pinOrder > 0, pin_order: pinOrder, sort_order: sortOrder };
  });
}

/** Layout fields a refresh must not overwrite while a save is still in flight. */
export function keepLocalLayout(fresh: Conversation[], local: Conversation[]): Conversation[] {
  const byId = new Map(local.map(conversation => [conversation.id, conversation]));
  return fresh.map(conversation => {
    const mine = byId.get(conversation.id);
    return mine ? { ...conversation, pinned: mine.pinned, pin_order: mine.pin_order, sort_order: mine.sort_order } : conversation;
  });
}
