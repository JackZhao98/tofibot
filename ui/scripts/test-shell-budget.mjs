// Shell budgets, measured in a production build in a real browser (synthetic data; /api is answered in the page):
//  1. Panels are code-split: Settings, the Bot desktop, onboarding and the other on-demand panels are not in the entry chunks,
//     and the entry JS (gzip) stays at or under 80% of the pre-split size (394,757 bytes before -> 315,806 budget).
//  2. Opening Settings shows its frame in the first frame or two: first visible frame <= 32 ms (<= 100 ms at 4x CPU, Chromium),
//     with no frame over 50 ms in the next 200 ms. A cold chunk (slow network) still shows the frame at once, then the content.
//  3. The phone list/chat change is an iOS-style push: the chat slides in from the right over the list while the list shifts left
//     and dims; reversed on back; 280 ms in / 220 ms back (motion tokens panel-in / base); reduced motion switches at once;
//     at no sampled frame is a pane missing (no blank frames).
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:shell-budget
// SHELL_BUDGET_ENGINE=webkit runs the browser checks in WebKit. SHELL_BUDGET_DIST=/path/to/dist measures an existing build
// instead of building one; SHELL_BUDGET_ONLY=struct,panel,phone picks sections (both used to prove each check fails on the
// pre-change build).
import assert from "node:assert/strict";
import http from "node:http";
import zlib from "node:zlib";
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engineName = process.env.SHELL_BUDGET_ENGINE === "webkit" ? "webkit" : "chromium";
const engine = pw[engineName];

const only = (process.env.SHELL_BUDGET_ONLY || "struct,panel,phone").split(",");
// 80% of the pre-split size.
const EAGER_GZIP_BUDGET = Math.floor(394_757 * 0.8);
const ownDist = !process.env.SHELL_BUDGET_DIST;
const dist = process.env.SHELL_BUDGET_DIST || mkdtempSync(join(tmpdir(), "tofi-shell-budget-"));
if (ownDist) await build({ configFile: false, root: ui, plugins: [react()], build: { outDir: dist, emptyOutDir: true }, logLevel: "error" });

// ---- 1. bundle structure ----
const html = readFileSync(join(dist, "index.html"), "utf8");
const eager = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map(match => match[1]);
const gzip = eager.reduce((sum, file) => sum + zlib.gzipSync(readFileSync(join(dist, file)), { level: 9 }).length, 0);
const entryText = eager.map(file => readFileSync(join(dist, file), "utf8")).join("\n");
const chunks = name => [...entryText.matchAll(new RegExp(`assets/${name}-[\\w-]+\\.js`, "g"))].map(match => match[0]);
if (only.includes("struct")) {
for (const name of ["SettingsPanel", "BotDesktopPanel", "Onboarding", "ContextPanels", "WorkPanel", "TeamBoard"]) {
  assert.ok(chunks(name).length > 0, `${name} is a separate chunk that the entry only references dynamically`);
  assert.ok(!eager.some(file => file.includes(`/${name}-`)), `${name} is not an entry-time chunk`);
}
console.log(`entry JS: ${eager.length} files, ${gzip} bytes gzip (budget ${EAGER_GZIP_BUDGET})`);
assert.ok(gzip <= EAGER_GZIP_BUDGET, `entry JS must stay within its budget (80% of the pre-split size + 1.5 KB): ${gzip} > ${EAGER_GZIP_BUDGET}`);
}

