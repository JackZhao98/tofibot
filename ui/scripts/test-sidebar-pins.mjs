// Sidebar drag-to-reorder and pinned tiles, against a mocked API that stores the layout like the server does.
// Covers: drag reorder + persistence across reload, the pin drop zone (only while dragging), dropping pins, large tiles,
// tile reorder, unpin by dragging back and by the menu, keyboard moves, touch long-press at 390 px (and that a tap still
// opens the chat), reduced motion, and frame timing of a drag over 60 conversations.
// Synthetic data only. Screenshots go to SIDEBAR_PINS_SHOTS when set.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:sidebar-pins
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build, preview } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const outDir = await mkdtemp(join(tmpdir(), "tofi-sidebar-pins-"));
await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const server = await preview({ configFile: false, root: ui, plugins: [react()], build: { outDir }, preview: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
// SIDEBAR_PINS_ENGINE=webkit runs the same scenarios in WebKit (Safari's engine); frame timing needs CDP, so it is Chromium only.
const engine = process.env.SIDEBAR_PINS_ENGINE === "webkit" ? "webkit" : "chromium";
const browser = await pw[engine].launch({ headless: true, ...(engine === "chromium" && process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
console.log(`engine: ${engine}`);
const shots = process.env.SIDEBAR_PINS_SHOTS;
if (shots) await mkdir(shots, { recursive: true });

const INSTANCE = "00000000-0000-4000-8000-000000000002";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const pad = n => String(n).padStart(2, "0");
const COUNT = 60;
const bots = Array.from({ length: COUNT }, (_, i) => ({ id: `bot_${pad(i)}`, name: `Cat ${pad(i)}`, instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${pad(i)}`, created_at: iso(0) }));
// Cat 00 has the newest message, so the recency order is Cat 00, Cat 01, ...
const dms = bots.map((bot, i) => ({ id: bot.dm_conversation_id, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: iso(1000 - i), read_seq: 1, user_visible: true,
  last_message: { id: `m_${i}`, seq: 1, role: "assistant", content: `Synthetic note ${i}`, created_at: iso(1000 - i) } }));
const group = { id: "grp_1", kind: "group", name: "Synthetic Group", bot_ids: ["bot_00", "bot_01"], updated_at: iso(1000 - COUNT), read_seq: 1, user_visible: true, last_message: { id: "m_g", seq: 1, role: "assistant", content: "Synthetic group note", created_at: iso(1000 - COUNT) } };
const baseConvs = [...dms, group];
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function open({ reduced = false, viewport = { width: 1180, height: 820 }, touch = false, dms: seed } = {}) {
  const context = await browser.newContext({ viewport, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", ...(touch ? { hasTouch: true, isMobile: true } : {}) });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    window.EventSource = class { constructor() { this.readyState = 1; setTimeout(() => this.onopen?.(new Event("open")), 0); } addEventListener() {} removeEventListener() {} close() { this.readyState = 2; } };
  });
  const page = await context.newPage();
  const errors = [];
  if (process.env.DEBUG_TOUCH || process.env.DEBUG_FRAMES) page.on("console", m => console.log("  [page]", m.text()));
  page.on("pageerror", error => errors.push(error.message));
  // The mock keeps the layout the way the server does: 1-based pin slots and manual order slots.
  const layout = { pinned: [], sort: new Map() };
  const puts = [];
  const convs = seed ?? baseConvs;
  const listing = () => convs.map(c => {
    const pin = layout.pinned.indexOf(c.id) + 1;
    return { ...c, pinned: pin > 0, pin_order: pin, sort_order: layout.sort.get(c.id) ?? 0 };
  });
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = (body, status = 200) => route.fulfill({ status, json: body });
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations/order" && request.method() === "PUT") {
      const body = request.postDataJSON();
      puts.push(body);
      if (process.env.SIDEBAR_PINS_PUT_DELAY) await sleep(Number(process.env.SIDEBAR_PINS_PUT_DELAY));
      const known = new Set(convs.map(c => c.id));
      const all = [...body.pinned, ...body.order];
      if (new Set(all).size !== all.length || all.some(id => !known.has(id))) return json({ error: { code: "invalid_layout", message: "invalid" } }, 400);
      layout.pinned = [...body.pinned];
      body.order.forEach((id, index) => layout.sort.set(id, index + 1));
      return json({ layout: [] });
    }
    if (path === "/api/conversations") return json({ conversations: listing() });
    if (path.endsWith("/messages")) return json({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [] });
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
  await page.locator(".conversation-row").first().waitFor();
  // The drag engine is its own chunk, loaded when the browser is idle after the list mounts.
  await page.waitForFunction(() => document.querySelector(".contact-section")?.getAttribute("data-dnd-ready") === "1", null, { timeout: 8000 });
  await page.waitForTimeout(300);
  return { page, context, errors, puts, layout };
}

const names = page => page.locator(".contact-section .conversation-row .contact-name").allInnerTexts();
const pinnedNames = page => page.locator(".pin-tile .pin-tile-name").allInnerTexts();
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) }).first();
const tileFor = (page, name) => page.locator(".pin-tile").filter({ has: page.locator(".pin-tile-name", { hasText: name }) }).first();
const centre = async locator => { const b = await locator.boundingBox(); assert.ok(b, "element has a box"); return { x: b.x + b.width / 2, y: b.y + b.height / 2, box: b }; };
async function until(check, what, timeout = 5000) {
  const stop = Date.now() + timeout;
  while (Date.now() < stop) { if (await check()) return; await sleep(40); }
  throw new Error(`timed out waiting for ${what}`);
}
/** Mouse drag in small steps, optionally leaving the button down so the caller can look at the drag state. */
async function dragTo(page, from, to, { steps = 14, hold = false } = {}) {
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  for (let i = 1; i <= steps; i++) { await page.mouse.move(from.x + (to.x - from.x) * i / steps, from.y + (to.y - from.y) * i / steps); await sleep(12); }
  if (!hold) { await page.mouse.up(); }
}
const paneAttr = (page, name) => page.evaluate(attr => document.querySelector("[data-dnd-dropzone]")?.getAttribute(attr), name);
// The frame around the pinned area is always there once something is pinned; its hint shows only while a row is dragged.
const dropZoneVisible = page => page.locator(".pin-frame-hint").isVisible();
const live = page => page.locator(".contact-section [role=status]").innerText();
const settled = async page => { await page.waitForFunction(() => !document.querySelector(".is-dnd-dragging") && !document.querySelector("[data-dnd-dropzone][data-dnd]"), null, { timeout: 4000 }); await sleep(350); };

/** Samples the rect of [data-dnd-id=id] (and the running animations on it) every animation frame; call before the pointer is released. */
const sampleStart = (page, id) => page.evaluate(sampleId => {
  window.__samples = [];
  window.__upAt = -1;
  window.__sampling = true;
  window.addEventListener("pointerup", () => { if (window.__upAt < 0) window.__upAt = window.__samples.length; }, { capture: true, once: true });
  const t0 = performance.now();
  const tick = () => {
    const el = document.querySelector(`[data-dnd-id="${CSS.escape(sampleId)}"]`);
    const r = el?.getBoundingClientRect();
    const zone = document.querySelector(".pin-dropzone")?.getBoundingClientRect();
    window.__samples.push({ t: performance.now() - t0, rect: r ? { x: r.left, y: r.top, w: r.width, h: r.height } : null, running: el ? el.getAnimations().filter(a => a.playState === "running").length : 0, scroll: document.querySelector(".contact-section").scrollTop, zone: zone ? { top: zone.top, bottom: zone.bottom } : null });
    if (window.__sampling) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}, id);
const sampleStop = async page => { await page.evaluate(() => { window.__sampling = false; }); const { frames, upAt } = await page.evaluate(() => ({ frames: window.__samples, upAt: window.__upAt })); return Object.assign(frames, { upAt }); };
/** Release the button a few frames after the sampler started, so the frames before the release are on record. */
const releaseSampled = async (page, id) => { await sampleStart(page, id); await sleep(120); await page.mouse.up(); await sleep(900); return sampleStop(page); };
/** The settle must end where it ends: the last frame of the animation and the resting rect agree, and nothing steps afterwards. */
function assertNoJump(samples, what, { continuity = true } = {}) {
  const up = samples.upAt;
  assert.ok(up > 2, `${what}: the release was recorded (${up})`);
  // Continuity at the release: the first frame after it starts from where the lifted row was, not from its old slot.
  const beforeUp = samples.slice(0, up).filter(s => s.rect).at(-1);
  const afterUp = samples.slice(up).find(s => s.rect);
  const mid = r => ({ x: r.x + r.w / 2, y: r.y + r.h / 2 });
  const leap = Math.hypot(mid(afterUp.rect).x - mid(beforeUp.rect).x, mid(afterUp.rect).y - mid(beforeUp.rect).y);
  // The settle eases out of a standing start, so how far it may have travelled grows with the time between the two frames
  // (about 2.6 px per ms at its steepest over these distances); a start from the old slot is 100+ px at once.
  const elapsed = afterUp.t - beforeUp.t;
  // (A tile that lands above the scrolled viewport flies a long way off screen: its continuity is not visible, so not asserted.)
  if (continuity) assert.ok(leap <= 12 + 2.6 * elapsed, `${what}: the element leaps ${leap.toFixed(1)} px in ${elapsed.toFixed(0)} ms between its last lifted frame and the first frame after the release`);
  const have = samples.filter(s => s.rect);
  if (process.env.DEBUG_JUMP) console.log(`  [${what}] release at frame ${up}; before: ${samples.slice(Math.max(0, up - 3), up).map(s => s.rect && s.rect.y.toFixed(1)).join(" ")}`);
  if (process.env.DEBUG_JUMP) console.log(`  [${what}]`, have.map(s => `${s.t.toFixed(0)}:${s.rect.x.toFixed(1)},${s.rect.y.toFixed(1)},${s.rect.w.toFixed(0)}x${s.rect.h.toFixed(0)}${s.running ? "*" : ""}@${s.scroll}`).join(" "));
  assert.ok(have.length > 5, `${what}: frames were sampled (${have.length})`);
  const final = have.at(-1).rect;
  const animated = have.map((s, i) => s.running > 0 ? i : -1).filter(i => i >= 0);
  const lastAnimating = animated.length ? have[animated.at(-1)] : have[0];
  const gap = r => Math.max(Math.abs(r.x - final.x), Math.abs(r.y - final.y), Math.abs(r.w - final.w), Math.abs(r.h - final.h));
  assert.ok(gap(lastAnimating.rect) <= 0.5, `${what}: the last animated frame is ${gap(lastAnimating.rect).toFixed(2)} px from the resting rect (jump)`);
  const after = have.slice(animated.length ? animated.at(-1) : 0);
  for (let i = 1; i < after.length; i++) assert.ok(Math.max(Math.abs(after[i].rect.x - after[i - 1].rect.x), Math.abs(after[i].rect.y - after[i - 1].rect.y)) <= 0.5, `${what}: steps ${(after[i].rect.y - after[i - 1].rect.y).toFixed(2)} px at ${after[i].t.toFixed(0)} ms after the animation ended`);
  // No frame may sit further from the resting rect than the lift allows, and the path must be continuous: no frame-to-frame leap bigger than the whole travel.
  return have;
}

const results = [];
const failures = [];
const check = async (name, run) => {
  if (process.env.SIDEBAR_PINS_ONLY && !name.includes(process.env.SIDEBAR_PINS_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n").slice(0, 16).join("\n    ")}`); }
};

await check("dragging a row reorders the list, lifts it, and the order survives a reload", async () => {
  const { page, context, puts, errors } = await open();
  assert.deepEqual((await names(page)).slice(0, 4), ["Cat 00", "Cat 01", "Cat 02", "Cat 03"], "starts in recency order");
  const from = await centre(rowFor(page, "Cat 03"));
  // Lifting a row opens the pin zone above the list, which moves the rows down: aim at where Cat 00 is once the zone is open.
  await dragTo(page, from, { x: from.x, y: from.y - 20 }, { hold: true });
  await until(() => dropZoneVisible(page), "the drop zone");
  await sleep(300);
  const top = await centre(rowFor(page, "Cat 00"));
  await page.mouse.move(from.x, top.y - 12, { steps: 10 });
  // While held: the row is lifted (scale <= 1.02, shadow) and follows the pointer; neighbours have moved aside.
  const lift = await page.evaluate(() => {
    const el = document.querySelector(".is-dnd-dragging");
    const m = new DOMMatrixReadOnly(getComputedStyle(el).transform);
    return { scale: Math.hypot(m.a, m.b), shadow: getComputedStyle(el).boxShadow, label: el.querySelector(".contact-name").textContent };
  });
  assert.equal(lift.label, "Cat 03");
  assert.ok(lift.scale > 1.0 && lift.scale <= 1.0201, `lift scale ${lift.scale}`);
  assert.notEqual(lift.shadow, "none");
  await sleep(300);
  const moved = await page.evaluate(() => [...document.querySelectorAll(".conversation-row:not(.is-dnd-dragging)")].slice(0, 4).map(el => new DOMMatrixReadOnly(getComputedStyle(el).transform).m42));
  assert.ok(moved.slice(0, 3).every(y => y > 40), `the three rows above the pickup glide down one slot: ${moved}`);
  assert.ok(Math.abs(moved[3]) < 1, `rows below the pickup stay put: ${moved}`);
  if (shots) await page.screenshot({ path: join(shots, "drag-reorder.png") });
  await page.mouse.up();
  await settled(page);
  assert.deepEqual((await names(page)).slice(0, 4), ["Cat 03", "Cat 00", "Cat 01", "Cat 02"]);
  await until(() => puts.length === 1, "the layout save");
  assert.deepEqual(puts[0].pinned, []);
  assert.deepEqual(puts[0].order.slice(0, 4), ["dm_03", "dm_00", "dm_01", "dm_02"]);
  assert.equal(puts[0].order.length, COUNT + 1, "the whole list is numbered, not just the moved row");
  await page.reload();
  await page.locator(".conversation-row").first().waitFor();
  assert.deepEqual((await names(page)).slice(0, 4), ["Cat 03", "Cat 00", "Cat 01", "Cat 02"], "order comes back from the server");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("a click that does not drag still opens the chat, and a drag does not", async () => {
  const { page, context } = await open();
  await rowFor(page, "Cat 02").click();
  await page.waitForFunction(() => document.querySelector(".chat-name-pill h1")?.textContent === "Cat 02", null, { timeout: 5000 });
  const from = await centre(rowFor(page, "Cat 05"));
  await dragTo(page, from, { x: from.x, y: from.y - 130 });
  await settled(page);
  assert.equal(await page.locator(".chat-name-pill h1").innerText(), "Cat 02", "the drag did not open Cat 05");
  await context.close();
});

await check("the drop zone exists only while a row is dragged", async () => {
  const { page, context } = await open();
  assert.equal(await dropZoneVisible(page), false, "hidden at rest");
  const from = await centre(rowFor(page, "Cat 02"));
  await dragTo(page, from, { x: from.x, y: from.y + 60 }, { hold: true });
  assert.equal(await paneAttr(page, "data-dnd"), "row");
  await until(() => dropZoneVisible(page), "the drop zone to show");
  if (shots) await page.screenshot({ path: join(shots, "drop-zone-idle.png") });
  const zone = await centre(page.locator(".pin-frame"));
  await page.mouse.move(zone.x, zone.y, { steps: 8 });
  await until(async () => (await paneAttr(page, "data-dnd-over")) === "pin", "the zone to highlight");
  if (shots) await page.screenshot({ path: join(shots, "drop-zone-armed.png") });
  await page.mouse.move(from.x, from.y + 200, { steps: 8 });
  await until(async () => (await paneAttr(page, "data-dnd-over")) === "", "the highlight to clear");
  await page.keyboard.press("Escape");
  await settled(page);
  assert.equal(await dropZoneVisible(page), false, "hidden again after the drag ends");
  assert.deepEqual((await names(page)).slice(0, 3), ["Cat 00", "Cat 01", "Cat 02"], "Escape cancels the drag");
  await context.close();
});

await check("dropping on the zone pins: large tile with the cat and the name, row leaves the list", async () => {
  const { page, context, puts } = await open();
  const from = await centre(rowFor(page, "Cat 02"));
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x, from.y - 30, { steps: 5 });
  await until(() => dropZoneVisible(page), "the drop zone");
  const zone = await centre(page.locator(".pin-frame"));
  await page.mouse.move(zone.x, zone.y, { steps: 10 });
  await page.mouse.up();
  await settled(page);
  await until(() => puts.length === 1, "the layout save");
  assert.deepEqual(puts[0].pinned, ["dm_02"]);
  assert.ok(!puts[0].order.includes("dm_02"), "a pinned conversation is not in the list order");
  assert.deepEqual(await pinnedNames(page), ["Cat 02"]);
  assert.ok(!(await names(page)).includes("Cat 02"), "no duplicate row");
  const tile = tileFor(page, "Cat 02");
  const art = await tile.locator(".avatar").boundingBox();
  assert.ok(art.width >= 60 && art.height >= 60, `avatar is large (${art.width}x${art.height})`);
  assert.ok(await tile.locator(".avatar svg").count() > 0, "the cat is drawn");
  assert.ok(art.height > (await rowFor(page, "Cat 00").locator(".avatar").boundingBox()).height + 10, "bigger than the row avatar");
  assert.equal(await tile.getAttribute("aria-label"), "Cat 02");
  if (shots) await page.screenshot({ path: join(shots, "pinned-one.png") });
  await tile.click();
  await page.waitForFunction(() => document.querySelector(".chat-name-pill h1")?.textContent === "Cat 02", null, { timeout: 5000 });
  assert.ok((await tile.getAttribute("class")).includes("active"), "clicking a tile opens it and marks it current");
  await page.reload();
  await page.locator(".pin-tile").first().waitFor();
  assert.deepEqual(await pinnedNames(page), ["Cat 02"], "pins survive a reload");
  await context.close();
});

async function pinBy(page, name) {
  const from = await centre(rowFor(page, name));
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x, from.y - 30, { steps: 4 });
  await until(() => dropZoneVisible(page), "the drop zone");
  const zone = await centre(page.locator(".pin-frame"));
  await page.mouse.move(zone.x, zone.y, { steps: 8 });
  if (process.env.DEBUG_PIN) console.log("  [pinBy]", name, JSON.stringify(zone), await paneAttr(page, "data-dnd-over"), await page.evaluate(() => JSON.stringify(document.querySelector(".pin-dropzone").getBoundingClientRect())));
  await page.mouse.up();
  await settled(page);
}

await check("tiles reorder among themselves; dragging a tile back into the list unpins it at that spot", async () => {
  const { page, context, puts } = await open();
  await pinBy(page, "Cat 01");
  await pinBy(page, "Cat 04");
  await pinBy(page, "Cat 06");
  assert.deepEqual(await pinnedNames(page), ["Cat 01", "Cat 04", "Cat 06"]);
  const last = await centre(tileFor(page, "Cat 06"));
  const first = await centre(tileFor(page, "Cat 01"));
  await dragTo(page, last, { x: first.x - 20, y: first.y }, { hold: true });
  assert.equal(await paneAttr(page, "data-dnd"), "tile");
  assert.equal(await dropZoneVisible(page), false, "the pin zone is for rows, not tiles");
  await page.mouse.up();
  await settled(page);
  assert.deepEqual(await pinnedNames(page), ["Cat 06", "Cat 01", "Cat 04"]);
  await until(() => puts.at(-1)?.pinned.join() === "dm_06,dm_01,dm_04", "tile order saved");
  // Drag the middle tile down into the list between Cat 03 and Cat 05.
  const tile = await centre(tileFor(page, "Cat 01"));
  const between = await centre(rowFor(page, "Cat 03"));
  await dragTo(page, tile, { x: between.x, y: between.y + 24 }, { hold: true });
  if (shots) await page.screenshot({ path: join(shots, "tile-drag-out.png") });
  await page.mouse.up();
  await settled(page);
  assert.deepEqual(await pinnedNames(page), ["Cat 06", "Cat 04"]);
  const list = await names(page);
  assert.equal(list[list.indexOf("Cat 03") + 1], "Cat 01", `Cat 01 sits right after Cat 03: ${list.slice(0, 6)}`);
  assert.deepEqual(puts.at(-1).pinned, ["dm_06", "dm_04"]);
  await context.close();
});

await check("the pinned area is the drop frame: below the search bar, around every tile, and nothing covers it at any scroll position", async () => {
  for (const pins of [0, 3]) for (const where of ["top", "middle", "bottom"]) {
    const { page, context } = await open();
    for (const name of ["Cat 01", "Cat 04", "Cat 06"].slice(0, pins)) await pinBy(page, name);
    await page.evaluate(w => { const el = document.querySelector(".contact-section"); el.scrollTop = w === "top" ? 0 : w === "middle" ? (el.scrollHeight - el.clientHeight) / 2 : el.scrollHeight; }, where);
    await sleep(250);
    const box = await page.locator(".contact-section").boundingBox();
    let pick = null;
    for (const n of await names(page)) { const b = await rowFor(page, n).boundingBox(); if (b && b.y > box.y + 10 && b.y + b.height < box.y + box.height - 10) { pick = n; break; } }
    assert.ok(pick, "a row is on screen");
    const from = await centre(rowFor(page, pick));
    await dragTo(page, from, { x: from.x, y: from.y + 8 }, { hold: true, steps: 4 });
    await until(() => dropZoneVisible(page), "the hint inside the frame");
    await sleep(500);
    const probe = await page.evaluate(() => {
      const box = el => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height }; };
      const frameEl = document.querySelector(".pin-frame");
      const frame = box(frameEl);
      const inside = el => frameEl.contains(el) || el.contains(frameEl);
      const hit = (a, b) => a.top < b.bottom - 0.5 && a.bottom > b.top + 0.5 && a.left < b.right - 0.5 && a.right > b.left + 0.5;
      const tiles = [...document.querySelectorAll(".pin-tile")].map(box);
      const regions = [["frame", frame], ...tiles.map((t, i) => ["tile " + i, t])];
      const offenders = [];
      // Anything in the sidebar that is painted over a region: elements inside the frame do not count; nothing outside it may overlap it.
      for (const el of document.querySelectorAll(".contact-pane *")) {
        if (inside(el) || el.classList.contains("is-dnd-dragging") || el.closest(".is-dnd-dragging")) continue;
        const cs = getComputedStyle(el);
        if (cs.display === "none" || cs.visibility === "hidden" || cs.opacity === "0") continue;
        const r = box(el);
        if (!r.width || !r.height) continue;
        for (const [name, region] of regions) if (hit(r, region)) offenders.push(`${el.className || el.tagName} over ${name}`);
      }
      // What is actually on top at points of the frame and at each tile's centre (this sees positioned layers and blurs, not only boxes).
      const topmost = [];
      const points = [[frame.left + frame.width / 2, frame.top + 6], [frame.left + 6, frame.top + frame.height / 2], [frame.right - 20, frame.bottom - 20], ...tiles.map(t => [t.left + t.width / 2, t.top + t.height / 2])];
      for (const [x, y] of points) { const hitEl = document.elementFromPoint(x, y); if (!hitEl || !frameEl.contains(hitEl)) topmost.push(`${hitEl?.className || hitEl?.tagName} at ${Math.round(x)},${Math.round(y)}`); }
      const search = box(document.querySelector(".sidebar-top"));
      const scroller = document.querySelector(".contact-section");
      const ghost = document.querySelector(".is-dnd-dragging");
      const hint = box(document.querySelector(".pin-frame-hint"));
      return { frame, search, tiles, offenders, topmost, scrollTop: scroller.scrollTop, ghostZ: getComputedStyle(ghost).zIndex, frameZ: getComputedStyle(frameEl).zIndex, hint, hintDisplay: getComputedStyle(document.querySelector(".pin-frame-hint")).display, frameTouches: frame.height };
    });
    const what = `${pins} pins, scrolled ${where}`;
    assert.equal(probe.scrollTop, 0, `${what}: lifting a row takes the list to the top, where the whole frame is`);
    assert.ok(probe.frame.top >= probe.search.bottom - 0.5, `${what}: the frame top ${probe.frame.top} is at or below the sidebar header bottom ${probe.search.bottom}`);
    assert.equal(probe.tiles.length, pins, `${what}: every pinned tile is drawn`);
    for (const [i, t] of probe.tiles.entries()) assert.ok(t.left >= probe.frame.left - 0.5 && t.right <= probe.frame.right + 0.5 && t.top >= probe.frame.top - 0.5 && t.bottom <= probe.frame.bottom + 0.5, `${what}: tile ${i} is inside the frame ${JSON.stringify(t)} vs ${JSON.stringify(probe.frame)}`);
    assert.ok(probe.hint.top >= probe.frame.top - 0.5 && probe.hint.bottom <= probe.frame.bottom + 0.5, `${what}: the hint is inside the frame`);
    if (pins === 0) assert.ok(probe.frame.height >= 100 && probe.frame.height <= 140, `${what}: the empty frame is about one tile row tall (${probe.frame.height})`);
    assert.deepEqual(probe.offenders, [], `${what}: nothing is laid over the frame or the tiles`);
    assert.deepEqual(probe.topmost, [], `${what}: the frame and the tiles are topmost`);
    assert.equal(probe.ghostZ, "110", `${what}: the ghost is on --z-drag`);
    assert.ok(probe.frameZ === "auto" || Number(probe.frameZ) < 110, `${what}: the frame is under the ghost`);
    if (shots) await page.screenshot({ path: join(shots, `frame-${pins}-pins-${where}.png`) });
    await page.keyboard.press("Escape");
    await settled(page);
    await context.close();
  }
});

await check("a drop lands where it ends: no step between the last animated frame and the resting rect (reorder, pin, unpin)", async () => {
  const { page, context } = await open();
  // Reorder inside the list.
  let from = await centre(rowFor(page, "Cat 04"));
  let top = await centre(rowFor(page, "Cat 01"));
  await dragTo(page, from, { x: from.x + 6, y: top.y - 4 }, { hold: true });
  await sleep(250);
  let frames = assertNoJump(await releaseSampled(page, "dm_04"), "reorder");
  assert.ok(frames.some(f => f.running > 0), "reorder: the settle animated");
  await settled(page);
  // Pin: the row becomes the large tile.
  from = await centre(rowFor(page, "Cat 03"));
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x, from.y - 30, { steps: 4 });
  await until(() => dropZoneVisible(page), "the drop zone");
  const zone = await centre(page.locator(".pin-frame"));
  await page.mouse.move(zone.x, zone.y, { steps: 8 });
  await sleep(250);
  frames = assertNoJump(await releaseSampled(page, "dm_03"), "pin");
  await settled(page);
  // Reorder inside the list again, now that a tile is above it.
  from = await centre(rowFor(page, "Cat 06"));
  top = await centre(rowFor(page, "Cat 02"));
  await dragTo(page, from, { x: from.x, y: top.y - 4 }, { hold: true });
  await sleep(250);
  assertNoJump(await releaseSampled(page, "dm_06"), "reorder under a tile");
  await settled(page);
  // Unpin by dragging the tile into the list.
  const tile = await centre(tileFor(page, "Cat 03"));
  const between = await centre(rowFor(page, "Cat 05"));
  await dragTo(page, tile, { x: between.x, y: between.y + 10 }, { hold: true });
  await sleep(250);
  assertNoJump(await releaseSampled(page, "dm_03"), "unpin");
  await context.close();
});

await check("a drop lands where it ends while the list is scrolled (reorder, then pin)", async () => {
  const { page, context } = await open();
  await page.evaluate(() => { document.querySelector(".contact-section").scrollTop = 400; });
  await sleep(200);
  const rows = await page.locator(".contact-section .conversation-row .contact-name").allInnerTexts();
  const box = await page.locator(".contact-section").boundingBox();
  const vis = [];
  for (const n of rows) { const b = await rowFor(page, n).boundingBox(); if (b && b.y > box.y + 20 && b.y + b.height < box.y + box.height - 20) vis.push(n); }
  const pick = vis[4], dest = vis[1];
  const from = await centre(rowFor(page, pick));
  const top = await centre(rowFor(page, dest));
  const id = "dm_" + pick.slice(4);
  await dragTo(page, from, { x: from.x + 6, y: top.y - 4 }, { hold: true });
  await sleep(250);
  assertNoJump(await releaseSampled(page, id), "scrolled reorder");
  await settled(page);
  // pin from a scrolled list: the lift takes the list to the top, so scroll down again and pick a row that is on screen
  await page.evaluate(() => { document.querySelector(".contact-section").scrollTop = 300; });
  await sleep(200);
  let target = null;
  for (const n of await names(page)) { const b = await rowFor(page, n).boundingBox(); if (b && b.y > box.y + 20 && b.y + b.height < box.y + box.height - 20) { target = n; break; } }
  const p2 = await centre(rowFor(page, target));
  await page.mouse.move(p2.x, p2.y); await page.mouse.down(); await page.mouse.move(p2.x, p2.y - 30, { steps: 4 });
  await until(() => dropZoneVisible(page), "zone");
  const z = await centre(page.locator(".pin-frame"));
  await page.mouse.move(z.x, z.y, { steps: 8 });
  await sleep(250);
  assertNoJump(await releaseSampled(page, "dm_" + target.slice(4)), "scrolled pin", { continuity: false });
  await context.close();
});

await check("the context menu pins and unpins, for rows and tiles", async () => {
  const { page, context, puts } = await open();
  await rowFor(page, "Cat 03").click({ button: "right" });
  await page.getByRole("menuitem", { name: "Pin", exact: true }).click();
  await until(() => puts.length === 1, "save");
  assert.deepEqual(await pinnedNames(page), ["Cat 03"]);
  assert.deepEqual(puts[0].pinned, ["dm_03"]);
  await tileFor(page, "Cat 03").click({ button: "right" });
  await page.getByRole("menuitem", { name: "Unpin", exact: true }).click();
  await until(() => puts.length === 2, "save");
  assert.deepEqual(await pinnedNames(page), []);
  assert.equal((await names(page))[0], "Cat 03", "an unpinned conversation returns at the top of the list");
  await context.close();
});

await check("a pinned group shows the group mark (member heads), not a full cat", async () => {
  const { page, context } = await open();
  await rowFor(page, "Synthetic Group").scrollIntoViewIfNeeded();
  await rowFor(page, "Synthetic Group").click({ button: "right" });
  await page.getByRole("menuitem", { name: "Pin", exact: true }).click();
  await page.locator(".pin-tile-group").waitFor();
  assert.deepEqual(await pinnedNames(page), ["Synthetic Group"]);
  // The group mark is a cluster of static heads (GroupAvatar); no cat is drawn outside it.
  await page.locator(".pin-tile-group .group-head").first().waitFor();
  assert.equal(await page.locator(".pin-tile-group .group-head").count() > 0, true, "the group draws member heads");
  assert.equal(await page.locator(".pin-tile .tofi-avatar").evaluateAll(nodes => nodes.filter(node => !node.closest(".pin-tile-group")).length), 0, "no full cat for a group");
  await context.close();
});

await check("keyboard: Alt+Arrow moves a row and a tile, focus stays, and the move is announced", async () => {
  const { page, context, puts } = await open();
  await rowFor(page, "Cat 01").focus();
  await page.keyboard.press("Alt+ArrowDown");
  await until(() => puts.length === 1, "save");
  assert.deepEqual((await names(page)).slice(0, 3), ["Cat 00", "Cat 02", "Cat 01"]);
  assert.equal(await page.evaluate(() => document.activeElement?.querySelector(".contact-name")?.textContent), "Cat 01", "focus stays on the moved row");
  assert.match(await live(page), /Cat 01 moved to position 3 of \d+/);
  await page.keyboard.press("Alt+ArrowUp");
  await until(() => puts.length === 2, "save");
  assert.deepEqual((await names(page)).slice(0, 3), ["Cat 00", "Cat 01", "Cat 02"]);
  // The first row cannot move up: nothing is saved.
  await rowFor(page, "Cat 00").focus();
  await page.keyboard.press("Alt+ArrowUp");
  await sleep(200);
  assert.equal(puts.length, 2);
  // Pin from the keyboard through the menu, then move the tile.
  await rowFor(page, "Cat 05").focus();
  await page.keyboard.press("Shift+F10");
  await page.getByRole("menuitem", { name: "Pin", exact: true }).waitFor();
  assert.equal(await page.evaluate(() => document.activeElement?.textContent), "Pin", "the menu takes focus");
  await page.keyboard.press("Enter");
  await until(() => puts.length === 3, "pin saved");
  await rowFor(page, "Cat 06").focus();
  await page.keyboard.press("Shift+F10");
  await page.keyboard.press("Enter");
  await until(() => puts.length === 4, "second pin saved");
  assert.deepEqual(await pinnedNames(page), ["Cat 05", "Cat 06"]);
  await tileFor(page, "Cat 06").focus();
  await page.keyboard.press("Alt+ArrowLeft");
  await until(() => puts.length === 5, "tile move saved");
  assert.deepEqual(await pinnedNames(page), ["Cat 06", "Cat 05"]);
  assert.equal(await page.evaluate(() => document.activeElement?.querySelector(".pin-tile-name")?.textContent), "Cat 06");
  assert.match(await live(page), /Cat 06 moved to position 1 of 2/);
  await context.close();
});

await check("announcements: lift, drop and pin are spoken", async () => {
  const { page, context, puts } = await open();
  const from = await centre(rowFor(page, "Cat 04"));
  await dragTo(page, from, { x: from.x, y: from.y }, { hold: true, steps: 1 });
  await page.mouse.move(from.x, from.y + 10);
  await until(async () => /Picked up Cat 04\. Position 5 of /.test(await live(page)), "pickup announcement");
  await page.mouse.move(from.x, from.y - 140, { steps: 10 });
  await page.mouse.up();
  await settled(page);
  await until(async () => /Cat 04 moved to position \d of /.test(await live(page)), "drop announcement");
  await pinBy(page, "Cat 07");
  await until(async () => /Cat 07 pinned\. Position 1 of 1/.test(await live(page)), "pin announcement");
  assert.ok(puts.length >= 2);
  await context.close();
});

await check("reduced motion: neighbours do not glide, but the row still follows the pointer", async () => {
  for (const reduced of [false, true]) {
    const { page, context } = await open({ reduced });
    const from = await centre(rowFor(page, "Cat 03"));
    await dragTo(page, from, { x: from.x, y: from.y - 100 }, { hold: true });
    const probe = await page.evaluate(() => {
      const other = document.querySelector(".conversation-row:not(.is-dnd-dragging)");
      const drag = document.querySelector(".is-dnd-dragging");
      return { neighbour: getComputedStyle(other).transitionDuration, dragged: new DOMMatrixReadOnly(getComputedStyle(drag).transform).m42 };
    });
    const longest = Math.max(...probe.neighbour.split(",").map(part => parseFloat(part) * (part.includes("ms") ? 1 : 1000)));
    if (reduced) assert.ok(longest <= 1, `no glide under reduced motion (${probe.neighbour})`);
    else assert.ok(longest >= 100, `neighbours glide (${probe.neighbour})`);
    assert.ok(probe.dragged < -50, "the dragged row follows the pointer either way");
    await page.mouse.up();
    // The drop settles the row with a glide, unless motion is reduced: then it lands at once. Reduced motion still runs
    // every CSS transition for 0.01 ms (interaction-system.css switch-off); whether one is caught in this frame is timing,
    // so only motion longer than 1 ms counts as a glide.
    const gliding = await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => resolve(document.getAnimations().filter(a => a.effect?.target?.matches?.("[data-dnd-id]") && Number(a.effect.getComputedTiming().duration) > 1).length))));
    if (reduced) assert.equal(gliding, 0, "no settle animation under reduced motion");
    else assert.ok(gliding >= 1, "the dropped row glides into place");
    await settled(page);
    await context.close();
  }
});

