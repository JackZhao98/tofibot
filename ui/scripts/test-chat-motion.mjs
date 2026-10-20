// Message arrival and message actions in the browser build.
//  - a streamed message that is saved must not blink (opacity stays 1) and a message that arrives whole fades in once;
//  - the action bar is mounted only for the message under the pointer or focus, sits on that bubble's edge, and appears at once;
//  - Copy writes the message as displayed; reactions apply before the server answers and roll back on failure;
//  - the list is a single tab stop and arrows move between messages.
// Synthetic data; the app's /api is answered in the browser. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:chat-motion
// CHAT_MOTION_ENGINE=webkit runs the same checks in WebKit.
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engine = process.env.CHAT_MOTION_ENGINE === "webkit" ? pw.webkit : pw.chromium;
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: iso(0) },
  { id: "bot_b", name: "Simile Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_b", created_at: iso(0) }];
const group = { id: "grp_1", kind: "group", name: "Synthetic Group", bot_ids: ["bot_a", "bot_b"], updated_at: iso(0), read_seq: 99, user_visible: true };
const dm = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 99, user_visible: true };
const LONG = Array.from({ length: 6 }, (_, i) => `Synthetic ledger sentence number ${i + 1} describes the weekly entries and totals.`).join(" ");
const base = (id, seq, role, content, extra = {}) => ({ id, conversation_id: "dm_a", seq, role, content, created_at: iso(seq * 20), ...(role === "assistant" ? { sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: `run_${id}` } : {}), ...extra });
// A group: a person, one Bot's run of three, a speaker change to the other Bot, the person again, then a Bot.
const groupHistory = () => [
  base("g1", 1, "user", "[sender Owner id=user] Synthetic opening question."),
  base("g2", 2, "assistant", "First from Ledger."),
  base("g3", 3, "assistant", LONG),
  base("g4", 4, "assistant", "Third in the same run."),
  base("g5", 5, "assistant", "Simile answers now.", { sender_bot_id: "bot_b", sender_bot_name: "Simile Cat" }),
  base("g6", 6, "user", LONG),
  base("g7", 7, "assistant", "Last note.", { sender_bot_id: "bot_b", sender_bot_name: "Simile Cat" }),
].map(message => ({ ...message, conversation_id: "grp_1" }));
const history = () => [
  base("m1", 1, "user", "[sender Owner id=user] Synthetic opening question."),
  base("m2", 2, "assistant", "Done."),
  base("m3", 3, "assistant", `${LONG}\n\nSecond paragraph of the synthetic answer.`),
  base("m4", 4, "user", LONG),
  base("m5", 5, "assistant", "Final synthetic note."),
];

