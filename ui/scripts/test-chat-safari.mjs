// Safari chat-pane regressions: (1) a restored reading position whose anchor is no longer rendered must not
// leave the pane hidden, (2) conversation text must be selectable and copyable, (3) sending must scroll to the
// bottom first and then pop the bubble once, without long tasks.
// Synthetic data only. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:chat-safari
// CHAT_SWITCH_ENGINE=webkit (with a WebKit-capable PLAYWRIGHT_MODULE) runs the same checks in WebKit.
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { build, createServer, preview } from "vite";
import { createServer as createHttpServer } from "node:http";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const webkit = process.env.CHAT_SWITCH_ENGINE === "webkit";
const engine = webkit ? pw.webkit : pw.chromium;
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
// Timing is judged on a production build (React dev builds are several times slower than what ships).
const outDir = mkdtempSync(join(tmpdir(), "tofi-safari-"));
const nodeEnv = process.env.NODE_ENV;
process.env.NODE_ENV = "production"; // the React plugin picks the production JSX runtime from this
await build({ configFile: false, mode: "production", root: ui, plugins: [react()], logLevel: "silent", build: { outDir, emptyOutDir: true } });
if (nodeEnv === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = nodeEnv;
const prod = await preview({ configFile: false, root: ui, logLevel: "error", build: { outDir }, preview: { host: "127.0.0.1", port: 0 } });
const prodOrigin = `http://127.0.0.1:${prod.httpServer.address().port}`;
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error", cacheDir: join(outDir, ".vite-dev") });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
// A real event stream stays open; a stream that ends makes the app reconnect (and re-render) every second.
const sseServer = createHttpServer((request, response) => { response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", "access-control-allow-origin": "*" }); response.write(": synthetic stream\n\n"); request.on("close", () => response.end()); });
await new Promise(resolve => sseServer.listen(0, "127.0.0.1", resolve));
const sseOrigin = `http://127.0.0.1:${sseServer.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["bot_a", "bot_b"].map((id, i) => ({ id, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${"ab"[i]}`, created_at: iso(0) }));
const convs = [
  { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 150, user_visible: true },
  { id: "dm_b", kind: "dm", name: "Simile Cat", bot_id: "bot_b", bot_ids: ["bot_b"], updated_at: iso(0), read_seq: 150, user_visible: true },
];
const COUNT = { dm_a: 150, dm_b: 150 };
// dm_a carries a run with progress notes (seq 132-135) that fold away once the run is done (final at seq 136).
const PROGRESS_RUN = "run_progress_1";
const PROGRESS = [132, 133, 134, 135];
const FINAL = 136;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

