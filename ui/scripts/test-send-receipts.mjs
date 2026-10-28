// Optimistic send and receipts: the bubble lands on Enter, reconciles by client_message_id whichever of the
// POST answer and the stream copy comes first, and carries sending / delivered / read / failed marks.
// A real HTTP/SSE mock runs inside the vite dev server. All data is synthetic.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:send-receipts
// RECEIPTS_ENGINE=webkit runs the same checks in WebKit. RECEIPTS_UI_ROOT points at another checkout's ui/ (to prove failure on old code).
// RECEIPTS_ONLY=<substring> runs matching checks only.
import assert from "node:assert/strict";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build, createServer, preview } from "vite";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = process.env.RECEIPTS_UI_ROOT ? resolve(process.env.RECEIPTS_UI_ROOT) : dirname(dirname(fileURLToPath(import.meta.url)));
const INSTANCE = "00000000-0000-4000-8000-000000000002";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["a", "b"].map((k, i) => ({ id: `bot_${k}`, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${k}`, created_at: iso(0) }));
const convs = [
  ...["a", "b"].map((k, i) => ({ id: `dm_${k}`, kind: "dm", name: bots[i].name, bot_id: `bot_${k}`, bot_ids: [`bot_${k}`], updated_at: iso(0), read_seq: 4, user_visible: true })),
  { id: "grp", kind: "group", name: "Ops Room", bot_ids: ["bot_a", "bot_b"], updated_at: iso(0), read_seq: 1, user_visible: true },
];
const msg = (conv, seq, role, content, extra = {}) => ({ id: `${conv}_m${seq}`, conversation_id: conv, seq, role, content, created_at: iso(seq * 30), ...extra });
const run = (conv, id, bot, status, trigger, extra = {}) => ({ id, conversation_id: conv, bot_id: bot, status, model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: trigger, queue_seq: 1, created_at: iso(10), updated_at: iso(11), ...extra });
const seed = () => ({
  store: {
    dm_a: [msg("dm_a", 1, "user", "old question", { client_message_id: "c-old" }), msg("dm_a", 2, "assistant", "old answer"), msg("dm_a", 3, "user", "unanswered note", { client_message_id: "c-note" }), msg("dm_a", 4, "user", "being worked on", { client_message_id: "c-work" })],
    dm_b: [msg("dm_b", 1, "assistant", "hello from b")],
    grp: [msg("grp", 1, "assistant", "group opener", { sender_bot_id: "bot_a" })],
  },
  runs: [run("dm_a", "run_old", "bot_a", "done", "dm_a_m1"), run("dm_a", "run_work", "bot_a", "running", "dm_a_m4")],
});

const mock = { sse: new Set(), eventId: 100, posts: [], sent: new Map(), n: 0, sendDelayMs: 0, readDelayMs: 0, failSends: false, ssePreempt: false, ...seed() };
const sleep = ms => new Promise(r => setTimeout(r, ms));
const body = req => new Promise(done => { const c = []; req.on("data", d => c.push(d)); req.on("end", () => done(Buffer.concat(c).toString() || "null")); });
function push(conv, type, data) { const frame = `id: ${++mock.eventId}\nevent: ${type}\ndata: ${JSON.stringify(data)}\n\n`; for (const res of mock.sse) if (res.__conv === conv) res.write(frame); }

const mockPlugin = {
  name: "receipts-mock",
  configureServer(server) { install(server.middlewares); },
  configurePreviewServer(server) { install(server.middlewares); },
};
function install(middlewares) {
    middlewares.use(async (req, res, next) => {
      const url = new URL(req.url, "http://x");
      const path = url.pathname;
      if (!path.startsWith("/api/") && path !== "/health") return next();
      const json = (value, status = 200) => { res.writeHead(status, { "content-type": "application/json" }); res.end(JSON.stringify(value)); };
      if (path.endsWith("/events") || path === "/api/workspace/events") {
        res.__conv = path.split("/")[3] ?? "";
        const timer = setInterval(() => res.write("event: ping\ndata: {}\n\n"), 3000);
        req.on("close", () => { clearInterval(timer); mock.sse.delete(res); });
        res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
        res.write(": open\n\n");
        mock.sse.add(res);
        return;
      }
      const conv = path.split("/")[3] ?? "";
      if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
      if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
      if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
      if (path === "/api/bots") return json({ bots });
      if (path === "/api/conversations") return json({ conversations: convs });
      if (path.endsWith("/messages") && req.method === "POST") {
        const payload = JSON.parse(await body(req));
        mock.posts.push({ conv, cmid: payload.client_message_id });
        if (mock.sendDelayMs) await sleep(mock.sendDelayMs);
        if (mock.failSends) return req.socket.destroy();
        const key = `${conv}:${payload.client_message_id}`;
        const duplicate = mock.sent.get(key);
        if (duplicate) return json(duplicate, 200);
        const list = mock.store[conv];
        const message = msg(conv, list.length + 1, "user", payload.content, { id: `${conv}_sent${++mock.n}`, client_message_id: payload.client_message_id, created_at: new Date().toISOString() });
        list.push(message);
        const targets = conv === "grp" ? ["bot_a", "bot_b"] : [conv === "dm_a" ? "bot_a" : "bot_b"];
        const runs = targets.map((bot, i) => run(conv, `run_${mock.n}_${bot}`, bot, "queued", message.id, { queue_seq: i + 1 }));
        mock.runs.push(...runs);
        const answer = { message, run: runs[0], runs };
        mock.sent.set(key, answer);
        if (mock.ssePreempt) { push(conv, "message", message); await sleep(500); }
        return json(answer, 202);
      }
      if (path.endsWith("/messages")) { if (mock.readDelayMs) await sleep(mock.readDelayMs); } 
      if (path.endsWith("/messages")) return json({ messages: mock.store[conv] ?? [], drafts: [], has_more: false, event_cursor: mock.eventId });
      if (path.endsWith("/runs")) return json({ runs: mock.runs.filter(r => r.conversation_id === conv) });
      if (path.endsWith("/memories")) return json({ memories: [] });
      if (path.endsWith("/tools")) return json(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
      if (path.endsWith("/schedule-occurrences")) return json({ occurrences: [] });
      if (path.endsWith("/schedules")) return json({ schedules: [] });
      if (path === "/api/computers/firecracker/info") return json({ kind: "firecracker", state: "ready" });
      if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
      if (path === "/api/questions") return json({ questions: [] });
      if (path === "/api/mail-drafts") return json({ drafts: [] });
      return json({});
    });
}

const server = await createServer({ configFile: false, root: ui, plugins: [react(), mockPlugin], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const engine = process.env.RECEIPTS_ENGINE === "webkit" ? pw.webkit : pw.chromium;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const VIEWS = { desktop: { width: 1180, height: 820 }, phone: { width: 390, height: 780, isMobile: true, hasTouch: true } };
async function open({ view = "desktop", reduced = false, conv = "Ledger Cat", base = origin } = {}) {
  Object.assign(mock, { sse: new Set(), posts: [], sent: new Map(), n: 0, sendDelayMs: 0, readDelayMs: 0, failSends: false, ssePreempt: false, ...seed() });
  const { width, height, ...touch } = VIEWS[view];
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", ...touch });
  await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch {} });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(base + "/");
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, conv);
  return { page, context, errors };
}
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
async function openConversation(page, name) {
  const row = rowFor(page, name);
  if (!(await row.first().isVisible())) await page.locator(".mobile-back, [aria-label*='conversations' i]").first().click().catch(() => {});
  // A finger, not a mouse, on a phone: a mouse click would leave a hovering pointer over the list that opens (and a phone has none).
  if (page.viewportSize().width < 500) await row.first().tap(); else await row.first().click();
  await page.locator(".composer textarea").waitFor();
  await page.locator(".message-list .message-block").first().waitFor();
}
const block = (page, text) => page.locator(".message-list .message-block").filter({ has: page.locator(".message-user") }).filter({ hasText: text });
// Delivered and read are quiet: they are a mark at the end of the hover/focus/tap action row, so reading them means asking for the row
// (pointer on the bubble, or a tap on touch). Sending (dimmed bubble) and failed (the retry line) are always on show.
const touchView = page => page.viewportSize().width < 500;
async function revealMark(page, text) {
  const mark = block(page, text).locator(".send-receipt-mark");
  if (await mark.count()) return mark.first();
  const bubble = block(page, text).locator(".message-content").first();
  if (touchView(page)) await bubble.tap({ timeout: 2000 }).catch(() => {}); else await bubble.hover({ timeout: 2000 }).catch(() => {});
  await mark.first().waitFor({ timeout: 700 }).catch(() => {});
  return (await mark.count()) ? mark.first() : null;
}
async function stateOf(page, text) {
  const b = block(page, text);
  if (await b.locator('.send-receipt[data-state="failed"]').count()) return "failed";
  if (await b.locator(".message-user.is-sending").count()) return "sending";
  const mark = await revealMark(page, text);
  return mark ? mark.getAttribute("data-state") : null;
}
async function waitState(page, text, state, ms = 4000) {
  const end = Date.now() + ms; let seen;
  while (Date.now() < end) { seen = await stateOf(page, text); if (seen === state) return; await sleep(40); }
  throw new Error(`"${text}" never reached ${state} (last: ${seen})`);
}
async function type(page, text) { await page.locator(".composer textarea").fill(text); await page.keyboard.press("Enter"); }
const geometry = (page, text) => block(page, text).evaluate(node => { const r = node.querySelector(".message-user").getBoundingClientRect(); const bubble = node.querySelector(".message-content").getBoundingClientRect(); const row = node.querySelector(".send-receipt")?.getBoundingClientRect(); return { height: r.height, bubbleH: bubble.height, bubbleW: bubble.width, rowH: row?.height ?? 0 }; });

const shot = (page, name) => process.env.RECEIPTS_SHOTS ? page.screenshot({ path: `${process.env.RECEIPTS_SHOTS}/${name}.png` }) : undefined;
const results = [], failures = [];
const check = async (name, fn) => {
  if (process.env.RECEIPTS_ONLY && !name.includes(process.env.RECEIPTS_ONLY)) return;
  try { await fn(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.stack ?? error.message).split("\n").slice(0, 4).join("\n    ")}`); }
};

