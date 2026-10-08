import { api, ApiError } from "./api";
import { i18n } from "./i18n";
import type { RunStatus, WorkItem } from "./types";

/** Catalog keys (work namespace) for a work item's execution status. */
export const workExecutionStatusKeys = {
  queued: "execution.status.queued", running: "execution.status.running", waiting: "execution.status.waiting", done: "execution.status.done",
  failed: "execution.status.failed", cancelled: "execution.status.cancelled", interrupted: "execution.status.interrupted",
} as const satisfies Record<RunStatus, string>;

export const workExecutionActive = (item: WorkItem) => item.execution?.active === true;
export const workExecutionClosed = (item: WorkItem) => item.status === "done" || item.status === "cancelled";

// Keep uncertain requests across remounts, panel changes, and a page reload.
// The server alone resolves whether an attempt was accepted.
const requests = new Map<string, string>();
const inflight = new Map<string, ReturnType<typeof api.executeWorkItem>>();
const storageKey = (id: string) => `tofi:work-execution:${id}`;

export function hasPendingWorkExecution(id: string) {
  if (requests.has(id)) return true;
  try { return !!sessionStorage.getItem(storageKey(id)); } catch { return false; }
}

export function startWorkExecution(id: string) {
  const current = inflight.get(id);
  if (current) return current;
  let requestID = requests.get(id);
  try { requestID ||= sessionStorage.getItem(storageKey(id)) || undefined; } catch { /* memory fallback */ }
  requestID ||= crypto.randomUUID();
  requests.set(id, requestID);
  try { sessionStorage.setItem(storageKey(id), requestID); } catch { /* memory fallback */ }
  const resolveRequest = () => {
    requests.delete(id);
    try { sessionStorage.removeItem(storageKey(id)); } catch { /* memory fallback */ }
  };
  const operation = api.executeWorkItem(id, requestID).then(result => {
    resolveRequest();
    return result;
  }).catch(cause => {
    // Even a definite rejection now cannot rule out an earlier accepted request
    // whose response was lost. Only a successful replay resolves its UUID.
    if (!(cause instanceof ApiError)) throw new Error(i18n.t("work:execution.start_unconfirmed"), { cause });
    throw cause;
  }).finally(() => { inflight.delete(id); });
  inflight.set(id, operation);
  return operation;
}