for (const rate of engine === "chromium" ? [1, 4] : []) await check(`frame timing: a drag across 60 conversations keeps animation frames on budget (${rate}x CPU)`, async () => {
  const { page, context } = await open({ viewport: { width: 1180, height: 760 } });
  const cdp = await context.newCDPSession(page);
  await cdp.send("Performance.enable");
  if (rate > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate });
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(m => [m.name, m.value]));
  await page.evaluate(() => {
    window.__frames = [];
    window.__long = 0;
    try { new PerformanceObserver(list => { window.__long += list.getEntries().length; }).observe({ entryTypes: ["longtask"] }); } catch {}
    let last = performance.now();
    const tick = now => { window.__frames.push(now - last); last = now; requestAnimationFrame(tick); };
    requestAnimationFrame(tick);
  });
  await sleep(600);
  await page.evaluate(() => { window.__frames.length = 0; window.__long = 0; });
  // Idle baseline: the page's own cost per frame with nothing dragged (the avatars breathe, so it is not zero).
  const idle0 = await metrics();
  await sleep(700);
  const idle1 = await metrics();
  const idleFrames = await page.evaluate(() => window.__frames.length);
  const idleLayouts = (idle1.LayoutCount - idle0.LayoutCount) / idleFrames;
  const idleScript = (idle1.ScriptDuration - idle0.ScriptDuration) * 1000 / idleFrames;
  await page.evaluate(() => { window.__frames.length = 0; window.__long = 0; });
  const before = await metrics();
  const from = await centre(rowFor(page, "Cat 02"));
  const pane = await page.locator(".contact-section").boundingBox();
  // Down through the visible rows and into the bottom auto-scroll band, then back up past the top: both scroll directions.
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  for (let i = 1; i <= 90; i++) { await page.mouse.move(from.x, from.y + (pane.y + pane.height - 6 - from.y) * i / 90); await sleep(8); }
  await sleep(500);
  for (let i = 1; i <= 90; i++) { await page.mouse.move(from.x, pane.y + pane.height - 6 - (pane.height - 70) * i / 90); await sleep(8); }
  const after = await metrics();
  const frames = await page.evaluate(() => window.__frames.slice());
  const long = await page.evaluate(() => window.__long);
  await page.mouse.up();
  await settled(page);
  if (process.env.DEBUG_FRAMES) console.log("  slow frames at index", frames.map((ms, i) => ms > 30 ? `${i}:${ms.toFixed(0)}` : "").filter(Boolean).join(" "), "of", frames.length);
  const sorted = [...frames].sort((a, b) => a - b);
  const p95 = sorted[Math.floor(sorted.length * 0.95)];
  const worst = sorted.at(-1);
  const script = (after.ScriptDuration - before.ScriptDuration) * 1000 / frames.length;
  const layouts = (after.LayoutCount - before.LayoutCount) / frames.length;
  console.log(`  ${rate}x CPU, ${frames.length} frames over ${COUNT + 1} conversations: p95 ${p95.toFixed(1)} ms, worst ${worst.toFixed(1)} ms, ${long} long tasks, script ${script.toFixed(2)} ms/frame (idle ${idleScript.toFixed(2)}), layouts ${layouts.toFixed(2)}/frame (idle ${idleLayouts.toFixed(2)})`);
  assert.ok(frames.length > 60, "enough frames were observed");
  assert.ok(p95 <= (rate === 1 ? 20 : 34), `p95 frame ${p95.toFixed(1)} ms is within budget`);
  // At 4x CPU the pickup (measuring 61 rows, restyling them once) may cost a single frame or two; nothing after it may.
  assert.ok(worst < (rate === 1 ? 50 : 100), `no frame over ${rate === 1 ? 50 : 100} ms (worst ${worst.toFixed(1)})`);
  assert.ok(long <= (rate === 1 ? 0 : 1), `${long} long tasks during the drag`);
  assert.ok(sorted.filter(ms => ms > 34).length <= (rate === 1 ? 0 : 6), "dropped frames are rare, not sustained");
  assert.ok(script < 4 * rate, `script ${script.toFixed(2)} ms per frame`);
  assert.ok(layouts <= idleLayouts + 1.5, `a drag adds at most a layout or so per frame to the idle ${idleLayouts.toFixed(2)} (got ${layouts.toFixed(2)})`);
  await context.close();
});

