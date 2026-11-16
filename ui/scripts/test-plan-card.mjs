// Pinned step list (PlanCard.tsx, planModel.ts, planMerge.ts; server: internal/app/run_plans.go). Synthetic data only.
//  1. Model (no browser): a pushed list replaces a listed one only when newer; one card per Bot, from its working run's
//     own list, else the checklist of the task that run works on, else a list whose run ended its turn waiting on work
//     the Bot still holds; failed, stopped and triage runs draw none; history marks stopped and not-done steps.
//  2. Browser (--browser, Chromium + WebKit, production bundle): the card sits in the composer above the field and the
//     message pane is padded for it; a newer run_plan event moves the marks, an older one changes nothing; the done card
//     stays a moment and leaves; a run that ended before its list was done keeps one history line; in a group the cards
//     fold to one line each, opening one closes the other, and a fourth Bot folds into "+1 more"; at phone width nothing
//     overflows; the card is not in the entry chunk.
//  3. Screenshots (--shots <dir>): 2x, light and dark, desktop and phone width, Chromium and WebKit, DM and group.
// Run: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:plan-card [-- --browser] [-- --shots /abs/dir]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { openUiModules } from "./ui-modules.mjs";

const T = n => `2026-11-15T10:00:${String(n).padStart(2, "0")}.000000000Z`;
const plan = (run_id, bot_id, steps, extra = {}) => ({ run_id, conversation_id: "c", bot_id, version: 1, open: true, updated_at: T(1), steps: steps.map(([text, status], i) => ({ id: `s${i + 1}`, text, status })), ...extra });
const run = (id, bot_id, status, extra = {}) => ({ id, conversation_id: "c", bot_id, status, created_at: T(0), updated_at: T(0), ...extra });

