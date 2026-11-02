/**
 * Task cards (decision 0060): plain data the timeline rows and the member list draw. A task is one card; the
 * server's `work_item` events update it in place and add no message. Nothing here touches React, so the
 * coalescer, the anchoring and the roster are tested without a browser.
 */
import type { Message, Run, WorkChecklistItem, WorkItem, WorkStatus } from "./types";

/** A part holder's message folded under its parent's card: the holder, the words and when. */
export interface FoldedResult { id: string; taskId: string; sender: string; text: string; at: string }
/** A member the card's holder waits on, and when that wait times out. */
export interface CardWait { name: string; deadlineAt: string }

export interface TaskCardModel {
  id: string;
  title: string;
  holderId: string;
  holderName: string;
  status: WorkStatus;
  done: number;
  total: number;
  items: WorkChecklistItem[];
  claimed: boolean;
  open: boolean;
  /** Left unfinished when a person's message took the Bot away; it waits to be resumed. */
  paused: boolean;
  /** The Bot starts a paused task again by itself. */
  autoResume: boolean;
  /** What the holder waits on (a task result of a member), earliest deadline first. */
  waiting?: CardWait[];
  /** The one line the holder keeps for its status (task_update progress). */
  progressLine?: string;
  /** Messages of the parts' holders, folded here (their task_id names the part). */
  results?: FoldedResult[];
}

/** Whole minutes from `now` to `deadline`; negative once it passed. NaN for no deadline. */
export function minutesLeft(deadline: string | undefined, now: number): number {
  const at = deadline ? Date.parse(deadline) : NaN;
  return Number.isNaN(at) ? NaN : Math.ceil((at - now) / 60_000);
}

const OPEN: ReadonlySet<WorkStatus> = new Set(["todo", "in_progress", "blocked", "review", "input_required"]);

export function isOpenTask(status: WorkStatus): boolean { return OPEN.has(status); }

/** A task draws a card while it is open, or when it carries a checklist or came from another Bot. A one-turn answer to a person leaves none. */
export function showsCard(task: WorkItem): boolean {
  if (task.kind !== "task" || task.status === "cancelled") return false;
  return isOpenTask(task.status) || (task.checklist?.length ?? 0) > 0 || task.assigner_kind === "bot";
}

export function checklistProgress(items: readonly WorkChecklistItem[] | undefined): { done: number; total: number } {
  const list = items ?? [];
  return { done: list.filter(item => item.status === "done").length, total: list.length };
}

function sameModel(a: TaskCardModel, b: TaskCardModel): boolean {
  if (a.title !== b.title || a.holderId !== b.holderId || a.holderName !== b.holderName || a.status !== b.status || a.claimed !== b.claimed || a.paused !== b.paused || a.autoResume !== b.autoResume || a.done !== b.done || a.total !== b.total || a.items.length !== b.items.length || a.progressLine !== b.progressLine) return false;
  if (JSON.stringify(a.waiting) !== JSON.stringify(b.waiting) || JSON.stringify(a.results) !== JSON.stringify(b.results)) return false;
  return a.items.every((item, index) => { const other = b.items[index]; return item.id === other.id && item.text === other.text && item.status === other.status && item.reason === other.reason && item.task_id === other.task_id && item.task_status === other.task_status && item.deadline_at === other.deadline_at && item.progress_line === other.progress_line && item.bot_name === other.bot_name; });
}

/**
 * The card model of every task that draws one. A model whose content did not change keeps its object identity
 * (from `previous`), so a row that shows it is skipped by `rowModelEqual`.
 */
export function buildCardModels(tasks: readonly WorkItem[], botName: (id: string) => string | undefined, previous?: ReadonlyMap<string, TaskCardModel>, results?: ReadonlyMap<string, FoldedResult[]>): Map<string, TaskCardModel> {
  const out = new Map<string, TaskCardModel>();
  for (const task of tasks) {
    if (!showsCard(task)) continue;
    const { done, total } = checklistProgress(task.checklist);
    const model: TaskCardModel = {
      id: task.id, title: task.title, holderId: task.bot_id, holderName: botName(task.bot_id) ?? task.bot_name ?? "", status: task.status,
      done, total, items: task.checklist ?? [], claimed: Boolean(task.claimed_at), open: isOpenTask(task.status),
      paused: task.status === "todo" && Boolean(task.interrupted_at), autoResume: Boolean(task.auto_resume),
    };
    const waiting = (task.waiting_on ?? []).filter(wait => wait.kind === "task_result").map(wait => ({ name: wait.target_name ?? "", deadlineAt: wait.deadline_at })).sort((x, y) => x.deadlineAt < y.deadlineAt ? -1 : x.deadlineAt > y.deadlineAt ? 1 : 0);
    if (waiting.length && model.open) model.waiting = waiting;
    if (task.progress_line && model.open) model.progressLine = task.progress_line;
    const folded = results?.get(task.id);
    if (folded?.length) model.results = folded;
    const old = previous?.get(task.id);
    out.set(task.id, old && sameModel(old, model) ? old : model);
  }
  return out;
}

