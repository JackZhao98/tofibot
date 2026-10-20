// Boot screen: a frosted skeleton of the app behind one blur layer, the calico mark breathing, no spinner.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:preloader   (Chromium and WebKit)
// PRELOADER_SHOTS=<dir> also writes light/dark, desktop/phone screenshots.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = process.env.PRELOADER_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

const bots = [{ id: "bot_a", name: "Ledger Cat", instructions: "x", model: "m", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: "2026-10-13T10:00:00Z" }];
const convs = [{ id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: "2026-10-13T10:00:00Z", user_visible: true }];

/** Records, in page time, when each boot layer appears and goes away. */
const recorder = () => {
  const seen = {};
  const mark = (key, extra) => { seen[key] ??= []; seen[key].push(Math.round(performance.now())); void extra; };
  const state = { skeleton: false, dissolve: false };
  const scan = () => {
    const skeleton = !!document.querySelector(".boot:not(.boot-dissolve) .boot-skeleton");
    const dissolve = !!document.querySelector(".boot-dissolve");
    if (!state.boot && document.querySelector(".boot")) { state.boot = true; mark("bootOn"); }
    if (skeleton !== state.skeleton) { mark(skeleton ? "skeletonOn" : "skeletonOff"); state.skeleton = skeleton; }
    if (dissolve !== state.dissolve) { mark(dissolve ? "dissolveOn" : "dissolveOff"); state.dissolve = dissolve; }
    if (document.querySelector(".spinner")) mark("spinner");
  };
  window.__boot = seen;
  new MutationObserver(scan).observe(document, { childList: true, subtree: true });
  scan();
};

