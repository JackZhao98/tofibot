// Composer typing cost: keystrokes must only re-render the composer, never the message list, so typing cost is
// independent of conversation length. Measured through CDP Performance.getMetrics over 40 typed characters in a
// ~150 message group, plus behaviour checks (draft switch/restore, IME, Enter/Shift+Enter, draft persistence).
// Synthetic data only; /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:composer-typing
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build, preview } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
// A production build: development React is several times slower per render and would measure the wrong thing.
const outDir = await mkdtemp(join(tmpdir(), "tofi-composer-typing-"));
await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const server = await preview({ configFile: false, root: ui, plugins: [react()], build: { outDir }, preview: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await pw.chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["bot_a", "bot_b"].map((id, i) => ({ id, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${"ab"[i]}`, created_at: iso(0) }));
const convs = [
  { id: "grp_1", kind: "group", name: "Synthetic Group", bot_ids: ["bot_a", "bot_b"], updated_at: iso(0), read_seq: 150, user_visible: true },
  { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 10, user_visible: true },
];
const COUNT = { grp_1: 150, dm_a: 12 };
const SCHED_RUN = "5c1e0a00-0000-4000-8000-000000000001";
const SCHED_ID = "5c1e0a00-0000-4000-8000-000000000002";
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

function history(conv) {
  const out = [];
  for (let seq = 1; seq <= COUNT[conv]; seq++) {
    const user = seq % 5 === 1;
    const lines = 1 + ((seq * 7) % 5);
    const sender = seq % 3 === 0 ? ["bot_b", "Simile Cat"] : ["bot_a", "Ledger Cat"];
    const base = { id: `${conv}_m${seq}`, conversation_id: conv, seq, created_at: iso(seq * 30) };
    if (conv === "grp_1" && seq === 140) out.push({ ...base, role: "assistant", kind: "scheduled_task", run_id: SCHED_RUN, content: "Synthetic scheduled instruction", sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" });
    else if (conv === "grp_1" && seq === 120) out.push({ ...base, role: "assistant", kind: "notice", sender_bot_id: "bot_b", run_id: "run_child", content: "@Ledger Cat take the other side. Requirements: plain language, short.", notice: { type: "handoff", from_bot_id: "bot_b", to_bot_id: "bot_a", target_conversation_id: "grp_1", target_run_id: "run_child" } });
    else if (!user && seq % 7 === 2) out.push({ ...base, role: "assistant", kind: "segment", run_id: `run_${conv}_${seq}`, content: `Synthetic ${conv} segment ${seq}`, sender_bot_id: sender[0], sender_bot_name: sender[1] });
    else out.push({ ...base, role: user ? "user" : "assistant", content: Array.from({ length: lines }, (_, n) => `Synthetic ${conv} line ${seq}.${n}`).join("\n\n"), ...(user ? {} : { run_id: `run_${conv}_${seq}`, sender_bot_id: sender[0], sender_bot_name: sender[1] }) });
  }
  return out;
}

async function open({ reduced = true } = {}) {
  const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference" });
  await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch {} });
  // A held-open event stream: a mocked stream that ends would make the app reconnect and re-render in a loop,
  // which is not what this test measures.
  await context.addInitScript(() => {
    window.EventSource = class { constructor() { this.readyState = 1; setTimeout(() => this.onopen?.(new Event("open")), 0); } addEventListener() {} removeEventListener() {} close() { this.readyState = 2; } };
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const store = Object.fromEntries(Object.keys(COUNT).map(id => [id, history(id)]));
  const sends = [];
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = path.split("/")[3] ?? "";
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: convs });
    if (path.endsWith("/messages") && request.method() === "POST") {
      const body = request.postDataJSON();
      const list = store[conv];
      const seq = list[list.length - 1].seq + 1;
      const message = { id: `${conv}_sent${seq}`, conversation_id: conv, role: "user", content: body.content, seq, created_at: iso(seq * 30), client_message_id: body.client_message_id };
      list.push(message);
      sends.push(message);
      const run = { id: `run_sent_${seq}`, conversation_id: conv, bot_id: "bot_a", status: "queued", model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: message.id, queue_seq: 1, created_at: iso(seq * 30), updated_at: iso(seq * 30) };
      return json({ message, run, runs: [run] });
    }
    if (path.endsWith("/messages")) {
      // One page holding the whole history: the point is a long rendered list.
      return json({ messages: store[conv], drafts: [], has_more: false, event_cursor: 0 });
    }
    if (path.endsWith("/runs")) return json({ runs: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/tools")) return json(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) return json({ occurrences: [{ root_run_id: SCHED_RUN, schedule_id: SCHED_ID, scheduled_for_utc: iso(5000), title: "Synthetic nightly digest", description: "Synthetic nightly digest at 18:40", created_by: "user", kind: "daily", timezone: "UTC", daily_time: "18:40", occurrence_number: 3, execution_status: "done", status_run_id: SCHED_RUN, result_in_conversation: true }] });
    if (path.endsWith("/schedules")) return json({ schedules: [{ id: SCHED_ID, conversation_id: conv, bot_id: "bot_a", content: "x", kind: "daily", timezone: "UTC", next_at_utc: iso(9000), status: "active", created_at: iso(0), updated_at: iso(0), title: "Synthetic nightly digest", daily_time: "18:40" }] });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    if (path === "/api/secret-inputs") return json({ requests: [] });
    return json({});
  });
  await page.goto(origin + "/");
  await page.locator(".conversation-row").first().waitFor();
  return { page, context, errors, sends };
}
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
const box = page => page.locator(".composer textarea");
async function show(page, name, minBlocks) {
  await rowFor(page, name).first().click();
  await page.waitForFunction(({ name, minBlocks }) => document.querySelector(".chat-name-pill h1")?.textContent === name && document.querySelectorAll(".message-block").length >= minBlocks, { name, minBlocks }, { timeout: 15000 });
  await page.waitForTimeout(500);
}
const STORAGE_KEY = `tofi:composer-drafts:${origin}:${INSTANCE}`;
const stored = (page, id) => page.evaluate(([key, id]) => { try { return JSON.parse(sessionStorage.getItem(key) ?? "{}")[id]?.content ?? null; } catch { return null; } }, [STORAGE_KEY, id]);

const results = [];
const failures = [];
const check = async (name, run) => {
  if (process.env.COMPOSER_TYPING_ONLY && !name.includes(process.env.COMPOSER_TYPING_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

// Typing cost. `reduced` turns the avatars' always-on life animation off; animated, the page repaints every frame
// whatever the user types, so layouts are counted beyond one per frame.
async function measureTyping({ reduced }) {
  const { page, context, errors } = await open({ reduced });
  await show(page, "Synthetic Group", 100);
  const blocks = await page.locator(".message-block").count();
  const cdp = await context.newCDPSession(page);
  await cdp.send("Performance.enable");
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(m => [m.name, m.value]));
  await box(page).click();
  await page.keyboard.type("warm up", { delay: 5 });
  await box(page).fill("");
  await page.waitForTimeout(3000); // let late fetches, lazy chunks and the debounced flush finish
  await page.evaluate(() => { window.__frames = 0; const tick = () => { window.__frames++; requestAnimationFrame(tick); }; requestAnimationFrame(tick); });
  const text = "the quick brown fox jumps over lazy dogs".slice(0, 40);
  const frames0 = await page.evaluate(() => window.__frames);
  const before = await metrics();
  await page.keyboard.type(text, { delay: 15 });
  const after = await metrics();
  const frames = (await page.evaluate(() => window.__frames)) - frames0;
  const keys = text.length;
  const script = (after.ScriptDuration - before.ScriptDuration) * 1000 / keys;
  const layouts = (after.LayoutCount - before.LayoutCount) / keys;
  const styles = (after.RecalcStyleCount - before.RecalcStyleCount) / keys;
  const extra = (after.LayoutCount - before.LayoutCount - frames) / keys;
  console.log(`  ${blocks} message blocks${reduced ? ", reduced motion" : ", animated"}: script ${script.toFixed(2)} ms/key, layouts ${layouts.toFixed(2)}/key (${extra.toFixed(2)}/key beyond one per frame), style recalcs ${styles.toFixed(2)}/key`);
  const got = await box(page).inputValue();
  assert.equal(got, text, `typed text arrived intact (got ${JSON.stringify(got)})`);
  assert.deepEqual(errors, []);
  await context.close();
  return { script, layouts, extra };
}

await check("typing cost per keystroke is independent of conversation length", async () => {
  const { script, layouts } = await measureTyping({ reduced: true });
  assert.ok(script <= 4, `script time ${script.toFixed(2)} ms/key exceeds 4`);
  assert.ok(layouts <= 2, `layouts ${layouts.toFixed(2)}/key exceed 2`);
});

await check("typing cost stays low with the avatars' life animation running", async () => {
  const { script, extra } = await measureTyping({ reduced: false });
  assert.ok(script <= 4, `script time ${script.toFixed(2)} ms/key exceeds 4`);
  assert.ok(extra <= 2, `${extra.toFixed(2)} layouts/key beyond one per frame exceed 2`);
});

await check("the message list is not re-rendered by keystrokes", async () => {
  const { page, context } = await open();
  await show(page, "Synthetic Group", 100);
  // Reduced motion (see open) keeps the avatars' own animation out of the list's mutations.
  await page.evaluate(() => { window.__mut = 0; new MutationObserver(records => { window.__mut += records.length; }).observe(document.querySelector(".message-list"), { subtree: true, childList: true, attributes: true, characterData: true }); });
  await box(page).click();
  await page.keyboard.type("no list churn", { delay: 10 });
  await page.waitForTimeout(900);
  const mutations = await page.evaluate(() => window.__mut);
  assert.equal(mutations, 0, `message list mutated ${mutations} times while typing`);
  await context.close();
});

await check("drafts survive switching A -> B -> A and reach storage", async () => {
  const { page, context } = await open();
  await show(page, "Synthetic Group", 100);
  await box(page).click();
  await page.keyboard.type("alpha draft", { delay: 5 });
  // Switch immediately: the last characters must not be lost to the debounce.
  await show(page, "Ledger Cat", 5);
  assert.equal(await box(page).inputValue(), "", "other conversation starts empty");
  await box(page).click();
  await page.keyboard.type("beta draft", { delay: 5 });
  await show(page, "Synthetic Group", 100);
  assert.equal(await box(page).inputValue(), "alpha draft");
  await show(page, "Ledger Cat", 5);
  assert.equal(await box(page).inputValue(), "beta draft");
  assert.equal(await stored(page, "grp_1"), "alpha draft");
  await context.close();
});

await check("the saved draft is written within 1s of the last keystroke", async () => {
  const { page, context } = await open();
  await show(page, "Synthetic Group", 100);
  await box(page).click();
  await page.keyboard.type("persist me", { delay: 5 });
  await page.waitForTimeout(900);
  assert.equal(await stored(page, "grp_1"), "persist me", `stored ${JSON.stringify(await stored(page, "grp_1"))}`);
  // Page hide flushes unsaved characters immediately.
  await page.keyboard.type("!!", { delay: 0 });
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")));
  assert.equal(await stored(page, "grp_1"), "persist me!!");
  await context.close();
});

await check("Chinese IME composition yields exactly the composed text", async () => {
  const { page, context } = await open();
  await show(page, "Synthetic Group", 100);
  const cdp = await context.newCDPSession(page);
  await box(page).click();
  await page.evaluate(() => { window.__ime = { start: 0, end: 0, input: [] }; const t = document.querySelector(".composer textarea"); t.addEventListener("compositionstart", () => window.__ime.start++); t.addEventListener("compositionend", () => window.__ime.end++); t.addEventListener("input", () => window.__ime.input.push(t.value)); });
  await cdp.send("Input.imeSetComposition", { text: "n", selectionStart: 1, selectionEnd: 1 });
  await cdp.send("Input.imeSetComposition", { text: "ni", selectionStart: 2, selectionEnd: 2 });
  await cdp.send("Input.imeSetComposition", { text: "你", selectionStart: 1, selectionEnd: 1 });
  await page.waitForTimeout(600); // a debounced flush lands mid-composition
  await cdp.send("Input.insertText", { text: "你好" });
  await page.keyboard.type(" ok", { delay: 5 });
  await page.waitForTimeout(600);
  const value = await box(page).inputValue();
  const ime = await page.evaluate(() => window.__ime);
  assert.equal(value, "你好 ok", `composed text was ${JSON.stringify(value)} (inputs ${JSON.stringify(ime.input)})`);
  assert.equal(await stored(page, "grp_1"), "你好 ok");
  await context.close();
});

await check("Shift+Enter inserts a newline; Enter sends and clears the draft", async () => {
  const { page, context, sends } = await open();
  await show(page, "Synthetic Group", 100);
  await box(page).click();
  await page.keyboard.type("line one", { delay: 5 });
  await page.keyboard.press("Shift+Enter");
  await page.keyboard.type("line two", { delay: 5 });
  assert.equal(await box(page).inputValue(), "line one\nline two");
  assert.equal(sends.length, 0);
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector(".composer textarea").value === "", null, { timeout: 5000 });
  assert.equal(sends.length, 1);
  assert.equal(sends[0].content, "line one\nline two");
  await page.waitForTimeout(700);
  assert.ok(["", null].includes(await stored(page, "grp_1")), "stored draft cleared after send");
  // The cleared draft stays cleared after a round trip.
  await show(page, "Ledger Cat", 5);
  await show(page, "Synthetic Group", 100);
  assert.equal(await box(page).inputValue(), "");
  await context.close();
});

await check("@mention autocomplete still completes a member name", async () => {
  const { page, context } = await open();
  await show(page, "Synthetic Group", 100);
  await box(page).click();
  await page.keyboard.type("hi @Led", { delay: 5 });
  await page.locator(".mention-menu").waitFor();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector(".composer textarea").value === "hi @Ledger Cat ", null, { timeout: 3000 });
  await context.close();
});

await browser.close();
await server.close();
await rm(outDir, { recursive: true, force: true });
if (failures.length) { console.log(`composer typing checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`composer typing checks: PASS (${results.length} scenarios)`);
