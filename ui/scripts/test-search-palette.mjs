// Search palette, production bundle in a real browser, synthetic data (the API is answered in the page).
//   - the sidebar search field is gone; a magnifier button opens the palette, so does Cmd/Ctrl+K
//   - the palette is a lazy chunk portaled into #tofi-layers; focus lands in the input
//   - empty query: quick actions and recent chats; typing: chats by name and messages with the hit highlighted
//   - typing is debounced (one request for a burst) and a superseded request is aborted
//   - arrows move the selection, Enter opens the conversation and scrolls to the message (the jump flash is on it)
//   - Escape closes and gives focus back to the button; exit runs before the dialog is removed
//   - at 390 px it is a full-width sheet; dark mode keeps the text readable
//   - per-key cost with the palette open stays small
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:search-palette
// SEARCH_SHOTS=/dir keeps screenshots.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { createFixture } from "./lib/chat-fixture.mjs";

const shots = process.env.SEARCH_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const fx = await createFixture();
let failures = 0;
async function check(name, fn) {
  if (process.env.ONLY && !name.includes(process.env.ONLY)) return;
  try { await fn(); console.log(`ok - ${name}`); }
  catch (error) { failures += 1; console.log(`FAIL - ${name}\n  ${error.stack?.split("\n").slice(0, 4).join("\n  ") ?? error}`); }
}

// Two conversations: the empty one is the most recent (so it is the one open), the search finds text in the other.
const other = { id: "dm_b", kind: "dm", name: "Harbor Cat", bot_id: "bot_b", bot_ids: ["bot_b"], updated_at: fx.iso(900), read_seq: 99, user_visible: true };
const botB = { id: "bot_b", name: "Harbor Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_b", created_at: fx.iso(0) };
const seed = () => fx.history().map(m => m.seq === 5 || m.seq === 12 ? { ...m, content: `Remember the quokka budget for item ${m.seq}, then continue.` } : m);
const searches = [];
function routes() {
  searches.length = 0;
  return async (route, messages) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path === "/api/bots") { await json({ bots: [...fx.bots, botB] }); return true; }
    if (path === "/api/conversations") { await json({ conversations: [other, fx.dm] }); return true; }
    if (path === "/api/conversations/dm_b/messages") { await json({ messages: [], drafts: [], has_more: false, event_cursor: 0 }); return true; }
    if (path === "/api/search") {
      const q = (url.searchParams.get("q") ?? "").trim();
      searches.push(q);
      // A short query answers slowly, so a longer one can supersede it.
      if (q.length <= 3) await new Promise(resolve => setTimeout(resolve, 500));
      const lower = q.toLowerCase();
      const hits = messages.filter(m => m.content.toLowerCase().includes(lower)).sort((a, b) => b.seq - a.seq).slice(0, 20).map(m => {
        const at = m.content.toLowerCase().indexOf(lower);
        return { id: m.id, conversation_id: "dm_a", conversation_name: "Ledger Cat", conversation_kind: "dm", seq: m.seq, role: m.role, created_at: m.created_at, snippet: m.content.slice(Math.max(0, at - 20), at + 80), match_start: Math.min(at, 20), match_len: q.length };
      });
      await json({ query: q, chats: [], messages: hits }).catch(() => {});
      return true;
    }
    return false;
  };
}
const open = (options = {}) => fx.open({ route: routes(), messages: seed(), path: "/b/bot_b", waitFor: ".search-trigger", ...options });
const shot = async (page, name) => { if (shots) await page.screenshot({ path: join(shots, name) }); };

