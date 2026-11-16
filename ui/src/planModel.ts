/**
 * What the pinned step list card shows (internal/app/run_plans.go is the server side). Plain data, no React: the dock's
 * choice of runs, the two sources of steps (the Bot's own list, or the checklist of the task its run holds), the end
 * states and the history view are tested without a browser (scripts/test-plan-card.mjs).
 */

import type { PlanStepImage, Run, RunPlan, WorkChecklistItem, WorkItem } from "./types";

/** A step as drawn. `stopped`: the run ended while the step was in progress; `not_done`: the run ended before it. */
export type StepState = "pending" | "in_progress" | "done" | "stopped" | "not_done" | "skipped" | "blocked";
/** `images`: the pictures the step made or used (the server picks them by the step's time), newest last. */
export interface DockStep { id: string; text: string; state: StepState; images?: readonly PlanStepImage[] }

const ACTIVE: ReadonlySet<string> = new Set(["queued", "running", "waiting"]);
export const isActiveRun = (run: Pick<Run, "status">) => ACTIVE.has(run.status);
/** working: the run is at it; waiting: the run waits for the person, or ended its turn waiting on others; done: the run just finished every step. */
export type DockState = "working" | "waiting" | "done";
export interface DockEntry {
  /** One card per Bot: the key stays when a later run of the same work takes the list over. */
  key: string;
  botId: string;
  runId: string;
  source: "plan" | "checklist";
  state: DockState;
  steps: DockStep[];
  done: number;
  total: number;
  /** The step in progress, else the first step still to do. */
  current?: DockStep;
  /** The task the run works on, when it has a visible one. */
  title?: string;
}

const OPEN_TASK: ReadonlySet<string> = new Set(["todo", "in_progress", "blocked", "review", "input_required"]);

export function planSteps(plan: RunPlan): DockStep[] {
  return plan.steps.map(step => ({ id: step.id, text: step.text, state: step.status, images: step.images }));
}

/** A task checklist as steps: the first open item is the one in progress while the run works. */
export function checklistSteps(items: readonly WorkChecklistItem[], working: boolean): DockStep[] {
  let started = !working;
  return items.map(item => {
    const state: StepState = item.status === "done" ? "done" : item.status === "skipped" ? "skipped" : item.status === "blocked" ? "blocked" : started ? "pending" : "in_progress";
    if (state === "in_progress") started = true;
    return { id: item.id, text: item.text, state };
  });
}

function summarize(steps: DockStep[]): Pick<DockEntry, "done" | "total" | "current"> {
  return { done: steps.filter(step => step.state === "done").length, total: steps.length, current: steps.find(step => step.state === "in_progress") ?? steps.find(step => step.state === "pending") };
}

const rank = (run: Run) => run.status === "running" ? 0 : run.status === "waiting" ? 1 : 2;

/**
 * The cards of a conversation: for each Bot, its working run's own list, else the checklist of the task that run works
 * on, else a list whose run ended its turn waiting on work the Bot gave or holds here. A failed or stopped run leaves at
 * once (its issue card carries the failure); a Bot with no list draws no card. Working cards come first.
 */
