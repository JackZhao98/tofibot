// Mail draft card: Cc and Bcc the Bot added are editable like To, so a person can change or drop them before sending.
// Synthetic data; /api is answered in the browser. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:mail-draft-copies
import assert from "node:assert/strict";
import { createFixture } from "./lib/chat-fixture.mjs";

const fixture = await createFixture();
const iso = fixture.iso;
const draft = {
  draft_id: "d1", conversation_id: "dm_a", bot_id: "bot_a", run_id: "run_orphan_mail", to: "to@example.invalid", cc: "bot-cc@example.invalid", bcc: "stranger@example.invalid",
  subject: "Synthetic plan", body: "Synthetic body", demo: false, status: "pending", revision: 1, created_at: iso(900), updated_at: iso(900),
};
const patches = [];
const route = async (route) => {
  const url = new URL(route.request().url());
  if (url.pathname === "/api/mail-drafts") { await route.fulfill({ status: 200, json: { drafts: [draft] } }); return true; }
  if (url.pathname === "/api/mail-drafts/d1" && route.request().method() === "PATCH") {
    const body = JSON.parse(route.request().postData());
    patches.push(body);
    Object.assign(draft, body, { revision: draft.revision + 1 });
    if (body.cc === "") delete draft.cc;
    if (body.bcc === "") delete draft.bcc;
    await route.fulfill({ status: 200, json: { draft } });
    return true;
  }
  return false;
};
let failed = 0;
const check = async (name, run) => { try { await run(); console.log(`ok - ${name}`); } catch (error) { failed++; console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); } };
const { page, errors } = await fixture.open({ route });
try {
  const card = page.locator('[data-draft-id="d1"]');
  await card.waitFor({ timeout: 8000 });
  await check("Cc and Bcc show read-only until Edit", async () => {
    assert.equal(await card.locator("input").count(), 0);
    assert.ok(await card.getByText("bot-cc@example.invalid").count());
    assert.ok(await card.getByText("stranger@example.invalid").count());
  });
  await check("Edit makes Cc and Bcc inputs beside To", async () => {
    await card.getByRole("button", { name: "Edit" }).click();
    assert.equal(await card.getByLabel("CC", { exact: true }).inputValue(), "bot-cc@example.invalid");
    assert.equal(await card.getByLabel("BCC", { exact: true }).inputValue(), "stranger@example.invalid");
  });
  await check("clearing Bcc and changing Cc sends both in the PATCH", async () => {
    await card.getByLabel("BCC", { exact: true }).fill("");
    await card.getByLabel("CC", { exact: true }).fill("me@example.invalid");
    await card.getByRole("button", { name: "Save draft" }).click();
    await page.waitForFunction(() => !document.querySelector('[data-draft-id="d1"] input'), null, { timeout: 5000 });
    assert.equal(patches.length, 1);
    assert.equal(patches[0].cc, "me@example.invalid");
    assert.equal(patches[0].bcc, "");
    assert.equal(patches[0].revision, 1);
  });
  await check("the saved card no longer shows the removed Bcc", async () => {
    await card.getByText("me@example.invalid").first().waitFor({ timeout: 5000 });
    assert.equal(await card.getByText("stranger@example.invalid").count(), 0);
    assert.equal(await card.getByText("BCC").count(), 0);
  });
  await check("no page errors", async () => assert.deepEqual(errors, []));
} finally { await fixture.close(); }
if (failed) { console.log(`${failed} check(s) failed`); process.exit(1); }
