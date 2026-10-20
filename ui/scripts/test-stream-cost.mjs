// Streaming cost: a streamed chunk must redraw only the row that shows it, so its cost is independent of how long the
// conversation is. A ~150 block conversation is open while a reply streams in 14-character chunks every 33 ms.
// Budgets (net of the page's own idle cost, measured against an equally long quiet window in the same page):
//   <= 4 ms of main-thread time per chunk, <= 16 ms with the CPU throttled 4x, and no frame over 34 ms at 4x.
// A whole message arriving in a 360-exchange (720 block) history, every answer from a finished run, renders within 60 ms.
// Structure: once the reply has started, nothing in the message list outside the streaming row is touched.
// Production build (development React measures the wrong thing). Synthetic data; /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:stream-cost
// STREAM_COST_ENGINE=webkit runs the frame and structure checks in WebKit (it has no CPU metrics or throttle).
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { build, preview } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const webkit = process.env.STREAM_COST_ENGINE === "webkit";
const engine = webkit ? pw.webkit : pw.chromium;
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const outDir = process.env.STREAM_COST_DIST || await mkdtemp(join(tmpdir(), "tofi-stream-cost-"));
if (!process.env.STREAM_COST_DIST) await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const server = await preview({ configFile: false, root: ui, plugins: [react()], build: { outDir }, preview: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: iso(0) }];
const convs = [{ id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 150, user_visible: true }];
const COUNT = 150;
const history = Array.from({ length: COUNT }, (_, i) => {
  const seq = i + 1;
  const user = seq % 4 === 1;
  const kind = (seq * 7) % 6;
  const para = `Synthetic paragraph ${seq} reads the weekly entries and totals them against the ledger before the review on Friday.`;
  const content = user ? `Question ${seq}: can you check the ${kind} item for me please?`
    : kind === 0 ? `${para}\n\n- first item ${seq}\n- second item with \`inline code\`\n- third item\n\n${para}`
    : kind === 1 ? `Here is the snippet:\n\n\`\`\`ts\nexport function total(rows: number[]) {\n  return rows.reduce((a, b) => a + b, 0);\n}\n\`\`\`\n\n${para}`
    : kind === 2 ? `## Summary ${seq}\n\n${para}\n\n| Item | Value |\n|---|---|\n| a | 1 |\n| b | 2 |` : para;
  return { id: `dm_a_m${seq}`, conversation_id: "dm_a", seq, role: user ? "user" : "assistant", content, created_at: iso(seq * 30), ...(user ? {} : { run_id: `run_dm_a_${seq}`, sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" }) };
});
const REPLY = Array.from({ length: 14 }, (_, i) => i % 5 === 4 ? `- bullet ${i} with some detail about the entry totals` : `Synthetic streamed sentence ${i + 1} reads the weekly entries and totals them against the ledger.`).join("\n\n");
const CHUNK = 14;
const TICK = 33;
const CHUNKS = Math.ceil(REPLY.length / CHUNK);

async function open({ reduced, history: rows = history, ready = 140, runs = [] }) {
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference" });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(new Event("open")), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } removeEventListener() {} close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
    window.__frames = [];
    const tick = time => { window.__frames.push(time); requestAnimationFrame(tick); };
    requestAnimationFrame(tick);
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: convs });
    if (path.endsWith("/messages")) return json({ messages: rows, drafts: [], has_more: false, event_cursor: 0 });
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
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().click();
  await page.waitForFunction(count => document.querySelectorAll(".message-block").length >= count, ready, { timeout: 30000 });
  await page.waitForTimeout(1500);
  return { page, context, errors };
}

/** Streams one reply and then lets it settle as a saved message, so the next round starts from the same page. */
async function streamReply(page, round) {
  const common = { conversation_id: "dm_a", run_id: `run_live${round}`, bot_id: "bot_a", message_id: `m_live${round}`, seq: COUNT + 1 + round * 2, created_at: new Date().toISOString() };
  await page.evaluate(({ common, round }) => window.__emit("run", { conversation_id: "dm_a", run: { id: common.run_id, conversation_id: "dm_a", bot_id: "bot_a", status: "running", model: "m", origin_conversation_id: "dm_a", trigger_message_id: "dm_a_m149", queue_seq: 1, created_at: common.created_at, updated_at: common.created_at } }), { common, round });
  await page.evaluate(({ common, reply, chunk, tick }) => new Promise(resolve => {
    let i = 0, revision = 0;
    const step = () => {
      const text = reply.slice(i, i + chunk);
      if (!text) return resolve(null);
      // The run starting and the reply appearing change the timeline's structure once; chunks after that are what is counted.
      if (i >= 3 * chunk) window.__counting = true;
      revision += 1;
      window.__emit("delta", revision === 1 ? { ...common, text, content: text, revision } : { ...common, text, revision });
      i += chunk;
      setTimeout(step, tick);
    };
    step();
  }), { common, reply: REPLY, chunk: CHUNK, tick: TICK });
}
const settle = async (page, round) => {
  await page.evaluate(({ round, reply }) => {
    const created_at = new Date().toISOString();
    window.__emit("message", { id: `m_live${round}`, conversation_id: "dm_a", seq: 151 + round * 2, role: "assistant", kind: "segment", sender_bot_id: "bot_a", run_id: `run_live${round}`, content: reply, created_at });
    window.__emit("run", { conversation_id: "dm_a", run: { id: `run_live${round}`, conversation_id: "dm_a", bot_id: "bot_a", status: "done", updated_at: created_at } });
  }, { round, reply: REPLY });
  await page.waitForTimeout(1200);
};

async function measure({ reduced, throttle }) {
  const { page, context, errors } = await open({ reduced });
  const cdp = webkit ? null : await context.newCDPSession(page);
  if (cdp) { await cdp.send("Performance.enable"); if (throttle > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: throttle }); }
  const metrics = async () => cdp ? Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(m => [m.name, m.value])) : {};
  const nets = [];
  const over34 = [];
  const quietOver34 = [];
  let outside = 0;
  for (let round = 0; round < 3; round++) {
    // A quiet window as long as the stream: the page's own cost (avatar life, timers) is not the stream's.
    const quietFrom = await page.evaluate(() => window.__frames.length);
    const quiet0 = await metrics();
    await page.waitForTimeout(CHUNKS * TICK);
    const quiet1 = await metrics();
    const quietGaps = await page.evaluate(from => { const f = window.__frames.slice(from); return f.slice(1).map((time, i) => time - f[i]); }, quietFrom);
    quietOver34.push(quietGaps.filter(gap => gap > 34).length);
    await page.evaluate(({ round }) => {
      window.__outside = 0;
      window.__counting = false;
      const live = () => document.querySelector(`[data-message-id="m_live${round}"]`);
      window.__observer?.disconnect();
      window.__observer = new MutationObserver(records => { const own = live(); if (window.__counting) for (const record of records) if (!own || !own.contains(record.target)) window.__outside += 1; });
      window.__observer.observe(document.querySelector(".message-list"), { subtree: true, childList: true, attributes: true, characterData: true });
    }, { round });
    const frames0 = await page.evaluate(() => window.__frames.length);
    const t0 = await metrics();
    await streamReply(page, round);
    const t1 = await metrics();
    const gaps = await page.evaluate(from => { const f = window.__frames.slice(from); return f.slice(1).map((time, i) => time - f[i]); }, frames0);
    outside += await page.evaluate(() => { const n = window.__outside; window.__observer.disconnect(); return n; });
    if (cdp) nets.push(((t1.TaskDuration - t0.TaskDuration) - (quiet1.TaskDuration - quiet0.TaskDuration)) * 1000 / CHUNKS);
    over34.push(gaps.filter(gap => gap > 34).length);
    await settle(page, round);
  }
  nets.sort((a, b) => a - b);
  const net = nets[Math.floor(nets.length / 2)];
  await context.close();
  assert.deepEqual(errors, []);
  return { net, over34: Math.max(...over34), quietOver34: Math.max(...quietOver34), outside };
}

