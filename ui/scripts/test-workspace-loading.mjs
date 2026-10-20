// Workspace load must finish even when every list call is slower than the 5 s poll.
// Before the fix each poll superseded the response still in flight, so the full-screen
// "Loading workspace…" spinner never ended. Also pins that the poll does not stack
// requests behind a slow one and does not ask for archived rows.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:workspace-loading
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await pw.chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "x", model: "m", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: "2026-10-13T10:00:00Z" },
  { id: "bot_old", name: "Archived Cat", instructions: "x", model: "m", reasoning_effort: "medium", dm_conversation_id: "dm_old", created_at: "2026-10-13T10:00:00Z", archived: true }];
const convs = [
  { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: "2026-10-13T10:00:00Z", user_visible: true },
  { id: "dm_old", kind: "dm", name: "Archived Cat", bot_id: "bot_old", bot_ids: ["bot_old"], updated_at: "2026-10-13T09:00:00Z", user_visible: true, archived: true },
];
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

/** Opens the app against a mocked API. `slow` delays every list call; `replay` is how many workspace events the stream replays on connect. */
async function scenario({ slow, replay }) {
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US" });
  // Streams stay open like a real server's: on connect they replay `replay` bot/group events,
  // as a server that does not know the live head would for a client with no cursor.
  await context.addInitScript(count => {
    window.EventSource = class FakeEventSource {
      constructor(url) {
        this.url = url; this.listeners = {}; this.readyState = 0; this.onopen = null; this.onerror = null;
        const replays = /\/api\/workspace\/events|workspace_after/.test(url);
        setTimeout(() => {
          this.readyState = 1;
          this.onopen?.(new Event("open"));
          if (!replays) return;
          // An old server reads "head" as 0 and replays everything; a numeric cursor resumes after it.
          const cursor = Number(new URL(url, location.href).searchParams.get(/workspace_after/.test(url) ? "workspace_after" : "after")) || 0;
          for (let id = cursor + 1; id <= count; id++) {
            const event = new MessageEvent("workspace", { data: JSON.stringify({ scope: id % 2 ? "bots" : "groups", revision: id }), lastEventId: /\/api\/workspace\/events/.test(url) ? String(id) : "" });
            for (const fn of this.listeners.workspace ?? []) fn(event);
          }
        }, 30);
      }
      addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); }
      removeEventListener() {}
      close() { this.readyState = 2; }
    };
  }, replay);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const log = [];
  let inFlight = 0, maxInFlight = 0, configCalls = 0;
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const reply = body => route.fulfill({ status: 200, json: body });
    if (path === "/api/server-info") return reply({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "none" } });
    if (path === "/api/preferences") return reply({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") { configCalls += 1; return reply({ model_configured: true, default_model: "m", provider: "synthetic" }); }
    if (path === "/api/bots" || path === "/api/conversations") {
      const archived = url.searchParams.get("include_archived") === "true";
      log.push({ path, archived, at: Date.now() });
      inFlight += 1; maxInFlight = Math.max(maxInFlight, inFlight);
      await sleep(slow);
      inFlight -= 1;
      const visible = rows => archived ? rows : rows.filter(row => !row.archived);
      return reply(path === "/api/bots" ? { bots: visible(bots) } : { conversations: visible(convs) });
    }
    if (path.endsWith("/messages")) return reply({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return reply({ runs: [] });
    if (path.endsWith("/memories")) return reply({ memories: [] });
    if (path.endsWith("/tools")) return reply(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) return reply({ occurrences: [] });
    if (path.endsWith("/schedules")) return reply({ schedules: [] });
    if (path.startsWith("/api/computer")) return reply({ owner: null, waiting: [] });
    if (path === "/api/questions") return reply({ questions: [] });
    if (path === "/api/mail-drafts") return reply({ drafts: [] });
    return reply({});
  });
  const started = Date.now();
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor({ timeout: 25_000 });
  const shownAfter = Date.now() - started;
  assert.equal(await page.locator(".spinner").count(), 0, "spinner must be gone once the workspace is shown");
  assert.deepEqual(log.slice(0, 2).map(entry => entry.archived), [false, false], "the first load must not ask for archived rows");
  return { page, context, errors, log, started, shownAfter, stats: () => ({ maxInFlight, configCalls }) };
}

const only = process.env.WL_ONLY;
// 1. Every list call is slower than the 5 s poll: the workspace must still appear, and polls must not stack.
if (!only || only === '1') {
  const SLOW_MS = 6500;
  const run = await scenario({ slow: SLOW_MS, replay: 0 });
  assert.ok(run.shownAfter < 15_000, `workspace appeared after ${run.shownAfter} ms`);
  await sleep(12_000);
  const { maxInFlight } = run.stats();
  assert.ok(maxInFlight <= 4, `list requests stacked up: ${maxInFlight} in flight at once`);
  const polls = run.log.filter(entry => entry.at - run.started >= run.shownAfter && entry.path === "/api/conversations" && !entry.archived);
  assert.ok(polls.length <= 3, `polling over-fired during a slow response: ${polls.length}`);
  assert.deepEqual(run.errors, []);
  console.log(`ok - slow lists: workspace shown after ${run.shownAfter} ms with ${SLOW_MS} ms list calls; max in flight ${maxInFlight}`);
  await run.context.close();
}

// 2. (Dev React StrictMode doubles the mount load, hence the small allowance.) The event stream replays 600 bot/group events on connect (an eval just wrote them), delivered by a stream that stays open. One burst, one refresh.
if (!only || only === '2') {
  const run = await scenario({ slow: 150, replay: 600 });
  await sleep(4_000);
  const loads = run.log.filter(entry => entry.path === "/api/conversations" && !entry.archived).length;
  const { configCalls } = run.stats();
  if (process.env.WL_DEBUG) console.log(run.log.map(e => `${e.path}${e.archived ? "?a" : ""}@${e.at - run.started}`).join(" "));
  assert.ok(loads <= 4, `600 replayed events caused ${loads} conversation list loads`);
  assert.ok(configCalls <= 6, `600 replayed events caused ${configCalls} config loads`);
  assert.deepEqual(run.errors, []);
  console.log(`ok - event replay: ${loads} list loads, ${configCalls} config loads, workspace shown after ${run.shownAfter} ms`);
  await run.context.close();
}

await browser.close();
await server.close();
