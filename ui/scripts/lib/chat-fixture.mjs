// Shared synthetic-chat fixture for the browser tests that need the production bundle and a mocked API: builds
// the UI once, serves it, and opens /b/bot_a with 40 synthetic messages and a controllable event stream.
// Synthetic data only. Callers: scripts/test-live-fixes.mjs (and anything that wants the same page).
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import http from "node:http";
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { extname, join } from "node:path";
import { build } from "vite";
import react from "@vitejs/plugin-react";

export async function createFixture({ engineName = "chromium", throttle = 1 } = {}) {
const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engine = engineName === "webkit" ? pw.webkit : pw.chromium;
const THROTTLE = throttle;
const ui = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
// Frame budgets only mean something against the production bundle: React's development build alone stalls a frame for 150 ms.
const outDir = mkdtempSync(join(tmpdir(), "tofi-fixture-"));
await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const mime = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".json": "application/json", ".woff2": "font/woff2", ".png": "image/png" };
const server = http.createServer((req, res) => {
  let file = join(outDir, new URL(req.url, "http://x").pathname);
  if (!existsSync(file) || statSync(file).isDirectory()) file = join(outDir, "index.html");
  res.writeHead(200, { "content-type": mime[extname(file)] || "application/octet-stream" });
  res.end(readFileSync(file));
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: iso(0) }];
const dm = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 99, user_visible: true };
const LONG = Array.from({ length: 4 }, (_, i) => `Synthetic ledger sentence number ${i + 1} describes the weekly entries and totals.`).join(" ");
const history = () => Array.from({ length: 40 }, (_, i) => {
  const seq = i + 1, user = seq % 3 === 1;
  return { id: `m${seq}`, conversation_id: "dm_a", seq, role: user ? "user" : "assistant", content: user ? `Question ${seq}: ${LONG}` : `${LONG}\n\n- point ${seq}\n- another point`, created_at: iso(seq * 20), ...(user ? {} : { sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: `run_m${seq}` }) };
});
const WHOLE = "Here is the full answer arriving in one piece.\n\n- first point about the ledger\n- second point about totals\n- third point with `code`\n\nClosing paragraph with a bit more detail about the review on Friday and what to check first before then.";
async function open({ motion = "no-preference", viewport = { width: 1180, height: 820 }, route: extraRoute, messages: seed, path = "/b/bot_a", waitFor = ".message-block" } = {}) {
  const context = await browser.newContext({ viewport, locale: "en-US", reducedMotion: motion });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
    window.__ls = [];
    try { new PerformanceObserver(list => { for (const e of list.getEntries()) if (!e.hadRecentInput) window.__ls.push([e.startTime, e.value]); }).observe({ type: "layout-shift", buffered: true }); } catch {}
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const messages = seed ?? history();
  await page.route("**/api/**", async route => {
    if (extraRoute && await extraRoute(route, messages)) return;
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: [dm] });
    if (path.endsWith("/messages")) return json({ messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [{ id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m38", queue_seq: 1, created_at: iso(39 * 20), updated_at: iso(39 * 20 + 5) }] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/tools")) {
      const activities = [1, 2, 3].map(n => ({ conversation_id: "dm_a", bot_id: "bot_a", run_id: "run_m39", call_id: `c${n}`, name: "tofi_shell", arguments: `{"command":"ls ${n}"}`, result: "ok", status: "completed", truncated: false, started_at: iso(39 * 20 + n), updated_at: iso(39 * 20 + n + 1) }));
      const summary = { run_id: "run_m39", bot_id: "bot_a", tool_count: 3, completed_count: 3, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: activities[0].started_at, updated_at: activities[2].updated_at };
      if (url.searchParams.has("run_ids")) return json({ summaries: [summary] });
      if (url.searchParams.has("run_id")) return json({ activities, tool_count: 3, has_more: false });
      return json({ activities, summaries: [summary], tool_count: 3, has_more: false });
    }
    if (path.endsWith("/schedule-occurrences")) return json({ occurrences: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(origin + path);
  if (waitFor) await page.locator(waitFor).first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(700);
  if (THROTTLE > 1 && engine === pw.chromium) await (await context.newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: THROTTLE });
  return { page, context, errors };
}

const close = async () => { await browser.close(); server.close(); rmSync(outDir, { recursive: true, force: true }); };
return { pw, engine, browser, origin, open, close, iso, history, WHOLE, bots, dm, T0 };
}
