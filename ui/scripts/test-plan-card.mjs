// Pinned step list (PlanCard.tsx, planModel.ts, planMerge.ts; server: internal/app/run_plans.go). Synthetic data only.
//  1. Model (no browser): a pushed list replaces a listed one only when newer; one card per Bot, from its working run's
//     own list, else the checklist of the task that run works on, else a list whose run ended its turn waiting on work
//     the Bot still holds; failed, stopped and triage runs draw none; history marks stopped and not-done steps.
//  2. Browser (--browser, Chromium + WebKit, production bundle): the card sits in the composer above the field and the
//     message pane is padded for it; a newer run_plan event moves the marks, an older one changes nothing; the done card
//     stays a moment and leaves; a run that ended before its list was done keeps one history line; in a group the cards
//     fold to one line each, opening one closes the other, and a fourth Bot folds into "+1 more"; at phone width nothing
//     overflows; the card is not in the entry chunk. A step with pictures shows a small stack that opens the viewer;
//     a picture whose file is gone drops out. The team board is gone: a group's open to-dos are one line under the
//     cards that opens the Work panel, the More menu has Tasks, and the Work panel filters by goal and changes
//     the assignee of a group's item.
//  3. Screenshots (--shots <dir>): 2x, light and dark, desktop and phone width, Chromium and WebKit, DM and group.
// Run: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:plan-card [-- --browser] [-- --shots /abs/dir]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { openUiModules } from "./ui-modules.mjs";
import { pickSelect } from "./lib/select.mjs";

const T = n => `2026-11-15T10:00:${String(n).padStart(2, "0")}.000000000Z`;
const plan = (run_id, bot_id, steps, extra = {}) => ({ run_id, conversation_id: "c", bot_id, version: 1, open: true, updated_at: T(1), steps: steps.map(([text, status], i) => ({ id: `s${i + 1}`, text, status })), ...extra });
const run = (id, bot_id, status, extra = {}) => ({ id, conversation_id: "c", bot_id, status, created_at: T(0), updated_at: T(0), ...extra });

