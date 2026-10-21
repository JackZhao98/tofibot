// Phone swipe-back (D33, D35; src/phoneSwipeGesture.ts + src/phoneSwipeEngine.ts), in the production bundle at 390 px:
//  - the chat follows the finger 1:1 and the list follows the chat exactly as in the tap push (22% shift, dim), with no
//    frame where a pane is missing;
//  - released past half it completes, short of half it springs back, a fast flick right completes from anywhere, a fast
//    flick left cancels from anywhere; dragged left of its start it resists (rubber band, at most 10% of the width);
//  - it leaves the finger with the finger's velocity (no jump, no stall at release) and a finger that lands mid-flight
//    catches the pane where it is;
//  - vertical moves still scroll and never move the pane; a text field, a sideways-scrolled code block and a wide window
//    never start it; a drag that began on a button does not click it;
//  - afterwards nothing is left behind (inline styles, animations) and the tap push still runs its 280 / 220 ms;
//  - reduced motion: the pane still follows the finger, a release lands at once;
//  - cost: at 4x CPU, at most one frame over 25 ms while dragging and settling (the push's own gate), and the gesture's
//    script per finger move is measured; the engine is a lazy chunk, fetched on a phone-sized window only.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:swipe-back
// SWIPE_BACK_ENGINE=webkit runs it in WebKit (synthetic touch PointerEvents; the browser-pan checks are Chromium only).
// SWIPE_BACK_DIST=/path/to/dist measures an existing build.
import assert from "node:assert/strict";
import { buildUI, bundleSizes, cleanup, drag, flick, line, measureCost, openChat, reopenChat, restState, serve, startTrace, stopTrace } from "./lib/swipe-harness.mjs";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const engineName = process.env.SWIPE_BACK_ENGINE === "webkit" ? "webkit" : "chromium";
const chromium = engineName === "chromium";
const ownDist = !process.env.SWIPE_BACK_DIST;
const dist = process.env.SWIPE_BACK_DIST || await buildUI();
const sizes = bundleSizes(dist, /phoneSwipe/);
const server = await serve(dist);
const browser = await pw[engineName].launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const W = 390, Y = 420;
const results = [], failures = [];
const check = async (name, run, { only } = {}) => {
  if (only === "chromium" && !chromium) return;
  if (process.env.SWIPE_BACK_ONLY && !name.includes(process.env.SWIPE_BACK_ONLY)) return;
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n").slice(0, 3).join("\n    ")}`); }
};
const settle = page => page.waitForTimeout(650);
/** Frames from the first in which the pane is off its rest, to the end. */
const moving = trace => trace.filter(frame => frame.swipe === "drag");
/** Every frame paints a pane; while the chat is partway across, the list is painted under it and reaches its edge. */
function coverage(trace, label) {
  for (const f of trace) {
    assert.ok(f.cv === "visible" || f.lv === "visible", `${label}: a pane is painted on every frame (t=${f.t.toFixed(0)})`);
    if (f.cv === "visible" && f.chat > 0.5 && f.chat < W - 0.5) {
      assert.equal(f.lv, "visible", `${label}: the list is painted under the moving chat (t=${f.t.toFixed(0)}, chat ${f.chat.toFixed(1)})`);
      assert.ok(f.list + f.lw >= f.chat - 0.5, `${label}: the list reaches the chat's edge (t=${f.t.toFixed(0)})`);
      assert.ok(f.list <= 0.5 && f.list >= -0.22 * W - 1, `${label}: the list shifts by at most 22% (t=${f.t.toFixed(0)}: ${f.list.toFixed(1)})`);
      // The same picture as the tap push at this point: list offset and dim follow the chat's progress.
      const p = f.chat / W;
      assert.ok(Math.abs(f.list - -0.22 * W * (1 - p)) <= 1.5, `${label}: the list follows the chat (t=${f.t.toFixed(0)}: list ${f.list.toFixed(1)}, want ${(-0.22 * W * (1 - p)).toFixed(1)})`);
      assert.ok(Math.abs(f.dim - (1 - p)) <= 0.06, `${label}: the dim follows the chat (t=${f.t.toFixed(0)}: ${f.dim.toFixed(2)}, want ${(1 - p).toFixed(2)})`);
    }
  }
}
function clean(state, label) {
  assert.equal(state.inline, "", `${label}: no inline transform left`);
  assert.equal(state.swipe, "", `${label}: no data-swipe left`);
  assert.equal(state.animations, 0, `${label}: no swipe animation left`);
}
/** Largest per-frame travel of the chat after the finger lifted, and the travel in the first frame after it. */
function afterRelease(trace) {
  const i = trace.findIndex((f, k) => k > 0 && trace[k - 1].finger != null && f.finger == null);
  return { index: i, frames: i < 0 ? [] : trace.slice(i - 1) };
}