async function open({ width = 1180, height = 820, motion = "no-preference", touch = false, reactionDelay = 0, reactionFail = false, extra = [], runs = [], inGroup = false } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: motion, hasTouch: touch });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    // The clipboard is recorded, not used, so the check is the same in every engine and permission state.
    Object.defineProperty(navigator, "clipboard", { value: { writeText: async text => { window.__clip = text; } }, configurable: true });
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const messages = [...(inGroup ? groupHistory() : history()), ...extra];
  const reactionCalls = [];
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: [dm, group] });
    if (path.endsWith("/reactions") && request.method() === "PUT") {
      const body = request.postDataJSON();
      reactionCalls.push(body);
      await new Promise(resolve => setTimeout(resolve, reactionDelay));
      if (reactionFail) return route.fulfill({ status: 500, json: { error: "synthetic failure" } });
      const id = path.split("/").at(-2);
      return json({ conversation_id: "dm_a", message_id: id, reactions: body.present ? [{ emoji: body.emoji, actor_type: "user", created_at: new Date().toISOString() }] : [] });
    }
    if (path.endsWith("/messages")) return json({ messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/tools")) return json(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) return json({ occurrences: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(origin + (inGroup ? "/g/grp_1" : "/b/bot_a"));
  await page.locator(".message-block").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(600);
  return { page, context, errors, reactionCalls };
}

const results = [], failures = [];
const check = async (name, run) => {
  if (process.env.CHAT_MOTION_ONLY && !name.includes(process.env.CHAT_MOTION_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};
const block = (page, id) => page.locator(`[data-message-id="${id}"]`);
const bar = page => page.locator(".message-hover-actions");

/** Samples a message's content opacity on every frame while `action` runs. */
async function sampleOpacity(page, id, action, frames = 90) {
  await page.evaluate(({ id, frames }) => {
    window.__op = [];
    let n = 0;
    const tick = () => { const el = document.querySelector(`[data-message-id="${id}"] .message-content`); window.__op.push(el ? Number(getComputedStyle(el).opacity) : -1); if (++n < frames) requestAnimationFrame(tick); else window.__opDone = true; };
    window.__opDone = false; requestAnimationFrame(tick);
  }, { id, frames });
  await action();
  await page.waitForFunction(() => window.__opDone, null, { timeout: 5000 });
  return page.evaluate(() => window.__op);
}

await check("a streamed reply appears in place and keeps full opacity when saved", async () => {
  const { page, context, errors } = await open();
  const full = Array.from({ length: 8 }, (_, i) => `Synthetic streamed line ${i + 1} reads the entries.`).join(" ");
  const common = { conversation_id: "dm_a", run_id: "run_s", bot_id: "bot_a", message_id: "m_s", seq: 50, created_at: new Date().toISOString() };
  const mount = await sampleOpacity(page, "m_s", () => page.evaluate(({ common, full }) => window.__emit("delta", { ...common, text: full, content: full, revision: 1 }), { common, full }), 30);
  const mounted = mount.filter(v => v >= 0);
  assert.ok(mounted.length > 5 && Math.min(...mounted) === 1, `a streaming draft appears in place, without a container fade (min opacity ${Math.min(...mounted)})`);
  await page.waitForTimeout(700);
  const samples = await sampleOpacity(page, "m_s", () => page.evaluate(({ common, full }) => {
    window.__emit("message", { id: "m_s", conversation_id: "dm_a", seq: 50, role: "assistant", kind: "segment", sender_bot_id: "bot_a", run_id: "run_s", content: full, created_at: common.created_at });
  }, { common, full }));
  const seen = samples.filter(v => v >= 0);
  assert.ok(seen.length > 20, "the message stayed on screen through the swap");
  assert.ok(Math.min(...seen) === 1, `message content dipped to opacity ${Math.min(...seen)} when the stream was saved`);
  assert.deepEqual(errors, []);
  await context.close();
});

await check("a message that arrives whole fades in once and not on later renders", async () => {
  const { page, context } = await open();
  const created_at = new Date().toISOString();
  const samples = await sampleOpacity(page, "m_w", () => page.evaluate(created_at => {
    window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 60, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: "Synthetic whole message.", created_at });
  }, created_at), 40);
  const seen = samples.filter(v => v >= 0);
  assert.ok(seen.some(v => v < 0.99), "the arrival fades in from a lower opacity");
  assert.equal(seen.at(-1), 1, "and ends fully opaque");
  const old = await block(page, "m3").locator(".message-content").evaluate(el => ({ name: getComputedStyle(el).animationName }));
  assert.equal(old.name, "none", "history messages never animate");
  await context.close();
});

for (const nudge of [false, true]) await check(`the end of a streamed reply never moves the conversation down${nudge ? " nor slides the saved reply when the reader nudges the list" : ""}`, async () => {
  const at = new Date(Date.now() - 4000).toISOString();
  const run = { id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", status: "running", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m_u", queue_seq: 1, created_at: at, updated_at: at };
  const { page, context, errors } = await open({ extra: [base("m_u", 70, "user", "Synthetic request that starts a run.", { created_at: at })], runs: [run] });
  await page.locator(".task-run-live-line").waitFor();
  await page.evaluate(() => { const s = document.querySelector(".message-scroll"); s.scrollTop = s.scrollHeight; });
  const full = Array.from({ length: 6 }, (_, i) => `Synthetic reply line ${i + 1} reads the entries and totals.`).join("\n\n") + "\n\n- first item\n- last item";
  const common = { conversation_id: "dm_a", run_id: "run_live", bot_id: "bot_a", message_id: "m_live", seq: 71, created_at: new Date().toISOString() };
  await page.evaluate(() => {
    window.__ys = [];
    const tick = () => { const el = document.querySelector('[data-message-id="m_u"] .message-content'); const sc = document.querySelector(".message-scroll"); if (el && sc) window.__ys.push({ t: performance.now(), y: el.getBoundingClientRect().top, live: document.querySelectorAll(".task-run-live-line").length, tail: [...document.querySelectorAll(".typing-cursor")].length }); window.__ysRaf = requestAnimationFrame(tick); };
    tick();
    window.__flip = 0;
    const watch = () => { const el = document.querySelector('[data-message-id="m_live"] .message-content'); if (el) for (const a of el.getAnimations()) if ("transform" in (a.effect?.getKeyframes?.()[0] ?? {})) window.__flip++; window.__flipRaf = requestAnimationFrame(watch); };
    watch();
  });
  await page.evaluate(({ common, full }) => window.__emit("delta", { ...common, text: full, content: full, revision: 1 }), { common, full });
  await page.waitForTimeout(1500);
  await page.evaluate(({ common, full, nudge }) => {
    window.__mark = performance.now();
    if (nudge) document.querySelector(".message-scroll").scrollTop -= 30; // the reader nudges the list as the reply is saved: that is not the answer moving
    window.__emit("message", { id: "m_live", conversation_id: "dm_a", seq: 71, role: "assistant", sender_bot_id: "bot_a", run_id: "run_live", content: full, created_at: common.created_at });
    window.__emit("run", { conversation_id: "dm_a", run: { id: "run_live", conversation_id: "dm_a", bot_id: "bot_a", status: "done", updated_at: new Date().toISOString() } });
  }, { common, full, nudge });
  await page.waitForTimeout(1800);
  const ys = await page.evaluate(() => { cancelAnimationFrame(window.__ysRaf); cancelAnimationFrame(window.__flipRaf); return { ys: window.__ys, mark: window.__mark, flip: window.__flip }; });
  assert.equal(ys.flip, 0, "the saved reply is not slid by a stale layout offset");
  const after = ys.ys.filter(f => f.t >= ys.mark - 50);
  assert.ok(after.length > 20, "frames recorded across the stream end");
  let worst = 0;
  // With a deliberate nudge the first frames carry the reader's own 30px; only the reply's own motion is judged.
  for (let i = nudge ? 4 : 1; i < after.length; i++) worst = Math.max(worst, after[i].y - after[i - 1].y);
  assert.ok(worst <= 1, `the request bubble moved down by ${worst.toFixed(1)}px in one frame when the stream ended`);
  const during = ys.ys.filter(f => f.t < ys.mark);
  assert.ok(during.slice(2).every(f => f.live === 0), "the live status line steps aside once the reply has text");
  assert.deepEqual(errors, []);
  await context.close();
});

/** Fastest of seven timings, from an incoming message to the frame that shows it, with `count` messages (each with its own finished run) loaded. */
async function renderCost(count) {
  const extra = [], runs = [];
  for (let i = 0; i < count; i++) {
    const id = `p${i}`;
    extra.push(base(id, 100 + i * 2, "user", `Synthetic question ${i}`, { created_at: iso(5000 + i * 20) }), base(`${id}r`, 101 + i * 2, "assistant", `Synthetic answer ${i}`, { run_id: `run_${id}`, created_at: iso(5001 + i * 20) }));
    runs.push({ id: `run_${id}`, conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: id, queue_seq: 1, created_at: iso(5000 + i * 20), updated_at: iso(5001 + i * 20) });
  }
  const { page, context } = await open({ extra, runs });
  await page.waitForTimeout(800);
  const times = [];
  for (let n = 0; n < 7; n++) {
    times.push(await page.evaluate(async n => {
      const t0 = performance.now();
      window.__emit("message", { id: `cost${n}`, conversation_id: "dm_a", seq: 9000 + n, role: "assistant", sender_bot_id: "bot_a", run_id: `run_cost${n}`, content: `Synthetic probe ${n}`, created_at: new Date().toISOString() });
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      return performance.now() - t0;
    }, n));
    await page.waitForTimeout(150);
  }
  await context.close();
  return Math.min(...times);
}
await check("rendering cost does not grow faster than the loaded history", async () => {
  const small = await renderCost(60), large = await renderCost(360);
  console.log(`  render: ${Math.round(small)} ms at 60 exchanges, ${Math.round(large)} ms at 360 exchanges`);
  assert.ok(large / Math.max(small, 8) < 14, `6x the history cost ${(large / Math.max(small, 8)).toFixed(1)}x per render (small ${Math.round(small)} ms, large ${Math.round(large)} ms)`);
});

await check("reduced motion: whole arrival has no animation", async () => {
  const { page, context } = await open({ motion: "reduce" });
  await page.evaluate(created_at => window.__emit("message", { id: "m_r", conversation_id: "dm_a", seq: 61, role: "assistant", sender_bot_id: "bot_a", run_id: "run_r", content: "Synthetic.", created_at }), new Date().toISOString());
  await block(page, "m_r").waitFor();
  assert.equal(await block(page, "m_r").locator(".message-content").evaluate(el => getComputedStyle(el).animationName), "none");
  await context.close();
});

await check("action bars are not in the DOM until a message is hovered, and only that message has one", async () => {
  const { page, context } = await open();
  await page.mouse.move(2, 2);
  assert.equal(await bar(page).count(), 0, "no bars while idle");
  const box = await block(page, "m3").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 60, box.y + 20);
  await bar(page).first().waitFor();
  assert.equal(await bar(page).count(), 1, "one bar");
  assert.equal(await block(page, "m3").locator(".message-hover-actions").count(), 1, "it belongs to the hovered message");
  await page.mouse.move(2, 2);
  await page.waitForFunction(() => document.querySelectorAll(".message-hover-actions").length === 0);
  await context.close();
});