// Timing is judged on the production bundle: the dev server's JSX instrumentation alone costs more than the budget.
let production;
async function productionBase() {
  if (production) return production.base;
  const outDir = mkdtempSync(resolve(tmpdir(), "tofi-receipts-"));
  // The dev server above pinned NODE_ENV to development for this process; the bundle must be a real production one.
  const nodeEnv = process.env.NODE_ENV;
  process.env.NODE_ENV = "production";
  try { await build({ configFile: false, mode: "production", root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" }); }
  finally { if (nodeEnv === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = nodeEnv; }
  const served = await preview({ configFile: false, root: ui, plugins: [mockPlugin], build: { outDir }, preview: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
  production = { served, outDir, base: `http://127.0.0.1:${served.httpServer.address().port}` };
  return production.base;
}
// The dev server variant runs here; the production one runs last (see the end): building for production in this process
// leaves the dev server serving React's production JSX runtime ("_jsxDEV is not a function") for every later test.
const timingCheck = mode => check(`${mode}: the bubble is on screen within 50 ms of Enter, with the composer cleared, while the server takes 1 s`, async () => {
  const { page, context, errors } = await open({ base: mode === "production" ? await productionBase() : origin });
  mock.sendDelayMs = 1000;
  const timings = [], frames = [];
  for (const text of ["instant probe one", "instant probe two", "instant probe three"]) {
    await page.evaluate(text => {
      window.__probe = {};
      const onKey = event => { if (event.key === "Enter" && window.__probe.key === undefined) window.__probe.key = performance.now(); };
      document.addEventListener("keydown", onKey, true);
      const watch = new MutationObserver(() => {
        const node = [...document.querySelectorAll(".message-list .message-user .message-content")].find(item => item.textContent.includes(text));
        if (!node || window.__probe.seen !== undefined) return;
        window.__probe.seen = performance.now();
        window.__probe.composerEmpty = document.querySelector(".composer textarea").value === "";
        window.__probe.node = node;
        // The frame that carries the bubble to the screen: the next animation frame callback, then the task after it.
        requestAnimationFrame(() => { window.__probe.frame = performance.now(); });
        watch.disconnect(); document.removeEventListener("keydown", onKey, true);
      });
      watch.observe(document.body, { subtree: true, childList: true, characterData: true });
    }, text);
    await type(page, text);
    await page.waitForFunction(() => window.__probe.seen !== undefined);
    await page.waitForFunction(() => window.__probe.frame !== undefined);
    const probe = await page.evaluate(() => ({ key: window.__probe.key, seen: window.__probe.seen, frame: window.__probe.frame, composerEmpty: window.__probe.composerEmpty }));
    assert.ok(probe.composerEmpty, "composer cleared in the same frame as the bubble");
    const shown = await page.evaluate(() => Boolean(document.querySelector(".v2-send-flight-ghost")) || getComputedStyle(window.__probe.node).visibility === "visible");
    assert.ok(shown, "bubble (or its flight) visible");
    timings.push(probe.seen - probe.key); frames.push(probe.frame - probe.key);
    assert.equal(await stateOf(page, text), "sending", "still in flight");
    await waitState(page, text, "delivered", 4000);
  }
  console.log(`    Enter to bubble in the DOM: ${timings.map(ms => ms.toFixed(1)).join(" / ")} ms, to the frame that paints it: ${frames.map(ms => ms.toFixed(1)).join(" / ")} ms (server latency 1000 ms)`);
  // The dev server renders every element through React's instrumented JSX; its number is reported, the budget binds the shipped bundle.
  if (mode === "production") assert.ok(Math.max(...frames) < 50, `slowest ${Math.max(...frames).toFixed(1)} ms`);
  assert.deepEqual(errors, []);
  await context.close();
});
await timingCheck("dev server");

for (const view of ["desktop", "phone"]) {
  await check(`${view}: sending, delivered, read, in place, with one bubble all the way`, async () => {
    const { page, context } = await open({ view });
    mock.sendDelayMs = 700;
    const text = `state walk ${view}`;
    await type(page, text);
    assert.equal(await stateOf(page, text), "sending");
    assert.ok(await block(page, text).locator(".message-user.is-sending").count(), "bubble dimmed while in flight");
    const sending = await geometry(page, text);
    await waitState(page, text, "delivered");
    assert.equal(await block(page, text).locator(".message-user.is-sending").count(), 0);
    const delivered = await geometry(page, text);
    assert.equal(await block(page, text).count(), 1, "no duplicate after the POST answered");
    // A queued run is not a read: the Bot has not started.
    await sleep(300);
    assert.equal(await stateOf(page, text), "delivered");
    const queued = mock.runs.find(r => r.status === "queued" && r.conversation_id === "dm_a");
    push("dm_a", "run", { ...queued, status: "running", updated_at: iso(50) });
    await waitState(page, text, "read");
    const read = await geometry(page, text);
    for (const key of ["height", "bubbleH", "bubbleW", "rowH"]) {
      assert.ok(Math.abs(sending[key] - delivered[key]) < 0.5 && Math.abs(delivered[key] - read[key]) < 0.5, `${key} shifted: ${sending[key]} -> ${delivered[key]} -> ${read[key]}`);
    }
    const color = await (await revealMark(page, text)).evaluate(node => getComputedStyle(node).color);
    const quiet = await block(page, "old answer").count();
    assert.notEqual(color, "", "accent colour resolved");
    void quiet;
    assert.equal(await block(page, text).count(), 1);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    assert.ok(overflow <= 0, `page scrolls sideways by ${overflow}px`);
    await context.close();
  });
}

await check("desktop: delivered and read are not on show and own no row; hover puts the mark at the end of the action row without moving anything", async () => {
  const { page, context } = await open();
  await page.mouse.move(5, 5);
  // Settled history: every message of the person's, with the pointer elsewhere, carries no mark and no receipt row.
  assert.equal(await page.locator(".send-receipt-mark, .send-receipt").count(), 0, "a receipt is on show without hover");
  const quietHeights = await page.locator(".message-list .message-block").evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height));
  const listBefore = await page.evaluate(() => document.querySelector(".message-scroll").scrollHeight);
  // Spacing is what the action-row design gives: the bubble's block is the bubble, the row's reserved 28 px and its margin.
  const spacing = await block(page, "old question").evaluate(node => { const article = node.querySelector(".message-user").getBoundingClientRect(); const row = node.querySelector(".message-actions-row")?.getBoundingClientRect(); return { rowH: row ? row.height : 0, gap: row ? row.top - article.bottom : 0 }; });
  assert.ok(spacing.rowH >= 28 && spacing.gap <= 6, `the action row's reserve sits right under the bubble (${JSON.stringify(spacing)})`);
  const bubble = block(page, "old question").locator(".message-content").first();
  await bubble.hover();
  const mark = block(page, "old question").locator(".send-receipt-mark");
  await mark.waitFor({ timeout: 2000 });
  const placed = await block(page, "old question").evaluate(node => { const row = node.querySelector(".message-actions-row").getBoundingClientRect(); const mk = node.querySelector(".send-receipt-mark").getBoundingClientRect(); const time = node.querySelector(".message-hover-actions time").getBoundingClientRect(); const last = node.querySelector(".message-hover-actions").lastElementChild; return { inRow: mk.top >= row.top - 0.5 && mk.bottom <= row.bottom + 0.5, afterTime: mk.left >= time.right - 0.5, lastChild: last.classList.contains("send-receipt-mark"), sameLine: Math.abs((mk.top + mk.bottom) / 2 - (time.top + time.bottom) / 2) <= 4 }; });
  assert.deepEqual(placed, { inRow: true, afterTime: true, lastChild: true, sameLine: true }, "the mark sits at the end of the action row, next to the time");
  const afterHeights = await page.locator(".message-list .message-block").evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height));
  assert.deepEqual(afterHeights, quietHeights, "hovering moved something");
  assert.equal(await page.evaluate(() => document.querySelector(".message-scroll").scrollHeight), listBefore, "hovering changed the page height");
  assert.equal(await page.locator(".send-receipt").count(), 0, "a receipt row exists");
  await shot(page, "receipt-hover");
  await page.mouse.move(5, 5);
  await sleep(200);
  assert.equal(await page.locator(".send-receipt-mark").count(), 0, "the mark goes with the row when the pointer leaves");
  await context.close();
});

await check("phone: a tap on the bubble brings the row, and the mark with it", async () => {
  const { page, context } = await open({ view: "phone" });
  assert.equal(await page.locator(".send-receipt-mark, .send-receipt").count(), 0, "a receipt is on show without a tap");
  await block(page, "old question").locator(".message-content").first().tap();
  await block(page, "old question").locator(".send-receipt-mark").waitFor({ timeout: 2000 });
  assert.equal(await block(page, "old question").locator(".send-receipt-mark").getAttribute("data-state"), "read");
  await shot(page, "receipt-tap");
  await context.close();
});

await check("sending and failed stay visible without hover, and the failed line is the only receipt row", async () => {
  const { page, context } = await open();
  mock.sendDelayMs = 900;
  await type(page, "dimmed in flight");
  await page.mouse.move(5, 5);
  assert.ok(await block(page, "dimmed in flight").locator(".message-user.is-sending").count(), "a message in flight is dimmed");
  assert.equal(await page.locator(".send-receipt-mark, .send-receipt").count(), 0, "no mark and no row while sending");
  await waitState(page, "dimmed in flight", "delivered");
  mock.sendDelayMs = 0;
  mock.failSends = true;
  await type(page, "shows failure");
  await page.mouse.move(5, 5);
  await block(page, "shows failure").locator('.send-receipt[data-state="failed"] .send-receipt-retry').waitFor({ timeout: 5000 });
  assert.equal(await page.locator(".send-receipt").count(), 1, "exactly one receipt row: the failed one");
  await context.close();
});

await check("no duplicate when the stream copy arrives before the POST answer", async () => {
  const { page, context } = await open();
  mock.ssePreempt = true;
  await type(page, "stream first");
  await waitState(page, "stream first", "delivered", 450);
  assert.equal(await block(page, "stream first").count(), 1, "one bubble while the POST is still pending");
  assert.equal(await block(page, "stream first").locator(".message-user.is-sending").count(), 0, "the server copy already replaced the optimistic one");
  await sleep(900);
  assert.equal(await block(page, "stream first").count(), 1, "one bubble after the POST answered");
  assert.equal(mock.store.dm_a.filter(m => m.content === "stream first").length, 1);
  await context.close();
});

await check("no duplicate when the stream copy arrives after the POST answer", async () => {
  const { page, context } = await open();
  await type(page, "post first");
  await waitState(page, "post first", "delivered");
  push("dm_a", "message", mock.store.dm_a.find(m => m.content === "post first"));
  await sleep(500);
  assert.equal(await block(page, "post first").count(), 1);
  await context.close();
});

for (const view of ["desktop", "phone"]) {
  await check(`${view}: a failed send stays as Not sent, retries under the same id, and is delivered once`, async () => {
    const { page, context } = await open({ view });
    mock.failSends = true;
    // Count what the page sends, not what the server hears: the browser's network stack may replay a reset POST by itself.
    let outgoing = 0;
    page.on("request", request => { if (request.method() === "POST" && request.url().endsWith("/messages")) outgoing++; });
    const text = `will fail ${view}`;
    await type(page, text);
    const retry = block(page, text).locator(".send-receipt-retry");
    await retry.waitFor({ timeout: 5000 });
    assert.match(await retry.innerText(), /^Not sent · Retry$/);
    assert.equal(await page.locator(".composer textarea").inputValue(), "");
    const box = await retry.boundingBox();
    assert.ok(box && box.x >= 0 && box.x + box.width <= VIEWS[view].width, "the affordance fits the width");
    // Reconnecting sends it again once by itself (composer resilience: the line says "Sending again"); if that fails too it stays for the person.
    await page.evaluate(() => window.dispatchEvent(new Event("online")));
    const until = Date.now() + 4000;
    while (outgoing < 2 && Date.now() < until) await sleep(40);
    assert.equal(outgoing, 2, "one automatic try when the connection came back");
    await block(page, text).locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 4000 });
    await page.evaluate(() => window.dispatchEvent(new Event("online")));
    await sleep(1200);
    assert.equal(outgoing, 2, "no second automatic try");
    assert.equal(await stateOf(page, text), "failed");
    mock.failSends = false;
    await shot(page, `failed-${view}`);
    await retry.click();
    await waitState(page, text, "delivered");
    assert.equal(outgoing, 3, "one deliberate retry");
    assert.equal(new Set(mock.posts.map(post => post.cmid)).size, 1, "every attempt carried the same client_message_id");
    assert.equal(await block(page, text).count(), 1);
    assert.equal(mock.store.dm_a.filter(m => m.content === text).length, 1, "stored once");
    assert.equal(mock.runs.filter(r => r.status === "queued" && r.conversation_id === "dm_a").length, 1, "one run");
    await context.close();
  });
}

