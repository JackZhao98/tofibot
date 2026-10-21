// Rendered acceptance for the shared model catalog: every mounted model picker refetches when a provider changes.
// Mounts two real ModelFields pickers in a Vite fixture with /api answered by a stateful stub (synthetic data) and checks:
//  1. both pickers share one GET /api/models on mount (deduplicated);
//  2. invalidateModelCatalog() makes both show the new list with exactly one more request, and keeps the old list on screen meanwhile;
//  3. the real provider calls (API key save, key removal, Codex sign-in poll, Codex disconnect) invalidate it;
//  4. a status check that sees Codex connected from another tab invalidates it;
//  5. a window refocus refetches only when the last load is old, and nothing polls while idle.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:model-catalog-refresh
import assert from "node:assert/strict";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".model-catalog-refresh-"));
let server, browser;
try {
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `import React from 'react';import {createRoot} from 'react-dom/client';import {i18nReady,setLanguage} from '../src/i18n';import {ModelFields} from '../src/ModelSettings';import {invalidateModelCatalog} from '../src/modelCatalogStore';import {api} from '../src/api';
(window as any).__invalidate=invalidateModelCatalog;(window as any).__api=api;
void i18nReady.then(()=>setLanguage('en')).then(()=>createRoot(document.getElementById('root')!).render(<div className="workspace"><section data-testid="a"><ModelFields model="" effort="" allowDefault={false} onChange={()=>{}}/></section><section data-testid="b"><ModelFields model="" effort="" allowDefault={false} onChange={()=>{}}/></section></div>));`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME});
  const page = await browser.newPage();

  const old = [{id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", reasoning_efforts: ["high"], default_reasoning: "high"}];
  const withCodex = [{id: "codex-gpt-6-sol", name: "Codex · GPT-6 Sol", provider: "codex", reasoning_efforts: ["high"], default_reasoning: "high"}, ...old];
  let served = old, modelRequests = 0, codexConnected = false, hold = 0;
  const unexpected = [];
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    if (url.origin !== origin) { unexpected.push(url.href); await route.abort(); return; }
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    const reply = (body, status = 200) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(body)});
    if (url.pathname === "/api/models") {
      modelRequests++;
      const snapshot = served;
      if (hold) await new Promise(resolve => setTimeout(resolve, hold));
      return reply({models: snapshot, source: "live", warning: ""});
    }
    if (url.pathname === "/api/auth/codex" && request.method() === "GET") return reply({connected: codexConnected});
    if (url.pathname === "/api/auth/codex" && request.method() === "DELETE") { codexConnected = false; served = old; return reply({}); }
    if (/\/api\/auth\/codex\/connect\/.+\/poll$/.test(url.pathname)) { codexConnected = true; served = withCodex; return reply({connected: true, pending: false}); }
    if (/^\/api\/providers\/(openai|anthropic)\/key$/.test(url.pathname)) return reply({id: "openai", configured: request.method() === "PUT"});
    unexpected.push(`${request.method()} ${url.pathname}`);
    return reply({});
  });
  const options = async which => (await page.locator(`[data-testid=${which}] select option`).allTextContents()).join("|");
  const settle = async expectedRequests => { await page.waitForFunction(() => true); for (let i = 0; i < 50 && modelRequests < expectedRequests; i++) await page.waitForTimeout(20); await page.waitForTimeout(150); };

  await page.goto(`${origin}/${basename(fixture)}/index.html`);
  await page.waitForSelector("[data-testid=a] optgroup", {state: "attached"});
  await settle(1);
  assert.equal(modelRequests, 1, "two pickers mounting together share one request");
  assert.ok((await options("a")).includes("Claude Opus 5.5") && (await options("b")).includes("Claude Opus 5.5"));
  console.log("PASS 1: two pickers share one /api/models request");

  // 2. Explicit invalidation; the old list stays visible while the new request is in flight.
  served = withCodex; hold = 400;
  await page.evaluate(() => window.__invalidate());
  await page.waitForTimeout(100);
  assert.ok((await options("a")).includes("Claude Opus 5.5") && !(await options("a")).includes("Codex"), "old list stays during the refetch");
  await page.waitForFunction(() => ["a", "b"].every(id => document.querySelector(`[data-testid=${id}]`).textContent.includes("Codex")));
  await settle(2);
  assert.ok((await options("a")).includes("Codex · GPT-6 Sol") && (await options("b")).includes("Codex · GPT-6 Sol"));
  assert.equal(modelRequests, 2, "one more request for both pickers");
  hold = 0;
  console.log("PASS 2: invalidateModelCatalog() refreshes every mounted picker with one request");

  // 3. Real provider calls invalidate it.
  const run = async (label, fn, next, count) => {
    served = next;
    await page.evaluate(fn);
    await page.waitForFunction(want => document.querySelector("[data-testid=b]").textContent.includes("Codex") === want, next === withCodex);
    await settle(count);
    assert.equal(modelRequests, count, `${label}: one refetch`);
  };
  await run("key removal", () => window.__api.deleteProviderKey("openai"), old, 3);
  assert.ok(!(await options("a")).includes("Codex"));
  await run("key save", () => window.__api.setProviderKey("openai", "sk-synthetic-key-0000"), withCodex, 4);
  assert.ok((await options("b")).includes("Codex"));
  await run("codex disconnect", () => window.__api.codexDisconnect(), old, 5);
  await run("codex sign-in poll", () => window.__api.codexPoll("session-1"), withCodex, 6);
  assert.ok((await options("a")).includes("Codex"));
  console.log("PASS 3: key save/removal and Codex poll/disconnect refresh the pickers");

  // 4. Another tab finished the sign-in: the status check sees a new state.
  codexConnected = true; await page.evaluate(() => window.__api.codexStatus()); await settle(7); // first sighting is the baseline for this tab
  assert.equal(modelRequests, 6, "first status sighting is not a change");
  codexConnected = false; served = old;
  await page.evaluate(() => window.__api.codexStatus());
  await page.waitForFunction(() => !document.querySelector("[data-testid=a]").textContent.includes("Codex"));
  await settle(7);
  assert.equal(modelRequests, 7);
  console.log("PASS 4: a Codex status change seen from another tab refreshes the pickers");

  // 5. Focus: a recent load is not refetched; an old one is. Nothing polls.
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await page.waitForTimeout(300);
  assert.equal(modelRequests, 7, "focus right after a load does not refetch");
  served = withCodex;
  await page.evaluate(() => { const real = Date.now; Date.now = () => real() + 60_000; window.dispatchEvent(new Event("focus")); });
  await page.waitForFunction(() => document.querySelector("[data-testid=a]").textContent.includes("Codex"));
  await settle(8);
  assert.equal(modelRequests, 8, "focus after the last load went stale refetches once");
  const idle = modelRequests;
  await page.waitForTimeout(1500);
  assert.equal(modelRequests, idle, "no polling while idle");
  console.log("PASS 5: refocus refetches a stale catalog once; idle pages make no requests");

  assert.deepEqual(unexpected, [], "no unexpected requests");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