/** Reads the geometry that decides whether the bar collides with anything: every message, every avatar, the bar and its own bubble. */
const barGeometry = (page, id) => page.evaluate(id => {
  const rect = el => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
  const blockEl = document.querySelector(`[data-message-id="${id}"]`);
  const barEl = blockEl.querySelector(".message-hover-actions");
  if (!barEl) return null;
  const content = blockEl.querySelector(".message-content");
  const others = [...document.querySelectorAll(".message-block")].filter(el => el !== blockEl).flatMap(el => [...el.querySelectorAll(".message-content, .message-reactions")].map(rect));
  return { bar: rect(barEl), content: rect(content), others, avatars: [...document.querySelectorAll(".sender-avatar")].map(rect), vw: innerWidth, isUser: blockEl.querySelector(".message-user") !== null, position: getComputedStyle(barEl).position, background: getComputedStyle(barEl).backgroundColor, shadow: getComputedStyle(barEl).boxShadow, borderWidth: getComputedStyle(barEl).borderTopWidth };
}, id);
const intersects = (a, b) => Math.min(a.right, b.right) - Math.max(a.left, b.left) > 0.5 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 0.5;
const heights = page => page.evaluate(() => [...document.querySelectorAll(".message-block")].map(el => el.getBoundingClientRect().height));
const latencies = [];

