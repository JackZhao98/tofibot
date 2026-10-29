// Composer resilience: a message that cannot be sent stays (failed bubble with Retry / Edit / Delete, kept across a reload),
// is sent again once when the connection is back (shown as "Sending again"), and reaches the server exactly once; a sent
// file shows from the device with upload progress and swaps to the server's copy; the draft per conversation survives
// a switch and a reload. A real HTTP/SSE mock runs inside the vite dev server; all data is synthetic.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:composer-resilience
// RESILIENCE_ENGINE=webkit runs the same checks in WebKit.
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const INSTANCE = "00000000-0000-4000-8000-000000000003";
const T0 = Date.parse("2026-10-27T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["a", "b"].map((k, i) => ({ id: `bot_${k}`, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${k}`, created_at: iso(0) }));
const convs = ["a", "b"].map((k, i) => ({ id: `dm_${k}`, kind: "dm", name: bots[i].name, bot_id: `bot_${k}`, bot_ids: [`bot_${k}`], updated_at: iso(0), read_seq: 1, user_visible: true }));
const msg = (conv, seq, role, content, extra = {}) => ({ id: `${conv}_m${seq}`, conversation_id: conv, seq, role, content, created_at: iso(seq * 30), ...extra });
const seed = () => ({ store: { dm_a: [msg("dm_a", 1, "assistant", "hello from a")], dm_b: [msg("dm_b", 1, "assistant", "hello from b")] }, runs: [] });
const mock = { dictation: false, sse: new Set(), eventId: 100, posts: [], uploads: [], files: new Map(), sent: new Map(), n: 0, failSends: false, uploadDelayMs: 0, ...seed() };
const sleep = ms => new Promise(r => setTimeout(r, ms));
const raw = req => new Promise(done => { const c = []; req.on("data", d => c.push(d)); req.on("end", () => done(Buffer.concat(c))); });

function install(middlewares) {
  middlewares.use(async (req, res, next) => {
    const url = new URL(req.url, "http://x");
    const path = url.pathname;
    if (!path.startsWith("/api/") && path !== "/health") return next();
    const json = (value, status = 200) => { res.writeHead(status, { "content-type": "application/json" }); res.end(JSON.stringify(value)); };
    if (path.endsWith("/events") || path === "/api/workspace/events") {
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
    if (path === "/api/config") return json({ model_configured: true, dictation_configured: mock.dictation, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: convs });
    if (path.endsWith("/attachments") && req.method === "POST") {
      const body = await raw(req);
      mock.uploads.push(conv);
      if (mock.uploadDelayMs) await sleep(mock.uploadDelayMs);
      const name = /filename="([^"]+)"/.exec(body.subarray(0, 400).toString("latin1"))?.[1] ?? "file";
      const attachment = { id: `att_${mock.uploads.length}`, conversation_id: conv, name, mime: name.endsWith(".png") ? "image/png" : "text/plain", size: body.length, created_at: new Date().toISOString() };
      mock.files.set(attachment.id, attachment);
      return json(attachment, 201);
    }
    if (path.endsWith("/messages") && req.method === "POST") {
      const payload = JSON.parse((await raw(req)).toString() || "null");
      mock.posts.push({ conv, cmid: payload.client_message_id });
      if (mock.failSends) return json({ error: { code: "unavailable", message: "Synthetic: restarting for an update" } }, 503);
      const key = `${conv}:${payload.client_message_id}`;
      const duplicate = mock.sent.get(key);
      if (duplicate) return json(duplicate, 200);
      const list = mock.store[conv];
      const message = msg(conv, list.length + 1, "user", payload.content, { id: `${conv}_sent${++mock.n}`, client_message_id: payload.client_message_id, created_at: new Date().toISOString(), attachments: (payload.attachment_ids ?? []).map(id => ({ ...mock.files.get(id), message_id: `${conv}_sent${mock.n}` })) });
      list.push(message);
      const answer = { message, run: null, runs: [] };
      mock.sent.set(key, answer);
      return json(answer, 202);
    }
    if (path.endsWith("/messages")) return json({ messages: mock.store[conv] ?? [], drafts: [], has_more: false, event_cursor: mock.eventId });
    if (path.endsWith("/runs")) return json({ runs: [] });
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
const server = await createServer({ configFile: false, root: ui, plugins: [react(), { name: "resilience-mock", configureServer(s) { install(s.middlewares); } }], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await (process.env.RESILIENCE_ENGINE === "webkit" ? pw.webkit : pw.chromium).launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

async function open({ view = "desktop" } = {}) {
  Object.assign(mock, { sse: new Set(), posts: [], uploads: [], files: new Map(), sent: new Map(), n: 0, failSends: false, uploadDelayMs: 0, ...seed() });
  const size = view === "phone" ? { width: 390, height: 780, isMobile: true, hasTouch: true } : { width: 1180, height: 820 };
  const context = await browser.newContext({ viewport: { width: size.width, height: size.height }, locale: "en-US", ...(view === "phone" ? { isMobile: true, hasTouch: true } : {}) });
  await context.addInitScript(() => { try { if (!sessionStorage.getItem("seeded")) { localStorage.clear(); sessionStorage.setItem("seeded", "1"); localStorage.setItem("tofi:appearance", "light"); } } catch {} });
  const page = await context.newPage();
  const errors = [];
  // Going offline also stops lazy chunks that were not loaded yet: that is the browser, not the app.
  page.on("pageerror", error => { if (!/dynamically imported module/.test(error.message)) errors.push(error.message); });
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  return { page, context, errors };
}
async function openConversation(page, name) {
  const row = page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
  if (!(await row.first().isVisible())) await page.locator(".mobile-back, [aria-label*='conversations' i]").first().click().catch(() => {});
  await row.first().click();
  await page.locator(".composer textarea").waitFor();
  await page.locator(".message-list .message-block").first().waitFor();
}
const block = (page, text) => page.locator(".message-list .message-block").filter({ has: page.locator(".message-user") }).filter({ hasText: text });
const type = async (page, text) => { await page.locator(".composer textarea").fill(text); await page.keyboard.press("Enter"); };
async function waitFor(fn, ms, what) { const end = Date.now() + ms; while (Date.now() < end) { if (await fn()) return; await sleep(30); } throw new Error(`timed out: ${what}`); }
const delivered = (page, text) => block(page, text).locator(".message-user:not(.is-sending):not(:has(.send-receipt))").count().then(n => n > 0);
const stored = text => mock.store.dm_a.filter(m => m.content === text).length;

const results = [], failures = [];
const check = async (name, fn) => {
  if (process.env.RESILIENCE_ONLY && !name.includes(process.env.RESILIENCE_ONLY)) return;
  try { await fn(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.stack ?? error.message).split("\n").slice(0, 4).join("\n    ")}`); }
};

for (const view of ["desktop", "phone"]) {
  await check(`${view}: offline send -> failed -> connection back -> sent again once, shown, stored once`, async () => {
    const { page, context, errors } = await open({ view });
    await context.setOffline(true);
    const text = `sent offline ${view}`;
    await type(page, text);
    await block(page, text).locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 5000 });
    assert.equal(await page.locator(".composer textarea").inputValue(), "", "the text lives in the bubble, not the composer");
    const line = block(page, text).locator(".send-receipt");
    assert.match(await line.innerText(), /Not sent/);
    assert.match(await line.innerText(), /offline/i, "the reason is in the app's words");
    assert.match(await line.innerText(), /try again when you're back/i, "the promise is on show");
    for (const action of ["Retry", "Edit", "Delete"]) assert.ok(await line.getByRole("button", { name: new RegExp(action, "i") }).count(), `${action} is offered`);
    assert.equal(mock.posts.length, 0, "nothing reached the server while offline");
    // Connection back: one automatic try, visibly labelled, then delivered.
    await context.setOffline(false);
    await block(page, text).locator('.send-receipt[data-state="retrying"], .send-receipt-mark, .message-user:not(.is-sending):not(:has(.send-receipt))').first().waitFor({ timeout: 6000 });
    await waitFor(() => delivered(page, text), 6000, "delivered after the connection came back");
    assert.equal(stored(text), 1, "stored exactly once");
    assert.equal(new Set(mock.posts.map(p => p.cmid)).size, 1, "every attempt carried the same client_message_id");
    assert.equal(await block(page, text).count(), 1, "one bubble");
    await sleep(600);
    assert.equal(mock.posts.length, 1, "no second automatic send");
    assert.deepEqual(errors, []);
    await context.close();
  });
}

await check("the automatic try is announced: a 'Sending again' line while it is in flight", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "announce retry");
  await block(page, "announce retry").locator('.send-receipt[data-state="failed"]').waitFor();
  assert.match(await block(page, "announce retry").locator(".send-receipt").innerText(), /Server unavailable/);
  mock.failSends = false;
  await page.route("**/messages", async route => { await sleep(700); await route.continue(); });
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  const note = block(page, "announce retry").locator('.send-receipt[data-state="retrying"]');
  await note.waitFor({ timeout: 3000 });
  assert.match(await note.innerText(), /Sending again/);
  await waitFor(() => delivered(page, "announce retry"), 6000, "delivered");
  assert.equal(stored("announce retry"), 1);
  await context.close();
});

await check("a failed automatic try is not repeated: one automatic try, then the person decides", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "still down");
  await block(page, "still down").locator('.send-receipt[data-state="failed"]').waitFor();
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await waitFor(() => mock.posts.length >= 2, 4000, "one automatic try");
  await block(page, "still down").locator('.send-receipt[data-state="failed"]').waitFor();
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await sleep(900);
  assert.equal(mock.posts.length, 2, "one automatic try only; the person decides after that");
  assert.doesNotMatch(await block(page, "still down").locator(".send-receipt").innerText(), /try again when you're back/i, "the promise is gone once it was kept");
  mock.failSends = false;
  await block(page, "still down").locator(".send-receipt-retry").click();
  await waitFor(() => delivered(page, "still down"), 5000, "delivered by the manual Retry");
  assert.equal(stored("still down"), 1);
  await context.close();
});

await check("a failed message survives a reload (Retry works), and a reload never sends it by itself", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "survives reload");
  await block(page, "survives reload").locator('.send-receipt[data-state="failed"]').waitFor();
  const posts = mock.posts.length;
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  await block(page, "survives reload").locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 5000 });
  mock.failSends = false;
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await sleep(900);
  assert.equal(mock.posts.length, posts, "no automatic send after a reload");
  assert.equal(await block(page, "survives reload").count(), 1, "one bubble");
  await block(page, "survives reload").locator(".send-receipt-retry").click();
  await waitFor(() => delivered(page, "survives reload"), 5000, "delivered");
  assert.equal(stored("survives reload"), 1);
  assert.equal(new Set(mock.posts.map(p => p.cmid)).size, 1, "the same client_message_id after the reload");
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  assert.equal(await block(page, "survives reload").count(), 1);
  assert.equal(await page.locator(".send-receipt[data-state=failed]").count(), 0, "a delivered message leaves the outbox");
  await context.close();
});

await check("a message the server already stored (answer lost) does not come back as failed after a reload", async () => {
  const { page, context } = await open();
  await page.route("**/dm_a/messages", async route => { if (route.request().method() === "POST") { await route.fetch(); await route.abort("connectionreset"); } else await route.continue(); });
  await type(page, "answer lost");
  await block(page, "answer lost").locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 5000 });
  assert.equal(stored("answer lost"), 1);
  await page.unroute("**/dm_a/messages");
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  assert.equal(await block(page, "answer lost").count(), 1);
  assert.equal(await page.locator(".send-receipt[data-state=failed]").count(), 0, "the server's copy wins; the outbox entry is dropped");
  await context.close();
});

await check("Edit after an ambiguous failure: the server already has it -> no duplicate, the person is told", async () => {
  const { page, context } = await open();
  await page.route("**/dm_a/messages", async route => { if (route.request().method() === "POST") { await route.fetch(); await route.abort("connectionreset"); } else await route.continue(); });
  await type(page, "edited but delivered");
  await block(page, "edited but delivered").locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 5000 });
  assert.equal(stored("edited but delivered"), 1);
  await page.unroute("**/dm_a/messages");
  await block(page, "edited but delivered").locator(".send-receipt-edit").click();
  await page.locator(".error-banner", { hasText: /already sent/i }).waitFor({ timeout: 5000 });
  assert.equal(await page.locator(".composer textarea").inputValue(), "", "nothing moved to the composer");
  assert.equal(await block(page, "edited but delivered").count(), 1, "one bubble: the stored message");
  assert.equal(await page.locator(".send-receipt[data-state=failed]").count(), 0);
  assert.equal(stored("edited but delivered"), 1, "still stored once");
  await context.close();
});

await check("Edit when the check cannot be made keeps the bubble (no risk of a duplicate)", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "cannot check");
  await block(page, "cannot check").locator('.send-receipt[data-state="failed"]').waitFor();
  await page.route("**/dm_a/messages**", route => route.request().method() === "GET" ? route.abort("connectionreset") : route.continue());
  await block(page, "cannot check").locator(".send-receipt-edit").click();
  await page.locator(".error-banner", { hasText: /couldn't check/i }).waitFor({ timeout: 5000 });
  assert.equal(await block(page, "cannot check").locator('.send-receipt[data-state="failed"]').count(), 1, "the bubble stays");
  assert.equal(await page.locator(".composer textarea").inputValue(), "");
  await context.close();
});

await check("Delete removes the failed message for good; Edit puts it back in the composer", async () => {
  const { page, context } = await open();
  mock.failSends = true;
  await type(page, "delete me");
  await type(page, "edit me");
  await block(page, "edit me").locator('.send-receipt[data-state="failed"]').waitFor();
  await block(page, "delete me").locator('.send-receipt[data-state="failed"]').waitFor();
  await block(page, "delete me").locator(".send-receipt-delete").click();
  await waitFor(async () => (await block(page, "delete me").count()) === 0, 2000, "bubble removed");
  await block(page, "edit me").locator(".send-receipt-edit").click();
  await waitFor(async () => (await block(page, "edit me").count()) === 0, 2000, "bubble moved to the composer");
  assert.equal(await page.locator(".composer textarea").inputValue(), "edit me");
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  assert.equal(await page.locator(".send-receipt[data-state=failed]").count(), 0, "nothing comes back after a reload");
  assert.equal(await page.locator(".composer textarea").inputValue(), "edit me", "the edited text is the draft now");
  mock.failSends = false;
  await page.locator(".composer textarea").press("Enter");
  await waitFor(() => delivered(page, "edit me"), 5000, "sent from the composer");
  assert.equal(stored("edit me"), 1);
  assert.equal(stored("delete me"), 0);
  await context.close();
});

await check("the draft of each conversation survives switching and a reload", async () => {
  const { page, context } = await open();
  await page.locator(".composer textarea").fill("half written for A");
  await openConversation(page, "Simile Cat");
  assert.equal(await page.locator(".composer textarea").inputValue(), "");
  await page.locator(".composer textarea").fill("half written for B");
  await openConversation(page, "Ledger Cat");
  assert.equal(await page.locator(".composer textarea").inputValue(), "half written for A");
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  await openConversation(page, "Ledger Cat");
  assert.equal(await page.locator(".composer textarea").inputValue(), "half written for A", "A after a reload");
  await openConversation(page, "Simile Cat");
  assert.equal(await page.locator(".composer textarea").inputValue(), "half written for B", "B after a reload");
  const keys = await page.evaluate(() => Object.keys(localStorage).filter(k => k.startsWith("tofi:composer-drafts:")));
  assert.equal(keys.length, 1, "kept in localStorage under the instance key");
  await context.close();
});

await check("an attachment shows from the device at once with upload progress, then swaps to the server's copy", async () => {
  const { page, context, errors } = await open();
  mock.uploadDelayMs = 900;
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64");
  const big = Buffer.concat([png, Buffer.alloc(900_000, 1)]);
  await page.locator('.composer input[type="file"]').first().setInputFiles({ name: "photo.png", mimeType: "image/png", buffer: big });
  await page.locator(".composer textarea").fill("with picture");
  await page.keyboard.press("Enter");
  const tile = block(page, "with picture").locator(".attachment-thumb.is-pending");
  await tile.waitFor({ timeout: 2000 });
  assert.match(await tile.locator("img").getAttribute("src"), /^blob:/, "the picture shows from the file on this device");
  assert.ok(await tile.locator('[role="progressbar"]').count(), "an upload progress bar is on the tile");
  assert.equal(await page.locator(".composer textarea").inputValue(), "", "the composer did not wait for the upload");
  assert.equal(mock.posts.length, 0, "the message is posted after the file is up");
  await waitFor(async () => (await block(page, "with picture").locator(".attachment-thumb.is-pending").count()) === 0, 8000, "tile swapped to the uploaded copy");
  await waitFor(() => delivered(page, "with picture"), 5000, "delivered");
  assert.equal(stored("with picture"), 1);
  assert.equal(mock.uploads.length, 1, "uploaded once");
  assert.equal(await block(page, "with picture").locator(".attachment-thumb img").count(), 1);
  assert.deepEqual(errors, []);
  await context.close();
});

await check("a file whose upload fails keeps the message: Retry uploads again, one message stored", async () => {
  const { page, context } = await open();
  let fail = true;
  await page.route("**/attachments", async route => { if (fail) await route.abort("connectionreset"); else await route.continue(); });
  await page.locator('.composer input[type="file"]').first().setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("synthetic notes") });
  await page.locator(".composer textarea").fill("with notes");
  await page.keyboard.press("Enter");
  await block(page, "with notes").locator('.send-receipt[data-state="failed"]').waitFor({ timeout: 5000 });
  assert.ok(await block(page, "with notes").locator(".upload-progress.is-failed").count(), "the file says it was not uploaded");
  fail = false;
  await block(page, "with notes").locator(".send-receipt-retry").click();
  await waitFor(() => delivered(page, "with notes"), 6000, "delivered");
  assert.equal(stored("with notes"), 1);
  assert.equal(mock.uploads.length, 1, "uploaded once (the failed attempt never reached the server)");
  await context.close();
});

// Dictation needs OpenAI (ChatGPT sign-in or API key): with neither connected the composer has no mic.
await check("the composer mic appears only once an OpenAI transcription method is connected", async () => {
  mock.dictation = false;
  const { page, context } = await open();
  await page.locator(".composer textarea").waitFor();
  await sleep(400);
  assert.equal(await page.locator(".dictation-button").count(), 0, "no mic without an OpenAI method");
  mock.dictation = true;
  await page.reload();
  await openConversation(page, "Ledger Cat");
  await page.locator(".dictation-button").waitFor({ timeout: 5000 });
  mock.dictation = false;
  await context.close();
});

await browser.close();
await server.close();
if (failures.length) { console.log(`composer resilience checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`composer resilience checks: PASS (${results.length})`);
