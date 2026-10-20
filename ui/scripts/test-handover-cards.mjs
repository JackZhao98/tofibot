// Handover cards (request_takeover / request_connection): render, buttons, mobile width, answer bodies,
// handover binding, release-before-answer, and per-key interaction cost. Synthetic only: Vite dev server for
// the real App, Playwright stubs for every /api/* call.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:handover-cards
import assert from "node:assert/strict";
import { installStubs, launch, startUi } from "./computer-window-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
const NOW = "2026-10-20T12:00:00Z";
const base = { conversation_id: "conv-synthetic", bot_id: "bot-synthetic", run_id: "run-1", type: "question", created_at: NOW, updated_at: NOW, status: "pending" };
const takeover = (extra = {}) => ({ ...base, question_id: "q-take", question_type: "takeover", question: "Enter the verification code for your account.", source_url: "https://login.example.test", ...extra });
const connection = (extra = {}) => ({ ...base, question_id: "q-conn", question_type: "connection", question: "Connect Notes so I can file the summary.", target: "notes", ...extra });
const results = [];

async function open({ question, mobile = false, vm = true, vmStatus = "pending" }) {
  const context = await browser.newContext(mobile ? { viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true } : { viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const state = { questions: [question], requests: [], vmStatus };
  await installStubs(page, { info: { kind: "firecracker", state: "ready" }, requests: [] });
  await page.route("**/api/**", async route => {
    const request = route.request();
    const url = new URL(request.url());
    const p = url.pathname;
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    const body = request.method() === "POST" ? request.postData() : null;
    if (p === "/api/questions") return json({ questions: state.questions });
    if (p.startsWith("/api/questions/") && p.endsWith("/answer")) {
      state.requests.push({ path: p, body });
      const answered = { ...state.questions[0], status: "answered", answer: JSON.parse(body).result, updated_at: "2026-10-20T12:00:05Z" };
      if (state.answerStatus) return json({ error: { code: state.answerCode ?? "x", message: "x" } }, state.answerStatus);
      state.questions = [answered];
      return json({ question: answered });
    }
    if (p === "/api/computers/firecracker/actions") {
      const action = JSON.parse(request.postData() ?? "{}");
      if (action.action.startsWith("desktop.control.")) {
        state.requests.push({ path: action.action, body: JSON.stringify(action.args) });
        return json({ ok: true, result: { control_id: "ctl-1", lease_ms: 15000, renew_after_ms: 5000 } });
      }
      return route.fallback();
    }
    if (p === "/api/extensions/oauth-options") return json({ vm_available: vm, web_callback_origin: "", desktop_redirect_uri: "" });
    if (p === "/api/extensions/mcp") return json({ servers: [{ name: "notes", url: "https://mcp.example.test", oauth: { connected: false } }] });
    if (p === "/api/extensions/mcp/notes/oauth/vm/start") { state.requests.push({ path: p, body }); return json({ session_id: "s1", bot_id: "bot-synthetic", status: "pending" }); }
    if (p === "/api/extensions/mcp/notes/oauth/vm/s1/status") return json({ session_id: "s1", bot_id: "bot-synthetic", status: state.vmStatus });
    if (p === "/api/extensions/mcp/notes/oauth/vm/s1/cancel") { state.requests.push({ path: p, body }); return json({}); }
    return route.fallback();
  });
  await page.goto(origin + "/");
  if (mobile) await page.locator(".conversation-row").first().click();
  await page.locator(`[data-question-id="${question.question_id}"]`).waitFor({ timeout: 20000 });
  return { context, page, errors, state };
}
const answers = state => state.requests.filter(r => r.path.endsWith("/answer")).map(r => JSON.parse(r.body));
const noOverflow = async page => assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "no horizontal scroll");
const card = (page, id) => page.locator(`[data-question-id="${id}"]`);

try {
  for (const mobile of [false, true]) {
    const label = mobile ? "mobile" : "desktop";
    // Takeover: renders what/where and the three buttons; Done and Can't do it answer one word.
    {
      const { context, page, errors, state } = await open({ question: takeover(), mobile });
      const c = card(page, "q-take");
      await c.getByText("Enter the verification code for your account.").waitFor();
      assert.ok((await c.innerText()).includes("https://login.example.test"), "names the site");
      for (const name of ["Take over", "Done", "Can't do it"]) await c.getByRole("button", { name, exact: true }).waitFor();
      if (mobile) for (const name of ["Take over", "Done", "Can't do it"]) assert.ok((await c.getByRole("button", { name, exact: true }).boundingBox()).height >= 40, `${name} is a touch target`);
      await noOverflow(page);
      await c.getByRole("button", { name: "Done", exact: true }).click();
      await c.getByText("Handed back to the Bot").waitFor();
      assert.deepEqual(answers(state), [{ result: "done" }]);
      assert.deepEqual(errors, []);
      results.push(`${label}: takeover renders and Done answers one word`);
      await context.close();
    }
    {
      const { context, page, state } = await open({ question: takeover(), mobile });
      await card(page, "q-take").getByRole("button", { name: "Can't do it", exact: true }).click();
      await card(page, "q-take").getByText("Couldn't be done").waitFor();
      assert.deepEqual(answers(state), [{ result: "cannot" }]);
      results.push(`${label}: takeover Can't do it`);
      await context.close();
    }
    // Take over: modal, bound acquire, release before the answer, Back to chat keeps the request open.
    {
      const { context, page, state } = await open({ question: takeover(), mobile });
      await card(page, "q-take").getByRole("button", { name: "Take over", exact: true }).click();
      const dialog = page.getByRole("dialog");
      await dialog.waitFor();
      await noOverflow(page);
      await page.waitForFunction(() => true);
      await dialog.getByRole("button", { name: "Back to chat", exact: true }).waitFor();
      await state.requests.length;
      await page.waitForTimeout(300);
      const acquire = state.requests.find(r => r.path === "desktop.control.acquire");
      assert.ok(acquire, "dialog acquires control on open");
      assert.deepEqual(JSON.parse(acquire.body), { handover_question: "q-take" }, "acquire is bound to the open card");
      await dialog.getByRole("button", { name: "Back to chat", exact: true }).click();
      await dialog.waitFor({ state: "detached" });
      assert.ok(state.requests.some(r => r.path === "desktop.control.release"), "leaving releases control");
      assert.deepEqual(answers(state), [], "leaving does not answer");
      await card(page, "q-take").getByRole("button", { name: "Done", exact: true }).waitFor();
      state.requests.length = 0;
      await card(page, "q-take").getByRole("button", { name: "Take over", exact: true }).click();
      await page.waitForFunction(() => document.querySelector(".remote-desktop.in-control"));
      state.requests.length = 0; // only what Done itself sends
      await page.getByRole("dialog").getByRole("button", { name: "Done", exact: true }).click();
      await page.getByRole("dialog").waitFor({ state: "detached" });
      const order = state.requests.map(r => r.path);
      assert.ok(order.indexOf("desktop.control.release") >= 0 && order.indexOf("desktop.control.release") < order.findIndex(p => p.endsWith("/answer")), `release precedes answer: ${order}`);
      assert.deepEqual(answers(state), [{ result: "done" }]);
      results.push(`${label}: takeover dialog binds, releases before answering, back keeps it open`);
      await context.close();
    }
    // Connection card.
    {
      const { context, page, errors, state } = await open({ question: connection(), mobile, vmStatus: "complete" });
      const c = card(page, "q-conn");
      await c.getByText("Connect Notes so I can file the summary.").waitFor();
      assert.ok((await c.innerText()).includes("notes"));
      await c.getByRole("button", { name: "Not now", exact: true }).waitFor();
      await noOverflow(page);
      await c.getByRole("button", { name: "Connect", exact: true }).click();
      await c.getByText("Connected", { exact: true }).waitFor({ timeout: 10000 });
      const start = state.requests.find(r => r.path.endsWith("/oauth/vm/start"));
      assert.deepEqual(JSON.parse(start.body), { bot_id: "bot-synthetic" });
      assert.deepEqual(answers(state), [{ result: "connected" }]);
      assert.deepEqual(errors, []);
      results.push(`${label}: connection success via the same VM flow Settings uses`);
      await context.close();
    }
    {
      const { context, page, state } = await open({ question: connection(), mobile, vmStatus: "error" });
      const c = card(page, "q-conn");
      await c.getByRole("button", { name: "Connect", exact: true }).click();
      await c.getByRole("alert").waitFor({ timeout: 10000 });
      await c.getByRole("button", { name: "Try again", exact: true }).waitFor();
      assert.deepEqual(answers(state), [], "a retryable failure leaves the request open");
      await c.getByRole("button", { name: "Not now", exact: true }).click();
      await c.getByText("Not connected", { exact: true }).waitFor();
      assert.deepEqual(answers(state), [{ result: "declined" }]);
      results.push(`${label}: connection failure is retryable, Not now declines`);
      await context.close();
    }
    {
      const { context, page, state } = await open({ question: connection(), mobile, vm: false });
      const c = card(page, "q-conn");
      await c.getByRole("button", { name: "Connect", exact: true }).click();
      await c.getByText("Couldn't connect", { exact: true }).waitFor({ timeout: 10000 });
      assert.deepEqual(answers(state), [{ result: "failed" }], "a blocked route reports failure");
      results.push(`${label}: connection blocked route reports failure`);
      await context.close();
    }
    for (const [kind, make, text] of [["takeover", takeover, "Expired"], ["connection", connection, "Expired"]]) {
      const { context, page } = await open({ question: make({ status: "expired" }), mobile });
      await card(page, make().question_id).getByText(text, { exact: true }).waitFor();
      assert.equal(await card(page, make().question_id).getByRole("button").count(), 0, `${kind}: an expired card offers no buttons`);
      await context.close();
    }
    results.push(`${label}: expired cards are inert`);
  }

  // Interaction cost: typing into the takeover dialog must not re-lay-out the page per key.
  {
    const { context, page, state } = await open({ question: takeover() });
    await card(page, "q-take").getByRole("button", { name: "Take over", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.waitFor();
    await page.waitForFunction(() => document.querySelector(".remote-desktop.in-control"));
    const session = await context.newCDPSession(page);
    await session.send("Performance.enable");
    const metric = async name => (await session.send("Performance.getMetrics")).metrics.find(m => m.name === name).value;
    const keys = 40;
    const before = { layout: await metric("LayoutCount"), recalc: await metric("RecalcStyleCount") };
    const t0 = Date.now();
    await page.locator(".remote-keyboard-input").focus();
    for (let i = 0; i < keys; i++) await page.keyboard.press("a");
    const elapsed = Date.now() - t0;
    const layout = (await metric("LayoutCount")) - before.layout;
    const recalc = (await metric("RecalcStyleCount")) - before.recalc;
    console.log(`interaction cost: ${keys} keys, ${(elapsed / keys).toFixed(1)} ms/key, ${(layout / keys).toFixed(2)} layouts/key, ${(recalc / keys).toFixed(2)} style recalcs/key`);
    // The remote keyboard textarea resets its value per key (one layout + one style pass, as in the OAuth dialog); the card adds none.
    assert.ok(elapsed / keys <= 8, `ms per key ${elapsed / keys}`);
    assert.ok(layout / keys <= 3, `layouts per key ${layout / keys}`);
    assert.ok(recalc / keys <= 3, `style recalcs per key ${recalc / keys}`);
    await page.waitForTimeout(400);
    assert.ok(state.requests.some(r => r.path === "desktop.control.input"), "keys reach the control channel");
    assert.deepEqual(answers(state), [], "typing never produces an answer");
    results.push("interaction cost measured");
    await context.close();
  }
  console.log(`PASS ${results.length} handover card checks\n - ${results.join("\n - ")}`);
} finally {
  await browser.close();
  await server.close();
}
