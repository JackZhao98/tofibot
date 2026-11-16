import type { Run, RunPlan } from "./types";

/**
 * A `run_plan` event: the run takes the pushed list when it is newer (by version) than the one it holds. The list then
 * travels with the run (mergeRunUpdates carries a newer listed one onto a run whose updated_at did not move). Entry code,
 * kept to this: everything that draws or reads a list is the lazy `plan` chunk (planModel.ts, PlanCard.tsx).
 */
export function applyRunPlan(runs: Run[], data: unknown): Run[] {
  const plan = data as RunPlan;
  let changed = false;
  const next = runs.map(run => run.id !== plan.run_id || (run.plan && run.plan.version >= plan.version) ? run : (changed = true, { ...run, plan }));
  return changed ? next : runs;
}
