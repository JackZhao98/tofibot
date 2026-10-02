import { api, ApiError } from "./api";
import type { RunStatus, WorkItem } from "./types";

export const workExecutionStatus: Record<RunStatus, string> = {
  queued: "待执行", running: "执行中", waiting: "等待回复", done: "执行已结束",
  failed: "执行失败", cancelled: "已取消", interrupted: "已中断",
};

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
    if (!(cause instanceof ApiError)) throw new Error("未能确认执行是否已开始，请重试确认。重试不会重复启动。", { cause });
    throw cause;
  }).finally(() => { inflight.delete(id); });
  inflight.set(id, operation);
  return operation;
}