for (const [layout, inGroup, ids] of [["DM", false, ["m1", "m2", "m3", "m4", "m5"]], ["group", true, ["g1", "g2", "g3", "g4", "g5", "g6", "g7"]]]) {
  await check(`${layout}: the bar sits below its own bubble, clear of every other message, with no layout shift`, async () => {
    const { page, context } = await open({ inGroup });
    for (const id of ids) {
      await block(page, id).evaluate(el => el.scrollIntoView({ block: "center" }));
      await page.waitForTimeout(60);
      const box = await block(page, id).locator(".message-content").boundingBox();
      await page.mouse.move(2, 2);
      await page.waitForFunction(() => document.querySelectorAll(".message-hover-actions").length === 0);
      const before = await heights(page);
      await page.evaluate(() => { window.__t0 = performance.now(); window.__vis = null; const tick = () => { const b = document.querySelector(".message-hover-actions"); if (b && Number(getComputedStyle(b).opacity) >= 0.99 && b.getBoundingClientRect().height > 0) { window.__vis = performance.now() - window.__t0; return; } if (performance.now() - window.__t0 < 1500) requestAnimationFrame(tick); }; requestAnimationFrame(tick); });
      await page.mouse.move(box.x + 30, box.y + 14);
      await page.waitForFunction(() => window.__vis != null, null, { timeout: 3000 });
      const latency = await page.evaluate(() => window.__vis);
      latencies.push(latency);
      assert.ok(latency <= 100, `${layout} ${id}: bar took ${Math.round(latency)} ms to become visible`);
      const after = await heights(page);
      assert.deepEqual(after, before, `${layout} ${id}: hovering changed a message height`);
      const g = await barGeometry(page, id);
      assert.ok(g.bar.top >= g.content.bottom - 0.5, `${layout} ${id}: bar top ${g.bar.top} is above its bubble's bottom ${g.content.bottom}`);
      assert.ok(g.bar.top - g.content.bottom <= 40, `${layout} ${id}: bar is ${g.bar.top - g.content.bottom}px below its bubble`);
      assert.ok(g.bar.left >= 0 && g.bar.right <= g.vw, `${layout} ${id}: bar stays inside the window`);
      for (const other of [...g.others, ...g.avatars]) assert.ok(!intersects(g.bar, other), `${layout} ${id}: bar covers another message or avatar`);
      // The bar belongs to the bubble's side: the reader's messages end at the bubble's right edge, a Bot's begin at its left.
      if (g.isUser) assert.ok(Math.abs(g.bar.right - g.content.right) <= 4, `${layout} ${id}: bar is not on the bubble's right edge`);
      else assert.ok(g.bar.left >= g.content.left - 8 && g.bar.left <= g.content.left + 8, `${layout} ${id}: bar is not on the bubble's left edge (${g.bar.left} vs ${g.content.left})`);
      // Quiet style: no container.
      assert.ok(g.background === "rgba(0, 0, 0, 0)" && g.shadow === "none" && g.borderWidth === "0px", `${layout} ${id}: the bar draws a container`);
      // Pointer path bubble -> bar crosses no dead space.
      const bb = await block(page, id).locator(".message-hover-actions").boundingBox();
      const fromY = box.y + box.height - 2, toY = bb.y + bb.height / 2;
      for (let step = 1; step <= 8; step++) {
        await page.mouse.move(box.x + 30 + (bb.x + 10 - box.x - 30) * step / 8, fromY + (toY - fromY) * step / 8);
        assert.equal(await block(page, id).locator(".message-hover-actions").count(), 1, `${layout} ${id}: bar closed on the way from the bubble to the bar (step ${step})`);
      }
      assert.equal(await bar(page).count(), 1, `${layout} ${id}: exactly one bar`);
      if (process.env.CHAT_MOTION_SHOT) await page.screenshot({ path: `${process.env.CHAT_MOTION_SHOT}-${layout}-${id}.png` });
    }
    await context.close();
  });
}

