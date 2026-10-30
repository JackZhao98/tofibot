// Task cards (decision 0060): which tasks draw a card, where a card sits, how task events are coalesced to one
// update a second, what the member list shows, and that an unchanged task keeps its model so its row is skipped.
import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();
try {
  const { showsCard, buildCardModels, anchorCards, rosterTasks, mergeTasks, coalesce, checklistProgress } = await modules.load("/src/taskCards.ts");
  const { rowModelEqual } = await modules.load("/src/timelineRow.ts");

  const task = (id, extra = {}) => ({ id, conversation_id: "g", bot_id: "b1", kind: "task", title: `Task ${id}`, description: "", status: "in_progress", created_at: "2026-10-23T10:00:05Z", updated_at: "2026-10-23T10:00:05Z", ...extra });
  const items = n => Array.from({ length: n }, (_, i) => ({ id: `i${i}`, text: `Item ${i}`, status: i < 2 ? "done" : "open" }));
  const names = id => ({ b1: "Quill", b2: "Atlas" })[id];

  // Which tasks draw a card.
  assert.equal(showsCard(task("a")), true, "an open task draws a card");
  assert.equal(showsCard(task("a", { status: "done" })), false, "a one-turn answer that closed leaves no card");
  assert.equal(showsCard(task("a", { status: "done", checklist: items(3) })), true, "a finished task with a checklist keeps its card");
  assert.equal(showsCard(task("a", { status: "done", assigner_kind: "bot" })), true, "a finished delegated task keeps its card");
  assert.equal(showsCard(task("a", { status: "cancelled", assigner_kind: "bot" })), false, "a cancelled task draws none");
  assert.equal(showsCard(task("a", { kind: "goal" })), false, "a board goal is not a task card");
  assert.deepEqual(checklistProgress(items(7)), { done: 2, total: 7 });

  // Models keep their identity while the task does not change.
  const first = buildCardModels([task("a", { checklist: items(7) })], names);
  const model = first.get("a");
  assert.deepEqual([model.holderName, model.done, model.total, model.claimed, model.open], ["Quill", 2, 7, false, true]);
  const again = buildCardModels([task("a", { checklist: items(7), updated_at: "2026-10-23T10:00:09Z" })], names, first);
  assert.equal(again.get("a"), model, "the same content keeps the same object");
  const claimed = buildCardModels([task("a", { checklist: items(7), claimed_at: "2026-10-23T10:00:10Z" })], names, again);
  assert.notEqual(claimed.get("a"), model, "a claim is a change");
  assert.equal(claimed.get("a").claimed, true);
  assert.equal(rowModelEqual({ taskCards: [model], n: 1 }, { taskCards: [again.get("a")], n: 1 }), true, "a row whose card is unchanged is skipped");
  assert.equal(rowModelEqual({ taskCards: [model], n: 1 }, { taskCards: [claimed.get("a")], n: 1 }), false, "a changed card redraws its row");

  // Anchoring: under the message that started the run, else the last message before the task.
  const msg = (id, at) => ({ id, conversation_id: "g", seq: 1, role: "user", content: id, created_at: at });
  const messages = [msg("m1", "2026-10-23T10:00:00Z"), msg("m2", "2026-10-23T10:00:04Z"), msg("m3", "2026-10-23T10:01:00Z")];
  const tasks = [task("a", { assignee_run_id: "r1" }), task("b", { created_at: "2026-10-23T10:00:30Z" }), task("c", { created_at: "2026-10-22T00:00:00Z" })];
  const cards = buildCardModels(tasks, names);
  const anchors = anchorCards(tasks, cards, messages, new Map([["r1", { id: "r1", trigger_message_id: "m1" }]]));
  assert.deepEqual([...anchors.keys()].sort(), ["m1", "m2"]);
  assert.equal(anchors.get("m1")[0].id, "a", "the run's trigger message holds the card");
  assert.equal(anchors.get("m2")[0].id, "b", "otherwise the last message before the task");
  assert.equal([...anchors.values()].flat().some(card => card.id === "c"), false, "a task older than the loaded history draws nowhere");

  // A paused task (a person's message took the Bot away from it) draws a card with its resume state, under the interrupted run's message.
  const pausedTask = task("p", { status: "todo", interrupted_at: "2026-10-23T10:02:00Z", auto_resume: true, source: "schedule_occurrence", source_run_id: "r2", assignee_run_id: "" });
  const paused = buildCardModels([pausedTask], names).get("p");
  assert.deepEqual([paused.paused, paused.autoResume, paused.open], [true, true, true]);
  assert.equal(buildCardModels([task("p", { status: "todo" })], names).get("p").paused, false, "a task that waits to start is not paused");
  const pausedAnchor = anchorCards([pausedTask], new Map([["p", paused]]), messages, new Map([["r2", { id: "r2", trigger_message_id: "m2" }]]));
  assert.equal(pausedAnchor.get("m2")[0].id, "p", "a paused task sits under the message of the run it was taken from");
  const exhausted = buildCardModels([{ ...pausedTask, auto_resume: false, updated_at: "2026-10-23T10:03:00Z" }], names, new Map([["p", paused]])).get("p");
  assert.notEqual(exhausted, paused, "a task whose automatic resumes ran out is a change");

  // The member list: open tasks per holder, claimed first, nothing for finished ones.
  const roster = rosterTasks(buildCardModels([
    task("a", { claimed_at: "x" }), task("b", { bot_id: "b2" }), task("c", { status: "todo" }), task("d", { status: "done", checklist: items(2) }),
  ], names).values());
  assert.deepEqual(roster.get("b1").map(card => card.id), ["a", "c"], "Quill holds a claimed and an assigned task; the claimed one is first");
  assert.deepEqual(roster.get("b2").map(card => card.id), ["b"]);
  assert.equal(roster.get("b1").some(card => card.id === "d"), false, "a finished task is not in the member list");

  // Merging: the same array when nothing changed, newer wins.
  const base = [task("a", { updated_at: "2026-10-23T10:00:09Z" })];
  assert.equal(mergeTasks(base, [task("a", { updated_at: "2026-10-23T10:00:09Z" })]), base, "an identical copy changes nothing");
  assert.equal(mergeTasks(base, [task("a", { updated_at: "2026-10-23T10:00:01Z", status: "todo" })]), base, "an older copy never wins");
  const next = mergeTasks(base, [task("a", { updated_at: "2026-10-23T10:00:11Z", status: "done" }), task("z")]);
  assert.deepEqual(next.map(item => [item.id, item.status]), [["a", "done"], ["z", "in_progress"]]);

  // Coalescing: at most one update a second, the newest value per task, the first one at once.
  let clock = 0;
  const timers = [];
  const applied = [];
  const co = coalesce(batch => applied.push(batch.map(item => item.v)), 1000, { now: () => clock, set: (fn, ms) => { const handle = { fn, at: clock + ms }; timers.push(handle); return handle; }, clear: handle => { const i = timers.indexOf(handle); if (i >= 0) timers.splice(i, 1); } });
  const run = until => { for (;;) { const due = timers.filter(timer => timer.at <= until).sort((a, b) => a.at - b.at)[0]; if (!due) break; timers.splice(timers.indexOf(due), 1); clock = due.at; due.fn(); } clock = until; };
  co.push("t1", { v: 1 });
  assert.deepEqual(applied, [[1]], "the first event goes out at once");
  clock = 100; co.push("t1", { v: 2 }); clock = 200; co.push("t2", { v: 3 }); clock = 300; co.push("t1", { v: 4 });
  assert.equal(applied.length, 1, "events inside the window wait");
  run(999);
  assert.equal(applied.length, 1, "nothing before the window ends");
  run(1000);
  assert.deepEqual([...applied.at(-1)].sort(), [3, 4], "one update with the newest value of each task");
  run(1500); co.push("t1", { v: 5 });
  assert.equal(applied.length, 2, "inside the second window the event waits again");
  run(2100);
  assert.equal(applied.length, 3);
  const burstStart = clock;
  for (let i = 0; i < 50; i++) { co.push("t1", { v: 100 + i }); run(clock + 10); }
  run(clock + 1000);
  assert.ok(applied.length - 3 <= Math.ceil((clock - burstStart) / 1000) + 1, `a burst of 50 events made ${applied.length - 3} updates`);
  const before = applied.length;
  co.push("t1", { v: 99 }); co.cancel(); run(clock + 5000);
  assert.ok(applied.length <= before + 1, "a cancelled coalescer applies nothing later");
  console.log("task cards: ok");
} finally {
  await modules.close();
}
