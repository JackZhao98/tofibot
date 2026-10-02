import { useEffect, useState } from "react";
import { ApiError, request } from "./api";
import { hasActiveOccurrence, loadScheduleOccurrences, type ScheduleOccurrence } from "./scheduleOccurrences";

const emptyOccurrences: ReadonlyMap<string, ScheduleOccurrence> = new Map();

export function useScheduleOccurrences(conversationId: string | null, rootKey: string, revision: number): ReadonlyMap<string, ScheduleOccurrence> {
  const key = JSON.stringify([conversationId, rootKey]);
  const [snapshot, setSnapshot] = useState<{ key: string; items: ReadonlyMap<string, ScheduleOccurrence> }>({ key: "", items: emptyOccurrences });
  useEffect(() => {
    if (!conversationId || !rootKey) return;
    const controller = new AbortController();
    let timer: number | undefined;
    let inFlight = false;
    let refreshPending = false;
    let failures = 0;
    const refresh = async () => {
      if (controller.signal.aborted || document.hidden) return;
      if (inFlight) { refreshPending = true; return; }
      inFlight = true;
      if (timer !== undefined) window.clearTimeout(timer);
      let active = false;
      let retry = false;
      try {
        const items = await loadScheduleOccurrences(conversationId, rootKey, controller.signal, request);
        if (controller.signal.aborted) return;
        setSnapshot({ key, items });
        failures = 0;
        active = hasActiveOccurrence(items);
      } catch (cause) {
        // Older servers and failed reads are unknown, not successful. A root
        // run may still be displayed separately without claiming family success.
        if (!controller.signal.aborted) setSnapshot({ key, items: emptyOccurrences });
        failures += 1;
        retry = !(cause instanceof ApiError && [400, 401, 403, 404, 405].includes(cause.status));
      } finally {
        inFlight = false;
        if (!controller.signal.aborted && (active || retry || refreshPending)) {
          timer = window.setTimeout(() => void refresh(), refreshPending ? 200 : retry ? Math.min(failures * 10000, 30000) : 5000);
          refreshPending = false;
        }
      }
    };
    const visible = () => { if (!document.hidden) void refresh(); };
    // Coalesce workspace/run invalidations; a delegated run can live in an
    // entirely different conversation, so active families also refresh quietly.
    timer = window.setTimeout(() => void refresh(), 200);
    window.addEventListener("focus", visible);
    document.addEventListener("visibilitychange", visible);
    return () => {
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
      window.removeEventListener("focus", visible);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [conversationId, rootKey, key, revision]);
  // Never flash another conversation's status while the next effect starts.
  return snapshot.key === key ? snapshot.items : emptyOccurrences;
}