await check("a failed send survives switching conversations and back", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "stays failed");
  await block(page, "stays failed").locator(".send-receipt-retry").waitFor();
  await openConversation(page, "Simile Cat");
  assert.equal(await block(page, "stays failed").count(), 0, "not shown in the other conversation");
  await openConversation(page, "Ledger Cat");
  assert.equal(await stateOf(page, "stays failed"), "failed");
  mock.failSends = false;
  await block(page, "stays failed").locator(".send-receipt-retry").click();
  await waitState(page, "stays failed", "delivered");
  await context.close();
});

await check("a send that finishes after switching away is final when the reader returns", async () => {
  const { page, context } = await open();
  mock.sendDelayMs = 600;
  await type(page, "left while sending");
  await openConversation(page, "Simile Cat");
  await sleep(1000);
  // The server's refresh is held back, so what shows on return is the cached snapshot alone.
  mock.readDelayMs = 3000;
  await openConversation(page, "Ledger Cat");
  assert.equal(await block(page, "left while sending").count(), 1);
  assert.equal(await stateOf(page, "left while sending"), "delivered");
  await context.close();
});

await check("group: read once any addressed Bot starts, and the mark names who", async () => {
  const { page, context } = await open({ conv: "Ops Room" });
  await type(page, "group hello");
  await waitState(page, "group hello", "delivered");
  await revealMark(page, "group hello");
  // The read-by names are the mark's hint (ActionHints, quiet tone, in #tofi-layers; design migration step 2).
  assert.equal(await block(page, "group hello").locator(".send-receipt-mark").getAttribute("data-hint"), null, "nothing to name yet");
  const [a, b] = mock.runs.filter(r => r.conversation_id === "grp");
  push("grp", "run", { ...b, status: "running", updated_at: iso(60) });
  await waitState(page, "group hello", "read");
  const mark = await revealMark(page, "group hello");
  await mark.hover();
  const tip = page.locator(".action-hint.is-quiet");
  await tip.waitFor({ timeout: 2000 });
  await sleep(200);
  await shot(page, "group-tip");
  assert.equal(await tip.textContent(), "Read by Simile Cat");
  assert.equal(await tip.evaluate(node => getComputedStyle(node).visibility), "visible");
  assert.equal(await tip.evaluate(node => Boolean(node.closest("#tofi-layers"))), true, "the names are portaled, not inside the row");
  push("grp", "run", { ...a, status: "running", updated_at: iso(61) });
  await page.waitForFunction(() => document.querySelector(".action-hint.is-quiet")?.textContent === "Read by Ledger Cat and Simile Cat");
  await page.mouse.move(5, 5);
  await sleep(300);
  assert.equal(await tip.count(), 0, "tip only on hover or focus: the row, and with it the mark, is gone when neither is on the message");
  await revealMark(page, "group hello");
  await block(page, "group hello").locator(".send-receipt-mark").focus();
  await page.locator(".action-hint.is-quiet").waitFor({ timeout: 2000 });
  assert.equal(await page.locator(".action-hint.is-quiet").evaluate(node => getComputedStyle(node).visibility), "visible", "keyboard focus shows it too");
  await context.close();
});

