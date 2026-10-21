// Shared harness for the phone swipe-back (D35): builds the production bundle (optionally with the step 4 Motion variants
// swapped in by motionExperimentPlugin), serves it with a synthetic API, opens a 390 px touch page on a chat and drives a
// finger. Chromium gets real touch input through CDP (Input.dispatchTouchEvent: touch-action, pointercancel and scrolling
// behave as on a phone); WebKit gets synthetic touch PointerEvents dispatched in the page (it has no CDP), which exercises
// the same engine but not the browser's own pan decision. Synthetic data only.
// Callers: scripts/test-swipe-back.mjs, scripts/test-motion-experiment.mjs.
import http from "node:http";
import zlib from "node:zlib";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

export const ui = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

/** A production build like test-shell-budget's; `experiment` ("swipe", "cascade", "motion") swaps the Motion variants in. */
export async function buildUI({ experiment = "" } = {}) {
  const outDir = mkdtempSync(join(tmpdir(), "tofi-swipe-"));
  const plugins = [react()];
  if (experiment) {
    const { motionExperimentPlugin } = await import(join(ui, "motionExperimentPlugin.ts"));
    plugins.unshift(motionExperimentPlugin(experiment));
  }
  await build({ configFile: false, root: ui, plugins, build: { outDir, emptyOutDir: true }, logLevel: "error" });
  return outDir;
}

/** Entry gzip (as test-shell-budget counts it) and the gzip of every lazy chunk whose name matches `pattern`. */
export function bundleSizes(dist, pattern) {
  const html = readFileSync(join(dist, "index.html"), "utf8");
  const eager = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map(match => match[1]);
  const gz = file => zlib.gzipSync(readFileSync(join(dist, file)), { level: 9 }).length;
  const entry = eager.reduce((sum, file) => sum + gz(file), 0);
  const all = readdirSync(join(dist, "assets")).filter(name => name.endsWith(".js")).map(name => `assets/${name}`);
  const lazy = all.filter(file => !eager.includes(file) && pattern.test(file)).map(file => ({ file, gzip: gz(file) }));
  const total = all.reduce((sum, file) => sum + gz(file), 0);
  return { entry, lazy, total, files: all };
}

const mime = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".json": "application/json", ".woff2": "font/woff2", ".png": "image/png" };
export async function serve(dist) {
  const requested = [];
  const server = http.createServer((request, response) => {
    const path = new URL(request.url, "http://x").pathname;
    requested.push(path);
    let file = join(dist, path);
    if (!existsSync(file) || statSync(file).isDirectory()) file = join(dist, "index.html");
    response.writeHead(200, { "content-type": mime[extname(file)] || "application/octet-stream" });
    response.end(readFileSync(file));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return { origin: `http://127.0.0.1:${server.address().port}`, requested, close: () => { server.close(); } };
}
export const cleanup = dist => rmSync(dist, { recursive: true, force: true });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const names = ["Ledger Cat", "Simile Cat", "Mocha Cat", "Pixel Cat", "Orbit Cat", "Nimbus Cat"];
const bots = names.map((name, i) => ({ id: `bot_${i}`, name, instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${i}`, created_at: iso(0) }));
const convs = bots.map((bot, i) => ({ id: `dm_${i}`, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: iso(100 - i), read_seq: 40, user_visible: true }));
const history = conv => Array.from({ length: 40 }, (_, i) => {
  const seq = i + 1; const user = seq % 3 === 1;
  return { id: `${conv}_m${seq}`, conversation_id: conv, seq, role: user ? "user" : "assistant", content: user ? `Question ${seq}: can you check the weekly ledger?` : `Synthetic ${conv} answer ${seq} reads the weekly entries and totals them against the ledger.\n\n- first item\n- second item with \`code\`\n\n\`\`\`\nwide_code_line = "${"x".repeat(160)}"\n\`\`\``, created_at: iso(seq * 30), ...(user ? {} : { run_id: `run_${conv}_${seq}`, sender_bot_id: `bot_${conv.split("_")[1]}`, sender_bot_name: names[Number(conv.split("_")[1])] }) };
});

/** Opens the list at `width` and taps into Mocha Cat's chat (on a phone). Returns the page and a finger. */
export async function openChat(browser, engineName, origin, { width = 390, height = 844, reduced = false, cpu = 1, compact = false, init } = {}) {
  const touch = width < 768;
  const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", hasTouch: true, isMobile: touch && engineName === "chromium", deviceScaleFactor: 2 });
  await context.addInitScript(() => {
    try { localStorage.setItem("tofi:appearance", "light"); } catch {}
    // A controllable stream: window.__emit(type, data) delivers an event as the server would.
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
  });
  if (init) await context.addInitScript(init);
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
    if (url.pathname.endsWith("/messages")) return json({ messages: history(conv).map(m => compact ? { ...m, content: m.role === "user" ? `Question ${m.seq}: short.` : `Short answer ${m.seq}.` } : m), drafts: [], has_more: false, event_cursor: 0 });
    if (url.pathname.endsWith("/runs")) return json({ runs: [] });
    if (url.pathname.endsWith("/tools")) return json(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (url.pathname.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (url.pathname === "/api/questions") return json({ questions: [] });
    if (url.pathname === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  await page.goto(origin + (touch ? "/" : "/b/bot_2"));
  await page.locator(".conversation-row").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  if (touch) {
    await page.waitForTimeout(400);
    await page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap();
  }
  await page.locator(".message-block").first().waitFor();
  await page.waitForTimeout(900);
  const cdp = engineName === "chromium" ? await context.newCDPSession(page) : null;
  if (cpu > 1 && cdp) await cdp.send("Emulation.setCPUThrottlingRate", { rate: cpu });
  return { page, context, errors, finger: makeFinger(page, cdp), cdp };
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
/** A finger: down/move/up at viewport coordinates. Real touch through CDP when there is one, else in-page PointerEvents. */
function makeFinger(page, cdp) {
  if (cdp) return {
    down: (x, y) => cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y, id: 1 }] }),
    move: (x, y) => cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y, id: 1 }] }),
    up: () => cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] }),
  };
  const fire = (type, x, y) => page.evaluate(({ type, x, y }) => {
    if (type === "pointerdown") window.__fingerTarget = document.elementFromPoint(x, y);
    const target = window.__fingerTarget ?? document.body;
    window.__fingerAt = [x, y];
    target.dispatchEvent(new PointerEvent(type, { pointerId: 7, pointerType: "touch", isPrimary: true, clientX: x, clientY: y, bubbles: true, cancelable: true, composed: true, button: type === "pointermove" ? -1 : 0, buttons: type === "pointerup" ? 0 : 1 }));
    if (type === "pointerup") target.dispatchEvent(new MouseEvent("click", { clientX: x, clientY: y, bubbles: true, cancelable: true }));
  }, { type, x, y });
  let last = [0, 0];
  return {
    down: (x, y) => { last = [x, y]; return fire("pointerdown", x, y); },
    move: (x, y) => { last = [x, y]; return fire("pointermove", x, y); },
    up: () => fire("pointerup", ...last),
  };
}