await check("the engine is a lazy chunk, fetched on a phone-sized window only", async () => {
  assert.equal(sizes.lazy.length, 1, `one phoneSwipeEngine chunk (${sizes.lazy.map(c => c.file).join(", ")})`);
  console.log(`    entry ${sizes.entry} B gzip; engine chunk ${sizes.lazy[0].gzip} B gzip`);
  assert.ok(sizes.lazy[0].gzip <= 3000, `the engine chunk is small (${sizes.lazy[0].gzip} B)`);
  assert.ok(!sizes.files.some(file => /motion|framer/i.test(file) && !/motion-lab/.test(file)), "no Motion chunk in the build");
  const before = server.requested.length;
  const wide = await openChat(browser, engineName, server.origin, { width: 1280, height: 860 });
  await wide.page.waitForTimeout(800);
  assert.ok(!server.requested.slice(before).some(path => path.includes("phoneSwipeEngine")), "a wide window does not fetch the engine");
  await wide.context.close();
  const phone = await openChat(browser, engineName, server.origin);
  assert.ok(server.requested.some(path => path.includes("phoneSwipeEngine")), "a phone-sized window fetches it in idle time");
  await phone.context.close();
});

await check("past half and released at rest, the chat goes back to the list, following the finger 1:1 on the way", async () => {
  const { page, context, errors, finger } = await openChat(browser, engineName, server.origin);
  await startTrace(page);
  await drag(finger, [...line(40, 300, Y, 26), [300, Y, 140]]);
  await settle(page);
  const trace = await stopTrace(page);
  const drags = moving(trace);
  assert.ok(drags.length >= 8, `the chat is seen moving under the finger (${drags.length} frames)`);
  // 1:1 tracking: while the finger is down, the chat is offset from the finger by one constant (where the drag began).
  const held = drags.filter(f => f.finger != null);
  const lead = held.map(f => f.finger - f.chat);
  const spread = Math.max(...lead) - Math.min(...lead);
  assert.ok(spread <= 1.5, `the chat tracks the finger 1:1 (finger - chat varies by ${spread.toFixed(1)} px)`);
  coverage(trace, "swipe back");
  const state = await restState(page);
  assert.ok(state.listOpen && !state.chatShown, "the list is open, as after the back button");
  assert.equal(state.listX, 0); assert.equal(state.dim, 0); assert.equal(state.cv, "hidden"); assert.equal(state.lv, "visible");
  clean(state, "swipe back");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("short of half and released at rest, it springs back to the chat", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  await startTrace(page);
  await drag(finger, [...line(40, 160, Y, 14), [160, Y, 140]]);
  await settle(page);
  const trace = await stopTrace(page);
  coverage(trace, "spring back");
  const { frames } = afterRelease(trace);
  assert.ok(frames.some(f => f.chat > 10 && f.chat < 100), "it travels back over several frames, not at once");
  const state = await restState(page);
  assert.ok(state.chatShown && !state.listOpen, "still on the chat");
  assert.equal(state.chatX, 0); assert.equal(state.lv, "hidden", "the list is released again");
  clean(state, "spring back");
  await context.close();
});

await check("a fast flick right completes from a short distance, and the pane leaves with the finger's speed", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  // Fast moves (24 px each), lifted short of half the width (flick() drives it again if the input arrived too bunched).
  const finger_ = await flick(page, finger, line(40, 160, Y, 5, 10));
  await settle(page);
  const trace = await stopTrace(page);
  const state = await restState(page);
  assert.ok(state.listOpen && !state.chatShown, `a flick completes (finger ${finger_.toFixed(2)} px/ms; chat ${state.chatShown ? "still shown" : "gone"})`);
  clean(state, "flick");
  const { frames } = afterRelease(trace);
  assert.ok(frames[0].chat < 0.5 * W, `it was released short of half (${frames[0].chat.toFixed(0)} px)`);
  const first = frames.slice(1, 3);
  const leaveSpeed = (first.at(-1).chat - frames[0].chat) / Math.max(1, first.at(-1).t - frames[0].t);
  console.log(`    finger ${finger_.toFixed(2)} px/ms at release, pane over the next two frames ${leaveSpeed.toFixed(2)} px/ms`);
  assert.ok(leaveSpeed >= finger_ * 0.8, `the pane keeps at least the finger's speed at release (${leaveSpeed.toFixed(2)} vs ${finger_.toFixed(2)} px/ms)`);
  coverage(trace, "flick");
  await context.close();
});