export function dockEntries(runs: readonly Run[], tasks: readonly WorkItem[]): DockEntry[] {
  const byBot = new Map<string, Run[]>();
  for (const run of runs) {
    if (run.kind === "triage") continue;
    const list = byBot.get(run.bot_id);
    if (list) list.push(run); else byBot.set(run.bot_id, [run]);
  }
  const out: (DockEntry & { at: string })[] = [];
  for (const [botId, list] of byBot) {
    const active = list.filter(isActiveRun).sort((a, b) => rank(a) - rank(b) || (a.created_at < b.created_at ? 1 : -1));
    let entry: (DockEntry & { at: string }) | undefined;
    for (const run of active) {
      const plan = run.plan;
      const state: DockState = run.status === "waiting" ? "waiting" : "working";
      if (plan?.open && plan.steps.length) {
        const steps = planSteps(plan);
        entry = { key: botId, botId, runId: run.id, source: "plan", state, steps, ...summarize(steps), at: run.created_at };
        break;
      }
      const task = tasks.find(item => item.assignee_run_id === run.id && item.kind === "task" && (item.checklist?.length ?? 0) > 0);
      if (task) {
        const steps = checklistSteps(task.checklist ?? [], state === "working");
        entry = { key: botId, botId, runId: run.id, source: "checklist", state, steps, title: task.title, ...summarize(steps), at: run.created_at };
        break;
      }
    }
    if (!entry && !active.length) {
      const newest = [...list].sort((a, b) => a.created_at < b.created_at ? 1 : -1)[0];
      const plan = newest?.plan;
      const holdsWork = tasks.some(task => OPEN_TASK.has(task.status) && (task.bot_id === botId || task.assigner_bot_id === botId));
      if (newest?.status === "done" && plan?.open && plan.steps.length && holdsWork) {
        const steps = planSteps(plan);
        entry = { key: botId, botId, runId: newest.id, source: "plan", state: "waiting", steps, ...summarize(steps), at: newest.created_at };
      }
    }
    if (entry) {
      const held = tasks.find(task => task.assignee_run_id === entry!.runId && task.kind === "task");
      if (held && !entry.title) entry.title = held.title;
      out.push(entry);
    }
  }
  out.sort((a, b) => (a.state === "working" ? 0 : 1) - (b.state === "working" ? 0 : 1) || (a.at < b.at ? -1 : a.at > b.at ? 1 : 0));
  return out.map(({ at: _at, ...entry }) => entry);
}

/** The card of a run that just finished every step: it stays a moment in its done state, then leaves. */
export function finishedEntry(previous: DockEntry, runs: readonly Run[]): DockEntry | undefined {
  const run = runs.find(item => item.id === previous.runId);
  const plan = run?.plan;
  // The run event may arrive before the list's closing event: a done run with every step done is enough.
  if (!run || run.status !== "done" || !plan || !plan.steps.length || plan.steps.some(step => step.status !== "done")) return undefined;
  const steps = planSteps(plan);
  return { ...previous, state: "done", steps, ...summarize(steps) };
}


/**
 * A finished run's list as history: what was still in progress stopped, what was still to do was not done. An answered
 * list finished its step in progress with the answer, so a step the answer never reached was skipped (neutral); a
 * blocked, stopped or failed list keeps "not done".
 */
export function historySteps(plan: RunPlan, run: Pick<Run, "status">): DockStep[] {
  const failed = run.status === "failed" || run.status === "cancelled" || run.status === "interrupted";
  const answered = plan.closed === "answered" && run.status === "done";
  return plan.steps.map(step => ({ id: step.id, text: step.text, state: step.status === "done" ? "done" : step.status === "in_progress" && failed ? "stopped" : answered && step.status === "pending" ? "skipped" : "not_done", images: step.images }));
}

/**
 * A list is history once its run has ended, unless the work goes on: a later run took it over, or the run ended its
 * turn waiting (the list stays open for the run that carries the work on).
 */
export function planHistory(plan: RunPlan | undefined, run: Pick<Run, "status">): DockStep[] | undefined {
  if (!plan || !plan.steps.length || isActiveRun(run) || plan.closed === "passed" || (plan.open && run.status === "done")) return undefined;
  return historySteps(plan, run);
}

/** An ended list with a step that was not finished: the run's record keeps a line for it. */
export function planEndedIncomplete(plan: RunPlan | undefined, run: Pick<Run, "status">): boolean {
  const steps = planHistory(plan, run);
  return Boolean(steps?.some(step => step.state !== "done"));
}

/** The list of a run family (a run and its retries): from the newest attempt that kept one. */
export function familyPlan(attempts: readonly Run[]): { plan: RunPlan; run: Run } | undefined {
  for (let index = attempts.length - 1; index >= 0; index--) {
    const plan = attempts[index].plan;
    if (plan) return { plan, run: attempts[index] };
  }
  return undefined;
}
