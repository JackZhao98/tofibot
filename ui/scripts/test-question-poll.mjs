// Questions list cost: an idle conversation must stop polling at the 3 s rate and a settled approval must render from the
// compact list row (payload and facts are fetched only when the record is expanded). Synthetic data; /api is answered in the browser.
//  - idle: the list is requested with backoff (3, 6, 12 s ... capped at 15 s), not every 3 s;
//  - a pending question keeps the 3 s cadence, and a "question" stream event shows it at once;
//  - once answered the polling backs off again;
//  - a compact settled approval renders its collapsed record, expanding it fetches GET /api/questions/{id} once
//    and shows the payload and the server facts.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:question-poll
import assert from "node:assert/strict";
import { createFixture } from "./lib/chat-fixture.mjs";

const fixture = await createFixture();
const iso = fixture.iso;
const compact = {
  type: "question", question_id: "q_done", conversation_id: "dm_a", run_id: "run_orphan", bot_id: "bot_a", question_type: "approval", question: "Run the shell command `ls`?",
  status: "answered", answer: true, answered_by: "human", created_at: iso(39 * 20 + 1), updated_at: iso(39 * 20 + 5), detail_omitted: true,
  approval: { action: "Run ls", target: "tofi_shell", impact: "Lists files", review: { source: "auto-review", status: "human_required", reason: "ok", model: "m", confirmation_required: true } },
};
const detail = { ...compact, detail_omitted: undefined, approval: { ...compact.approval, payload: '{"command":"ls -la /srv/synthetic"}', facts: { kind: "mcp_create", name: "files-server", source: "https://files.example.com/mcp", transport: "streamable_http" } } };
const pending = { ...compact, question_id: "q_new", run_id: "run_new", question: "Delete the synthetic cache?", status: "pending", answer: undefined, answered_by: undefined, detail_omitted: undefined, created_at: iso(900), updated_at: iso(900), approval: { action: "Delete cache", target: "cache", impact: "Frees space" } };

const state = { questions: [compact], listCalls: [], detailCalls: 0, archived: false };
const route = async (route) => {
  const url = new URL(route.request().url());
  if (url.pathname === "/api/conversations" && state.archived) { await route.fulfill({ status: 200, json: { conversations: [{ ...fixture.dm, archived: true }] } }); return true; }
  if (url.pathname === "/api/questions") { state.listCalls.push(Date.now()); await route.fulfill({ status: 200, json: { questions: state.questions } }); return true; }
  if (url.pathname === "/api/questions/q_done") { state.detailCalls++; await route.fulfill({ status: 200, json: { question: detail } }); return true; }
  return false;
};
let failed = 0;
const check = async (name, run) => { try { await run(); console.log(`ok - ${name}`); } catch (error) { failed++; console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); } };
const { page, errors } = await fixture.open({ route });
try {
  await check("idle list polling backs off instead of repeating every 3 s", async () => {
    state.listCalls.length = 0;
    await page.waitForTimeout(22_000);
    // Old behaviour: 7 requests in 22 s. Backoff: polls at +3, +9 (3+6) and +21 (3+6+12) at most.
    assert.ok(state.listCalls.length <= 4, `${state.listCalls.length} list requests in 22 s while idle`);
    assert.ok(state.listCalls.length >= 1, "idle polling stopped completely");
  });
  await check("a question event shows a pending card within 3 s even while backed off", async () => {
    state.questions = [pending, compact];
    await page.evaluate(() => window.__emit("question", { question_id: "q_new" }));
    await page.locator('[data-question-id="q_new"]').waitFor({ timeout: 3000 });
  });
  await check("a pending question keeps the 3 s cadence", async () => {
    state.listCalls.length = 0;
    await page.waitForTimeout(10_000);
    assert.ok(state.listCalls.length >= 3, `${state.listCalls.length} list requests in 10 s with a pending question`);
  });
  await check("answering backs the polling off again", async () => {
    state.questions = [{ ...pending, status: "answered", answer: true, answered_by: "human", detail_omitted: true, updated_at: iso(950) }, compact];
    await page.evaluate(() => window.__emit("question_answered", {}));
    await page.waitForTimeout(4000);
    state.listCalls.length = 0;
    await page.waitForTimeout(18_000);
    assert.ok(state.listCalls.length <= 3, `${state.listCalls.length} list requests in 18 s after the answer`);
  });
  await check("a compact settled approval in a read-only chat fetches detail once and shows payload and facts", async () => {
    state.archived = true; state.detailCalls = 0; state.questions = [compact];
    await page.reload();
    const card = page.locator('[data-question-id="q_done"]');
    await card.waitFor({ timeout: 8000 });
    await page.getByText("ls -la /srv/synthetic", { exact: false }).first().waitFor({ state: "attached", timeout: 5000 });
    assert.ok(await page.getByText("files-server").count(), "server facts missing after the detail arrived");
    assert.equal(state.detailCalls, 1, "detail should be fetched exactly once");
    assert.ok(await card.getByText("Run ls").count(), "collapsed fields from the compact row are missing");
  });
  await check("no page errors", async () => assert.deepEqual(errors, []));
} finally { await fixture.close(); }
if (failed) { console.log(`${failed} check(s) failed`); process.exit(1); }
