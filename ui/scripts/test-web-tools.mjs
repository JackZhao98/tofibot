// Rendered acceptance for Settings > Web search & fetch.
// Mounts the real WebToolsSettings in a Vite fixture with /api/web-tools stubbed as a small stateful server
// (synthetic data) and drives it with Playwright: both chains list their sources with brand marks, status and a
// paid marker; fixed sources (model search, built-in fetch) have no grip or switch; the order changes by keyboard,
// arrow buttons and pointer drag and is sent as one PUT; Test shows testing, then a stable code or "Working";
// a key is sealed (never shown again) and removable; a switch turns a source off; the Bot computer row exists only
// when a computer does; a failed save reverts and says so; all seven languages render without raw keys or overflow.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:web-tools
//   WEB_TOOLS_SHOTS=<dir>  also saves a screenshot there
import {pickSelect} from "./lib/select.mjs";
import assert from "node:assert/strict";
import {mkdir, mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const shots = process.env.WEB_TOOLS_SHOTS;
if (shots) await mkdir(shots, {recursive: true});
const fixture = await mkdtemp(join(ui, ".web-tools-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {WebToolsSettings} from '../src/WebToolsSettings';
const query=new URLSearchParams(location.search);
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<div className="workspace"><div className="settings-page-body" data-page="models"><WebToolsSettings/></div></div>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  // ---- the stub server
  const state = {};
  const requests = [];
  const reset = () => {
    Object.assign(state, {
      computer: false, masterOn: true, failNext: false, testDelay: 0,
      order: {search: ["tavily", "brave", "searxng", "computer"], fetch: ["firecrawl", "brightdata", "computer"]},
      off: new Set(), keys: {}, errors: {}, address: "", testResult: {}, limits: {search: 300, fetch: 300}, used: {},
    });
    requests.length = 0;
  };
  reset();
  const specs = {
    tavily: {label: "Tavily", kind: "api", paid: true, fields: ["key"]},
    brave: {label: "Brave Search", kind: "api", paid: true, fields: ["key"]},
    searxng: {label: "SearXNG", kind: "self_hosted", paid: false, fields: ["base_url"]},
    firecrawl: {label: "Firecrawl", kind: "api", paid: true, fields: ["key"]},
    brightdata: {label: "Bright Data", kind: "api", paid: true, fields: ["key", "zone"]},
  };
  const entry = (chain, id) => {
    const enabled = !state.off.has(`${chain}/${id}`);
    if (id === "computer") return {id, label: "Bot computer", kind: "computer", paid: false, fixed: false, enabled, status: !enabled ? "off" : "ready", testable: false};
    const sp = specs[id];
    const e = {id, label: sp.label, kind: sp.kind, paid: sp.paid, fixed: false, enabled, fields: sp.fields, testable: true, status: "needs_key"};
    if (sp.kind === "self_hosted") { e.base_url = state.address; e.status = state.address ? "ready" : "needs_address"; }
    else if (state.keys[id]) { e.status = state.errors[id] ? "error" : "ready"; e.key_hint = "…" + state.keys[id].slice(-4); if (state.errors[id]) e.error_code = state.errors[id]; }
    if (sp.paid) {
      if (state.used[id]) e.used_today = state.used[id];
      if (e.status === "ready" && state.limits[chain] >= 0 && (state.used[id] ?? 0) >= state.limits[chain]) e.status = "limit";
    }
    if (!enabled && (e.status === "ready" || e.status === "limit")) e.status = "off";
    return e;
  };
  const viewOf = () => ({
    enabled: state.masterOn, computer: state.computer, limits: state.limits,
    search: [{id: "native", label: "Model web search", kind: "native", paid: false, fixed: true, enabled: true, status: "always", testable: false}, ...state.order.search.filter(id => id !== "computer" || state.computer).map(id => entry("search", id))],
    fetch: [{id: "builtin", label: "Built-in fetch", kind: "builtin", paid: false, fixed: true, enabled: true, status: "always", testable: true}, ...state.order.fetch.filter(id => id !== "computer" || state.computer).map(id => entry("fetch", id))],
    search_offered: Object.keys(state.keys).some(id => id in specs && ["tavily", "brave"].includes(id) && !state.errors[id]) || Boolean(state.address),
  });
  const answer = (url, method, body) => {
    const path = url.pathname;
    if (path === "/api/web-tools" && method === "GET") return viewOf();
    let m;
    if (path === "/api/web-tools/limits" && method === "PUT") { Object.assign(state.limits, body); return viewOf(); }
    if ((m = path.match(/^\/api\/web-tools\/(search|fetch)\/order$/)) && method === "PUT") {
      if (state.failNext) { state.failNext = false; return [500, {error: {code: "storage", message: "could not save the order"}}]; }
      const known = body.order.filter(id => state.order[m[1]].includes(id));
      state.order[m[1]] = [...known, ...state.order[m[1]].filter(id => !known.includes(id))];
      return viewOf();
    }
    if ((m = path.match(/^\/api\/web-tools\/(search|fetch)\/(\w+)$/)) && method === "PUT") {
      if (body.enabled) state.off.delete(`${m[1]}/${m[2]}`); else state.off.add(`${m[1]}/${m[2]}`);
      return viewOf();
    }
    if ((m = path.match(/^\/api\/web-tools\/providers\/(\w+)\/test$/)) && method === "POST") {
      const id = m[1];
      const code = state.testResult[id] ?? "";
      if (code) state.errors[id] = code; else delete state.errors[id];
      return {...viewOf(), test: {id, ok: !code, code}};
    }
    if ((m = path.match(/^\/api\/web-tools\/providers\/(\w+)$/)) && method === "PUT") {
      const id = m[1];
      if (specs[id].kind === "self_hosted") state.address = body.base_url; else { state.keys[id] = body.key; delete state.errors[id]; }
      return viewOf();
    }
    if ((m = path.match(/^\/api\/web-tools\/providers\/(\w+)$/)) && method === "DELETE") {
      delete state.keys[m[1]]; delete state.errors[m[1]]; if (m[1] === "searxng") state.address = "";
      return viewOf();
    }
    return undefined;
  };
  async function open(params = {}, {width = 1100, height = 1400, theme = "light", locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: "reduce", colorScheme: theme, locale});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      let body = {};
      try { body = JSON.parse(request.postData() || "{}"); } catch {}
      if (request.method() !== "GET") requests.push(`${request.method()} ${url.pathname} ${request.postData() ?? ""}`.trim());
      if (request.method() === "POST" && state.testDelay) await new Promise(resolve => setTimeout(resolve, state.testDelay));
      const result = answer(url, request.method(), body);
      if (result === undefined) { errors.push(`unstubbed ${request.method()} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {code: "not_found", message: "not stubbed"}})}); return; }
      const [status, payload] = Array.isArray(result) ? result : [200, result];
      await route.fulfill({status, contentType: "application/json", body: JSON.stringify(payload)});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams(params)}`);
    await page.locator(".web-row").first().waitFor({timeout: 20000});
    return {page, errors, close: () => context.close()};
  }
  const ids = (page, chain) => page.locator(`.web-row[data-chain="${chain}"]`).evaluateAll(rows => rows.map(row => row.dataset.id));
  const row = (page, chain, id) => page.locator(`.web-row[data-chain="${chain}"][data-id="${id}"]`);

  // ---- 1. Both chains list their sources with marks, status and the paid marker.
  {
    reset();
    const t = await open();
    assert.deepEqual(await ids(t.page, "search"), ["native", "tavily", "brave", "searxng"], "search chain: the model's own search first, no computer row without a computer");
    assert.deepEqual(await ids(t.page, "fetch"), ["builtin", "firecrawl", "brightdata"]);
    for (const id of ["tavily", "brave", "firecrawl", "brightdata"]) {
      assert.equal(await row(t.page, id === "firecrawl" || id === "brightdata" ? "fetch" : "search", id).locator('[data-tag="paid"]').count(), 1, `${id} is marked paid`);
      assert.equal(await row(t.page, id === "firecrawl" || id === "brightdata" ? "fetch" : "search", id).locator('.service-mark-brand[data-mark="' + id + '"] img').count(), 1, `${id} wears its official brand mark`);
    }
    assert.equal(await row(t.page, "search", "searxng").locator('[data-tag="paid"]').count(), 0, "SearXNG is free");
    assert.equal(await row(t.page, "search", "searxng").locator('.service-mark-brand[data-mark="searxng"]').count(), 1);
    for (const [chain, id] of [["search", "native"], ["fetch", "builtin"]]) {
      const fixed = row(t.page, chain, id);
      assert.equal(await fixed.locator(".web-grip").count(), 0, `${id} cannot be dragged`);
      assert.equal(await fixed.getByRole("switch").count(), 0, `${id} cannot be switched off`);
      assert.match(await fixed.innerText(), /Always on/);
    }
    assert.match(await row(t.page, "search", "tavily").innerText(), /Needs a key/);
    assert.match(await row(t.page, "search", "searxng").innerText(), /Needs an address/);
    assert.equal(await row(t.page, "search", "tavily").getByRole("button", {name: "Test"}).isDisabled(), true, "Test is off until a source is set up");
    assert.equal(await row(t.page, "fetch", "builtin").getByRole("button", {name: "Test"}).isDisabled(), false, "the built-in fetch can always be tested");
    assert.ok(!/webtools\./.test(await t.page.locator(".web-tools").innerText()), "no raw i18n keys");
    assert.match(await t.page.locator(".settings-banner").innerText(), /No search source is set up/, "no usable search source says so instead of pretending");
    assert.equal(await t.page.locator(".web-row-status").first().getAttribute("role"), "status");
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    if (shots) await t.page.locator(".settings-page-body").screenshot({path: join(shots, "web-tools-light.png")});
    await t.close();
    console.log("PASS both chains render sources, brand marks, status and paid markers; fixed sources are fixed");
  }

  // ---- 2. Reordering: keyboard, arrow buttons, pointer drag. One PUT each; the list follows at once.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111", brave: "brave-bbbb2222"}; state.address = "http://searxng.local:8080";
    const t = await open();
    assert.equal(await t.page.locator(".settings-banner").count(), 0, "a ready source removes the banner");
    await row(t.page, "search", "tavily").locator(".web-grip").focus();
    await t.page.keyboard.press("ArrowDown");
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="tavily"]')?.previousElementSibling?.dataset.id === "brave");
    assert.deepEqual(requests, ['PUT /api/web-tools/search/order {"order":["brave","tavily","searxng"]}'], "one write with the whole order");
    assert.deepEqual(await ids(t.page, "search"), ["native", "brave", "tavily", "searxng"]);
    assert.equal(await t.page.evaluate(() => document.activeElement?.closest("[data-id]")?.dataset.id), "tavily", "focus follows the moved row");
    assert.match(await t.page.locator(".sr-only[aria-live]").innerText(), /Tavily is now 2 of 3/, "the move is announced");
    await row(t.page, "search", "searxng").getByRole("button", {name: "Move SearXNG up"}).click();
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="searxng"]')?.previousElementSibling?.dataset.id === "brave");
    assert.deepEqual(await ids(t.page, "search"), ["native", "brave", "searxng", "tavily"]);
    assert.equal(await row(t.page, "search", "brave").getByRole("button", {name: "Move Brave Search up"}).isDisabled(), true, "the first movable row cannot go up");
    assert.equal(await row(t.page, "search", "tavily").getByRole("button", {name: "Move Tavily down"}).isDisabled(), true, "the last cannot go down");
    // Pointer drag: Tavily (last) above Brave Search (first movable).
    const grip = await row(t.page, "search", "tavily").locator(".web-grip").boundingBox();
    const target = await row(t.page, "search", "brave").boundingBox();
    await t.page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await t.page.mouse.down();
    await t.page.mouse.move(grip.x + 4, target.y + 4, {steps: 6});
    assert.equal(await t.page.locator(".web-row[data-dragging]").count(), 1, "the dragged row is marked");
    await t.page.mouse.up();
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="tavily"]')?.previousElementSibling?.dataset.id === "native");
    assert.deepEqual(await ids(t.page, "search"), ["native", "tavily", "brave", "searxng"]);
    assert.deepEqual(state.order.search.slice(0, 3), ["tavily", "brave", "searxng"], "the server holds the dragged order");
    assert.equal(await t.page.locator(".web-row[data-dragging]").count(), 0);
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS order changes by keyboard, arrow buttons and drag");
  }

  // ---- 3. A failed save reverts the list and says so.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111", brave: "brave-bbbb2222"};
    const t = await open();
    state.failNext = true;
    await row(t.page, "search", "brave").getByRole("button", {name: "Move Brave Search up"}).click();
    await t.page.locator(".web-tools > .error-text").waitFor();
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="tavily"]')?.previousElementSibling?.dataset.id === "native");
    assert.deepEqual(await ids(t.page, "search"), ["native", "tavily", "brave", "searxng"], "the old order is back");
    await t.close();
    console.log("PASS a failed order save reverts and shows the error");
  }

  // ---- 4. Test button: testing, then a stable code or Working.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111"}; state.testResult = {tavily: "auth_failed"}; state.testDelay = 300;
    const t = await open();
    await row(t.page, "search", "tavily").getByRole("button", {name: "Test"}).click();
    await t.page.waitForFunction(() => /Testing/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    assert.equal(await row(t.page, "search", "brave").getByRole("button", {name: "Test"}).isDisabled(), true, "one test at a time");
    await t.page.waitForFunction(() => /Key rejected/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    assert.equal(await row(t.page, "search", "tavily").getAttribute("data-status"), "error");
    assert.deepEqual(requests, ["POST /api/web-tools/providers/tavily/test"]);
    state.testResult = {};
    await row(t.page, "search", "tavily").getByRole("button", {name: "Test"}).click();
    await t.page.waitForFunction(() => /Working/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    assert.equal(await row(t.page, "search", "tavily").getAttribute("data-status"), "ready");
    state.testResult = {tavily: "quota_exceeded"};
    await row(t.page, "search", "tavily").getByRole("button", {name: "Test"}).click();
    await t.page.waitForFunction(() => /No credit left or rate limited/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    await t.close();
    console.log("PASS Test shows testing, a stable code, and Working");
  }

  // ---- 5. Keys: sealed on save, never shown again, removable; Bright Data needs its zone; SearXNG takes an address.
  {
    reset();
    const t = await open();
    await row(t.page, "search", "tavily").getByRole("button", {name: "Set up"}).click();
    const input = row(t.page, "search", "tavily").locator('input[type="password"]');
    assert.equal(await input.getAttribute("autocomplete"), "off");
    await input.fill("tvly-secret-9999");
    await row(t.page, "search", "tavily").getByRole("button", {name: "Save"}).click();
    await t.page.waitForFunction(() => /Ready/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    assert.deepEqual(requests, ['PUT /api/web-tools/providers/tavily {"key":"tvly-secret-9999"}']);
    const text = await t.page.locator(".web-tools").innerText();
    assert.ok(!text.includes("tvly-secret-9999"), "the key is never shown again");
    assert.match(text, /…9999/, "only the last four are shown");
    assert.equal(await t.page.locator(".settings-banner").count(), 0, "the banner goes once search is set up");
    // Bright Data: the zone is required with the key.
    await row(t.page, "fetch", "brightdata").getByRole("button", {name: "Set up"}).click();
    await row(t.page, "fetch", "brightdata").locator('input[type="password"]').fill("bd-key-0000");
    assert.equal(await row(t.page, "fetch", "brightdata").getByRole("button", {name: "Save"}).isDisabled(), true, "no zone, no save");
    await row(t.page, "fetch", "brightdata").locator('input[name="brightdata-zone"]').fill("web_unlocker1");
    await row(t.page, "fetch", "brightdata").getByRole("button", {name: "Save"}).click();
    await t.page.waitForFunction(() => /Ready/.test(document.querySelector('.web-row[data-id="brightdata"]').innerText));
    assert.equal(requests.at(-1), 'PUT /api/web-tools/providers/brightdata {"key":"bd-key-0000","zone":"web_unlocker1"}');
    // SearXNG: an address, shown as given.
    await row(t.page, "search", "searxng").getByRole("button", {name: "Set up"}).click();
    await row(t.page, "search", "searxng").locator('input[type="url"]').fill("http://10.0.0.5:8080");
    await row(t.page, "search", "searxng").getByRole("button", {name: "Save"}).click();
    await t.page.waitForFunction(() => /10\.0\.0\.5/.test(document.querySelector('.web-row[data-id="searxng"]').innerText));
    // Remove with confirmation.
    await row(t.page, "search", "tavily").getByRole("button", {name: "Edit"}).click();
    await row(t.page, "search", "tavily").getByRole("button", {name: "Remove"}).click();
    await t.page.getByRole("button", {name: /^(Remove|Yes|Confirm)/}).last().click();
    await t.page.waitForFunction(() => /Needs a key/.test(document.querySelector('.web-row[data-id="tavily"]').innerText));
    assert.equal(requests.at(-1), "DELETE /api/web-tools/providers/tavily");
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS keys are sealed, shown as a hint only, removable; zone and address forms work");
  }

  // ---- 6. A source can be switched off; the Bot computer row exists only with a computer.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111"}; state.computer = true;
    const t = await open();
    assert.deepEqual(await ids(t.page, "search"), ["native", "tavily", "brave", "searxng", "computer"]);
    assert.deepEqual(await ids(t.page, "fetch"), ["builtin", "firecrawl", "brightdata", "computer"]);
    await row(t.page, "search", "tavily").getByRole("switch").click();
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="tavily"]')?.dataset.status === "off");
    assert.deepEqual(requests, ['PUT /api/web-tools/search/tavily {"enabled":false}']);
    assert.equal(await row(t.page, "search", "tavily").getByRole("switch").isChecked(), false);
    await row(t.page, "search", "tavily").getByRole("switch").click();
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-chain="search"][data-id="tavily"]')?.dataset.status === "ready");
    await t.close();
    console.log("PASS switches turn sources off and on; the computer row follows the computer");
  }

  // ---- 6b. Daily limit: the cap is adjustable; a paid source at its cap says so and shows its count.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111"}; state.used = {tavily: 300};
    const t = await open();
    assert.match(await row(t.page, "search", "tavily").innerText(), /Daily limit reached/);
    assert.match(await row(t.page, "search", "tavily").innerText(), /300 in the last 24 h/);
    assert.equal(await row(t.page, "search", "tavily").getAttribute("data-status"), "limit");
    await pickSelect(t.page.locator("#web-limit-search"), "1000");
    await t.page.waitForFunction(() => document.querySelector('.web-row[data-id="tavily"]')?.dataset.status === "ready");
    assert.equal(requests.at(-1), 'PUT /api/web-tools/limits {"search":1000}');
    await pickSelect(t.page.locator("#web-limit-fetch"), "-1");
    await t.page.waitForFunction(() => document.querySelector("#web-limit-fetch").dataset.value === "-1");
    assert.equal(requests.at(-1), 'PUT /api/web-tools/limits {"fetch":-1}');
    await t.close();
    console.log("PASS the daily limit is adjustable and shown on the source");
  }

  // ---- 7. Master switch off, seven languages, dark theme, phone width.
  {
    reset();
    state.keys = {tavily: "tvly-aaaa1111"}; state.errors = {tavily: "quota_exceeded"}; state.computer = true; state.masterOn = false;
    for (const lang of ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
      for (const [width, theme] of [[1100, "light"], [390, "dark"]]) {
        const t = await open({lang}, {width, theme, height: 1800});
        const text = await t.page.locator(".web-tools").innerText();
        assert.ok(!/webtools\.|\{\{/.test(text), `${lang}: raw key or placeholder in the page`);
        assert.equal(await t.page.locator(".settings-banner.is-warn").count() >= 1, true, `${lang}: master-off notice`);
        const overflow = await t.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert.ok(overflow <= 0, `${lang} ${width}px ${theme}: horizontal overflow ${overflow}px`);
        assert.deepEqual(t.errors, [], `${lang}: page errors ${t.errors}`);
        await t.close();
      }
    }
    console.log("PASS seven languages, light and dark, desktop and phone, without raw keys or overflow");
  }
  console.log("Web tools settings: PASS");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