/**
 * The messages of part holders that fold under their parent's card: a message whose task_id names a task with a parent whose
 * card is drawn. A message with files keeps its own place (the card shows words only).
 */
export function foldParts(tasks: readonly WorkItem[], cards: ReadonlyMap<string, unknown>, messages: readonly Message[], botName: (id: string) => string | undefined): { folded: Set<string>; results: Map<string, FoldedResult[]> } {
  const folded = new Set<string>();
  const results = new Map<string, FoldedResult[]>();
  if (!cards.size) return { folded, results };
  const byId = new Map(tasks.map(task => [task.id, task]));
  for (const message of messages) {
    const part = message.task_id ? byId.get(message.task_id) : undefined;
    const parent = part?.parent_task_id;
    if (!part || !parent || !cards.has(parent) || message.attachments?.length || message.local) continue;
    folded.add(message.id);
    const entry: FoldedResult = { id: message.id, taskId: part.id, sender: botName(message.sender_bot_id ?? part.bot_id) ?? message.sender_bot_name ?? part.bot_name ?? "", text: message.content, at: message.created_at };
    const list = results.get(parent);
    if (list) list.push(entry); else results.set(parent, [entry]);
  }
  return { folded, results };
}

/** A part the lead's card lists as an item has no card of its own in the timeline: the plan item stands for it. */
export function plannedPartIds(tasks: readonly WorkItem[], cards: ReadonlyMap<string, unknown>): Set<string> {
  const out = new Set<string>();
  for (const task of tasks) if (cards.has(task.id)) for (const item of task.checklist ?? []) if (item.task_id) out.add(item.task_id);
  return out;
}

/**
 * The message each card sits under: the message that started the task's run, else the last message at or before
 * the task's creation. A task older than the loaded history has no anchor and draws nowhere.
 */
export function anchorCards(tasks: readonly WorkItem[], cards: ReadonlyMap<string, TaskCardModel>, messages: readonly Message[], runById: ReadonlyMap<string, Run>): Map<string, TaskCardModel[]> {
  const out = new Map<string, TaskCardModel[]>();
  if (!cards.size || !messages.length) return out;
  const byId = new Map(messages.map(message => [message.id, message]));
  for (const task of [...tasks].sort((a, b) => a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0)) {
    const card = cards.get(task.id);
    if (!card) continue;
    const trigger = task.assignee_run_id || task.source_run_id ? runById.get(task.assignee_run_id || task.source_run_id!)?.trigger_message_id : undefined;
    let anchor = trigger ? byId.get(trigger) : undefined;
    if (!anchor) for (const message of messages) if (message.created_at <= task.created_at) anchor = message;
    if (!anchor) continue;
    const list = out.get(anchor.id);
    if (list) list.push(card); else out.set(anchor.id, [card]);
  }
  return out;
}

/** The open tasks each member holds, for the member list. A task a member claimed sorts first. */
export function rosterTasks(cards: Iterable<TaskCardModel>): Map<string, TaskCardModel[]> {
  const out = new Map<string, TaskCardModel[]>();
  for (const card of cards) {
    if (!card.open) continue;
    const list = out.get(card.holderId);
    if (list) list.push(card); else out.set(card.holderId, [card]);
  }
  for (const list of out.values()) list.sort((a, b) => Number(b.claimed) - Number(a.claimed));
  return out;
}