function history(conv) {
  const out = [];
  for (let seq = 1; seq <= COUNT[conv]; seq++) {
    const user = seq % 5 === 1;
    const base = { id: `${conv}_m${seq}`, conversation_id: conv, seq, created_at: iso(seq * 30) };
    const lines = 1 + ((seq * 7) % 5);
    const content = Array.from({ length: lines }, (_, n) => `Synthetic ${conv} line ${seq}.${n}`).join("\n\n");
    if (conv === "dm_a" && PROGRESS.includes(seq)) out.push({ ...base, role: "assistant", kind: "progress", run_id: PROGRESS_RUN, content: `Synthetic progress note ${seq}`, sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" });
    else if (conv === "dm_a" && seq === FINAL) out.push({ ...base, role: "assistant", run_id: PROGRESS_RUN, content: "Synthetic final answer after the progress notes", sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" });
    else out.push({ ...base, role: user ? "user" : "assistant", content, ...(user ? {} : { run_id: `run_${conv}_${seq}`, sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" }) });
  }
  return out;
}

async function open({ width = 1180, height = 820, touch = false, mobile = false, nativeState = null, production = false, reduced = false } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", ...(touch ? { hasTouch: true, isMobile: mobile } : {}), permissions: webkit ? [] : ["clipboard-read", "clipboard-write"] });
  await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch {} });
  if (nativeState) await context.addInitScript(state => {
    window.tofiDesktop = { platform: "darwin", themePreference: "light", onCommand: () => () => {}, onWindowState: () => () => {}, onFlushState: () => () => {},
      readWorkspaceState: async () => state, writeWorkspaceState: async () => {}, appearance: async () => {}, remoteInputFocus: () => {},
      getPermissionStatus: async () => ({ supported: false, microphone: "unknown", screen: "unknown", accessibility: false }), requestMicrophoneAccess: async () => false };
  }, nativeState);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const store = Object.fromEntries(Object.keys(COUNT).map(id => [id, history(id)]));
  const world = { runs: true, sends: [], store };
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = path.split("/")[3] ?? "";
    const later = async (ms, body) => { await sleep(ms); return json(body); };
    if (path.endsWith("/events") || path.includes("/events/")) return route.continue({ url: `${sseOrigin}/events` });
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
      world.sends.push(message);
      const run = { id: `run_sent_${seq}`, conversation_id: conv, bot_id: "bot_a", status: "queued", model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: message.id, queue_seq: 1, created_at: iso(seq * 30), updated_at: iso(seq * 30) };
      return later(60, { message, run, runs: [run] });
    }
    if (path.endsWith("/messages")) {
      const before = Number(url.searchParams.get("before_seq") ?? Infinity);
      const eligible = store[conv].filter(m => m.seq < before);
      const pageRows = eligible.slice(-50);
      return later(120, { messages: pageRows, drafts: [], has_more: eligible.length > pageRows.length, event_cursor: 0 });
    }
    if (path.endsWith("/runs")) {
      const runs = conv === "dm_a" && world.runs ? [{ id: PROGRESS_RUN, conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "dm_a_m131", queue_seq: 1, created_at: iso(131 * 30), updated_at: iso(136 * 30) }] : [];
      return later(40, { runs });
    }
    if (path.endsWith("/memories")) return later(30, { memories: [] });
    if (path.endsWith("/tools")) return later(60, url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) return later(60, { occurrences: [] });
    if (path.endsWith("/schedules")) return later(60, { schedules: [] });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto((production ? prodOrigin : origin) + "/");
  await page.locator(".conversation-row").first().waitFor();
  return { page, context, errors, world };
}
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
const settle = async page => { await page.waitForTimeout(1400); };

const results = [];
const failures = [];
const check = async (name, run) => {
  if (process.env.CHAT_SWITCH_ONLY && !name.includes(process.env.CHAT_SWITCH_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

// ---- scenarios are appended below ----

// 1. A saved reading position anchored on a progress note that is no longer rendered (the run finished and
//    its notes fold into the final answer) while older history is still paged out (has_more) must not leave
//    the pane hidden. Reading positions are persisted by the native shell, so the shell is stubbed here.
await check("restore with an unrendered progress anchor reveals the pane", async () => {
  const { page, context, errors } = await open({ nativeState: { activeConversation: "dm_a", readingPositions: { dm_a: { anchorSeq: 133, anchorOffset: 24, entryMax: 150, atBottom: false, detached: true } } } });
  const samples = [];
  const start = Date.now();
  while (Date.now() - start < 1600) {
    samples.push(await page.evaluate(() => { const l = document.querySelector(".message-list"); return { t: performance.now(), vis: l ? getComputedStyle(l).visibility : null, rendered: document.querySelectorAll(".message-block").length, anchor: Boolean(document.querySelector('[data-message-seq="133"]')) }; }));
    await page.waitForTimeout(25);
  }
  const rendered = samples.filter(sample => sample.rendered > 0);
  assert.ok(rendered.length > 0, "messages rendered");
  assert.equal(rendered.some(sample => sample.anchor), false, "precondition: the anchor progress note is not rendered");
  const hiddenMs = rendered.filter(sample => sample.vis === "hidden").length * 25;
  const last = samples.at(-1);
  assert.equal(last.vis, "visible", `pane still hidden 1.6 s after the restore (${hiddenMs} ms hidden)`);
  assert.ok(hiddenMs <= 250, `pane hidden for about ${hiddenMs} ms`);
  const bottom = await page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop; });
  assert.ok(bottom >= 0, "scroll position is valid");
  assert.deepEqual(errors, []);
  await context.close();
});

// 2. Conversation text is selectable and copyable: a real mouse drag across a bot bubble and a user bubble keeps
//    its selection (also after polling and re-renders) and copies the text.
const selectionProbe = async (page, selector, label) => {
  const target = page.locator(selector).last();
  // Centred, not just revealed: a drag that starts at the scroller's edge auto-scrolls and the selection runs away.
  await target.evaluate(el => el.scrollIntoView({ block: "center" }));
  const box = await target.boundingBox();
  assert.ok(box, `${label}: bubble text is on screen`);
  const text = (await target.innerText()).trim();
  await page.evaluate(() => { window.__copied = []; document.addEventListener("copy", event => { window.__copied.push(String(getSelection())); }, true); });
  const y = box.y + Math.min(box.height / 2, 12);
  await page.mouse.move(box.x + 1, y);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, y, { steps: 6 });
  await page.mouse.move(box.x + Math.min(box.width - 1, box.width * 0.9), y, { steps: 6 });
  await page.mouse.up();
  const first = await page.evaluate(() => String(getSelection()));
  assert.ok(first.trim().length >= 3, `${label}: dragging selected "${first}"`);
  assert.ok(text.includes(first.trim().split("\n")[0].trim()), `${label}: selection ${JSON.stringify(first)} is bubble text ${JSON.stringify(text)}`);
  await page.waitForTimeout(3000);
  const later = await page.evaluate(() => String(getSelection()));
  assert.equal(later, first, `${label}: selection changed after 3 s (${JSON.stringify(first)} -> ${JSON.stringify(later)})`);
  await page.keyboard.press(process.platform === "darwin" ? "Meta+c" : "Control+c");
  const copied = await page.evaluate(() => window.__copied);
  assert.ok(copied.length >= 1 && copied.at(-1) === first, `${label}: copy event saw ${JSON.stringify(copied)}`);
  await page.evaluate(() => getSelection()?.removeAllRanges());
};
for (const [name, conv] of [["Simile Cat", "dm_b"]]) await check("drag-select and copy bot and user bubble text", async () => {
  const { page, context, errors } = await open();
  await settle(page);
  await rowFor(page, name).click();
  await settle(page);
  await selectionProbe(page, ".message-block:not(:has(.message-user)) .message-content p", "bot bubble");
  await selectionProbe(page, ".message-block:has(.message-user) .message-content p", "user bubble");
  assert.deepEqual(errors, []);
  await context.close();
});

// 2b. Copying across bubbles yields the conversation text only: the floating message toolbar's timestamp
//     is chrome, not content.
await check("selection across bubbles copies message text without toolbar timestamps", async () => {
  const { page, context } = await open();
  await settle(page);
  await rowFor(page, "Simile Cat").click();
  await settle(page);
  await page.evaluate(() => { window.__copied = []; document.addEventListener("copy", () => window.__copied.push(String(getSelection())), true); });
  const target = page.locator(".message-block:not(:has(.message-user)) .message-content p").nth(-3);
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  await page.mouse.move(box.x + 2, box.y + 8);
  await page.mouse.down();
  await page.mouse.move(box.x + 100, box.y + 300, { steps: 12 });
  await page.mouse.up();
  await page.keyboard.press(process.platform === "darwin" ? "Meta+c" : "Control+c");
  // What the clipboard really holds where it can be read (Chromium); otherwise what the copy event saw.
  const copied = webkit ? (await page.evaluate(() => window.__copied)).at(-1) ?? "" : await page.evaluate(() => navigator.clipboard.readText());
  assert.ok(copied.split("\n").filter(line => line.includes("Synthetic dm_b line")).length >= 2, `selection spans bubbles: ${JSON.stringify(copied.slice(0, 80))}`);
  assert.ok(!/\d{1,2}\/\d{1,2},? \d{1,2}:\d{2}/.test(copied), `copied text carries a toolbar timestamp: ${JSON.stringify(copied.slice(0, 120))}`);
  await context.close();
});

// 2c. Touch: a press that lands on selected text dismisses the selection and leaves the tap actions alone;
//     a plain tap still opens them (iPad behaviour).
await check("touch: tapping selected text does not also open the message actions; plain tap does", async () => {
  const { page, context } = await open({ width: 820, height: 1180, touch: true, mobile: true });
  await settle(page);
  assert.ok(await page.evaluate(() => matchMedia("(hover: none)").matches), "context has no hover, like an iPad");
  await rowFor(page, "Simile Cat").tap();
  await settle(page);
  const para = page.locator(".message-block:not(:has(.message-user)) .message-content p").nth(-3);
  await para.evaluate(p => { const r = document.createRange(); r.selectNodeContents(p); getSelection().removeAllRanges(); getSelection().addRange(r); });
  const box = await para.boundingBox();
  await page.touchscreen.tap(box.x + 20, box.y + 6);
  await page.waitForTimeout(300);
  assert.equal(await page.locator(".message-hover-actions").count(), 0, "tap on selected text opened the actions toolbar");
  await page.evaluate(() => getSelection().removeAllRanges());
  await page.touchscreen.tap(box.x + 20, box.y + 6);
  await page.waitForTimeout(300);
  assert.equal(await page.locator(".message-hover-actions").count(), 1, "a plain tap opens the actions toolbar");
  await context.close();
});

/** Records one sample per animation frame, plus main-thread stalls (the Long Tasks API is missing in WebKit). */
const SEND_RECORDER = () => {
  const frames = [];
  const t0 = performance.now();
  window.__t0 = t0;
  let last = t0;
  let on = true;
  window.__stalls = [];
  window.__animations = new Set();
  const channel = new MessageChannel();
  let beat = t0;
  channel.port1.onmessage = () => { const now = performance.now(); if (now - beat > 20) window.__stalls.push({ at: Math.round(beat - t0), ms: Math.round(now - beat) }); beat = now; if (on) setTimeout(() => channel.port2.postMessage(0), 0); };
  channel.port2.postMessage(0);
  const tick = now => {
    const scroller = document.querySelector(".message-scroll");
    const box = scroller.getBoundingClientRect();
    const block = document.querySelector('.message-block[data-message-id*="_sent"]');
    const content = block?.querySelector(".message-content");
    const r = content?.getBoundingClientRect();
    const ghost = document.querySelector(".v2-send-flight-ghost");
    const ghostBox = ghost?.getBoundingClientRect();
    // Every animation that belongs to the arrival: on the new bubble, or on its flight ghost (not the run's status shimmer).
    for (const animation of document.getAnimations()) {
      const target = animation.effect?.target;
      if (!target || animation.transitionProperty) continue;
      if (target === ghost || (block && target.closest?.(".message-block") === block && target.closest(".message-content") && !target.closest(".task-activity-live, .live-run-status"))) window.__animations.add(animation);
    }
    frames.push({ t: Math.round(now - t0), dt: Math.round(now - last), scrollTop: Math.round(scroller.scrollTop), fromBottom: Math.round(scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop),
      bubble: r ? { top: Math.round(r.top - box.top), inView: r.bottom > box.top && r.top < box.bottom, opacity: Number(getComputedStyle(content).opacity), hidden: getComputedStyle(content).visibility === "hidden" } : null,
      ghost: ghostBox ? { top: Math.round(ghostBox.top - box.top), inView: ghostBox.bottom > box.top && ghostBox.top < box.bottom } : null });
    last = now;
    if (on) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
  window.__stopSend = () => { on = false; return { frames, stalls: window.__stalls, animations: window.__animations.size }; };
};
/** Sends one message in a 150-message conversation and returns what every frame showed, from Enter until it settled. */
const traceSend = async (page, { scrollUp }) => {
  for (let i = 0; i < 2; i++) { await page.locator(".load-older").click(); await page.waitForTimeout(500); }
  assert.equal(await page.locator(".message-block").count(), 150, "all 150 messages are mounted");
  await page.evaluate(() => { document.querySelector(".message-scroll").scrollTop = 1e9; });
  if (scrollUp) {
    await page.mouse.move(600, 400);
    await page.mouse.wheel(0, -400);
    await page.evaluate(() => { document.querySelector(".message-scroll").scrollTop = 0; });
    await page.waitForTimeout(400);
  }
  await page.locator(".composer textarea").fill("Synthetic message for the frame trace");
  await page.waitForTimeout(1500); // the draft autosave and every poll go quiet before the measured send
  await page.evaluate(SEND_RECORDER);
  await page.waitForTimeout(150);
  const enter = await page.evaluate(() => performance.now() - window.__t0);
  await page.locator(".composer textarea").press("Enter");
  await page.waitForTimeout(1800);
  return { ...(await page.evaluate(() => window.__stopSend())), enter };
};
const sendScenario = (reduced, scrollUp) => async () => {
  const { page, context, world } = await open({ production: true, reduced });
  await settle(page);
  await rowFor(page, "Simile Cat").click();
  await settle(page);
  const { frames, stalls, animations, enter } = await traceSend(page, { scrollUp });
  assert.equal(world.sends.length, 1, "one message sent");
  const after = frames.filter(frame => frame.t >= enter);
  const first = after.findIndex(frame => frame.bubble);
  assert.ok(first >= 0, "the bubble appeared");
  // (a) The list is at the newest message in a frame of its own, before the bubble shows.
  const atBottom = after.findIndex(frame => frame.fromBottom <= 2);
  assert.ok(atBottom >= 0 && atBottom < first, `scroll reached the bottom in frame ${atBottom}, the bubble appeared in frame ${first}; the scroll must come first`);
  for (const frame of after.slice(atBottom, first)) assert.ok(frame.fromBottom <= 2, `list left the bottom again before the bubble (${frame.fromBottom}px)`);
  // (b) The bubble lands in view, once: no frame where it is out of view, and it never moves afterwards.
  const shown = after.slice(first);
  assert.ok(shown.every(frame => frame.bubble.inView), "the bubble stays in view");
  assert.ok(shown.at(-1).fromBottom <= 2, `list ends ${shown.at(-1).fromBottom}px from the bottom`);
  if (process.env.CHAT_SAFARI_DEBUG) console.log(shown.slice(0, 40).map(f => `${f.t}:st${f.scrollTop}/fb${f.fromBottom}/b${f.bubble.top}`).join(" "));
  const settled = shown.slice(Math.min(shown.length - 1, 3));
  const tops = settled.map(frame => frame.bubble.top);
  assert.ok(Math.max(...tops) - Math.min(...tops) <= 1, `bubble moved ${Math.max(...tops) - Math.min(...tops)}px after it appeared`);
  const scrolls = settled.map(frame => frame.scrollTop);
  assert.ok(Math.max(...scrolls) - Math.min(...scrolls) <= 1, `list scrolled ${Math.max(...scrolls) - Math.min(...scrolls)}px after the bubble appeared`);
  // One arrival animation, played in view: the flight ghost covers the hidden bubble and hands over once.
  assert.equal(animations, reduced ? 0 : 1, `${animations} arrival animations ran`);
  if (!reduced) {
    const flight = shown.filter(frame => frame.ghost);
    assert.ok(flight.length > 5, "the flight ghost played");
    assert.ok(flight.every(frame => frame.ghost.inView), "the flight stayed inside the scroller");
    const handedOver = shown.findIndex(frame => !frame.bubble.hidden);
    assert.ok(handedOver > 0, "the bubble was revealed after the flight");
    assert.equal(shown.slice(handedOver).filter(frame => frame.ghost).length, 0, "the ghost does not return after the hand-over");
  }
  // Frame continuity: nothing blocks the main thread for long while the message goes out (150 mounted messages).
  const worst = Math.max(0, ...stalls.map(stall => stall.ms));
  assert.ok(worst <= LONG_TASK_MS, `main thread blocked for ${worst} ms during the send (limit ${LONG_TASK_MS} ms): ${JSON.stringify(stalls)}`);
  await context.close();
};
const LONG_TASK_MS = Number(process.env.CHAT_SAFARI_LONG_TASK_MS || 100);
for (const reduced of [false, true]) for (const scrollUp of [false, true]) await check(`send: scroll to the bottom first, then the bubble arrives once, no long tasks (${scrollUp ? "from history" : "at bottom"}, ${reduced ? "reduced" : "full"} motion)`, sendScenario(reduced, scrollUp));

// 4. Phone layout (iPhone-sized touch viewport): list<->chat switch keeps the chat laid out under the sliding list,
//    the keyboard state gives the conversation its room back, the tap toolbar stays between header and composer
//    and can copy, fields stay at 16px (no iOS focus zoom) and wide tables scroll instead of breaking words.
const phone = () => open({ width: 390, height: 664, touch: true, mobile: true });
await check("phone: chat pane stays laid out under the list and hides only after the slide", async () => {
  const { page, context, errors } = await phone();
  await settle(page);
  const state = () => page.evaluate(() => { const chat = document.querySelector(".chat-pane"); const list = document.querySelector(".contact-pane"); return { display: getComputedStyle(chat).display, vis: getComputedStyle(chat).visibility, listOpen: list.classList.contains("open") }; });
  const listOpen = await state();
  assert.equal(listOpen.display, "flex", "chat pane keeps its layout while the list is open");
  assert.equal(listOpen.vis, "hidden", "chat pane is hidden (not focusable) while the list is open");
  await rowFor(page, "Simile Cat").tap();
  await page.waitForTimeout(60);
  assert.equal((await state()).vis, "visible", "chat is visible as soon as it is chosen");
  await settle(page);
  await page.locator(".back-button").tap();
  await page.waitForTimeout(80);
  const mid = await state();
  assert.equal(mid.vis, "visible", "chat stays visible while the list slides over it (no blank frame)");
  await page.waitForTimeout(500);
  assert.equal((await state()).vis, "hidden", "chat hides after the slide");
  assert.deepEqual(errors, []);
  await context.close();
});
await check("phone: keyboard open hides the perch and returns room to the conversation", async () => {
  const { page, context } = await phone();
  await settle(page);
  await rowFor(page, "Simile Cat").tap();
  await settle(page);
  const measure = () => page.evaluate(() => { const scroll = document.querySelector(".message-scroll"); const perch = document.querySelector(".composer-perch"); const composer = document.querySelector(".composer").getBoundingClientRect().height; const cs = getComputedStyle(scroll); return { pad: parseFloat(cs.paddingBottom) - composer, top: parseFloat(cs.paddingTop), perch: perch ? getComputedStyle(perch).display : "none" }; });
  const closed = await measure();
  assert.ok(closed.pad <= 70, `closed-keyboard band is ${closed.pad}px beyond the composer (limit 70)`);
  await page.evaluate(() => document.querySelector(".workspace").classList.add("keyboard-viewport-open"));
  await page.waitForTimeout(150);
  const open_ = await measure();
  assert.equal(open_.perch, "none", "perch hidden while the keyboard is up");
  assert.ok(open_.pad <= 9, `keyboard-open band is ${open_.pad}px beyond the composer (limit 9)`);
  // The message pane runs up under the header's glass, so its top padding is the header's height plus the 8 px of room.
  const headerH = await page.evaluate(() => document.querySelector(".chat-header").getBoundingClientRect().height);
  assert.ok(open_.top - headerH <= 9, `keyboard-open top padding is ${open_.top}px (header ${headerH}px)`);
  await context.close();
});
await check("phone: tap toolbar opens below the bubble, one toolbar, one 44px Copy", async () => {
  // Merged design: ui-chat's in-bubble bar (opens in the flow below the bubble) replaces the floating placement
  // clamp; the phone requirement that remains is that a tap yields exactly one toolbar with a reachable Copy.
  const { page, context } = await phone();
  await settle(page);
  await rowFor(page, "Simile Cat").tap();
  await settle(page);
  const id = await page.evaluate(() => { const hosts = [...document.querySelectorAll(".message-hover-host")].filter(host => !host.classList.contains("is-user") && host.querySelector(".message-content")); const host = hosts.at(-1); host.scrollIntoView({ block: "center" }); return host.dataset.hostId; });
  await page.waitForTimeout(300);
  await page.locator(`.message-hover-host[data-host-id="${id}"] .message-content`).first().tap();
  await page.waitForTimeout(300);
  const bar = await page.evaluate(() => {
    const bars = [...document.querySelectorAll(".message-hover-actions")].filter(el => el.getBoundingClientRect().height > 0);
    const copies = document.querySelectorAll('.message-hover-actions [data-action="copy"]').length;
    const el = bars[0]; if (!el) return { count: 0, copies };
    const header = document.querySelector(".chat-header").getBoundingClientRect().bottom, composer = document.querySelector(".composer").getBoundingClientRect().top;
    const r = el.getBoundingClientRect(); const copy = el.querySelector('[data-action="copy"]');
    return { count: bars.length, copies, top: r.top, bottom: r.bottom, header, composer, copyW: copy ? copy.getBoundingClientRect().width : 0 };
  });
  assert.equal(bar.count, 1, "exactly one visible toolbar on touch");
  assert.equal(bar.copies, 1, "exactly one Copy button");
  assert.ok(bar.copyW >= 44, `Copy button is ${bar.copyW}px wide`);
  assert.ok(bar.top >= bar.header - 1 && bar.bottom <= bar.composer + 1, `toolbar ${bar.top}-${bar.bottom} outside header ${bar.header} / composer ${bar.composer}`);
  const placement = await page.evaluate(() => {
    const el = [...document.querySelectorAll(".message-hover-actions")].find(b => b.getBoundingClientRect().height > 0);
    const blockEl = el.closest(".message-block"), c = blockEl.querySelector(".message-content").getBoundingClientRect(), b = el.getBoundingClientRect();
    const next = blockEl.nextElementSibling?.querySelector(".message-content")?.getBoundingClientRect();
    return { belowBubble: b.top >= c.bottom - 0.5, overNext: next ? b.bottom > next.top + 0.5 && b.top < next.bottom && b.right > next.left && b.left < next.right : false, inside: b.left >= 0 && b.right <= innerWidth };
  });
  assert.ok(placement.belowBubble, "the toolbar is below its own bubble");
  assert.ok(!placement.overNext, "the toolbar does not cover the next message");
  assert.ok(placement.inside, "the toolbar stays inside the window");
  await context.close();
});
await check("desktop: the hover bar sits below its bubble, clear of the neighbours, and hovering moves nothing", async () => {
  const { page, context } = await open();
  await settle(page);
  await rowFor(page, "Simile Cat").click();
  await settle(page);
  const ids = await page.evaluate(() => [...document.querySelectorAll(".message-block")].slice(-6, -1).map(el => el.dataset.messageId));
  for (const id of ids) {
    await page.locator(`.message-block[data-message-id="${id}"]`).evaluate(el => el.scrollIntoView({ block: "center" }));
    await page.mouse.move(2, 2);
    await page.waitForFunction(() => document.querySelectorAll(".message-hover-actions").length === 0);
    const heights = () => page.evaluate(() => [...document.querySelectorAll(".message-block")].map(el => Math.round(el.getBoundingClientRect().height * 10)));
    const before = await heights();
    const box = await page.locator(`.message-block[data-message-id="${id}"] .message-content`).first().boundingBox();
    await page.mouse.move(box.x + 20, box.y + 6);
    await page.locator(".message-hover-actions").first().waitFor();
    assert.deepEqual(await heights(), before, `${id}: hovering changed a message height`);
    const geometry = await page.evaluate(id => {
      const blockEl = document.querySelector(`.message-block[data-message-id="${id}"]`), b = blockEl.querySelector(".message-hover-actions").getBoundingClientRect(), c = blockEl.querySelector(".message-content").getBoundingClientRect();
      const hit = [...document.querySelectorAll(".message-block")].filter(el => el !== blockEl).flatMap(el => [...el.querySelectorAll(".message-content")]).some(el => { const r = el.getBoundingClientRect(); return Math.min(b.right, r.right) - Math.max(b.left, r.left) > 0.5 && Math.min(b.bottom, r.bottom) - Math.max(b.top, r.top) > 0.5; });
      return { below: b.top >= c.bottom - 0.5, hit, inside: b.left >= 0 && b.right <= innerWidth, btn: blockEl.querySelector(".message-hover-actions button").getBoundingClientRect().width };
    }, id);
    assert.ok(geometry.below, `${id}: bar is above its bubble's bottom edge`);
    assert.ok(!geometry.hit, `${id}: bar covers another message`);
    assert.ok(geometry.inside, `${id}: bar leaves the window`);
    assert.ok(geometry.btn >= 28, `${id}: desktop hit targets are ${geometry.btn}px`);
  }
  await context.close();
});
await check("phone: fields are 16px and wide tables scroll sideways", async () => {
  const { page, context } = await phone();
  await settle(page);
  const sizes = await page.evaluate(() => [...document.querySelectorAll("input:not([type=checkbox]):not([type=radio]):not([type=file]):not([type=hidden]),textarea,select")].filter(el => el.offsetParent).map(el => parseFloat(getComputedStyle(el).fontSize)));
  assert.ok(sizes.length > 0, "found a visible field");
  assert.ok(sizes.every(size => size >= 16), `field sizes ${sizes}`);
  await rowFor(page, "Simile Cat").tap();
  await settle(page);
  const textarea = await page.evaluate(() => parseFloat(getComputedStyle(document.querySelector(".composer textarea")).fontSize));
  assert.ok(textarea >= 16, `composer textarea is ${textarea}px`);
  const table = await page.evaluate(() => {
    const row = cells => `<tr>${cells.map(cell => `<td>${cell}</td>`).join("")}</tr>`;
    const host = [...document.querySelectorAll(".message-content")].filter(el => !el.closest(".is-user")).at(-1);
    host.insertAdjacentHTML("beforeend", `<div class="message-table"><table>${row(["Vendor", "Annual price", "Monthly price", "Contract ID", "Region", "Status"])}${row(["Northwind Storage", "$1,240.00", "$103.33", "CTR-2026-000417", "Northern Europe", "Renewal pending"])}</table></div>`);
    const wrap = document.querySelector(".message-table"); const cell = wrap.querySelector("td"); return { scroll: wrap.scrollWidth, client: wrap.clientWidth, wrap: getComputedStyle(cell).wordBreak, over: getComputedStyle(cell).overflowWrap, h: cell.getBoundingClientRect().height }; });
  assert.ok(table.scroll > table.client, `table does not scroll (scrollWidth ${table.scroll} <= ${table.client})`);
  assert.equal(table.wrap, "normal"); assert.equal(table.over, "normal");
  await context.close();
});
//SCENARIOS

await browser.close();
await server.close();
rmSync(outDir, { recursive: true, force: true });
sseServer.closeAllConnections();
await new Promise(resolve => sseServer.close(resolve));
await new Promise(resolve => prod.httpServer.close(resolve));
if (failures.length) { console.log(`chat safari checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`chat safari checks: PASS (${results.length} scenarios)`);
