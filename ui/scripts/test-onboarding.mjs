// First-run onboarding, rendered: welcome, model (ChatGPT sign-in and API key), services queue, the
// skipped state (sidebar chip and composer banner), returning accounts, reduced motion, phones and German.
// Synthetic data only; the app's /api is answered in the browser by a small stateful stub.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:onboarding
// Screenshots (1440x900 and 390x844, light and dark) go to artifacts/onboarding/ (gitignored).
import {pickSelect, selectItems} from "./lib/select.mjs";
import { auditFocus, focusRingProbe } from "./focus-audit.mjs";
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", process.env.SHOTS_DIR || "onboarding");
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
    if (path === "/api/config") { state.configReads = (state.configReads ?? 0) + 1; return json({ model_configured: state.model, default_model: state.model ? state.modelSettings.model : "", provider: "synthetic", ...(state.capabilities ? { capabilities: state.capabilities } : {}) }); }
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
    if (path === "/api/auth/codex/connect" && state.codexConnectFails) return json({ error: { code: "codex_connect", message: "dial tcp: lookup auth.openai.com: no such host" } }, 503);
    if (path === "/api/auth/codex/connect") { state.popups++; return json({ session_id: "sess_1", verification_url: "https://auth.example.test/device", user_code: CODE, expires_at: Date.now() + 120000, interval: 1 }); }
    if (path === "/api/auth/codex/connect/sess_1/poll") { if (state.codexConnected) { state.model = true; return json({ connected: true, pending: false }); } return json({ connected: false, pending: true }); }
    if (path === "/api/auth/codex") return json({ connected: state.codexConnected });
    if (path === "/api/providers/compatible/models") {
      if (state.serverBlocked) return json({ error: { code: "endpoint_not_allowed", message: "blocked" } }, 400);
      state.calls.push(["probe", body()]);
      return json({ models: [{ id: "qwen3:8b", context_window: 16384 }, { id: "llama3.1:latest" }] });
    }
    if (path === "/api/providers/compatible/config" && method === "PUT") { state.calls.push(["config", body()]); state.model = true; state.catalog = [{ id: "compat:qwen3:8b", name: "qwen3:8b", provider: "compatible", reasoning_efforts: [], default_reasoning: "" }]; return json({ id: "compatible", label: "OpenAI-compatible", kind: "endpoint", configured: true }); }
    if (path === "/api/providers" && method === "GET") return json({ providers: [] });
    if (path.startsWith("/api/providers/") && path.endsWith("/key")) {
      state.keyTries++;
      await new Promise(resolve => setTimeout(resolve, state.keyDelay ?? 500));
      if (state.keyTries <= state.keyFailures) return json({ error: { code: "invalid_key", message: "" } }, 400);
      // Mirrors the server: a valid key that lists no models or has no usage left is refused by name and not stored.
      if (state.keyCode) return json({ error: { code: state.keyCode, message: "" } }, state.keyCode === "no_quota" ? 402 : 400);
      if (Array.isArray(state.catalog) && !state.catalog.length) return json({ error: { code: "no_models", message: "" } }, 400);
      state.model = true;
      return json({ id: "openai", label: "OpenAI", kind: "api_key", configured: true, key_hint: "…0000" });
    }
    if (path === "/api/models") return json({ models: state.model ? (state.catalog ?? modelsByProvider.three) : [], source: "live" });
    if (path === "/api/model-settings" && method === "GET") return json(state.modelSettings);
    if (path === "/api/model-settings" && method === "PUT") { state.modelSettings = body(); return json(state.modelSettings); }
    if (path === "/api/extensions/oauth-options") return json({ vm_available: false, web_callback_origin: "", desktop_redirect_uri: "" });
    if (path === "/api/extensions/mcp" && method === "GET") return json({ servers: state.mcp });
    if (path === "/api/extensions/mcp") { state.calls.push(method + " /api/extensions/mcp"); const server = body(); state.mcp = [...state.mcp.filter(item => item.name !== server.name), server]; return json({}); }
    if (path.endsWith("/test") && path.startsWith("/api/extensions/mcp/")) { state.calls.push("TEST " + path); return json(state.testResult ?? { ok: true, tool_count: 7 }); }
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools$/.test(path)) return json({ tools: [], read_only: 0, write: 0, high_risk: 0, pending: 0, unclassified: 0 });
    if (path === "/api/onboarding/handovers") { state.calls.push(`${method} ${path}${new URL(request.url()).search}`); return json({ handovers: state.handovers ?? [] }); }
    if (path.endsWith("/messages") && method === "GET") return json({ messages: state.messages ?? [], drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [] });
    if (path.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  return external;
}

