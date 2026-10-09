// Computer window: small floating screen -> big dialog, a stable 16:10 frame while loading, and its failure state.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:computer-window
// Synthetic only: a Vite dev server for the real App and Playwright stubs for every /api/* call.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const output = resolve(ui, "../artifacts/computer-window");
mkdirSync(output, { recursive: true });
const TARGET = 1.6;
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const info = (state, extra = {}) => ({ kind: "firecracker", state, ...extra });

async function open({ width = 1440, height = 900, theme = "light", reducedMotion = false, state }) {
  const context = await browser.newContext({ viewport: { width, height }, reducedMotion: reducedMotion ? "reduce" : "no-preference", colorScheme: theme });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await context.addInitScript(() => {
    // Records every Element.animate() on the computer screen: the FLIP is the only one the window makes.
    window.__flips = [];
    const animate = Element.prototype.animate;
    Element.prototype.animate = function (...args) {
      if (this.classList?.contains("computer-detail")) window.__flips.push(JSON.stringify(args[0]));
      return animate.apply(this, args);
    };
  });
  await installStubs(page, state);
  await page.goto(origin + "/");
  await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
  // On a phone the conversation list comes first.
  if (width < 700) await page.locator(".conversation-row").first().click();
  const button = page.locator(".computer-button").first();
  await button.waitFor();
  return { context, page, errors, button };
}

const ratio = async page => page.locator("[data-desktop-frame]").first().evaluate(element => { const box = element.getBoundingClientRect(); return box.width / box.height; });
const within = (value, label) => assert.ok(Math.abs(value / TARGET - 1) <= 0.02, `${label}: frame ratio ${value.toFixed(3)} is not within 2% of ${TARGET}`);
const shell = (page, kind) => page.locator(`.desktop-floating-shell${kind ? "." + kind : ""}`);
const flush = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
const shot = (page, name) => page.screenshot({ path: `${output}/${name}.png` });

// Samples the frame's ratio on every animation frame, so first paint and the loading period are both covered.
const record = page => page.evaluate(() => {
  window.__ratios = [];
  const sample = () => {
    const frame = document.querySelector("[data-desktop-frame]");
    if (frame) { const box = frame.getBoundingClientRect(); if (box.width) window.__ratios.push(box.width / box.height); }
    window.__sampler = requestAnimationFrame(sample);
  };
  sample();
});
const samples = async page => { const values = await page.evaluate(() => { cancelAnimationFrame(window.__sampler); return window.__ratios; }); assert.ok(values.length > 0, "no frame was ever painted"); return values; };

