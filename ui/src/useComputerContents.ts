import {useCallback, useEffect, useRef, useState} from "react";
import {request} from "./api";

/** What the server can name of the computer's own state: GET /api/computer/contents (names and counts, never values). */
export type ComputerContents = {environment: string[]; ssh_keys: string[]; pending_file_copies: number};

/**
 * Reads the list while `active`, again when the window regains focus. Callers mount it only when a computer is
 * attached (useHasComputer): a workspace without one makes no /api/computer/* request.
 */
export function useComputerContents(active = true) {
  const [data, setData] = useState<ComputerContents | null>(null);
  const [failed, setFailed] = useState(false);
  const alive = useRef(true);
  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const next = await request<ComputerContents>("/api/computer/contents", {signal});
      if (!alive.current || signal?.aborted) return;
      setData({environment: next.environment ?? [], ssh_keys: next.ssh_keys ?? [], pending_file_copies: next.pending_file_copies ?? 0});
      setFailed(false);
    } catch {
      if (alive.current && !signal?.aborted) setFailed(true);
    }
  }, []);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void load(controller.signal);
    const focus = () => { if (!document.hidden) void load(); };
    window.addEventListener("focus", focus);
    document.addEventListener("visibilitychange", focus);
    return () => { controller.abort(); window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); };
  }, [active, load]);
  return {data, failed, retry: () => { setFailed(false); void load(); }};
}
