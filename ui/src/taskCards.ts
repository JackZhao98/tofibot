/**
 * Task cards (decision 0060): plain data the timeline rows and the member list draw. A task is one card; the
 * server's `work_item` events update it in place and add no message. Nothing here touches React, so the
 * coalescer, the anchoring and the roster are tested without a browser.
 */
import type { Message, Run, WorkChecklistItem, WorkItem, WorkStatus } from "./types";

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
  if (a.title !== b.title || a.holderId !== b.holderId || a.holderName !== b.holderName || a.status !== b.status || a.claimed !== b.claimed || a.done !== b.done || a.total !== b.total || a.items.length !== b.items.length) return false;
  return a.items.every((item, index) => { const other = b.items[index]; return item.id === other.id && item.text === other.text && item.status === other.status && item.reason === other.reason; });
}

/**
 * The card model of every task that draws one. A model whose content did not change keeps its object identity
 * (from `previous`), so a row that shows it is skipped by `rowModelEqual`.
 */
export function buildCardModels(tasks: readonly WorkItem[], botName: (id: string) => string | undefined, previous?: ReadonlyMap<string, TaskCardModel>): Map<string, TaskCardModel> {
  const out = new Map<string, TaskCardModel>();
  for (const task of tasks) {
    if (!showsCard(task)) continue;
    const { done, total } = checklistProgress(task.checklist);
    const model: TaskCardModel = {
      id: task.id, title: task.title, holderId: task.bot_id, holderName: botName(task.bot_id) ?? task.bot_name ?? "", status: task.status,
      done, total, items: task.checklist ?? [], claimed: Boolean(task.claimed_at), open: isOpenTask(task.status),
    };
    const old = previous?.get(task.id);
    out.set(task.id, old && sameModel(old, model) ? old : model);
  }
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
    const trigger = task.assignee_run_id ? runById.get(task.assignee_run_id)?.trigger_message_id : undefined;
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
      if (old.updated_at === task.updated_at && old.status === task.status && old.bot_id === task.bot_id && old.claimed_at === task.claimed_at && JSON.stringify(old.checklist) === JSON.stringify(task.checklist)) continue;
    }
    next = next ?? [...current];
    if (index >= 0) next[index] = task; else next.push(task);
  }
  return next ?? current;
}

/** Everything the timeline and the member list draw from the task list, in one pass. */
export function deriveCards(tasks: readonly WorkItem[], botById: ReadonlyMap<string, { name: string }>, messages: readonly Message[], runById: ReadonlyMap<string, Run>, previous?: ReadonlyMap<string, TaskCardModel>) {
  const models = buildCardModels(tasks, id => botById.get(id)?.name, previous);
  return { models, byMessage: anchorCards(tasks, models, messages, runById), roster: rosterTasks(models.values()) };
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