try {
  // 1. Loading: small window, frame is 16:10 from the first paint, placeholder says what is happening.
  {
    const state = { info: info("starting"), capture: "pending", requests: [] };
    const { context, page, errors, button } = await open({ state });
    await record(page);
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.getByText("Waking the computer").waitFor();
    await page.waitForTimeout(1600);
    for (const value of await samples(page)) within(value, "small loading");
    const box = await shell(page, "is-small").boundingBox();
    const frame = await page.locator("[data-desktop-frame]").boundingBox();
    assert.ok(frame.width >= 360 && frame.width <= 420, `small frame width ${frame.width}`);
    const composer = await page.locator(".composer").first().boundingBox();
    assert.ok(box.y + box.height <= composer.y + 1, `small window (${box.y + box.height}) must sit above the composer (${composer.y})`);
    assert.ok(box.x + box.width <= 1440 && box.x >= 0, "small window inside the viewport");
    assert.equal(await shell(page, "is-expanded").count(), 0, "the button opens the small window first");
    assert.match(await page.locator(".desktop-placeholder-line").innerText(), /\d+s$/, "the status line counts seconds");
    assert.equal(await page.locator(".desktop-placeholder-grid").count(), 1, "dot texture present");
    assert.ok(await page.getByRole("switch").isVisible(), "control switch stays reachable in the small window");
    await shot(page, "small-loading-light-1440");
    // Hibernation states are named, not shown as a generic boot.
    state.info = info("resuming");
    await page.getByText("Resuming from hibernation").waitFor({ timeout: 6000 });
    state.info = info("hibernated");
    await page.getByText(/Hibernated\./).waitFor({ timeout: 6000 });
    assert.deepEqual(errors, []);
    results.push("small loading");
    await context.close();
  }

  // 2. Ready: click -> big, Escape -> small, shrink/expand controls, close; last size is remembered for the page only.
  {
    const state = { info: info("ready"), capture: "image", requests: [] };
    const { context, page, errors, button } = await open({ state });
    await record(page);
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.locator(".computer-detail.desktop-live-screen, [data-desktop-frame].desktop-live-screen").first().waitFor({ timeout: 10000 }).catch(() => undefined);
    await page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
    await flush(page);
    within(await ratio(page), "small ready");
    await shot(page, "small-ready-light-1440");
    // Hover reveals the glass controls, 8px inside the top-right corner.
    await page.locator("[data-desktop-frame]").hover();
    const controls = await page.locator(".desktop-window-controls").boundingBox();
    const smallDetail = await page.locator(".computer-detail").boundingBox();
    assert.ok(Math.abs(smallDetail.x + smallDetail.width - (controls.x + controls.width) - 14) <= 3 && Math.abs(controls.y - smallDetail.y - 14) <= 3, "controls sit 8px inside the screen corner");
    await page.waitForTimeout(1100); // the CRT reveal of the first picture
    await page.evaluate(() => { window.__flips.length = 0; });
    await page.locator("[data-desktop-frame]").click({ position: { x: 40, y: 80 } });
    await shell(page, "is-expanded").waitFor();
    assert.equal(await shell(page, "is-expanded").getAttribute("role"), "dialog");
    assert.equal(await shell(page, "is-small").count(), 0);
    assert.equal((await page.evaluate(() => window.__flips)).length, 1, "small -> big expands from the small window's rect (FLIP)");
    assert.match((await page.evaluate(() => window.__flips))[0], /translate\(.*scale\(/);
    await page.waitForTimeout(400);
    within(await ratio(page), "big ready");
    for (const value of await samples(page)) within(value, "ready (small, expanding, big)");
    assert.ok(await page.getByRole("switch").isVisible(), "control switch in the big window");
    await shot(page, "big-ready-light-1440");
    await page.keyboard.press("Escape");
    await shell(page, "is-small").waitFor();
    assert.equal(await shell(page, "is-expanded").count(), 0, "Escape shrinks big to small");
    await page.locator("[data-desktop-frame]").hover();
    await page.getByRole("button", { name: "Enlarge shared computer" }).click();
    await shell(page, "is-expanded").waitFor();
    await page.getByRole("button", { name: "Shrink shared computer" }).click();
    await shell(page, "is-small").waitFor();
    // Close from big removes the window entirely; the header button always reopens small.
    await page.getByRole("button", { name: "Enlarge shared computer" }).click();
    await shell(page, "is-expanded").waitFor();
    await page.getByRole("button", { name: "Close shared computer" }).click();
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell"));
    await button.click();
    await shell(page, "is-small").waitFor();
    assert.equal(await shell(page, "is-expanded").count(), 0, "every open from the header button starts small");
    await page.locator("[data-desktop-frame]").hover();
    await page.getByRole("button", { name: "Close shared computer" }).click();
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell"));
    await page.reload();
    await page.locator(".computer-button").first().click();
    await shell(page, "is-small").waitFor();
    assert.deepEqual(errors, []);
    results.push("ready small/big/close/escape");
    await context.close();
  }

  // 3. Dark theme screenshots.
  {
    const state = { info: info("ready"), capture: "image", requests: [] };
    const { context, page } = await open({ theme: "dark", state });
    await page.locator(".computer-button").first().click();
    await shell(page, "is-small").waitFor();
    await page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
    await page.waitForTimeout(1100);
    await shot(page, "small-ready-dark-1440");
    await page.locator("[data-desktop-frame]").click({ position: { x: 40, y: 80 } });
    await shell(page, "is-expanded").waitFor();
    await page.waitForTimeout(400);
    await shot(page, "big-ready-dark-1440");
    await context.close();
    const loading = await open({ theme: "dark", state: { info: info("starting"), capture: "pending" } });
    await loading.page.locator(".computer-button").first().click();
    await shell(loading.page, "is-small").waitFor();
    await loading.page.waitForTimeout(1200);
    await shot(loading.page, "small-loading-dark-1440");
    await loading.context.close();
    const failed = await open({ theme: "dark", state: { info: info("ready"), capture: "fail" } });
    await failed.button.click();
    await failed.page.locator("[data-desktop-frame] [role=alert]").waitFor({ timeout: 15000 });
    await shot(failed.page, "small-failure-dark-1440");
    await failed.context.close();
    const sheet = await open({ width: 390, height: 844, state: { info: info("ready"), capture: "image" } });
    await sheet.button.click();
    await sheet.page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
    await sheet.page.waitForTimeout(1200);
    await shot(sheet.page, "sheet-ready-light-390");
    await sheet.context.close();
  }

  // 4. Reduced motion: nothing animates or transitions on the way up.
  {
    const state = { info: info("ready"), capture: "image", requests: [] };
    const { context, page, errors, button } = await open({ reducedMotion: true, state });
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
    await page.waitForTimeout(1100); // the CRT reveal of the first picture
    await page.waitForTimeout(300);
    await page.evaluate(() => { window.__flips.length = 0; });
    await page.locator("[data-desktop-frame]").click({ position: { x: 40, y: 80 } });
    await shell(page, "is-expanded").waitFor();
    const running = await page.evaluate(() => document.getAnimations().filter(animation => {
      const target = animation.effect?.target;
      return target?.closest?.(".desktop-floating-shell") && animation.playState === "running";
    }).map(animation => `${animation.constructor.name}:${animation.transitionProperty ?? animation.animationName ?? "waapi"}`));
    assert.deepEqual(running, [], "reduced motion leaves no running transition or animation on the window");
    assert.equal((await page.evaluate(() => window.__flips)).length, 0, "reduced motion skips the FLIP");
    await page.keyboard.press("Escape");
    await shell(page, "is-small").waitFor();
    assert.deepEqual(errors, []);
    results.push("reduced motion");
    await context.close();
  }

  // 5. Mobile: the button opens the big view directly, as a full-screen sheet.
  {
    const state = { info: info("starting"), capture: "pending", requests: [] };
    const { context, page, errors, button } = await open({ width: 390, height: 844, state });
    await record(page);
    await button.click();
    await shell(page, "is-expanded").waitFor();
    assert.equal(await shell(page, "is-small").count(), 0, "no small window on mobile");
    await shell(page, "is-sheet").waitFor();
    const sheet = await shell(page, "is-sheet").boundingBox();
    assert.deepEqual([sheet.x, sheet.y, sheet.width, sheet.height], [0, 0, 390, 844], "the sheet fills the screen");
    await page.waitForTimeout(800);
    for (const value of await samples(page)) within(value, "mobile loading");
    assert.equal(await page.getByRole("button", { name: /Enlarge|Shrink/ }).count(), 0, "the sheet has no small form to shrink to");
    assert.ok(await page.getByRole("switch").isVisible(), "control switch stays reachable on the sheet");
    assert.ok(await page.getByRole("button", { name: "Close shared computer" }).isVisible());
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "no horizontal overflow");
    await shot(page, "sheet-loading-light-390");
    await page.getByRole("button", { name: "Close shared computer" }).click();
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell"));
    assert.deepEqual(errors, []);
    results.push("mobile sheet");
    await context.close();
  }

  // 6. Failure: a one-line reason and Retry inside the frame, which keeps its shape; Retry recovers.
  {
    const state = { info: info("ready"), capture: "fail", requests: [] };
    const { context, page, errors, button } = await open({ state });
    await record(page);
    await button.click();
    await shell(page, "is-small").waitFor();
    const alert = page.locator("[data-desktop-frame] [role=alert]");
    await alert.waitFor({ timeout: 15000 });
    assert.match(await alert.innerText(), /Can't reach the desktop right now|Computer connection unavailable/);
    const retry = page.locator("[data-desktop-frame]").getByRole("button", { name: "Retry" });
    assert.ok(await retry.isVisible(), "Retry sits inside the frame");
    for (const value of await samples(page)) within(value, "failure");
    await shot(page, "small-failure-light-1440");
    await page.keyboard.press("Tab");
    state.capture = "image";
    await retry.click();
    await page.locator("[data-desktop-frame].desktop-live-screen").waitFor({ timeout: 15000 });
    assert.equal(await page.locator(".desktop-placeholder").count(), 0, "Retry brings the picture back");
    assert.deepEqual(errors, []);
    results.push("failure + retry");
    await context.close();
    const mobile = await open({ width: 390, height: 844, state: { info: info("ready"), capture: "fail" } });
    await mobile.button.click();
    await mobile.page.locator("[data-desktop-frame] [role=alert]").waitFor({ timeout: 15000 });
    await shot(mobile.page, "sheet-failure-light-390");
    await mobile.context.close();
  }
  console.log(`PASS computer window: ${results.join("; ")}`);
} finally {
  await browser.close();
  await server.close();
}