await check("the sidebar search field is gone and a magnifier button is in the header", async () => {
  const { page, context, errors } = await open();
  assert.equal(await page.locator(".contact-pane .search-box").count(), 0);
  const button = page.locator(".sidebar-actions .search-trigger");
  assert.equal(await button.count(), 1);
  assert.ok(await button.getAttribute("aria-label"));
  assert.equal(await page.locator(".search-palette").count(), 0, "nothing mounted before it is asked for");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("Ctrl+K opens the lazy palette in the layer root; empty state shows quick actions and recent chats", async () => {
  const { page, context } = await open();
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor();
  assert.equal(await page.evaluate(() => document.querySelector(".search-palette").closest("#tofi-layers") !== null), true, "portaled into #tofi-layers");
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute("role")), "combobox", "focus is in the input");
  const groups = await page.locator(".search-palette-label").allTextContents();
  assert.deepEqual(groups.map(g => g.toLowerCase()), ["quick actions", "recent chats"]);
  assert.equal(await page.locator(".search-palette-row.is-action").count(), 3);
  assert.deepEqual(await page.locator(".search-palette-row.is-chat .search-palette-title").allTextContents(), ["Harbor Cat", "Ledger Cat"]);
  assert.equal(await page.locator(".search-palette-row[aria-selected=true]").count(), 1);
  const z = await page.evaluate(() => getComputedStyle(document.querySelector(".search-palette-scrim")).zIndex);
  assert.equal(z, "80", "dialog layer");
  await shot(page, "palette-empty-desktop.png");
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor({ state: "detached" });
  await context.close();
});

await check("typing: debounced, the superseded request is aborted, hits are highlighted newest first", async () => {
  const { page, context } = await open();
  const aborted = [];
  page.on("requestfailed", request => { if (request.url().includes("/api/search")) aborted.push(request.url()); });
  await page.getByRole("button", { name: /search/i }).first().click();
  await page.locator(".search-palette").waitFor();
  await page.keyboard.type("quo", { delay: 20 });
  await page.waitForTimeout(260); // the 150 ms debounce has fired for "quo"; the server is still answering
  await page.keyboard.type("kka", { delay: 20 });
  await page.locator(".search-palette-row.is-message").first().waitFor();
  await page.waitForTimeout(300);
  assert.deepEqual(searches, ["quo", "quokka"], `one request per settled query, got ${JSON.stringify(searches)}`);
  assert.equal(aborted.length, 1, "the stale request was cancelled");
  const rows = page.locator(".search-palette-row.is-message");
  assert.equal(await rows.count(), 2);
  assert.match(await rows.nth(0).textContent(), /item 12/, "newest first");
  assert.equal((await rows.first().locator("mark").textContent()).toLowerCase(), "quokka");
  assert.match(await rows.first().locator(".search-palette-meta").textContent(), /Ledger Cat/);
  await shot(page, "palette-results-desktop.png");
  await context.close();
});

await check("arrows move the selection; Enter opens the conversation at the message", async () => {
  const { page, context } = await open();
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor();
  await page.keyboard.type("quokka", { delay: 10 });
  await page.locator(".search-palette-row.is-message").nth(1).waitFor();
  const active = () => page.evaluate(() => document.getElementById(document.activeElement.getAttribute("aria-activedescendant"))?.textContent ?? "");
  assert.match(await active(), /item 12/);
  await page.keyboard.press("ArrowDown");
  assert.match(await active(), /item 5/);
  await page.keyboard.press("ArrowDown");
  assert.match(await active(), /item 12/, "wraps");
  await page.keyboard.press("ArrowUp");
  assert.match(await active(), /item 5/);
  await page.evaluate(() => { window.__flash = false; new MutationObserver(() => { if (document.querySelector("[data-jump-target]")) window.__flash = true; }).observe(document.body, { subtree: true, attributes: true, attributeFilter: ["data-jump-target"] }); });
  await page.keyboard.press("Enter");
  await page.locator(".search-palette").waitFor({ state: "detached", timeout: 3000 });
  await page.waitForFunction(() => document.querySelector(".chat-name-pill h1")?.textContent === "Ledger Cat", null, { timeout: 5000 });
  await page.waitForFunction(() => {
    const el = document.querySelector('[data-message-id="m5"]');
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.top >= 0 && r.bottom <= innerHeight;
  }, null, { timeout: 8000 });
  assert.equal(await page.evaluate(() => window.__flash), true, "the jump flash was shown on the message");
  await shot(page, "palette-jumped-desktop.png");
  await context.close();
});

