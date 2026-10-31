// Day-one templates: an empty direct chat offers Morning briefing, Inbox digest and Watch a web page; each opens a short
// form and creates an ordinary schedule through POST /api/conversations/{id}/schedules, then shows it. The app's /api is
// answered in the browser with synthetic data.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:day-one-templates
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";
import { pickSelect } from "./lib/select.mjs";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const at = "2026-10-01T00:00:00Z";
const bot = { id: "bot_1", name: "Synthetic Bot", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "conv_1", created_at: at };
const dm = { id: "conv_1", kind: "dm", name: "Synthetic Bot", bot_id: "bot_1", bot_ids: ["bot_1"], updated_at: at, read_seq: 0, user_visible: true };

/** `existing` are schedules already in the chat; `messages` its history; `gmail` the Gmail connection the server reports. */
async function open({ existing = [], messages = [], gmail = { connected: false, email: "" }, width = 1280 } = {}) {
  const context = await browser.newContext({ viewport: { width, height: 900 }, locale: "en-US" });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const created = [];
  await page.route("**/api/**", route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const json = (body, status = 200) => route.fulfill({ status, json: body });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots: [bot] });
    if (path === "/api/conversations") return json({ conversations: [dm] });
    if (path === "/api/conversations/conv_1/schedules" && request.method() === "POST") {
      const body = request.postDataJSON();
      created.push(body);
      const next = body.kind === "interval" ? body.run_at : "2026-10-31T07:30:00Z";
      return json({ id: `sched_${created.length}`, conversation_id: "conv_1", bot_id: "bot_1", status: "active", created_at: at, updated_at: at, next_at_utc: next, ...body }, 201);
    }
    if (path === "/api/conversations/conv_1/schedules") return json({ schedules: existing });
    if (path === "/api/extensions/local-mcp/gog/oauth/status") return json(gmail);
    if (path.endsWith("/messages")) return json({ messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [] });
    if (path.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(`${origin}/b/bot_1`);
  return { page, context, errors, created };
}

const offer = page => page.locator("[data-day-one='offer']");
const results = [];
const pass = name => { results.push(name); console.log("PASS", name); };

