// First-run onboarding, rendered: welcome, model (ChatGPT sign-in and API key), services queue, the
// skipped state (sidebar chip and composer banner), returning accounts, reduced motion, phones and German.
// Synthetic data only; the app's /api is answered in the browser by a small stateful stub.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:onboarding
// Screenshots (1440x900 and 390x844, light and dark) go to artifacts/onboarding/ (gitignored).
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "onboarding");
mkdirSync(shots, { recursive: true });

const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const at = "2026-10-01T00:00:00Z";
const CODE = "WQ7K-3MPD";
const SYNTHETIC_KEY = "sk-proj-synthetic-0000000000";
const modelsByProvider = {
  none: [],
  three: [
    { id: "synthetic-a", name: "Synthetic A", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium" },
    { id: "synthetic-b", name: "Synthetic B", reasoning_efforts: ["minimal", "low", "medium", "high", "xhigh"], default_reasoning: "medium" },
    { id: "synthetic-c", name: "Synthetic C", reasoning_efforts: [], default_reasoning: "" },
  ],
};

/** A tiny stateful stand-in for the server: every route the onboarding touches, nothing else. */
function makeServer({ model = false, bots = 0, onboarding = { step: 0, completed: false, skipped: false }, keyAttempts = [] } = {}) {
  const state = { model, onboarding: { ...onboarding }, bots: [], calls: [], codexConnected: false, keyTries: 0, keyAttempts, mcp: [], modelSettings: { model: "synthetic-a", reasoning_effort: "medium" }, popups: 0 };
  const makeBot = () => ({ id: "bot_1", name: "New Bot", instructions: "", model: "default", reasoning_effort: "default", dm_conversation_id: "conv_1", created_at: at, archived: false });
  if (bots) state.bots.push(makeBot());
  state.makeBot = makeBot;
  return state;
}
const dmFor = bot => ({ id: bot.dm_conversation_id, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: at, read_seq: 0, user_visible: true });

async function mockApi(page, state) {
  const external = [];
  page.on("request", request => { const url = new URL(request.url()); if (url.origin !== origin && !url.protocol.startsWith("data")) external.push(request.url()); });
  await page.context().route(url => new URL(url).origin !== origin && !String(url).startsWith("data"), route => route.fulfill({ status: 200, contentType: "text/html", body: "<title>stub</title>" }));
  await page.route("**/api/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const body = () => { try { return JSON.parse(request.postData() || "{}"); } catch { return {}; } };
    const json = (value, status = 200) => route.fulfill({ status, json: value });
    state.calls.push(`${method} ${path}`);
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
    if (path === "/api/config") return json({ model_configured: state.model, default_model: state.model ? state.modelSettings.model : "", provider: "synthetic" });
    if (path === "/api/bots" && method === "GET") return json({ bots: state.bots });
    if (path === "/api/bots" && method === "POST") { const bot = state.makeBot(); state.bots = [bot]; return json(bot, 201); }
    if (path === "/api/conversations") return json({ conversations: state.bots.map(dmFor) });
    if (path === "/api/onboarding" && method === "GET") return json(state.onboarding);
    if (path === "/api/onboarding" && method === "PUT") {
      const update = body(), current = state.onboarding;
      const next = { ...current };
      if (typeof update.step === "number") next.step = Math.max(current.step, update.step);
      if (update.completed) { next.completed = true; next.skipped = false; }
      if (typeof update.skipped === "boolean" && !next.completed) next.skipped = update.skipped;
      state.onboarding = next;
      return json(next);
    }
    if (path === "/api/onboarding/first-bot") {
      if (state.bots.length) return json({ bot: state.bots[0], created: false });
      if (!state.model) return json({ error: { code: "model_required", message: "connect a model first" } }, 409);
      state.bots = [state.makeBot()];
      return json({ bot: state.bots[0], created: true }, 201);
    }
    if (path === "/api/auth/codex/connect") { state.popups++; return json({ session_id: "sess_1", verification_url: "https://auth.example.test/device", user_code: CODE, expires_at: Date.now() + 120000, interval: 1 }); }
    if (path === "/api/auth/codex/connect/sess_1/poll") { if (state.codexConnected) { state.model = true; return json({ connected: true, pending: false }); } return json({ connected: false, pending: true }); }
    if (path === "/api/auth/codex") return json({ connected: state.codexConnected });
    if (path === "/api/providers" && method === "GET") return json({ providers: [] });
    if (path.startsWith("/api/providers/") && path.endsWith("/key")) {
      state.keyTries++;
      await new Promise(resolve => setTimeout(resolve, state.keyDelay ?? 500));
      if (state.keyTries <= state.keyFailures) return json({ error: { code: "invalid_key", message: "" } }, 400);
      state.model = true;
      return json({ id: "openai", label: "OpenAI", kind: "api_key", configured: true, key_hint: "…0000" });
    }
    if (path === "/api/models") return json({ models: state.model ? (state.catalog ?? modelsByProvider.three) : [], source: "live" });
    if (path === "/api/model-settings" && method === "GET") return json(state.modelSettings);
    if (path === "/api/model-settings" && method === "PUT") { state.modelSettings = body(); return json(state.modelSettings); }
    if (path === "/api/extensions/oauth-options") return json({ vm_available: false, web_callback_origin: "", desktop_redirect_uri: "" });
    if (path === "/api/extensions/mcp" && method === "GET") return json({ servers: state.mcp });
    if (path === "/api/extensions/mcp") { const server = body(); state.mcp = [...state.mcp.filter(item => item.name !== server.name), server]; return json({}); }
    if (path.endsWith("/test") && path.startsWith("/api/extensions/mcp/")) return json({ ok: true, tool_count: 7 });
    if (path.endsWith("/messages")) return json({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [] });
    if (path.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    if (path === "/api/secret-inputs") return json({ requests: [] });
    return json({});
  });
  return external;
}

async function open(state, { width = 1440, height = 900, theme = "light", reduced = false, locale = "en-US", url = "/" } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale, reducedMotion: reduced ? "reduce" : "no-preference", colorScheme: theme, permissions: ["clipboard-read", "clipboard-write"] });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const external = await mockApi(page, state);
  await page.goto(origin + url);
  return { page, context, errors, external };
}