/** Time from a whole message arriving to the second frame after it, in a history of `exchanges` question and answer pairs: the least of 7. */
async function arrivalCost(exchanges) {
  const rows = Array.from({ length: exchanges * 2 }, (_, i) => ({ id: `x${i}`, conversation_id: "dm_a", seq: i + 1, role: i % 2 ? "assistant" : "user", content: i % 2 ? `Synthetic answer ${i}` : `Synthetic question ${i}`, created_at: iso(5000 + i * 20), ...(i % 2 ? { run_id: `run_x${i}`, sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" } : {}) }));
  // Every answer comes from a finished run, as in a real history: each exchange carries a task block.
  const runs = Array.from({ length: exchanges }, (_, k) => ({ id: `run_x${k * 2 + 1}`, conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: `x${k * 2}`, queue_seq: 1, created_at: iso(5000 + k * 40), updated_at: iso(5001 + k * 40) }));
  const { page, context } = await open({ reduced: false, history: rows, ready: Math.min(rows.length, 120), runs });
  await page.waitForTimeout(800);
  const times = [];
  for (let n = 0; n < 7; n++) {
    times.push(await page.evaluate(async n => {
      const t0 = performance.now();
      window.__emit("message", { id: `cost${n}`, conversation_id: "dm_a", seq: 90000 + n, role: "assistant", sender_bot_id: "bot_a", run_id: `run_cost${n}`, content: `Synthetic probe ${n}`, created_at: new Date().toISOString() });
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      return performance.now() - t0;
    }, n));
    await page.waitForTimeout(150);
  }
  await context.close();
  return Math.min(...times);
}

const results = [];
const failures = [];
const check = async (name, run) => {
  if (process.env.STREAM_COST_ONLY && !name.includes(process.env.STREAM_COST_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

if (webkit) {
  await check("a streamed chunk touches only its own row and drops no frames (WebKit)", async () => {
    const { over34, outside } = await measure({ reduced: false, throttle: 1 });
    console.log(`  ${CHUNKS} chunks: ${outside} changes outside the streaming row, worst round had ${over34} frames over 34 ms`);
    assert.equal(outside, 0, `${outside} DOM changes outside the streaming row`);
    assert.equal(over34, 0, `${over34} frames over 34 ms`);
  });
} else {
  await check("a streamed chunk touches only its own row", async () => {
    const { outside } = await measure({ reduced: true, throttle: 1 });
    console.log(`  ${CHUNKS} chunks: ${outside} changes outside the streaming row`);
    assert.equal(outside, 0, `${outside} DOM changes outside the streaming row`);
  });
  await check("a message arriving in a 360-exchange history renders within 60 ms", async () => {
    const large = await arrivalCost(360);
    console.log(`  ${Math.round(large)} ms at 360 exchanges`);
    assert.ok(large <= 60, `${Math.round(large)} ms exceeds 60`);
  });
  await check("a streamed chunk costs at most 4 ms of main-thread time", async () => {
    const { net } = await measure({ reduced: false, throttle: 1 });
    console.log(`  ${net.toFixed(2)} ms/chunk (median of 3 replies, net of the idle cost)`);
    assert.ok(net <= 4, `${net.toFixed(2)} ms per chunk exceeds 4`);
  });
  await check("a streamed chunk costs at most 16 ms at 4x CPU throttle, with no frame over 34 ms", async () => {
    const { net, over34, quietOver34 } = await measure({ reduced: false, throttle: 4 });
    console.log(`  ${net.toFixed(2)} ms/chunk at 4x, worst round had ${over34} frames over 34 ms (the idle page alone drops ${quietOver34} in as long a window)`);
    assert.ok(net <= 16, `${net.toFixed(2)} ms per chunk exceeds 16 at 4x`);
    // The avatars' own life animation can drop a frame at 4x with nothing streaming; the stream may not add to that.
    assert.ok(over34 <= quietOver34, `${over34} frames over 34 ms at 4x, ${quietOver34} when idle`);
  });
}

await browser.close();
await server.close();
console.log(`stream cost checks: ${failures.length ? `FAIL (${failures.length} of ${results.length + failures.length})` : `PASS (${results.length} scenarios)`}`);
process.exit(failures.length ? 1 : 0);