await check("the gap between messages holds the bar: same sender and speaker change, and the bar's row is reserved", async () => {
  for (const [layout, inGroup, run, change] of [["DM", false, ["m2", "m3"], ["m3", "m4"]], ["group", true, ["g2", "g3"], ["g4", "g5"]]]) {
    const { page, context } = await open({ inGroup });
    const gap = async (a, b) => page.evaluate(([a, b]) => document.querySelector(`[data-message-id="${b}"] .message`).getBoundingClientRect().top - document.querySelector(`[data-message-id="${a}"] .message`).getBoundingClientRect().bottom, [a, b]);
    const sameSender = await gap(...run), speaker = await gap(...change);
    console.log(`  ${layout}: gap between consecutive messages of one sender ${sameSender}px, at a speaker change ${speaker}px`);
    assert.ok(sameSender >= 32, `${layout}: the gap inside a sender's run is ${sameSender}px, too tight for the bar`);
    assert.ok(speaker > sameSender, `${layout}: a speaker change (${speaker}px) is not wider than a run (${sameSender}px)`);
    await context.close();
  }
});

await check("hover-to-visible latency is reported", async () => {
  assert.ok(latencies.length > 0);
  const sorted = [...latencies].sort((x, y) => x - y);
  console.log(`  bar hover-to-visible: median ${sorted[Math.floor(sorted.length / 2)].toFixed(1)} ms, max ${sorted.at(-1).toFixed(1)} ms over ${sorted.length} hovers`);
});

