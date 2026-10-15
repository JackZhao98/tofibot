import { useEffect, useState } from "react";
import { request } from "./api";
import type { Schedule } from "./types";

const empty: ReadonlyMap<string, Schedule> = new Map();

/** One conversation-level read; occurrence rows never each fetch a task list. */
const recent = new Map<string, ReadonlyMap<string, Schedule>>();
const REMEMBERED = 8;

/** `ready` is false until this conversation's first answer (success or failure) has arrived. */
export function useScheduleMetadata(conversationId: string | null, rootKey: string, revision: number): { items: ReadonlyMap<string, Schedule>; ready: boolean } {
  const [snapshot, setSnapshot] = useState<{ id: string; items: ReadonlyMap<string, Schedule> }>({ id:"", items:empty });
  useEffect(() => {
    if (!conversationId || !rootKey) return;
    const abort = new AbortController();
    void request<{ schedules: Schedule[] }>(`/api/conversations/${encodeURIComponent(conversationId)}/schedules`, { signal:abort.signal })
      .then(result => { if (!abort.signal.aborted) { const items = new Map((result.schedules ?? []).filter(item => item.conversation_id === conversationId).map(item => [item.id, item] as const)); recent.delete(conversationId); recent.set(conversationId, items); if (recent.size > REMEMBERED) recent.delete(recent.keys().next().value as string); setSnapshot({ id:conversationId, items }); } })
      .catch(() => { if (!abort.signal.aborted) setSnapshot({ id:conversationId, items:empty }); });
    return () => abort.abort();
  }, [conversationId, rootKey, revision]);
  if (!conversationId || !rootKey) return { items: empty, ready: true };
  // Stale-while-revalidate: this conversation's last answer stands until the next one replaces it.
  if (snapshot.id === conversationId) return { items: snapshot.items, ready: true };
  const remembered = recent.get(conversationId);
  return remembered ? { items: remembered, ready: true } : { items: empty, ready: false };
}
