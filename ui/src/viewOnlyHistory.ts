import type { Message } from "./types";

export type ViewOnlyHistoryPage = { messages: Message[] | null; has_more: boolean };
export type ViewOnlyHistory = { messages: Message[]; beforeSeq?: number; hasMore: boolean };

/** The cursor belongs to the conversation page, not the filtered run's rows. */
export function mergeViewOnlyHistory(current: ViewOnlyHistory, page: ViewOnlyHistoryPage, conversationId: string, runId?: string): ViewOnlyHistory {
  const raw = (page.messages ?? []).filter(message => message.conversation_id === conversationId && Number.isSafeInteger(message.seq) && message.seq > 0);
  const older = raw.filter(message => current.beforeSeq === undefined || message.seq < current.beforeSeq);
  const beforeSeq = older.reduce<number | undefined>((min, message) => min === undefined ? message.seq : Math.min(min, message.seq), current.beforeSeq);
  // Never loop the same cursor, or mistake an invalid page for end-of-history.
  if (page.has_more && (beforeSeq === undefined || beforeSeq === current.beforeSeq)) throw new Error("History cursor did not advance");
  const byId = new Map<string, Message>();
  for (const message of older) {
    // The ordinary messages endpoint excludes private bot_result anchors too.
    if ((message.kind as string) !== "bot_result" && (!runId || message.run_id === runId)) byId.set(message.id, message);
  }
  // An overlapping older page cannot replace a newer snapshot of an existing ID.
  for (const message of current.messages) byId.set(message.id, message);
  return { messages: [...byId.values()].sort((a, b) => a.seq - b.seq || a.id.localeCompare(b.id)), beforeSeq, hasMore: page.has_more };
}
