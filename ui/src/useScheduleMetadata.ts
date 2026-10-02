import { useEffect, useState } from "react";
import { request } from "./api";
import type { Schedule } from "./types";

const empty: ReadonlyMap<string, Schedule> = new Map();

/** One conversation-level read; occurrence rows never each fetch a task list. */
export function useScheduleMetadata(conversationId: string | null, rootKey: string, revision: number): ReadonlyMap<string, Schedule> {
  const [snapshot, setSnapshot] = useState<{ id: string; items: ReadonlyMap<string, Schedule> }>({ id:"", items:empty });
  useEffect(() => {
    if (!conversationId || !rootKey) return;
    const abort = new AbortController();
    void request<{ schedules: Schedule[] }>(`/api/conversations/${encodeURIComponent(conversationId)}/schedules`, { signal:abort.signal })
      .then(result => { if (!abort.signal.aborted) setSnapshot({ id:conversationId, items:new Map((result.schedules ?? []).filter(item => item.conversation_id === conversationId).map(item => [item.id, item])) }); })
      .catch(() => { if (!abort.signal.aborted) setSnapshot({ id:conversationId, items:empty }); });
    return () => abort.abort();
  }, [conversationId, rootKey, revision]);
  return conversationId && rootKey && snapshot.id === conversationId ? snapshot.items : empty;
}
