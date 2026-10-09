// Rendered acceptance for the settings shell (docs/agent-plan/settings-redesign.md, 2.6 item 3).
// Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed
// (neutral sample data only) and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//   SETTINGS_SHOTS=<dir>  also saves every tab at 1440x900 and 390x844, light and dark.
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"];
const TABS = ["general", "connections", "skills", "approvals", "models", "computer", "keys", "usage", "advanced"];
const ICONS = {general: "sliders", connections: "plug", skills: "skill", approvals: "shield-check", models: "sparkles", computer: "monitor", keys: "key", usage: "progress", advanced: "settings", admin: "group"};
const NAMES = {general: "General", connections: "Connections", skills: "Skills", approvals: "Approvals", models: "Models", computer: "Computer", keys: "Keys & secrets", usage: "Usage", advanced: "Advanced", admin: "Admin console"};

// ---- Catalog checks (every locale): groups, page names and descriptions are real translations.
const catalogs = {};
for (const lang of LANGS) catalogs[lang] = JSON.parse(await readFile(join(ui, "src/locales", lang, "settings.json"), "utf8"));
for (const lang of LANGS) {
  const shell = catalogs[lang].shell;
  assert.deepEqual(Object.keys(shell.group).sort(), ["bots", "system", "workspace", "you"], `${lang}: shell.group keys`);
  assert.deepEqual(Object.keys(shell.page).sort(), [...TABS, "admin"].sort(), `${lang}: shell.page keys (no stale tab ids)`);
  let sameAsEnglish = 0;
  for (const [id, page] of Object.entries(shell.page)) {
    assert.ok(page.name && page.description, `${lang}: ${id} needs a name and a description`);
    assert.ok([...page.description].length <= 70, `${lang}: ${id} description is ${[...page.description].length} characters (max 70)`);
    if (lang !== "en") {
      if (page.name === catalogs.en.shell.page[id].name) sameAsEnglish++;
      assert.notEqual(page.description, catalogs.en.shell.page[id].description, `${lang}: ${id} description is an English copy`);
    }
  }
  assert.ok(sameAsEnglish <= 3, `${lang}: ${sameAsEnglish} page names equal the English ones`);
  for (const id of ["gpt-4o-mini-transcribe", "gpt-4o-transcribe"]) {
    assert.ok(catalogs[lang].dictation.model[id].hint && catalogs[lang].dictation.model[id].price, `${lang}: dictation copy for ${id}`);
    if (lang !== "en") assert.notEqual(catalogs[lang].dictation.model[id].hint, catalogs.en.dictation.model[id].hint, `${lang}: dictation hint is an English copy`);
  }
}
console.log(`PASS catalogs: ${LANGS.length} locales have groups, 10 page names, descriptions of at most 70 characters, dictation copy`);