await check("icon hints: the first waits, a neighbour's follows at once", async () => {
  const { page, context } = await open();
  await block(page, "m3").scrollIntoViewIfNeeded();
  const box = await block(page, "m3").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 30, box.y + 10);
  const buttons = block(page, "m3").locator(".message-hover-actions button");
  await buttons.first().waitFor();
  const timeToHint = async button => {
    const b = await button.boundingBox();
    await page.evaluate(() => { window.__h0 = performance.now(); window.__h = null; const old = document.querySelector(".action-hint"); const oldText = old?.textContent; const tick = () => { const now = document.querySelector(".action-hint"); if (now && (now !== old || now.textContent !== oldText)) { window.__h = performance.now() - window.__h0; return; } if (performance.now() - window.__h0 < 2000) requestAnimationFrame(tick); }; requestAnimationFrame(tick); });
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
    await page.waitForFunction(() => window.__h != null || performance.now() - window.__h0 > 1900, null, { timeout: 4000 });
    return page.evaluate(() => window.__h);
  };
  const first = await timeToHint(buttons.nth(0));
  assert.ok(first >= 300 && first < 900, `the first hint waits (${Math.round(first)} ms)`);
  const next = await timeToHint(buttons.nth(1));
  assert.ok(next !== null && next < 120, `the neighbour's hint took ${next === null ? "never" : Math.round(next) + " ms"}`);
  await context.close();
});

await check("Copy puts the message as displayed on the clipboard and shows a check", async () => {
  const { page, context } = await open();
  await block(page, "m1").scrollIntoViewIfNeeded();
  const box = await block(page, "m1").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 20, box.y + 10);
  await page.waitForTimeout(200);
  if (process.env.CHAT_MOTION_SHOT) await page.screenshot({ path: process.env.CHAT_MOTION_SHOT });
  await block(page, "m1").locator('[data-action="copy"]').click({ timeout: 4000 });
  assert.equal(await page.evaluate(() => window.__clip), "Synthetic opening question.", "sender label is stripped");
  assert.equal(await block(page, "m1").locator(".copy-feedback.is-copied").count(), 1, "check state shown");
  await context.close();
});

await check("Copy falls back when the async clipboard is unavailable", async () => {
  const { page, context } = await open();
  await page.evaluate(() => { Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true }); window.__copied = null; document.execCommand = cmd => { if (cmd === "copy") { window.__copied = document.activeElement?.value ?? document.getSelection().toString(); return true; } return false; }; });
  await block(page, "m2").scrollIntoViewIfNeeded();
  const box = await block(page, "m2").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 10, box.y + 10);
  await block(page, "m2").locator('[data-action="copy"]').click({ timeout: 4000 });
  assert.equal(await page.evaluate(() => window.__copied), "Done.");
  await context.close();
});

await check("a reaction shows at once and the menu closes before the server answers", async () => {
  const { page, context, reactionCalls } = await open({ reactionDelay: 1500 });
  const box = await block(page, "m3").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 30, box.y + 10);
  await block(page, "m3").locator('[data-action="react"]').click();
  await page.locator(".reaction-context-menu").waitFor();
  await page.locator('.reaction-options button[aria-label*="👍"]').click();
  await page.waitForTimeout(150);
  assert.equal(await page.locator(".reaction-context-menu").count(), 0, "menu closed while the request is in flight");
  assert.equal(await block(page, "m3").locator(".reaction-chip").count(), 1, "chip is on screen before the response");
  assert.equal(reactionCalls.length, 1);
  await page.waitForTimeout(1800);
  assert.equal(await block(page, "m3").locator(".reaction-chip").count(), 1, "chip stays after the response");
  await context.close();
});

await check("a failed reaction rolls back and says so", async () => {
  const { page, context } = await open({ reactionFail: true });
  const box = await block(page, "m3").locator(".message-content").boundingBox();
  await page.mouse.move(box.x + 30, box.y + 10);
  await block(page, "m3").locator('[data-action="react"]').click();
  await page.locator('.reaction-options button[aria-label*="👍"]').click();
  await page.waitForFunction(() => document.querySelector('[data-message-id="m3"] .reaction-error'), null, { timeout: 10000 });
  assert.equal(await block(page, "m3").locator(".reaction-chip").count(), 0, "the optimistic chip is removed");
  await context.close();
});

