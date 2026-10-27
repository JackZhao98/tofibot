// Arrival glide: when a block lands under a reader who follows the bottom, the content above rides up with a
// scroll tween instead of jumping. Budgets (plan item 3):
//  - a whole message: no frame moves the previous message more than 24 px, it is home within ~220 ms (+2 frames),
//    the pane is never more than 34 px from the bottom, layout shift <= 0.01, no frame over 34 ms;
//  - streaming growth: the follow step is at most 12 px per frame at p95 (hops of ~120 ms);
//  - reduced motion: the pane lands in one frame; a reader who scrolls up abandons the hop and is not pulled back.
// Synthetic data. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:arrival-glide
// ARRIVAL_GLIDE_ENGINE=webkit runs the same checks in WebKit; ARRIVAL_GLIDE_THROTTLE=4 slows Chromium's CPU 4x.
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import http from "node:http";
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { extname, join } from "node:path";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engine = process.env.ARRIVAL_GLIDE_ENGINE === "webkit" ? pw.webkit : pw.chromium;
const THROTTLE = Number(process.env.ARRIVAL_GLIDE_THROTTLE || 1);
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
// Frame budgets only mean something against the production bundle: React's development build alone stalls a frame for 150 ms.
const outDir = mkdtempSync(join(tmpdir(), "tofi-glide-"));
await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const mime = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".json": "application/json", ".woff2": "font/woff2", ".png": "image/png" };
const server = http.createServer((req, res) => {
  let file = join(outDir, new URL(req.url, "http://x").pathname);
  if (!existsSync(file) || statSync(file).isDirectory()) file = join(outDir, "index.html");
  res.writeHead(200, { "content-type": mime[extname(file)] || "application/octet-stream" });
  res.end(readFileSync(file));
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: iso(0) }];
const dm = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 99, user_visible: true };
const LONG = Array.from({ length: 4 }, (_, i) => `Synthetic ledger sentence number ${i + 1} describes the weekly entries and totals.`).join(" ");
const history = () => Array.from({ length: 40 }, (_, i) => {
  const seq = i + 1, user = seq % 3 === 1;
  return { id: `m${seq}`, conversation_id: "dm_a", seq, role: user ? "user" : "assistant", content: user ? `Question ${seq}: ${LONG}` : `${LONG}\n\n- point ${seq}\n- another point`, created_at: iso(seq * 20), ...(user ? {} : { sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: `run_m${seq}` }) };
});
const WHOLE = "Here is the full answer arriving in one piece.\n\n- first point about the ledger\n- second point about totals\n- third point with `code`\n\nClosing paragraph with a bit more detail about the review on Friday and what to check first before then.";

