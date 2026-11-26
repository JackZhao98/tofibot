// Empty / first-run states: still calico art on the kit EmptyState (no live cats), fonts are self-hosted,
// nothing overflows at 390px. Synthetic data only; the app's /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:empty-states
import assert from "node:assert/strict";
import { viteCacheDir } from "./lib/vite-cache.mjs";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "empty-states");
mkdirSync(shots, { recursive: true });

const server = await createServer({ cacheDir: viteCacheDir(), configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const at = "2026-10-01T00:00:00Z";
const bot = { id: "bot_1", name: "Synthetic Bot", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "conv_1", created_at: at };
const dm = { id: "conv_1", kind: "dm", name: "Synthetic Bot", bot_id: "bot_1", bot_ids: ["bot_1"], updated_at: at, read_seq: 0, user_visible: true };

/** Answers the app's reads in the browser; `withBot` gives one Bot and an empty direct message. */
async function mockApi(page, { withBot = false } = {}) {
  const external = [];
  page.on("request", request => { const url = new URL(request.url()); if (url.origin !== origin && !url.protocol.startsWith("data")) external.push(request.url()); });
  await page.route("**/api/**", route => {
    const path = new URL(route.request().url()).pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots: withBot ? [bot] : [] });
    if (path === "/api/conversations") return json({ conversations: withBot ? [dm] : [] });
    if (path.endsWith("/messages")) return json({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
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

async function open(url, { width = 1440, height = 900, theme = "light", reduced = false, locale = "en-US", api } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale, reducedMotion: reduced ? "reduce" : "no-preference", colorScheme: theme });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const external = api ? await mockApi(page, api) : [];
  await page.goto(origin + url);
  return { page, context, errors, external };
}

const overflow = page => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
async function settle(page) { await page.evaluate(() => document.fonts.ready); await page.waitForTimeout(700); }
/** The state is on screen with its still art: an image, no SVG cat, nothing animated (owner Q3: only the header cat moves). */
async function stillState(page, name) {
  const box = page.locator(`[data-empty-state="${name}"]`).first();
  await box.waitFor({ state: "attached" });
  assert.equal(await box.locator("img").count() > 0, true, `${name}: calico art`);
  assert.equal(await page.locator("[data-live]").count(), 0, `${name}: no live cat`);
  assert.equal(await box.locator("svg").count(), 0, `${name}: no cat SVG`);
  return box;
}

const results = [];
const pass = name => { results.push(name); console.log("PASS", name); };

try {
  // 1. First run through the real App: no Bot, kit EmptyState, display-face title, still art.
  {
    const { page, context, errors, external } = await open("/", { api: {} });
    await page.locator("[data-first-run='pane'] .tb-empty-title").waitFor();
    await settle(page);
    assert.equal(await page.locator("[data-first-run='pane'] img").count(), 1, "one still calico");
    assert.equal(await page.locator("[data-live]").count(), 0, "no live cats");
    const title = await page.locator("[data-first-run='pane'] .tb-empty-title").evaluate(node => getComputedStyle(node).fontFamily);
    assert.match(title, /^"?Bricolage Grotesque/);
    assert.equal(await page.evaluate(() => document.fonts.check("700 18px 'Bricolage Grotesque'")), true, "Bricolage Grotesque 700 is loaded, not a system fallback");
    assert.deepEqual(external, [], "no request leaves the origin (fonts are self-hosted)");
    assert.equal(await page.locator("link[rel=preload][as=font]").count(), 1, "display font is preloaded");
    await stillState(page, "sidebar");
    assert.deepEqual(errors, []);
    await context.close();
    pass("first run: kit EmptyState, still art, self-hosted fonts, no external requests");
  }

  // 1b. On a phone the sidebar is the first screen: the hero is there.
  {
    const { page, context, errors } = await open("/", { width: 390, height: 844, api: {} });
    await page.locator("[data-first-run='side'] .tb-empty-title").waitFor({ state: "visible" });
    assert.equal(await page.locator("[data-first-run='pane']").isVisible().catch(() => false), false, "the pane hero is hidden on a phone");
    assert.ok((await overflow(page)) <= 0);
    assert.deepEqual(errors, []);
    await context.close();
    pass("first run on a 390px phone: sidebar hero");
  }

  // 2. Other App-level empty states.
  {
    const { page, context, errors } = await open("/b/missing", { api: { withBot: true } });
    await page.locator("[data-empty-cat='route-unavailable']").waitFor({ state: "attached" });
    await context.close();
    const second = await open("/b/bot_1", { api: { withBot: true } });
    await stillState(second.page, "conversation");
    assert.deepEqual(second.errors, []);
    assert.deepEqual(errors, []);
    await second.context.close();
    pass("app: unavailable route and empty conversation show still art");
  }

  // 3. Component empty states, each with still art.
  const states = [
    ["archive-empty", "archive-empty"], ["memory-empty", "memory-empty"], ["memory-no-match", null],
    ["usage-no-agents", "usage-no-agents"], ["work-empty", "work-empty"], ["mcp-empty", "mcp-empty"],
    ["integrations-no-match", "integrations-no-match"], ["skills-empty", "skills-empty"],
    ["credentials-env-empty", "credentials-env-empty"], ["terminal-empty", "terminal-empty"],
    ["view-only-empty", "view-only-empty"],
  ];
  for (const [state, name] of states) {
    const { page, context, errors } = await open(`/fixtures/empty-states.html?state=${state}`);
    if (state === "memory-no-match") { await page.locator(".memory-search input").fill("zzzz"); await stillState(page, "memory-no-match"); }
    else if (state === "terminal-empty") await page.locator("[data-empty-cat]").first().waitFor({ state: "attached" });
    else await stillState(page, name);
    assert.deepEqual(errors, [], state);
    await context.close();
  }
  pass(`${states.length} component empty states show still art`);

  // 4. No horizontal scroll at 390px, and screenshots (1440x900 and 390x844, light and dark).
  const pages = [
    ["first-run", "/", { api: {} }], ["sidebar-conversation", "/b/bot_1", { api: { withBot: true } }], ["route-unavailable", "/b/missing", { api: {} }],
    ...states.map(([state]) => [state, `/fixtures/empty-states.html?state=${state}`, {}]),
    ["first-run-zh-CN", "/fixtures/empty-states.html?state=first-run&lang=zh-CN", {}],
  ];
  for (const [name, url, extra] of pages) {
    for (const [label, width, height] of [["desktop", 1440, 900], ["mobile", 390, 844]]) {
      for (const theme of ["light", "dark"]) {
        const sep = url.includes("?") ? "&" : "?";
        const { page, context, errors } = await open(`${url}${url.startsWith("/fixtures") ? `${sep}theme=${theme}` : ""}`, { width, height, theme, ...extra });
        if (name === "memory-no-match") await page.locator(".memory-search input").fill("zzzz");
        await page.locator(".tb-empty:visible, [data-empty-cat]:visible").first().waitFor({ timeout: 8000 }).catch(error => { throw new Error(`${name} ${label} ${theme}: no visible empty state (${error.message.split("\n")[0]})`); });
        await page.locator(".tb-empty:visible, [data-empty-cat]:visible").first().waitFor();
        await settle(page);
        if (width === 390) assert.ok((await overflow(page)) <= 0, `${name} ${theme}: horizontal overflow ${await overflow(page)}px at 390px`);
        assert.deepEqual(errors, [], name);
        await page.screenshot({ path: join(shots, `${name}-${label}-${theme}.png`) });
        await context.close();
      }
    }
  }
  pass("no horizontal scroll at 390px; screenshots written to artifacts/empty-states");
  console.log(`PASS: ${results.length} groups`);
} finally {
  await browser.close();
  await server.close();
}