const modules = await openUiModules({ language: "en" });
try {
  const merge = await modules.load("/src/planMerge.ts");
  const model = await modules.load("/src/planModel.ts");
  // A pushed list replaces the run's list only when newer; an unchanged list keeps the array (no re-render).
  const p1 = plan("r1", "a", [["Read", "in_progress"]]);
  let held = merge.applyRunPlan([run("r1", "a", "running")], p1);
  assert.equal(held[0].plan, p1);
  assert.equal(merge.applyRunPlan(held, { ...p1 }), held, "the same version changes nothing");
  const p2 = { ...p1, version: 2, steps: [] };
  held = merge.applyRunPlan(held, p2);
  assert.equal(held[0].plan.steps.length, 0, "a newer cleared list wins");
  assert.equal(merge.applyRunPlan(held, p1), held, "an older list never replaces a newer one");
  assert.equal(merge.applyRunPlan(held, { ...p1, run_id: "other", version: 9 }), held, "a list of a run not held changes nothing");
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
  const entries = model.dockEntries(runs, tasks);
  assert.deepEqual(entries.map(entry => `${entry.botId}:${entry.source}:${entry.state}`), ["a:plan:working", "b:checklist:working", "d:plan:waiting"], "working first; failed, triage and a waiting list with no open work draw nothing");
  assert.equal(entries[0].current.text, "Compare");
  assert.deepEqual([entries[0].done, entries[0].total], [1, 3]);
  assert.deepEqual(entries[1].steps.map(step => step.state), ["done", "in_progress", "pending"], "the first open checklist item is the one in progress");
  assert.equal(entries[1].title, "Weekly numbers");
  assert.deepEqual(model.dockEntries([run("w", "a", "waiting", { plan: plan("w", "a", [["Ask", "in_progress"]]) })], [])[0].state, "waiting", "a run parked on the person waits");
  assert.equal(model.dockEntries([run("x", "a", "running", { plan: plan("x", "a", [["Gone", "done"]], { open: false, closed: "stopped" }) })], []).length, 0, "a closed list draws no card");

  const finished = model.finishedEntry(entries[0], [run("ra", "a", "done", { plan: plan("ra", "a", [["Read", "done"], ["Compare", "done"], ["Write", "done"]], { open: false, closed: "answered", version: 4 }) })]);
  assert.equal(finished.state, "done");
  assert.equal(model.finishedEntry(entries[0], [run("ra", "a", "failed", { plan: plan("ra", "a", [["Read", "done"]]) })]), undefined, "a failed run leaves at once");

  const stopped = plan("h", "a", [["Read", "done"], ["Compare", "in_progress"], ["Write", "pending"]]);
  assert.deepEqual(model.historySteps(stopped, { status: "failed" }).map(step => step.state), ["done", "stopped", "not_done"]);
  assert.deepEqual(model.historySteps(stopped, { status: "done" }).map(step => step.state), ["done", "not_done", "not_done"]);
  // An answered list finished its step in progress with the answer: the steps it never reached were skipped.
  assert.deepEqual(model.historySteps({ ...stopped, steps: stopped.steps.map((step, i) => ({ ...step, status: i < 2 ? "done" : "pending" })), open: false, closed: "answered" }, { status: "done" }).map(step => step.state), ["done", "done", "skipped"]);
  assert.deepEqual(model.historySteps({ ...stopped, open: false, closed: "blocked" }, { status: "done" }).map(step => step.state), ["done", "not_done", "not_done"], "a blocked answer leaves its steps not done");
  // A step's pictures ride along to the card and the history.
  const pictured = plan("pic", "a", [["Look", "in_progress"]]);
  pictured.steps[0].images = [{ id: "att_1", kind: "capture" }];
  assert.deepEqual(model.planSteps(pictured)[0].images, [{ id: "att_1", kind: "capture" }]);
  assert.deepEqual(model.historySteps({ ...pictured, open: false, closed: "stopped" }, { status: "cancelled" })[0].images, [{ id: "att_1", kind: "capture" }]);
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
  assert.equal(model.finishedEntry(entries[0], [run("ra", "a", "done", { plan: plan("ra", "a", [["Read", "done"], ["Compare", "done"], ["Write", "done"]], { version: 3 }) })]).state, "done", "every step done is enough to linger");
  const family = model.familyPlan([run("f1", "a", "failed", { plan: stopped }), run("f2", "a", "done")]);
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
// A step's pictures, by step number: the server picks them by the step's time (run_plans.go).
const STEP_IMAGES = { 1: [{ id: "img_view", kind: "viewed" }], 2: [{ id: "img_cap1", kind: "capture" }, { id: "img_cap2", kind: "capture" }, { id: "img_cap3", kind: "capture" }], 3: [{ id: "img_gone", kind: "capture" }, { id: "img_cap4", kind: "capture" }] };
const steps = (list, pictures = STEP_IMAGES) => list.map(([text, status], i) => ({ id: `s${i + 1}`, text, status, ...(pictures[i + 1] ? { images: pictures[i + 1] } : {}) }));
const LEDGER = [["Read the three expense reports", "done"], ["Check each total against the bank export", "done"], ["Mark the entries that do not match", "in_progress"], ["Draft the summary for the team", "pending"], ["Send the summary to the person", "pending"]];

// Synthetic screen pictures for the thumbnails: a page with a coloured bar and grey lines, one hue per picture.
const { deflateSync } = await import("node:zlib");
function crc32(bytes) { let c, crc = 0xffffffff; for (const byte of bytes) { c = (crc ^ byte) & 0xff; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; crc = (crc >>> 8) ^ c; } return (crc ^ 0xffffffff) >>> 0; }
function syntheticPNG(hue) {
  const w = 168, h = 120, rows = [];
  const rgb = (r, g, b) => [r, g, b];
  const bar = rgb(Math.round(120 + 100 * Math.cos(hue)), Math.round(120 + 100 * Math.cos(hue - 2.1)), Math.round(120 + 100 * Math.cos(hue + 2.1)));
  for (let y = 0; y < h; y++) {
    const row = [0];
    for (let x = 0; x < w; x++) {
      let px = rgb(246, 246, 244);
      if (y < 22) px = bar;
      else if ((y - 34) % 16 < 6 && y > 30 && x > 14 && x < (y % 32 < 16 ? 150 : 112)) px = rgb(206, 208, 212);
      else if (x > 112 && x < 156 && y > 34 && y < 78) px = bar.map(v => Math.round(v * 0.55 + 246 * 0.45));
      row.push(...px);
    }
    rows.push(...row);
  }
  const chunk = (type, data) => { const len = Buffer.alloc(4); len.writeUInt32BE(data.length); const body = Buffer.concat([Buffer.from(type), data]); const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(body)); return Buffer.concat([len, body, crc]); };
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(Buffer.from(rows))), chunk("IEND", Buffer.alloc(0))]);
}
const PICTURES = Object.fromEntries(["img_view", "img_cap1", "img_cap2", "img_cap3", "img_cap4"].map((id, i) => [id, syntheticPNG(0.4 + i * 1.3)]));
// The thumbnails' files; a picture whose file is gone answers 404 and drops out of the row.
async function pictureRoute(route) {
  const path = new URL(route.request().url()).pathname;
  const id = path.startsWith("/api/attachments/") ? decodeURIComponent(path.split("/")[3] ?? "") : "";
  if (!id) return false;
  if (PICTURES[id]) await route.fulfill({ status: 200, contentType: "image/png", body: PICTURES[id] });
  else await route.fulfill({ status: 404, json: { error: "not found" } });
  return true;
}
const dmPlan = (version, list, extra = {}, pictures = STEP_IMAGES) => ({ run_id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", steps: steps(list, pictures), version, open: true, updated_at: at(900 + version), ...extra });
const liveRun = (status, extra = {}) => ({ id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", status, model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m40", queue_seq: 2, created_at: at(860), updated_at: at(870), ...extra });
const doneRun = (extra = {}) => ({ id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m38", queue_seq: 1, created_at: at(780), updated_at: at(785), ...extra });

function dmRoute({ runs }) {
  return async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/runs")) { await route.fulfill({ status: 200, json: { runs: runs() } }); return true; }
    return pictureRoute(route);
  };
}

const group = { id: "g_team", kind: "group", name: "Launch team", bot_ids: BOTS.map(([id]) => id), updated_at: at(0), read_seq: 99, user_visible: true };
const groupMessages = [
  { id: "g1", conversation_id: "g_team", seq: 1, role: "user", content: "Prepare the launch: pricing page, release notes, the support macros and the partner email.", created_at: at(10) },
  { id: "g2", conversation_id: "g_team", seq: 2, role: "assistant", content: "On it. I split the work across the team.", sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: "ga", created_at: at(20) },
];
const groupRun = (id, bot, created) => ({ id, conversation_id: "g_team", bot_id: bot, status: "running", model: "synthetic-model", origin_conversation_id: "g_team", trigger_message_id: "g1", queue_seq: created, created_at: at(created), updated_at: at(created) });
const groupPlan = (run_id, bot_id, list, pictures = {}) => ({ run_id, conversation_id: "g_team", bot_id, steps: steps(list, pictures), version: 1, open: true, updated_at: at(40) });
const groupRuns = () => [
  { ...groupRun("ga", "bot_a", 20), plan: groupPlan("ga", "bot_a", [["Split the launch into parts", "done"], ["Review the pricing page", "in_progress"], ["Join the results", "pending"]]) },
  { ...groupRun("gb", "bot_b", 21), plan: groupPlan("gb", "bot_b", [["Collect the changes since 0.6", "done"], ["Write the release notes", "in_progress"], ["Check the links", "pending"]], { 1: [{ id: "img_cap1", kind: "capture" }, { id: "img_cap2", kind: "capture" }] }) },
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
  return pictureRoute(route);
};
// The Work panel's read for the group: a goal, the task under it, and one task without a goal; PATCHes are recorded.
const groupWork = [
  { id: "g_goal", kind: "goal", title: "Ship the launch", conversation_id: "g_team", bot_id: "bot_a", status: "todo", created_at: at(5), updated_at: at(5) },
  { ...groupTasks[0], parent_goal_id: "g_goal" },
  { id: "t_x", kind: "task", title: "Update the FAQ", conversation_id: "g_team", bot_id: "bot_b", status: "todo", created_at: at(24), updated_at: at(24) },
];
const groupWorkRoute = patches => async route => {
  const request = route.request();
  const url = new URL(request.url());
  if (url.pathname.endsWith("/task-cards")) { await route.fulfill({ status: 200, json: { tasks: groupWork.filter(item => item.kind === "task") } }); return true; }
  if (url.pathname === "/api/conversations/g_team/work-items") { await route.fulfill({ status: 200, json: { work_items: url.searchParams.get("history") === "true" ? [] : groupWork } }); return true; }
  if (url.pathname.startsWith("/api/work-items/") && request.method() === "PATCH") {
    const body = request.postDataJSON();
    patches.push({ id: decodeURIComponent(url.pathname.split("/").at(-1)), ...body });
    await route.fulfill({ status: 200, json: { ...groupWork.find(item => url.pathname.endsWith(item.id)), ...body } });
    return true;
  }
  return groupRoute(route);
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
      assert.equal(await card.locator('li[data-state="in_progress"].is-active').count(), 1, `${engineName}: one live mark, on the step in progress`);
      assert.equal(await page.locator(".plan-dock .live-mark, .plan-dock .tb-plan-step.is-active").count(), 1, `${engineName}: no second live mark in the card`);
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

      // -- Pictures: a step that made or used pictures shows a small stack at the row's end, newest on top; it opens the
      //    image viewer on the newest; a picture whose file is gone drops out; a picture arriving later shows at once.
      {
        const { page, context, errors } = await dmScenario(fixture, {});
        const card = page.locator(".plan-card").first();
        await page.waitForFunction(() => { const images = [...document.querySelectorAll(".plan-step-pictures img")]; return images.length === 5 && images.every(image => image.complete && image.naturalWidth > 0); }, null, { timeout: 8000 });
        const counts = () => card.locator(".plan-steps li").evaluateAll(items => items.map(item => item.querySelectorAll(".plan-step-pictures img").length));
        assert.deepEqual(await counts(), [1, 3, 1, 0, 0], `${engineName}: the pictures of each step, without the one whose file is gone`);
        const second = card.locator(".plan-steps li").nth(1);
        const stack = second.locator(".plan-step-pictures");
        assert.equal(await stack.getAttribute("aria-label"), "Open the 3 pictures of this step: Check each total against the bank export");
        const [row, box, text] = await Promise.all([second.boundingBox(), stack.boundingBox(), second.locator(".plan-step-text").boundingBox()]);
        assert.ok(box.x >= text.x + text.width - 1 && box.x <= text.x + text.width + 12 && box.x + box.width <= row.x + row.width + 4.5, `${engineName}: the stack sits right after the step's text (${JSON.stringify({ text, box })})`);
        assert.ok(row.height <= 30, `${engineName}: the pictures do not make the row taller (${row.height})`);
        await stack.click();
        const viewer = page.locator("dialog.attachment-lightbox");
        await viewer.waitFor({ timeout: 6000 });
        assert.match(await viewer.locator("img").first().getAttribute("src"), /img_cap3$/, `${engineName}: the viewer opens on the newest picture`);
        await page.keyboard.press("Escape");
        await viewer.waitFor({ state: "detached", timeout: 4000 });
        assert.equal(await page.evaluate(() => document.activeElement?.classList.contains("plan-step-pictures")), true, `${engineName}: focus goes back to the stack`);
        await page.evaluate(plan => window.__emit("run_plan", plan), dmPlan(2, LEDGER, {}, { ...STEP_IMAGES, 4: [{ id: "img_view", kind: "file" }] }));
        await page.waitForFunction(() => document.querySelectorAll(".plan-steps li")[3]?.querySelectorAll(".plan-step-pictures img").length === 1);
        assert.deepEqual(errors, [], `${engineName}: no page errors`);
        await context.close();
      }

      // -- A run that ended before its list was done keeps one quiet history line (a stopped run; a failed one draws its list in the failure card, V3 4.4).
      {
        const stoppedPlan = { run_id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", steps: steps([["Open the ledger", "done"], ["Fix the totals", "in_progress"], ["Report back", "pending"]]), version: 2, open: true, updated_at: at(786) };
        const opened = await fixture.open({ route: dmRoute({ runs: () => [doneRun({ status: "cancelled", plan: stoppedPlan })] }) });
        const row = opened.page.locator(".plan-history-trigger");
        await row.waitFor({ timeout: 8000 });
        assert.match(await row.textContent(), /stopped at step 2 of 3/);
        assert.equal(await opened.page.locator(".plan-dock").count(), 0, `${engineName}: a failed run's list is not pinned`);
        await row.click();
        await opened.page.locator('.plan-history-body li[data-state="stopped"]').waitFor();
        assert.deepEqual(opened.errors, []);
        await opened.context.close();
        const failed = await fixture.open({ route: dmRoute({ runs: () => [doneRun({ status: "failed", error: "synthetic failure", plan: stoppedPlan })] }) });
        await failed.page.locator(".task-issue-card .tb-plan-step.is-failed").waitFor({ timeout: 8000 });
        assert.equal(await failed.page.locator(".plan-history-trigger").count(), 0, `${engineName}: a failed run's list is in its failure card, not a history row`);
        assert.equal(await failed.page.locator(".task-issue-card .tb-plan-step").count(), 3, `${engineName}: the failure card draws every step`);
        await failed.context.close();
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

      // -- Group without the team board: the open to-dos line opens the Work panel; goal filter; assignee change.
      {
        const patches = [];
        const opened = await fixture.open({ path: "/g/g_team", messages: groupMessages, route: groupWorkRoute(patches) });
        const { page } = opened;
        await page.locator(".plan-dock-tasks").waitFor({ timeout: 8000 });
        assert.equal((await page.locator(".plan-dock-tasks").textContent()).trim(), "2 open to-dos", `${engineName}: the open to-dos of this group`);
        assert.equal(await page.locator('.header-actions [data-hint="Team board"]').count(), 0, `${engineName}: no board button`);
        // V3 PR 3.2: the header holds at most three actions; Tasks moved into the More menu (PLAN 9.3 row 3.2).
        assert.equal(await page.locator('.header-actions [data-hint="Tasks"]').count(), 0, `${engineName}: Tasks is not a header button`);
        await page.locator(".header-more > button").click();
        await page.locator('.more-menu[data-state=open] [role=menuitem]').filter({ hasText: "Tasks" }).waitFor({ timeout: 4000 });
        await page.keyboard.press("Escape");
        await page.locator(".plan-dock-tasks").click();
        const panel = page.locator(".work-panel");
        await panel.locator(".work-row").first().waitFor({ timeout: 8000 });
        const titles = () => panel.locator(".work-row-main > strong").allTextContents();
        assert.deepEqual(await titles(), ["Ship the launch", "Support macros", "Update the FAQ"]);
        await pickSelect(panel.locator("button.work-goal-filter"), "g_goal");
        assert.deepEqual(await titles(), ["Ship the launch", "Support macros"], `${engineName}: the goal and the to-dos under it`);
        await pickSelect(panel.locator("button.work-goal-filter"), "");
        const faq = panel.locator(".work-row", { hasText: "Update the FAQ" });
        await faq.locator(".work-row-main > .disclosure > .disclosure-toggle").click();
        // The drawer opens over --m-base; a Select opened inside it meanwhile is placed against a moving trigger.
        await faq.locator(".work-row-main > .disclosure > .disclosure-region").evaluate(region => Promise.all(region.getAnimations().map(animation => animation.finished)));
        const patched = page.waitForResponse(response => response.request().method() === "PATCH" && response.url().includes("/api/work-items/"));
        await pickSelect(faq.locator('button.ui-select-trigger[aria-label="Assignee of Update the FAQ"]'), "bot_c");
        await patched;
        assert.deepEqual(patches, [{ id: "t_x", bot_id: "bot_c" }], `${engineName}: the assignee change is one PATCH`);
        // WebKit now and then reports the ResizeObserver loop notice when any Select opens in the Work panel, with or
        // without the dock (the status Select too; checked 2026-11-15). It is a browser notice, not an exception.
        assert.deepEqual(opened.errors.filter(error => error !== "ResizeObserver loop completed with undelivered notifications."), []);
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
    console.log("PASS plan card browser: placement, padding, live updates, version order, done linger, step pictures, history line, group accordion, open to-dos line, Work panel goal filter and assignee, phone width (chromium, webkit)");
  }

  if (shotsDir) {
    mkdirSync(shotsDir, { recursive: true });
    for (const engineName of ["chromium", "webkit"]) {
      const fixture = await fixtureFor(engineName);
      for (const theme of ["light", "dark"]) {
        for (const [label, viewport] of [["desktop", { width: 1180, height: 820 }], ["phone", { width: 390, height: 844 }]]) {
          const { page, context } = await dmScenario(fixture, { viewport, theme, scale: 2 });
          await page.waitForFunction(() => [...document.querySelectorAll(".plan-step-pictures img")].every(image => image.complete), null, { timeout: 8000 });
          await page.waitForTimeout(600);
          await page.screenshot({ path: join(shotsDir, `dm-${label}-${theme}-${engineName}.png`) });
          // The dock alone, for the step pictures at their real size.
          await page.locator(".plan-dock").screenshot({ path: join(shotsDir, `dock-${label}-${theme}-${engineName}.png`) });
          await context.close();
          // History of an answered list: the step the answer never reached is skipped; the pictures stay.
          const answeredPlan = { run_id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", steps: steps([["Open the ledger", "done"], ["Check the totals", "done"], ["Email the team", "pending"]], { 1: STEP_IMAGES[1], 2: STEP_IMAGES[2] }), version: 5, open: false, closed: "answered", updated_at: at(786) };
          const past = await fixture.open({ route: dmRoute({ runs: () => [doneRun({ plan: answeredPlan })] }), viewport, theme, contextOptions: { deviceScaleFactor: 2 } });
          await past.page.locator(".plan-history-trigger").click();
          await past.page.locator('.plan-history-body li[data-state="skipped"]').waitFor({ timeout: 8000 });
          await past.page.waitForTimeout(500);
          await past.page.screenshot({ path: join(shotsDir, `history-${label}-${theme}-${engineName}.png`) });
          await past.context.close();
          const opened = await fixture.open({ path: "/g/g_team", messages: groupMessages, route: groupRoute, viewport, theme, contextOptions: { deviceScaleFactor: 2 } });
          await opened.page.locator(".plan-dock .plan-card").first().waitFor({ timeout: 8000 });
          await opened.page.locator(".plan-card-trigger").first().click();
          await opened.page.waitForTimeout(600);
          await opened.page.screenshot({ path: join(shotsDir, `group-${label}-${theme}-${engineName}.png`) });
          await opened.context.close();
          // The Work panel opened from the open to-dos line, filtered by goal, with a to-do's details open.
          const work = await fixture.open({ path: "/g/g_team", messages: groupMessages, route: groupWorkRoute([]), viewport, theme, contextOptions: { deviceScaleFactor: 2 } });
          await work.page.locator(".plan-dock-tasks").click();
          const panel = work.page.locator(".work-panel");
          await panel.locator(".work-row").first().waitFor({ timeout: 8000 });
          await panel.locator(".work-row", { hasText: "Update the FAQ" }).locator(".work-row-main > .disclosure > .disclosure-toggle").click();
          await work.page.waitForTimeout(600);
          await work.page.screenshot({ path: join(shotsDir, `work-${label}-${theme}-${engineName}.png`) });
          await work.context.close();
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
          const opened = await fixture.open({ theme, contextOptions: { deviceScaleFactor: 2 }, route: dmRoute({ runs: () => [doneRun({ status: "cancelled", plan: stoppedPlan })] }) });
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