await check("a fast flick left cancels even past half", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  const speed = await flick(page, finger, [...line(40, 280, Y, 22), [280, Y, 80], ...line(280, 220, Y, 4, 12).slice(1)], { reset: () => reopenChat(page) });
  await stopTrace(page);
  await settle(page);
  assert.ok(speed <= -0.5, `the finger flicked left (${speed.toFixed(2)} px/ms)`);
  const state = await restState(page);
  assert.ok(state.chatShown && !state.listOpen, "a leftward flick returns to the chat");
  assert.equal(state.chatX, 0);
  clean(state, "flick left");
  await context.close();
});

await check("dragged left of its start, the chat resists and gives at most 10% of the width; it returns on release", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  await startTrace(page);
  await drag(finger, [...line(100, 140, Y, 4), ...line(140, 10, Y, 14).slice(1), [10, Y, 60]]);
  await settle(page);
  const trace = await stopTrace(page);
  const left = moving(trace).filter(f => f.finger != null && f.chat < 0);
  assert.ok(left.length >= 3, `the chat is pulled left of its place (${left.length} frames)`);
  const deepest = Math.min(...left.map(f => f.chat));
  const pulled = Math.max(...left.map(f => (f.finger - 110) * -1));
  console.log(`    finger ${pulled.toFixed(0)} px left of the start, chat ${deepest.toFixed(1)} px`);
  assert.ok(-deepest < pulled * 0.5, `it resists: ${(-deepest).toFixed(1)} px for ${pulled.toFixed(0)} px of finger`);
  assert.ok(deepest >= -0.1 * W - 0.5, `at most 10% of the width (${deepest.toFixed(1)})`);
  const state = await restState(page);
  assert.ok(state.chatShown && state.chatX === 0, "it returns to its place");
  clean(state, "rubber band");
  await context.close();
});

await check("a finger that lands mid-flight catches the pane where it is and can take it back", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  await startTrace(page);
  await drag(finger, [...line(40, 270, Y, 20), [270, Y, 120]]);
  await page.waitForTimeout(chromium ? 50 : 30);
  const caught = await page.evaluate(() => document.querySelector(".chat-pane").getBoundingClientRect().x);
  await drag(finger, [[caught + 30, Y + 40], ...line(caught + 30, caught - 150, Y + 40, 16).slice(1), [caught - 150, Y + 40, 140]]);
  await settle(page);
  const trace = await stopTrace(page);
  const state = await restState(page);
  assert.ok(caught > 240 && caught < W - 2, `it was caught partway (${caught.toFixed(1)})`);
  assert.ok(state.chatShown && !state.listOpen && state.chatX === 0, "taken back, it stays on the chat");
  clean(state, "catch");
  // No jump at the catch: around the second touch, no frame moves the chat further than the release itself did per frame.
  let worst = 0;
  for (let i = 1; i < trace.length; i++) worst = Math.max(worst, Math.abs(trace[i].chat - trace[i - 1].chat));
  console.log(`    caught at ${caught.toFixed(1)} px; largest per-frame step ${worst.toFixed(1)} px`);
  assert.ok(worst <= 60, `no frame jumps (largest step ${worst.toFixed(1)} px)`);
  coverage(trace, "catch");
  await context.close();
});

