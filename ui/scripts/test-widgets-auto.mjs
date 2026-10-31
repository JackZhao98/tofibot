// The widgets' automatic life (docs/design/widgets-and-sidebar.md sections 2.1, 3, 6), in a real browser against synthetic data:
//  - Artifacts opens when the first file a Bot made arrives live in the conversation (not for history, not for a person's upload), takes no
//    focus, the next file refreshes the list and flags its row for a moment; the Automatic behavior switch turns it off.
//  - Computer opens when a Bot takes the desktop in THIS conversation (not another one), does not close itself, folds into its 56px strip
//    30 s after the Bot lets go (video stays mounted), unfolds from the strip, stays closed for a run the person closed it in, and is
//    off with its switch.
//  - Space: three widgets on a window shorter than 760 px fold Artifacts, then Members; one the person unfolded stays unfolded.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:widgets-auto   (WIDGETS_AUTO_SHOTS=<dir> also saves screenshots)
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, DM2, GROUP, botOwner, newState, openWorkspace, talk } from "./widgets-fixture.mjs";

const shots = process.env.WIDGETS_AUTO_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const cards = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => node.dataset.card));
const settled = page => page.waitForTimeout(450);
const poll = (page, fn, arg, timeout = 8000) => page.waitForFunction(fn, arg, { timeout });
const live = page => poll(page, () => document.querySelector(".desktop-live-screen"), null, 20000);

