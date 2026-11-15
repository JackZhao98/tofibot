import { createContext } from "react";
import type { Run, RunPlan } from "./types";

/**
 * Step lists that arrived as `run_plan` events, by run id. A run's list is the newer (by version) of this and the
 * `plan` its run carried in the last list read, so a list response that crossed a newer event never shows an older
 * list. Entry code: kept small, the card itself is the lazy `plan` chunk.
 */
export type PlanEvents = Readonly<Record<string, RunPlan>>;
export const PlanEventsContext = createContext<PlanEvents>({});

export function applyRunPlan(current: PlanEvents, data: unknown): PlanEvents {
  const plan = data as RunPlan;
  if (!plan || typeof plan.run_id !== "string" || !Array.isArray(plan.steps)) return current;
  const old = current[plan.run_id];
  return old && old.version >= plan.version ? current : { ...current, [plan.run_id]: plan };
}

/** A step as drawn. `stopped`: the run ended while the step was in progress; `not_done`: the run ended before it. */
export type StepState = "pending" | "in_progress" | "done" | "stopped" | "not_done" | "skipped" | "blocked";
export interface DockStep { id: string; text: string; state: StepState }

const ACTIVE: ReadonlySet<string> = new Set(["queued", "running", "waiting"]);
export const isActiveRun = (run: Pick<Run, "status">) => ACTIVE.has(run.status);

export function planOf(run: Pick<Run, "id" | "plan"> | undefined, events: PlanEvents): RunPlan | undefined {
  if (!run) return undefined;
  const pushed = events[run.id], listed = run.plan;
  return pushed && (!listed || pushed.version >= listed.version) ? pushed : listed;
}

/** A finished run's list as history: what was still in progress stopped, what was still to do was not done. */
export function historySteps(plan: RunPlan, run: Pick<Run, "status">): DockStep[] {
  const failed = run.status === "failed" || run.status === "cancelled" || run.status === "interrupted";
  return plan.steps.map(step => ({ id: step.id, text: step.text, state: step.status === "done" ? "done" : step.status === "in_progress" && failed ? "stopped" : "not_done" }));
}

/**
 * A list is history once its run has ended, unless the work goes on: a later run took it over, or the run ended its
 * turn waiting (the list stays open for the run that carries the work on).
 */
export function planHistory(plan: RunPlan | undefined, run: Pick<Run, "status">): DockStep[] | undefined {
  if (!plan || !plan.steps.length || isActiveRun(run) || plan.closed === "passed" || (plan.open && run.status === "done")) return undefined;
  return historySteps(plan, run);
}

/** An ended list with a step that was not finished: the run's record keeps a line for it even with Activity hidden. */
export function planEndedIncomplete(plan: RunPlan | undefined, run: Pick<Run, "status">): boolean {
  const steps = planHistory(plan, run);
  return Boolean(steps?.some(step => step.state !== "done"));
}

/** The list of a run family (a run and its retries): from the newest attempt that kept one. */
export function familyPlan(attempts: readonly Run[], events: PlanEvents): { plan: RunPlan; run: Run } | undefined {
  for (let index = attempts.length - 1; index >= 0; index--) {
    const plan = planOf(attempts[index], events);
    if (plan) return { plan, run: attempts[index] };
  }
  return undefined;
}