/**
 * Drives the finger through `path` ([[x, y, holdMs], ...]): down at the first point, one move per later point (each held
 * for its ms, default 16), up after the last unless `lift` is false.
 */
export async function drag(finger, path, { lift = true } = {}) {
  const [x0, y0] = path[0];
  await finger.down(x0, y0);
  for (const [x, y, hold = 16] of path.slice(1)) { await sleep(hold); await finger.move(x, y); }
  if (lift) await finger.up();
}
/**
 * A fast flick: input timing on a loaded machine can bunch CDP moves and delay the lift, which a real finger never does;
 * a try whose measured release speed (as the gesture sees it) is under `min` px/ms was not a flick and is driven again.
 */
export async function flick(page, finger, path, { min = 0.6, tries = 4, reset } = {}) {
  for (let i = 1; ; i++) {
    await startTrace(page);
    await drag(finger, path);
    const speed = await fingerSpeed(page);
    if (Math.abs(speed) >= min || i >= tries) return speed;
    await page.waitForTimeout(700);
    await stopTrace(page);
    await reset?.();
  }
}
/** Back on the chat if a try ended on the list. */
export async function reopenChat(page) {
  if (await page.evaluate(() => document.querySelector(".chat-pane").classList.contains("mobile-chat"))) return;
  await page.locator(".conversation-row").filter({ hasText: "Mocha Cat" }).first().tap();
  await page.waitForTimeout(700);
}
/** Points from (x0,y) to (x1,y) in `steps` moves, `hold` ms apart. */
export const line = (x0, x1, y, steps, hold = 16) => Array.from({ length: steps + 1 }, (_, i) => [x0 + (x1 - x0) * i / steps, y, hold]);

