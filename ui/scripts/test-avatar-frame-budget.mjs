// Idle cat animation budget: N mounted small cats must share one animation-frame pump and step at ~24 fps,
// instead of one 60 fps rAF loop (full SVG path recompute) per cat. Measured in real Chromium.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:avatar-frame-budget
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await pw.chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
try {
  const page = await browser.newPage({ viewport: { width: 800, height: 600 } });
  await page.route("**/blank.html", route => route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>blank</title><body></body>" }));
  await page.goto(`${origin}/blank.html`);
  const result = await page.evaluate(async () => {
    let registered = 0;
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (fn) => { registered++; return raf(fn); };
    const { mountCat } = await import("/src/lib/tofi-avatar/index.js");
    const root = document.createElement("div");
    root.style.cssText = "position:fixed;left:0;top:0";
    document.body.append(root);
    const hosts = [];
    for (let i = 0; i < 20; i++) {
      const host = document.createElement("span");
      host.style.cssText = "display:inline-block;width:25px;height:25px";
      root.append(host);
      hosts.push(host);
    }
    const cats = hosts.map(host => mountCat(host, { initialState: "awake" }));
    let steps = 0;
    const observer = new MutationObserver(records => { for (const r of records) if (r.target === hosts[0].querySelector("path")) steps++; });
    observer.observe(hosts[0], { attributes: true, attributeFilter: ["d"], subtree: true });
    await new Promise(r => setTimeout(r, 300));
    registered = 0; steps = 0;
    const t0 = performance.now();
    await new Promise(r => setTimeout(r, 2000));
    const seconds = (performance.now() - t0) / 1000;
    cats.forEach(cat => cat.destroy());
    return { rafPerSecond: registered / seconds, firstPathUpdatesPerSecond: steps / seconds };
  });
  console.log(JSON.stringify(result));
  assert.ok(result.rafPerSecond <= 75, `20 cats must share one frame pump, got ${result.rafPerSecond.toFixed(0)} rAF/s`);
  assert.ok(result.firstPathUpdatesPerSecond <= 30, `a 25 px cat must step at <= ~24 fps, got ${result.firstPathUpdatesPerSecond.toFixed(1)}/s`);

  // Idle pause: no input for IDLE_PAUSE_MS settles every cat and the shared pump goes quiet; input resumes within a frame.
  const idle = await page.evaluate(async () => {
    const { mountCat, IDLE_PAUSE_MS, setIdlePauseDelay, isIdlePaused, holdAvatarLife } = await import("/src/lib/tofi-avatar/index.js");
    let registered = 0;
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (fn) => { registered++; return raf(fn); };
    const root = document.createElement("div");
    root.style.cssText = "position:fixed;left:0;top:0";
    document.body.append(root);
    const hosts = Array.from({ length: 37 }, () => { const h = document.createElement("span"); h.style.cssText = "display:inline-block;width:25px;height:25px"; root.append(h); return h; });
    setIdlePauseDelay(700);
    const cats = hosts.map((host, i) => mountCat(host, { initialState: i % 2 ? "awake" : "asleep", autoplay: true }));
    const wait = ms => new Promise(r => setTimeout(r, ms));
    const rate = async ms => { registered = 0; const t0 = performance.now(); await wait(ms); return registered / ((performance.now() - t0) / 1000); };
    const before = await rate(400);
    await wait(1000);
    const pausedFlag = isIdlePaused();
    const paused = await rate(1000);
    // A clip is not part of the life layer: it plays to its end while paused.
    const clip = await Promise.race([cats[0].play("slow-blink").then(r => r.cancelled ? "cancelled" : "done"), wait(5000).then(() => "stalled")]);
    registered = 0;
    document.dispatchEvent(new PointerEvent("pointermove", { bubbles: true }));
    await new Promise(r => raf(() => r()));
    await new Promise(r => raf(() => r()));
    const resumedFlag = !isIdlePaused();
    const resumed = await rate(500);
    // A page transition holds the life layer: no path is rewritten while held, and the cat carries on afterwards.
    let writes = 0;
    const watcher = new MutationObserver(records => { writes += records.length; });
    watcher.observe(root, { attributes: true, attributeFilter: ["d"], subtree: true });
    document.dispatchEvent(new PointerEvent("pointermove", { bubbles: true }));
    holdAvatarLife(450);
    await wait(60); writes = 0;
    await wait(300);
    const heldWrites = writes;
    document.dispatchEvent(new PointerEvent("pointermove", { bubbles: true }));
    await wait(200); writes = 0;
    await wait(300);
    const afterHoldWrites = writes;
    watcher.disconnect();
    // Still idle again later: it settles a second time.
    await wait(1100);
    const settledAgain = isIdlePaused();
    cats.forEach(cat => cat.destroy());
    return { IDLE_PAUSE_MS, before, pausedFlag, paused, clip, resumedFlag, resumed, heldWrites, afterHoldWrites, settledAgain };
  });
  console.log(JSON.stringify(idle));
  assert.equal(idle.IDLE_PAUSE_MS, 20000, "cats settle after 20 s without input");
  assert.ok(idle.before >= 20, `37 cats animate while there is input recently, got ${idle.before.toFixed(0)} rAF/s`);
  assert.ok(idle.pausedFlag, "the idle timer flags the pause");
  assert.ok(idle.paused <= 2, `no frames are scheduled once idle, got ${idle.paused.toFixed(1)} rAF/s`);
  assert.equal(idle.clip, "done", "a playing clip is not held by the idle pause");
  assert.ok(idle.resumedFlag, "input clears the pause");
  assert.ok(idle.resumed >= 20, `input resumes the life layer, got ${idle.resumed.toFixed(0)} rAF/s`);
  assert.equal(idle.heldWrites, 0, "no path is rewritten while a page transition holds the cats");
  assert.ok(idle.afterHoldWrites > 20, `the cats carry on once the hold lapses, got ${idle.afterHoldWrites} path writes`);
  assert.ok(idle.settledAgain, "cats settle again after the next quiet stretch");
  console.log("avatar frame budget: PASS");
} finally {
  await browser.close();
  await server.close();
}
