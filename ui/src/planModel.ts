/**
 * What the pinned step list card shows (internal/app/run_plans.go is the server side). Plain data, no React: the dock's
 * choice of runs, the two sources of steps (the Bot's own list, or the checklist of the task its run holds), the end
 * states and the history view are tested without a browser (scripts/test-plan-card.mjs).
 */
import { isActiveRun, planOf, type PlanEvents } from "./planMerge";
export { historySteps, planEndedIncomplete, planHistory } from "./planMerge";
import type { Run, RunPlan, WorkChecklistItem, WorkItem } from "./types";

export type { DockStep, StepState } from "./planMerge";
import type { DockStep, StepState } from "./planMerge";
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
  return plan.steps.map(step => ({ id: step.id, text: step.text, state: step.status }));
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
export function dockEntries(runs: readonly Run[], events: PlanEvents, tasks: readonly WorkItem[]): DockEntry[] {
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
      const plan = planOf(run, events);
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
      const plan = planOf(newest, events);
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
export function finishedEntry(previous: DockEntry, runs: readonly Run[], events: PlanEvents): DockEntry | undefined {
  const run = runs.find(item => item.id === previous.runId);
  const plan = planOf(run, events);
  // The run event may arrive before the list's closing event: a done run with every step done is enough.
  if (!run || run.status !== "done" || !plan || !plan.steps.length || plan.steps.some(step => step.status !== "done")) return undefined;
  const steps = planSteps(plan);
  return { ...previous, state: "done", steps, ...summarize(steps) };
}