/** Starts sampling every frame: chat and list x, visibilities, dim, finger x (last pointermove), swipe state. */
export const startTrace = page => page.evaluate(() => {
  window.__tr = []; window.__trOn = true; window.__lastPointer = null; window.__moves = 0; window.__downs = 0; window.__ev = []; window.__upAt = null;
  if (!window.__trHooked) {
    window.__trHooked = true;
    window.addEventListener("pointermove", event => { window.__lastPointer = event.clientX; window.__moves++; window.__ev.push([event.timeStamp, event.clientX]); }, { capture: true, passive: true });
    window.addEventListener("pointerdown", event => { window.__lastPointer = event.clientX; window.__downs++; }, { capture: true, passive: true });
    window.addEventListener("pointerup", event => { window.__lastPointer = null; window.__upAt = event.timeStamp; }, { capture: true, passive: true });
    window.addEventListener("pointercancel", () => { window.__pointercancel = (window.__pointercancel ?? 0) + 1; window.__lastPointer = null; }, { capture: true, passive: true });
  }
  const t0 = performance.now();
  const tick = now => {
    const chat = document.querySelector(".chat-pane"), list = document.querySelector(".contact-pane");
    const c = chat.getBoundingClientRect(), l = list.getBoundingClientRect();
    window.__tr.push({ t: now - t0, chat: c.x, w: c.width, list: l.x, lw: l.width, cv: getComputedStyle(chat).visibility, lv: getComputedStyle(list).visibility, dim: Number(getComputedStyle(list, "::after").opacity), finger: window.__lastPointer, swipe: chat.dataset.swipe ?? "", scroll: document.querySelector(".chat-pane .message-scroll")?.scrollTop ?? 0 });
    if (window.__trOn) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
});
export const stopTrace = page => page.evaluate(() => { window.__trOn = false; return window.__tr; });
/** The finger's speed (px/ms) over the 80 ms before it lifted (or before its last move), as the gesture measures it. */
export const fingerSpeed = page => page.evaluate(() => {
  const ev = window.__ev ?? [];
  const end = window.__upAt ?? ev.at(-1)?.[0];
  const recent = end == null ? [] : ev.filter(([t]) => t >= end - 80 && t <= end);
  return recent.length > 1 ? (recent.at(-1)[1] - recent[0][1]) / (recent.at(-1)[0] - recent[0][0]) : 0;
});

/** The page's settled state: which pane shows, and whether the gesture left anything behind. */
export const restState = page => page.evaluate(() => {
  const chat = document.querySelector(".chat-pane"), list = document.querySelector(".contact-pane");
  return {
    chatShown: chat.classList.contains("mobile-chat"), listOpen: list.classList.contains("open"),
    chatX: chat.getBoundingClientRect().x, listX: list.getBoundingClientRect().x,
    cv: getComputedStyle(chat).visibility, lv: getComputedStyle(list).visibility, dim: Number(getComputedStyle(list, "::after").opacity),
    inline: chat.style.transform + list.style.transform, swipe: (chat.dataset.swipe ?? "") + (list.dataset.swipe ?? ""),
    animations: document.getAnimations().filter(a => a.id === "tofi-swipe" || a.effect?.pseudoElement === "::after").length,
    panel: Boolean(document.querySelector(".detail-pane.visible, .detail-pane[data-open=true]")),
  };
});

/** Frame times only (no layout reads), so the cost measured is the gesture's and not the probe's. */
export const startFrames = page => page.evaluate(() => {
  window.__ft = []; window.__ftOn = true; window.__moves = 0; window.__down = false;
  if (!window.__ftHooked) {
    window.__ftHooked = true;
    window.addEventListener("pointermove", () => { window.__moves++; }, { capture: true, passive: true });
    window.addEventListener("pointerdown", () => { window.__down = true; }, { capture: true, passive: true });
    window.addEventListener("pointerup", () => { window.__down = false; }, { capture: true, passive: true });
  }
  const tick = now => { window.__ft.push([now, document.querySelector(".chat-pane").dataset.swipe === "drag" || window.__down]); if (window.__ftOn) requestAnimationFrame(tick); };
  requestAnimationFrame(tick);
});
export const stopFrames = page => page.evaluate(() => { window.__ftOn = false; return window.__ft; });
/** One swipe back at whatever CPU rate the page has: frame gaps from the touch to the commit, and CDP script and layout
 *  totals for the drag (finger down) and for the release (lift to the moment the gesture has handed the panes back). */
export async function measureCost(page, finger, cdp, { y = 420 } = {}) {
  await cdp.send("Performance.enable");
  const metric = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(m => [m.name, m.value]));
  await startFrames(page);
  const m0 = await metric();
  await drag(finger, line(40, 300, y, 26), { lift: false });
  const m1 = await metric();
  await finger.up();
  await page.waitForFunction(() => !document.querySelector(".chat-pane").dataset.swipe && !document.querySelector(".contact-pane").dataset.swipe, null, { timeout: 4000 });
  const m2 = await metric();
  await page.waitForTimeout(100);
  const frames = await stopFrames(page);
  const moves = await page.evaluate(() => window.__moves);
  const active = frames.map(([t, on], k) => ({ t, on, k })).filter(f => f.on);
  const inside = frames.slice(Math.max(0, active[0].k - 1), active.at(-1).k + 3);
  const gaps = inside.slice(1).map(([t], k) => t - inside[k][0]);
  return { slow: gaps.filter(gap => gap > 25).length, worst: Math.max(...gaps), moves,
    dragScriptPerMove: (m1.ScriptDuration - m0.ScriptDuration) * 1000 / Math.max(1, moves), dragLayouts: m1.LayoutCount - m0.LayoutCount,
    settleScript: (m2.ScriptDuration - m1.ScriptDuration) * 1000, settleMs: (m2.Timestamp - m1.Timestamp) * 1000, settleLayouts: m2.LayoutCount - m1.LayoutCount };
}

