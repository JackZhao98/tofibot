import type { Run } from "./types";
import { isTerminalRun } from "./runFamily.js";

export type RunUpdate = Partial<Run> & { id: string };

function updatedAtNanoseconds(value: string | undefined) {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value ?? "");
  if (!match) return undefined;
  const seconds = Date.parse(`${match[1]}Z`);
  if (!Number.isFinite(seconds)) return undefined;
  const fraction = BigInt(`${match[2] ?? ""}000000000`.slice(0, 9));
  return BigInt(seconds) * 1_000_000n + fraction;
}

function isStaleRunUpdate(existing: Run, incoming: RunUpdate) {
  const existingTime = updatedAtNanoseconds(existing.updated_at);
  const incomingTime = updatedAtNanoseconds(incoming.updated_at);
  return existingTime !== undefined && incomingTime !== undefined && incomingTime <= existingTime;
}

// A queued POST response can arrive after its SSE running update. Retain the
// current state when the incoming snapshot is not newer by updated_at.
export function mergeRunUpdates(current: Run[], updates: RunUpdate[]) {
  const currentByID = new Map(current.map((run) => [run.id, run]));
  const updatedIDs = new Set<string>();
  const merged: Run[] = [];
  for (const incoming of updates) {
    if (updatedIDs.has(incoming.id)) continue;
    updatedIDs.add(incoming.id);
    const existing = currentByID.get(incoming.id);
    merged.push(existing && (isStaleRunUpdate(existing, incoming) || (isTerminalRun(existing) && incoming.status !== undefined && incoming.status !== existing.status)) ? existing : { ...existing, ...incoming } as Run);
  }
  return [...merged, ...current.filter((run) => !updatedIDs.has(run.id))];
}