// ---- touch at phone width
async function touch(cdp, type, x, y) {
  await cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x, y, id: 1 }] });
}

for (const _ of engine === "chromium" ? [0] : []) await check("touch: a long press drags a row at 390 px; a quick swipe scrolls; a tap still pushes into the chat", async () => {
  const { page, context, puts } = await open({ viewport: { width: 390, height: 844 }, touch: true });
  const cdp = await context.newCDPSession(page);
  // A swipe that starts moving before the press delay is a scroll, not a drag.
  let p = await centre(rowFor(page, "Cat 03"));
  await touch(cdp, "touchStart", p.x, p.y);
  for (let i = 1; i <= 6; i++) { await touch(cdp, "touchMove", p.x, p.y - i * 12); await sleep(16); }
  await touch(cdp, "touchEnd");
  await sleep(500);
  assert.equal(puts.length, 0);
  assert.equal(await page.locator(".is-dnd-dragging").count(), 0);
  if (process.env.DEBUG_TOUCH) await page.evaluate(() => { window.__ev = []; for (const t of ["pointerdown","pointermove","pointerup","pointercancel","touchmove","contextmenu","click","touchstart","touchend"]) window.addEventListener(t, e => window.__ev.push(t + (e.cancelable === false ? "!" : "")), true); });
  // Long press, then drag Cat 03 to the top.
  await page.evaluate(() => document.querySelector(".contact-section").scrollTo(0, 0));
  p = await centre(rowFor(page, "Cat 03"));
  await touch(cdp, "touchStart", p.x, p.y);
  await sleep(520);
  await until(async () => (await page.locator(".is-dnd-dragging").count()) === 1, "the long press to lift the row");
  await sleep(300);
  const top = await centre(rowFor(page, "Cat 00")); // the pin zone opened above the list: Cat 00 is lower than before the lift
  if (shots) await page.screenshot({ path: join(shots, "touch-lifted-390.png") });
  for (let i = 1; i <= 12; i++) { await touch(cdp, "touchMove", p.x, p.y + (top.y - 14 - p.y) * i / 12); await sleep(16); }
  await touch(cdp, "touchEnd");
  if (process.env.DEBUG_TOUCH) console.log((await page.evaluate(() => window.__ev)).join(" "));
  await settled(page);
  assert.deepEqual((await names(page)).slice(0, 4), ["Cat 03", "Cat 00", "Cat 01", "Cat 02"]);
  await until(() => puts.length === 1, "the layout save");
  // The page did not open the chat from the drag.
  assert.ok(await page.locator(".contact-pane.open").count() > 0, "still on the list");
  // A long press released without moving opens the menu rather than leaving the row lifted.
  p = await centre(rowFor(page, "Cat 05"));
  await touch(cdp, "touchStart", p.x, p.y);
  await sleep(520);
  await touch(cdp, "touchEnd");
  if (process.env.DEBUG_TOUCH) { await sleep(300); console.log("menu", await page.locator(".sidebar-context-menu").count(), await page.evaluate(() => window.__ev.slice(-12).join(" "))); }
  await page.getByRole("menuitem", { name: "Pin", exact: true }).waitFor({ timeout: 4000 });
  await until(async () => (await page.locator(".is-dnd-dragging").count()) === 0, "the lifted row to settle back");
  await sleep(450); // the release's own click is ignored for a moment so it cannot hit the menu that just opened under the finger
  await page.getByRole("menuitem", { name: "Pin", exact: true }).tap();
  await until(() => puts.length === 2, "pin saved");
  await page.locator(".pin-tile").first().waitFor();
  await sleep(600);
  if (shots) await page.screenshot({ path: join(shots, "pinned-390.png") });
  // Taps still push the chat in over the list, for a tile and for a row.
  await page.locator(".pin-tile").first().tap();
  await page.waitForFunction(() => document.querySelector(".chat-name-pill h1")?.textContent === "Cat 05", null, { timeout: 5000 });
  await until(async () => (await page.locator(".contact-pane.open").count()) === 0, "the list to slide away");
  await context.close();
});

await browser.close();
await server.close();
await rm(outDir, { recursive: true, force: true });
if (failures.length) { console.log(`sidebar pins checks: FAIL (${failures.length} of ${failures.length + results.length}): ${failures.join("; ")}`); process.exit(1); }
console.log(`sidebar pins checks: PASS (${results.length} scenarios)`);