const modules = await openUiModules({ language: "en" });
try {
  const merge = await modules.load("/src/planMerge.ts");
  const model = await modules.load("/src/planModel.ts");
  const p1 = plan("r1", "a", [["Read", "in_progress"]]);
  let events = merge.applyRunPlan({}, p1);
  assert.equal(events.r1, p1);
  assert.equal(merge.applyRunPlan(events, { ...p1 }), events, "the same version changes nothing");
  const p2 = { ...p1, version: 2, steps: [] };
  events = merge.applyRunPlan(events, p2);
  assert.equal(events.r1.steps.length, 0, "a newer cleared list wins");
  assert.equal(merge.applyRunPlan(events, p1), events, "an older list never replaces a newer one");
  assert.equal(merge.planOf({ id: "r1", plan: { ...p1, version: 3 } }, events).version, 3, "a newer listed plan wins over an older event");
  assert.equal(merge.planOf({ id: "r1", plan: p1 }, events), p2, "a newer event wins over an older listed plan");

  const runs = [
    run("ra", "a", "running", { plan: plan("ra", "a", [["Read", "done"], ["Compare", "in_progress"], ["Write", "pending"]]) }),
    run("rb_old", "b", "done", { plan: plan("rb_old", "b", [["Old", "done"]], { open: false, closed: "answered" }) }),
    run("rb", "b", "running", { created_at: T(5) }),
    run("rc", "c", "failed", { plan: plan("rc", "c", [["Try", "in_progress"]]) }),
    run("rd", "d", "done", { plan: plan("rd", "d", [["Give parts", "done"], ["Join", "pending"]]) }),
    run("rt", "t", "running", { kind: "triage", plan: plan("rt", "t", [["No", "pending"]]) }),
    run("re", "e", "done", { plan: plan("re", "e", [["Stale", "pending"]]) }),
  ];
  const tasks = [
    { id: "task_b", kind: "task", title: "Weekly numbers", bot_id: "b", status: "in_progress", assignee_run_id: "rb", checklist: [{ id: "i1", text: "Collect", status: "done" }, { id: "i2", text: "Check", status: "open" }, { id: "i3", text: "Send", status: "open" }] },
    { id: "task_d", kind: "task", title: "Part", bot_id: "x", assigner_bot_id: "d", assigner_kind: "bot", status: "in_progress" },
  ];
  const entries = model.dockEntries(runs, {}, tasks);
  assert.deepEqual(entries.map(entry => `${entry.botId}:${entry.source}:${entry.state}`), ["a:plan:working", "b:checklist:working", "d:plan:waiting"], "working first; failed, triage and a waiting list with no open work draw nothing");
  assert.equal(entries[0].current.text, "Compare");
  assert.deepEqual([entries[0].done, entries[0].total], [1, 3]);
  assert.deepEqual(entries[1].steps.map(step => step.state), ["done", "in_progress", "pending"], "the first open checklist item is the one in progress");
  assert.equal(entries[1].title, "Weekly numbers");
  assert.deepEqual(model.dockEntries([run("w", "a", "waiting", { plan: plan("w", "a", [["Ask", "in_progress"]]) })], {}, [])[0].state, "waiting", "a run parked on the person waits");
  assert.equal(model.dockEntries([run("x", "a", "running", { plan: plan("x", "a", [["Gone", "done"]], { open: false, closed: "stopped" }) })], {}, []).length, 0, "a closed list draws no card");

  const finished = model.finishedEntry(entries[0], [run("ra", "a", "done", { plan: plan("ra", "a", [["Read", "done"], ["Compare", "done"], ["Write", "done"]], { open: false, closed: "answered", version: 4 }) })], {});
  assert.equal(finished.state, "done");
  assert.equal(model.finishedEntry(entries[0], [run("ra", "a", "failed", { plan: plan("ra", "a", [["Read", "done"]]) })], {}), undefined, "a failed run leaves at once");

  const stopped = plan("h", "a", [["Read", "done"], ["Compare", "in_progress"], ["Write", "pending"]]);
  assert.deepEqual(model.historySteps(stopped, { status: "failed" }).map(step => step.state), ["done", "stopped", "not_done"]);
  assert.deepEqual(model.historySteps(stopped, { status: "done" }).map(step => step.state), ["done", "not_done", "not_done"]);
  assert.equal(model.planHistory(stopped, { status: "running" }), undefined, "a working run's list is not history");
  assert.equal(model.planHistory(stopped, { status: "done" }), undefined, "an open list of a run that ended waiting is not history yet");
  assert.equal(model.planHistory({ ...stopped, open: false, closed: "passed" }, { status: "failed" }), undefined, "a list passed to a retry is the retry's history");
  assert.equal(model.planEndedIncomplete({ ...stopped, open: false, closed: "answered" }, { status: "done" }), true);
  assert.equal(model.planEndedIncomplete(plan("ok", "a", [["All", "done"]], { open: false, closed: "answered" }), { status: "done" }), false);
  // A list changes without touching the run's updated_at: a newer listed list rides in on an otherwise stale run.
  const runMerge = await modules.load("/src/runMerge.ts");
  const cached = run("rm", "a", "running", { updated_at: T(9), plan: plan("rm", "a", [["Old", "in_progress"]], { version: 3 }) });
  const listed = { ...cached, updated_at: T(9), plan: { ...cached.plan, version: 7, steps: [{ id: "s1", text: "New", status: "in_progress" }] } };
  assert.equal(runMerge.mergeRunUpdates([cached], [listed])[0].plan.version, 7, "a newer list survives a stale run update");
  assert.equal(runMerge.mergeRunUpdates([listed], [cached])[0].plan.version, 7, "an older list never replaces a newer one");
  // The run's done event can arrive before the list's closing event.
  assert.equal(model.finishedEntry(entries[0], [run("ra", "a", "done", { plan: plan("ra", "a", [["Read", "done"], ["Compare", "done"], ["Write", "done"]], { version: 3 }) })], {}).state, "done", "every step done is enough to linger");
  const family = merge.familyPlan([run("f1", "a", "failed", { plan: stopped }), run("f2", "a", "done")], {});
  assert.equal(family.run.id, "f1", "a family's list comes from its newest attempt that kept one");
  console.log("PASS plan model: versions, sources, states, history");
} finally { await modules.close(); }