async function open(state, { width = 1440, height = 900, theme = "light", reduced = false, locale = "en-US", url = "/", latency = false } = {}) {
  const context = await browser.newContext({ hasTouch: width < 700, viewport: { width, height }, locale, reducedMotion: reduced ? "reduce" : "no-preference", colorScheme: theme, permissions: ["clipboard-read", "clipboard-write"] });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const external = await mockApi(page, state);
  if (latency) await page.route("**/api/**", async route => { await new Promise(resolve => setTimeout(resolve, 200 + Math.random() * 700)); return route.fallback(); });
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
    for (const node of document.querySelectorAll(".onb-sheet button, .onb-sheet li, .onb-sheet strong, .onb-sheet small, .onb-sheet h2, .onb-sheet p, .onb-sheet label, .onb-sheet span, .onb-sheet code, .onb-sheet a")) {
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
    assert.ok((await page.locator(".onb-count").innerText()).startsWith(locale.startsWith("de") ? "Schritt 2 von 3" : "Step 2 of 3"));
    assert.ok((await overflow(page)) <= 0);
    if (strict) await assertNoTruncation(page, "model default");
    await shoot(page, "2-model-default");

    const choices = page.locator(".onb-choice");
    await choices.nth(1).click();
    assert.equal(await choices.nth(1).getAttribute("aria-checked"), "true", "the API key radio can be clicked");
    await choices.first().click();
    assert.equal(await choices.first().getAttribute("aria-checked"), "true", "the ChatGPT radio can be clicked");
    // Radix moves roving focus on a timer after keydown and selects only while the arrow is still down, as it is for a
    // person; Playwright's press() releases the key in the same turn, so hold it for one timer turn.
    const arrow = async key => { await page.keyboard.down(key); await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 0))); await page.keyboard.up(key); };
    await choices.first().focus();
    await arrow("ArrowRight");
    await page.waitForFunction(() => document.querySelectorAll(".onb-choice")[1]?.getAttribute("aria-checked") === "true");
    assert.equal(await choices.nth(1).evaluate(el => el === document.activeElement), true, "Radix moves focus and selection together");
    await arrow("ArrowLeft");
    await page.waitForFunction(() => document.querySelector(".onb-choice")?.getAttribute("aria-checked") === "true");
    assert.equal(await choices.evaluateAll(items => items.filter(el => el.tabIndex === 0).length), 1, "one radio tab stop");

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
    await page.locator(".onb-foot button[form=\"onb-key-form\"]").click();
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
    await page.locator(".onb-foot button[form=\"onb-key-form\"]").click();
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
    assert.equal(await page.locator(".onb-tile").count(), 7);
    const names = await page.locator(".onb-tile strong").allInnerTexts();
    assert.ok(!names.includes("Slack") && !names.includes("Discord"), "no tile without a working connect path");
    assert.ok(names.includes("Robinhood"));
    const primary = page.locator(".onb-foot .onb-primary");
    assert.equal(await primary.isDisabled(), true, "nothing selected: the primary waits");
    assert.doesNotMatch(await primary.innerText(), /\d/, "never a count of 0");
    assert.match(await primary.innerText(), /Select a service|Dienst auswählen/);
    // Every named service wears its own real brand mark, not a shared glyph.
    const marks = await page.locator(".onb-tile").evaluateAll(tiles => tiles.map(tile => `${tile.dataset.integrationId}:${tile.querySelector(".service-mark")?.dataset.mark}:${tile.querySelector(".service-mark img")?.getAttribute("src")}`));
    assert.deepEqual(marks, ["github:github", "notion:notion", "linear:linear", "home-assistant:homeassistant", "robinhood:robinhood", "context7:context7", "microsoft-learn:microsoft-learn"].map(item => { const key = item.split(":")[1]; return `${item.split(":")[0]}:${key}:/brand/services/${{ github: "github-monochrome.svg", notion: "notion.svg", linear: "linear.svg", robinhood: "robinhood.svg", homeassistant: "homeassistant.svg", context7: "context7.svg", "microsoft-learn": "microsoft-learn.svg" }[key]}`; }));
    await page.locator("[data-integration-id='github']").click();
    await page.locator("[data-integration-id='linear']").click();
    assert.match(await primary.innerText(), /2/);
    assert.equal(await primary.isEnabled(), true);
    if (strict) await assertNoTruncation(page, "services pick");
    await shoot(page, "7-services-pick");
    await primary.click();
    await page.locator(".onb-queue").waitFor();
    assert.deepEqual(await page.locator(".onb-queue li").evaluateAll(items => items.map(item => item.dataset.state)), ["now", "next"]);
    assert.match(await page.locator(".onb-queue li").first().innerText(), /GitHub/, "chips name the service");
    assert.match(await page.locator(".onb-queue li").nth(1).innerText(), /Linear/);
    assert.match(await page.locator(".onb-title").innerText(), /GitHub/, "the title follows the service");
    assert.doesNotMatch(await page.locator(".onb-title").innerText(), /Pick a few|Ein paar/);
    await page.locator(".onb-count", { hasText: /GitHub/ }).waitFor();
    assert.match(await page.locator(".onb-count").innerText(), /GitHub \(1 (of|von) 2\)/);
    assert.equal(await page.locator(".onb-foot .onb-primary").count(), 1, "one primary action");
    assert.equal(await page.locator(".onb-service-form button.primary-button").count(), 0, "no second Add button in the form");
    assert.equal(await page.locator(".onb-service-form a.onb-link[href='https://github.com/settings/personal-access-tokens/new']").count(), 1, "token page link");
    assert.equal(await page.locator(".onb-service-form").innerText().then(text => /\bPAT\b|Bearer|toolset/.test(text)), false, "no jargon in the main view");
    assert.equal(await page.locator(".onb-foot .onb-primary").isDisabled(), true, "Connect waits for the token");
    await page.locator(".onb-service-form input[type=password]").fill("synthetic-token");
    if (strict) await assertNoTruncation(page, "services walk");
    await shoot(page, "8-services-walk");
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-service-done").waitFor();
    await shoot(page, "9-services-done");
    await page.locator(".onb-foot .onb-primary").click();
    assert.deepEqual(await page.locator(".onb-queue li").evaluateAll(items => items.map(item => item.dataset.state)), ["done", "now"]);
    await page.locator(".onb-foot .onb-quiet").click();
    // The walk ends on a done state, never back on the pick screen.
    await page.locator(".onb-summary").waitFor();
    assert.equal(await page.locator(".onb-tiles").count(), 0, "not back on the pick screen");
    assert.deepEqual(await page.locator(".onb-summary li").evaluateAll(items => items.map(item => item.dataset.serviceResult)), ["done", "skipped"]);
    assert.equal(await page.locator(".onb-foot .onb-primary").isEnabled(), true);
    if (strict) await assertNoTruncation(page, "services summary");
    await shoot(page, "10-services-all-set");
    await page.locator(".onb-foot .onb-primary").click();

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
  for (const [width, height] of [[1440, 900], [1180, 820], [1024, 768], [390, 844]]) for (const theme of ["light", "dark"]) {
    await walkthrough({ width, height, theme, strict: width > 700 && width < 1400 });
    pass(`walkthrough ${width}x${height} ${theme}: welcome, model, key error and success, services, DM focus`);
  }

  // Focus rule: the sheet auto-focuses its first control on every step; a click or tap never rings it, Tab does.
  for (const [width, height] of [[1440, 900], [1180, 820], [1024, 768], [390, 844]]) for (const theme of ["light", "dark"]) {
    const touch = width < 700;
    const { page, context } = await open(makeServer(), { width, height, theme });
    try {
      await sheet(page).waitFor();
      await page.locator(".onb-welcome h2").waitFor();
      await page.waitForTimeout(400);
      const welcome = await page.evaluate(focusRingProbe);
      assert.ok(welcome.none || welcome.field || !welcome.ring, `welcome step rings ${welcome.focused}: ${welcome.ring?.ring}`);
      const next = page.getByRole("button", { name: /Let.s set up your crew/ });
      if (touch) await next.tap(); else await next.click();
      await page.getByRole("radio").first().waitFor();
      await page.waitForTimeout(400);
      const model = await page.evaluate(focusRingProbe);
      assert.ok(model.none || model.field || !model.ring, `model step rings ${model.focused}: ${model.ring?.ring}`);
      const found = await auditFocus(page, { touch, scope: "onboarding" });
      assert.deepEqual(found, [], JSON.stringify(found));
    } finally { await context.close(); }
    pass(`focus rings are keyboard-only in onboarding ${width}x${height} ${theme}`);
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
    // The row is frosted glass: fading the row itself fades its blur and the messages behind show through sharp (owner, 11-09).
    assert.equal(await page.locator(".composer-row").evaluate(node => getComputedStyle(node).opacity), "1", "the frosted row itself is not faded");
    assert.ok(await page.locator(".composer-row textarea").evaluate(node => { let o = 1; for (let n = node; n && !n.classList.contains("composer-row"); n = n.parentElement) o *= Number(getComputedStyle(n).opacity); return o; }) < 1, "its controls are dimmed");
    assert.equal(await page.getByText("No model set up").count(), 0, "the bare line is gone");
    await page.waitForTimeout(600);
    // The banner shows the Bot's own cat; the perched cat waits until the banner goes, instead of sitting above it on the messages.
    assert.equal(await page.locator(".composer-perch").isVisible(), false, "1440 light: no perched cat while the banner is up");
    assert.ok(await page.locator("[data-model-banner] .model-banner-cat").isVisible(), "the banner carries the Bot's cat");
    currentShot = "1440x900-light";
    await shoot(page, "11-after-skip-banner-chip");
    currentShot = null;
    // The banner's Connect and the chip both reopen setup; a stored skip resumes at step 2.
    await page.locator("[data-model-banner] button").click();
    await page.locator(".onb-choices").waitFor();
    assert.match(await page.locator(".onb-count").innerText(), /^Step 2 of 3/);
    assert.deepEqual(errors, []);
    await context.close();
    for (const [w, h, theme] of [[1440, 900, "dark"], [390, 844, "light"], [390, 844, "dark"]]) {
      const view = await open(makeServer({ bots: 1, onboarding: { step: 1, completed: false, skipped: true } }), { width: w, height: h, theme, url: "/b/bot_1" });
      await view.page.locator("[data-model-banner]").waitFor();
      await view.page.waitForTimeout(600);
      assert.equal(await view.page.locator(".composer-perch").isVisible(), false, `${w} ${theme}: no perched cat while the banner is up`);
      assert.equal(await view.page.locator(".composer-row").evaluate(node => getComputedStyle(node).opacity), "1", `${w} ${theme}: the frosted row is not faded`);
      await view.context.close();
    }
    const second = await open(makeServer({ onboarding: { step: 1, completed: false, skipped: true } }));
    await second.page.locator("[data-finish-setup]").click();
    await second.page.locator(".onb-choices").waitFor();
    assert.match(await second.page.locator(".onb-count").innerText(), /^Step 2 of 3/, "Finish setup resumes at step 2");
    await second.context.close();
    pass("welcome -> skip: Finish setup chip (2 of 3), composer banner, disabled composer, resume at step 2");
  }

  // "Not now" on the model step stores the skip too, and a reload resumes where the person was.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context } = await open(state);
    await page.locator(".onb-choices").waitFor();
    assert.match(await page.locator(".onb-count").innerText(), /^Step 2 of 3 · required/);
    await page.getByRole("button", { name: "Not now" }).click();
    await page.locator("[data-finish-setup]").waitFor();
    assert.equal(state.onboarding.skipped, true);
    await page.reload();
    await page.locator("[data-finish-setup]").waitFor();
    assert.equal(await sheet(page).count(), 0, "a skipped setup does not reopen by itself");
    await context.close();
    const fresh = await open(makeServer({ onboarding: { step: 2, completed: false, skipped: false } }));
    await fresh.page.locator(".onb-choices").waitFor();
    assert.match(await fresh.page.locator(".onb-count").innerText(), /^Step 2 of 3/, "an unfinished setup resumes on its step on any device");
    await fresh.context.close();
    pass("model step: Not now stores the skip; progress resumes after a reload");
  }

  // Closing the sheet early on an account with no Bot lands on the first-run pane, never on "Can't open this conversation", even when
  // the tab holds an address from an earlier install (a Bot link, a group link, a typo) and the API is slow. The error card sits
  // under the sheet from the first paint, so closing is what used to reveal it.
  for (const start of ["/b/bot_old", "/g/conv_old", "/nope"]) for (const [step, close] of [[0, "skip"], [2, "skip"]]) {
    const state = makeServer({ onboarding: { step, completed: false, skipped: false } });
    const { page, context } = await open(state, { theme: "dark", url: start, latency: true });
    await sheet(page).waitFor();
    // Escape and the scrim never close the sheet; only its own buttons do, and the stale address is already gone under it.
    await page.keyboard.press("Escape");
    await page.locator("[data-onboarding='true']").click({ position: { x: 4, y: 4 } });
    assert.equal(await sheet(page).count(), 1, `${start} step ${step}: Escape and the scrim leave the sheet open`);
    await page.getByRole("button", { name: step === 0 ? "Skip for now" : "Not now" }).click();
    await page.locator("[data-finish-setup]").waitFor();
    await page.locator("[data-first-run='pane']").waitFor();
    assert.equal(await page.locator("[role=alert]").count(), 0, `${start} step ${step} ${close}: no unavailable-conversation card`);
    assert.equal(new URL(page.url()).pathname, "/", `${start} step ${step} ${close}: the address is the first-run page`);
    await context.close();
  }
  pass("closing the sheet early with a stale address lands on the first-run pane (Skip for now / Not now, steps 1-2, slow API)");

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
    assert.match(await page.locator(".onb-count").innerText(), /Step 2 of 3 · done/);
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
    await pickSelect(page.locator("#model-default-select"), "synthetic-b");
    await page.locator("#model-effort-select").waitFor();
    assert.equal(await page.locator(".onb-advanced .onb-seg").count(), 0, "five efforts fall back to a select");
    assert.equal((await selectItems(page.locator("#model-effort-select"))).filter(item => item.value !== "").length, 5);
    assert.deepEqual(state.modelSettings, { model: "synthetic-b", reasoning_effort: "medium" }, "the choice is saved");
    await pickSelect(page.locator("#model-default-select"), "synthetic-c");
    await page.waitForFunction(() => !document.querySelector("#model-effort-select") && !document.querySelector(".onb-advanced .onb-seg"));
    assert.equal(await page.locator(".onb-chips").innerText().then(text => /Thinking/.test(text)), false, "a model without efforts shows no thinking chip");
    await context.close();
    pass("Advanced: thinking options derive from the selected model (segmented, select, none)");
  }

  // Home Assistant tile: address, long-lived token, a LAN reason in words, then a retry that edits the saved connection.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context, errors } = await open(state);
    await page.locator(".onb-connected").waitFor();
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-tiles").waitFor();
    const tile = page.locator("[data-integration-id='home-assistant']");
    assert.equal(await tile.locator("img[src='/brand/services/homeassistant.svg']").count(), 1, "official mark on the tile");
    await tile.click();
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-service-form").waitFor();
    assert.match(await page.locator(".onb-title").innerText(), /Home Assistant \(MCP\)/);
    const address = page.locator(".onb-service-form input[type=url]");
    assert.equal(await address.inputValue(), "http://homeassistant.local:8123/api/mcp", "the usual address, editable");
    assert.match(await page.locator(".onb-service-form").innerText(), /\.local names often do not resolve inside the container/);
    assert.equal(await page.locator(".onb-foot .onb-primary").isDisabled(), true, "Connect waits for the token");
    await address.fill("http://192.168.1.20:8123/api/mcp");
    await page.locator(".onb-service-form input[type=password]").fill("synthetic-ha-token");
    assert.equal(await page.locator(".onb-service-form a.onb-link[href='http://192.168.1.20:8123/profile/security']").count(), 1, "the token page follows the address");
    state.testResult = { ok: false, tool_count: 0, diagnostics: [{ code: "lan_connection_refused", message: "English text the page must not show" }] };
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-error").waitFor();
    assert.match(await page.locator(".onb-error").innerText(), /refused the connection/);
    assert.doesNotMatch(await page.locator(".onb-error").innerText(), /must not show/);
    state.testResult = null;
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-service-done").waitFor();
    assert.deepEqual([...new Set(state.calls.filter(call => /^(POST|PUT) \/api\/extensions\/mcp$/.test(call)))], ["POST /api/extensions/mcp", "PUT /api/extensions/mcp"], "the retry edits the saved connection, it does not add a second one");
    assert.equal(state.mcp.length, 1);
    assert.equal(state.mcp[0].name, "home-assistant");
    assert.equal(state.mcp[0].url, "http://192.168.1.20:8123/api/mcp");
    assert.equal(state.mcp[0].headers.Authorization, "Bearer synthetic-ha-token");
    assert.equal(state.mcp[0].oauth, undefined);
    assert.deepEqual(errors, []);
    await context.close();
    pass("Home Assistant: official mark, editable address with the .local caveat, token link, LAN reason in words, retry edits the saved connection");
  }

  // A key that works but has no models, and an unreachable provider, each give one sentence and a fix.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    state.catalog = [];
    const { page, context } = await open(state);
    await page.locator(".onb-choice").nth(1).click();
    await page.locator("#onb-key").fill(SYNTHETIC_KEY);
    await page.locator(".onb-foot button[form=\"onb-key-form\"]").click();
    await page.locator("#onb-key-error").waitFor();
    assert.match(await page.locator("#onb-key-error").innerText(), /can.t use any models yet/);
    assert.equal(await page.locator("#onb-key-error a").count(), 1, "Open billing is offered");
    await context.close();
    pass("key without model access: one sentence and an Open billing link");
  }

  // An account with no usage left is named as such, with the billing link.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    state.keyCode = "no_quota";
    const { page, context } = await open(state);
    await page.locator(".onb-choice").nth(1).click();
    await page.locator("#onb-key").fill(SYNTHETIC_KEY);
    await page.locator(".onb-foot button[form=\"onb-key-form\"]").click();
    await page.locator("#onb-key-error").waitFor();
    assert.match(await page.locator("#onb-key-error").innerText(), /no usage left/);
    assert.equal(await page.locator("#onb-key-error a").count(), 1, "Open billing is offered");
    await context.close();
    pass("key without usage: named as such, with an Open billing link");
  }

  // The Anthropic path explains the required Workspace ID, and Enter in either field submits.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context } = await open(state);
    await page.locator(".onb-choice").nth(1).click();
    await page.locator(".onb-seg button").nth(1).click();
    await page.locator("#onb-workspace-note").waitFor();
    assert.match(await page.locator("#onb-workspace-note").innerText(), /wrkspc_/);
    assert.equal(await page.locator(".onb-keyrow button").count(), 0, "one primary action lives in the footer");
    assert.equal(await page.locator(".onb-foot button[form=\"onb-key-form\"]").isDisabled(), true);
    await page.locator("#onb-key").fill(SYNTHETIC_KEY);
    await page.locator("#onb-workspace").fill("wrkspc_synthetic");
    assert.equal(await page.locator(".onb-foot button[form=\"onb-key-form\"]").isDisabled(), false);
    await page.locator("#onb-workspace").press("Enter");
    await page.locator(".onb-connected").waitFor();
    await context.close();
    pass("Anthropic path: Workspace ID explained, single footer action, Enter submits");
  }

  // Ollama or any server of your own: preset address, test the connection, pick a model, connect; no cloud key.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    const { page, context } = await open(state);
    await page.locator(".onb-choice[data-choice='server']").click();
    await page.locator("#onb-server-form").waitFor();
    assert.equal(await page.locator("#onb-server-form input[type=url]").inputValue(), "http://host.docker.internal:11434/v1", "the Ollama preset is filled in");
    assert.match(await page.locator("[data-compat-note='ollama']").innerText(), /OLLAMA_HOST=0\.0\.0\.0/);
    await page.getByRole("button", { name: "Test connection" }).click();
    await page.getByText("Models found: 2").waitFor();
    await page.locator(".onb-foot button[form=\"onb-server-form\"]").click();
    await page.locator(".onb-connected").waitFor();
    assert.deepEqual(state.calls.find(call => call[0] === "config")[1], { base_url: "http://host.docker.internal:11434/v1", models: [] }, "no key is sent when none is typed");
    assert.deepEqual(state.modelSettings, { model: "compat:qwen3:8b", reasoning_effort: "" }, "the picked model becomes the default");
    await context.close();
    pass("own server: Ollama preset, test, pick a model, connect without a key");
  }

  // A private address on a multi-account install is refused by name, with the admin setting that allows it.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    state.serverBlocked = true;
    const { page, context } = await open(state);
    await page.locator(".onb-choice[data-choice='server']").click();
    await page.getByRole("button", { name: "Test connection" }).click();
    await page.getByRole("alert").filter({ hasText: "TOFI_ALLOW_PRIVATE_ENDPOINTS=1" }).waitFor();
    await context.close();
    pass("own server: a blocked private address names TOFI_ALLOW_PRIVATE_ENDPOINTS");
  }

  // The server failing to reach OpenAI is not the person cancelling.
  {
    const state = makeServer({ onboarding: { step: 2, completed: false, skipped: false } });
    state.codexConnectFails = true;
    const { page, context } = await open(state);
    await page.locator(".onb-foot .primary-button").click();
    await page.getByText(/can.t reach OpenAI/).waitFor();
    assert.equal(await page.getByText(/cancelled or timed out/).count(), 0);
    await context.close();
    pass("ChatGPT sign-in: an unreachable OpenAI is not reported as cancelled");
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

  // The walk ends on a done state with Continue, which opens the Bot's DM (no way to get stuck).
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 3, completed: false, skipped: false } });
    const { page, context, errors } = await open(state);
    await page.locator(".onb-tiles").waitFor();
    await page.locator("[data-integration-id='microsoft-learn']").click();
    assert.match(await page.locator(".onb-foot .onb-primary").innerText(), /Connect 1/);
    await page.locator(".onb-foot .onb-primary").click();
    assert.match(await page.locator(".onb-foot .onb-primary").innerText(), /^Connect$/, "a no-sign-in service just says Connect");
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-service-done").waitFor();
    assert.match(await page.locator(".onb-foot .onb-primary").innerText(), /Finish/, "the last service finishes");
    await page.locator(".onb-foot .onb-primary").click();
    await page.locator(".onb-summary").waitFor();
    assert.match(await page.locator(".onb-title").innerText(), /all set/);
    assert.match(await page.locator(".onb-summary li").first().innerText(), /Microsoft Learn[\s\S]*Connected/);
    await page.locator(".onb-count", { hasText: /^Step 3 of 3 · done/ }).waitFor();
    const cont = page.locator(".onb-foot .onb-primary");
    assert.equal(await cont.isEnabled(), true);
    assert.match(await cont.innerText(), /^Continue$/);
    await cont.click();
    await sheet(page).waitFor({ state: "detached" });
    await page.locator(".composer textarea").waitFor();
    assert.equal(state.onboarding.completed, true);
    assert.deepEqual(errors, []);
    await context.close();
    pass("walk -> done state with enabled Continue -> the Bot's DM");
  }

  // Reopening the pick step with a service already connected: a clear Connected state and an enabled Continue.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 3, completed: false, skipped: false } });
    state.mcp = [{ name: "microsoft-learn", url: "https://learn.microsoft.com/api/mcp" }];
    const { page, context } = await open(state, { width: 1024, height: 768 });
    await page.locator(".onb-tiles").waitFor();
    const tile = page.locator("[data-integration-id='microsoft-learn']");
    await tile.locator(".status-badge").waitFor();
    assert.match(await tile.innerText(), /Connected/);
    assert.equal(await page.locator("button[data-integration-id='microsoft-learn']").count(), 0, "a connected service is not selectable");
    assert.equal(await tile.evaluate(node => getComputedStyle(node).opacity), "1", "normal contrast, not greyed out");
    const primary = page.locator(".onb-foot .onb-primary");
    assert.equal(await primary.isEnabled(), true);
    assert.match(await primary.innerText(), /^Continue$/);
    await page.locator("[data-integration-id='github']").click();
    assert.match(await primary.innerText(), /Connect 1/);
    await page.locator("[data-integration-id='github']").click();
    assert.match(await primary.innerText(), /^Continue$/);
    await primary.click();
    await sheet(page).waitFor({ state: "detached" });
    assert.equal(state.onboarding.completed, true);
    await context.close();
    pass("pick step with a connected service: Connected badge, enabled Continue that finishes");
  }

  // First-run handovers: the pending Bot's first direct message offers up to four proven tasks instead of the day-one card.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 3, completed: true, skipped: false } });
    state.bots[0].onboarding_pending = true;
    state.handovers = [{ id: "computer_script" }, { id: "reminder_in_minutes" }, { id: "unit_conversion_table" }, { id: "project_still_maintained" }, { id: "id_without_wording" }];
    state.messages = ["Hi, welcome to TOFI.", "I'm your own Bot.", "First, what should I be called?"].map((content, index) => ({ id: `m${index + 1}`, conversation_id: "conv_1", seq: index + 1, role: "assistant", sender_bot_id: "bot_1", content, created_at: at }));
    const { page, context, errors } = await open(state, { url: "/b/bot_1" });
    const chips = page.locator("[data-handover-chips]");
    await chips.waitFor({ timeout: 20000 });
    assert.equal(await page.locator(".handover-chip").count(), 4, "four chips; an id with no wording is left out");
    assert.equal(await page.locator("[data-day-one]").count(), 0, "the day-one card does not stack under the chips");
    assert.ok(state.calls.some(call => typeof call === "string" && call.startsWith("GET /api/onboarding/handovers?bot_id=bot_1")), "the list is read for this Bot");
    // Layout: a row of label-wide keys that wraps, left on the chat column with the heading and the bubbles; no card around it.
    // The block slides in (translate) when it appears: measure at rest, not mid-entry (a 2 px drift made the row check flaky).
    await chips.evaluate(node => Promise.all(node.getAnimations({ subtree: true }).map(animation => animation.finished)));
    const box = await chips.locator(".handover-chips-grid").boundingBox(), heading = await chips.locator("h2").boundingBox();
    const first = await page.locator(".handover-chip").nth(0).boundingBox(), second = await page.locator(".handover-chip").nth(1).boundingBox(), last = await page.locator(".handover-chip").nth(3).boundingBox();
    assert.ok(Math.abs(first.y - second.y) < 2 && box.width > 300, "the first two keys share a row");
    assert.ok(last.y > first.y + 10, "four English labels do not fit one row: the row wraps instead of squeezing them");
    assert.ok(first.width < box.width / 2 && Math.abs(first.x - heading.x) < 1, `keys take their label's width and start under the heading: ${JSON.stringify({ first, heading })}`);
    assert.equal(await chips.locator("p").count(), 0, "no footnote under the keys");
    const frame = await chips.evaluate(node => { const style = getComputedStyle(node); return { edge: style.borderTopWidth, shadow: style.boxShadow, image: style.backgroundImage }; });
    assert.deepEqual(frame, { edge: "0px", shadow: "none", image: "none" }, `no card around the keys: ${JSON.stringify(frame)}`);
    const heights = await page.locator(".handover-chip").evaluateAll(nodes => nodes.map(node => Math.round(node.getBoundingClientRect().height)));
    assert.ok(heights.every(height => height === heights[0] && height >= 40), `keys share one height and clear 40 px: ${heights.join(",")}`);
    const key = await page.locator(".handover-chip").first().evaluate(node => { const style = getComputedStyle(node); return { shadow: style.boxShadow, edge: style.borderTopWidth }; });
    assert.ok(key.shadow !== "none" && key.edge === "2px", `a key carries the ink edge and the hard shadow: ${JSON.stringify(key)}`);
    const sends = () => state.calls.filter(call => typeof call === "string" && /^POST .*\/messages$/.test(call)).length;
    const sentBefore = sends();
    await page.locator("[data-handover='reminder_in_minutes']").click();
    const composer = page.locator("form.composer textarea");
    await page.waitForFunction(() => document.querySelector("form.composer textarea")?.value === "Remind me in a few minutes to stand up and stretch.");
    assert.equal(sends(), sentBefore, "a click fills the composer and sends nothing");
    assert.equal(await composer.evaluate(node => document.activeElement === node), true, "the composer has focus");
    await composer.pressSequentially(" Use my timezone.");
    assert.ok((await composer.inputValue()).endsWith("stretch. Use my timezone."), "the text stays editable");
    // Keyboard: Space on a focused key holds the pressed pose (data-pressed) until release, which fills the composer the
    // same way. (Enter activates on keydown, so its pose is one frame long: focus moves to the composer and releases it.)
    const unitKey = page.locator("[data-handover='unit_conversion_table']");
    await unitKey.focus();
    await page.keyboard.down("Space");
    assert.equal(await unitKey.evaluate(node => node.hasAttribute("data-pressed")), true, "Space held shows the pressed pose");
    await page.keyboard.up("Space");
    assert.equal(await unitKey.evaluate(node => node.hasAttribute("data-pressed")), false, "Space released lifts the key");
    await page.waitForFunction(() => document.querySelector("form.composer textarea")?.value === "Convert some cooking measurements into a small table. Ask me which ones.");
    assert.equal(sends(), sentBefore, "Space fills the composer and sends nothing");
    assert.ok((await overflow(page)) <= 0, "no horizontal scroll");
    assert.deepEqual(errors, []);
    await context.close();
    pass("first-run handovers: a row of keys replaces the day-one card, a click fills the composer without sending");
  }

  // Phone width: one column.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 3, completed: true, skipped: false } });
    state.bots[0].onboarding_pending = true;
    state.handovers = [{ id: "reminder_in_minutes" }, { id: "unit_conversion_table" }];
    state.messages = [{ id: "m1", conversation_id: "conv_1", seq: 1, role: "assistant", sender_bot_id: "bot_1", content: "Hi.", created_at: at }];
    const { page, context } = await open(state, { width: 390, height: 844, url: "/b/bot_1" });
    await page.locator("[data-handover-chips]").waitFor({ timeout: 20000 });
    const a = await page.locator(".handover-chip").nth(0).boundingBox(), b = await page.locator(".handover-chip").nth(1).boundingBox(), row = await page.locator(".handover-chips-grid").boundingBox();
    assert.ok(b.y > a.y + 10, "one column on a phone");
    assert.ok(Math.abs(a.width - (row.width - 3)) < 2 && a.height >= 44, `full-width keys of at least 44 px on a phone: ${JSON.stringify({ a, row })}`);
    assert.ok((await overflow(page)) <= 0, "no horizontal scroll on a phone");
    await context.close();
    pass("first-run handovers: one column on a phone");
  }

  // A Bot whose setup is finished gets the day-one card, never the chips.
  {
    const state = makeServer({ model: true, bots: 1, onboarding: { step: 3, completed: true, skipped: false } });
    state.handovers = [{ id: "reminder_in_minutes" }];
    const { page, context } = await open(state, { url: "/b/bot_1" });
    await page.locator("[data-day-one]").waitFor({ timeout: 20000 });
    assert.equal(await page.locator("[data-handover-chips]").count(), 0);
    await context.close();
    pass("first-run handovers: a finished Bot shows the day-one card, not the chips");
  }

  // Models provided by the organization (user groups): with one available the model step passes itself on and connects nothing;
  // without one the step waits for the administrator, with no key field, no ChatGPT sign-in and no dead button.
  {
    const capabilities = { ai_models: { mode: "provided", source: "organization" }, computer: { mode: "host", source: "own" }, web_search: { mode: "own", source: "own" }, web_fetch: { mode: "own", source: "own" } };
    const available = makeServer({ model: true });
    available.capabilities = capabilities;
    const first = await open(available);
    await sheet(first.page).waitFor();
    // An account that already has a model resumes at the model step, which passes itself on: no welcome, no choices.
    await first.page.locator(".onb-count", { hasText: "Step 3 of 3" }).waitFor();
    assert.equal(await first.page.locator(".onb-choice, #onb-key, .onb-keyform").count(), 0, "provided models: nothing to connect is ever shown");
    assert.ok(available.calls.includes("POST /api/onboarding/first-bot"), "the first Bot is still created when the model step passes itself on");
    assert.ok(!available.calls.some(call => typeof call === "string" && call.includes("/api/auth/codex")), "no ChatGPT sign-in is started or polled");
    assert.deepEqual(first.errors, []);
    await first.context.close();

    const waiting = makeServer({ model: false });
    waiting.capabilities = capabilities;
    const second = await open(waiting);
    await sheet(second.page).waitFor();
    await second.page.getByRole("button", { name: /set up your crew/ }).click();
    await second.page.locator(".onb-waiting-admin").waitFor();
    assert.equal(await second.page.locator(".onb-waiting-admin").getAttribute("data-state"), "org_resource_missing");
    assert.match(await sheet(second.page).innerText(), /Waiting for your administrator[\s\S]*has not provided a model yet/);
    assert.equal(await sheet(second.page).locator(".onb-choice, #onb-key, input, textarea").count(), 0, "no input while waiting for the administrator");
    assert.equal(await second.page.getByRole("button", { name: /ChatGPT|Connect/ }).count(), 0, "no sign-in or connect button");
    assert.ok((await overflow(second.page)) <= 0);
    currentShot = "organization-waiting"; await shoot(second.page, "model-step"); currentShot = null;
    const before = waiting.configReads;
    await second.page.getByRole("button", { name: "Check again" }).click();
    await second.page.waitForTimeout(400);
    assert.ok(waiting.configReads > before, "Check again reads the configuration again");
    assert.equal(await second.page.locator(".onb-waiting-admin").count(), 1, "still waiting while the administrator has added nothing");
    waiting.model = true; // the administrator adds a model
    await second.page.getByRole("button", { name: "Check again" }).click();
    await second.page.locator(".onb-count", { hasText: "Step 3 of 3" }).waitFor();
    assert.ok(waiting.calls.includes("POST /api/onboarding/first-bot"));
    await second.context.close();
    pass("organization-provided models: the model step passes itself on, or waits for the administrator without a key field or dead button");
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
  for (const [width, height] of [[1440, 900], [1024, 768], [390, 844]]) {
    await walkthrough({ width, height, theme: "light", locale: "de-DE", shotsOn: false, strict: true });
    pass(`German at ${width}px: no truncation on any step`);
  }
} finally {
  await browser.close();
  await server.close();
}
console.log(`\n${results.length} checks passed`);