async function open({ motion = "no-preference", compact = false, height = 820 } = {}) {
  const context = await browser.newContext({ viewport: { width: 1180, height }, locale: "en-US", reducedMotion: motion });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
    window.__ls = [];
    try { new PerformanceObserver(list => { for (const e of list.getEntries()) if (!e.hadRecentInput) window.__ls.push([e.startTime, e.value]); }).observe({ type: "layout-shift", buffered: true }); } catch {}
  });
  // ARRIVAL_GLIDE_NOBLUR=1 switches the progressive blur off, to see what it costs.
  if (process.env.ARRIVAL_GLIDE_NOBLUR) await context.addInitScript(() => document.addEventListener("DOMContentLoaded", () => { const st = document.createElement("style"); st.textContent = ".edge-blur{display:none!important}"; document.head.append(st); }));
  // ARRIVAL_GLIDE_CSS='...' injects extra CSS (to try blur variants against the budgets).
  if (process.env.ARRIVAL_GLIDE_CSS) await context.addInitScript(css => document.addEventListener("DOMContentLoaded", () => { const st = document.createElement("style"); st.textContent = css; document.head.append(st); }), process.env.ARRIVAL_GLIDE_CSS);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const messages = history();
  // Compact history: short messages so a tall pane shows a dozen of them (the cascade's full chain).
  if (compact) for (const m of messages) m.content = m.role === "user" ? `Question ${m.seq}: short.` : `Short answer ${m.seq}.`;
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path.endsWith("/events") || path.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: ": synthetic stream\n\n" });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: [dm] });
    if (path.endsWith("/messages")) return json({ messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [{ id: "run_m39", conversation_id: "dm_a", bot_id: "bot_a", status: "done", model: "synthetic-model", origin_conversation_id: "dm_a", trigger_message_id: "m38", queue_seq: 1, created_at: iso(39 * 20), updated_at: iso(39 * 20 + 5) }] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/tools")) {
      const activities = [1, 2, 3].map(n => ({ conversation_id: "dm_a", bot_id: "bot_a", run_id: "run_m39", call_id: `c${n}`, name: "tofi_shell", arguments: `{"command":"ls ${n}"}`, result: "ok", status: "completed", truncated: false, started_at: iso(39 * 20 + n), updated_at: iso(39 * 20 + n + 1) }));
      const summary = { run_id: "run_m39", bot_id: "bot_a", tool_count: 3, completed_count: 3, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: activities[0].started_at, updated_at: activities[2].updated_at };
      if (url.searchParams.has("run_ids")) return json({ summaries: [summary] });
      if (url.searchParams.has("run_id")) return json({ activities, tool_count: 3, has_more: false });
      return json({ activities, summaries: [summary], tool_count: 3, has_more: false });
    }
    if (path.endsWith("/schedule-occurrences")) return json({ occurrences: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(origin + "/b/bot_a");
  await page.locator(".message-block").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(700);
  if (THROTTLE > 1 && engine === pw.chromium) await (await context.newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: THROTTLE });
  return { page, context, errors };
}

/** Records, per frame, the last message's top, the pane's distance from the bottom, the frame time and the scroll offset. */
const startTrace = page => page.evaluate(() => {
  window.__trace = []; window.__traceOn = true;
  const sc = document.querySelector(".message-scroll");
  const tick = t => {
    const blocks = document.querySelectorAll("[data-message-id]");
    const prev = document.querySelector('[data-message-id="m40"]');
    window.__trace.push({ t, y: prev ? prev.getBoundingClientRect().top : null, fb: sc.scrollHeight - sc.clientHeight - sc.scrollTop, top: sc.scrollTop, jump: Boolean(document.querySelector(".jump-latest")), n: blocks.length });
    if (window.__traceOn) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
});
const stopTrace = page => page.evaluate(() => { window.__traceOn = false; return { trace: window.__trace, ls: window.__ls }; });
const maxStep = (trace, from) => { let m = 0; for (let i = 1; i < trace.length; i++) if (trace[i].t >= from && trace[i].y != null && trace[i - 1].y != null) m = Math.max(m, Math.abs(trace[i].y - trace[i - 1].y)); return +m.toFixed(1); };

const results = [], failures = [], report = {};
const check = async (name, run) => {
  if (process.env.ARRIVAL_GLIDE_ONLY && !name.includes(process.env.ARRIVAL_GLIDE_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};

// The tuning values the page uses; read from motion.ts so the test follows the constants when they are tuned.
const motionSource = readFileSync(join(ui, "src", "motion.ts"), "utf8");
const CASCADE = Object.fromEntries([...motionSource.slice(motionSource.indexOf("export const CASCADE")).matchAll(/^\s{2}(\w+):\s*([\d.]+),/gm)].map(([, k, v]) => [k, Number(v)]));
assert.ok(CASCADE.staggerMs && CASCADE.durationMs && CASCADE.maxLinks, "CASCADE constants are exported from motion.ts");

/** Per frame: every message's top (blocks in the pane), scroll state and frame time, for the cascade analysis. */
const startCascadeTrace = page => page.evaluate(() => {
  window.__ct = []; window.__ctOn = true;
  const sc = document.querySelector(".message-scroll");
  const tick = t => {
    const blocks = [...document.querySelectorAll(".message-block[data-message-id]")];
    window.__ct.push({ t, top: sc.scrollTop, h: sc.scrollHeight, fb: sc.scrollHeight - sc.clientHeight - sc.scrollTop, ids: blocks.slice(-30).map(b => b.dataset.messageId), ys: blocks.slice(-30).map(b => { const r = b.getBoundingClientRect(); return [r.top, r.bottom]; }), jump: Boolean(document.querySelector(".jump-latest")) });
    if (window.__ctOn) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
});
const stopCascadeTrace = page => page.evaluate(() => { window.__ctOn = false; return { trace: window.__ct, ls: window.__ls }; });

await check("a whole message arrives as a spring chain: staggered bottom-first, 2-4 px overshoot, home in ~400 ms, nothing shifts", async () => {
  const { page, context, errors } = await open({ compact: true, height: 1150 });
  const bottom = await page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop; });
  assert.ok(bottom <= 2, `starts at the bottom (${bottom}px away)`);
  await startCascadeTrace(page);
  await page.waitForTimeout(200);
  const info = await page.evaluate(({ SHORT }) => {
    const sc = document.querySelector(".message-scroll");
    const before = document.querySelector('[data-message-id="m40"]').getBoundingClientRect().top;
    const t = performance.now();
    window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: SHORT, created_at: new Date().toISOString() });
    return { t, h: sc.scrollHeight, before };
  }, { SHORT: "Short reply: the totals match." });
  // The animations exist as soon as React has committed: read their timing straight from the page.
  const anims = await page.evaluate(async () => {
    for (let i = 0; i < 30 && !document.querySelector('[data-message-id="m_w"]'); i++) await new Promise(r => requestAnimationFrame(r));
    await new Promise(r => requestAnimationFrame(r));
    return [...document.querySelectorAll(".message-block[data-message-id]")].reverse().flatMap(b => b.getAnimations().filter(a => a.id === "tofi-cascade").map(a => ({ id: b.dataset.messageId, delay: a.effect.getTiming().delay, duration: a.effect.getTiming().duration })));
  });
  await page.waitForTimeout(1000);
  const { trace, ls } = await stopCascadeTrace(page);
  const lastFrame = trace.at(-1);
  // From the first painted frame that holds the new block: before it the pane had not been rendered yet.
  const landed = trace.find(f => f.t >= info.t - 20 && f.ids.includes("m_w"));
  const after = trace.filter(f => f.t >= landed.t);
  // offset of a message at a frame = its top then minus its top at the end (the scroll is exact, so the end is the true place).
  const offsetOf = (f, id) => { const i = f.ids.indexOf(id); const j = lastFrame.ids.indexOf(id); return i < 0 || j < 0 ? null : f.ys[i][0] - lastFrame.ys[j][0]; };
  const chain = lastFrame.ids.slice().reverse().filter(id => after.some(f => { const o = offsetOf(f, id); return o != null && Math.abs(o) > 0.5; }));
  assert.ok(chain.length >= CASCADE.maxLinks, `every message in view took part (${chain.length})`);
    // When each link has covered half of its way home, interpolated between frames: that is where its delay shows, free of the
  // spring's slow first milliseconds and of the 16.7 ms frame quantum.
  const start0 = Object.fromEntries(chain.map(id => [id, offsetOf(landed, id)]));
  const startT = Object.fromEntries(chain.map(id => {
    const half = start0[id] / 2;
    for (let i = 1; i < after.length; i++) { const a = offsetOf(after[i - 1], id), b = offsetOf(after[i], id); if (a > half && b <= half) return [id, after[i - 1].t + (after[i].t - after[i - 1].t) * (a - half) / (a - b)]; }
    return [id, undefined];
  }));
  const order = chain.map(id => startT[id]);
  if (process.env.ARRIVAL_GLIDE_DEBUG) console.log("    m29", after.slice(0, 24).map(f => `${(f.t - info.t).toFixed(0)}:${offsetOf(f, "m29")?.toFixed(1)}`).join(" "));
  if (process.env.ARRIVAL_GLIDE_DEBUG) console.log("    chain", chain.join(","), order.map(t => (t - info.t).toFixed(0)).join(","));
  assert.ok(order.every(t => typeof t === "number"), "every link covered half its way");
  for (let k = 1; k < Math.min(order.length, CASCADE.maxLinks); k++) assert.ok(order[k] > order[k - 1] - 0.5, `link ${k} (${chain[k]}) crossed half way at ${order[k].toFixed(0)} before link ${k - 1} at ${order[k - 1].toFixed(0)}: the wave must run bottom-first`);
  assert.ok(order[CASCADE.maxLinks - 1] > order[0], "the wave takes time to travel up");
  // The timing, exactly: delay of link k is k * stagger.
  if (process.env.ARRIVAL_GLIDE_DEBUG) console.log("    anims", anims.length, anims.map(a => a.id).join(","));
  assert.ok(anims.length >= chain.length, `animations found for ${anims.length} links`);
  anims.forEach((a, k) => { const want = Math.min(k, CASCADE.maxLinks - 1) * CASCADE.staggerMs; assert.ok(Math.abs(a.delay - want) < 0.01, `link ${k} delay ${a.delay} (want ${want})`); });
  const perLink = (order[CASCADE.maxLinks - 1] - order[0]) / (CASCADE.maxLinks - 1);
  // The first frame in which a link visibly leaves its start has to be judged against its own hold; frame quantisation is 16.7 ms.
  // Overshoot: how far each message passes its true place (offset below 0 = past it, upward).
  const overshoot = chain.map(id => -Math.min(...after.map(f => offsetOf(f, id) ?? 0)));
  const settledAt = Math.max(...chain.map(id => { const last = after.filter(f => Math.abs(offsetOf(f, id) ?? 0) > 0.5).at(-1); return last ? last.t - info.t : 0; }));
  const finals = chain.map(id => offsetOf(lastFrame, id));
  const cls = ls.filter(([t]) => t >= info.t - 20).reduce((sum, [, v]) => sum + v, 0);
  // Exactness: the scroll position landed at once and never moved again; the page never changed height after the block rendered.
  const scrollMoves = after.filter(f => Math.abs(f.top - landed.top) > 0.01).length;
  const heightMoves = after.filter(f => Math.abs(f.h - landed.h) > 0.01).length;
  // Continuity: the message above the new one is where it was before the arrival when the frame is first painted.
  const startGap = landed.ys[landed.ids.indexOf("m40")][0] - info.before;
  // Neighbours: the smallest gap between two adjacent messages while the wave passes, against their final gap.
  let minGap = Infinity, finalGapAtMin = 0;
  for (const f of after) for (let i = 1; i < f.ids.length; i++) {
    const j = lastFrame.ids.indexOf(f.ids[i]), j0 = lastFrame.ids.indexOf(f.ids[i - 1]);
    if (j < 0 || j0 < 0 || !chain.includes(f.ids[i]) || !chain.includes(f.ids[i - 1])) continue;
    const gap = f.ys[i][0] - f.ys[i - 1][1];
    if (gap < minGap) { minGap = gap; finalGapAtMin = lastFrame.ys[j][0] - lastFrame.ys[j0][1]; }
  }
  const frames = after.slice(1).map((f, i) => f.t - after[i].t);
  report.cascade = { links: chain.length, perLinkMs: +perLink.toFixed(1), overshootPx: overshoot.map(v => +v.toFixed(1)), settledMs: +settledAt.toFixed(0), finalOffsets: finals.map(v => +v.toFixed(3)), startOffsetPx: +startGap.toFixed(1), scrollMoves, heightMoves, cls: +cls.toFixed(4), minGapPx: +minGap.toFixed(1), finalGapPx: +finalGapAtMin.toFixed(1), frameMax: +Math.max(...frames).toFixed(1) };
  console.log("    cascade", JSON.stringify(report.cascade));
  assert.ok(perLink >= 8 && perLink <= 24, `per-link delay ${perLink.toFixed(1)} ms measured over the chain (want ~${CASCADE.staggerMs})`);
  overshoot.forEach((v, k) => assert.ok(v >= 2 - 0.15 && v <= 4 + 0.05, `link ${k} overshoots by ${v.toFixed(2)} px (want 2-4)`));
  assert.ok(settledAt <= 450 + THROTTLE * 10, `settled after ${settledAt.toFixed(0)} ms (want ~400)`);
  finals.forEach(v => assert.ok(Math.abs(v) < 0.01, `a message ended ${v}px off its place`));
  assert.ok(Math.abs(startGap) <= 2, `the message above the new one jumped ${startGap.toFixed(1)}px when the block rendered`);
  assert.equal(scrollMoves, 0, `scrollTop changed on ${scrollMoves} frames after landing: the scroll must be exact`);
  assert.equal(heightMoves, 0, `scrollHeight changed on ${heightMoves} frames: the cascade must not move layout`);
  assert.ok(cls <= 0.01, `layout shift ${cls}`);
  assert.ok(Math.max(...frames) <= 34 * THROTTLE ** 0.5 + 2, `a frame took ${Math.max(...frames).toFixed(1)} ms during the cascade`);
  assert.ok(!after.some(f => f.jump), "the jump-to-latest button never showed while the pane followed");
  assert.equal(await page.evaluate(() => document.getAnimations().filter(a => a.id === "tofi-cascade").length), 0, "no cascade animation is left running");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("a reader a little above the bottom still gets the reply brought in; one far up is never moved", async () => {
  const near = await open();
  // 100 px up: inside the follow zone, even though the scroll detached them.
  await near.page.locator(".message-scroll").hover();
  await near.page.mouse.wheel(0, -100);
  await near.page.waitForTimeout(800);
  const d0 = await near.page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop; });
  assert.ok(d0 > 60 && d0 < 140, `set up ${d0}px above the bottom`);
  await near.page.evaluate(({ WHOLE }) => window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() }), { WHOLE });
  await near.page.waitForTimeout(900);
  const d1 = await near.page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop; });
  assert.ok(d1 <= 1, `the near reader ended ${d1}px from the bottom`);
  assert.equal(await near.page.locator(".jump-latest").count(), 0, "no pill for a reader who was brought along");
  assert.deepEqual(near.errors, []);
  await near.context.close();
  const far = await open();
  await far.page.locator(".message-scroll").hover();
  await far.page.mouse.wheel(0, -900);
  await far.page.waitForTimeout(500);
  const top0 = await far.page.evaluate(() => document.querySelector(".message-scroll").scrollTop);
  await far.page.evaluate(({ WHOLE }) => window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() }), { WHOLE });
  await far.page.waitForTimeout(900);
  const top1 = await far.page.evaluate(() => document.querySelector(".message-scroll").scrollTop);
  assert.ok(Math.abs(top1 - top0) <= 1, `the far reader was moved ${top1 - top0}px`);
  assert.equal(await far.page.locator(".jump-latest").count(), 1, "the far reader gets the pill");
  assert.deepEqual(far.errors, []);
  await far.context.close();
});