// ---- browser harness ----
const mime = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".json": "application/json", ".woff2": "font/woff2", ".png": "image/png" };
let chunkDelay = 0;
const server = http.createServer((request, response) => {
  const path = new URL(request.url, "http://x").pathname;
  let file = join(dist, path);
  if (!existsSync(file) || statSync(file).isDirectory()) file = join(dist, "index.html");
  const send = () => { response.writeHead(200, { "content-type": mime[extname(file)] || "application/octet-stream" }); response.end(readFileSync(file)); };
  if (chunkDelay && /SettingsPanel-[\w-]+\.(js|css)$/.test(path)) setTimeout(send, chunkDelay); else send();
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const names = ["Ledger Cat", "Simile Cat", "Mocha Cat", "Pixel Cat", "Orbit Cat", "Nimbus Cat"];
const bots = names.map((name, i) => ({ id: `bot_${i}`, name, instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${i}`, created_at: iso(0) }));
const convs = bots.map((bot, i) => ({ id: `dm_${i}`, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: iso(100 - i), read_seq: 40, user_visible: true }));
const history = conv => Array.from({ length: 40 }, (_, i) => {
  const seq = i + 1; const user = seq % 3 === 1;
  return { id: `${conv}_m${seq}`, conversation_id: conv, seq, role: user ? "user" : "assistant", content: user ? `Question ${seq}: can you check the weekly ledger?` : `Synthetic ${conv} answer ${seq} reads the weekly entries and totals them against the ledger.\n\n- first item\n- second item with \`code\``, created_at: iso(seq * 30), ...(user ? {} : { run_id: `run_${conv}_${seq}`, sender_bot_id: `bot_${conv.split("_")[1]}`, sender_bot_name: names[Number(conv.split("_")[1])] }) };
});

async function open({ width = 1280, height = 860, reduced = false, touch = false, path = "/b/bot_0", cpu = 1 } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", hasTouch: touch, isMobile: touch && width < 600, deviceScaleFactor: 2 });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    window.EventSource = class { constructor(url) { this.url = url; setTimeout(() => this.onopen?.(), 0); } addEventListener() {} close() {} };
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = url.pathname.split("/")[3] ?? "";
    if (url.pathname.endsWith("/events") || url.pathname.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream" }, body: ": synthetic stream\n\n" });
    if (url.pathname === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (url.pathname === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (url.pathname === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (url.pathname === "/api/bots") return json({ bots });
    if (url.pathname === "/api/conversations") return json({ conversations: convs });
    if (url.pathname.endsWith("/messages")) return json({ messages: history(conv), drafts: [], has_more: false, event_cursor: 0 });
    if (url.pathname.endsWith("/runs")) return json({ runs: [] });
    if (url.pathname.endsWith("/tools")) return json(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (url.pathname.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (url.pathname === "/api/questions") return json({ questions: [] });
    if (url.pathname === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(origin + path);
  await page.locator(".conversation-row").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  if (cpu > 1 && engineName === "chromium") await (await context.newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: cpu });
  return { page, context, errors };
}
const settle = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
/** Timing checks run on a shared machine: a check that misses once is measured once more before it fails. */
async function attempt(label, run, tries = 2) {
  for (let i = 1; ; i++) {
    try { return await run(); } catch (error) { if (i >= tries || error?.code !== "ERR_ASSERTION") throw error; console.log(`${label}: missed (${error.message}); measuring again`); }
  }
}
const median = values => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)];

// ---- 2. Settings first frame ----
/** Click Settings; record every frame from the pointer-down: time, pane opacity, and whether the frame/skeleton/real shell exist. */
async function settingsOpen(page) {
  await page.evaluate(() => {
    window.__open = [];
    document.addEventListener("pointerdown", () => {
      const t0 = performance.now();
      const tick = () => {
        const now = performance.now();
        const pane = document.querySelector(".detail-pane");
        const style = pane && getComputedStyle(pane);
        window.__open.push({ t: now - t0, opacity: style ? Number(style.opacity) : 0, shown: style?.visibility === "visible", skeleton: !!document.querySelector(".settings-shell-skeleton"), real: !!document.querySelector(".settings-shell:not(.settings-shell-skeleton) .settings-page-body") });
        if (now - t0 < 900) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
    }, { capture: true, once: true });
  });
  await page.locator('[aria-label="Settings"]').first().click();
  await page.waitForFunction(() => window.__open.at(-1)?.t > 850, null, { timeout: 5000 });
  return page.evaluate(() => window.__open);
}
const firstVisible = frames => frames.find(frame => frame.shown && frame.opacity > 0.01)?.t ?? Infinity;
const worstGap = (frames, until) => { const inside = frames.filter(frame => frame.t <= until); let worst = 0; for (let i = 1; i < inside.length; i++) worst = Math.max(worst, inside[i].t - inside[i - 1].t); return worst; };

async function warmedSettings(cpu, budget) {
  const samples = [];
  for (let i = 0; i < 5; i++) {
    const { page, context, errors } = await open({ cpu });
    await page.waitForFunction(() => performance.getEntriesByType("resource").some(entry => /SettingsPanel-[\w-]+\.js/.test(entry.name)), null, { timeout: 6000 }).catch(() => {});
    await page.waitForTimeout(300);
    const frames = await settingsOpen(page);
    assert.ok(frames.some(frame => frame.real), "the Settings content mounts after the frame");
    assert.deepEqual(errors, [], "no page errors while opening Settings");
    samples.push({ visible: firstVisible(frames), worst: worstGap(frames, 200 * cpu), frames });
    await context.close();
  }
  const visible = median(samples.map(sample => sample.visible));
  console.log(`settings first visible frame x${cpu}: ${samples.map(sample => sample.visible.toFixed(1)).join(", ")} ms (median ${visible.toFixed(1)}, budget ${budget}); worst gap ${samples.map(sample => sample.worst.toFixed(0)).join(", ")} ms`);
  assert.ok(visible <= budget, `Settings first visible frame ${visible.toFixed(1)} ms must be <= ${budget} ms at ${cpu}x`);
  if (cpu === 1) assert.ok(median(samples.map(sample => sample.worst)) <= 50, `no frame over 50 ms while Settings opens, median worst gap ${median(samples.map(sample => sample.worst)).toFixed(0)} ms`);
}
if (only.includes("panel")) {
await attempt("settings x1", () => warmedSettings(1, 32));
if (engineName === "chromium") await attempt("settings x4", () => warmedSettings(4, 100));

// Cold chunk: the network answers the Settings code 350 ms late. The frame is there at once; the content follows.
{
  chunkDelay = 350;
  const { page, context, errors } = await open();
  const frames = await settingsOpen(page);
  chunkDelay = 0;
  const visible = firstVisible(frames);
  console.log(`settings cold chunk: first visible frame ${visible.toFixed(1)} ms; real content at ${frames.find(frame => frame.real)?.t.toFixed(0)} ms`);
  assert.ok(visible <= 40, `a cold chunk still shows the Settings frame at once, got ${visible.toFixed(1)} ms`);
  assert.ok(frames.find(frame => frame.shown && frame.skeleton), "the skeleton frame is what shows while the chunk loads");
  assert.ok(frames.some(frame => frame.real), "the content arrives once the chunk does");
  assert.deepEqual(errors, [], "no page errors on a cold chunk");
  await context.close();
}
}

// ---- 3. Phone push ----
/** Sample both panes on every frame from the tap until `ms` later. */
async function sampleSwap(page, action, ms = 520) {
  await page.evaluate(() => {
    const read = el => { const r = el.getBoundingClientRect(); return { x: r.x, w: r.width, v: getComputedStyle(el).visibility, tf: getComputedStyle(el).transform }; };
    window.__swap = { frames: [], runs: {}, ends: {} };
    for (const sel of [".chat-pane", ".contact-pane"]) {
      const el = document.querySelector(sel);
      el.addEventListener("transitionend", e => { if (e.target === el && e.propertyName === "transform") window.__swap.ends[sel] = e.elapsedTime * 1000; });
    }
    const start = performance.now();
    const tick = () => {
      const now = performance.now();
      const chat = document.querySelector(".chat-pane"), list = document.querySelector(".contact-pane");
      const dim = Number(getComputedStyle(list, "::after").opacity);
      window.__swap.frames.push({ t: now - start, chat: read(chat), list: read(list), dim });
      if (now - start < 700) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  await action();
  await page.waitForFunction(() => window.__swap.frames.at(-1)?.t > 650, null, { timeout: 5000 });
  return page.evaluate(() => window.__swap);
}
/** Time between the first and last sampled frame in which the chat is partway across. */
function moveSpan(swap, viewport) {
  const moving = swap.frames.filter(frame => frame.chat.x > 0.5 && frame.chat.x < viewport - 0.5);
  return moving.length ? moving.at(-1).t - moving[0].t : 0;
}
function checkCoverage(swap, label, viewport) {
  let moving = 0;
  for (const frame of swap.frames) {
    const chatRight = frame.chat.x + frame.chat.w;
    const chatOnScreen = frame.chat.x < viewport - 0.5;
    const listLeft = frame.list.x, listRight = frame.list.x + frame.list.w;
    if (chatOnScreen) assert.equal(frame.chat.v, "visible", `${label}: the chat is painted while any of it is on screen (t=${frame.t.toFixed(0)})`);
    if (chatOnScreen && frame.chat.x > 0.5) {
      moving++;
      assert.equal(frame.list.v, "visible", `${label}: the list is painted under the sliding chat (t=${frame.t.toFixed(0)})`);
      assert.ok(listRight >= frame.chat.x - 0.5, `${label}: the list reaches the chat's edge, no gap (t=${frame.t.toFixed(0)}: list right ${listRight.toFixed(1)}, chat left ${frame.chat.x.toFixed(1)})`);
      assert.ok(listLeft <= 0.5 && listLeft >= -0.23 * viewport - 1, `${label}: the list shifts left by at most 22% (t=${frame.t.toFixed(0)}: ${listLeft.toFixed(1)})`);
    }
    assert.ok(frame.chat.v === "visible" || frame.list.v === "visible", `${label}: a pane is painted on every frame (t=${frame.t.toFixed(0)})`);
  }
  return moving;
}
if (only.includes("phone")) {
{
  const viewport = 390;
  const { page, context, errors } = await open({ width: viewport, height: 844, touch: true, path: "/" });
  await page.waitForTimeout(600);
  const phoneInfo = await page.evaluate(() => ({ chat: getComputedStyle(document.querySelector(".chat-pane")).transform, list: getComputedStyle(document.querySelector(".contact-pane")).transform }));
  assert.ok(phoneInfo.chat !== "none", "the chat waits off to the right while the list shows");

  const into = await sampleSwap(page, () => page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap());
  const moving = checkCoverage(into, "list to chat", viewport);
  assert.ok(moving >= 6, `the chat is seen sliding for several frames, got ${moving}`);
  const mid = into.frames.find(frame => frame.chat.x > 0.2 * viewport && frame.chat.x < 0.8 * viewport);
  assert.ok(mid && mid.list.x < -2 && mid.dim > 0.05, "while the chat slides the list shifts left and is dimmed");
  const enter = into.ends[".chat-pane"];
  const span = moveSpan(into, viewport);
  console.log(`phone push in: transition ${enter} ms (token 280), frames moving ${moving}, seen moving for ${span.toFixed(0)} ms`);
  assert.equal(enter, 280, "entering takes the panel-in token (280 ms)");
  assert.equal(into.ends[".contact-pane"], 280, "the list shifts for the same 280 ms");
  assert.ok(span <= 280 + 20, `the push settles within its 280 ms, seen moving for ${span.toFixed(0)} ms`);
  assert.equal(into.frames.at(-1).chat.x, 0, "the chat lands at the left edge");
  assert.equal(into.frames.at(-1).chat.v, "visible");
  assert.equal(into.frames.at(-1).list.v, "hidden", "the list is released once covered");

  const back = await sampleSwap(page, () => page.locator(".back-button").tap());
  const movingBack = checkCoverage(back, "chat to list", viewport);
  assert.ok(movingBack >= 5, `the chat is seen sliding back for several frames, got ${movingBack}`);
  const leave = back.ends[".chat-pane"];
  const spanBack = moveSpan(back, viewport);
  console.log(`phone push back: transition ${leave} ms (token 220), frames moving ${movingBack}, seen moving for ${spanBack.toFixed(0)} ms`);
  assert.equal(leave, 220, "going back takes the base token (220 ms)");
  assert.equal(back.ends[".contact-pane"], 220, "the list returns over the same 220 ms");
  assert.ok(spanBack <= 220 + 20, `the push back settles within its 220 ms, seen moving for ${spanBack.toFixed(0)} ms`);
  assert.equal(back.frames.at(-1).list.x, 0, "the list returns to rest");
  assert.equal(back.frames.at(-1).dim, 0, "the dim is gone at rest");
  assert.equal(back.frames.at(-1).chat.v, "hidden", "the chat is released once off screen");
  assert.deepEqual(errors, [], "no page errors during the push");
  await context.close();
}
{
  const viewport = 390;
  const { page, context } = await open({ width: viewport, height: 844, touch: true, path: "/", reduced: true });
  await page.waitForTimeout(600);
  const into = await sampleSwap(page, () => page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap(), 300);
  const intermediate = into.frames.filter(frame => frame.chat.x > 0.5 && frame.chat.x < viewport - 0.5);
  assert.equal(intermediate.length, 0, `reduced motion switches panes without sliding, saw ${intermediate.length} intermediate frames`);
  assert.equal(into.frames.at(-1).chat.x, 0);
  assert.ok(into.frames.every(frame => frame.list.x === 0 || frame.list.v === "hidden"), "reduced motion never shifts the list");
  const back = await sampleSwap(page, () => page.locator(".back-button").tap(), 300);
  assert.equal(back.frames.filter(frame => frame.chat.x > 0.5 && frame.chat.x < viewport - 0.5).length, 0, "reduced motion goes back without sliding");
  await context.close();
}
if (engineName === "chromium") await attempt("phone push x4", async () => {
  const { page, context } = await open({ width: 390, height: 844, touch: true, path: "/", cpu: 4 });
  await page.waitForTimeout(800);
  // The conversation is opened once first, so the measured push is the motion and not the first load of a history.
  await page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap();
  await page.waitForTimeout(900);
  await page.locator(".back-button").tap();
  await page.waitForTimeout(900);
  const into = await sampleSwap(page, () => page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap(), 520);
  checkCoverage(into, "list to chat at 4x", 390);
  const gaps = into.frames.slice(1).map((frame, i) => frame.t - into.frames[i].t);
  const slow = gaps.filter(gap => gap > 25).length;
  console.log(`phone push at 4x CPU: frames over 25 ms: ${slow}`);
  assert.ok(slow <= 1, `at most one frame over 25 ms during the push at 4x, got ${slow}`);
  await context.close();
});
}

await browser.close();
server.close();
if (ownDist) rmSync(dist, { recursive: true, force: true });
console.log(`shell budget (${engineName}): PASS`);
