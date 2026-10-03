import type { RunStatus } from "./types";
import { validTimezone } from "./timezone";

export type ScheduleExecutionStatus = RunStatus;
export type ScheduleOccurrence = {
  root_run_id: string;
  schedule_id: string;
  scheduled_for_utc: string;
  title?: string;
  description?: string;
  created_by?: "user" | "bot";
  kind?: "once" | "daily" | "interval";
  timezone?: string;
  interval_seconds?: number;
  daily_time?: string;
  occurrence_number?: number;
  execution_status: ScheduleExecutionStatus;
  status_run_id: string;
  status_error?: string;
  result_in_conversation: boolean;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const states = new Set(["queued", "running", "waiting", "done", "failed", "cancelled", "interrupted"]);
export const occurrenceRootKey = (roots: readonly string[]) => [...new Set(roots.filter(id => uuid.test(id)))].sort().join(",");

type RequestOccurrences = (path: string, init: RequestInit) => Promise<unknown>;

// Bind each response to an actual requested occurrence root, never to the
// schedule's latest run, task text, or an approximate timestamp.
export async function loadScheduleOccurrences(conversationId: string, rootKey: string, signal: AbortSignal, request: RequestOccurrences): Promise<Map<string, ScheduleOccurrence>> {
  const roots = occurrenceRootKey(rootKey.split(",")).split(",").filter(Boolean);
  const result = new Map<string, ScheduleOccurrence>();
  for (let offset = 0; offset < roots.length; offset += 50) {
    signal.throwIfAborted();
    const batch = roots.slice(offset, offset + 50);
    const requested = new Set(batch);
    const response = await request(`/api/conversations/${encodeURIComponent(conversationId)}/schedule-occurrences?root_run_ids=${batch.join(",")}`, { signal });
    signal.throwIfAborted();
    if (!response || typeof response !== "object" || !("occurrences" in response) || !Array.isArray(response.occurrences)) throw new Error("Invalid schedule occurrence response");
    for (const item of response.occurrences) {
      if (!item || typeof item !== "object" || !requested.has(item.root_run_id)) continue;
      if (!uuid.test(item.schedule_id) || !uuid.test(item.status_run_id) || !states.has(item.execution_status) || typeof item.scheduled_for_utc !== "string" || !Number.isFinite(Date.parse(item.scheduled_for_utc))) continue;
      result.set(item.root_run_id, {
        root_run_id: item.root_run_id, schedule_id: item.schedule_id,
        scheduled_for_utc: item.scheduled_for_utc, execution_status: item.execution_status,
        status_run_id: item.status_run_id,
        result_in_conversation: item.result_in_conversation === true && item.execution_status === "done",
        ...(typeof item.title === "string" && item.title ? { title: item.title } : {}),
        ...(typeof item.description === "string" ? { description: item.description } : {}),
        ...((item.created_by === "user" || item.created_by === "bot") ? { created_by: item.created_by } : {}),
        ...((item.kind === "once" || item.kind === "daily" || item.kind === "interval") ? { kind: item.kind } : {}),
        ...(typeof item.timezone === "string" && validTimezone(item.timezone) ? { timezone: item.timezone } : {}),
        ...(Number.isSafeInteger(item.interval_seconds) && item.interval_seconds > 0 ? { interval_seconds: item.interval_seconds } : {}),
        ...(typeof item.daily_time === "string" && /^\d{2}:\d{2}$/.test(item.daily_time) ? { daily_time: item.daily_time } : {}),
        ...(Number.isSafeInteger(item.occurrence_number) && item.occurrence_number > 0 ? { occurrence_number: item.occurrence_number } : {}),
        ...((item.execution_status === "failed" || item.execution_status === "interrupted") && typeof item.status_error === "string"
          ? { status_error: Array.from(item.status_error).slice(0, 2400).join("") } : {}),
      });
    }
  }
  return result;
}

export const hasActiveOccurrence = (items: ReadonlyMap<string, ScheduleOccurrence>) => [...items.values()].some(item => item.execution_status === "queued" || item.execution_status === "running" || item.execution_status === "waiting");