async function open(browser, { delay = 0, fail = false, reduced = false, viewport = { width: 1180, height: 820 }, scheme = "light" }) {
  const context = await browser.newContext({ viewport, locale: "en-US", colorScheme: scheme, reducedMotion: reduced ? "reduce" : "no-preference" });
  await context.addInitScript(recorder);
  await context.addInitScript(() => {
    window.EventSource = class { constructor() { this.readyState = 0; } addEventListener() {} removeEventListener() {} close() {} };
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const control = { fail, delay };
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const reply = body => route.fulfill({ status: 200, json: body });
    if (path === "/api/server-info") return reply({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "none" } });
    if (path === "/api/preferences") return reply({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return reply({ model_configured: true, default_model: "m", provider: "synthetic" });
    if (path === "/api/bots" || path === "/api/conversations") {
      await sleep(control.delay);
      if (control.fail) return route.fulfill({ status: 503, json: { error: "unavailable" } });
      return reply(path === "/api/bots" ? { bots } : { conversations: convs });
    }
    if (path.endsWith("/messages")) return reply({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return reply({ runs: [] });
    if (path.endsWith("/memories")) return reply({ memories: [] });
    if (path.endsWith("/tools")) return reply(url.searchParams.has("run_ids") ? { summaries: [] } : { activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.endsWith("/schedule-occurrences")) return reply({ occurrences: [] });
    if (path.endsWith("/schedules")) return reply({ schedules: [] });
    if (path.startsWith("/api/computer")) return reply({ owner: null, waiting: [] });
    if (path === "/api/questions") return reply({ questions: [] });
    if (path === "/api/mail-drafts") return reply({ drafts: [] });
    return reply({});
  });
  return { page, context, errors, control };
}
const seen = page => page.evaluate(() => window.__boot);

for (const [name, launcher] of [["chromium", pw.chromium], ["webkit", pw.webkit]]) {
  const browser = await launcher.launch({ headless: true, ...(name === "chromium" && process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
  const timings = {};

  // 1. Slow lists: nothing for the first 300 ms, then skeleton + frost + logo, no spinner, dissolve on arrival.
  {
    const { page, context, errors } = await open(browser, { delay: 1800 });
        await page.goto(origin + "/", { waitUntil: "commit" });
    await page.locator(".boot .boot-skeleton").waitFor({ timeout: 5000 });
    await sleep(300);
    const probe = await page.evaluate(() => {
      const frost = document.querySelector(".boot-frost");
      const style = getComputedStyle(frost);
      const logo = document.querySelector(".boot-logo");
      const logoStyle = getComputedStyle(logo);
      const status = document.querySelector('.boot [role="status"]');
      return {
        blur: style.backdropFilter || style.webkitBackdropFilter,
        skeleton: !!document.querySelector(".boot-skeleton .boot-side .boot-row") && !!document.querySelector(".boot-skeleton .boot-head") && document.querySelectorAll(".boot-bubble").length >= 4 && !!document.querySelector(".boot-composer"),
        logoSrc: logo.getAttribute("src"), logoFilter: logoStyle.filter, breathe: logoStyle.animationName, duration: logoStyle.animationDuration,
        busy: document.querySelector(".boot").getAttribute("aria-busy"), live: status.getAttribute("aria-live"), label: status.textContent,
        spinners: document.querySelectorAll(".spinner").length, fixed: getComputedStyle(document.querySelector(".boot")).position,
      };
    });
    assert.match(probe.blur, /blur\(/, `${name}: frost layer must blur`);
    assert.ok(probe.skeleton, `${name}: skeleton must hold sidebar rows, header, bubbles and composer`);
    assert.match(probe.logoSrc, /tofi-logo-calico\.svg/);
    assert.match(probe.logoFilter, /grayscale/);
    assert.equal(probe.breathe, "boot-breathe");
    assert.equal(probe.duration, "2.4s");
    assert.equal(probe.busy, "true");
    assert.equal(probe.live, "polite");
    assert.equal(probe.label, "Loading workspace…");
    assert.equal(probe.spinners, 0, `${name}: no spinner element`);
    await page.locator(".conversation-row").first().waitFor({ timeout: 15_000 });
    await page.locator(".boot").first().waitFor({ state: "detached", timeout: 3000 });
    const log = await seen(page);
    assert.ok(log.dissolveOn?.length, `${name}: the loader must dissolve over the real UI`);
    const exit = log.dissolveOff[0] - log.dissolveOn[0];
    assert.ok(exit >= 250 && exit <= 450, `${name}: exit took ${exit} ms`);
    timings.delay = log.skeletonOn[0] - log.bootOn[0]; timings.exit = exit;
    assert.ok(timings.delay >= 280 && timings.delay <= 700, `${name}: loader appeared after ${timings.delay} ms`);
    assert.equal(log.spinner, undefined, `${name}: a spinner appeared`);
    assert.deepEqual(errors, []);
    await context.close();
  }

  // 2. Reduced motion: no transform animation, plain crossfade.
  {
    const { page, context } = await open(browser, { delay: 1500, reduced: true });
    await page.goto(origin + "/", { waitUntil: "commit" });
    await page.locator(".boot .boot-skeleton").waitFor({ timeout: 5000 });
    await sleep(400);
    const motion = await page.evaluate(() => ({
      name: getComputedStyle(document.querySelector(".boot-logo")).animationName,
      transforms: document.getAnimations().filter(a => a.effect?.getKeyframes().some(k => "transform" in k || "scale" in k)).length,
      running: document.getAnimations().filter(a => a.playState === "running" && a.effect?.getComputedTiming().iterations === Infinity).length,
    }));
    assert.equal(motion.name, "none", `${name}: logo must not breathe under reduced motion`);
    assert.equal(motion.transforms, 0, `${name}: no transform animation under reduced motion`);
    assert.equal(motion.running, 0);
    await page.locator(".conversation-row").first().waitFor({ timeout: 15_000 });
    await context.close();
  }

  // 3. Fast load: no loader at all.
  {
    const { page, context } = await open(browser, { delay: 40 });
    await page.goto(origin + "/");
    await page.locator(".conversation-row").first().waitFor({ timeout: 15_000 });
    await sleep(500);
    const log = await seen(page);
    assert.equal(log.skeletonOn, undefined, `${name}: a fast load must not show the loader`);
    assert.equal(log.dissolveOn, undefined, `${name}: a fast load must not dissolve anything`);
    assert.equal(await page.locator(".boot").count(), 0);
    await context.close();
  }

  // 4. Failure: same backdrop, message and Retry; Retry recovers.
  {
    const { page, context, control } = await open(browser, { delay: 0, fail: true });
    await page.goto(origin + "/");
    const retry = page.getByRole("button", { name: "Retry" });
    await retry.waitFor({ timeout: 15_000 });
    const failed = await page.evaluate(() => ({
      backdrop: !!document.querySelector(".boot.is-failed .boot-skeleton") && /blur\(/.test(getComputedStyle(document.querySelector(".boot-frost")).backdropFilter || getComputedStyle(document.querySelector(".boot-frost")).webkitBackdropFilter || ""),
      heading: document.querySelector(".boot-card h2")?.textContent, spinners: document.querySelectorAll(".spinner").length,
    }));
    assert.ok(failed.backdrop, `${name}: the error state must sit on the blurred backdrop`);
    assert.ok(failed.heading);
    assert.equal(failed.spinners, 0);
    if (shots && name === "chromium") await page.screenshot({ path: `${shots}/error-light-desktop.png` });
    control.fail = false;
    await retry.click();
    await page.locator(".conversation-row").first().waitFor({ timeout: 15_000 });
    await context.close();
  }

  // 5. Screenshots.
  if (shots) for (const scheme of ["light", "dark"]) for (const [label, viewport] of [["desktop", { width: 1180, height: 820 }], ["phone", { width: 390, height: 844 }]]) {
    const { page, context } = await open(browser, { delay: 2500, scheme, viewport });
    await page.goto(origin + "/", { waitUntil: "commit" });
    await page.locator(".boot .boot-skeleton").waitFor({ timeout: 5000 });
    await sleep(500);
    await page.screenshot({ path: `${shots}/loading-${scheme}-${label}-${name}.png` });
    await page.locator(".conversation-row").first().waitFor({ timeout: 15_000 }).catch(() => {});
    await context.close();
    if (scheme === "light" && label === "desktop") {
      const mid = await open(browser, { delay: 2000, scheme, viewport });
      await mid.page.goto(origin + "/", { waitUntil: "commit" });
      await mid.page.locator(".boot .boot-skeleton").waitFor({ timeout: 5000 });
      await mid.page.locator(".boot-dissolve").waitFor({ timeout: 8000 });
      await sleep(90);
      await mid.page.screenshot({ path: `${shots}/dissolve-mid-${name}.png` });
      await mid.context.close();
    }
  }
  console.log(`ok - ${name}: loader after ${timings.delay} ms, exit ${timings.exit} ms; no spinner; reduced motion has no transform; fast load shows nothing; error state keeps the backdrop`);
  await browser.close();
}
await server.close();