const browserRun = process.argv.includes("--browser");
const shotsIndex = process.argv.indexOf("--shots");
const shotsDir = shotsIndex > 0 ? process.argv[shotsIndex + 1] : "";
if (!browserRun && !shotsDir) process.exit(0);
assert.ok(process.env.PLAYWRIGHT_MODULE, "Set PLAYWRIGHT_MODULE to run the browser part");
const { createFixture } = await import("./lib/chat-fixture.mjs");

// ---- fixture data
const BOTS = [["bot_a", "Ledger Cat"], ["bot_b", "Atlas"], ["bot_c", "Juniper"], ["bot_d", "Quill"]];
const at = s => new Date(Date.parse("2026-11-15T10:00:00Z") + s * 1000).toISOString();
const steps = (list) => list.map(([text, status], i) => ({ id: `s${i + 1}`, text, status }));
const LEDGER = [["Read the three expense reports", "done"], ["Check each total against the bank export", "done"], ["Mark the entries that do not match", "in_progress"], ["Draft the summary for the team", "pending"], ["Send the summary to the person", "pending"]];
const dmPlan = (version, list, extra = {}) => ({ run_id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", steps: steps(list), version, open: true, updated_at: at(900 + version), ...extra });
const liveRun = (status, extra = {}) => ({ id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", status, model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m40", queue_seq: 2, created_at: at(860), updated_at: at(870), ...extra });
const doneRun = (extra = {}) => ({ id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m38", queue_seq: 1, created_at: at(780), updated_at: at(785), ...extra });

function dmRoute({ runs }) {
  return async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/runs")) { await route.fulfill({ status: 200, json: { runs: runs() } }); return true; }
    return false;
  };
}

const group = { id: "g_team", kind: "group", name: "Launch team", bot_ids: BOTS.map(([id]) => id), updated_at: at(0), read_seq: 99, user_visible: true };
const groupMessages = [
  { id: "g1", conversation_id: "g_team", seq: 1, role: "user", content: "Prepare the launch: pricing page, release notes, the support macros and the partner email.", created_at: at(10) },
  { id: "g2", conversation_id: "g_team", seq: 2, role: "assistant", content: "On it. I split the work across the team.", sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: "ga", created_at: at(20) },
];
const groupRun = (id, bot, created) => ({ id, conversation_id: "g_team", bot_id: bot, status: "running", model: "synthetic-model", origin_conversation_id: "g_team", trigger_message_id: "g1", queue_seq: created, created_at: at(created), updated_at: at(created) });
const groupPlan = (run_id, bot_id, list) => ({ run_id, conversation_id: "g_team", bot_id, steps: steps(list), version: 1, open: true, updated_at: at(40) });
const groupRuns = () => [
  { ...groupRun("ga", "bot_a", 20), plan: groupPlan("ga", "bot_a", [["Split the launch into parts", "done"], ["Review the pricing page", "in_progress"], ["Join the results", "pending"]]) },
  { ...groupRun("gb", "bot_b", 21), plan: groupPlan("gb", "bot_b", [["Collect the changes since 0.6", "done"], ["Write the release notes", "in_progress"], ["Check the links", "pending"]]) },
  groupRun("gc", "bot_c", 22),
  { ...groupRun("gd", "bot_d", 23), plan: groupPlan("gd", "bot_d", [["Draft the partner email", "in_progress"], ["Ask for a review", "pending"]]) },
];
const groupTasks = [{ id: "t_c", kind: "task", title: "Support macros", conversation_id: "g_team", bot_id: "bot_c", assigner_kind: "bot", assigner_bot_id: "bot_a", status: "in_progress", assignee_run_id: "gc", created_at: at(22), updated_at: at(30), checklist: [{ id: "i1", text: "List the five common questions", status: "done" }, { id: "i2", text: "Write one macro per question", status: "open" }, { id: "i3", text: "Tag them for the launch", status: "open" }] }];
const groupRoute = async route => {
  const path = new URL(route.request().url()).pathname;
  const json = body => route.fulfill({ status: 200, json: body });
  if (path === "/api/bots") { await json({ bots: BOTS.map(([id, name], i) => ({ id, name, instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${i}`, created_at: at(0) })) }); return true; }
  if (path === "/api/conversations") { await json({ conversations: [group] }); return true; }
  if (path.endsWith("/runs")) { await json({ runs: groupRuns() }); return true; }
  if (path.endsWith("/task-cards")) { await json({ tasks: groupTasks }); return true; }
  return false;
};

const MAC = () => { const off = () => () => {}; window.tofiDesktop = { platform: "darwin", themePreference: "light", onCommand: off, onWindowState: off, onFlushState: off, appearance: async () => {}, remoteInputFocus: () => {}, readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {} }; };

const geometry = page => page.evaluate(() => {
  const box = selector => { const element = document.querySelector(selector); if (!element) return null; const r = element.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height }; };
  const blocks = [...document.querySelectorAll(".message-block")];
  const last = blocks.at(-1)?.getBoundingClientRect();
  return { dock: box(".plan-dock"), row: box("form.composer .composer-row"), form: box("form.composer"), inForm: Boolean(document.querySelector("form.composer .plan-dock")), lastBottom: last?.bottom ?? 0, overflow: document.scrollingElement.scrollWidth - innerWidth };
});

async function dmScenario(fixture, { viewport, motion = "no-preference", theme = "light", scale = 1, init } = {}) {
  let state = { runs: () => [doneRun(), liveRun("running", { plan: dmPlan(1, LEDGER) })] };
  const opened = await fixture.open({ viewport, motion, theme, init, contextOptions: { deviceScaleFactor: scale }, route: dmRoute({ runs: () => state.runs() }) });
  await opened.page.locator(".plan-dock .plan-card").waitFor({ timeout: 8000 });
  return { ...opened, setRuns: fn => { state = { runs: fn }; } };
}

const fixtures = {};
async function fixtureFor(engineName) { return fixtures[engineName] ??= await createFixture({ engineName }); }

try {
  if (browserRun) {
    for (const engineName of ["chromium", "webkit"]) {
      const fixture = await fixtureFor(engineName);
      // -- DM: the card above the field, live updates, version order, the done card leaving.
      const { page, context, errors } = await dmScenario(fixture, {});
      const g = await geometry(page);
      assert.ok(g.inForm, `${engineName}: the card is in the composer`);
      assert.ok(g.dock.bottom <= g.row.top + 1, `${engineName}: the card sits above the field (${g.dock.bottom} > ${g.row.top})`);
      assert.ok(g.lastBottom <= g.form.top + 2, `${engineName}: the message pane is padded for the card (${g.lastBottom} > ${g.form.top})`);
      const card = page.locator(".plan-card").first();
      assert.equal(await card.locator(".plan-card-trigger").getAttribute("aria-expanded"), "true", `${engineName}: a single card opens`);
      assert.equal(await card.locator(".plan-steps li").count(), 5);
      assert.equal(await card.locator('li[data-state="in_progress"] .live-mark').count(), 1, `${engineName}: one live mark, on the step in progress`);
      assert.equal(await page.locator(".plan-dock .live-mark").count(), 1, `${engineName}: no second live mark in the card`);
      assert.match(await card.locator(".plan-card-count").textContent(), /2 of 5/);
      await page.evaluate(plan => window.__emit("run_plan", plan), dmPlan(2, LEDGER.map(([text], i) => [text, i < 3 ? "done" : i === 3 ? "in_progress" : "pending"])));
      await page.waitForFunction(() => document.querySelector(".plan-card-count")?.textContent.includes("3 of 5"));
      assert.equal(await card.locator('li[data-state="in_progress"]').textContent(), "Draft the summary for the teamIn progress", `${engineName}: the mark moved to the next step`);
      await page.evaluate(plan => window.__emit("run_plan", plan), dmPlan(1, LEDGER));
      await page.waitForTimeout(400);
      assert.match(await card.locator(".plan-card-count").textContent(), /3 of 5/, `${engineName}: an older list changes nothing`);
      // The person folds it: the header carries the step in progress and the live mark.
      await card.locator(".plan-card-trigger").click();
      await page.waitForFunction(() => document.querySelector(".plan-card-trigger")?.getAttribute("aria-expanded") === "false");
      assert.equal((await card.locator(".plan-card-line").textContent()).trim(), "Draft the summary for the team");
      assert.equal(await card.locator(".plan-card-head .live-mark").count(), 1);
      await card.locator(".plan-card-trigger").click();
      // Every step done and the answer given: the done card stays a moment, then leaves.
      const finalPlan = dmPlan(4, LEDGER.map(([text]) => [text, "done"]), { open: false, closed: "answered" });
      // The server sends the run's done event before the list's closing event (the answer finishes the last step): the
      // card must still hold its done state, whatever renders between the two.
      await page.evaluate(plan => window.__emit("run_plan", plan), dmPlan(3, LEDGER.map(([text], i) => [text, i < 4 ? "done" : "in_progress"])));
      await page.evaluate(finished => window.__emit("run", finished), liveRun("done", { updated_at: at(990) }));
      await page.waitForTimeout(120);
      await page.evaluate(plan => window.__emit("run_plan", plan), finalPlan);
      await page.locator('.plan-card[data-plan-state="done"]').waitFor({ timeout: 4000 });
      await page.mouse.move(5, 5);
      await page.locator(".plan-dock").waitFor({ state: "detached", timeout: 6000 });
      assert.deepEqual(errors, [], `${engineName}: no page errors`);
      await context.close();

      // -- A run that ended before its list was done keeps one quiet history line.
      {
        const stoppedPlan = { run_id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", steps: steps([["Open the ledger", "done"], ["Fix the totals", "in_progress"], ["Report back", "pending"]]), version: 2, open: true, updated_at: at(786) };
        const opened = await fixture.open({ route: dmRoute({ runs: () => [doneRun({ status: "failed", error: "synthetic failure", plan: stoppedPlan })] }) });
        const row = opened.page.locator(".plan-history-trigger");
        await row.waitFor({ timeout: 8000 });
        assert.match(await row.textContent(), /stopped at step 2 of 3/);
        assert.equal(await opened.page.locator(".plan-dock").count(), 0, `${engineName}: a failed run's list is not pinned`);
        await row.click();
        await opened.page.locator('.plan-history-body li[data-state="stopped"]').waitFor();
        assert.deepEqual(opened.errors, []);
        await opened.context.close();
      }

      // -- Group: three cards, one line each; opening one closes the other; the fourth Bot folds into "+1 more".
      {
        const opened = await fixture.open({ path: "/g/g_team", messages: groupMessages, route: groupRoute });
        const { page } = opened;
        await page.locator(".plan-dock .plan-card").first().waitFor({ timeout: 8000 });
        assert.equal(await page.locator(".plan-dock .plan-card").count(), 3);
        assert.match(await page.locator(".plan-dock-more").textContent(), /\+1 more: Quill/);
        assert.equal(await page.locator('.plan-card-trigger[aria-expanded="true"]').count(), 0, `${engineName}: several cards start folded`);
        assert.equal(await page.locator('.plan-card[data-source="checklist"] .plan-card-name').textContent(), "Juniper", `${engineName}: a checklist holder's card`);
        const triggers = page.locator(".plan-card-trigger");
        await triggers.nth(0).click();
        await page.waitForFunction(() => document.querySelectorAll('.plan-card-trigger[aria-expanded="true"]').length === 1);
        await triggers.nth(1).click();
        await page.waitForFunction(() => document.querySelector('.plan-card:nth-child(1) .plan-card-trigger')?.getAttribute("aria-expanded") !== "true" || document.querySelectorAll('.plan-card-trigger[aria-expanded="true"]').length === 1);
        assert.equal(await page.locator('.plan-card-trigger[aria-expanded="true"]').count(), 1, `${engineName}: one card open at a time`);
        assert.equal(await triggers.nth(1).getAttribute("aria-expanded"), "true");
        await page.keyboard.press("Enter");
        assert.deepEqual(opened.errors, []);
        await opened.context.close();
      }

      // -- Phone width and reduced motion: nothing overflows, the list scrolls inside the card.
      {
        const { page, context, errors } = await dmScenario(fixture, { viewport: { width: 390, height: 844 }, motion: "reduce" });
        const g = await geometry(page);
        assert.ok(g.overflow <= 0, `${engineName}: no horizontal overflow at phone width (${g.overflow})`);
        assert.ok(g.dock.right <= 390 && g.dock.left >= 0);
        const list = await page.evaluate(() => { const element = document.querySelector(".plan-steps"); return { max: getComputedStyle(element).maxHeight, height: element.getBoundingClientRect().height }; });
        assert.ok(list.height <= 0.31 * 844 + 1, `${engineName}: the list stays within a third of the screen (${list.height})`);
        assert.deepEqual(errors, []);
        await context.close();
      }
    }
    // The card and its libraries are not in the entry chunk.
    const { readFileSync, readdirSync } = await import("node:fs");
    const fixture = fixtures.chromium;
    const html = await (await fetch(fixture.origin)).text();
    const eager = [...html.matchAll(/src="\/(assets\/[^"]+\.js)"/g)].map(match => match[1]);
    const entry = await Promise.all(eager.map(async file => (await fetch(`${fixture.origin}/${file}`)).text()));
    assert.ok(!entry.join("\n").includes("plan-card-trigger"), "PlanCard is a lazy chunk");
    assert.ok(!entry.join("\n").includes("data-radix-accordion"), "Radix Accordion stays out of the entry");
    void readFileSync; void readdirSync;
    console.log("PASS plan card browser: placement, padding, live updates, version order, done linger, history line, group accordion, phone width (chromium, webkit)");
  }

  if (shotsDir) {
    mkdirSync(shotsDir, { recursive: true });
    for (const engineName of ["chromium", "webkit"]) {
      const fixture = await fixtureFor(engineName);
      for (const theme of ["light", "dark"]) {
        for (const [label, viewport] of [["desktop", { width: 1180, height: 820 }], ["phone", { width: 390, height: 844 }]]) {
          const { page, context } = await dmScenario(fixture, { viewport, theme, scale: 2 });
          await page.waitForTimeout(600);
          await page.screenshot({ path: join(shotsDir, `dm-${label}-${theme}-${engineName}.png`) });
          await context.close();
          const opened = await fixture.open({ path: "/g/g_team", messages: groupMessages, route: groupRoute, viewport, theme, contextOptions: { deviceScaleFactor: 2 } });
          await opened.page.locator(".plan-dock .plan-card").first().waitFor({ timeout: 8000 });
          await opened.page.locator(".plan-card-trigger").first().click();
          await opened.page.waitForTimeout(600);
          await opened.page.screenshot({ path: join(shotsDir, `group-${label}-${theme}-${engineName}.png`) });
          await opened.context.close();
        }
      }
      if (engineName === "chromium") {
        for (const theme of ["light", "dark"]) {
          const { page, context } = await dmScenario(fixture, { viewport: { width: 1180, height: 820 }, theme, scale: 2, init: MAC });
          await page.waitForTimeout(600);
          await page.screenshot({ path: join(shotsDir, `dm-mac-${theme}-chromium.png`) });
          await context.close();
        }
        const stoppedPlan = { run_id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", steps: steps([["Open the ledger", "done"], ["Fix the totals", "in_progress"], ["Report back", "pending"]]), version: 2, open: true, updated_at: at(786) };
        for (const theme of ["light", "dark"]) {
          const opened = await fixture.open({ theme, contextOptions: { deviceScaleFactor: 2 }, route: dmRoute({ runs: () => [doneRun({ status: "failed", error: "synthetic failure", plan: stoppedPlan })] }) });
          await opened.page.locator(".plan-history-trigger").waitFor({ timeout: 8000 });
          await opened.page.locator(".plan-history-trigger").click();
          await opened.page.waitForTimeout(400);
          await opened.page.screenshot({ path: join(shotsDir, `history-${theme}-chromium.png`) });
          await opened.context.close();
        }
      }
    }
    console.log(`shots in ${shotsDir}`);
  }
} finally {
  for (const fixture of Object.values(fixtures)) await fixture.close();
}