await check("a vertical move scrolls the messages and never moves the pane", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  const before = await page.evaluate(() => document.querySelector(".chat-pane .message-scroll").scrollTop);
  await startTrace(page);
  await drag(finger, Array.from({ length: 16 }, (_, i) => [200 + i * 0.6, 300 + i * 18, 16]));
  await page.waitForTimeout(400);
  const trace = await stopTrace(page);
  assert.ok(trace.every(f => Math.abs(f.chat) < 0.01 && f.swipe === ""), "the pane never moved");
  const after = await page.evaluate(() => document.querySelector(".chat-pane .message-scroll").scrollTop);
  assert.ok(Math.abs(after - before) > 100, `the messages scrolled (${before} -> ${after})`);
  await context.close();
}, { only: "chromium" });

await check("a horizontal drag is not taken by the browser (no pointercancel); a sideways-scrolled code block keeps its own drag", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  await page.evaluate(() => { window.__pointercancel = 0; });
  await drag(finger, line(40, 200, Y, 16), { lift: false });
  await page.waitForTimeout(120);
  const held = await page.evaluate(() => ({ cancelled: window.__pointercancel ?? 0, x: document.querySelector(".chat-pane").getBoundingClientRect().x }));
  await finger.up();
  await settle(page);
  assert.equal(held.cancelled, 0, "the browser left the horizontal drag to the page");
  assert.ok(held.x > 100, `the pane is still under the finger while it is held (${held.x.toFixed(1)} px)`);
  // A code block scrolled sideways: a drag that starts on it scrolls it back instead of moving the pane.
  const box = await page.evaluate(() => {
    const pres = [...document.querySelectorAll(".chat-pane pre")].filter(pre => pre.scrollWidth > pre.clientWidth);
    const pre = pres.find(p => { const r = p.getBoundingClientRect(); return r.top > 120 && r.bottom < innerHeight - 160; });
    if (!pre) return null;
    pre.scrollLeft = 80;
    const r = pre.getBoundingClientRect();
    return { x: r.x + 30, y: r.y + r.height / 2 };
  });
  assert.ok(box, "a sideways-scrollable code block is in view");
  await startTrace(page);
  await drag(finger, [...line(box.x, box.x + 120, box.y, 10), [box.x + 120, box.y, 120]]);
  await settle(page);
  const trace = await stopTrace(page);
  assert.ok(trace.every(f => Math.abs(f.chat) < 0.01), "the pane stayed while the code block was dragged");
  await context.close();
}, { only: "chromium" });

await check("a text field, a wide window and a mouse never start it; a drag that began on a button does not click it", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  const field = await page.locator(".composer textarea").boundingBox();
  await startTrace(page);
  await drag(finger, [...line(field.x + 20, field.x + 260, field.y + field.height / 2, 18), [field.x + 260, field.y + field.height / 2, 100]]);
  await settle(page);
  let trace = await stopTrace(page);
  assert.ok(trace.every(f => Math.abs(f.chat) < 0.01), "a drag in the composer never moves the pane");
  // Mouse: no gesture.
  await page.mouse.move(60, Y); await page.mouse.down(); for (let x = 60; x <= 320; x += 20) await page.mouse.move(x, Y); await page.mouse.up();
  await settle(page);
  assert.ok((await restState(page)).chatShown, "a mouse drag is not a swipe");
  // A drag from the header's identity button completes the swipe and opens nothing.
  const identity = await page.locator(".chat-identity").boundingBox();
  await drag(finger, [...line(identity.x + 10, identity.x + 300, identity.y + identity.height / 2, 22), [identity.x + 300, identity.y + identity.height / 2, 120]]);
  await settle(page);
  const state = await restState(page);
  assert.ok(state.listOpen, "the swipe completed");
  assert.ok(!state.panel, "the button under the finger was not clicked");
  await context.close();
  const wide = await openChat(browser, engineName, server.origin, { width: 1000, height: 800 });
  await startTrace(wide.page);
  await drag(wide.finger, [...line(420, 760, 420, 20), [760, 420, 100]]);
  await settle(wide.page);
  trace = await stopTrace(wide.page);
  assert.ok(trace.every(f => f.swipe === ""), "no swipe on a wide window");
  await wide.context.close();
});

