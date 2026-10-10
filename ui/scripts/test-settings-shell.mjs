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
import {UpdateBanner} from '../src/UpdateNotice';
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
 return <div className="workspace"><UpdateBanner/><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true">{!closed&&<SettingsShell tab={tab} onTab={setTab} onClose={()=>setClosed(true)} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,notifications:<NotificationSetting/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/>}</aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const requested = [], unexpected = [], writes = [], accessWrites = [];
  const skillAccess = new Map();
  const updateState = {latest: "v0.1.0"};
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
    if (path === "/api/system/update") {
      if (query.get("role") === "user") return undefined;
      return {current: "v0.1.0", latest: updateState.latest, update_available: updateState.latest !== "v0.1.0", notes_url: `https://github.com/example/tofibot/releases/tag/${updateState.latest}`, checked_at: "2026-10-09T09:30:00Z", auto_update: query.get("auto") === "patch" ? "patch" : "off"};
    }
    if (path === "/api/auth/session") return query.get("role") === "user"
      ? {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_user", username: "Sam Example", email: "sam@example.test", role: "user"}}
      : query.get("admin") === "0"
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
    return undefined;
  };
  // ---- Admin console stub: the real response shapes, mutable so flows can be driven end to end.
  const GIB = 2 ** 30;
  const adminState = {};
  const adminWrites = [];
  function resetAdmin() {
    adminState.accounts = [
      {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin", disabled: false, must_change_password: false, deleting: false},
      {id: "acct_two", username: "Sam Example", email: "sam@example.test", role: "user", disabled: false, must_change_password: false, deleting: false},
      {id: "acct_three", username: "Lee Example", email: "lee@example.test", role: "user", disabled: true, must_change_password: false, deleting: false},
      {id: "acct_four", username: "Max Example", email: "acct_four@account.invalid", role: "user", disabled: true, must_change_password: false, deleting: true, delete_step: "computer", delete_error: "computer_unavailable"},
    ];
    adminState.disks = {acct_sample: [16, "ready"], acct_two: [8, "ready"], acct_three: [8, "disabled"], acct_four: [8, "disabled"]};
    adminState.exports = [
      {id: "exp_old", username: "Kim Example", email: "kim@example.test", created_at: Math.floor(Date.parse("2026-09-30T10:00:00Z") / 1000), expires_at: Math.floor(Date.parse("2026-10-30T10:00:00Z") / 1000), size: 3_400_000, link_path: "/exports/" + "A".repeat(43), passphrase_pending: false},
      {id: "exp_new", username: "Joy Example", email: "", created_at: Math.floor(Date.parse("2026-10-08T10:00:00Z") / 1000), expires_at: Math.floor(Date.parse("2026-11-07T10:00:00Z") / 1000), size: 820_000, link_path: "/exports/" + "B".repeat(43), passphrase_pending: true},
    ];
    adminState.failDelete = 0;
    adminState.lastDeleted = null;
    adminWrites.length = 0;
  }
  resetAdmin();
  function adminStub(url, method, postData) {
    const path = url.pathname, json = (status, body) => ({status, body});
    const bodyOf = () => { try { return JSON.parse(postData || "{}"); } catch { return {}; } };
    if (method !== "GET") adminWrites.push(`${method} ${path} ${postData ?? ""}`.trim());
    if (path === "/api/admin/accounts" && method === "GET") return json(200, adminState.accounts);
    if (path === "/api/admin/accounts" && method === "POST") {
      const input = bodyOf();
      adminState.accounts.push({id: "acct_new", username: input.username, email: input.email || "acct_new@account.invalid", role: "user", disabled: false, must_change_password: true, deleting: false});
      adminState.disks.acct_new = [8, "reserved"];
      return json(201, adminState.accounts.at(-1));
    }
    if (path === "/api/admin/capacity") {
      const accounts = adminState.accounts.filter(a => adminState.disks[a.id]).map(a => ({account_id: a.id, quota_bytes: adminState.disks[a.id][0] * GIB, logical_bytes: adminState.disks[a.id][0] * GIB, state: adminState.disks[a.id][1], pending_quota: false}));
      return json(200, {total_bytes: 500 * GIB, available_bytes: 300 * GIB, allocated_bytes: 100 * GIB, promised_bytes: accounts.reduce((sum, a) => sum + a.quota_bytes, 0), admission_remaining_bytes: 120 * GIB, warning: false, accounts});
    }
    if (path === "/api/admin/deleted-exports" && method === "GET") return json(200, adminState.exports.map(({passphrase, ...rest}) => rest));
    let match = /^\/api\/admin\/deleted-exports\/([^/]+)(?:\/(passphrase|ack))?$/.exec(path);
    if (match) {
      const item = adminState.exports.find(x => x.id === match[1]);
      if (!item) return json(404, {error: {code: "not_found", message: "export not found"}});
      if (match[2] === "passphrase") return item.passphrase_pending ? json(200, {passphrase: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567"}) : json(409, {error: {code: "passphrase_unavailable", message: "gone"}});
      if (match[2] === "ack") { item.passphrase_pending = false; return json(200, {id: item.id, passphrase_pending: false}); }
      if (method === "DELETE") { adminState.exports = adminState.exports.filter(x => x !== item); return json(200, {id: item.id, deleted: true}); }
    }
    match = /^\/api\/admin\/accounts\/([^/]+)\/quota$/.exec(path);
    if (match) {
      adminState.disks[match[1]][0] = bodyOf().quota_gib;
      return json(200, {quota_bytes: bodyOf().quota_gib * GIB, applied: true});
    }
    match = /^\/api\/admin\/accounts\/([^/]+)$/.exec(path);
    if (match) {
      const account = adminState.accounts.find(a => a.id === match[1]);
      if (!account) return json(404, {error: {code: "not_found", message: "account not found"}});
      if (method === "PATCH") {
        const input = bodyOf();
        if (account.deleting) return json(409, {error: {code: "account_deleting", message: "account is being deleted"}});
        if (input.role) account.role = input.role;
        if (typeof input.disabled === "boolean") account.disabled = input.disabled;
        if (input.initial_password) account.must_change_password = true;
        return json(200, account);
      }
      if (method === "DELETE") {
        const input = bodyOf();
        if (!account.disabled) return json(409, {error: {code: "account_not_deactivated", message: "deactivate first"}});
        if (input.confirm_username !== account.username) return json(400, {error: {code: "confirmation_mismatch", message: "mismatch"}});
        if (adminState.failDelete > 0) {
          adminState.failDelete--;
          account.deleting = true; account.delete_step = "computer"; account.delete_error = "computer_unavailable";
          return json(503, {error: {code: "account_delete_failed", message: "stopped"}});
        }
        adminState.accounts = adminState.accounts.filter(a => a !== account);
        delete adminState.disks[account.id];
        const created = Math.floor(Date.parse("2026-10-09T12:00:00Z") / 1000);
        const exported = {id: "exp_" + account.id, username: account.username, email: account.email, created_at: created, expires_at: created + 30 * 86400, size: 1_250_000, link_path: "/exports/" + "C".repeat(43), passphrase: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567", passphrase_pending: true};
        adminState.exports.unshift({...exported, passphrase: undefined});
        adminState.lastDeleted = account.id;
        return json(200, {id: account.id, deleted: true, export: exported});
      }
    }
    return json(404, {error: {code: "not_found", message: "not stubbed"}});
  }
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
      if (url.pathname.startsWith("/api/admin/")) {
        const answer = adminStub(url, method, request.postData());
        await route.fulfill({status: answer.status, contentType: "application/json", body: JSON.stringify(answer.body)});
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

  // ---- 7c. Admin console: list, detail, role, disk, password, deactivation and the delete flow (stubbed API).
  {
    const adminTab = (page, mobile) => mobile ? page.locator('.settings-mhome [data-tab="admin"]').click() : Promise.resolve();
    const openAdmin = async (params = {}, size = {}) => {
      resetAdmin();
      const view = await open({admin: "1", tab: "admin", ...params}, size);
      if (size.width && size.width < 700) await adminTab(view.page, true);
      const body = view.page.locator('.settings-page-body[data-page="admin"]');
      await body.locator(".admin-row").first().waitFor({timeout: 15000});
      return {...view, body};
    };
    const text = async locator => (await locator.innerText()).replace(/\s+/g, " ").trim();
    const { page, close, body } = await openAdmin();
    // List: capacity summary, rows with avatar, role, status and computer summary.
    assert.deepEqual((await body.locator(".admin-capacity dd").allInnerTexts()).map(item => item.replace(/\s+/g, " ").trim()), ["500 GiB", "40 GiB", "120 GiB"], "host capacity: total, allocated, free");
    assert.equal(await body.locator(".admin-row").count(), 4);
    assert.equal(await body.getByRole("button", {name: "Add account"}).count(), 1, "Add account is a primary action on the list");
    const row = id => body.locator(`.admin-row[data-account="${id}"]`);
    assert.match(await text(row("acct_sample")), /A.*Ada Sample.*You.*Admin.*Active.*Ready · 16 GiB/);
    assert.match(await text(row("acct_two")), /Sam Example.*Member.*Active.*Ready · 8 GiB/);
    assert.match(await text(row("acct_three")), /Lee Example.*Member.*Deactivated.*Stopped · 8 GiB/);
    assert.match(await text(row("acct_four")), /Max Example.*No email set.*Deletion stopped/);
    assert.equal(await row("acct_two").locator(".admin-avatar").innerText(), "S");
    // The exports section: link copy for both, "Show passphrase" only while it was never acknowledged.
    assert.equal(await body.locator(".admin-export").count(), 2);
    assert.match(await text(body.locator('[data-export="exp_old"]')), /Kim Example.*Created.*expires.*3\.2 MB.*shown once and isn't stored/);
    assert.equal(await body.locator('[data-export="exp_old"]').getByRole("button", {name: "Show passphrase"}).count(), 0);
    assert.equal(await body.locator('[data-export="exp_new"]').getByRole("button", {name: "Show passphrase"}).count(), 1);
    // No bare internal ids anywhere on the list.
    assert.ok(!/acct_(sample|two|three|four)/.test(await body.innerText()), "no raw ids on the list");

    // Detail for an ordinary member: Role, Computer, Sign-in, Danger zone.
    await row("acct_two").click();
    const detail = body.locator('[data-testid="admin-detail"]');
    await detail.waitFor();
    assert.equal(await body.locator(".admin-row").count(), 0, "the list is replaced by the detail");
    for (const title of ["Role", "Computer", "Sign-in", "Danger zone"]) assert.ok(await detail.getByText(title, {exact: true}).first().isVisible(), `section ${title}`);
    const segmented = detail.getByRole("radiogroup", {name: "Role"});
    assert.equal(await segmented.getByRole("radio", {name: "Member"}).getAttribute("aria-checked"), "true");
    assert.equal(await segmented.getByRole("radio", {name: "Admin"}).isDisabled(), false, "another member's role can be changed");
    const deleteButton = detail.getByRole("button", {name: "Delete account…"});
    assert.equal(await deleteButton.isDisabled(), true, "Delete is disabled until the account is deactivated");
    assert.match(await text(detail), /Deactivate the account first\. Only a deactivated account can be deleted\./);
    // Disk: only sizes of at least the current one; sizes that do not fit the free capacity are disabled and say why.
    const select = detail.getByLabel("Disk quota");
    assert.deepEqual(await select.locator("option").evaluateAll(options => options.map(option => option.value)), ["8", "16", "32", "64", "128", "256", "512", "1024"]);
    assert.deepEqual(await select.locator("option:disabled").evaluateAll(options => options.map(option => option.value)), ["256", "512", "1024"], "120 GiB free: growing by more than that is disabled");
    assert.match(await text(select.locator("option").nth(0)), /8 GiB · current/);
    assert.match(await text(detail), /Changing the quota stops the computer/);
    const save = detail.getByRole("button", {name: /^Save .* GiB and stop computer$/});
    assert.equal(await save.isDisabled(), true, "nothing to save yet");
    await select.selectOption("32");
    assert.equal((await save.textContent()).trim(), "Save 32 GiB and stop computer", "the button names the consequence");
    adminWrites.length = 0;
    await save.click();
    await detail.getByText("Saved. The computer was stopped and will restart on demand.").waitFor();
    assert.deepEqual(adminWrites, ['PATCH /api/admin/accounts/acct_two/quota {"quota_gib":32}']);
    assert.equal(await select.inputValue(), "32");
    // Role change.
    adminWrites.length = 0;
    await segmented.getByRole("radio", {name: "Admin"}).click();
    await page.waitForFunction(() => document.querySelector('[data-testid="admin-detail"] [role="radio"][aria-checked="true"]')?.textContent === "Admin");
    assert.deepEqual(adminWrites, ['PATCH /api/admin/accounts/acct_two {"role":"admin"}']);
    await segmented.getByRole("radio", {name: "Member"}).click();
    await page.waitForFunction(() => document.querySelector('[data-testid="admin-detail"] [role="radio"][aria-checked="true"]')?.textContent === "Member");
    // Reset password: confirmation, then the one-time password shown once with Copy.
    adminWrites.length = 0;
    await detail.getByRole("button", {name: "Reset password"}).click();
    const reset = page.getByRole("dialog", {name: "Reset password for Sam Example?"});
    await reset.waitFor();
    await reset.getByRole("button", {name: "Reset and sign out"}).click();
    const once = page.getByRole("dialog", {name: "One-time password"});
    await once.waitFor();
    const password = await once.locator("input").inputValue();
    assert.match(password, /^[A-Za-z0-9]{20}$/, "a 20 character one-time password");
    assert.equal(adminWrites.length, 1);
    assert.equal(JSON.parse(adminWrites[0].split(" ").slice(2).join(" ")).initial_password, password, "the shown password is the one that was set");
    assert.ok(await once.getByRole("button", {name: "Copy"}).isVisible());
    await page.keyboard.press("Escape");
    assert.equal(await once.count(), 1, "the one-time password sheet only closes with its button");
    await once.getByRole("button", {name: "I've copied it"}).click();
    await once.waitFor({state: "detached"});
    // Deactivate / reactivate.
    adminWrites.length = 0;
    await detail.getByRole("button", {name: "Deactivate", exact: true}).click();
    await detail.getByRole("button", {name: "Reactivate"}).waitFor();
    assert.deepEqual(adminWrites, ['PATCH /api/admin/accounts/acct_two {"disabled":true}']);
    assert.equal(await deleteButton.isDisabled(), false, "a deactivated account can be deleted");
    await detail.getByRole("button", {name: "Reactivate"}).click();
    await detail.getByRole("button", {name: "Deactivate", exact: true}).waitFor();

    // Your own account: nothing destructive is offered; each dead end is explained.
    await detail.getByRole("button", {name: "Accounts"}).click();
    await row("acct_sample").click();
    await detail.waitFor();
    const mine = detail.getByRole("radiogroup", {name: "Role"});
    assert.equal(await mine.getByRole("radio", {name: "Member"}).isDisabled(), true);
    assert.equal(await mine.getByRole("radio", {name: "Admin"}).isDisabled(), true);
    assert.equal(await detail.getByRole("button", {name: "Deactivate", exact: true}).isDisabled(), true);
    assert.equal(await detail.getByRole("button", {name: "Delete account…"}).isDisabled(), true);
    const mineText = await text(detail);
    for (const reason of ["You can't change your own role.", "You can't deactivate the account you are signed in with.", "You can't delete the account you are signed in with."]) assert.ok(mineText.includes(reason), reason);
    await detail.getByRole("button", {name: "Accounts"}).click();

    // Delete: only a deactivated account; the sheet lists what goes and needs the exact username.
    await row("acct_three").click();
    await detail.waitFor();
    adminWrites.length = 0;
    await detail.getByRole("button", {name: "Delete account…"}).click();
    await page.getByRole("dialog", {name: "Delete Lee Example?"}).waitFor();
    const sheet = page.locator(".admin-delete-sheet");
    const sheetText = await text(sheet);
    for (const line of ["All Bots and their settings", "All conversations", "All memory", "Files and attachments", "The computer and its disk — 8 GiB will be freed", "This can't be undone.", "encrypted copy", "30 days"]) assert.ok(sheetText.includes(line), `delete sheet says: ${line}`);
    const confirm = sheet.getByRole("button", {name: "Delete account", exact: true});
    assert.equal(await confirm.isDisabled(), true);
    const field = sheet.getByLabel("Type Lee Example to confirm");
    await field.fill("lee example");
    assert.equal(await confirm.isDisabled(), true, "the username must match exactly, case included");
    await field.fill("Lee Example");
    assert.equal(await confirm.isDisabled(), false);
    // First attempt stops part-way: the account stays Deleting, the sheet shows where and offers Retry.
    adminState.failDelete = 1;
    await confirm.click();
    await sheet.getByText("Deletion stopped at: Removing the computer and freeing its disk").waitFor();
    assert.match(await text(sheet), /The computer service didn't confirm the removal\./);
    assert.deepEqual(adminWrites, ['DELETE /api/admin/accounts/acct_three {"confirm_username":"Lee Example"}']);
    const retry = sheet.getByRole("button", {name: "Retry deletion"});
    assert.equal(await retry.isEnabled(), true);
    await retry.click();
    // Success: the result sheet with link and passphrase separately, a message to send, closes only on acknowledgement.
    const result = page.getByRole("dialog", {name: "Lee Example was deleted"});
    await result.waitFor();
    assert.equal(await result.getByLabel("Download link").inputValue(), `${new URL(page.url()).origin}/exports/${"C".repeat(43)}`);
    assert.equal(await result.getByLabel("Passphrase").inputValue(), "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567");
    assert.equal(await result.getByRole("button", {name: "Copy", exact: true}).count(), 2, "link and passphrase each have Copy");
    const message = await result.getByLabel("Message to send").inputValue();
    assert.match(message, /^Your TOFI account was closed\. Download your Bots: http.*\/exports\/C+ \(passphrase sent separately\), available until November 8, 2026\. Import it in Tofi: Settings → Advanced → Data transfer, then enter the passphrase\.$/);
    assert.ok(!message.includes("ABCD-EFGH"), "the message never carries the passphrase");
    await page.keyboard.press("Escape");
    assert.equal(await result.count(), 1);
    adminWrites.length = 0;
    await result.getByRole("button", {name: "I've saved the passphrase"}).click();
    await result.waitFor({state: "detached"});
    assert.deepEqual(adminWrites, ["POST /api/admin/deleted-exports/exp_acct_three/ack"]);
    await body.locator(".admin-row").first().waitFor();
    assert.equal(await body.locator(".admin-row").count(), 3, "the deleted account left the list");
    assert.match(await text(body.locator('[data-export="exp_acct_three"]')), /Lee Example.*1\.2 MB.*isn't stored/);
    // A stopped deletion is visible on the account and resumable from its detail.
    await row("acct_four").click();
    await detail.waitFor();
    const banner = detail.locator(".settings-banner");
    assert.match(await text(banner), /Deletion stopped at: Removing the computer and freeing its disk.*The computer service didn't confirm the removal\./);
    assert.equal(await detail.getByRole("button", {name: "Reactivate"}).isDisabled(), true, "a deleting account cannot be reactivated");
    adminWrites.length = 0;
    await banner.getByRole("button", {name: "Retry deletion"}).click();
    const resume = page.getByRole("dialog", {name: "Deleting Max Example"});
    await resume.waitFor();
    assert.equal(await resume.getByLabel(/Type/).count(), 0, "resuming needs no second confirmation");
    await resume.getByRole("button", {name: "Retry deletion"}).click();
    await page.getByRole("dialog", {name: "Max Example was deleted"}).getByRole("button", {name: "I've saved the passphrase"}).click();
    await body.locator(".admin-row").first().waitFor();
    assert.equal(await body.locator(".admin-row").count(), 2);
    assert.deepEqual(adminWrites.slice(0, 1), ['DELETE /api/admin/accounts/acct_four {"confirm_username":"Max Example"}']);
    // Exports: copy link, show a pending passphrase, delete.
    await body.locator('[data-export="exp_new"]').getByRole("button", {name: "Show passphrase"}).click();
    const shown = page.getByRole("dialog", {name: "Joy Example was deleted"});
    await shown.waitFor();
    await shown.getByLabel("Passphrase").waitFor();
    await page.waitForFunction(() => [...document.querySelectorAll(".admin-result-sheet input")].some(input => input.value.startsWith("ABCD-")));
    await shown.getByRole("button", {name: "I've saved the passphrase"}).click();
    await shown.waitFor({state: "detached"});
    assert.equal(await body.locator('[data-export="exp_new"]').getByRole("button", {name: "Show passphrase"}).count(), 0, "after acknowledgement the passphrase is gone");
    adminWrites.length = 0;
    await body.locator('[data-export="exp_old"]').getByRole("button", {name: "Delete export"}).click();
    await page.getByRole("dialog", {name: "Delete the export of Kim Example?"}).getByRole("button", {name: "Delete export"}).click();
    await page.waitForFunction(() => !document.querySelector('[data-export="exp_old"]'));
    assert.deepEqual(adminWrites, ["DELETE /api/admin/deleted-exports/exp_old"]);
    // Create.
    adminWrites.length = 0;
    await body.getByRole("button", {name: "Add account"}).click();
    const create = page.getByRole("dialog", {name: "Add account"});
    await create.getByLabel("Username").fill("Nia Example");
    await create.getByLabel("Initial password").fill("synthetic-pass-12345");
    await create.getByRole("button", {name: "Create account"}).click();
    await body.locator('.admin-row[data-account="acct_new"]').waitFor();
    assert.match(adminWrites[0], /^POST \/api\/admin\/accounts \{"username":"Nia Example","email":"","password":"synthetic-pass-12345"\}$/);
    assert.match(await text(body.locator('.admin-row[data-account="acct_new"]')), /Awaiting first sign-in.*Not started · 8 GiB/);
    await close();

    // Mobile 390: the list stacks, the detail is a pushed page with a way back, nothing scrolls sideways.
    const mobile = await openAdmin({}, {width: 390, height: 844});
    const overflow = async view => view.page.evaluate(() => { const el = document.querySelector(".settings-page-body:not([hidden])"); return [el.scrollWidth, el.clientWidth, document.documentElement.scrollWidth, innerWidth]; });
    let widths = await overflow(mobile);
    assert.ok(widths[0] <= widths[1] && widths[2] <= widths[3], `list does not scroll sideways ${widths}`);
    await mobile.body.locator('.admin-row[data-account="acct_three"]').click();
    const pushed = mobile.body.locator('[data-testid="admin-detail"]');
    await pushed.waitFor();
    await mobile.page.waitForTimeout(400);
    widths = await overflow(mobile);
    assert.ok(widths[0] <= widths[1] && widths[2] <= widths[3], `detail does not scroll sideways ${widths}`);
    await pushed.getByRole("button", {name: "Delete account…"}).click();
    const mobileSheet = mobile.page.getByRole("dialog", {name: "Delete Lee Example?"});
    await mobileSheet.waitFor();
    const box = await mobileSheet.boundingBox();
    assert.ok(box.x >= 0 && box.x + box.width <= 390, "the delete sheet fits a 390px screen");
    await mobileSheet.getByRole("button", {name: "Cancel"}).click();
    await pushed.getByRole("button", {name: "Accounts"}).click();
    await mobile.body.locator(".admin-row").first().waitFor();
    await mobile.close();

    // Every locale: real words, no catalog key paths leaking, the delete sheet and detail render.
    for (const lang of LANGS.filter(code => code !== "en")) {
      const view = await openAdmin({lang});
      const catalog = catalogs[lang].admin;
      assert.notEqual(catalog.add, catalogs.en.admin.add, `${lang}: Add account is translated`);
      assert.notEqual(catalog.delete.confirm_action, catalogs.en.admin.delete.confirm_action, `${lang}: Delete account is translated`);
      await view.body.locator('.admin-row[data-account="acct_three"]').click();
      await view.body.locator('[data-testid="admin-detail"]').waitFor();
      await view.page.locator(".admin-detail .admin-danger-rows button:not([disabled])").last().click();
      await view.page.locator(".admin-delete-sheet").waitFor();
      const shown = await view.page.evaluate(() => document.querySelector(".settings-page-body:not([hidden])").innerText + " " + (document.querySelector(".admin-delete-sheet")?.innerText ?? ""));
      assert.ok(!/\badmin\.[a-z_]+\.?[a-z_]*/.test(shown), `${lang}: a catalog key path is showing: ${shown.match(/\badmin\.[a-z_.]+/)?.[0]}`);
      assert.ok(shown.includes("8 GiB") || shown.includes("8 Gio"), `${lang}: freed disk size is shown`);
      await view.close();
    }
    console.log("PASS admin console: list with capacity, roles, statuses and exports; detail with role, disk, password, deactivate; own-account guards; delete sheet, stop and retry, result sheet and acknowledgement; mobile push; 7 locales");
  }


  // ---- 7e. Opening a deleted account's export: Advanced -> Data transfer asks for the passphrase (with or without dashes).
  {
    const {page, close} = await open({tab: "advanced"});
    const section = page.locator('.settings-page-body[data-page="advanced"] .portability-section');
    await section.waitFor();
    await section.locator('input[type="file"]').setInputFiles(join(ui, "test-fixtures/server-sealed-export.tofi"));
    const field = section.getByLabel("Decryption passphrase");
    await field.waitFor();
    assert.equal(await section.getByText("server-sealed-export.tofi").count(), 1, "the chosen file name is shown");
    await field.fill("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GGGG-HHHH");
    await section.getByRole("button", {name: "Decrypt and view"}).click();
    await section.getByText("Couldn't decrypt. Check the passphrase, the file version, and whether the file is damaged.").waitFor();
    assert.equal(await section.getByText("Synthetic Helper").count(), 0, "nothing is shown after a wrong passphrase");
    await field.fill("  abcd efgh ijkl mnop qrst uvwx yz23 4567 ");
    await section.getByRole("button", {name: "Decrypt and view"}).click();
    await section.getByText("Synthetic Helper").waitFor();
    assert.equal(await section.getByLabel("Decryption passphrase").count(), 0, "the passphrase prompt is gone once opened");
    assert.ok(await section.getByRole("button", {name: /preview/i}).count() > 0, "the unchanged preview step follows");
    await close();
    const damaged = await open({tab: "advanced"});
    const dsection = damaged.page.locator('.settings-page-body[data-page="advanced"] .portability-section');
    await dsection.waitFor();
    await dsection.locator('input[type="file"]').setInputFiles({name: "broken.tofi", mimeType: "application/json", buffer: Buffer.from(JSON.stringify({format: "tofi.encrypted", version: 1, kdf: "PBKDF2-SHA256", iterations: 1, salt: "AA==", iv: "AA==", ciphertext: "AA=="}))});
    await dsection.getByLabel("Decryption passphrase").fill("ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567");
    await dsection.getByRole("button", {name: "Decrypt and view"}).click();
    await dsection.getByText("This file is damaged or isn't a Tofi export.").waitFor();
    await damaged.close();
    for (const lang of LANGS.filter(code => code !== "en")) {
      assert.notEqual(catalogs[lang].portability.error.file_damaged, catalogs.en.portability.error.file_damaged, `${lang}: file_damaged is translated`);
      const message = catalogs[lang].admin.result.message;
      assert.notEqual(message, catalogs.en.admin.result.message, `${lang}: the message to send is translated`);
      assert.ok(message.includes(catalogs[lang].portability.title) && message.includes(catalogs[lang].shell.page.advanced.name), `${lang}: the message names Advanced and Data transfer as this locale shows them`);
    }
    assert.ok(catalogs.en.admin.result.message.includes("Settings → Advanced → Data transfer, then enter the passphrase."));
    console.log("PASS export import: passphrase prompt, wrong passphrase, with/without dashes, damaged file, preview step follows, message names the path in 7 locales");
  }

  // ---- 7d. Admin console screenshots (ADMIN_SHOTS=<dir>): list, detail, delete sheet, stopped and retry states, result sheet.
  if (process.env.ADMIN_SHOTS) {
    const dir = process.env.ADMIN_SHOTS;
    await mkdir(dir, {recursive: true});
    let count = 0;
    for (const theme of ["light", "dark"]) for (const [label, size] of [["1440", {width: 1440, height: 900}], ["390", {width: 390, height: 844}]]) {
      resetAdmin();
      const view = await open({admin: "1", tab: "admin"}, {...size, theme});
      if (size.width < 700) await view.page.locator('.settings-mhome [data-tab="admin"]').click();
      const body = view.page.locator('.settings-page-body[data-page="admin"]');
      await body.locator(".admin-row").first().waitFor({timeout: 15000});
      const shot = async name => { await view.page.waitForTimeout(450); await view.page.screenshot({path: join(dir, `admin-${label}-${theme}-${name}.png`)}); count++; };
      const bottom = () => body.evaluate(element => { element.scrollTop = element.scrollHeight; });
      const top = () => body.evaluate(element => { element.scrollTop = 0; });
      const detail = body.locator('[data-testid="admin-detail"]');
      const back = () => detail.getByRole("button", {name: "Accounts"}).click();
      await shot("1-list");
      await bottom(); await shot("2-list-exports"); await top();
      await body.locator('.admin-row[data-account="acct_two"]').click();
      await detail.waitFor(); await shot("3-detail");
      await bottom(); await shot("4-detail-danger"); await top();
      await back();
      await body.locator('.admin-row[data-account="acct_three"]').click();
      await detail.waitFor();
      await detail.getByRole("button", {name: "Delete account…"}).click();
      const sheet = view.page.locator(".admin-delete-sheet");
      await sheet.waitFor();
      await sheet.getByLabel(/^Type/).fill("Lee Example");
      await shot("5-delete-sheet");
      adminState.failDelete = 1;
      await sheet.getByRole("button", {name: "Delete account", exact: true}).click();
      await sheet.getByText("Deletion stopped at", {exact: false}).waitFor();
      await shot("6-deleting-failed-retry");
      await sheet.getByRole("button", {name: "Retry deletion"}).click();
      const result = view.page.locator(".admin-result-sheet");
      await result.waitFor();
      await shot("7-result-sheet");
      await result.getByRole("button", {name: "I've saved the passphrase"}).click();
      await result.waitFor({state: "detached"});
      await body.locator('.admin-row[data-account="acct_four"]').click();
      await detail.waitFor(); await shot("8-deleting-state");
      await detail.locator(".settings-banner").getByRole("button", {name: "Retry deletion"}).click();
      await sheet.waitFor(); await shot("9-retry-sheet");
      await view.close();
    }
    console.log(`PASS admin screenshots: ${count} saved to ${dir}`);
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

  // ---- 10. Update notice: banner for admins, Version card, per-version dismissal.
  {
    const banner = '[data-update-banner]';
    updateState.latest = "v0.1.1";
    const admin = await open({admin: "1", auto: "off"});
    await admin.page.locator(banner).waitFor({timeout: 15000});
    const text = (await admin.page.locator(banner).innerText()).replace(/\s+/g, " ");
    assert.match(text, /Tofi v0\.1\.1 is available\. Run sudo tofi update on the server\./);
    assert.equal(await admin.page.locator(`${banner} a`).getAttribute("href"), "https://github.com/example/tofibot/releases/tag/v0.1.1");
    assert.equal(await admin.page.locator(`${banner} a`).textContent(), "What's new");
    // Dismissal persists for that version across a reload...
    await admin.page.getByRole("button", {name: "Dismiss update notice"}).click();
    assert.equal(await admin.page.locator(banner).count(), 0, "dismissed banner is gone");
    assert.equal(await admin.page.evaluate(() => localStorage.getItem("tofi.update-notice.dismissed")), "v0.1.1");
    await admin.page.reload();
    await admin.page.locator(".settings-shell").waitFor();
    await admin.page.waitForTimeout(800);
    assert.equal(await admin.page.locator(banner).count(), 0, "dismissal survives a reload");
    // ...but not for the next version.
    updateState.latest = "v0.1.2";
    await admin.page.reload();
    await admin.page.locator(banner).waitFor({timeout: 15000});
    assert.match(await admin.page.locator(banner).innerText(), /v0\.1\.2/);
    await admin.close();

    // Automatic-install wording only when auto_update=patch and the new version is a patch bump.
    const auto = await open({admin: "1", auto: "patch"});
    await auto.page.locator(banner).waitFor({timeout: 15000});
    assert.match((await auto.page.locator(banner).innerText()).replace(/\s+/g, " "), /Tofi v0\.1\.2 is available and will install automatically overnight\./);
    await auto.close();
    updateState.latest = "v0.2.0";
    const minor = await open({admin: "1", auto: "patch"});
    await minor.page.locator(banner).waitFor({timeout: 15000});
    assert.match(await minor.page.locator(banner).innerText(), /Run\s+sudo tofi update/, "a minor bump is never described as automatic");
    await minor.close();

    // Single-owner mode: the owner is the admin.
    updateState.latest = "v0.1.1";
    const owner = await open({admin: "0"});
    await owner.page.locator(banner).waitFor({timeout: 15000});
    await owner.close();

    // A non-admin account never sees the banner and never asks for the data.
    const before = requested.filter(line => line.endsWith("/api/system/update")).length;
    const member = await open({role: "user"});
    await member.page.waitForTimeout(1200);
    assert.equal(await member.page.locator(banner).count(), 0, "banner hidden for a non-admin");
    await member.page.locator('.settings-tabs [data-tab="advanced"]').click();
    await member.page.locator('.settings-page-body[data-page="advanced"]').getByText("Danger zone", {exact: false}).first().waitFor();
    assert.equal(await member.page.locator(".version-card").count(), 0, "Version card hidden for a non-admin");
    assert.equal(requested.filter(line => line.endsWith("/api/system/update")).length, before, "non-admin made no /api/system/update request");
    await member.close();

    // Up to date: no banner; the Version card says so.
    updateState.latest = "v0.1.0";
    const current = await open({admin: "1", tab: "advanced"});
    await current.page.locator(".version-card").waitFor({timeout: 15000});
    await current.page.waitForTimeout(500);
    assert.equal(await current.page.locator(banner).count(), 0, "no banner when up to date");
    const card = (await current.page.locator(".version-card").innerText()).replace(/\s+/g, " ");
    assert.match(card, /Installed version v0\.1\.0 Up to date/);
    assert.match(card, /Latest release v0\.1\.0/);
    assert.match(card, /Last checked Oct 9, 2026/);
    assert.match(card, /Automatic fix releases .*sudo tofi config auto-update on or sudo tofi config auto-update off Off/);
    await current.close();

    // Update available + auto on: card shows the install hint and the state.
    updateState.latest = "v0.1.1";
    const pending = await open({admin: "1", tab: "advanced", auto: "patch"});
    await pending.page.locator(".version-card").waitFor({timeout: 15000});
    await pending.page.getByText("Update available", {exact: true}).waitFor();
    const pendingCard = (await pending.page.locator(".version-card").innerText()).replace(/\s+/g, " ");
    assert.match(pendingCard, /Run sudo tofi update on the server to install it\./);
    assert.match(pendingCard, / On$/);
    assert.equal(await pending.page.locator(".version-card [data-auto-update]").getAttribute("data-auto-update"), "patch");
    assert.equal(await pending.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
    await pending.close();
    // No horizontal scroll at 390px.
    const narrow = await open({admin: "1", tab: "advanced"}, {width: 390, height: 844});
    await narrow.page.locator(".settings-mhome [data-tab=\"advanced\"]").click().catch(() => {});
    await narrow.page.locator(".version-card").waitFor({timeout: 15000});
    await narrow.page.waitForTimeout(500);
    assert.equal(await narrow.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll at 390px");
    await narrow.close();

    // Translations render (Japanese banner and card).
    const ja = await open({admin: "1", lang: "ja", tab: "advanced", auto: "off"});
    await ja.page.locator(banner).waitFor({timeout: 15000});
    assert.match((await ja.page.locator(banner).innerText()).replace(/\s+/g, " "), /Tofi v0\.1\.1 が公開されました/);
    assert.match(await ja.page.locator(".version-card").innerText(), /現在のバージョン/);
    await ja.close();
    console.log("PASS update notice: banner for admins and the single owner, hidden for members, per-version dismissal, patch-only automatic wording, Version card, 390px, ja");

    if (process.env.UPDATE_SHOTS) {
      const dir = process.env.UPDATE_SHOTS;
      await mkdir(dir, {recursive: true});
      let count = 0;
      updateState.latest = "v0.1.1";
      for (const theme of ["light", "dark"]) for (const [label, size] of [["1440", {width: 1440, height: 900}], ["390", {width: 390, height: 844}]]) {
        const {page, close} = await open({admin: "1", tab: "advanced", auto: "patch"}, {...size, theme});
        await page.locator(banner).waitFor({timeout: 15000});
        if (label === "390") await page.locator(".settings-mhome [data-tab=\"advanced\"]").click().catch(() => {});
        await page.locator(".version-card").waitFor({timeout: 15000});
        await page.waitForTimeout(900);
        await page.screenshot({path: join(dir, `update-${label}-${theme}.png`)}); count++;
        await close();
        const manual = await open({admin: "1", tab: "advanced", auto: "off"}, {...size, theme});
        await manual.page.locator(banner).waitFor({timeout: 15000});
        if (label === "390") await manual.page.getByRole("button", {name: "Close settings"}).first().click();
        if (label === "390") await manual.page.evaluate(() => document.querySelector("aside.detail-pane")?.remove());
        await manual.page.waitForTimeout(700);
        await manual.page.screenshot({path: join(dir, `update-manual-${label}-${theme}.png`)}); count++;
        await manual.close();
      }
      console.log(`PASS update screenshots: ${count} saved to ${dir}`);
    }
  }

  assert.deepEqual(unexpected, [], `unexpected requests or page errors: ${unexpected.join("; ")}`);
  assert.deepEqual(writes, [], `no writes expected: ${writes.join("; ")}`);
  console.log("PASS no unstubbed /api request, no write, no page error");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
