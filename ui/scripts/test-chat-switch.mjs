// Chat pane: conversation switching must never blank, jump or flash, and sending while scrolled up must
// return to the bottom with the arrival animation played once, in view.
// Synthetic conversations (long enough to need older-history paging); the app's /api is answered in the
// browser with realistic latency. Every frame is sampled with requestAnimationFrame.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:chat-switch
// CHAT_SWITCH_ENGINE=webkit (with a WebKit-capable PLAYWRIGHT_MODULE) runs the same checks in WebKit.
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engine = process.env.CHAT_SWITCH_ENGINE === "webkit" ? pw.webkit : pw.chromium;
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = ["bot_a", "bot_b"].map((id, i) => ({ id, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${"ab"[i]}`, created_at: iso(0) }));
const convs = [
  { id: "grp_1", kind: "group", name: "Synthetic Group", bot_ids: ["bot_a", "bot_b"], updated_at: iso(0), read_seq: 40, user_visible: true },
  { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 100, user_visible: true },
  { id: "dm_b", kind: "dm", name: "Simile Cat", bot_id: "bot_b", bot_ids: ["bot_b"], updated_at: iso(0), read_seq: 60, user_visible: true },
];
const COUNT = { grp_1: 130, dm_a: 118, dm_b: 125 };
const SCHED_RUN = "5c1e0a00-0000-4000-8000-000000000001";
const SCHED_ID = "5c1e0a00-0000-4000-8000-000000000002";
const SCHEDULE_SEQ = 128; // a scheduled-task card sits near the end of the group
const LATENCY = { messages: 160, runs: 60, memories: 40, tools: 120, occurrences: 260, schedules: 260, send: 60 };

function history(conv) {
  const out = [];
  for (let seq = 1; seq <= COUNT[conv]; seq++) {
    const user = seq % 5 === 1;
    const lines = 1 + ((seq * 7) % 5);
    const base = { id: `${conv}_m${seq}`, conversation_id: conv, seq, created_at: iso(seq * 30) };
    if (conv === "grp_1" && seq === SCHEDULE_SEQ) out.push({ ...base, role: "assistant", kind: "scheduled_task", run_id: SCHED_RUN, content: "Synthetic scheduled instruction", sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" });
    else out.push({ ...base, role: user ? "user" : "assistant", content: Array.from({ length: lines }, (_, n) => `Synthetic ${conv} line ${seq}.${n}`).join("\n\n"), ...(user ? {} : { run_id: `run_${conv}_${seq}`, sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat" }) });
  }
  return out;
}
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function open({ width = 1180, height = 820, reduced = false, first = "" } = {}) {
  const order = first ? [convs.find(c => c.id === first), ...convs.filter(c => c.id !== first)] : convs;
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference" });
  await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch {} });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  if (process.env.CHAT_SWITCH_DEBUG) page.on("console", message => { if (message.text().startsWith("DBG")) console.log("  console:", message.text()); });
  const store = Object.fromEntries(Object.keys(COUNT).map(id => [id, history(id)]));
  const sends = [];
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = path.split("/")[3] ?? "";
    if (process.env.CHAT_SWITCH_DEBUG && /schedul/.test(path)) console.log("  api", path, url.search);
    const later = async (ms, body) => { await sleep(ms); return json(body); };
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: order });
    if (path.endsWith("/messages") && request.method() === "POST") {
      const body = request.postDataJSON();
      const list = store[conv];
      const seq = list[list.length - 1].seq + 1;
      const message = { id: `${conv}_sent${seq}`, conversation_id: conv, role: "user", content: body.content, seq, created_at: iso(seq * 30), client_message_id: body.client_message_id };
      list.push(message);
      sends.push(message);
      const run = { id: `run_sent_${seq}`, conversation_id: conv, bot_id: "bot_a", status: "queued", model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: message.id, queue_seq: 1, created_at: iso(seq * 30), updated_at: iso(seq * 30) };
      return later(LATENCY.send, { message, run, runs: [run] });
    }
    if (path.endsWith("/messages")) {
      const before = Number(url.searchParams.get("before_seq") ?? Infinity);
      const eligible = store[conv].filter(m => m.seq < before);
      const pageRows = eligible.slice(-50);
      return later(LATENCY.messages, { messages: pageRows, drafts: [], has_more: eligible.length > pageRows.length, event_cursor: 0 });
    }
    if (path.endsWith("/runs")) return later(LATENCY.runs, { runs: [] });
    if (path.endsWith("/memories")) return later(LATENCY.memories, { memories: [] });
    if (path.endsWith("/tools")) return later(LATENCY.tools, url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) {
      return later(LATENCY.occurrences, { occurrences: [{ root_run_id: SCHED_RUN, schedule_id: SCHED_ID, scheduled_for_utc: iso(5000), title: "Synthetic nightly digest", description: "Synthetic nightly digest at 18:40", created_by: "user", kind: "daily", timezone: "UTC", daily_time: "18:40", occurrence_number: 3, execution_status: "done", status_run_id: SCHED_RUN, result_in_conversation: true }] });
    }
    if (path.endsWith("/schedules")) return later(LATENCY.schedules, { schedules: [{ id: SCHED_ID, conversation_id: conv, bot_id: "bot_a", content: "x", kind: "daily", timezone: "UTC", next_at_utc: iso(9000), status: "active", created_at: iso(0), updated_at: iso(0), title: "Synthetic nightly digest", daily_time: "18:40" }] });
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

/** Starts a per-frame recorder in the page. Each frame notes the pane geometry and what the reader can see. */
const RECORDER = () => {
  const frames = [];
  const t0 = performance.now();
  let on = true;
  // The switch starts at the click itself, not when the test began recording.
  document.addEventListener("click", event => { if (event.target.closest?.(".conversation-row")) { window.__mark = performance.now(); window.__lastMark = window.__mark - t0; } }, true);
  const tick = () => {
    const scroller = document.querySelector(".message-scroll");
    if (scroller) {
      const box = scroller.getBoundingClientRect();
      const blocks = [...scroller.querySelectorAll(".message-block")];
      const visible = blocks.filter(block => { const r = block.getBoundingClientRect(); return r.bottom > box.top + 1 && r.top < box.bottom - 1; });
      const seen = visible.map(block => {
        const r = block.getBoundingClientRect();
        const content = block.querySelector(".message-content") ?? block;
        return { id: block.dataset.messageId, seq: Number(block.dataset.messageSeq), opacity: Math.min(Number(getComputedStyle(block).opacity), Number(getComputedStyle(content).opacity)), top: r.top - box.top, inside: r.bottom > box.top && r.top < box.bottom };
      });
      const card = scroller.querySelector(".scheduled-run");
      const ghost = document.querySelector(".v2-send-flight-ghost");
      const ghostBox = ghost?.getBoundingClientRect();
      frames.push({
        after: performance.now() >= (window.__mark ?? Infinity),
        t: performance.now() - t0, blocks: blocks.length, scrollTop: scroller.scrollTop, scrollHeight: scroller.scrollHeight, clientHeight: scroller.clientHeight,
        fromBottom: scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop, firstSeq: seen[0]?.seq ?? 0, lastSeq: seen.at(-1)?.seq ?? 0, seen,
        ghost: ghostBox ? { top: ghostBox.top - box.top, left: ghostBox.left } : null,
        pb: getComputedStyle(scroller).paddingBottom, pt: getComputedStyle(scroller).paddingTop, listH: Math.round(scroller.querySelector(".message-list").getBoundingClientRect().height),
        sc: scroller.querySelectorAll('[data-speaker-change="true"]').length, art: scroller.querySelectorAll("article.message").length, dm: scroller.querySelectorAll(".message-block .message-content").length,
        an: (() => { const el = scroller.querySelector('[data-speaker-change="true"]'); return el ? getComputedStyle(el).transition + "#" + el.getAnimations().map(a => `${a.constructor.name}:${a.transitionProperty ?? a.animationName}:${a.playState}:${Math.round(a.currentTime)}`).join(",") : ""; })(),
        hs: blocks.map(b => Math.round(b.getBoundingClientRect().height)),
        mt: blocks.map(b => { const a = b.querySelector("article") ?? b; const c = getComputedStyle(a), d = getComputedStyle(b); return `${c.marginTop}/${d.marginTop}/${d.paddingTop}/${a.dataset.speakerChange ?? ""}/${a.dataset.last ?? ""}`; }),
        tail: [...scroller.querySelector(".message-list").children].map(c => `${(c.className || c.tagName).toString().slice(0, 30)}:${Math.round(c.getBoundingClientRect().height)}`).filter((_, i, a) => i < 2 || i >= a.length - 3).join(" | "),
        loading: Boolean(scroller.querySelector(".inline-state")), card: card ? { text: card.textContent, height: card.getBoundingClientRect().height, pending: card.closest(".scheduled-run-pending") !== null, visibility: getComputedStyle(card).visibility } : null,
      });
    }
    if (on) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
  window.__stopRecorder = () => { on = false; return frames; };
};

async function record(page, action, ms, { markNow = false } = {}) {
  await page.evaluate(RECORDER);
  await page.evaluate(now => { window.__mark = now ? performance.now() : Infinity; }, markNow);
  await action();
  await page.waitForTimeout(ms);
  // Only frames from the moment of the action on: what was on screen before it is not part of the switch.
  const all = await page.evaluate(() => ({ frames: window.__stopRecorder(), lastMark: window.__lastMark ?? 0 }));
  const frames = all.frames.filter(frame => frame.after);
  frames.lastMark = all.lastMark;
  return frames;
}
const rowFor = (page, name) => page.locator(".conversation-row").filter({ has: page.locator(".contact-name", { hasText: name }) });
const settle = async page => { await page.waitForTimeout(1600); };
const withContent = frames => frames.filter(frame => frame.blocks > 0 && !frame.loading);

/** Invariants for the frames that follow the first painted content of a freshly shown conversation. */
function dump(frames, label) {
  if (!process.env.CHAT_SWITCH_DEBUG) return;
  let prev = ""; let prevF = null;
  for (const f of frames) {
    const line = `blocks=${f.blocks} top=${Math.round(f.scrollTop)} h=${Math.round(f.scrollHeight)} fromBottom=${Math.round(f.fromBottom)} seq=${f.firstSeq}-${f.lastSeq} loading=${f.loading} an=${f.an} sc=${f.sc} art=${f.art} mc=${f.dm} pb=${f.pb} pt=${f.pt} listH=${f.listH} card=${f.card ? (f.card.pending ? "pending" : f.card.text.slice(0, 40)) : "-"}`;
    if (line !== prev) console.log(`  [${label}] t=${Math.round(f.t)} ${line}`);
    if (prevF && prevF.hs.length === f.hs.length && prevF.scrollHeight !== f.scrollHeight) console.log("     margins:", f.mt.map((m, i) => m === prevF.mt[i] ? null : `#${i}:${prevF.mt[i]}->${m}`).filter(Boolean).slice(0, 6).join(" "), "\n     height diffs:", f.hs.map((h, i) => h === prevF.hs[i] ? null : `#${i}:${prevF.hs[i]}->${h}`).filter(Boolean).join(" "));
    prev = line; prevF = f;
  }
}
function assertStable(frames, label, { expectBottom = true } = {}) {
  dump(frames, label);
  const shown = frames.findIndex(frame => frame.blocks > 0);
  assert.ok(shown >= 0, `${label}: content never appeared`);
  const after = frames.slice(shown);
  const first = after[0];
  if (expectBottom) assert.ok(first.fromBottom <= 2, `${label}: first painted frame is ${first.fromBottom}px from the bottom (positioned only after first paint)`);
  const emptied = after.filter(frame => frame.blocks === 0);
  assert.equal(emptied.length, 0, `${label}: ${emptied.length} frames went empty after content appeared`);
  const counts = new Set(after.map(frame => frame.blocks));
  assert.equal(counts.size, 1, `${label}: message count changed after first paint (${[...counts].join(" -> ")}); older history was loaded without the reader asking`);
  const tops = after.map(frame => frame.scrollTop);
  const moved = Math.max(...tops) - Math.min(...tops);
  assert.ok(moved <= 2, `${label}: scrollTop moved by ${moved}px after first paint`);
  assert.equal(new Set(after.map(frame => frame.firstSeq)).size, 1, `${label}: first visible message changed after first paint`);
  for (const frame of after) {
    for (const item of frame.seen) assert.ok(item.opacity === 1 && item.inside, `${label}: message ${item.id} visible with opacity ${item.opacity}`);
  }
}

const results = [];
const failures = [];
const check = async (name, run) => {
  if (process.env.CHAT_SWITCH_ONLY && !name.includes(process.env.CHAT_SWITCH_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

// 1. First visit to a long conversation: shows loading, then lands at the bottom before the first painted
//    message frame and never moves again (no hidden older-history paging, no second render).
for (const reduced of [false, true]) await check(`first visit lands at the bottom before first paint and stays put${reduced ? " (reduced motion)" : ""}`, async () => {
  const { page, context, errors } = await open({ reduced });
  await settle(page);
  const frames = await record(page, () => rowFor(page, "Simile Cat").click(), 1500);
  assertStable(frames, "first visit dm_b");
  const last = frames.at(-1);
  assert.equal(last.lastSeq, COUNT.dm_b, "newest message is visible at the bottom");
  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  await context.close();
});

// 2. Group with unread backlog larger than one page: still opens at the newest messages, no stepwise paging.
await check("long unread backlog opens at the latest page without paging", async () => {
  const { page, context } = await open();
  await settle(page);
  await rowFor(page, "Ledger Cat").click();
  await settle(page);
  const frames = await record(page, () => rowFor(page, "Synthetic Group").click(), 1800);
  assertStable(frames, "group first visit");
  assert.equal(frames.at(-1).lastSeq, COUNT.grp_1);
  await context.close();
});

// 2b. Unread boundary inside the first page: opens at the divider before first paint, then holds still.
await check("unread boundary inside the page opens at the divider without moving", async () => {
  const { page, context } = await open();
  await settle(page);
  const frames = await record(page, () => rowFor(page, "Ledger Cat").click(), 1500);
  assertStable(frames, "dm_a unread divider", { expectBottom: false });
  const shown = await page.evaluate(() => { const d = document.querySelector(".unread-divider"); const s = document.querySelector(".message-scroll"); if (!d) return null; const r = d.getBoundingClientRect(), b = s.getBoundingClientRect(); return r.top >= b.top - 1 && r.bottom <= b.bottom + 1; });
  assert.equal(shown, true, "divider is in view");
  await context.close();
});

// 3. A -> B -> A -> B quickly: B fully visible, no scrolling needed, nothing blank, nothing faded.
await check("rapid A->B->A->B leaves B visible without scrolling", async () => {
  const { page, context } = await open();
  await settle(page);
  await rowFor(page, "Ledger Cat").click();
  await settle(page);
  let frames = await record(page, async () => {
    await rowFor(page, "Simile Cat").click(); await page.waitForTimeout(90);
    await rowFor(page, "Ledger Cat").click(); await page.waitForTimeout(90);
    await rowFor(page, "Simile Cat").click();
  }, 1800);
  // Judged from the final click on; the earlier hops are just the rapid switching being tested.
  const hops = frames;
  frames = frames.filter(frame => frame.t >= hops.lastMark);
  dump(frames, "rapid");
  const tail = frames.slice(-20);
  for (const frame of tail) {
    assert.ok(frame.blocks > 0, "B is rendered");
    assert.ok(frame.fromBottom <= 2, `B rests ${frame.fromBottom}px above the bottom`);
    assert.equal(frame.lastSeq, COUNT.dm_b);
    for (const item of frame.seen) assert.ok(item.opacity === 1 && item.inside, `message ${item.id} faded (${item.opacity})`);
  }
  const visibleFrames = frames.filter(frame => frame.blocks > 0);
  const shown = visibleFrames.findIndex(frame => frame.lastSeq === COUNT.dm_b);
  assert.ok(shown >= 0);
  const settled = visibleFrames.slice(shown);
  assert.equal(new Set(settled.map(frame => frame.blocks)).size, 1, "B's message count changed after it first showed");
  assert.ok(Math.max(...settled.map(frame => frame.scrollTop)) - Math.min(...settled.map(frame => frame.scrollTop)) <= 2, "B scrolled after it first showed");
  await context.close();
});

// 4. Returning to a conversation already seen shows it on the very first frame (cached snapshot), at the
//    position the reader left it, then reconciles without moving.
await check("revisit shows the cached snapshot immediately and does not jump on reconcile", async () => {
  const { page, context } = await open();
  await settle(page);
  await rowFor(page, "Simile Cat").click();
  await settle(page);
  await rowFor(page, "Ledger Cat").click();
  await settle(page);
  // Leave Simile Cat scrolled away from the bottom, then come back.
  await rowFor(page, "Simile Cat").click();
  await settle(page);
  await page.evaluate(() => { document.querySelector(".message-scroll").scrollTop -= 700; });
  await page.waitForTimeout(250);
  const before = await page.evaluate(() => { const s = document.querySelector(".message-scroll"); const box = s.getBoundingClientRect(); const b = [...s.querySelectorAll(".message-block")].find(x => x.getBoundingClientRect().bottom > box.top + 1); return { seq: Number(b.dataset.messageSeq), top: b.getBoundingClientRect().top - box.top }; });
  await rowFor(page, "Ledger Cat").click();
  await settle(page);
  const frames = await record(page, () => rowFor(page, "Simile Cat").click(), 1500);
  const firstFrame = frames[0];
  assert.ok(firstFrame.blocks > 0 && !firstFrame.loading, "cached conversation is on screen at the first frame after the click");
  assertStable(frames, "revisit dm_b", { expectBottom: false });
  assert.equal(frames.at(-1).firstSeq, before.seq, "reader's place is restored");
  assert.ok(Math.abs(frames.at(-1).seen[0].top - before.top) <= 2, "reader's place is restored to the pixel");
  await context.close();
});

// 5. The scheduled-task card never shows placeholder status text while its data is loading.
await check("scheduled card renders no placeholder before its data arrives", async () => {
  const { page, context } = await open({ first: "dm_a" });
  await settle(page);
  const frames = await record(page, () => rowFor(page, "Synthetic Group").click(), 1800);
  const cards = frames.filter(frame => frame.card);
  assert.ok(cards.length > 0, "card present");
  if (process.env.CHAT_SWITCH_DEBUG) console.log("  cards:", cards.length, "distinct", [...new Set(cards.map(f => f.card.pending ? "PENDING" : f.card.text.slice(0, 60)))].join(" || "));
  const wrong = cards.filter(frame => frame.card.visibility !== "hidden" && !frame.card.pending && /This run ended|Time unconfirmed|Unconfirmed|Scheduled task/.test(frame.card.text));
  assert.equal(wrong.length, 0, `placeholder text visible in ${wrong.length} frames: ${wrong[0]?.card.text}`);
  const final = cards.at(-1).card;

  assert.match(final.text, /Synthetic nightly digest/);
  assert.match(final.text, /18:40/);
  const heights = cards.map(frame => frame.card.height);
  assert.ok(Math.max(...heights) - Math.min(...heights) <= 24, `card height jumped by ${Math.max(...heights) - Math.min(...heights)}px`);
  await context.close();
});

// 6. Sending while scrolled up: the list returns to the bottom, the new bubble lands in view, its arrival
//    animation runs once and only while on screen. Checked with and without reduced motion.
for (const reduced of [true, false]) {
  await check(`send while scrolled up returns to the bottom (${reduced ? "reduced motion" : "full motion"})`, async () => {
    const { page, context, sends } = await open({ reduced });
    await settle(page);
    await rowFor(page, "Simile Cat").click();
    await settle(page);
    // A real gesture: the wheel is what tells the pane the reader left the bottom.
    await page.mouse.move(600, 400);
    await page.mouse.wheel(0, -400);
    await page.evaluate(() => { const s = document.querySelector(".message-scroll"); s.scrollTop = 0; });
    await page.waitForTimeout(400);
    const away = await page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop; });
    assert.ok(away > 1500, `scrolled up (${away}px from bottom)`);
    await page.evaluate(() => {
      window.__anim = [];
      const watch = () => { for (const animation of document.getAnimations()) {
        const target = animation.effect?.target;
        const block = target?.closest?.(".message-block");
        if (!block || !block.dataset.messageId?.includes("_sent") || animation.transitionProperty) continue; // CSS transitions (margins) are not arrival animations
        const scroller = document.querySelector(".message-scroll");
        const sBox = scroller.getBoundingClientRect(); const r = target.getBoundingClientRect();
        const inView = r.bottom > sBox.top && r.top < sBox.bottom;
        window.__anim.push({ id: animation.id || animation.animationName || "anim", t: performance.now(), state: animation.playState, inView, name: animation.animationName ?? ("waapi:" + target.className + ":" + Object.keys(animation.effect.getKeyframes()[0] ?? {}).join("+")), fromBottom: scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop });
      } };
      const loop = () => { watch(); window.__animRaf = requestAnimationFrame(loop); };
      loop();
    });
    const frames = await record(page, async () => {
      await page.locator(".composer textarea").fill("Synthetic message sent while scrolled up");
      await page.locator(".composer textarea").press("Enter");
    }, 2200, { markNow: true });
    assert.equal(sends.length, 1, "one message sent");
    const anims = await page.evaluate(() => { cancelAnimationFrame(window.__animRaf); return window.__anim; });
    const last = frames.at(-1);
    if (process.env.CHAT_SWITCH_DEBUG) {
      console.log("anims", JSON.stringify(anims.slice(0, 12)), anims.length);
      let prev = null;
      for (const f of frames) { if (!prev || Math.abs(f.scrollTop - prev.scrollTop) > 2 || f.ghost) console.log("frame", Math.round(f.t), "top", Math.round(f.scrollTop), "h", f.scrollHeight, "fromBottom", Math.round(f.fromBottom), "sent", JSON.stringify(f.seen.filter(i => i.id.includes("_sent"))), "ghost", JSON.stringify(f.ghost)); prev = f; }
    }
    assert.ok(last.fromBottom <= 2, `viewport ends ${last.fromBottom}px from the bottom`);
    assert.ok(last.seen.some(item => item.id.includes("_sent")), "sent bubble is in the viewport at the end");
    assert.equal(last.seen.find(item => item.id.includes("_sent")).opacity, 1, "sent bubble fully opaque");
    // Arrival animations on the sent bubble must only run while the bubble is inside the scroller.
    const offscreen = anims.filter(a => a.state === "running" && !a.inView);
    assert.equal(offscreen.length, 0, `animation ran off-screen in ${offscreen.length} samples (${[...new Set(offscreen.map(a => a.name))].join(",")})`);
    // One arrival: the bubble must not be moving (apart from the animation) after the jump completes; one jump only.
    const jumps = frames.slice(1).filter((frame, i) => Math.abs(frame.scrollTop - frames[i].scrollTop) > 40);
    assert.ok(jumps.length <= 1, `${jumps.length} large scroll jumps (double jump)`);
    if (reduced) {
      const sentRunning = anims.filter(a => a.state === "running");
      assert.equal(sentRunning.length, 0, "no travelling animation under reduced motion");
    }
    // After the jump, the new bubble's position is stable.
    const sentTops = frames.filter(frame => frame.seen.some(item => item.id.includes("_sent"))).slice(-30).map(frame => frame.seen.find(item => item.id.includes("_sent")).top);
    assert.ok(sentTops.length > 5 && Math.max(...sentTops) - Math.min(...sentTops) <= 2, "sent bubble rests at its final position");
    await context.close();
  });
}

await browser.close();
await server.close();
if (failures.length) { console.log(`chat switch checks: FAIL (${failures.length} of ${failures.length + results.length})`); process.exit(1); }
console.log(`chat switch checks: PASS (${results.length} scenarios)`);
