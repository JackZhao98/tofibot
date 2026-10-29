// Retry on a failed-run card: busy at once, the reason inline on failure, and the card folds to a one-line
// note once the new run exists. Synthetic page, no backend.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:retry-feedback
import assert from "node:assert/strict";
import { launch, startUi } from "./computer-window-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
try {
  const page = await (await browser.newContext({ viewport: { width: 800, height: 700 } })).newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(`${origin}/fixtures/task-retry-feedback.html`);
  const retry = page.locator("[data-issue-retry]");
  await retry.waitFor();
  assert.equal(await retry.innerText(), "Retry");
  assert.equal(await retry.isEnabled(), true);

  // Click: busy immediately, before the server answers.
  await retry.click();
  await page.waitForFunction(() => window.retryCalls === 1);
  assert.equal(await retry.innerText(), "Retrying…");
  assert.equal(await retry.isDisabled(), true);
  assert.equal(await page.locator(".task-issue-card").getAttribute("aria-busy"), "true");
  assert.equal(await page.locator("[data-issue-retried]").count(), 0, "the card does not fold before the run exists");

  // Error path: reason inline, button back.
  await page.evaluate(() => window.retryGate.reject("Conversation is archived"));
  await page.locator(".task-issue-feedback").waitFor();
  assert.match(await page.locator(".task-issue-feedback").innerText(), /did not start: Conversation is archived/);
  assert.equal(await retry.isEnabled(), true);
  assert.equal(await retry.innerText(), "Retry");

  // Success path: busy again, then the card folds once the new run exists.
  await retry.click();
  await page.waitForFunction(() => window.retryCalls === 2);
  assert.equal(await retry.isDisabled(), true);
  assert.equal(await page.locator(".task-issue-feedback").count(), 0, "the old error clears on a new attempt");
  await page.evaluate(() => window.retryGate.resolve());
  const note = page.locator("[data-issue-retried]");
  await note.waitFor();
  assert.equal(await note.innerText(), "Retried. The new run is below.");
  assert.equal(await page.locator(".task-issue-card").count(), 0, "the failure card is gone");
  assert.equal(await page.locator("[data-run-id='run-retry']").count(), 1, "the live run is drawn");
  assert.equal(await page.evaluate(() => document.activeElement?.hasAttribute("data-issue-retried")), true, "focus moves to the note, not the page");
  assert.deepEqual(errors, []);
  console.log("PASS: retry busy -> error re-enables -> card folds to a Retried note once the new run starts");
} finally {
  await browser.close();
  await server.close();
}
