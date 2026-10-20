// Resilience: outages, dead links and restarts as the reader sees them.
// A real HTTP/SSE mock runs inside the vite dev server so connections can be held open, dropped and
// silenced (a plain page.route cannot). All data is synthetic.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:resilience
// RESILIENCE_UI_ROOT points the check at another checkout's ui/ (to prove a failure on the old code).
// RESILIENCE_ONLY=<substring> runs matching checks only.
import assert from "node:assert/strict";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = process.env.RESILIENCE_UI_ROOT ? resolve(process.env.RESILIENCE_UI_ROOT) : dirname(dirname(fileURLToPath(import.meta.url)));
const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["bot_a", "bot_b"].map((id, i) => ({ id, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${"ab"[i]}`, created_at: iso(0) }));
const convs = ["a", "b"].map((k, i) => ({ id: `dm_${k}`, kind: "dm", name: bots[i].name, bot_id: `bot_${k}`, bot_ids: [`bot_${k}`], updated_at: iso(0), read_seq: 4, user_visible: true }));
const history = conv => Array.from({ length: 4 }, (_, n) => ({ id: `${conv}_m${n + 1}`, conversation_id: conv, seq: n + 1, created_at: iso((n + 1) * 30), role: n % 2 ? "assistant" : "user", content: `Synthetic ${conv} line ${n + 1}`, ...(n % 2 ? { run_id: `run_${conv}_${n}`, sender_bot_id: conv === "dm_a" ? "bot_a" : "bot_b" } : {}) }));

/** The mock server's controls; the middleware below reads them on every request. */
const mock = { down: false, silent: false, held: [], pingMs: 3000, sse: new Set(), sseOpens: 0, requests: [], sendFails: 0, sendDelayMs: 0, serverInfoFails: 0, store: { dm_a: history("dm_a"), dm_b: history("dm_b") }, eventId: 100, runs: [], failSends: false };
function dropAll() { for (const res of mock.sse) res.destroy(); mock.sse.clear(); }
function setSilent(value) { mock.silent = value; if (!value) { for (const res of mock.held.splice(0)) res.__accept(); } }
function setDown(value) { mock.down = value; if (value) dropAll(); }
function push(conv, type, data) { const frame = `id: ${++mock.eventId}\nevent: ${type}\ndata: ${JSON.stringify(data)}\n\n`; for (const res of mock.sse) if (res.__conv === conv) res.write(frame); }
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const body = req => new Promise(resolveBody => { const chunks = []; req.on("data", c => chunks.push(c)); req.on("end", () => resolveBody(Buffer.concat(chunks).toString() || "null")); });

const mockPlugin = {
  name: "resilience-mock",
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      const url = new URL(req.url, "http://x");
      const path = url.pathname;
      if (!path.startsWith("/api/") && path !== "/health") return next();
      mock.requests.push(`${req.method} ${path}`);
      if (mock.down) return req.socket.destroy();
      const json = (value, status = 200) => { res.writeHead(status, { "content-type": "application/json" }); res.end(JSON.stringify(value)); };
      if (path.endsWith("/events") || path === "/api/workspace/events") {
        res.__conv = path.split("/")[3] ?? "";
        const timer = setInterval(() => { if (!mock.silent && !res.__held) res.write("event: ping\ndata: {}\n\n"); }, mock.pingMs);
        req.on("close", () => { clearInterval(timer); mock.sse.delete(res); mock.held = mock.held.filter(item => item !== res); });
        const accept = () => { res.__held = false; res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" }); res.write(": open\n\n"); mock.sse.add(res); mock.sseOpens++; };
        // A black-holed path accepts the connection and never answers it.
        if (mock.silent) { res.__held = true; res.__accept = accept; mock.held.push(res); return; }
        return accept();
      }
      const conv = path.split("/")[3] ?? "";
      if (/^\/api\/runs\/[^/]+\/retry$/.test(path)) {
        const old = mock.runs.find(run => run.id === path.split("/")[3]);
        const run = { ...old, id: old.id + "-retry", status: "queued", error: "", failure: undefined, parent_run_id: old.id, updated_at: iso(900) };
        mock.runs.push(run);
        return json(run);
      }
      if (path === "/api/server-info") { if (mock.serverInfoFails > 0) { mock.serverInfoFails--; return req.socket.destroy(); } return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } }); }
      if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
      if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
      if (path === "/api/bots") return json({ bots });
      if (path === "/api/conversations") return json({ conversations: convs });
      if (path.endsWith("/messages") && req.method === "POST") {
        const payload = JSON.parse(await body(req));
        if (mock.sendDelayMs) await sleep(mock.sendDelayMs);
        if (mock.failSends) return req.socket.destroy();
        const list = mock.store[conv];
        const message = { id: `${conv}_sent${list.length + 1}`, conversation_id: conv, role: "user", content: payload.content, seq: list.length + 1, created_at: iso(list.length * 30), client_message_id: payload.client_message_id };
        list.push(message);
        const run = { id: `run_sent_${list.length}`, conversation_id: conv, bot_id: conv === "dm_a" ? "bot_a" : "bot_b", status: "queued", model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: message.id, queue_seq: 1, created_at: iso(list.length * 30), updated_at: iso(list.length * 30) };
        return json({ message, run, runs: [run] });
      }
      if (path.endsWith("/messages")) return json({ messages: mock.store[conv] ?? [], drafts: [], has_more: false, event_cursor: mock.eventId });
      if (path.endsWith("/runs")) return json({ runs: mock.runs.filter(run => run.conversation_id === conv) });
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
  },
};

const server = await createServer({ configFile: false, root: ui, plugins: [react(), mockPlugin], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const engine = process.env.RESILIENCE_ENGINE === "webkit" ? pw.webkit : pw.chromium;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

function reset() {
  mock.down = false; setSilent(false); mock.sendFails = 0; mock.sendDelayMs = 0; mock.serverInfoFails = 0; mock.failSends = false; mock.runs = [];
  mock.store = { dm_a: history("dm_a"), dm_b: history("dm_b") };
  dropAll(); mock.sseOpens = 0; mock.requests = [];
}
async function open({ width = 1180, height = 820, first = true } = {}) {
  reset();
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US" });
  await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch {} });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor();
  if (first) await rowFor(page, "Ledger Cat").click();
  return { page, context, errors };
}
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
const liveOk = page => page.locator('.sidebar-account-identity[aria-label*="Live connection OK"]');
const liveLost = page => page.locator('.sidebar-account-identity[aria-label*="Live connection lost"]');
async function waitFor(fn, ms, what) { const end = Date.now() + ms; while (Date.now() < end) { if (await fn()) return Date.now() - (end - ms); await sleep(100); } throw new Error(`timed out after ${ms}ms: ${what}`); }

const results = [], failures = [];
const check = async (name, run) => {
  if (process.env.RESILIENCE_ONLY && !name.includes(process.env.RESILIENCE_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

// RES-1: a conversation opened during an outage must fill in and go live by itself once the server returns.
await check("conversation opened during an outage recovers without a click", async () => {
  const { page, context, errors } = await open();
  await liveOk(page).waitFor();
  setDown(true);
  await rowFor(page, "Simile Cat").click();
  await page.waitForTimeout(1500);
  assert.equal(await page.getByText("Start chatting").count(), 0, "an unreachable server must not read as 'no history'");
  await page.waitForTimeout(6000);
  setDown(false);
  const took = await waitFor(async () => (await page.getByText("Synthetic dm_b line 4").count()) > 0, 15000, "messages after the server returned");
  await waitFor(async () => (await liveOk(page).count()) > 0, 15000, "live connection after the server returned");
  console.log(`    recovered ${took}ms after the server returned`);
  assert.deepEqual(errors, []);
  await context.close();
});

// RES-2: a stream that dies silently is noticed and replaced; a returning network reconnects at once.
await check("a silent stream is detected and replaced", async () => {
  const { page, context } = await open();
  await liveOk(page).waitFor();
  const opensBefore = mock.sseOpens;
  setSilent(true);
  const warned = await waitFor(async () => (await liveLost(page).count()) > 0, 50000, "a warning once the stream went silent");
  console.log(`    warned ${warned}ms after the path went silent`);
  setSilent(false);
  const healed = await waitFor(async () => (await liveOk(page).count()) > 0, 5000, "recovery after the path came back");
  console.log(`    recovered ${healed}ms after the path came back`);
  assert.ok(mock.sseOpens > opensBefore, "a fresh stream was opened");
  await context.close();
});

await check("coming back online reconnects at once", async () => {
  const { page, context } = await open();
  await liveOk(page).waitFor();
  setDown(true);
  await page.waitForTimeout(5000);
  setDown(false);
  await page.waitForTimeout(100);
  const opens = mock.sseOpens;
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await waitFor(async () => mock.sseOpens > opens, 1500, "an immediate reconnect after the online event");
  await context.close();
});

// RES-3: a run sent back to the queue must not leave its old half reply on screen.
await check("a requeued run drops its stale draft", async () => {
  const { page, context } = await open();
  await liveOk(page).waitFor();
  const run = { id: "run_x", conversation_id: "dm_a", bot_id: "bot_a", status: "running", model: "synthetic-model", origin_conversation_id: "dm_a", queue_seq: 1, created_at: iso(500), updated_at: iso(500) };
  push("dm_a", "run", run);
  push("dm_a", "delta", { conversation_id: "dm_a", run_id: "run_x", bot_id: "bot_a", message_id: "draft_x", text: "HALF-WRITTEN-REPLY", content: "HALF-WRITTEN-REPLY", revision: 1, seq: 1 });
  await page.getByText("HALF-WRITTEN-REPLY").waitFor();
  push("dm_a", "run", { ...run, status: "queued", updated_at: iso(501) });
  await waitFor(async () => (await page.getByText("HALF-WRITTEN-REPLY").count()) === 0, 2000, "the stale draft to disappear");
  await context.close();
});

// RES-8: a blip changes nothing on screen.
await check("a two second blip shows nothing and moves nothing", async () => {
  const { page, context } = await open();
  await liveOk(page).waitFor();
  const probe = () => page.evaluate(() => ({ grid: document.querySelector(".workspace-grid")?.getBoundingClientRect().top ?? -1, status: document.querySelectorAll(".workspace-status, .stream-status").length, statusText: [...document.querySelectorAll(".workspace-status, .stream-status")].map(e => e.className + ":" + e.textContent.slice(0, 60)).join("|"), lost: document.querySelectorAll('.sidebar-account-identity[aria-label*="lost"]').length, cards: document.body.innerText.includes("status is unconfirmed") }));
  const seen = [];
  setDown(true);
  const sampler = (async () => { for (let i = 0; i < 40; i++) { seen.push(await probe()); await sleep(200); } })();
  await sleep(2000);
  setDown(false);
  await sampler;
  assert.equal(new Set(seen.map(s => s.grid)).size, 1, "the layout moved during the blip");
  assert.ok(seen.every(s => s.status === 0 && s.lost === 0 && !s.cards), `something announced the blip: ${JSON.stringify(seen.find(s => s.status || s.lost || s.cards))}`);
  await context.close();
});

// RES-4: a failed send stays as a Not sent bubble in the app's words; a failed startup retries by itself.
await check("a failed send stays on screen as Not sent, with the reason in the app's words", async () => {
  const { page, context } = await open();
  await liveOk(page).waitFor();
  mock.failSends = true;
  await page.locator(".composer textarea").fill("synthetic hello");
  await page.keyboard.press("Enter");
  const retry = page.locator(".message-list .send-receipt-retry");
  await retry.waitFor({ timeout: 5000 });
  assert.match(await retry.innerText(), /not sent.*retry/i);
  const reason = (await retry.getAttribute("title")) ?? "";
  assert.ok(!/failed to fetch|load failed|networkerror/i.test(reason), `raw browser text shown: ${reason}`);
  assert.match(reason, /reach the server/i);
  assert.equal(await page.locator(".message-list .error-banner").count(), 0, "one signal, on the bubble itself");
  assert.equal(await page.locator(".composer textarea").inputValue(), "", "the text lives in the bubble now, not the composer");
  await page.locator(".message-list .message-user .message-content", { hasText: "synthetic hello" }).first().waitFor();
  await context.close();
});

await check("an unreachable server at startup is retried within seconds", async () => {
  reset();
  mock.down = true;
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US" });
  const page = await context.newPage();
  await page.goto(origin + "/");
  await page.waitForTimeout(1500);
  const shown = await page.locator(".owner-error, .startup-error").first().innerText().catch(() => "");
  assert.ok(!/failed to fetch|load failed/i.test(shown), `raw browser text on the startup screen: ${shown}`);
  mock.down = false;
  const took = await waitFor(async () => (await page.locator(".conversation-row").count()) > 0, 12000, "the workspace after the server answered");
  console.log(`    workspace appeared ${took}ms after load`);
  await context.close();
});

// RES-5: a run that failed because the service was busy can be sent again from its own card.
await check("a failed run offers Retry and sends it again", async () => {
  reset();
  mock.runs = [{ id: "run_busy", conversation_id: "dm_a", bot_id: "bot_a", status: "failed", error: "server_is_overloaded", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "dm_a_m1", queue_seq: 1, created_at: iso(100), updated_at: iso(110) }];
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US" });
  const page = await context.newPage();
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor();
  await rowFor(page, "Ledger Cat").click();
  const retry = page.locator('[data-issue-retry="true"]');
  await retry.waitFor({ timeout: 8000 });
  await retry.click();
  await waitFor(async () => mock.requests.includes("POST /api/runs/run_busy/retry"), 3000, "the retry request");
  await context.close();
});

// RES-5: an expired or cancelled run must not offer Retry (the server refuses it).
await check("a cancelled run does not offer Retry", async () => {
  reset();
  mock.runs = [{ id: "run_gone", conversation_id: "dm_a", bot_id: "bot_a", status: "failed", error: "approval_expired", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "dm_a_m1", queue_seq: 1, created_at: iso(100), updated_at: iso(110), stop_reason: "approval_expired" }];
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US" });
  const page = await context.newPage();
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor();
  await rowFor(page, "Ledger Cat").click();
  await page.locator(".task-issue-card").waitFor({ timeout: 8000 });
  assert.equal(await page.locator('[data-issue-retry="true"]').count(), 0);
  await context.close();
});

await browser.close();
await server.close();
if (failures.length) { console.log(`resilience checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`resilience checks: PASS (${results.length})`);
