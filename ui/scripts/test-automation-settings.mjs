// Rendered acceptance for the Automatic behavior settings page.
// Mounts the real component in a Vite fixture with /api/automation-settings stubbed as a small stateful server
// (synthetic data) and drives it with Playwright: every item shows what it does, its default and its current value;
// a change is sent once, persists across a reload and touches only its own row; an environment-locked item cannot be
// changed; a failed save reverts and says so; all seven languages render the page without raw keys or overflow.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:automation-settings
//   AUTOMATION_SHOTS=<dir>  also saves screenshots there
import assert from "node:assert/strict";
import {mkdir, mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

/** Opens our Select under `scope` and picks the option with this value. */
async function pickSelect(scope, value) {
  await scope.locator(".ui-select-trigger").first().click();
  await scope.page().locator(`.ui-select-item[data-value="${value}"]`).click();
  await scope.page().locator(".ui-select-content").waitFor({ state: "detached" });
}
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.AUTOMATION_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

const KEYS = ["conversation_model", "background_model", "structured_output", "force_say_haiku_dm", "web_search", "group_exchange_gists", "own_replies_verbatim", "completion_review", "answer_gate", "mcp_auto_review", "remembered_approval_hours", "provider_retries", "schedule_receipt_review", "desktop_idle_seconds", "browser_engine", "desktop_engine", "widget_artifacts_auto", "widget_computer_auto"];

const fixture = await mkdtemp(join(ui, ".automation-settings-"));
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
import {AutomationSettings} from '../src/AutomationSettings';
import {AllowancesPage} from '../src/settings/SettingsPages';
import {TimezoneProvider} from '../src/UserTimezone';
const query=new URLSearchParams(location.search);
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><div className="workspace"><div className="settings-page-body" data-page={query.get('page')||'automation'}>{query.get('page')==='permissions'?<AllowancesPage openTab={tab=>{(window as any).__openTab=tab}}/>:<AutomationSettings onOpenPermissions={()=>{(window as any).__openTab='permissions'}}/>}</div></div></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const meta = {
    conversation_model: {group: "models", kind: "model", def: "claude-sonnet-5-5"},
    background_model: {group: "models", kind: "model", def: "claude-haiku-5-5", auto: true},
    structured_output: {group: "replies", kind: "bool", def: true},
    force_say_haiku_dm: {group: "replies", kind: "bool", def: true},
    web_search: {group: "replies", kind: "bool", def: true},
    group_exchange_gists: {group: "history", kind: "bool", def: true},
    own_replies_verbatim: {group: "history", kind: "int", def: 3, min: 1, max: 20},
    completion_review: {group: "checks", kind: "bool", def: true},
    answer_gate: {group: "checks", kind: "int", def: 2, min: 0, max: 5},
    mcp_auto_review: {group: "checks", kind: "enum", def: "off", options: ["off", "shadow", "auto"]},
    remembered_approval_hours: {group: "checks", kind: "int", def: 24, min: 1, max: 720, unit: "hours"},
    provider_retries: {group: "reliability", kind: "int", def: 5, min: 0, max: 10},
    schedule_receipt_review: {group: "schedules", kind: "bool", def: true},
    desktop_idle_seconds: {group: "computer", kind: "readonly"},
    browser_engine: {group: "computer", kind: "enum", def: "classic", options: ["classic", "playwright"]},
    desktop_engine: {group: "computer", kind: "enum", def: "classic", options: ["classic", "a11y"]},
    widget_artifacts_auto: {group: "widgets", kind: "bool", def: true},
    widget_computer_auto: {group: "widgets", kind: "bool", def: true},
  };
  const state = {};
  const requests = [];
  const reset = () => {
    state.stored = {};
    state.env = {};
    state.failNext = false;
    state.idle = 300;
    state.allowances = [];
    state.remembered = [];
    requests.length = 0;
  };
  reset();
  const view = key => {
    const m = meta[key];
    if (m.kind === "readonly") return {key, group: m.group, kind: "readonly", default: null, value: state.idle, source: "default", read_only: true, unit: "seconds"};
    const locked = key in state.env;
    const stored = key in state.stored;
    const value = locked ? state.env[key] : stored ? state.stored[key] : (m.auto ? "" : m.def);
    const out = {key, group: m.group, kind: m.kind, default: m.def, value, source: locked ? "env" : stored ? "stored" : "default"};
    if (m.kind === "model") out.effective = value || m.def;
    if (locked) { out.locked = true; out.env = "TOFI_" + key.toUpperCase(); }
    if (m.options) out.options = m.options;
    if (m.min !== undefined) { out.min = m.min; out.max = m.max; }
    if (m.unit) out.unit = m.unit;
    return out;
  };
  const models = [
    {id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium"},
    {id: "claude-haiku-5-5", name: "Claude Haiku 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium"], default_reasoning: "low"},
    {id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium"},
  ];
  const answer = (url, method, body) => {
    const path = url.pathname;
    if (path === "/api/automation-settings" && method === "GET") return {items: KEYS.map(view)};
    if (path === "/api/automation-settings" && method === "PUT") {
      if (state.failNext) { state.failNext = false; return [400, {error: {code: "invalid_setting", message: "Could not save this setting"}}]; }
      for (const [key, value] of Object.entries(body.values)) {
        if (key in state.env) return [409, {error: {code: "env_override", message: "set by the server environment"}}];
        if (value === null) delete state.stored[key]; else state.stored[key] = value;
      }
      return {items: KEYS.map(view)};
    }
    if (path === "/api/auto-review-settings" && method === "GET") return {mode: state.stored.mcp_auto_review ?? "off", review_scope: "all_external_tools"};
    if (path === "/api/auto-review-settings" && method === "PUT") { state.stored.mcp_auto_review = body.mode; return {mode: body.mode, review_scope: "all_external_tools"}; }
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/standing-allowances" && method === "GET") return {allowances: state.allowances, remembered: state.remembered};
    if (path.startsWith("/api/standing-allowances/") && method === "DELETE") {
      const id = decodeURIComponent(path.split("/").pop());
      if (!state.allowances.some(item => item.id === id)) return [404, {error: {code: "not_found", message: "standing allowance not found"}}];
      state.allowances = state.allowances.filter(item => item.id !== id);
      return {ok: true, id};
    }
    if (path.startsWith("/api/conversations/") && path.includes("/remembered-approvals/") && method === "DELETE") {
      const id = decodeURIComponent(path.split("/").pop());
      state.remembered = state.remembered.filter(item => item.id !== id);
      return {ok: true, id};
    }
    if (path === "/api/models") return {models, source: "live", warning: ""};
    return undefined;
  };
  async function open(params = {}, {width = 1100, height = 900, theme = "light", locale = "en-US"} = {}) {
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
      const result = answer(url, request.method(), body);
      if (result === undefined) { errors.push(`unstubbed ${request.method()} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {code: "not_found", message: "not stubbed"}})}); return; }
      const [status, payload] = Array.isArray(result) ? result : [200, result];
      await route.fulfill({status, contentType: "application/json", body: JSON.stringify(payload)});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams(params)}`);
    await page.locator(params.page === "permissions" ? ".permissions-page" : ".auto-row").first().waitFor({timeout: 20000});
    return {page, errors, close: () => context.close()};
  }
  const row = (page, key) => page.locator(`.auto-row[data-key="${key}"]`);

  // ---- 1. Every item shows what it does, its default and its current value.
  {
    reset();
    const t = await open({}, {height: 2400});
    assert.deepEqual(await t.page.locator(".auto-row").evaluateAll(rows => rows.map(r => r.dataset.key)), KEYS, "every behavior is listed, in order");
    for (const key of KEYS) {
      const text = (await row(t.page, key).innerText()).replace(/\s+/g, " ");
      assert.ok(!/automation\./.test(text), `${key}: raw i18n key on the page: ${text}`);
      assert.ok(text.length > 60, `${key}: name and one-sentence description are shown: ${text}`);
      if (key !== "desktop_idle_seconds") assert.match(text, /Default: /, `${key}: shows its default`);
    }
    assert.equal(await t.page.locator(".settings-sec h4").count(), 8, "eight calm groups; the allowance tables live on the Permissions page");
    assert.equal(await t.page.locator(".standing-item, .standing-list").count(), 0, "no allowance table on this page");
    assert.match(await row(t.page, "conversation_model").innerText(), /Claude Sonnet 5\.5/, "conversation model default and value are named");
    assert.equal(await row(t.page, "conversation_model").locator(".ui-select-trigger").getAttribute("data-value"), "claude-sonnet-5-5");
    assert.match(await row(t.page, "background_model").innerText(), /Automatic · Claude Haiku 5\.5/, "background model says what automatic means");
    assert.equal(await row(t.page, "web_search").getByRole("switch").isChecked(), true, "on-by-default switches start on");
    // The reviewer mode has one control, on the Permissions page; this row shows the value (D15) and links there.
    assert.equal(await row(t.page, "mcp_auto_review").locator(".ui-select-trigger, input, [role=switch]").count(), 0, "no second control for the reviewer mode");
    assert.match(await row(t.page, "mcp_auto_review").innerText(), /Off/);
    await row(t.page, "mcp_auto_review").getByRole("button", {name: "Set in Permissions"}).click();
    assert.equal(await t.page.evaluate(() => window.__openTab), "permissions", "the link opens the Permissions page");
    assert.equal(await row(t.page, "provider_retries").locator(".ui-select-trigger").getAttribute("data-value"), "5");
    assert.equal(await row(t.page, "remembered_approval_hours").locator(".ui-select-trigger").getAttribute("data-value"), "24");
    assert.match(await row(t.page, "desktop_idle_seconds").innerText(), /5 min/, "the computer service's idle timeout is shown");
    assert.equal(await row(t.page, "desktop_idle_seconds").locator(".ui-select-trigger, input").count(), 0, "a value owned elsewhere has no control");
    assert.equal(await row(t.page, "browser_engine").locator(".ui-select-trigger").getAttribute("data-value"), "classic", "the browser engine is a real choice, classic by default");
    assert.equal(await row(t.page, "desktop_engine").locator(".ui-select-trigger").getAttribute("data-value"), "classic", "the desktop engine is a real choice, classic by default");
    assert.equal(await t.page.locator(".auto-chip").count(), 0, "nothing is marked changed at the defaults");
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    if (shots) await t.page.locator(".settings-page-body").screenshot({path: join(shots, "automation-light.png")});
    await t.close();
    console.log("PASS every behavior renders with what it does, its default and its current value");
  }

  // ---- 2. A change is sent once, persists across a reload, and renders only its own row.
  {
    reset();
    const t = await open();
    await t.page.evaluate(() => {
      window.__mutations = [];
      new MutationObserver(records => { for (const r of records) window.__mutations.push(r.target.closest?.(".auto-row")?.dataset.key ?? r.target.parentElement?.closest?.(".auto-row")?.dataset.key ?? "outside"); }).observe(document.querySelector(".settings-page-body"), {subtree: true, childList: true, attributes: true, characterData: true});
    });
    const start = Date.now();
    await row(t.page, "web_search").getByRole("switch").click();
    await t.page.waitForFunction(() => document.querySelector('.auto-row[data-key="web_search"] .auto-chip'));
    const elapsed = Date.now() - start;
    assert.deepEqual(requests, ['PUT /api/automation-settings {"values":{"web_search":false}}'], "one write with just that value");
    assert.equal(await row(t.page, "web_search").getByRole("switch").isChecked(), false);
    assert.match(await row(t.page, "web_search").innerText(), /Changed/);
    assert.match(await row(t.page, "web_search").innerText(), /Default: On/, "the default stays visible after a change");
    const touched = await t.page.evaluate(() => [...new Set(window.__mutations)]);
    assert.deepEqual(touched, ["web_search"], `a change must touch only its own row, touched: ${touched}`);
    assert.ok(elapsed < 1500, `toggle round trip took ${elapsed}ms`);
    await pickSelect(row(t.page, "provider_retries"), "2");
    await t.page.waitForFunction(() => document.querySelector('.auto-row[data-key="provider_retries"] .auto-chip'));
    await pickSelect(row(t.page, "background_model"), "claude-opus-5-5");
    await t.page.waitForFunction(() => document.querySelector('.auto-row[data-key="background_model"] .auto-chip'));
    assert.equal(requests.length, 3);
    assert.deepEqual(state.stored, {web_search: false, provider_retries: 2, background_model: "claude-opus-5-5"});
    await t.page.reload();
    await t.page.locator(".auto-row").first().waitFor();
    assert.equal(await row(t.page, "web_search").getByRole("switch").isChecked(), false, "persisted across a reload");
    assert.equal(await row(t.page, "provider_retries").locator(".ui-select-trigger").getAttribute("data-value"), "2");
    assert.equal(await row(t.page, "background_model").locator(".ui-select-trigger").getAttribute("data-value"), "claude-opus-5-5");
    // Reset returns to the default and drops the marker.
    requests.length = 0;
    await row(t.page, "web_search").getByRole("button", {name: "Reset to default"}).click();
    await t.page.waitForFunction(() => !document.querySelector('.auto-row[data-key="web_search"] .auto-chip'));
    assert.deepEqual(requests, ['PUT /api/automation-settings {"values":{"web_search":null}}']);
    assert.equal(await row(t.page, "web_search").getByRole("switch").isChecked(), true);
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS changes persist, reset restores the default, and a change renders only its own row");
  }

  // ---- 3. An item set by the server environment says so and cannot be changed.
  {
    reset();
    state.env = {web_search: false, provider_retries: 1};
    const t = await open();
    const locked = row(t.page, "web_search");
    assert.match(await locked.innerText(), /Set by server environment/);
    assert.equal(await locked.getByRole("switch").isDisabled(), true);
    assert.equal(await locked.getByRole("switch").isChecked(), false, "shows the value in force");
    assert.equal(await row(t.page, "provider_retries").locator(".ui-select-trigger").isDisabled(), true);
    assert.equal(await row(t.page, "provider_retries").locator(".ui-select-trigger").getAttribute("data-value"), "1");
    assert.equal(await locked.getByRole("button", {name: "Reset to default"}).count(), 0, "no reset for a locked item");
    assert.equal(await row(t.page, "completion_review").getByRole("switch").isDisabled(), false, "other items stay editable");
    if (shots) 
    await t.close();
    console.log("PASS environment-locked items show the value in force and stay read-only");
  }

  // ---- 4. A failed save reverts and says so.
  {
    reset();
    const t = await open();
    state.failNext = true;
    await row(t.page, "completion_review").getByRole("switch").click();
    await row(t.page, "completion_review").getByRole("alert").waitFor();
    assert.match(await row(t.page, "completion_review").innerText(), /Could not save this setting/);
    assert.equal(await row(t.page, "completion_review").getByRole("switch").isChecked(), true, "the control still shows what is in force");
    assert.deepEqual(state.stored, {});
    await t.close();
    console.log("PASS a failed save keeps the old value and shows the reason");
  }

  // ---- 4b. Standing allowances: the list names Bot, server, tool, when granted and last used; Revoke removes one immediately.
  const sample = () => ({
    allowances: [
      {id: "al-1", bot_id: "bot-tofi", bot_name: "tofi", server: "notion", tool: "notion-update-page", state: "active", created_at: "2026-10-01T16:00:00Z", use_count: 3, last_used_at: "2026-10-19T14:30:00Z"},
      {id: "al-2", bot_id: "bot-two", bot_name: "Scout", server: "linear", tool: "create-issue", state: "server_changed", created_at: "2026-10-02T16:00:00Z", use_count: 0},
      {id: "al-3", bot_id: "bot-two", bot_name: "Scout", server: "linear", tool: "update-issue", state: "tool_changed", created_at: "2026-10-03T16:00:00Z", use_count: 0},
      {id: "al-4", bot_id: "bot-two", bot_name: "Scout", server: "linear", tool: "delete-issue", state: "tier_changed", created_at: "2026-10-04T16:00:00Z", use_count: 0},
    ],
    remembered: [{id: "rm-1", conversation_id: "conv-1", bot_id: "bot-tofi", bot_name: "tofi", conversation_name: "Daily notes", server: "notion", tool: "notion-create-pages", target_kind: "exact", expires_at: "2026-10-21T16:00:00Z", use_count: 1}],
  });
  {
    reset();
    Object.assign(state, sample());
    const t = await open({page: "permissions"}, {});
    await t.page.locator(".permissions-page").waitFor();
    const items = t.page.locator(".standing-item[data-allowance-id]");
    await items.first().waitFor();
    assert.equal(await items.count(), 4);
    assert.match(await t.page.locator(".settings-page-body").innerText(), /also applies when the Bot works on its own, for example on a schedule/, "Settings says the allowance applies when the Bot works on its own");
    assert.deepEqual(await t.page.locator(".permissions-how-row strong").allInnerTexts(), ["Runs without asking", "Asks when unsure", "Always asks me"], "the three words, in order");
    assert.equal(await t.page.locator("#auto-review-mode").getAttribute("data-value"), "off", "the reviewer mode has its one control here");
    assert.equal(await t.page.locator(".permissions-page .settings-banner").count(), 0, "no 'more controls are on the way' banner");
    assert.match(await t.page.locator(".permissions-page").innerText(), /Always allowed/);
    assert.match(await t.page.locator(".permissions-page").innerText(), /Remembered in chats/);
    const first = (await items.nth(0).innerText()).replace(/\s+/g, " ");
    assert.match(first, /tofi/); assert.match(first, /notion-update-page/); assert.match(first, /notion/);
    assert.match(first, /Allowed .*2026/); assert.match(first, /Last used .*2026/); assert.match(first, /used 3 times/);
    assert.equal(await items.nth(0).locator(".standing-avatar").count(), 1, "the Bot avatar is shown");
    assert.match(await items.nth(1).innerText(), /Not used yet/);
    assert.match(await items.nth(1).innerText(), /Server changed\. Asks again\./);
    assert.match(await items.nth(2).innerText(), /redefined by its server\. Asks again\./);
    assert.match(await items.nth(3).innerText(), /setting changed\. Asks again\./);
    assert.equal(await t.page.locator(".standing-item[data-remembered-id]").count(), 1, "chat-scoped approvals are listed too");
    assert.match(await t.page.locator(".standing-item[data-remembered-id]").innerText(), /Chat: Daily notes/);
    requests.length = 0;
    await items.nth(0).getByRole("button", {name: "Revoke"}).click();
    await t.page.waitForFunction(() => document.querySelectorAll(".standing-item[data-allowance-id]").length === 3);
    assert.deepEqual(requests, ["DELETE /api/standing-allowances/al-1"], "revoke is one request for that allowance");
    assert.deepEqual(state.allowances.map(item => item.id), ["al-2", "al-3", "al-4"]);
    await t.page.locator(".standing-item[data-remembered-id]").getByRole("button", {name: "Revoke"}).click();
    await t.page.waitForFunction(() => document.querySelectorAll(".standing-item[data-remembered-id]").length === 0);
    assert.match(await t.page.locator(".settings-page-body").innerText(), /Nothing is remembered in any chat/);
    for (let left = 3; left > 0; left--) {
      await t.page.locator(".standing-item[data-allowance-id]").first().getByRole("button", {name: "Revoke"}).click();
      await t.page.waitForFunction(n => document.querySelectorAll(".standing-item[data-allowance-id]").length === n, left - 1);
    }
    assert.match(await t.page.locator(".settings-page-body").innerText(), /No standing allowances yet/);
    // Settings still carries its own table of words: Permissions in the rail copy, never Approvals.
    assert.doesNotMatch(await t.page.locator(".settings-page-body").innerText(), /Standing allowances/);
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS standing allowances list Bot, server, tool and dates, and Revoke removes them");
  }

  // ---- 5. Seven languages: no raw keys, no horizontal overflow at 390px.
  for (const [lang, locale, title] of [["en", "en-US", "Conversation model"], ["zh-CN", "zh-CN", "对话模型"], ["zh-TW", "zh-TW", "對話模型"], ["ja", "ja-JP", "会話モデル"], ["ko", "ko-KR", "대화 모델"], ["de", "de-DE", "Gesprächsmodell"], ["fr", "fr-FR", "Modèle de conversation"]]) {
    reset();
    state.stored = {remembered_approval_hours: 48, provider_retries: 0};
    Object.assign(state, sample());
    const t = await open({lang}, {width: 390, height: 844, locale});
    await t.page.getByText(title, {exact: true}).first().waitFor({timeout: 15000});
    const text = await t.page.locator(".settings-page-body").innerText();
    assert.ok(!/automation\.|settings:/.test(text), `${lang}: raw key on the page`);
    const overflow = await t.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    assert.ok(overflow <= 1, `${lang}: page overflows by ${overflow}px`);
    const clipped = await t.page.evaluate(() => [...document.querySelectorAll(".auto-row .auto-chip, .auto-row .settings-row-copy strong, .standing-main strong")].filter(el => el.scrollWidth > el.clientWidth + 1).map(el => el.textContent));
    assert.deepEqual(clipped, [], `${lang}: clipped text`);
    if (shots && (lang === "en" || lang === "zh-CN")) await t.page.locator(".settings-page-body").screenshot({path: join(shots, `automation-390-${lang}.png`)});
    await t.close();
    const permissions = await open({lang, page: "permissions"}, {width: 390, height: 844, locale});
    const permissionsText = await permissions.page.locator(".settings-page-body").innerText();
    assert.ok(!/permissionsPage\.|extensions:|settings:/.test(permissionsText), `${lang}: raw key on the Permissions page`);
    assert.ok(await permissions.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 1, `${lang}: Permissions page overflows`);
    await permissions.close();
  }
  console.log("PASS all seven languages render the page at 390px without raw keys or overflow");

  // ---- 6. Dark theme screenshot for the record.
  if (shots) {
    reset();
    state.stored = {web_search: false, mcp_auto_review: "shadow"};
    state.env = {provider_retries: 1};
    const t = await open({}, {theme: "dark", height: 2400});
    await t.page.locator(".settings-page-body").screenshot({path: join(shots, "automation-dark.png")});
    await t.close();
  }
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