try {
  // 1. The empty chat offers the three templates; Morning briefing creates a daily schedule and shows it.
  {
    const { page, context, errors, created } = await open();
    await offer(page).waitFor();
    assert.deepEqual(await offer(page).locator(".day-one-choice strong").allTextContents(), ["Morning briefing", "Inbox digest", "Watch a web page"]);
    await offer(page).locator("[data-template='briefing']").click();
    await page.getByLabel("Topics").fill("AI, climate");
    await page.getByLabel("Time of day").fill("07:30");
    await page.getByRole("button", { name: "Create schedule" }).click();
    await page.locator("[data-day-one='created']").waitFor();
    assert.equal(created.length, 1);
    const [body] = created;
    assert.equal(body.kind, "daily");
    assert.equal(body.daily_time, "07:30");
    assert.equal(body.timezone, "UTC");
    assert.equal(body.bot_id, "bot_1");
    assert.equal(body.title, "Morning briefing");
    assert.match(body.content, /AI, climate/);
    assert.match(body.content, /Post the briefing in this chat/);
    assert.equal(body.run_at, undefined);
    const card = page.locator("[data-day-one='created']");
    assert.match(await card.innerText(), /Scheduled/);
    assert.match(await card.innerText(), /Morning briefing/);
    assert.match(await card.innerText(), /Every day at 07:30/);
    assert.match(await card.innerText(), /next run .*7:30/i);
    assert.deepEqual(errors, []);
    await context.close();
    pass("Morning briefing: form -> daily schedule -> shown with cadence and next run");
  }

  // 2. Inbox digest explains the connection while Gmail is not connected.
  {
    const { page, context, created } = await open({ gmail: { connected: false, email: "" } });
    await offer(page).waitFor();
    await offer(page).locator("[data-template='inbox']").click();
    await page.getByText("connect Google first").waitFor();
    assert.equal(await page.getByRole("button", { name: "Create schedule" }).count(), 0, "no form without Gmail");
    assert.equal(await page.getByRole("button", { name: "Connect Google" }).count(), 1);
    assert.equal(created.length, 0);
    await context.close();
    pass("Inbox digest without Gmail: explains the connection and offers no form");
  }

  // 3. With Gmail connected the Inbox digest creates its schedule.
  {
    const { page, context, created } = await open({ gmail: { connected: true, email: "me@example.test", services: ["gmail", "calendar"] } });
    await offer(page).waitFor();
    await offer(page).locator("[data-template='inbox']").click();
    await page.getByLabel("Anything to watch for (optional)").fill("Project Atlas");
    assert.equal(await page.getByLabel("Digest time").inputValue(), "18:00");
    await page.getByRole("button", { name: "Create schedule" }).click();
    await page.locator("[data-day-one='created']").waitFor();
    assert.equal(created[0].kind, "daily");
    assert.equal(created[0].daily_time, "18:00");
    assert.equal(created[0].title, "Inbox digest");
    assert.match(created[0].content, /Project Atlas/);
    assert.match(created[0].content, /Do not send, delete, archive or label any mail/);
    await context.close();
    pass("Inbox digest with Gmail: daily schedule created");
  }

  // 4. Watch a web page: an hourly check is an interval schedule whose first run is in the future; a daily one uses a clock time.
  {
    const { page, context, created } = await open();
    await offer(page).waitFor();
    await offer(page).locator("[data-template='watch']").click();
    await page.getByLabel("Page address").fill("https://example.test/pricing");
    await pickSelect(page.locator(".day-one-form .ui-select-trigger"), "hourly");
    assert.equal(await page.getByLabel("Time of day").count(), 0, "an hourly check needs no clock time");
    const before = Date.now();
    await page.getByRole("button", { name: "Create schedule" }).click();
    await page.locator("[data-day-one='created']").waitFor();
    const [body] = created;
    assert.equal(body.kind, "interval");
    assert.equal(body.interval_seconds, 3600);
    assert.ok(Date.parse(body.run_at) > before, "the first run is in the future");
    assert.equal(body.title, "Watch example.test");
    assert.match(body.content, /https:\/\/example\.test\/pricing/);
    assert.match(await page.locator("[data-day-one='created']").innerText(), /Every hour/);
    await page.getByRole("button", { name: "Add another" }).click();
    await offer(page).waitFor();
    await offer(page).locator("[data-template='watch']").click();
    await page.getByLabel("Page address").fill("https://example.test/news");
    await pickSelect(page.locator(".day-one-form .ui-select-trigger"), "daily");
    await page.getByLabel("Time of day").fill("09:15");
    await page.getByRole("button", { name: "Create schedule" }).click();
    await page.locator("[data-day-one='created']").waitFor();
    assert.equal(created[1].kind, "daily");
    assert.equal(created[1].daily_time, "09:15");
    await context.close();
    pass("Watch a web page: interval and daily schedules");
  }

  // 5. The offer stays away once it is not needed: a schedule exists, the person has written, or it was dismissed.
  {
    const existing = [{ id: "sched_old", conversation_id: "conv_1", bot_id: "bot_1", status: "active", kind: "daily", daily_time: "08:00", timezone: "UTC", next_at_utc: "2026-10-31T08:00:00Z", content: "x", title: "Old", created_at: at, updated_at: at }];
    const withSchedule = await open({ existing });
    await withSchedule.page.locator(".conversation-empty, .message-list").first().waitFor();
    await withSchedule.page.waitForTimeout(600);
    assert.equal(await offer(withSchedule.page).count(), 0, "hidden when a schedule exists");
    await withSchedule.context.close();

    const written = await open({ messages: [{ id: "m1", conversation_id: "conv_1", seq: 1, role: "user", content: "hello", created_at: at }] });
    await written.page.locator(".message-list").waitFor();
    await written.page.waitForTimeout(600);
    assert.equal(await offer(written.page).count(), 0, "hidden once the person has written");
    await written.context.close();

    const { page, context } = await open();
    await offer(page).waitFor();
    await page.getByRole("button", { name: "Not now" }).click();
    assert.equal(await offer(page).count(), 0);
    assert.equal(await page.evaluate(() => localStorage.getItem("tofi:day-one-dismissed")), "1");
    await page.reload();
    await page.locator(".conversation-empty").waitFor();
    await page.waitForTimeout(600);
    assert.equal(await offer(page).count(), 0, "a dismissal is remembered");
    await context.close();
    pass("offer hidden after a schedule exists, after the person writes, and once dismissed");
  }

  // 6. A failed create keeps the form and says so.
  {
    const { page, context } = await open();
    await page.route("**/api/conversations/conv_1/schedules", route => route.request().method() === "POST" ? route.fulfill({ status: 400, json: { error: { code: "schedule_time_invalid", message: "bad time" } } }) : route.fallback());
    await offer(page).waitFor();
    await offer(page).locator("[data-template='briefing']").click();
    await page.getByLabel("Topics").fill("AI");
    await page.getByRole("button", { name: "Create schedule" }).click();
    await page.getByRole("alert").first().waitFor();
    assert.equal(await page.locator("[data-day-one='created']").count(), 0);
    assert.equal(await page.getByLabel("Topics").inputValue(), "AI", "the form keeps what was typed");
    await context.close();
    pass("a failed create keeps the form and shows an error");
  }

  // 7. No horizontal scroll on a phone.
  {
    const { page, context } = await open({ width: 390 });
    await offer(page).waitFor();
    await offer(page).locator("[data-template='watch']").click();
    assert.ok((await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)) <= 0, "no horizontal scroll at 390px");
    await context.close();
    pass("no horizontal scroll at 390px");
  }
  console.log(`PASS: ${results.length} groups`);
} finally {
  await browser.close();
  await server.close();
}
