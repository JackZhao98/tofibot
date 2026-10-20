// The conversation tool list carries only a preview of long results. Opening a step must load the
// full result, and a later list refresh (a preview of the same version) must not shrink it again.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:tool-result-preview
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

const T = "2026-10-13T10:00:00Z";
const FULL = "FULL_RESULT_HEAD " + "x".repeat(40_000) + " FULL_RESULT_TAIL";
const PREVIEW = "FULL_RESULT_HEAD " + "x".repeat(900) + "…";
const bot = { id: "bot_a", name: "Ledger Cat", instructions: "x", model: "m", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: T };
const conv = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: T, user_visible: true };
const run = { id: "r1", conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "m", origin_conversation_id: "dm_a", trigger_message_id: "m1", queue_seq: 1, created_at: T, updated_at: "2026-10-13T10:00:30Z" };
const messages = [
  { id: "m1", conversation_id: "dm_a", seq: 1, role: "user", content: "go", created_at: T },
  { id: "m2", conversation_id: "dm_a", seq: 2, role: "assistant", content: "done", run_id: "r1", sender_bot_id: "bot_a", created_at: "2026-10-13T10:00:30Z" },
];
const activity = { conversation_id: "dm_a", bot_id: "bot_a", run_id: "r1", call_id: "c1", name: "tofi_shell", arguments: '{"command":"ls"}', status: "completed", started_at: "2026-10-13T10:00:05Z", updated_at: "2026-10-13T10:00:20Z" };
const summary = { run_id: "r1", bot_id: "bot_a", tool_count: 1, completed_count: 1, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: activity.started_at, updated_at: activity.updated_at };

const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US" });
const page = await context.newPage();
const errors = [];
page.on("pageerror", error => errors.push(error.message));
const calls = { list: 0, full: 0 };
let listBytes = 0;
await page.route("**/api/**", async route => {
  const url = new URL(route.request().url());
  const path = url.pathname;
  const reply = body => route.fulfill({ status: 200, json: body });
  if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream" }, body: ": synthetic\n\n" });
  if (path === "/api/server-info") return reply({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "none" } });
  if (path === "/api/preferences") return reply({ timezone: "UTC", timezone_configured: true, show_activity: true });
  if (path === "/api/config") return reply({ model_configured: true, default_model: "m", provider: "synthetic" });
  if (path === "/api/bots") return reply({ bots: [bot] });
  if (path === "/api/conversations") return reply({ conversations: [conv] });
  if (path.endsWith("/messages")) return reply({ messages, drafts: [], has_more: false, event_cursor: 0 });
  if (path.endsWith("/runs")) return reply({ runs: [run] });
  if (path.endsWith("/tools")) {
    if (url.searchParams.has("call_id")) { calls.full += 1; return reply({ activity: { ...activity, result: FULL } }); }
    if (url.searchParams.has("run_ids")) return reply({ summaries: [summary] });
    // The run detail page is answered empty so the only way to the full text is the per-step lookup.
    if (url.searchParams.has("run_id")) return reply({ activities: [], tool_count: 1, has_more: false });
    calls.list += 1;
    const body = { activities: [{ ...activity, result: PREVIEW, truncated: true, result_preview: true, result_bytes: FULL.length }] };
    listBytes = JSON.stringify(body).length;
    return reply(body);
  }
  if (path.endsWith("/memories")) return reply({ memories: [] });
  if (path.endsWith("/schedule-occurrences")) return reply({ occurrences: [] });
  if (path.endsWith("/schedules")) return reply({ schedules: [] });
  if (path.startsWith("/api/computer")) return reply({ owner: null, waiting: [] });
  if (path === "/api/questions") return reply({ questions: [] });
  if (path === "/api/mail-drafts") return reply({ drafts: [] });
  return reply({});
});
await page.goto(origin + "/");
await page.locator(".conversation-row").first().click();
const step = page.locator(".task-step").first();
await step.waitFor({ state: "attached", timeout: 15_000 });
assert.ok(listBytes > 0 && listBytes < 4_000, `the list response was ${listBytes} bytes`);
const resultText = () => page.evaluate(() => [...document.querySelectorAll(".task-step pre")].map(pre => pre.textContent).join("\n"));
assert.ok(!(await resultText()).includes("FULL_RESULT_TAIL"), "the list preview must not already hold the full result");
// Opening the step must replace the preview with the full result.
await page.locator("details.task-activity > summary").first().click();
await step.locator("> details > summary").click();
await page.waitForFunction(() => [...document.querySelectorAll(".task-step pre")].some(pre => pre.textContent.includes("FULL_RESULT_TAIL")), null, { timeout: 10_000 });
assert.equal(calls.full, 1, "the full result is fetched once per step");
// A refresh of the list returns the same preview; the full result must stay.
await page.evaluate(() => window.dispatchEvent(new Event("focus")));
await page.waitForTimeout(600);
const stillFull = (await resultText()).includes("FULL_RESULT_TAIL");
assert.ok(stillFull, "a list refresh replaced the loaded full result with its preview");
assert.deepEqual(errors, []);
console.log(`ok - list ${listBytes} bytes, full result loaded on expand (${calls.full} fetch)`);
await browser.close();
await server.close();