const sheet = page => page.locator("[data-onboarding='true'] .onb-sheet");
const overflow = page => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
async function settle(page) { await page.evaluate(() => document.fonts.ready); await page.waitForTimeout(900); }
let currentShot = null;
async function shoot(page, name) { if (currentShot) { await settle(page); await page.screenshot({ path: join(shots, `${currentShot}-${name}.png`) }); } }
/** Nothing in the sheet may be cut off: no element's content is wider than its box. */
async function assertNoTruncation(page, label) {
  await page.waitForTimeout(500); // let the step slide finish
  const clipped = await page.evaluate(() => {
    const bad = [];
    for (const node of document.querySelectorAll(".onb-sheet button, .onb-sheet strong, .onb-sheet small, .onb-sheet h2, .onb-sheet p, .onb-sheet label, .onb-sheet span, .onb-sheet code, .onb-sheet a")) {
      const style = getComputedStyle(node);
      if (style.display === "none" || node.getClientRects().length === 0) continue;
      if (style.textOverflow === "ellipsis") bad.push(`ellipsis: ${node.textContent.trim().slice(0, 40)}`);
      if (node.scrollWidth > node.clientWidth + 1 && style.display !== "inline" && style.overflowX !== "visible") bad.push(`clipped: ${node.textContent.trim().slice(0, 40)}`);
      const rect = node.getBoundingClientRect(), box = document.querySelector(".onb-sheet").getBoundingClientRect();
      if (rect.right > box.right + 1 || rect.left < box.left - 1) bad.push(`outside the sheet: ${node.textContent.trim().slice(0, 40)}`);
    }
    return bad;
  });
  assert.deepEqual(clipped, [], `${label}: truncated or overflowing text`);
}

const results = [];
const pass = name => { results.push(name); console.log("PASS", name); };