await check("a tall message hands the rest of the hop to the glide: no jump on the first frame, ends exactly at the bottom", async () => {
  const { page, context, errors } = await open();
  await startCascadeTrace(page);
  await page.waitForTimeout(200);
  const info = await page.evaluate(({ WHOLE }) => {
    const before = document.querySelector('[data-message-id="m40"]').getBoundingClientRect().top;
    const t = performance.now();
    window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() });
    return { t, before };
  }, { WHOLE });
  await page.waitForTimeout(1000);
  const { trace } = await stopCascadeTrace(page);
  const landed = trace.find(f => f.t >= info.t - 20 && f.ids.includes("m_w"));
  const first = landed.ys[landed.ids.indexOf("m40")][0];
  assert.ok(Math.abs(first - info.before) <= 2, `the message above the new one jumped ${(first - info.before).toFixed(1)}px on the first frame`);
  assert.ok(trace.at(-1).fb <= 0.5, `ends ${trace.at(-1).fb}px from the bottom`);
  assert.ok(!trace.filter(f => f.t >= landed.t).some(f => f.jump), "no pill while the pane followed");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("streaming growth follows with steps of at most 12 px", async () => {
  const { page, context, errors } = await open();
  const text = Array.from({ length: 14 }, (_, i) => `Synthetic streamed sentence ${i + 1} reads the weekly entries and totals them against the ledger.`).join("\n\n");
  const common = { conversation_id: "dm_a", run_id: "run_live", bot_id: "bot_a", message_id: "m_live", seq: 41, created_at: new Date().toISOString() };
  await startTrace(page);
  const t0 = await page.evaluate(() => performance.now());
  let rev = 0;
  for (let i = 0; i < text.length; i += 14) {
    rev++;
    const ch = text.slice(i, i + 14);
    await page.evaluate(({ common, ch, rev }) => window.__emit("delta", rev === 1 ? { ...common, text: ch, content: ch, revision: 1 } : { ...common, text: ch, revision: rev }), { common, ch, rev });
    await page.waitForTimeout(33);
  }
  // The reveal of buffered text outlasts the last delta; settle until the page stops growing.
  await page.waitForFunction(() => { const s = document.querySelector(".message-scroll"); const now = performance.now(); if (window.__h !== s.scrollHeight) { window.__h = s.scrollHeight; window.__hAt = now; } return now - window.__hAt > 600; }, null, { timeout: 15000, polling: 100 });
  const { trace, ls } = await stopTrace(page);
  const steps = [];
  for (let i = 1; i < trace.length; i++) if (trace[i].t >= t0 && trace[i].y != null && trace[i - 1].y != null && Math.abs(trace[i].y - trace[i - 1].y) > 0.5) steps.push(Math.abs(trace[i].y - trace[i - 1].y));
  steps.sort((a, b) => a - b);
  const p95 = steps[Math.min(steps.length - 1, Math.floor(steps.length * 0.95))] ?? 0;
  // The draft's first appearance is an arrival (a whole block, ~95 px); after that each line must be followed closely.
  const peakFb = Math.max(...trace.filter(f => f.t >= t0 + 300).map(f => f.fb));
  const endFb = trace.at(-1).fb;
  if (process.env.ARRIVAL_GLIDE_DEBUG) console.log(trace.filter(f => f.t >= t0).map(f => `${(f.t - t0).toFixed(0)}:${f.fb.toFixed(0)}${f.jump ? "J" : ""}`).join(" "));
  const cls = ls.filter(([t]) => t >= t0).reduce((sum, [, v]) => sum + v, 0);
  report.stream = { stepsMoving: steps.length, p95: +p95.toFixed(1), max: +(steps.at(-1) ?? 0).toFixed(1), fromBottomMax: +peakFb.toFixed(1), endFromBottom: +endFb.toFixed(1), cls: +cls.toFixed(4) };
  console.log("    stream", JSON.stringify(report.stream));
  assert.ok(steps.length > 10, `the pane followed the text (${steps.length} moving frames)`);
  assert.ok(p95 <= 12 * (THROTTLE > 1 ? 2 : 1), `follow step p95 ${p95}px (limit 12)`);
  assert.ok(steps.at(-1) <= 24.5 * (THROTTLE > 1 ? 2 : 1), `a follow step moved ${steps.at(-1)}px`);
  assert.ok(peakFb <= 34 + 24 * (THROTTLE > 1 ? 2 : 1), `${peakFb}px from the bottom at worst while streaming`);
  assert.ok(endFb <= 0.5, `ends at the bottom (${endFb}px)`);
  assert.ok(cls <= 0.01, `layout shift ${cls}`);
  assert.deepEqual(errors, []);
  await context.close();
});