await check("after a swipe the tap push still runs its own 280 / 220 ms and lands exactly", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin);
  await drag(finger, [...line(40, 300, Y, 26), [300, Y, 140]]);
  await settle(page);
  const sample = async action => {
    await page.evaluate(() => {
      window.__ends = {};
      for (const sel of [".chat-pane", ".contact-pane"]) { const el = document.querySelector(sel); el.addEventListener("transitionend", e => { if (e.target === el && e.propertyName === "transform") window.__ends[sel] = e.elapsedTime * 1000; }); }
    });
    await action();
    await page.waitForTimeout(600);
    return page.evaluate(() => window.__ends);
  };
  const into = await sample(() => page.locator(".conversation-row").filter({ hasText: "Pixel Cat" }).first().tap());
  assert.equal(into[".chat-pane"], 280, `tap push in still takes 280 ms (${JSON.stringify(into)})`);
  assert.equal(into[".contact-pane"], 280);
  let state = await restState(page);
  assert.ok(state.chatShown && state.chatX === 0 && state.dim === 1, "lands on the chat with the list dimmed under it");
  const back = await sample(() => page.locator(".back-button").tap());
  assert.equal(back[".chat-pane"], 220, `tap back still takes 220 ms (${JSON.stringify(back)})`);
  state = await restState(page);
  assert.ok(state.listOpen && state.listX === 0 && state.dim === 0);
  clean(state, "tap after swipe");
  await context.close();
});

await check("reduced motion: the chat follows the finger, a release lands at once", async () => {
  const { page, context, finger } = await openChat(browser, engineName, server.origin, { reduced: true });
  await startTrace(page);
  await drag(finger, [...line(40, 300, Y, 26), [300, Y, 140]]);
  await settle(page);
  const trace = await stopTrace(page);
  assert.ok(moving(trace).filter(f => f.finger != null).length >= 8, "the pane follows the finger");
  const { frames } = afterRelease(trace);
  const travelling = frames.slice(1).filter(f => f.cv === "visible" && f.chat > 0.5 && f.chat < W - 0.5 && f.finger == null);
  assert.ok(travelling.length <= 1, `no travel after the release (${travelling.length} frames)`);
  const state = await restState(page);
  assert.ok(state.listOpen, "it completed");
  clean(state, "reduced motion");
  await context.close();
});

await check("cost at 4x CPU: at most one frame over 25 ms while dragging and settling; script and layout per finger move", async () => {
  // Frame timings on a shared machine: a run that misses is measured again, up to four runs; one clean run passes.
  const runs = [];
  const good = r => r.slow <= 1 && r.dragScriptPerMove <= 1.5 && r.dragLayouts <= 8;
  for (let i = 0; i < 4 && !runs.some(good); i++) {
    const { page, context, finger, cdp } = await openChat(browser, engineName, server.origin, { cpu: 4 });
    runs.push(await measureCost(page, finger, cdp, { y: Y }));
    await context.close();
  }
  console.log(`    4x: frames over 25 ms ${runs.map(r => r.slow).join(", ")}; worst frame ${runs.map(r => r.worst.toFixed(0)).join(", ")} ms; drag: script per move ${runs.map(r => r.dragScriptPerMove.toFixed(2)).join(", ")} ms, layouts ${runs.map(r => r.dragLayouts).join(", ")} over ${runs.map(r => r.moves).join(", ")} moves; release to commit: script ${runs.map(r => r.settleScript.toFixed(1)).join(", ")} ms`);
  const best = runs.find(good) ?? runs.reduce((a, b) => (a.slow <= b.slow ? a : b));
  assert.ok(best.slow <= 1, `at most one frame over 25 ms at 4x (${runs.map(r => r.slow).join(", ")})`);
  assert.ok(best.dragScriptPerMove <= 1.5, `the page's script per finger move stays under 1.5 ms at 4x (${runs.map(r => r.dragScriptPerMove.toFixed(2)).join(", ")} ms)`);
  assert.ok(best.dragLayouts <= 8, `dragging lays out a few times in all, not once per move (${runs.map(r => r.dragLayouts).join(", ")})`);
}, { only: "chromium" });

await browser.close();
server.close();
if (ownDist) cleanup(dist);
console.log(`\n${results.length} passed, ${failures.length} failed (${engineName})`);
if (failures.length) process.exit(1);
console.log(`swipe back (${engineName}): PASS`);