const shots = process.env.SETTINGS_SHOTS;
if (shots) await mkdir(shots, {recursive: true});
const fixture = await mkdtemp(join(ui, ".settings-shell-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab,type SettingsView} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {subscribeSettingsDeepLinks} from '../src/settings/deepLinks';
import {CodexPanel,NotificationSetting} from '../src/App';
const query=new URLSearchParams(location.search);
const bots=[{id:'bot_research',name:'Research Bot',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_inbox',name:'Inbox Bot',archived:false,model:'gpt-6-luna',reasoning_effort:'medium'},{id:'bot_notes',name:'Notes Bot',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_old',name:'Retired Bot',archived:true,model:'default',reasoning_effort:'default'}] as any;
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as SettingsView)||'home'});
 const [closed,setClosed]=useState(false);
 const openSettings=(next:SettingsTab,view:SettingsView='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 useEffect(()=>{(window as any).__closed=closed},[closed]);
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true">{!closed&&<SettingsShell tab={tab} onTab={setTab} onClose={()=>setClosed(true)} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,notifications:<NotificationSetting/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/>}</aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const requested = [], unexpected = [], writes = [], accessWrites = [];
  const skillAccess = new Map();
  const INSTANCE = "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d";
  const providers = [
    {id: "codex", label: "Codex", kind: "oauth", configured: true, status: "connected"},
    {id: "openai", label: "OpenAI", kind: "api_key", configured: true, key_hint: "…9fa2", verified_at: "2026-10-08T16:05:00Z", error: ""},
    {id: "anthropic", label: "Claude", kind: "api_key", configured: false, key_hint: "", verified_at: "", error: ""},
  ];
  const models = [
    {id: "codex-gpt-6-sol", name: "Codex · GPT-6 Sol", provider: "codex", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium"},
    {id: "gpt-6-luna", name: "GPT-6 Luna", provider: "openai", reasoning_efforts: ["low", "medium", "high", "xhigh", "max"], default_reasoning: "medium"},
    {id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium", "high", "xhigh"], default_reasoning: "high"},
  ];
  const day = n => new Date(Date.UTC(2026, 9, 9 - n)).toISOString();
  const stub = (url, method, query) => {
    const path = url.pathname;
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: {mode: "owner"}};
    if (path === "/api/auth/session") return query.get("admin") === "0"
      ? {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}}
      : {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin"}};
    if (path === "/api/bots") return {bots: [{id: "bot_research", name: "Research Bot", archived: false}, {id: "bot_inbox", name: "Inbox Bot", archived: false}, {id: "bot_notes", name: "Notes Bot", archived: false}, {id: "bot_old", name: "Retired Bot", archived: true}]};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      {name: "acme_docs", url: "https://mcp.example.test/docs", oauth: {client_id: "sample", connected: false}},
      {name: "acme_tracker", url: "https://mcp.example.test/tracker", oauth: {client_id: "sample", connected: true}},
      {name: "notes_search", url: "https://mcp.example.test/notes", headers: {}},
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") return {ok: true, tool_count: 8};
    if (path === "/api/extensions/oauth-options") return {vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/local-mcp") return {available: false, reason: "sample"};
    if (path === "/api/extensions/skills") return {skills: [{name: "research-assistant", description: "Research and verify sources"}, {name: "weekly-report", description: "Draft the weekly status report"}].map(skill => ({...skill, access: skillAccess.get(skill.name) ?? {mode: "all"}}))};
    if (path === "/api/auto-review-settings") return {mode: "shadow", revision: 1, review_scope: "all_external_tools"};
    if (path === "/api/providers") return {providers};
    if (path === "/api/auth/codex") return {connected: true, expires_at: Date.parse("2026-11-01T00:00:00Z")};
    if (path === "/api/auth/codex/verify") return {connected: true, expires_at: Date.parse("2026-11-01T00:00:00Z"), check: "ok"};
    if (path === "/api/models") return {models, source: "live", warning: ""};
    if (path === "/api/model-settings") return {model: "codex-gpt-6-sol", reasoning_effort: "medium"};
    if (path === "/api/dictation-settings") return query.get("dict") === "unknown"
      ? {model: "mystery-model", configured: true, auth_source: "api_key", models: [{id: "mystery-model", name: "Mystery Transcribe"}]}
      : {model: "gpt-4o-mini-transcribe", configured: true, auth_source: "api_key", models: [{id: "gpt-4o-mini-transcribe", name: "GPT-4o mini Transcribe"}, {id: "gpt-4o-transcribe", name: "GPT-4o Transcribe"}]};
    if (path === "/api/computers/firecracker/info") return {state: "ready", workspace_root: "/workspace", browser: "Chromium"};
    if (path === "/api/computers") return {computers: [{id: "dev_sample", name: "Sample laptop", kind: "mac", online: true}]};
    if (path === "/api/computer/resources") return {current: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, desired: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, pending: false, state: "ready", host: {cpus: 8, memory_total_mib: 16384, memory_available_mib: 9000, disk_available_gib: 120}, limits: {max_vcpus: 6, max_memory_mib: 12288, max_disk_gib: 100}};
    if (path === "/api/computer/credentials") return {credentials: [{id: "cred_1", name: "WEB_API_TOKEN", kind: "env", target: "WEB_API_TOKEN", created_at: day(3)}, {id: "cred_2", name: "DEPLOY_REGION", kind: "env", target: "DEPLOY_REGION", created_at: day(5)}]};
    if (path === "/api/computer/ssh-keys") return {keys: []};
    if (path === "/api/usage") return {contexts: [{run_id: "run_1", conversation_id: "conv_1", conversation_name: "Weekly summary", bot_id: "bot_research", bot_name: "Research Bot", run_status: "completed", model: "codex-gpt-6-sol", window_tokens: 200000, window_known: true, compact_at: 160000, estimated_input: 52000, last_input: 48000, total_input: 310000, total_output: 42000, compact_count: 1, updated_at: day(0)}],
      agents: [{bot_id: "bot_research", bot_name: "Research Bot", input_tokens: 310000, output_tokens: 42000, runs: 14, compactions: 1}, {bot_id: "bot_inbox", bot_name: "Inbox Bot", input_tokens: 120000, output_tokens: 18000, runs: 6, compactions: 0}],
      periods: {"24h": [{bot_id: "bot_research", input_tokens: 90000, output_tokens: 12000, requests: 5, equivalent_usd: 0.42, unpriced_requests: 0}], "7d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}], "30d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}]},
      calls: [{id: 1, bot_id: "bot_research", bot_name: "Research Bot", conversation_name: "Weekly summary", model: "codex-gpt-6-sol", input_tokens: 18000, output_tokens: 2100, equivalent_usd: 0.09, price_known: true, trigger_content: "Summarize the week", occurred_at: day(0)}],
      price_source: "sample", price_as_of: "2026-10-01", note: ""};
    if (path === "/api/admin/accounts") return [{id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin", status: "active", created_at: day(30)}, {id: "acct_two", username: "Sam Example", email: "sam@example.test", role: "member", status: "active", created_at: day(9)}];
    if (path === "/api/admin/capacity") return {available_bytes: 80e9, admission_remaining_bytes: 60e9, warning: false, accounts: []};
    return undefined;
  };
  async function open(params = {}, {width = 1440, height = 900, theme = "light", reduced = false, locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: reduced ? "reduce" : "no-preference", colorScheme: theme, locale});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      requested.push(`${method} ${url.pathname}`);
      if (method === "PUT" && /^\/api\/extensions\/skills\/[^/]+\/access$/.test(url.pathname)) {
        const payload = JSON.parse(request.postData() ?? "{}"), name = url.pathname.split("/")[4];
        accessWrites.push(`${method} ${url.pathname} ${request.postData()}`);
        skillAccess.set(name, payload.mode === "all" ? {mode: "all"} : {mode: "selected", bot_ids: payload.bot_ids});
        await route.fulfill({contentType: "application/json", body: JSON.stringify({ok: true, access: skillAccess.get(name)})});
        return;
      }
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""));
      if (body === undefined) { unexpected.push(`${method} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {message: "not stubbed", code: "not_found"}})}); return; }
      if (method !== "GET" && !/\/(test|verify)$/.test(url.pathname)) writes.push(`${method} ${url.pathname}`);
      await route.fulfill({contentType: "application/json", body: JSON.stringify(body)});
    });
    const query = new URLSearchParams({lang: "en", ...params});
    await page.goto(`${origin}/${basename(fixture)}/index.html?${query}`);
    await page.locator(".settings-shell").waitFor({timeout: 20000});
    return {page, close: () => context.close()};
  }
  const railTabs = page => page.$$eval(".settings-tabs .settings-nav-group button", buttons => buttons.map(button => button.dataset.tab));
  const selectedTab = page => page.locator('.settings-tabs [aria-current="page"]').getAttribute("data-tab");
  const settle = page => page.waitForTimeout(450);

  // ---- 1. Rail: exactly the nine tabs in order; admin only for a multi-account admin session.
  {
    const {page, close} = await open({admin: "0"});
    assert.deepEqual(await railTabs(page), TABS, "rail shows the nine tabs in order for a non-admin session");
    assert.deepEqual(await page.$$eval(".settings-tabs .settings-nav-group>p", items => items.map(item => item.textContent)), ["You", "Bots can use", "Workspace", "System"]);
    assert.equal(await page.locator("#settings-category").count(), 0, "no category select");
    const icons = await page.$$eval(".settings-tabs .settings-nav-group button", buttons => buttons.map(button => button.querySelector("svg").dataset.tofiIcon));
    assert.equal(new Set(icons).size, icons.length, "no two tabs share an icon");
    assert.deepEqual(icons, TABS.map(id => ICONS[id]), "icons match the contract");
    // The connections badge counts saved connections that need sign-in, from the saved list alone.
    assert.equal((await page.locator('.settings-tabs [data-tab="connections"] .settings-count').textContent()).trim(), "1");
    assert.equal(await page.locator('.settings-tabs [data-tab="approvals"] .settings-count').count(), 0, "approvals badge stays hidden in Phase 1");
    await page.waitForTimeout(600);
    for (const forbidden of ["/api/extensions/local-mcp", "/api/computers", "/api/computer/"]) assert.ok(!requested.some(line => line.includes(forbidden)), `opening Settings on General made no ${forbidden} request`);
    await close();
    const admin = await open({admin: "1"});
    assert.deepEqual(await railTabs(admin.page), [...TABS, "admin"], "admin tab appears for a multi-account admin");
    const adminIcon = await admin.page.locator('.settings-tabs [data-tab="admin"] svg').getAttribute("data-tofi-icon");
    assert.equal(adminIcon, ICONS.admin);
    await admin.close();
    console.log("PASS rail: nine tabs in order, admin only for a multi-account admin, unique icons, badge from the saved list only");
  }

  // ---- 2. Each tab renders its heading and description; 3. deep links; 6. dirty draft.
  {
    const {page, close} = await open({admin: "1"});
    const markers = {general: "Language and region", connections: "Add", skills: "research-assistant", approvals: "Reviewer", models: "Defaults for new Bots", computer: null, keys: null, usage: null, advanced: "Danger zone", admin: null};
    for (const id of [...TABS, "admin"]) {
      await page.locator(`.settings-tabs [data-tab="${id}"]`).click();
      await page.waitForFunction(tab => document.querySelector(`.settings-tabs [aria-current="page"]`)?.dataset.tab === tab, id);
      const heading = (await page.locator(".settings-content .settings-page-title h2").textContent()).trim();
      const description = (await page.locator(".settings-content .settings-page-title p").textContent()).trim();
      assert.equal(heading, NAMES[id], `${id} heading`);
      assert.equal(description, catalogs.en.shell.page[id].description, `${id} description`);
      const body = page.locator(`.settings-page-body[data-page="${id}"]`);
      assert.equal(await body.isVisible(), true, `${id} page visible`);
      await page.waitForFunction(tab => (document.querySelector(`.settings-page-body[data-page="${tab}"]`)?.innerText ?? "").trim().length > 20, id, {timeout: 15000});
      if (markers[id]) await body.getByText(markers[id], {exact: false}).first().waitFor({timeout: 15000});
      assert.equal(await page.locator(".settings-page-body:not([hidden]) .settings-save-bar").count(), 0);
    }
    // The Models page: both columns, Dictation copy from i18n, no AutoReview section, reasoning as a segmented control.
    await page.locator('.settings-tabs [data-tab="models"]').click();
    const models = page.locator('.settings-page-body[data-page="models"]');
    await models.getByText("Dictation", {exact: true}).first().waitFor();
    await models.getByText("Cheaper, good for everyday use.", {exact: false}).waitFor();
    await models.getByText("$0.003 / min", {exact: false}).waitFor();
    assert.equal(await models.getByText("Approval mode for this account").count(), 0, "AutoReview left the Models page");
    assert.equal(await models.locator(".segmented").count(), 1, "the selected model's three reasoning values render as a segmented control");
    assert.equal(await models.locator(".segmented button").count(), 3);
    assert.equal(await models.locator(".settings-two").evaluate(element => getComputedStyle(element).gridTemplateColumns.split(" ").length), 2, "two columns at 1440");
    // Deep links open the tab.
    await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-tool-settings")));
    await page.waitForFunction(() => document.querySelector('.settings-tabs [aria-current="page"]')?.dataset.tab === "connections");
    await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-codex-settings")));
    await page.waitForFunction(() => document.querySelector('.settings-tabs [aria-current="page"]')?.dataset.tab === "models");
    // The Connections page keeps the existing panel.
    await page.locator('.settings-tabs [data-tab="connections"]').click();
    await page.locator('.settings-page-body[data-page="connections"] .mcp-service').first().waitFor();
    // A dirty Timezone draft blocks switching away behind the leave dialog.
    await page.locator('.settings-tabs [data-tab="general"]').click();
    const zone = page.locator('.settings-page-body[data-page="general"] .timezone-setting select').last();
    await zone.waitFor();
    await zone.selectOption("Europe/Berlin");
    await page.locator(".settings-save-bar").waitFor();
    await page.locator('.settings-tabs [data-tab="models"]').click();
    await page.locator("dialog.settings-leave-dialog[open]").waitFor();
    assert.equal(await selectedTab(page), "general", "still on General while the dialog is open");
    await page.getByRole("button", {name: "Keep editing"}).click();
    assert.equal(await page.locator("dialog.settings-leave-dialog[open]").count(), 0);
    assert.equal(await selectedTab(page), "general");
    await page.locator('.settings-tabs [data-tab="models"]').click();
    await page.locator("dialog.settings-leave-dialog").getByRole("button", {name: "Discard changes"}).click();
    await page.waitForFunction(() => document.querySelector('.settings-tabs [aria-current="page"]')?.dataset.tab === "models");
    await close();
    console.log("PASS pages: heading and description for all ten tabs, deep links, Models layout, dirty-draft leave dialog");
  }

  // ---- 3b. Unknown dictation model shows no copy.
  {
    const {page, close} = await open({tab: "models", dict: "unknown"});
    const body = page.locator('.settings-page-body[data-page="models"]');
    await body.getByText("Mystery Transcribe").first().waitFor({state: "attached"});
    await page.waitForTimeout(300);
    assert.equal(await body.getByText("API reference price").count(), 0, "unknown dictation model shows no price");
    assert.equal(await body.locator(".dictation-settings .settings-card-note").count(), 0, "unknown dictation model shows no hint text");
    await close();
    console.log("PASS dictation: an unknown model id shows no hint or price");
  }

  // ---- 4. Mobile: no select, home list -> Models page -> back -> home.
  {
    const {page, close} = await open({}, {width: 390, height: 844});
    assert.equal(await page.locator("#settings-category").count(), 0, "no #settings-category on mobile");
    await settle(page);
    assert.equal(await page.locator(".settings-shell").getAttribute("data-view"), "home");
    assert.equal(await page.locator(".settings-mhome").isVisible(), true);
    assert.equal(await page.locator(".settings-content").evaluate(element => getComputedStyle(element).visibility), "hidden", "page is parked off-screen on the home list");
    const hints = await page.$$eval(".settings-mhome .settings-mrow", rows => Object.fromEntries(rows.map(row => [row.dataset.tab, row.querySelector(".settings-mhint")?.textContent ?? ""])));
    assert.equal(hints.models, "Codex", "Models hint is the provider of the global model");
    assert.equal(hints.keys, "2", "Keys hint is the number of variables");
    assert.ok(hints.computer.length > 0, "Computer hint is the computer state");
    assert.equal(hints.usage, "", "Usage has no hint in Phase 1");
    assert.deepEqual(await page.$$eval(".settings-mhome .settings-mgroup>p", items => items.map(item => item.textContent)), ["Bots can use", "Workspace", "System"]);
    await page.locator('.settings-mhome [data-tab="models"]').click();
    await settle(page);
    assert.equal(await page.locator(".settings-shell").getAttribute("data-view"), "page");
    assert.equal((await page.locator(".settings-content .settings-page-title h2").textContent()).trim(), "Models");
    assert.equal(await page.locator(".settings-content").evaluate(element => Math.round(element.getBoundingClientRect().x)), 0, "the page has pushed in");
    assert.equal(await page.locator('.settings-page-body[data-page="models"]').isVisible(), true);
    await page.locator(".settings-back").click();
    await settle(page);
    assert.equal(await page.locator(".settings-shell").getAttribute("data-view"), "home");
    assert.equal(await page.locator(".settings-mhome").evaluate(element => Math.round(element.getBoundingClientRect().x)), 0);
    // A desktop-style deep link opens the page directly.
    await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-tool-settings")));
    await settle(page);
    assert.equal(await page.locator(".settings-shell").getAttribute("data-view"), "page");
    assert.equal((await page.locator(".settings-content .settings-page-title h2").textContent()).trim(), "Connections");
    await close();
    console.log("PASS mobile: no select; home list -> Models -> back -> home; hints; deep link lands on its page");
  }

  // ---- 5. prefers-reduced-motion: nothing runs on page switch. (And the same probe sees motion when allowed.)
  {
    const running = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve(document.getAnimations().filter(animation => (animation instanceof CSSAnimation || animation instanceof CSSTransition) && animation.playState === "running").map(animation => `${animation.constructor.name}:${animation.animationName ?? animation.transitionProperty}`))))));
    const calm = await open({}, {reduced: true});
    await calm.page.waitForTimeout(900);
    for (const id of ["models", "usage", "general", "advanced"]) {
      await calm.page.locator(`.settings-tabs [data-tab="${id}"]`).click();
      const active = (await running(calm.page)).filter(name => !/loading|spin/i.test(name));
      assert.deepEqual(active, [], `reduced motion: no CSS animation or transition runs after switching to ${id}`);
    }
    await calm.close();
    const lively = await open({});
    await lively.page.waitForTimeout(900);
    await lively.page.locator('.settings-tabs [data-tab="models"]').click();
    const names = await running(lively.page);
    assert.ok(names.some(name => name.includes("settings-page-in")), `the page fade-in runs when motion is allowed (saw ${names.join(", ") || "nothing"})`);
    await lively.close();
    console.log("PASS motion: reduced motion runs no animations or transitions on page switch; full motion fades the page in");
  }

  // ---- 7. German labels fit the rail: no ellipsis clipping, at most two lines.
  {
    const {page, close} = await open({lang: "de", admin: "1"});
    await page.waitForTimeout(500);
    const report = await page.$$eval(".settings-tabs .settings-nav-label", labels => labels.map(label => {
      const style = getComputedStyle(label), box = label.getBoundingClientRect(), row = label.closest("button").getBoundingClientRect();
      const lineHeight = parseFloat(style.lineHeight);
      return {text: label.textContent, ellipsis: style.textOverflow === "ellipsis", clipped: label.scrollWidth > label.clientWidth, lines: Math.round(box.height / lineHeight), inside: box.right <= row.right + 0.5 && box.left >= row.left - 0.5};
    }));
    assert.equal(report.length, 10);
    for (const item of report) {
      assert.equal(item.ellipsis, false, `${item.text}: no text-overflow ellipsis`);
      assert.equal(item.clipped, false, `${item.text}: scrollWidth <= clientWidth`);
      assert.ok(item.lines <= 2, `${item.text}: ${item.lines} lines`);
      assert.equal(item.inside, true, `${item.text}: inside its rail item`);
    }
    for (const text of ["Verbindungen", "Genehmigungen", "Schlüssel & Geheimnisse"]) assert.ok(report.some(item => item.text === text), `German rail shows ${text}`);
    const rail = await page.locator(".settings-tabs").evaluate(element => element.getBoundingClientRect().width);
    assert.equal(Math.round(rail), 248, "rail is 248px wide");
    await close();
    console.log("PASS german: Verbindungen, Genehmigungen and Schlüssel & Geheimnisse fit the 248px rail in at most two lines, no clipping");
  }

  // ---- 7b. Skill access (Phase 2, 3.5 #2): change access to one Bot; the card shows "1 of 3 Bots"; reload keeps it.
  {
    skillAccess.clear(); accessWrites.length = 0;
    const card = (page, name) => page.locator('.settings-page-body[data-page="skills"] .extension-card').filter({hasText: name});
    const summary = (page, name) => card(page, name).locator(".skill-access-summary");
    const {page, close} = await open({tab: "skills"});
    await card(page, "research-assistant").waitFor();
    assert.equal((await summary(page, "research-assistant").textContent()).trim(), "All Bots", "default access is All Bots");
    assert.equal((await summary(page, "weekly-report").textContent()).trim(), "All Bots");
    assert.equal(await card(page, "research-assistant").getByText("Who can use it", {exact: true}).count(), 1);
    // Escape closes the sheet without writing.
    await card(page, "research-assistant").getByRole("button", {name: "Change"}).click();
    const sheet = page.getByRole("dialog", {name: "Who can use research-assistant"});
    await sheet.waitFor();
    assert.equal(await sheet.getByRole("radio", {name: /^All Bots/}).isChecked(), true);
    assert.equal(await sheet.getByRole("checkbox").count(), 3, "checklist lists the three non-archived Bots");
    assert.equal(await sheet.getByRole("checkbox").first().isDisabled(), true, "checklist is disabled while All Bots is chosen");
    await page.keyboard.press("Escape");
    await sheet.waitFor({state: "detached"});
    assert.deepEqual(accessWrites, [], "closing the sheet writes nothing");
    // Only selected Bots needs at least one Bot.
    await card(page, "research-assistant").getByRole("button", {name: "Change"}).click();
    await sheet.waitFor();
    await sheet.getByRole("radio", {name: /^Only selected Bots/}).check();
    assert.equal(await sheet.getByRole("button", {name: "Save"}).isDisabled(), true, "Save needs a Bot");
    await sheet.getByRole("checkbox", {name: "Research Bot"}).check();
    await sheet.getByRole("button", {name: "Save"}).click();
    await sheet.waitFor({state: "detached"});
    assert.deepEqual(accessWrites, ['PUT /api/extensions/skills/research-assistant/access {"mode":"selected","bot_ids":["bot_research"]}']);
    await page.waitForFunction(() => document.querySelector('.settings-page-body[data-page="skills"]')?.innerText.includes("1 of 3 Bots"));
    assert.equal((await summary(page, "research-assistant").textContent()).trim(), "1 of 3 Bots");
    assert.equal((await summary(page, "weekly-report").textContent()).trim(), "All Bots", "the other skill is untouched");
    await page.reload();
    await card(page, "research-assistant").waitFor();
    await page.waitForFunction(() => document.querySelector('.settings-page-body[data-page="skills"]')?.innerText.includes("1 of 3 Bots"));
    assert.equal((await summary(page, "research-assistant").textContent()).trim(), "1 of 3 Bots", "reload keeps the restriction");
    // Back to All Bots.
    await card(page, "research-assistant").getByRole("button", {name: "Change"}).click();
    await sheet.waitFor();
    assert.equal(await sheet.getByRole("checkbox", {name: "Research Bot"}).isChecked(), true, "the sheet reopens with the saved selection");
    await sheet.getByRole("radio", {name: /^All Bots/}).check();
    await sheet.getByRole("button", {name: "Save"}).click();
    await sheet.waitFor({state: "detached"});
    assert.equal(accessWrites.at(-1), 'PUT /api/extensions/skills/research-assistant/access {"mode":"all"}');
    await page.waitForFunction(() => !document.querySelector('.settings-page-body[data-page="skills"]')?.innerText.includes("1 of 3 Bots"));
    await close();
    for (const lang of ["zh-CN", "de"]) {
      const view = await open({tab: "skills", lang});
      await card(view.page, "research-assistant").waitFor();
      const text = (await summary(view.page, "research-assistant").textContent()).trim();
      assert.notEqual(text, "All Bots", `${lang}: the access summary is translated`);
      await view.close();
    }
    console.log("PASS skill access: All Bots by default, sheet needs a Bot, saving shows 1 of 3 Bots, reload keeps it, back to All Bots, translated");
  }

  // ---- 8. Screenshots.
  if (shots) {
    let count = 0;
    for (const theme of ["light", "dark"]) {
      const desktop = await open({admin: "1"}, {theme});
      for (const id of [...TABS, "admin"]) {
        await desktop.page.locator(`.settings-tabs [data-tab="${id}"]`).click();
        await desktop.page.waitForFunction(tab => (document.querySelector(`.settings-page-body[data-page="${tab}"]`)?.innerText ?? "").trim().length > 20, id);
        await desktop.page.waitForTimeout(id === "usage" ? 1800 : 900);
        await desktop.page.screenshot({path: join(shots, `desktop-${theme}-${id}.png`)});
        count++;
        if (id === "advanced" || id === "models") {
          // These pages scroll: also keep the lower half (Debug and Danger zone; the rest of Models).
          await desktop.page.locator(`.settings-page-body[data-page="${id}"]`).evaluate(element => { element.scrollTop = element.scrollHeight; });
          await desktop.page.waitForTimeout(300);
          await desktop.page.screenshot({path: join(shots, `desktop-${theme}-${id}-bottom.png`)});
          count++;
        }
      }
      await desktop.close();
      const mobile = await open({admin: "1"}, {width: 390, height: 844, theme});
      await mobile.page.waitForTimeout(900);
      await mobile.page.screenshot({path: join(shots, `mobile-${theme}-home.png`)});
      count++;
      for (const id of [...TABS, "admin"]) {
        await mobile.page.locator(`.settings-mhome [data-tab="${id}"]`).first().scrollIntoViewIfNeeded().catch(() => {});
        await mobile.page.locator(`.settings-mhome [data-tab="${id}"]`).first().click();
        await mobile.page.waitForFunction(tab => (document.querySelector(`.settings-page-body[data-page="${tab}"]`)?.innerText ?? "").trim().length > 20, id);
        await mobile.page.waitForTimeout(id === "usage" ? 1800 : 1000);
        await mobile.page.screenshot({path: join(shots, `mobile-${theme}-${id}.png`)});
        count++;
        await mobile.page.locator(".settings-back").click();
        await settle(mobile.page);
      }
      await mobile.close();
    }
    console.log(`PASS screenshots: ${count} saved to ${shots}`);
  }

  // ---- 9. Phase 2 screenshots: Skills page with an "All Bots" card, a "1 of 3 Bots" card and the open sheet.
  if (process.env.SKILL_ACCESS_SHOTS) {
    const dir = process.env.SKILL_ACCESS_SHOTS;
    await mkdir(dir, {recursive: true});
    let count = 0;
    for (const theme of ["light", "dark"]) for (const [label, size] of [["desktop", {width: 1440, height: 900}], ["mobile", {width: 390, height: 844}]]) {
      skillAccess.clear();
      skillAccess.set("research-assistant", {mode: "selected", bot_ids: ["bot_research"]});
      const {page, close} = await open({tab: "skills"}, {...size, theme});
      if (label === "mobile") { await page.locator(".settings-mhome [data-tab=\"skills\"]").click().catch(() => {}); }
      const cards = page.locator('.settings-page-body[data-page="skills"] .extension-card');
      await cards.first().waitFor();
      await page.waitForFunction(() => document.querySelector('.settings-page-body[data-page="skills"]')?.innerText.includes("1 of 3 Bots"));
      await page.waitForTimeout(900);
      await page.screenshot({path: join(dir, `skills-${label}-${theme}.png`)}); count++;
      await cards.filter({hasText: "weekly-report"}).getByRole("button", {name: "Change"}).click();
      await page.locator(".sheet").waitFor();
      await page.getByRole("radio", {name: /^Only selected Bots/}).check();
      await page.getByRole("checkbox", {name: "Inbox Bot"}).check();
      await page.waitForTimeout(500);
      await page.screenshot({path: join(dir, `skills-${label}-${theme}-sheet.png`)}); count++;
      await close();
    }
    console.log(`PASS skill access screenshots: ${count} saved to ${dir}`);
  }

  assert.deepEqual(unexpected, [], `unexpected requests or page errors: ${unexpected.join("; ")}`);
  assert.deepEqual(writes, [], `no writes expected: ${writes.join("; ")}`);
  console.log("PASS no unstubbed /api request, no write, no page error");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
