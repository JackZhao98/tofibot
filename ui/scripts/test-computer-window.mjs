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
    // Wide browser windows dock the small screen in the outside panel; narrower ones float it over the chat.
    if (await shell(page, "is-docked").count()) {
      const card = await page.locator(".detail-pane.outside-panel").boundingBox();
      assert.ok(frame.width >= 300 && frame.width <= 480 && box.x >= card.x - 1 && box.x + box.width <= card.x + card.width + 1, `docked frame ${frame.width} sits inside the outside panel`);
    } else {
      assert.ok(frame.width >= 360 && frame.width <= 420, `small frame width ${frame.width}`);
      const composer = await page.locator(".composer").first().boundingBox();
      assert.ok(box.y + box.height <= composer.y + 1, `small window (${box.y + box.height}) must sit above the composer (${composer.y})`);
      assert.ok(box.x + box.width <= 1440 && box.x >= 0, "small window inside the viewport");
    }
    assert.equal(await shell(page, "is-expanded").count(), 0, "the button opens the small window first");
    assert.match(await page.locator(".desktop-placeholder-line").innerText(), /\d+s$/, "the status line counts seconds");
    assert.equal(await page.locator(".desktop-placeholder-grid").count(), 1, "dot texture present");
    assert.ok(await page.getByRole("switch").isVisible(), "control switch stays reachable in the small window");
    await shot(page, "small-loading-light-1440");
    // Hibernation states are named, not shown as a generic boot.
    state.info = info("resuming");
    await page.getByText("Resuming from hibernation").waitFor({ timeout: 6000 });
    state.info = info("hibernated");
    await page.getByText(/Asleep/).waitFor({ timeout: 6000 });
    assert.deepEqual(errors, []);
    results.push("small loading");
    await context.close();
  }

  // 1b. Opening the window wakes a hibernated computer; asleep is normal, so no banner across the top.
  {
    const state = { info: info("hibernated"), capture: "image", requests: [], wakes: 0 };
    state.onWake = () => {
      state.info = info("resuming", { phase: "restoring" });
      setTimeout(() => { state.info = info("ready"); }, 2500);
    };
    const { context, page, errors, button } = await open({ state });
    await page.waitForResponse(response => response.url().endsWith("/api/computers/firecracker/info"));
    await page.waitForTimeout(2200); // two more polls
    assert.equal(await page.locator(".computer-preparing").count(), 0, "no banner for a hibernated computer");
    assert.equal(state.wakes, 0, "nothing wakes it before the window opens");
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.getByText("Waking · Restoring session").waitFor({ timeout: 6000 });
    assert.equal(state.wakes, 1, "opening the small window wakes the computer once");
    assert.equal(await page.locator(".computer-preparing").count(), 0, "no banner while the window shows the waking state");
    await page.locator("[data-desktop-frame].desktop-live-screen").waitFor({ timeout: 15000 });
    assert.equal(state.wakes, 1, "a single wake request in total");
    assert.equal(await page.locator(".desktop-placeholder").count(), 0, "the viewer connected by itself once ready");
    assert.deepEqual(errors, []);
    results.push("wake on open");
    await context.close();
    // Real problems still get the banner.
    const stopped = await open({ state: { info: info("stopped"), capture: "image", requests: [] } });
    await stopped.page.locator(".computer-preparing").waitFor({ timeout: 6000 });
    await stopped.context.close();
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
  // 7. Quiet by default: a Bot using the computer never opens a window or shows a pill. The header button reports the
  //    power state (green on / neutral off, with aria-label + title, not colour alone) and a static dot while a Bot drives it.
  {
    const owner = { owner: { bot_id: "bot-synthetic", run_id: "run-1", kind: "bot", conversation_id: "conv-synthetic" }, waiting: [] };
    const state = { info: info("hibernated"), capture: "image", requests: [], ownership: { owner: null, waiting: [] } };
    const { context, page, errors, button } = await open({ state });
    const bg = () => button.evaluate(element => getComputedStyle(element).backgroundColor);
    await page.waitForResponse(response => response.url().endsWith("/api/computers/firecracker/info"));
    await page.waitForTimeout(1500);
    assert.equal(await button.getAttribute("data-computer-state"), "off");
    assert.equal(await button.getAttribute("aria-label"), "Shared computer, off");
    assert.equal(await button.getAttribute("title"), "Shared computer, off");
    assert.equal(await button.evaluate(element => element.classList.contains("is-on")), false, "neutral when asleep");
    const neutral = await bg();
    // The computer comes up, then a Bot starts driving it: nothing opens on its own.
    state.info = info("ready");
    await page.waitForFunction(() => document.querySelector("[data-computer-toggle]")?.getAttribute("data-computer-state") === "on", null, { timeout: 8000 });
    assert.equal(await button.getAttribute("aria-label"), "Shared computer, on");
    await page.waitForTimeout(400); // past the colour transition
    const green = await bg();
    assert.notEqual(green, neutral, "the button changes colour when the computer is on");
    const [r, g, b, a] = await page.evaluate(css => { const c = document.createElement("canvas").getContext("2d", { willReadFrequently: true }); c.clearRect(0, 0, 1, 1); c.fillStyle = css; c.fillRect(0, 0, 1, 1); return Array.from(c.getImageData(0, 0, 1, 1).data); }, green);
    assert.ok(a > 0 && g > r && g > b, `powered-on background ${green} (rgba ${r},${g},${b},${a}) is green`);
    state.ownership = owner;
    await page.waitForFunction(() => document.querySelector("[data-computer-toggle]")?.getAttribute("data-computer-state") === "driven", null, { timeout: 8000 });
    assert.match(await button.getAttribute("aria-label"), /Mochi is working in it/);
    assert.equal(await page.locator(".computer-driven-dot").count(), 1, "a small dot marks a Bot driving the computer");
    assert.equal(await page.locator(".computer-driven-dot").evaluate(element => getComputedStyle(element).animationName), "none", "the dot does not animate");
    await page.waitForTimeout(2500);
    assert.equal(await page.locator(".desktop-floating-shell").count(), 0, "a Bot using the computer never opens a window");
    assert.equal(await page.getByText(/is using the computer/).count(), 0, "no 'using the computer' pill");
    assert.equal(await page.locator(".desktop-mini-status, .desktop-presence-launcher").count(), 0);
    assert.ok(!state.requests.includes("/api/computers/firecracker/actions"), "no capture is requested while the window is closed");
    // Opening is the user's call, and frames arrive while the Bot holds the computer (the server serves view captures beside its lease).
    const clicked = Date.now();
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.locator("[data-desktop-frame].desktop-live-screen").waitFor({ timeout: 4000 });
    assert.ok(Date.now() - clicked < 2500, `first frame ${Date.now() - clicked}ms after opening`);
    assert.equal(await page.getByText(/is using the computer/).count(), 0);
    await shot(page, "quiet-open-driven-light-1440");
    // Back to neutral when the computer sleeps.
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Close shared computer" }).click().catch(() => undefined);
    state.ownership = { owner: null, waiting: [] };
    state.info = info("hibernated");
    await page.waitForFunction(() => document.querySelector("[data-computer-toggle]")?.getAttribute("data-computer-state") === "off", null, { timeout: 8000 });
    assert.deepEqual(errors, []);
    results.push("quiet button + no auto-open");
    await context.close();
  }

  // 8. Placement: while the small window is open the messages reflow beside it; nothing is covered, the composer stays usable.
  for (const [width, height] of [[1440, 900], [1180, 820], [1024, 768]]) {
    const messages = Array.from({ length: 14 }, (_, index) => ({ id: `m${index}`, seq: index + 1, conversation_id: "conv-synthetic", role: index % 2 ? "assistant" : "user", content: "Synthetic message ".repeat(14), created_at: "2026-10-09T12:00:00Z" }));
    const state = { info: info("ready"), capture: "image", requests: [], messages };
    const { context, page, errors, button } = await open({ width, height, state });
    const textRects = () => page.evaluate(() => {
      const out = [];
      const walker = document.createTreeWalker(document.querySelector(".message-scroll"), NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (!node.textContent.trim()) continue;
        const range = document.createRange(); range.selectNodeContents(node);
        for (const r of range.getClientRects()) if (r.width > 0) out.push({ x: r.x, y: r.y, w: r.width, h: r.height });
        const bubble = node.parentElement.closest(".bubble, .message-card, [class*=bubble]");
        if (bubble) { const r = bubble.getBoundingClientRect(); out.push({ x: r.x, y: r.y, w: r.width, h: r.height }); }
      }
      return out;
    });
    await page.getByText("Synthetic message").first().waitFor();
    const before = await textRects();
    assert.ok(before.length > 5, `${width}: message text found (${before.length})`);
    await button.click();
    await shell(page, "is-small").waitFor();
    await page.locator("img.computer-screen, video").first().waitFor({ state: "attached", timeout: 10000 });
    await page.waitForTimeout(500);
    const box = await shell(page, "is-small").boundingBox();
    const text = await textRects();
    const overlaps = text.filter(r => r.x < box.x + box.width && r.x + r.w > box.x && r.y < box.y + box.height && r.y + r.h > box.y);
    assert.deepEqual(overlaps, [], `${width}x${height}: the small window covers ${overlaps.length} message text/bubble rects`);
    const composer = await page.locator(".composer .composer-row").first().boundingBox();
    assert.ok(box.y + box.height <= composer.y, "window ends above the composer");
    assert.equal(await page.locator(".composer textarea, .composer [contenteditable]").first().isVisible(), true, "composer input stays visible");
    await shot(page, `quiet-placement-${width}`);
    // Closing the window gives the width back.
    await page.getByRole("button", { name: "Close shared computer" }).click({ trial: false }).catch(async () => { await page.locator("[data-desktop-frame]").hover(); await page.getByRole("button", { name: "Close shared computer" }).click(); });
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell"));
    assert.equal(await page.evaluate(() => document.documentElement.hasAttribute("data-desktop-reserve")), false, "padding removed after close");
    assert.deepEqual(errors, []);
    console.log(`placement ${width}x${height}: window x=${box.x}..${box.x + box.width}, ${text.length} text rects, 0 overlap`);
    await context.close();
  }
  results.push("placement");
  console.log(`PASS computer window: ${results.join("; ")}`);
} finally {
  await browser.close();
  await server.close();
}