/** The whole sheet once, with a shot of every step and state. `check` adds the viewport's assertions. */
async function walkthrough({ width, height, theme, locale = "en-US", shotsOn = true, strict = false }) {
  const state = makeServer();
  state.keyFailures = 1;
  const { page, context, errors } = await open(state, { width, height, theme, locale });
  currentShot = shotsOn ? `${width}x${height}-${theme}` : null;
  const phone = width < 700;
  try {
    // 1. Welcome.
    await sheet(page).waitFor();
    await page.locator(".onb-welcome h2").waitFor();
    await page.waitForTimeout(1500);
    assert.equal(await page.locator("[data-empty-cat='onboarding-miso'][data-live='true'], [data-empty-cat='onboarding-yuzu'][data-live='true'], [data-empty-cat='onboarding-nori'][data-live='true']").count(), 3, "three live cats on the welcome step");
    assert.equal(await page.locator(".onb-bubble").count(), 3);
    assert.ok((await page.locator("[data-empty-cat][data-live='true']").count()) <= 6, "at most six live cats");
    if (phone) assert.ok(Math.abs((await sheet(page).boundingBox()).height - height) <= 2, "the sheet fills the phone screen");
    else assert.equal(Math.round((await sheet(page).boundingBox()).width), 680, "680px sheet on desktop");
    assert.ok((await overflow(page)) <= 0, "no horizontal scroll on the welcome step");
    if (strict) await assertNoTruncation(page, "welcome");
    await shoot(page, "1-welcome");

    // 2. Model, default state.
    await page.getByRole("button", { name: /Let.s set up your crew|Los geht/ }).click();
    await page.getByRole("radio").first().waitFor();
    assert.equal(await page.locator(".onb-choice[aria-checked='true']").count(), 1);
    assert.equal(await page.locator(".onb-choice").first().getAttribute("aria-checked"), "true", "ChatGPT is the default choice");
    assert.ok((await page.locator(".onb-count").innerText()).startsWith(locale.startsWith("de") ? "2 von 3" : "2 of 3"));
    assert.ok((await overflow(page)) <= 0);
    if (strict) await assertNoTruncation(page, "model default");
    await shoot(page, "2-model-default");

    // 2b. ChatGPT waiting state: lagoon dot, code, Copy, Cancel.
    await page.locator(".onb-primary").click();
    await page.locator(".onb-code").waitFor();
    assert.equal((await page.locator(".onb-code").innerText()).trim(), CODE);
    await page.locator(".onb-waiting i").waitFor();
    assert.ok((await page.locator(".onb-waiting i").evaluate(node => getComputedStyle(node).animationName)) !== "", "waiting dot present");
    assert.ok((await overflow(page)) <= 0);
    if (strict) await assertNoTruncation(page, "chatgpt waiting");
    await shoot(page, "3-chatgpt-waiting");
    await page.locator(".onb-code-actions .secondary-button").click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), CODE, "Copy code copies the code");
    await page.locator(".onb-foot .onb-quiet").click();
    await page.locator(".onb-code").waitFor({ state: "detached" });

    // 2c. API key: checking, then an inline error, then success.
    await page.locator(".onb-choice").nth(1).click();
    await page.locator("#onb-key").fill(SYNTHETIC_KEY);
    await page.locator(".onb-keyrow button").click();
    await page.getByText(/Checking your key|Dein Schlüssel wird geprüft/).waitFor();
    assert.equal(await page.locator("#onb-key").isDisabled(), true, "the field is locked while checking");
    if (strict) await assertNoTruncation(page, "key checking");
    await shoot(page, "4-key-checking");
    await page.locator("#onb-key-error").waitFor();
    assert.match(await page.locator("#onb-key-error").innerText(), /sk-/);
    assert.equal(await page.locator("#onb-key").getAttribute("aria-invalid"), "true");
    assert.equal(await page.locator("#onb-key").inputValue(), SYNTHETIC_KEY, "the key stays for an easy fix");
    if (strict) await assertNoTruncation(page, "key error");
    await shoot(page, "5-key-error");
    await page.locator(".onb-keyrow button").click();
    await page.locator(".onb-connected").waitFor();
    assert.ok(state.calls.includes("POST /api/onboarding/first-bot"), "the first Bot is created once a model works");
    assert.equal(state.bots.length, 1);
    await page.locator(".onb-advanced .disclosure-toggle").click();
    await page.locator("#model-default-select").waitFor();
    assert.equal(await page.locator("[data-onboarding] .onb-seg").count(), 1, "three efforts render as a segmented control");
    assert.ok((await overflow(page)) <= 0);
    if (strict) await assertNoTruncation(page, "connected");
    await shoot(page, "6-connected");

    // 3. Services.
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-tiles").waitFor();
    assert.equal(await page.locator(".onb-tile").count(), 6);
    const names = await page.locator(".onb-tile strong").allInnerTexts();
    assert.ok(!names.includes("Slack") && !names.includes("Discord"), "no tile without a working connect path");
    assert.ok(names.includes("Robinhood"));
    await page.locator("[data-integration-id='github']").click();
    await page.locator("[data-integration-id='linear']").click();
    assert.match(await page.locator(".onb-foot .onb-primary").innerText(), /2/);
    if (strict) await assertNoTruncation(page, "services pick");
    await shoot(page, "7-services-pick");
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-queue").waitFor();
    assert.deepEqual(await page.locator(".onb-queue li").evaluateAll(items => items.map(item => item.dataset.state)), ["now", "next"]);
    await page.locator(".onb-service-form input[type=password]").fill("synthetic-token");
    if (strict) await assertNoTruncation(page, "services walk");
    await shoot(page, "8-services-walk");
    await page.locator(".onb-service-main").click();
    await page.locator(".onb-service-done").waitFor();
    await shoot(page, "9-services-done");
    await page.locator(".onb-foot .onb-primary").click();
    assert.deepEqual(await page.locator(".onb-queue li").evaluateAll(items => items.map(item => item.dataset.state)), ["done", "now"]);
    await page.locator(".onb-foot .onb-quiet").click();

    // 4. Lands in the first Bot's DM with the composer focused.
    await sheet(page).waitFor({ state: "detached" });
    await page.locator(".composer textarea").waitFor();
    await page.waitForFunction(() => document.activeElement === document.querySelector(".composer textarea"));
    assert.equal(state.onboarding.completed, true, "completion is stored on the server");
    assert.equal(state.mcp.length, 1);
    assert.equal(await page.locator("[data-finish-setup]").count(), 0, "no chip after finishing");
    assert.deepEqual(errors, []);
  } finally { currentShot = null; await context.close(); }
}

