// Rendered acceptance for Settings > Usage > Monthly limit and the Admin console spend line.
// Mounts SpendCap and AdminSpend in a Vite fixture with /api/usage/spend and /api/automation-settings stubbed as a small stateful
// server (synthetic data) and drives them with Playwright: the limit is off by default; choosing a limit sends one
// monthly_spend_limit_usd value and the page re-reads the month; the 80% and 100% states are worded; plan sign-ins are named as
// "plan limits apply"; a failed save says so; the admin sees spend and limit per account; all seven languages render without raw keys.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:spend-cap
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".spend-cap-"));
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
import {SpendCap} from '../src/SpendCap';
import {AdminSpend} from '../src/AdminSpend';
const query=new URLSearchParams(location.search);
const entry={account_id:'a1',month:'2026-11',spend_usd:42.5,limit_usd:50,state:'near' as const,available:true};
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<div className="workspace"><div className="settings-page-body">{query.get('page')==='admin'?<AdminSpend entry={query.get('missing')?undefined:entry}/>:<SpendCap/>}</div></div>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const state = {limit: 0, spend: 12.4, failNext: false};
  const requests = [];
  const status = () => {
    const near = state.limit > 0 && state.spend * 100 >= state.limit * 80;
    const reached = state.limit > 0 && state.spend >= state.limit;
    return {month: "2026-11", month_start: "2026-11-01T08:00:00.000000000Z", month_end: "2026-12-01T08:00:00.000000000Z", timezone: "America/Los_Angeles", spend_usd: state.spend, limit_usd: state.limit, state: state.limit <= 0 ? "off" : reached ? "reached" : near ? "near" : "ok"};
  };
  const answer = (url, method, body) => {
    if (url.pathname === "/api/usage/spend" && method === "GET") return status();
    if (url.pathname === "/api/automation-settings" && method === "PUT") {
      if (state.failNext) { state.failNext = false; return [400, {error: {code: "invalid_setting", message: "Could not save this setting"}}]; }
      state.limit = body.values.monthly_spend_limit_usd;
      return {items: []};
    }
    return undefined;
  };
  async function open(params = {}, {width = 1000, height = 900, locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: "reduce", locale});
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
      const result = answer(url, request.method(), body);
      if (result === undefined) { errors.push(`unstubbed ${request.method()} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {code: "not_found", message: "not stubbed"}})}); return; }
      const [code, payload] = Array.isArray(result) ? result : [200, result];
      await route.fulfill({status: code, contentType: "application/json", body: JSON.stringify(payload)});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams(params)}`);
    await page.locator(params.page === "admin" ? "[data-testid=admin-spend]" : "[data-testid=spend-figure]").waitFor({timeout: 20000});
    return {page, errors, close: () => context.close()};
  }
  async function pickSelect(page, value) {
    await page.locator("#spend-limit").click();
    await page.locator(`.ui-select-item[data-value="${value}"]`).click();
    await page.locator(".ui-select-content").waitFor({state: "detached"});
  }

  // ---- 1. Off by default; a limit is one setting write, and the month is read again.
  {
    state.limit = 0; state.spend = 12.4; requests.length = 0;
    const t = await open();
    const text = (await t.page.locator(".settings-page-body").innerText()).replace(/\s+/g, " ");
    assert.match(text, /\$12\.40/, "spend this month is shown");
    assert.match(text, /No limit set/, "no limit is the default");
    assert.equal(await t.page.locator("#spend-limit").getAttribute("data-value"), "0", "the limit control reads Off");
    assert.match(text, /plan limits apply/i, "plan sign-ins are named as plan limits");
    assert.equal(await t.page.locator(".spend-meter").count(), 0, "no meter without a limit");
    await pickSelect(t.page, "50");
    await t.page.locator(".spend-meter").waitFor();
    assert.deepEqual(requests, ['PUT /api/automation-settings {"values":{"monthly_spend_limit_usd":50}}'], "one write, one value");
    assert.equal(await t.page.locator(".spend-meter").getAttribute("aria-valuemax"), "50");
    assert.match(await t.page.locator(".settings-page-body").innerText(), /25% of the limit used/);
    assert.match(await t.page.locator(".settings-page-body").innerText(), /Resets Dec 1, 2026/, "the reset date is the first of next month in the account's zone");
    assert.deepEqual(t.errors, []);
    await t.close();
  }
  console.log("PASS the limit is off by default and one choice writes one setting");

  // ---- 2. 80% and 100% are worded; a limit outside the menu stays selectable.
  {
    state.limit = 50; state.spend = 42; requests.length = 0;
    let t = await open();
    assert.match(await t.page.locator(".settings-page-body").innerText(), /At 84% of the limit/);
    assert.equal(await t.page.locator(".spend-meter").getAttribute("data-state"), "near");
    await t.close();
    state.spend = 50.3; state.limit = 7;
    t = await open();
    const text = await t.page.locator(".settings-page-body").innerText();
    assert.match(text, /Limit reached\. Calls to billed models are refused\./);
    assert.equal(await t.page.locator("#spend-limit").getAttribute("data-value"), "7", "a limit set elsewhere stays selectable");
    assert.equal(await t.page.locator("[data-testid=spend-figure]").getAttribute("data-state"), "reached");
    await t.close();
  }
  console.log("PASS 80% and 100% states are worded");

  // ---- 3. A failed save says so and keeps the old limit.
  {
    state.limit = 0; state.spend = 1; state.failNext = true;
    const t = await open();
    await pickSelect(t.page, "25");
    await t.page.locator(".auto-error").waitFor();
    assert.match(await t.page.locator(".auto-error").innerText(), /Could not save this setting/);
    assert.equal(await t.page.locator("#spend-limit").getAttribute("data-value"), "0");
    await t.close();
  }
  console.log("PASS a failed save reverts and says so");

  // ---- 4. The admin sees spend and limit per account.
  {
    const t = await open({page: "admin"});
    const text = (await t.page.locator(".settings-page-body").innerText()).replace(/\s+/g, " ");
    assert.match(text, /\$42\.50 \/ \$50/, "spend and limit");
    assert.match(text, /At 85% of the limit/);
    await t.close();
    const gone = await open({page: "admin", missing: "1"});
    assert.match(await gone.page.locator("[data-testid=admin-spend]").innerText(), /—/);
    await gone.close();
  }
  console.log("PASS the admin sees spend and limit");

  // ---- 5. All seven languages render without raw keys or overflow at 390 px.
  for (const [lang, locale] of [["en", "en-US"], ["zh-CN", "zh-CN"], ["zh-TW", "zh-TW"], ["ja", "ja-JP"], ["ko", "ko-KR"], ["de", "de-DE"], ["fr", "fr-FR"]]) {
    state.limit = 50; state.spend = 42;
    for (const page of ["spend", "admin"]) {
      const t = await open({lang, page}, {width: 390, height: 844, locale});
      const text = await t.page.locator(".settings-page-body").innerText();
      assert.ok(!/spend\.|settings:|automation\./.test(text), `${lang}/${page}: raw key on the page`);
      assert.ok(await t.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 1, `${lang}/${page}: page overflows`);
      assert.deepEqual(t.errors, [], `${lang}/${page}: page errors`);
      await t.close();
    }
  }
  console.log("PASS all seven languages render without raw keys or overflow");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