await check("keyboard: the list is one tab stop, arrows move between messages, the bar is one more stop", async () => {
  const { page, context } = await open();
  const tabbable = await page.evaluate(() => [...document.querySelectorAll(".message-list .message-block")].filter(b => b.tabIndex >= 0).map(b => b.dataset.messageId));
  assert.deepEqual(tabbable, ["m5"], "only the newest message is a tab stop");
  await block(page, "m5").focus();
  assert.equal(await bar(page).count(), 1, "focus mounts the bar");
  await page.keyboard.press("ArrowUp");
  assert.equal(await page.evaluate(() => document.activeElement?.dataset.messageId), "m4");
  assert.equal(await block(page, "m4").locator(".message-hover-actions").count(), 1, "the bar follows focus");
  assert.equal(await block(page, "m5").locator(".message-hover-actions").count(), 0);
  await page.keyboard.press("Tab");
  assert.ok(await page.evaluate(() => document.activeElement?.closest(".message-hover-actions") !== null), "Tab enters the bar");
  const stops = await page.evaluate(() => [...document.querySelectorAll(".message-hover-actions button")].map(b => b.tabIndex));
  assert.deepEqual(stops.filter(v => v === 0).length, 1, "the bar has one tab stop");
  await page.keyboard.press("ArrowRight");
  assert.ok(await page.evaluate(() => document.activeElement?.tabIndex === 0 && document.activeElement.closest(".message-hover-actions") !== null), "arrows move inside the bar");
  await page.keyboard.press("Escape");
  assert.equal(await page.evaluate(() => document.activeElement?.dataset.messageId), "m4", "Escape returns to the message");
  await context.close();
});

await check("touch: tapping beside the bubble does nothing; tapping the bubble opens the bar below it", async () => {
  const { page, context } = await open({ width: 420, height: 800, touch: true });
  await block(page, "m4").scrollIntoViewIfNeeded();
  const content = await block(page, "m4").locator(".message-content").boundingBox();
  const host = await block(page, "m4").locator(".message-hover-host").boundingBox();
  assert.ok(content.x - host.x > 8, "the reader's bubble leaves empty row space on its left");
  await page.touchscreen.tap(host.x + 3, content.y + content.height / 2);
  await page.waitForTimeout(150);
  assert.equal(await bar(page).count(), 0, "a tap beside the bubble opens nothing");
  await page.touchscreen.tap(content.x + 30, content.y + 10);
  await bar(page).first().waitFor();
  const geometry = await page.evaluate(() => { const b = document.querySelector(".message-hover-actions"); const blockEl = b.closest(".message-block"); const c = blockEl.querySelector(".message-content").getBoundingClientRect(); const br = b.getBoundingClientRect(), btn = b.querySelector("button").getBoundingClientRect(); const next = blockEl.nextElementSibling?.getBoundingClientRect(); return { static: getComputedStyle(b).position === "static", size: Math.min(btn.width, btn.height), belowBubble: br.top >= c.bottom - 0.5, rightEdge: Math.abs(br.right - c.right) <= 4, overlapsNext: next ? br.bottom > next.top + 1 && br.top < next.bottom : false, inside: br.left >= 0 && br.right <= innerWidth }; });
  assert.ok(geometry.static, "bar is in the flow");
  assert.ok(geometry.belowBubble, "bar is below its bubble");
  assert.ok(geometry.rightEdge, "the reader's bar ends at the bubble's right edge");
  assert.ok(geometry.inside, "bar stays inside the window");
  assert.ok(!geometry.overlapsNext, "bar does not cover the next message");
  assert.ok(geometry.size >= 44, `touch targets are ${geometry.size}px`);
  // A press on the bar itself keeps it open (the bar sits outside the bubble's host).
  const time = await bar(page).locator("time").boundingBox();
  await page.touchscreen.tap(time.x + time.width / 2, time.y + time.height / 2);
  await page.waitForTimeout(150);
  assert.equal(await bar(page).count(), 1, "a tap on the bar does not close it");
  await page.touchscreen.tap(5, 300);
  await page.waitForTimeout(150);
  assert.equal(await bar(page).count(), 0, "a tap elsewhere closes it");
  await context.close();
});

await browser.close();
await server.close();
if (failures.length) { console.log(`chat motion checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`chat motion checks: PASS (${results.length} scenarios)`);