try {
  // Shots and per-viewport assertions: desktop and phone, light and dark.
  for (const [width, height] of [[1440, 900], [390, 844]]) for (const theme of ["light", "dark"]) {
    await walkthrough({ width, height, theme });
    pass(`walkthrough ${width}x${height} ${theme}: welcome, model, key error and success, services, DM focus`);
  }

  // Skip on the welcome step: chip and banner, and the chip resumes at step 2.
  {
    const state = makeServer();
    const { page, context, errors } = await open(state);
    await sheet(page).waitFor();
    await page.getByRole("button", { name: "Skip for now" }).click();
    await sheet(page).waitFor({ state: "detached" });
    assert.equal(state.onboarding.skipped, true);
    const chip = page.locator("[data-finish-setup]");
    await chip.waitFor();
    assert.match(await chip.innerText(), /Finish setup/);
    assert.match(await chip.innerText(), /2 of 3/);
    // No Bot yet and no model: the first-run page, as before. Create one to see the composer banner.
    await page.locator("[data-first-run='pane'] .first-run-create").click();
    await page.locator("[data-model-banner]").waitFor();
    assert.match(await page.locator("[data-model-banner]").innerText(), /Connect a model to start chatting/);
    assert.match(await page.locator("[data-model-banner]").innerText(), /New Bot can.t think without one\. It takes about a minute\./);
    assert.equal(await page.locator(".composer textarea").isDisabled(), true, "the composer is disabled without a model");
    assert.ok(Number(await page.locator(".composer-row").evaluate(node => getComputedStyle(node).opacity)) < 1, "and translucent");
    assert.equal(await page.getByText("No model set up").count(), 0, "the bare line is gone");
    await page.waitForTimeout(600);
    {
      const cat = await page.locator(".composer-perch").boundingBox();
      const button = await page.locator("[data-model-banner] button").boundingBox();
      assert.ok(cat && button, "perched cat and Connect button are laid out");
      assert.equal(cat.x < button.x + button.width && cat.x + cat.width > button.x && cat.y < button.y + button.height && cat.y + cat.height > button.y, false, "1440 light: cat clear of Connect");
      assert.ok(cat.y >= 0 && cat.x >= 0, "the cat is fully on screen");
    }
    currentShot = "1440x900-light";
    await shoot(page, "10-after-skip-banner-chip");
    currentShot = null;
    // The banner's Connect and the chip both reopen setup; a stored skip resumes at step 2.
    await page.locator("[data-model-banner] button").click();
    await page.locator(".onb-choices").waitFor();
    assert.match(await page.locator(".onb-count").innerText(), /^2 of 3/);
    assert.deepEqual(errors, []);
    await context.close();
    for (const [w, h, theme] of [[1440, 900, "dark"], [390, 844, "light"], [390, 844, "dark"]]) {
      const view = await open(makeServer({ bots: 1, onboarding: { step: 1, completed: false, skipped: true } }), { width: w, height: h, theme, url: "/b/bot_1" });
      await view.page.locator("[data-model-banner]").waitFor();
      await view.page.waitForTimeout(600);
      const cat = await view.page.locator(".composer-perch").boundingBox();
      const button = await view.page.locator("[data-model-banner] button").boundingBox();
      if (cat) {
        assert.equal(cat.x < button.x + button.width && cat.x + cat.width > button.x && cat.y < button.y + button.height && cat.y + cat.height > button.y, false, `${w} ${theme}: cat clear of Connect`);
        assert.ok(cat.y >= 0, `${w} ${theme}: cat on screen`);
      }
      await view.context.close();
    }
    const second = await open(makeServer({ onboarding: { step: 1, completed: false, skipped: true } }));
    await second.page.locator("[data-finish-setup]").click();
    await second.page.locator(".onb-choices").waitFor();
    assert.match(await second.page.locator(".onb-count").innerText(), /^2 of 3/, "Finish setup resumes at step 2");
    await second.context.close();
    pass("welcome -> skip: Finish setup chip (2 of 3), composer banner, disabled composer, resume at step 2");
  }

  // "Not now" on the model step stores the skip too, and a reload resumes where the person was.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context } = await open(state);
    await page.locator(".onb-choices").waitFor();
    assert.match(await page.locator(".onb-count").innerText(), /^2 of 3 · required/);
    await page.getByRole("button", { name: "Not now" }).click();
    await page.locator("[data-finish-setup]").waitFor();
    assert.equal(state.onboarding.skipped, true);
    await page.reload();
    await page.locator("[data-finish-setup]").waitFor();
    assert.equal(await sheet(page).count(), 0, "a skipped setup does not reopen by itself");
    await context.close();
    const fresh = await open(makeServer({ onboarding: { step: 2, completed: false, skipped: false } }));
    await fresh.page.locator(".onb-choices").waitFor();
    assert.match(await fresh.page.locator(".onb-count").innerText(), /^2 of 3/, "an unfinished setup resumes on its step on any device");
    await fresh.context.close();
    pass("model step: Not now stores the skip; progress resumes after a reload");
  }

  // ChatGPT sign-in: waiting, then connected, with the first Bot created and a window opened.
  {
    const state = makeServer({ onboarding: { step: 1, completed: false, skipped: false } });
    const { page, context, errors } = await open(state);
    await page.getByRole("button", { name: /set up your crew/ }).click();
    const popup = context.waitForEvent("page");
    await page.getByRole("button", { name: "Continue with ChatGPT" }).click();
    const opened = await popup;
    await page.locator(".onb-code").waitFor();
    assert.equal((await page.locator(".onb-code").innerText()).trim(), CODE);
    assert.match(await page.locator(".onb-waiting").innerText(), /Waiting for you to finish signing in/);
    assert.equal(await page.getByText("Enter this code").count(), 1);
    await page.waitForFunction(() => document.querySelector(".onb-code")); // still waiting, not connected
    assert.equal(await page.locator(".onb-connected").count(), 0);
    await opened.waitForLoadState().catch(() => {});
    assert.equal(opened.url(), "https://auth.example.test/device", "the sign-in page opens in its own window");
    state.codexConnected = true;
    await page.locator(".onb-connected").waitFor({ timeout: 8000 });
    assert.match(await page.locator(".onb-connected").innerText(), /ChatGPT/);
    assert.match(await page.locator(".onb-connected").innerText(), /Your first Bot is ready and waiting/);
    assert.match(await page.locator(".onb-count").innerText(), /2 of 3 · done/);
    assert.equal(state.bots.length, 1);
    assert.deepEqual(errors, []);
    await context.close();
    pass("ChatGPT path: waiting with code, then connected with the first Bot ready");
  }

  // Advanced follows the model: segmented for up to four efforts, a select beyond that, nothing without efforts.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context } = await open(state);
    await page.locator(".onb-connected").waitFor();
    assert.match(await page.locator(".onb-chips").innerText(), /Default: Synthetic A/);
    assert.match(await page.locator(".onb-chips").innerText(), /Thinking: Medium/);
    await page.locator(".onb-advanced .disclosure-toggle").click();
    const thinking = page.locator(".onb-advanced .onb-seg");
    await thinking.waitFor();
    assert.deepEqual((await thinking.locator("button").allInnerTexts()).map(text => text.trim()), ["Low", "Medium", "High"], "efforts come from the model, not a fixed Light/Normal/Deep");
    await page.locator("#model-default-select").selectOption("synthetic-b");
    await page.locator("#model-effort-select").waitFor();
    assert.equal(await page.locator(".onb-advanced .onb-seg").count(), 0, "five efforts fall back to a select");
    assert.equal(await page.locator("#model-effort-select option:not([value=''])").count(), 5);
    assert.deepEqual(state.modelSettings, { model: "synthetic-b", reasoning_effort: "medium" }, "the choice is saved");
    await page.locator("#model-default-select").selectOption("synthetic-c");
    await page.waitForFunction(() => !document.querySelector("#model-effort-select") && !document.querySelector(".onb-advanced .onb-seg"));
    assert.equal(await page.locator(".onb-chips").innerText().then(text => /Thinking/.test(text)), false, "a model without efforts shows no thinking chip");
    await context.close();
    pass("Advanced: thinking options derive from the selected model (segmented, select, none)");
  }

  // A key that works but has no models, and an unreachable provider, each give one sentence and a fix.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    state.catalog = [];
    const { page, context } = await open(state);
    await page.locator(".onb-choice").nth(1).click();
    await page.locator("#onb-key").fill(SYNTHETIC_KEY);
    await page.locator(".onb-keyrow button").click();
    await page.locator("#onb-key-error").waitFor();
    assert.match(await page.locator("#onb-key-error").innerText(), /can.t use any models yet/);
    assert.equal(await page.locator("#onb-key-error a").count(), 1, "Open billing is offered");
    await context.close();
    pass("key without model access: one sentence and an Open billing link");
  }

  // Returning accounts (a model and a Bot) never see it, and neither do finished ones.
  {
    for (const [name, serverState] of [["returning, no stored state", makeServer({ model: true, bots: 1 })], ["completed", makeServer({ onboarding: { step: 3, completed: true, skipped: false } })]]) {
      const { page, context, errors } = await open(serverState);
      await settle(page);
      assert.equal(await page.locator("[data-onboarding]").count(), 0, name);
      assert.equal(await page.locator("[data-finish-setup]").count(), 0, name);
      assert.deepEqual(errors, []);
      await context.close();
    }
    const { page, context } = await open(makeServer({ model: true, bots: 1 }), { url: "/b/bot_1" });
    await page.locator(".composer textarea").waitFor();
    await settle(page);
    assert.equal(await page.locator("[data-onboarding]").count(), 0);
    assert.equal(await page.locator("[data-model-banner]").count(), 0);
    await context.close();
    pass("returning and completed accounts see no sheet, chip or banner");
  }

  // Reduced motion: no transitions or animations; the bubbles and the waiting dot are simply there.
  {
    const state = makeServer();
    const { page, context } = await open(state, { reduced: true });
    await sheet(page).waitFor();
    await page.locator(".onb-bubble").first().waitFor();
    const motion = await page.evaluate(() => {
      const names = selector => [...document.querySelectorAll(selector)].map(node => getComputedStyle(node).animationName);
      return { step: names(".onb-step"), bubble: names(".onb-bubble"), scrim: names(".onb-scrim"), opacity: [...document.querySelectorAll(".onb-bubble")].map(node => getComputedStyle(node).opacity) };
    });
    assert.deepEqual([...new Set([...motion.step, ...motion.bubble, ...motion.scrim])], ["none"], "no animation runs");
    assert.deepEqual([...new Set(motion.opacity)], ["1"], "bubbles show at once");
    await page.getByRole("button", { name: /set up your crew/ }).click();
    await page.getByRole("button", { name: "Continue with ChatGPT" }).click();
    await page.locator(".onb-waiting i").waitFor();
    assert.equal(await page.locator(".onb-waiting i").evaluate(node => getComputedStyle(node).animationName), "none", "the waiting dot does not breathe");
    await context.close();
    pass("reduced motion: nothing animates, bubbles and dot show statically");
  }

  // German: the longest labels wrap instead of being cut, on desktop and on a phone.
  for (const [width, height] of [[1440, 900], [390, 844]]) {
    await walkthrough({ width, height, theme: "light", locale: "de-DE", shotsOn: false, strict: true });
    pass(`German at ${width}px: no truncation on any step`);
  }
} finally {
  await browser.close();
  await server.close();
}
console.log(`\n${results.length} checks passed`);