try {
  // 1. Artifacts.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    assert.deepEqual(await cards(page), [], "nothing is open for a conversation without files");
    await page.locator(".composer textarea").focus().catch(() => undefined);
    state.addFile(1);
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"] .artifact-row'));
    assert.deepEqual(await cards(page), ["artifacts"], "the first file opens the Artifacts widget");
    assert.equal(await page.evaluate(() => document.activeElement?.closest("[data-card]") === null), true, "opening by itself takes no focus");
    assert.equal(await page.locator('[data-card="artifacts"] li[data-fresh]').count(), 0, "the first file is not flagged: the whole widget is new");
    await page.getByRole("button", { name: /^Close Artifacts/ }).click();
    await settled(page);
    state.addFile(2);
    await page.waitForTimeout(1200);
    assert.deepEqual(await cards(page), [], "a widget the person closed does not come back for the next file");
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await poll(page, () => document.querySelector('[data-card="artifacts"] .artifact-row'));
    state.addFile(3);
    await poll(page, () => document.querySelectorAll('[data-card="artifacts"] .artifact-row').length === 3);
    assert.equal(await page.locator('[data-card="artifacts"] li[data-fresh]').count(), 1, "a later file refreshes the list and flags the new row");
    assert.match(await page.locator('[data-card="artifacts"] [role=status]').textContent(), /New file: /);
    if (shots) await page.screenshot({ path: `${shots}/artifacts-auto-1440.png` });
    await poll(page, () => !document.querySelector('[data-card="artifacts"] li[data-fresh]'), null, 4000);
    assert.deepEqual(errors, []);
    results.push("artifacts: first file opens it once, no focus, later files flag the new row");
    await context.close();
  }
  {
    // History is not an arrival, and a person's upload is not a Bot's file.
    const state = newState();
    state.addFile(1);
    const { context, page } = await openWorkspace(browser, origin, state);
    await page.waitForTimeout(800);
    assert.deepEqual(await cards(page), [], "files that were already in the conversation do not open the widget");
    state.messages[DM.id].push({ id: "u-file", conversation_id: DM.id, seq: 150, role: "user", content: "here", created_at: "2026-10-26T12:30:00Z", attachments: [{ id: "up1", conversation_id: DM.id, message_id: "u-file", name: "mine.txt", mime: "text/plain", size: 10, created_at: "2026-10-26T12:30:00Z" }] });
    state.push(DM.id, "message", state.messages[DM.id].at(-1));
    await page.waitForTimeout(1200);
    assert.deepEqual(await cards(page), [], "a person's own upload does not open it");
    state.addFile(51);
    await poll(page, () => document.querySelector('[data-card="artifacts"]'));
    results.push("artifacts: history and uploads do not open it");
    await context.close();
  }
  {
    const state = newState();
    state.settings.widget_artifacts_auto = false;
    const { context, page } = await openWorkspace(browser, origin, state);
    state.addFile(1);
    await page.waitForTimeout(1500);
    assert.deepEqual(await cards(page), [], "Automatic behavior off: the widget stays closed");
    await context.close();
    results.push("artifacts: the setting turns it off");
  }

  // 2. Computer.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    state.ownership = botOwner("run-other", DM2);
    await page.waitForTimeout(2200);
    assert.deepEqual(await cards(page), [], "a Bot using the Bot computer for another conversation does not open it here");
    state.ownership = botOwner("run-1");
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await live(page);
    assert.equal(await page.locator('[data-card="computer"] .context-card-header').count(), 0, "the Bot computer is the bare monitor: no header row");
    assert.equal(await page.locator('[data-card="computer"] .desktop-chin').count(), 1);
    assert.equal(await page.evaluate(() => document.activeElement?.closest("[data-card]") === null), true, "no focus taken");
    if (shots) await page.screenshot({ path: `${shots}/computer-auto-bot-1440.png` });
    // The Bot lets go: it does not close itself, and folds only after 30 s.
    state.ownership = { owner: null, waiting: [] };
    await page.waitForTimeout(5000);
    assert.equal(await page.locator(".computer-detail.is-folded").count(), 0, "not folded 5 s after the Bot let go");
    assert.deepEqual(await cards(page), ["computer"]);
    // The small screen has no fold button, no capsule, no chin controls and no shadow; the fold is the 30 s one (the video element is the same node throughout).
    for (const gone of [".desktop-control-fold", ".desktop-frame-status", ".desktop-power", ".desktop-driver-switch", ".desktop-drop"]) assert.equal(await page.locator(`[data-card="computer"] ${gone}`).count(), 0, `the small computer has no ${gone}`);
    assert.equal(await page.locator(".computer-detail").evaluate(node => getComputedStyle(node).boxShadow.replace(/rgba?\([^)]*\) 0px 0px 0px 1px/, "none")), "none", "no shadow under the small computer");
    await page.evaluate(() => { window.__screen = document.querySelector(".computer-screen-wrap"); });
    await poll(page, () => document.querySelector(".computer-detail.is-folded"), null, 40000);
    await page.waitForTimeout(500);
    assert.equal(await page.locator(".computer-detail.is-folded").count(), 1);
    const strip = await page.locator(".computer-detail").boundingBox();
    assert.ok(Math.abs(strip.height - 56) <= 1, `the strip is 56px (${strip.height})`);
    assert.equal(await page.evaluate(() => window.__screen === document.querySelector(".computer-screen-wrap") && document.contains(window.__screen)), true, "the screen stays mounted while folded");
    const thumb = await page.locator(".computer-screen-wrap").boundingBox();
    assert.ok(thumb.width <= 80 && thumb.height <= 44 && thumb.x < strip.x + strip.width / 3, "a small live thumbnail sits on the left");
    assert.ok(await page.locator(".desktop-chin-text").isVisible(), "the status text is visible in the strip");
    assert.equal(await page.locator(".desktop-driver-switch").count(), 0, "the Bot / You switch lives in the big window only");
    if (shots) await page.screenshot({ path: `${shots}/computer-folded-1440.png` });
    await page.locator(".desktop-strip-open").click();
    await page.waitForTimeout(500);
    assert.equal(await page.locator(".computer-detail.is-folded").count(), 0, "a click on the strip brings the monitor back");
    // 30 s after the Bot released it (the earlier wait counts): folded by itself.
    state.ownership = botOwner("run-2");
    await page.waitForTimeout(1500);
    state.ownership = { owner: null, waiting: [] };
    await poll(page, () => document.querySelector(".computer-detail.is-folded"), null, 40000);
    assert.deepEqual(await cards(page), ["computer"], "folded, not closed");
    // A new run unfolds it again.
    state.ownership = botOwner("run-3");
    await poll(page, () => !document.querySelector(".computer-detail.is-folded"), null, 6000);
    // Closing in a run suppresses the same run, not the next.
    await page.locator(".computer-detail").hover();
    await page.locator(".computer-hide-button").click();
    await poll(page, () => !document.querySelector('[data-card="computer"]'));
    await page.waitForTimeout(2500);
    assert.deepEqual(await cards(page), [], "closed by hand, it does not reopen for the same run");
    state.ownership = botOwner("run-4");
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    assert.deepEqual(errors, []);
    results.push("computer: opens for this conversation's Bot, folds after 30 s, strip click, close is per run");
    await context.close();
  }
  {
    const state = newState();
    state.settings.widget_computer_auto = false;
    const { context, page } = await openWorkspace(browser, origin, state);
    state.ownership = botOwner();
    await page.waitForTimeout(3000);
    assert.deepEqual(await cards(page), [], "Automatic behavior off: the Bot computer stays closed");
    await context.close();
    results.push("computer: the setting turns it off");
  }

  // 3. Space rule: a short window with all three widgets.
  {
    const state = newState();
    state.addFile(1, GROUP);
    const { context, page } = await openWorkspace(browser, origin, state, { height: 700, group: true });
    await page.locator(".chat-identity").click();
    await page.getByRole("button", { name: "Open artifacts" }).click();
    state.ownership = botOwner("run-g", GROUP);
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await poll(page, () => document.querySelector('[data-card="artifacts"].is-collapsed'), null, 6000);
    assert.deepEqual(await cards(page), ["computer", "members", "artifacts"]);
    assert.equal(await page.locator('[data-card="computer"]').evaluate(node => node.classList.contains("is-collapsed")), false, "the Bot computer never folds for space");
    if (shots) await page.screenshot({ path: `${shots}/space-rule-700.png` });
    // The person unfolds Artifacts: it is not folded again in this conversation.
    await page.getByRole("button", { name: /^Expand Artifacts/ }).click();
    await page.waitForTimeout(1200);
    assert.equal(await page.locator('[data-card="artifacts"]').evaluate(node => node.classList.contains("is-collapsed")), false, "a widget the person unfolded stays unfolded");
    results.push("space: Artifacts folds first on a short window; a manual unfold is kept");
    await context.close();
  }
  {
    const state = newState();
    const { context, page } = await openWorkspace(browser, origin, state, { height: 900, group: true });
    await page.locator(".chat-identity").click();
    await page.getByRole("button", { name: "Open artifacts" }).click();
    state.ownership = botOwner("run-g", GROUP);
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await page.waitForTimeout(1500);
    assert.equal(await page.locator(".widget-column .is-collapsed").count(), 0, "a tall window folds nothing");
    results.push("space: nothing folds at 900 px");
    await context.close();
  }
  console.log(results.map(line => `PASS ${line}`).join("\n"));
  console.log("widgets auto: PASS");
} finally {
  await browser.close();
  await server.close();
}