await check("history shows each message in its final state, drawn without animation", async () => {
  const { page, context } = await open();
  await shot(page, "history");
  assert.equal(await stateOf(page, "old question"), "read", "a finished run");
  assert.equal(await stateOf(page, "unanswered note"), "delivered", "no run, no reply");
  assert.equal(await stateOf(page, "being worked on"), "read", "a running run");
  assert.equal(await page.locator(".send-receipt-mark.is-settling").count(), 0);
  const moving = await page.evaluate(() => document.getAnimations().filter(a => a.effect?.target?.closest?.(".send-receipt-mark")).length);
  assert.equal(moving, 0, "no receipt animation on load");
  await openConversation(page, "Simile Cat");
  await openConversation(page, "Ledger Cat");
  assert.equal(await page.locator(".send-receipt-mark.is-settling").count(), 0, "a revisit from the cache stays still too");
  await context.close();
});

await check("reduced motion: no flight, no settle animation, no colour or opacity transition", async () => {
  const { page, context } = await open({ reduced: true });
  mock.sendDelayMs = 300;
  await type(page, "calm send");
  assert.equal(await page.locator(".v2-send-flight-ghost").count(), 0);
  await waitState(page, "calm send", "delivered");
  const queued = mock.runs.find(r => r.status === "queued");
  push("dm_a", "run", { ...queued, status: "running", updated_at: iso(50) });
  await waitState(page, "calm send", "read");
  const css = await (await revealMark(page, "calm send")).evaluate(node => { const svg = node.querySelector("svg"); return { anim: getComputedStyle(svg).animationName, transition: getComputedStyle(node).transitionDuration }; });
  assert.equal(css.anim, "none");
  assert.match(css.transition, /^0s/);
  await context.close();
});

await timingCheck("production");

await browser.close();
await server.close();
if (production) { await new Promise(done => production.served.httpServer.close(done)); rmSync(production.outDir, { recursive: true, force: true }); }
if (failures.length) { console.log(`send receipt checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`send receipt checks: PASS (${results.length})`);
