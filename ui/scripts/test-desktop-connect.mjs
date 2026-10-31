// Desktop viewer connect: opening the panel must reach a frame (or a clear, named state), never a false
// "unreachable". Synthetic only: Vite dev server for the real App, Playwright stubs for every /api/* call.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:desktop-connect
import assert from "node:assert/strict";
import { installStubs, launch, screenImage, startUi } from "./computer-window-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
const info = (state, extra = {}) => ({ kind: "firecracker", state, ...extra });
const UNREACHABLE = "Can't reach the desktop right now. Try again later.";
const NOT_RUNNING_502 = { error: { code: "computer_action_failed", message: "computer control returned 500 Internal Server Error: {\"ok\":false,\"error\":\"desktop is not running; call desktop.start first\"}" } };
const BOT_OWNER = { owner: { bot_id: "bot-synthetic", run_id: "run-1", kind: "bot", conversation_id: "conv-synthetic" }, waiting: [] };

// `capture(n)` answers the nth desktop.capture; `started` flips on desktop.start.
async function open({ state, capture, start }) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await installStubs(page, state);
  const log = { captures: 0, starts: 0 };
  await page.route("**/api/computers/firecracker/actions", async route => {
    const body = route.request().postDataJSON();
    const json = (payload, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });
    if (body.action === "desktop.start") { log.starts++; return start ? start(log, json) : (log.running = true, json({ ok: true, result: { started: true } })); }
    if (body.action === "desktop.capture") { log.captures++; return capture(log, json); }
    return json({ ok: true, result: {} });
  });
  await page.goto(origin + "/");
  await page.locator(".computer-button").first().click();
  await page.locator(".desktop-floating-shell").first().waitFor();
  return { context, page, errors, log };
}
const frame = page => page.locator("[data-desktop-frame].desktop-live-screen").first();
const image = (log, json) => json({ ok: true, result: { image_url: screenImage() } });
const results = [];

try {
  // 1. Ready computer, desktop stopped, old-style wrapped 502: owner viewer starts the desktop.
  {
    const { context, page, errors, log } = await open({ state: { info: info("ready"), requests: [] }, capture: (l, json) => l.running ? image(l, json) : json(NOT_RUNNING_502, 502) });
    await frame(page).waitFor({ state: "attached", timeout: 10000 });
    assert.equal(log.starts, 1, "desktop.start is requested once");
    assert.equal(await page.getByText(UNREACHABLE).count(), 0);
    assert.deepEqual(errors, []);
    results.push("wrapped not-running starts desktop");
    await context.close();
  }
  // 2. Typed code (no message substring): the UI decides by code.
  {
    const { context, page, log } = await open({ state: { info: info("ready"), requests: [] }, capture: (l, json) => l.running ? image(l, json) : json({ error: { code: "desktop_not_running", message: "Desktop is off" } }, 422) });
    await frame(page).waitFor({ state: "attached", timeout: 10000 });
    assert.equal(log.starts, 1);
    assert.equal(await page.getByText(UNREACHABLE).count(), 0);
    results.push("typed desktop_not_running starts desktop");
    await context.close();
  }
  // 3. Guest still restoring (info says ready, control plane answers 503 computer_restarting for ~5s).
  {
    const t0 = Date.now();
    const { context, page } = await open({ state: { info: info("ready"), requests: [] }, capture: (l, json) => Date.now() - t0 < 5000 ? json({ error: { code: "computer_restarting", message: "The Bot computer is restarting" } }, 503) : image(l, json) });
    await page.waitForTimeout(3500);
    assert.equal(await page.getByText(UNREACHABLE).count(), 0, "a restoring guest is waiting, not unreachable");
    await frame(page).waitFor({ state: "attached", timeout: 15000 });
    results.push("restoring guest waits");
    await context.close();
  }
  // 4. Real failure still ends in unreachable.
  {
    const { context, page } = await open({ state: { info: info("ready"), requests: [] }, capture: (l, json) => json({ error: { code: "computer_action_failed", message: "screenshot tool crashed" } }, 502) });
    await page.getByText(UNREACHABLE).waitFor({ timeout: 15000 });
    results.push("real failure is unreachable");
    await context.close();
  }
  // 5. Info flaps ready -> resuming -> ready while the first capture is in flight: it reconnects by itself.
  {
    const state = { info: info("ready"), requests: [] };
    let gate;
    const { context, page } = await open({ state, capture: async (l, json) => { if (l.captures === 1) { state.info = info("resuming"); await new Promise(resolve => setTimeout(resolve, 2500)); state.info = info("ready"); return json({ error: { code: "computer_restarting", message: "restarting" } }, 503); } return image(l, json); } });
    await frame(page).waitFor({ state: "attached", timeout: 15000 });
    assert.equal(await page.getByText(UNREACHABLE).count(), 0);
    results.push("reconnects after the Bot computer flaps");
    await context.close();
  }
  // 6. Passive viewer (a Bot run holds the computer, no desktop): a named state, not an endless "connecting".
  {
    const state = { info: info("ready"), ownership: BOT_OWNER, requests: [] };
    const { context, page, log } = await open({ state, capture: (l, json) => json(NOT_RUNNING_502, 502) });
    await page.getByText("The Bot is working in the browser without a desktop").waitFor({ timeout: 15000 });
    assert.equal(log.starts, 0, "a passive viewer never starts the desktop");
    assert.equal(await page.getByText(UNREACHABLE).count(), 0);
    results.push("passive viewer names the no-desktop state");
    await context.close();
  }
  console.log(`desktop connect: ${results.length} scenarios ok\n- ${results.join("\n- ")}`);
} finally {
  await browser.close();
  await server.close();
}