/** Replace the tasks that arrived, keep the rest; the same array comes back when nothing changed. */
export function mergeTasks(current: readonly WorkItem[], incoming: readonly WorkItem[]): readonly WorkItem[] {
  let next: WorkItem[] | null = null;
  for (const task of incoming) {
    const list = next ?? current;
    const index = list.findIndex(item => item.id === task.id);
    if (index >= 0) {
      const old = list[index];
      // An older copy (a fetch that crossed a newer event) never replaces a newer one; an identical one changes nothing.
      if (old.updated_at > task.updated_at) continue;
      if (old.updated_at === task.updated_at && old.status === task.status && old.bot_id === task.bot_id && old.claimed_at === task.claimed_at && old.interrupted_at === task.interrupted_at && old.progress_line === task.progress_line && old.deadline_at === task.deadline_at && JSON.stringify(old.checklist) === JSON.stringify(task.checklist) && JSON.stringify(old.waiting_on) === JSON.stringify(task.waiting_on)) continue;
    }
    next = next ?? [...current];
    if (index >= 0) next[index] = task; else next.push(task);
  }
  return next ?? current;
}

/** Everything the timeline and the member list draw from the task list, in one pass. */
export function deriveCards(tasks: readonly WorkItem[], botById: ReadonlyMap<string, { name: string }>, messages: readonly Message[], runById: ReadonlyMap<string, Run>, previous?: ReadonlyMap<string, TaskCardModel>) {
  const name = (id: string) => botById.get(id)?.name;
  const base = buildCardModels(tasks, name);
  const { folded, results } = foldParts(tasks, base, messages, name);
  const models = results.size ? buildCardModels(tasks, name, previous, results) : buildCardModels(tasks, name, previous);
  // A folded message draws no row, so it cannot anchor a card; a planned part draws no card of its own.
  const planned = plannedPartIds(tasks, models);
  const drawn = tasks.filter(task => !planned.has(task.id));
  const anchored = folded.size ? messages.filter(message => !folded.has(message.id)) : messages;
  return { models, folded, byMessage: anchorCards(drawn, models, anchored, runById), roster: rosterTasks(models.values()), progressByRun: progressByRun(tasks) };
}

/** The one-line status each working run's holder keeps (task_update progress): the live line shows it in place of the generic step label. */
export function progressByRun(tasks: readonly WorkItem[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const task of tasks) if (task.progress_line && task.assignee_run_id && isOpenTask(task.status)) out.set(task.assignee_run_id, task.progress_line);
  return out;
}

export interface TaskFeed { push: (event: object) => void; cancel: () => void }

/** Keeps a conversation's task list current: one fetch now, then the pushed task events at most once a second. */
export function startFeed(conversationId: string, setItems: (update: (current: readonly WorkItem[]) => readonly WorkItem[]) => void, load: (id: string) => Promise<{ tasks: WorkItem[] }>): TaskFeed {
  let live = true;
  const events = coalesce<WorkItem>(batch => setItems(current => mergeTasks(current, batch)), 1000);
  void load(conversationId).then(result => { if (live) setItems(current => mergeTasks(current, result.tasks ?? [])); }).catch(() => undefined);
  return { push(event) { const task = event as WorkItem; if (task.kind === "task" && task.id) events.push(task.id, task); }, cancel() { live = false; events.cancel(); } };
}

export interface Coalescer<T> { push: (key: string, value: T) => void; cancel: () => void }

/**
 * Hands the newest value per key to `apply` at most once per `intervalMs`: the first push goes out at once, the ones
 * inside the window wait for its end and arrive together. A burst of task events is one update.
 */
export function coalesce<T>(apply: (batch: T[]) => void, intervalMs: number, clock: { now: () => number; set: (fn: () => void, ms: number) => unknown; clear: (handle: unknown) => void } = { now: () => Date.now(), set: (fn, ms) => window.setTimeout(fn, ms), clear: handle => window.clearTimeout(handle as number) }): Coalescer<T> {
  const pending = new Map<string, T>();
  let last = -Infinity;
  let timer: unknown = null;
  const flush = () => {
    timer = null;
    if (!pending.size) return;
    const batch = [...pending.values()];
    pending.clear();
    last = clock.now();
    apply(batch);
  };
  return {
    push(key, value) {
      pending.set(key, value);
      if (timer !== null) return;
      const wait = Math.max(0, last + intervalMs - clock.now());
      if (wait === 0) flush(); else timer = clock.set(flush, wait);
    },
    cancel() { if (timer !== null) clock.clear(timer); timer = null; pending.clear(); },
  };
}