await check("reduced motion lands in a single frame", async () => {
  const { page, context, errors } = await open({ motion: "reduce" });
  await startTrace(page);
  await page.waitForTimeout(150);
  const t0 = await page.evaluate(({ WHOLE }) => { const t = performance.now(); window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() }); return t; }, { WHOLE });
  await page.waitForTimeout(500);
  const { trace } = await stopTrace(page);
  const after = trace.filter(f => f.t >= t0 - 20);
  const moving = after.slice(1).filter((f, i) => f.y != null && after[i].y != null && Math.abs(f.y - after[i].y) > 0.5).length;
  assert.ok(moving <= 1, `the pane moved over ${moving} frames`);
  assert.ok(Math.max(...after.map(f => f.fb)) <= 0.5, "never left the bottom");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("scrolling up mid-glide abandons the hop and is not pulled back", async () => {
  const { page, context, errors } = await open();
  await page.evaluate(({ WHOLE }) => window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() }), { WHOLE });
  await page.waitForTimeout(40 * THROTTLE);
  await page.locator(".message-scroll").hover();
  await page.mouse.wheel(0, -300);
  await page.waitForTimeout(80);
  const a = await page.evaluate(() => document.querySelector(".message-scroll").scrollTop);
  await page.waitForTimeout(500);
  const b = await page.evaluate(() => document.querySelector(".message-scroll").scrollTop);
  assert.ok(Math.abs(a - b) <= 1, `the pane drifted ${b - a}px after the reader scrolled up`);
  assert.ok(await page.locator(".jump-latest").count() > 0, "the jump-to-latest button is offered");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("viewing a message's activity opens the record as a drawer, not in one frame", async () => {
  const { page, context, errors } = await open();
  const target = page.locator('[data-message-id="m39"] .message-content');
  await target.hover();
  await page.locator('[data-action="activity"]').waitFor({ timeout: 5000 });
  await page.evaluate(() => {
    window.__rec = []; window.__recOn = true;
    const tick = t => {
      const records = document.querySelector(".task-activity-records");
      window.__rec.push({ t, h: records ? records.getBoundingClientRect().height : -1, open: Boolean(document.querySelector("details.task-activity[open]")) });
      if (window.__recOn) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  const t0 = await page.evaluate(() => performance.now());
  await page.locator('[data-action="activity"]').click();
  await page.waitForTimeout(900);
  const rec = await page.evaluate(() => { window.__recOn = false; return window.__rec; });
  const grown = rec.filter(f => f.t >= t0 && f.h >= 0);
  const final = grown.at(-1)?.h ?? 0;
  // The compact Activity list has no padding or rule of its own, so the settled record can be one line (~28 px), and loading
  // rows that disappear as the page arrives make the drawer's peak taller than its settled height: judge the opening
  // against the peak, between 10% and 90% of it.
  const peak = Math.max(...grown.map(f => f.h));
  const mid = grown.filter(f => f.h > peak * 0.1 && f.h < peak * 0.9).length;
  const frames = grown.slice(1).map((f, i) => f.t - grown[i].t);
  report.reveal = { finalHeight: +final.toFixed(0), framesInBetween: mid, frameMax: +Math.max(...frames).toFixed(1) };
  console.log("    reveal", JSON.stringify(report.reveal));
  assert.ok(final > 20, `the record opened (${final}px)`);
  assert.ok(mid >= 3, `the record grew over ${mid} frames instead of appearing at once`);
  assert.ok(Math.max(...frames) <= 34 * THROTTLE ** 0.5 + 2, `a frame took ${Math.max(...frames)} ms`);
  assert.deepEqual(errors, []);
  await context.close();
});

await check("a long hop jumps the excess first and still ends at the bottom", async () => {
  const { page, context, errors } = await open();
  const big = Array.from({ length: 40 }, (_, i) => `Paragraph ${i + 1} of a very long synthetic answer that keeps going for a while.`).join("\n\n");
  await startTrace(page);
  await page.evaluate(({ big }) => window.__emit("message", { id: "m_big", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_big", content: big, created_at: new Date().toISOString() }), { big });
  await page.waitForTimeout(900);
  const { trace } = await stopTrace(page);
  assert.ok(trace.at(-1).fb <= 0.5, `ends ${trace.at(-1).fb}px from the bottom`);
  assert.deepEqual(errors, []);
  await context.close();
});

await browser.close();
server.close();
rmSync(outDir, { recursive: true, force: true });
console.log(`\n${results.length} passed, ${failures.length} failed`);
if (failures.length) process.exit(1);
