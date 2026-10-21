import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { api } from "./api";
import { invalidateModelCatalog, modelCatalogVersion, subscribeModelCatalog } from "./modelCatalogStore";
import type { ModelCatalog } from "./types";

/** A load is reused this long, so pickers that mount together (or one after another) share one request. */
const REUSE_MS = 3_000;
/** A window refocus refetches only when the last load is older than this. No timers, no polling. */
const FOCUS_REFETCH_MS = 10_000;

type Load = { version: number; startedAt: number; settled: boolean; promise: Promise<ModelCatalog> };
let current: Load | null = null;
let last: ModelCatalog | null = null;

/** One shared, deduplicated GET /api/models. A load started before an invalidation is never reused after it. */
export function loadModelCatalog(): Promise<ModelCatalog> {
  const version = modelCatalogVersion();
  if (current && current.version === version && (!current.settled || Date.now() - current.startedAt < REUSE_MS)) return current.promise;
  const load = { version, startedAt: Date.now(), settled: false } as Load;
  load.promise = api.models().then(
    catalog => { load.settled = true; if (current === load) last = catalog; return catalog; },
    cause => { load.settled = true; if (current === load) current = null; throw cause; },
  );
  current = load;
  return load.promise;
}

let focusUsers = 0;
function onFocus() {
  if (current && (!current.settled || Date.now() - current.startedAt < FOCUS_REFETCH_MS)) return;
  invalidateModelCatalog();
}

/**
 * The model catalog for a picker. The previous list stays on screen while a refetch runs, so a
 * provider change updates the options in place. It refetches when invalidateModelCatalog() is called
 * and when the window regains focus (the Codex sign-in finishes in another tab).
 */
export function useModelCatalog(): { catalog: ModelCatalog | null; error: string; reload: () => void } {
  const version = useSyncExternalStore(subscribeModelCatalog, modelCatalogVersion);
  const [catalog, setCatalog] = useState<ModelCatalog | null>(last);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let alive = true;
    setError("");
    loadModelCatalog().then(next => { if (alive) setCatalog(next); }, cause => { if (alive) setError(cause instanceof Error ? cause.message : String(cause)); });
    return () => { alive = false; };
  }, [version, retry]);
  useEffect(() => {
    if (focusUsers++ === 0) window.addEventListener("focus", onFocus);
    return () => { if (--focusUsers === 0) window.removeEventListener("focus", onFocus); };
  }, []);
  const reload = useCallback(() => { current = null; setRetry(n => n + 1); }, []);
  return { catalog, error, reload };
}
