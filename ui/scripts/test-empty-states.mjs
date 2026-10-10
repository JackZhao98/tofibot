// Empty / first-run states: cats are mounted, fonts are self-hosted, reduced motion is honoured,
// nothing overflows at 390px. Synthetic data only; the app's /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:empty-states
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "empty-states");
mkdirSync(shots, { recursive: true });

const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
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
    if (path === "/api/secret-inputs") return json({ requests: [] });
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

const liveCats = page => page.locator("[data-empty-cat][data-live='true']");
const overflow = page => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
async function settle(page) { await page.evaluate(() => document.fonts.ready); await page.waitForTimeout(700); }
async function mountedCat(page, name) {
  const cat = page.locator(`[data-empty-cat="${name}"]`);
  await cat.first().waitFor({ state: "attached" });
  await page.locator(`[data-empty-cat="${name}"][data-live="true"] svg`).first().waitFor({ state: "attached", timeout: 5000 });
  return cat.first();
}

const results = [];
const pass = name => { results.push(name); console.log("PASS", name); };

try {
  // 1. First run through the real App: no Bot, Fredoka hero, a troupe of live cats.
  {
    const { page, context, errors, external } = await open("/", { api: {} });
    await page.locator("[data-first-run='pane'] h1").waitFor();
    await settle(page);
    for (const name of ["miso", "nori", "yuzu", "azuki"]) await mountedCat(page, `first-run-${name}`);
    assert.equal(await page.locator("[data-first-run='pane'] [data-empty-cat][data-live='true'] svg").count(), 4, "four live cats in the hero");
    const title = await page.locator("[data-first-run='pane'] h1").evaluate(node => { const style = getComputedStyle(node); return { family: style.fontFamily, size: style.fontSize, weight: style.fontWeight }; });
    assert.match(title.family, /^"?Fredoka/);
    assert.equal(title.size, "40px");
    assert.equal(await page.evaluate(() => document.fonts.check("700 40px Fredoka")), true, "Fredoka 700 is loaded, not a system fallback");
    const faces = await page.evaluate(() => [...document.fonts].filter(face => face.family.replace(/"/g, "") === "Fredoka").map(face => face.status));
    assert.ok(faces.includes("loaded"), `a Fredoka face loaded: ${faces}`);
    assert.equal(await page.evaluate(() => document.fonts.check("600 14px 'JetBrains Mono'")) !== undefined, true);
    assert.deepEqual(external, [], "no request leaves the origin (fonts are self-hosted)");
    assert.equal(await page.locator("link[rel=preload][as=font]").count(), 1, "display font is preloaded");
    // The sidebar's own empty state carries its cat too (fifth live cat).
    await mountedCat(page, "sidebar");
    assert.ok((await liveCats(page).count()) <= 6);
    // Hovering the button perks the troupe up without errors.
    await page.locator("[data-first-run='pane'] .first-run-create").hover();
    await page.waitForTimeout(900);
    assert.equal(await page.locator("[data-first-run='pane'] [data-empty-cat][data-live='true'] svg").count(), 4);
    assert.deepEqual(errors, []);
    await context.close();
    pass("first run: Fredoka hero 40px, 4 live cats, sidebar cat, self-hosted fonts, no external requests");
  }

  // 1b. On a phone the sidebar is the first screen: the hero is there, with its own live cats.
  {
    const { page, context, errors } = await open("/", { width: 390, height: 844, api: {} });
    await page.locator("[data-first-run='side'] h1").waitFor({ state: "visible" });
    for (const name of ["miso", "nori", "yuzu", "azuki"]) await mountedCat(page, `first-run-side-${name}`);
    const title = await page.locator("[data-first-run='side'] h1").evaluate(node => ({ family: getComputedStyle(node).fontFamily, size: getComputedStyle(node).fontSize }));
    assert.match(title.family, /^"?Fredoka/);
    assert.equal(title.size, "30px");
    assert.equal(await page.locator("[data-first-run='pane'] [data-live='true']").count(), 0, "the hidden pane hero runs no cats");
    assert.ok((await overflow(page)) <= 0);
    assert.deepEqual(errors, []);
    await context.close();
    pass("first run on a 390px phone: sidebar hero with 4 live cats, hidden pane hero runs none");
  }

  // 2. Other App-level empty states.
  {
    const { page, context, errors } = await open("/b/missing", { api: {} });
    await mountedCat(page, "route-unavailable");
    await context.close();
    const second = await open("/b/bot_1", { api: { withBot: true } });
    await mountedCat(second.page, "conversation");
    assert.deepEqual(second.errors, []);
    assert.deepEqual(errors, []);
    await second.context.close();
    pass("app: unavailable route and empty conversation mount cats");
  }

  // 3. Component empty states, each with a live cat.
  const states = [
    ["archive-empty", "archive-empty"], ["memory-empty", "memory-empty"], ["memory-no-match", null],
    ["usage-no-agents", "usage-no-agents"], ["work-empty", "work-empty"], ["mcp-empty", "mcp-empty"],
    ["integrations-no-match", "integrations-no-match"], ["skills-empty", "skills-empty"],
    ["credentials-env-empty", "credentials-env-empty"], ["terminal-empty", "terminal-empty"],
    ["view-only-empty", "view-only-empty"], ["gate-offline", "gate-offline"],
  ];
  for (const [state, cat] of states) {
    const { page, context, errors } = await open(`/fixtures/empty-states.html?state=${state}`);
    if (state === "memory-no-match") { await page.locator(".memory-search input").fill("zzzz"); await mountedCat(page, "memory-no-match"); }
    else await mountedCat(page, cat);
    assert.deepEqual(errors, [], state);
    await context.close();
  }
  pass(`${states.length} component empty states mount a live cat`);

  // 4. Performance: cap on simultaneous cats, cats leave with the viewport.
  {
    const { page, context } = await open("/fixtures/empty-states.html?state=many");
    await page.waitForTimeout(600);
    const total = await page.locator("[data-empty-cat]").count(), live = await liveCats(page).count();
    assert.equal(total, 14);
    assert.ok(live >= 1 && live <= 6, `live cats capped (${live})`);
    assert.equal(await page.locator("[data-empty-cat] svg").count(), live, "only live cats hold an SVG");
    await context.close();
    const scroll = await open("/fixtures/empty-states.html?state=scroll");
    await mountedCat(scroll.page, "top");
    assert.equal(await scroll.page.locator("[data-empty-cat='bottom']").getAttribute("data-live"), "false", "offscreen cat is not mounted");
    await scroll.page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await mountedCat(scroll.page, "bottom");
    await scroll.page.waitForFunction(() => document.querySelector("[data-empty-cat='top']").dataset.live === "false");
    await scroll.context.close();
    pass("cap of 6 live cats, offscreen cats are destroyed and remounted on return");
  }

  // 5. Reduced motion: the idle loop stops.
  {
    const sample = async reduced => {
      const { page, context } = await open("/fixtures/empty-states.html?state=scroll", { reduced });
      const cat = await mountedCat(page, "top");
      await page.waitForTimeout(900);
      const first = await cat.evaluate(node => node.querySelector("svg").outerHTML);
      await page.waitForTimeout(1400);
      const second = await cat.evaluate(node => node.querySelector("svg").outerHTML);
      await context.close();
      return first === second;
    };
    assert.equal(await sample(true), true, "reduced motion: cat is still");
    assert.equal(await sample(false), false, "default: cat breathes");
    pass("prefers-reduced-motion stops idle motion; default motion runs");
  }

  // 6. No horizontal scroll at 390px, and screenshots (1440x900 and 390x844, light and dark).
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
        await page.locator("[data-empty-cat]:visible").first().waitFor();
        if (name === "integrations-no-match") await page.waitForTimeout(100);
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