await check("Escape closes after the exit animation and returns focus to the button; Messages tab asks for a query", async () => {
  const { page, context } = await open();
  await page.locator(".search-trigger").click();
  await page.locator(".search-palette").waitFor();
  await page.getByRole("tab", { name: /messages/i }).click();
  assert.match(await page.locator(".search-palette-note").textContent(), /type/i);
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => document.querySelector(".search-palette")?.getAttribute("data-state") === "closing" || !document.querySelector(".search-palette"));
  await page.locator(".search-palette").waitFor({ state: "detached", timeout: 3000 });
  assert.equal(await page.evaluate(() => document.activeElement?.classList.contains("search-trigger")), true);
  await context.close();
});

await check("no results shows the empty message", async () => {
  const { page, context } = await open();
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor();
  await page.keyboard.type("zzzzqq", { delay: 10 });
  await page.locator(".search-palette-note", { hasText: "zzzzqq" }).waitFor();
  await shot(page, "palette-none-desktop.png");
  await context.close();
});

await check("dark mode", async () => {
  const { page, context } = await open();
  await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; });
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor();
  await page.keyboard.type("quokka", { delay: 10 });
  await page.locator(".search-palette-row.is-message").first().waitFor();
  const colors = await page.evaluate(() => { const s = getComputedStyle(document.querySelector(".search-palette")); return [s.backgroundColor, s.color]; });
  assert.notEqual(colors[0], colors[1]);
  await shot(page, "palette-results-dark.png");
  await context.close();
});

await check("390 px: a full-width sheet; a tap opens the message", async () => {
  const { page, context } = await open({ viewport: { width: 390, height: 844 }, path: "/", waitFor: ".search-trigger" });
  await page.locator(".search-trigger").click();
  await page.locator(".search-palette").waitFor();
  const box = await page.locator(".search-palette").boundingBox();
  assert.equal(Math.round(box.width), 390);
  assert.ok(box.height >= 800, `sheet height ${box.height}`);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "no horizontal scroll");
  await shot(page, "palette-empty-390.png");
  await page.locator(".search-palette input").fill("quokka");
  await page.locator(".search-palette-row.is-message").nth(1).waitFor();
  await shot(page, "palette-results-390.png");
  await page.locator(".search-palette-row.is-message").nth(1).click();
  await page.locator(".search-palette").waitFor({ state: "detached", timeout: 3000 });
  await page.waitForFunction(() => {
    const el = document.querySelector('[data-message-id="m5"]');
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.top >= 0 && r.bottom <= innerHeight;
  }, null, { timeout: 8000 });
  await context.close();
});

await check("per-key cost with the palette open stays small", async () => {
  const { page, context } = await open();
  await page.keyboard.press("Control+k");
  await page.locator(".search-palette").waitFor();
  await page.evaluate(() => { window.__ev = []; new PerformanceObserver(list => { for (const e of list.getEntries()) if (e.name === "keydown" || e.name === "input") window.__ev.push([e.duration, e.processingEnd - e.processingStart]); }).observe({ type: "event", durationThreshold: 16, buffered: true }); });
  await page.keyboard.type("remember the quokka budget", { delay: 25 });
  await page.waitForTimeout(400);
  const ev = await page.evaluate(() => window.__ev);
  const worst = Math.max(0, ...ev.map(e => e[0])), work = Math.max(0, ...ev.map(e => e[1]));
  console.log(`  per key: worst input-to-paint ${worst} ms, worst handler work ${work.toFixed(1)} ms (${ev.length} events over 16 ms)`);
  // Handler work is the app's cost; input-to-paint adds the browser's own paint (a full-window backdrop blur made it 72 ms).
  assert.ok(work < 20, `a key's handlers took ${work} ms`);
  assert.ok(worst < 90, `a key took ${worst} ms to paint`);
  await context.close();
});

await fx.close();
console.log(failures ? `search palette: ${failures} FAILED` : "search palette: PASS");
process.exit(failures ? 1 : 0);
