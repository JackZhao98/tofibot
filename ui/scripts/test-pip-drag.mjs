// The Bot-computer mini window's drag cost (docs/design/computer-pip.md sections 5.2, 7, 13): at 4x CPU slowdown, a 600 px drag does
// no layout, one style update per frame (the transform write), commits React zero times, spends little script per frame; the release settles within 300 ms and,
// once at rest, no frame callback of the engine is left running.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:pip-drag
import assert from "node:assert/strict";
import { installStubs, launch, startUi } from "./computer-window-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
// The page's own ambient animation (the cats) lays out every frame, so the drag cost is measured with reduced motion on (no ambient loop,
// the drag path is the same: only the spring and the rubber band are off); the settle and the idle check run with motion on.
async function run(reduced) {
  const context = await browser.newContext({ viewport: { width: 1024, height: 768 }, reducedMotion: reduced ? "reduce" : "no-preference" });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await context.addInitScript(() => {
    // Frame callbacks asked for by the engine (its stack names pipDrag), so an idle window can be shown to run none.
    window.__pipFrames = 0;
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = callback => { if (new Error().stack?.includes("pipDrag")) window.__pipFrames++; return raf(callback); };
  });
  await installStubs(page, { info: { kind: "firecracker", state: "ready" }, capture: "image", requests: [] });
  await page.goto(origin + "/");
  await page.locator("[data-computer-toggle]").first().click();
  const shell = page.locator(".desktop-floating-shell.is-small");
  await shell.waitFor();
  await page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
  await page.waitForTimeout(1500);
  assert.equal(await page.locator(".desktop-floating-shell.is-docked").count(), 0, "this viewport floats the window");

  const cdp = await context.newCDPSession(page);
  await cdp.send("Performance.enable");
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(metric => [metric.name, metric.value]));
  const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));

  // Commits: any DOM change under the shell other than the shell's own style/data-pip is React rendering (or a child changing).
  await page.evaluate(() => {
    const el = document.querySelector(".desktop-floating-shell");
    window.__mutations = 0;
    new MutationObserver(records => { for (const record of records) if (!(record.target === el && (record.attributeName === "style" || record.attributeName === "data-pip"))) window.__mutations++; }).observe(el, { subtree: true, childList: true, attributes: true, characterData: true });
    window.__settle = {};
    el.addEventListener("pointerup", () => { window.__settle.up = performance.now(); }, true);
    new MutationObserver(() => { if (!el.dataset.pip && window.__settle.up && !window.__settle.done) window.__settle.done = performance.now(); }).observe(el, { attributes: true, attributeFilter: ["data-pip"] });
  });

  const box = await shell.boundingBox();
  const start = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  await page.mouse.move(start.x - 12, start.y - 12, { steps: 3 }); // past the 6 px threshold: the drag has begun
  await frame();
  await frame();
  const before = await metrics();
  await page.evaluate(() => { window.__mutations = 0; });
  const MOVES = 60;
  for (let i = 1; i <= MOVES; i++) await page.mouse.move(start.x - 12 - (600 * i) / MOVES, start.y - 12 - (200 * i) / MOVES);
  await frame();
  const after = await metrics();
  const moved = await shell.boundingBox();
  assert.ok(Math.abs(moved.x - (box.x - 612)) < 60 || moved.x < box.x - 300, `the window followed the pointer (${box.x} -> ${moved.x})`);
  const layouts = after.LayoutCount - before.LayoutCount;
  const styles = after.RecalcStyleCount - before.RecalcStyleCount;
  const script = (after.ScriptDuration - before.ScriptDuration) * 1000 / MOVES;
  const commits = await page.evaluate(() => window.__mutations);
  console.log(`drag 600px at 4x: layouts ${layouts}, style recalcs ${styles}, script ${script.toFixed(2)} ms/frame, DOM mutations ${commits}`);
  if (reduced) {
    assert.equal(layouts, 0, "zero layouts during the drag");
    // The one inline transform write per frame is itself a style update of that single element (no layout, no paint of anything else).
    assert.ok(styles <= MOVES + 2, `${styles} style recalculations for ${MOVES} frames: more than one per frame`);
  }
  assert.equal(commits, 0, "React commits nothing while dragging");
  // The budget is 2 ms a frame of script; the CPU is slowed 4x here, so 8 ms measured is 2 ms of an unthrottled machine.
  if (reduced) assert.ok(script <= 8, `script per frame ${script.toFixed(2)} ms at 4x is over 8 ms`);

  await page.mouse.up();
  await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell")?.dataset.pip && window.__settle.done, null, { timeout: 2000 });
  const took = await page.evaluate(() => window.__settle.done - window.__settle.up);
  console.log(`release to rest: ${took.toFixed(0)} ms`);
  assert.ok(took <= 300, `settled in ${took.toFixed(0)} ms, over 300 ms`);
  const landed = await shell.boundingBox();
  assert.ok(Math.abs(landed.x - 20) <= 2 && Math.abs(landed.y - 64) <= 2, `snapped to the top-left corner (${landed.x}, ${landed.y})`);
  const frames = await page.evaluate(() => window.__pipFrames);
  await page.waitForTimeout(600);
  assert.equal(await page.evaluate(() => window.__pipFrames), frames, "no engine frame callback runs at rest");
  assert.deepEqual(errors, []);
  await context.close();
}
try {
  await run(true);
  await run(false);
  console.log("PASS pip drag");
} finally {
  await browser.close();
  await server.close();
}
