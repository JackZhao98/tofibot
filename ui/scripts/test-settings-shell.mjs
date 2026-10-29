// Rendered acceptance for the settings shell (docs/agent-plan/settings-redesign.md, 2.6 item 3).
// Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed
// (neutral sample data only) and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//   SETTINGS_SHOTS=<dir>  also saves every tab at 1440x900 and 390x844, light and dark.
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
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

const LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"];
// The rail with the Advanced group folded; the folded group holds ADV_TABS ("advanced" is the System page, labelled Maintenance).
const TABS = ["general", "connections", "skills", "permissions", "models", "tools", "automation", "computer", "keys", "usage", "advanced"];
// "devices" (Other devices) is behind TOFI_PERSONAL_COMPUTERS: the rail lists it only when `computer.personal_computers` is true.
const ADV_TABS = ["allowances", "followups"];
const ICONS = {general: "sliders", connections: "plug", skills: "skill", permissions: "shield-check", allowances: "checklist", followups: "bell", devices: "laptop", models: "sparkles", tools: "globe", automation: "workflow", computer: "monitor", keys: "key", usage: "progress", advanced: "settings", admin: "group"};
const NAMES = {general: "General", connections: "Connections", skills: "Skills", permissions: "Permissions", allowances: "Tool rules", followups: "Follow-ups", devices: "Other devices", models: "Models", tools: "Search & fetch", automation: "Behavior", computer: "Computer", keys: "Computer keys", usage: "Usage", advanced: "Maintenance", admin: "Admin console"};

// ---- Catalog checks (every locale): groups, page names and descriptions are real translations.
const catalogs = {};
for (const lang of LANGS) catalogs[lang] = JSON.parse(await readFile(join(ui, "src/locales", lang, "settings.json"), "utf8"));
for (const lang of LANGS) {
  const shell = catalogs[lang].shell;
  assert.deepEqual(Object.keys(shell.group).sort(), ["bots", "more", "system", "workspace", "you"], `${lang}: shell.group keys`);
  assert.deepEqual(Object.keys(shell.page).sort(), [...TABS, ...ADV_TABS, "devices", "admin"].sort(), `${lang}: shell.page keys (no stale tab ids)`);
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
console.log(`PASS catalogs: ${LANGS.length} locales have groups, 15 page names, descriptions of at most 70 characters, dictation copy`);

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
import {CodexPanel} from '../src/settings/AppSlots';
import {UpdateBanner} from '../src/UpdateNotice';
import {setComputerCapability} from '../src/computerCapability';
const query=new URLSearchParams(location.search);
if(query.get('personal'))setComputerCapability({backend:'kvm',available:true,capabilities:[],personal_computers:true});
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'},{id:'bot_inbox',name:'Inbox Bot',archived:false,model:'gpt-6-luna',reasoning_effort:'medium'},{id:'bot_notes',name:'Notes Bot',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_old',name:'Retired Bot',archived:true,model:'default',reasoning_effort:'default'}] as any;
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as SettingsView)||'home'});
 const [closed,setClosed]=useState(false);
 const openSettings=(next:SettingsTab,view:SettingsView='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 useEffect(()=>{(window as any).__closed=closed},[closed]);
 return <div className="workspace"><UpdateBanner/><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true">{!closed&&<SettingsShell tab={tab} onTab={setTab} onClose={()=>setClosed(true)} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/>}</aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const requested = [], unexpected = [], writes = [], tierWrites = [];
  // The skill library the stub serves (decision 0058): owner-installed, Bot-installed with versions, a bundle, an official skill.
  const libWrites = [];
  const libState = () => ({
    skills: [
      {name: "research-assistant", description: "Research and verify sources", installed_by: {kind: "owner"}, installed_at: "2026-10-20T10:00:00Z", version: 1, enabled_bots: 1, bots_total: 3},
      {name: "weekly-report", description: "Draft the weekly status report", installed_by: {kind: "bot", bot_id: "bot_research", bot_name: "Research Bot"}, installed_at: "2026-10-21T10:00:00Z", version: lib.weeklyVersion, updated: lib.weeklyVersion > 1, updated_at: "2026-10-22T10:00:00Z", enabled_bots: 2, bots_total: 3,
        versions: [{version: 2, created_at: "2026-10-22T10:00:00Z", created_by: {kind: "bot", bot_name: "Research Bot"}, note: "Shorter summary", current: lib.weeklyVersion === 2}, {version: 1, created_at: "2026-10-21T10:00:00Z", created_by: {kind: "bot", bot_name: "Research Bot"}, current: lib.weeklyVersion === 1}]},
      {name: "alpha-tool", description: "First tool of the kit", bundle_id: "office-kit", installed_by: {kind: "owner"}, installed_at: "2026-10-19T10:00:00Z", version: 1, enabled_bots: 1, bots_total: 3},
      {name: "beta-tool", description: "Second tool of the kit", bundle_id: "office-kit", installed_by: {kind: "owner"}, installed_at: "2026-10-19T10:00:00Z", version: 1, enabled_bots: 0, bots_total: 3},
      {name: "spreadsheet", description: "Build and reconcile XLSX workbooks with real numbers and formulas; verify the saved file.", builtin: true, enabled_bots: 3, bots_total: 3},
      ...lib.added,
    ].filter(skill => !lib.removed.has(skill.name)),
    bundles: lib.removed.has("office-kit") ? [] : [{id: "office-kit", description: "Office helpers", version: "1.0.0", installed_by: {kind: "owner"}, installed_at: "2026-10-19T10:00:00Z", skills: ["alpha-tool", "beta-tool"]}],
  });
  const lib = {weeklyVersion: 2, added: [], removed: new Set()};
  const resetLib = () => { libWrites.length = 0; lib.weeklyVersion = 2; lib.added = []; lib.removed = new Set(); };
  const updateState = {latest: "v0.1.0"};
  // The host side of "Update" (decision D69): what the server would report while an update runs. `mode` picks the ending.
  const updateFlow = {posted: [], reads: 0, mode: "done", rc: false, rcWrites: []};
  const resetUpdateFlow = (mode = "done") => { updateFlow.posted.length = 0; updateFlow.reads = -1; updateFlow.mode = mode; updateFlow.rcWrites.length = 0; };
  const hostStatus = (state, extra = {}) => ({state, version: updateState.latest, from: "v0.1.0", channel: updateFlow.posted.length && JSON.parse(updateFlow.posted[0]).channel || "stable", progress: null, updated_at: new Date().toISOString(), ...extra});
  const updateAnswer = query => {
    const base = {current: "v0.1.0", latest: updateState.latest, update_available: updateState.latest !== "v0.1.0", notes_url: `https://github.com/example/tofibot/releases/tag/${updateState.latest}`, checked_at: "2026-10-09T09:30:00Z", auto_update: query.get("auto") === "patch" ? "patch" : "off"};
    if (query.get("host") !== "1") return base;
    Object.assign(base, {host_update: true, channel_rc_enabled: updateFlow.rc, latest_rc: updateFlow.rc ? "v0.2.0-rc.1" : "", latest_rc_notes_url: updateFlow.rc ? "https://github.com/example/tofibot/releases/tag/v0.2.0-rc.1" : "", request: null, status: null});
    if (updateFlow.reads < 0) return base;
    const n = updateFlow.reads++;
    if (n === 0) return {...base, request: {version: updateState.latest, channel: "stable", requested_at: new Date().toISOString()}};
    if (n === 1) return {...base, status: hostStatus("waiting_for_runs", {progress: 10})};
    if (n === 2) return {...base, status: hostStatus("downloading", {progress: 30})};
    if (n === 3) return {...base, status: hostStatus("switching", {progress: 80})};
    if (updateFlow.mode === "fail") return {...base, status: hostStatus("failed", {code: "runs_still_active", finished_at: new Date().toISOString()})};
    if (updateFlow.mode === "rollback") return {...base, status: hostStatus("rolled_back", {code: "update_rejected", rolled_back_to: "v0.1.0", finished_at: new Date().toISOString()})};
    if (n < 6) return {__abort: true};
    return {...base, current: updateState.latest, update_available: false, status: hostStatus("done", {progress: 100, finished_at: new Date().toISOString()})};
  };
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
  // Tool tiers per saved connection (docs/agent-plan/mcp-tool-tiers.md): the tracker has a tool list with every tier, marker and the changed flag; the others have none yet.
  const toolTiers = name => name === "acme_tracker" ? {tools: [
    {name: "search_issues", description: "Search issues by text, label and assignee", tier: "read_only", source: "provider", read_only_hint: true},
    {name: "get_issue", description: "Fetch one issue with its comments", tier: "read_only", source: "owner", override: "read_only"},
    {name: "create_issue", description: "Create an issue in a project", tier: "write", source: "default"},
    {name: "update_issue", description: "Change title, status or assignee of an issue with a very long description that keeps going to exercise wrapping in the narrow layout", tier: "write", source: "default", changed: true, duplicated: true},
    {name: "delete_issue", description: "Delete an issue permanently", tier: "high_risk", source: "owner", override: "high_risk", destructive_hint: true},
    {name: "post_comment", description: "Post a comment visible to the whole team", tier: "high_risk", source: "owner", override: "high_risk", changed: true},
  ], read_only: 2, write: 2, high_risk: 2} : {tools: [], read_only: 0, write: 0, high_risk: 0};
  const GIB_ALL = 2 ** 30;
  // Version 1 of GET /api/storage with neutral sample numbers.
  const storageSample = (used, limit) => ({
    version: 1, measured_at: "2026-10-27T10:00:00Z", server_bytes: used, files_bytes: used - 0.2 * GIB_ALL, database_bytes: 0.2 * GIB_ALL, physical_files_bytes: used - 0.2 * GIB_ALL, pending_copy_count: 2,
    classes: [{class: "uploads", bytes: 0.6 * GIB_ALL, objects: 12, billable: true}, {class: "published", bytes: used - 0.8 * GIB_ALL, objects: 4, billable: true}, {class: "generated", bytes: 0, objects: 0, billable: true}, {class: "outbox", bytes: 0, objects: 0, billable: true}, {class: "mirror", bytes: 0, objects: 0, billable: true}, {class: "conversations", bytes: 0.1 * GIB_ALL, objects: 300, billable: false}, {class: "db", bytes: 0.2 * GIB_ALL, objects: 0, billable: true}],
    breakdown_measured_at: "2026-10-27T09:30:00Z", limit_bytes: limit, remaining_bytes: limit === null ? null : Math.max(0, limit - used), over_limit: limit !== null && used >= limit,
    computer: {logical_bytes: 3 * GIB_ALL, allocated_bytes: 4 * GIB_ALL, quota_bytes: 16 * GIB_ALL},
    by_bot: [{id: "bot_research", name: "Research Bot", bytes: 0.9 * GIB_ALL, objects: 8}, {id: "", name: "", bytes: 0.4 * GIB_ALL, objects: 8}],
    by_conversation: [{id: "conv_1", name: "Weekly summary", bytes: 1.1 * GIB_ALL, objects: 10}, {id: "conv_2", name: "", bytes: 0.2 * GIB_ALL, objects: 2}],
  });
    const stub = (url, method, query, postData = "") => {
    const path = url.pathname;
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools$/.test(path) && method === "GET") return toolTiers(path.split("/")[4]);
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools\/refresh$/.test(path) && method === "POST") return {...toolTiers(path.split("/")[4]), checked: true};
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools\/[^/]+$/.test(path) && method === "PUT") { const name = decodeURIComponent(path.split("/")[6]); const tool = toolTiers(path.split("/")[4]).tools.find(item => item.name === name); return {ok: true, tool, summary: toolTiers(path.split("/")[4])}; }
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: {mode: "owner"}};
    if (path === "/api/system/update") {
      if (query.get("role") === "user") return undefined;
      return updateAnswer(query);
    }
    if (path === "/api/system/update/request" && method === "POST") { updateFlow.posted.push(postData); updateFlow.reads = 0; return {request: {version: updateState.latest, channel: "stable"}}; }
    if (path === "/api/system/update/settings" && method === "PUT") { const payload = JSON.parse(postData); updateFlow.rc = payload.channel_rc; updateFlow.rcWrites.push(postData); return updateAnswer(new URLSearchParams("host=1")); }
    if (path === "/api/auth/session") return query.get("role") === "user"
      ? {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_user", username: "Sam Example", email: "sam@example.test", role: "user"}}
      : query.get("admin") === "0"
      ? {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}}
      : {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin"}};
    if (path === "/api/settings-changes") return {changes: []};
    if (path === "/api/storage") return query.get("storage") === "full" ? storageSample(10 * GIB_ALL, 10 * GIB_ALL) : storageSample(1.5 * GIB_ALL, query.get("storage") === "unlimited" ? null : 10 * GIB_ALL);
    if (path === "/api/bots") return {bots: [{id: "bot_research", name: "Research Bot", archived: false}, {id: "bot_inbox", name: "Inbox Bot", archived: false}, {id: "bot_notes", name: "Notes Bot", archived: false}, {id: "bot_old", name: "Retired Bot", archived: true}]};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/standing-allowances") return {allowances: [], remembered: []};
    if (path === "/api/settings-changes") return {changes: []};
    if (/^\/api\/extensions\/mcp\/[^/]+\/bots$/.test(path)) return {all: true, bots: []};
    if (path === "/api/automation-settings") return {items: [{key: "web_search", group: "replies", kind: "bool", default: true, value: true, source: "default"}, {key: "conversation_model", group: "models", kind: "model", default: "codex-gpt-6-sol", value: "codex-gpt-6-sol", source: "default"}]};
    if (path === "/api/followups") return {account: {max_nudges_per_day: 3, quiet_start: "22:00", quiet_end: "08:00", weekend_min_importance: 80, digest_weekday: "mon", digest_time: "09:00", daily_budget_usd: 0.5}, primary_bot_id: "bot_research", delivery_available: false, budget_paused: false, model_configured: true, day: "2026-10-13",
      bots: [{bot_id: "bot_research", name: "Research Bot", is_primary: true, mode: "shadow", source_chat: true, source_email: true, last_sweep_at: "2026-10-13T16:30:00Z"}, {bot_id: "bot_inbox", name: "Inbox Bot", is_primary: false, mode: "off", source_chat: true, source_email: true}, {bot_id: "bot_notes", name: "Notes Bot", is_primary: false, mode: "shadow", source_chat: true, source_email: true}],
      stats: {sweeps: 4, loops_created: 3, rejected_invalid: 1, rejected_band: 2, merged: 1, spent_usd: 0.012, would_nudge: 2, would_digest: 1, useful: 3, annoying: 1, handled: 1},
      loops: [
        {id: "loop_1", bot_id: "bot_research", bot_name: "Research Bot", source: "chat", kind: "awaiting_other", topic: "Pest control to call about the roof bees, with a deliberately long topic that has to wrap on a phone", counterparty_name: "Elyse", category: "home", importance: 90, band: "nudge", expected_by: "2026-10-15T17:00:00Z", suggested_next: "Draft a short follow-up to Elyse", quote: "Elyse says pest control will call me about the roof bees", resolution_phrases: ["pest control called"], state: "open", verdict: "", created_at: "2026-10-13T15:00:00Z"},
        {id: "loop_2", bot_id: "bot_notes", bot_name: "Notes Bot", source: "chat", kind: "owner_promise", topic: "Send Marc the draft", category: "work", importance: 60, band: "nudge", expected_by: "2026-10-30T16:00:00Z", suggested_next: "Attach the latest draft", quote: "I will send Marc the draft by 2026-10-30", resolution_phrases: [], state: "open", verdict: "useful", created_at: "2026-10-13T15:10:00Z"},
        {id: "loop_3", bot_id: "bot_research", bot_name: "Research Bot", source: "chat", kind: "owner_promise", topic: "Call the dentist", category: "health", importance: 45, band: "digest", expected_by: "2026-10-20T16:00:00Z", quote: "I need to call the dentist sometime", resolution_phrases: [], state: "digest_only", verdict: "", created_at: "2026-10-13T15:20:00Z"},
        {id: "loop_4", bot_id: "bot_research", bot_name: "Research Bot", source: "chat", kind: "owner_reminder", topic: "Renew the parking permit", category: "admin", importance: 65, band: "nudge", expected_by: "2026-10-12T16:00:00Z", quote: "remind me to renew the parking permit", resolution_phrases: [], state: "handled_by_owner", verdict: "handled", created_at: "2026-10-11T15:20:00Z"},
      ]};
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      {name: "acme_docs", url: "https://mcp.example.test/docs", oauth: {client_id: "sample", connected: false}},
      {name: "acme_tracker", url: "https://mcp.example.test/tracker", oauth: {client_id: "sample", connected: true}, status: {checked_at: "2026-10-09T09:30:00Z", ok: true, tool_count: 49}},
      {name: "notes_search", url: "https://mcp.example.test/notes", headers: {}},
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") return query.get("testfail") ? {ok: false, tool_count: 0, diagnostics: [{server: "x", message: "MCP server connection or discovery failed"}]} : {ok: true, tool_count: 8};
    if (path === "/api/extensions/oauth-options") return {vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/local-mcp") return {available: false, reason: "sample"};
    if (path === "/api/extensions/google") return {client: {configured: false}, apis: []};
    if (path === "/api/extensions/repo-stars") return {stars: {}};
    if (path === "/api/extensions/skills") return libState();
    if (path === "/api/auto-review-settings") return {mode: "shadow", revision: 1, review_scope: "all_external_tools"};
    if (path === "/api/providers") return {providers};
    if (path === "/api/web-tools") return {enabled: true, computer: false, search_offered: true, search: [{id: "native", label: "Model web search", kind: "native", paid: false, fixed: true, enabled: true, status: "always", testable: false}], fetch: [{id: "builtin", label: "Built-in fetch", kind: "builtin", paid: false, fixed: true, enabled: true, status: "always", testable: true}]};
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
    if (path === "/api/computer/contents") return {environment: ["WEB_API_TOKEN", "DEPLOY_REGION"], ssh_keys: [], pending_file_copies: 0};
    if (path === "/api/usage/runs") return {days: 7, since: day(7), runs: 48, by_status: {done: 41, failed: 4, cancelled: 2, running: 1}, failures: [{code: "model_auth_invalid", count: 3}, {code: "connection_interrupted", count: 1}], tool_calls: 212, tool_timeouts: 2, model_calls: 96};
    if (path === "/api/usage") return {contexts: [{run_id: "run_1", conversation_id: "conv_1", conversation_name: "Weekly summary", bot_id: "bot_research", bot_name: "Research Bot", run_status: "completed", model: "codex-gpt-6-sol", window_tokens: 200000, window_known: true, compact_at: 160000, estimated_input: 52000, last_input: 48000, total_input: 310000, total_output: 42000, compact_count: 1, updated_at: day(0)}],
      agents: [{bot_id: "bot_research", bot_name: "Research Bot", input_tokens: 310000, output_tokens: 42000, runs: 14, compactions: 1}, {bot_id: "bot_inbox", bot_name: "Inbox Bot", input_tokens: 120000, output_tokens: 18000, runs: 6, compactions: 0}],
      periods: {"24h": [{bot_id: "bot_research", input_tokens: 90000, output_tokens: 12000, requests: 5, equivalent_usd: 0.42, unpriced_requests: 0}], "7d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}], "30d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}]},
      review_periods: {"24h": [{bot_id: "bot_research", input_tokens: 6400, output_tokens: 310, requests: 3, equivalent_usd: 0, unpriced_requests: 3}], "7d": [{bot_id: "bot_research", input_tokens: 21000, output_tokens: 980, requests: 9, equivalent_usd: 0.04, unpriced_requests: 2}], "30d": [{bot_id: "bot_research", input_tokens: 21000, output_tokens: 980, requests: 9, equivalent_usd: 0.04, unpriced_requests: 2}]},
      calls: [{id: 1, bot_id: "bot_research", bot_name: "Research Bot", conversation_name: "Weekly summary", model: "codex-gpt-6-sol", input_tokens: 18000, output_tokens: 2100, equivalent_usd: 0.09, price_known: true, trigger_content: "Summarize the week", occurred_at: day(0), kind: "chat"}, {id: 2, bot_id: "bot_research", bot_name: "Research Bot", conversation_name: "Weekly summary", model: "gpt-5-mini", input_tokens: 2400, output_tokens: 110, equivalent_usd: 0, price_known: false, trigger_content: "Summarize the week", occurred_at: day(0), kind: "review"}],
      price_source: "sample", price_as_of: "2026-10-01", note: ""};
    return undefined;
  };
  // ---- Admin console stub: the real response shapes, mutable so flows can be driven end to end.
  const GIB = 2 ** 30;
  const adminState = {sizes: {}};
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
      {id: "exp_lost", username: "Rae Example", email: "", created_at: Math.floor(Date.parse("2026-10-01T10:00:00Z") / 1000), expires_at: Math.floor(Date.parse("2026-10-31T10:00:00Z") / 1000), size: 500_000, link_path: "", link_unavailable: true, passphrase_pending: true, passphrase_unavailable: true},
    ];
    adminState.sizes = {};
    adminState.limits = {acct_two: 10 * GIB};
    adminState.failDelete = 0;
    adminState.vm = {};
    adminState.failAttach = 0;
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
    if (path === "/api/admin/storage") {
      const used = {acct_sample: 1.5 * GIB, acct_two: 9.5 * GIB, acct_three: 0, acct_four: 0};
      const accounts = adminState.accounts.map(a => {
        const limit = adminState.limits[a.id] ?? null, server = used[a.id] ?? 0;
        return {account_id: a.id, source: "live", measured_at: "2026-10-27T10:00:00Z", server_bytes: server, files_bytes: server, database_bytes: 0, physical_files_bytes: server, pending_copy_count: 0, limit_bytes: limit, over_limit: limit !== null && server >= limit, classes: [], computer: null};
      });
      return json(200, {version: 1, host: {free_bytes: 80 * GIB, total_bytes: 500 * GIB, min_free_bytes: 2 * GIB, low: false}, totals: {accounts: accounts.length, server_bytes: 0, physical_files_bytes: 0, computer_logical_bytes: 0, computer_allocated_bytes: 0, pending_copy_count: 0}, accounts});
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
    // Hot attach / detach of the VM computer: gateway per account, or the single-account route (key "solo").
    match = /^\/api\/admin\/(?:accounts\/([^/]+)\/)?computer\/(state|attach|detach)$/.exec(path);
    if (match) {
      const key = match[1] ?? "solo", op = match[2];
      const vm = adminState.vm[key] ??= {status: "attached"};
      const summary = () => ({attached: vm.status === "attached", id: vm.status === "attached" ? "vm_" + key : undefined, kind: "vm", status: vm.status, backend_available: true});
      if (op === "state") return json(200, summary());
      if (op === "attach") {
        if (adminState.failAttach > 0) { adminState.failAttach--; return json(502, {error: {code: "computer_attach_failed", message: "the computer could not be attached"}}); }
        vm.status = "attached";
        return json(200, summary());
      }
      vm.status = "detached";
      return json(200, {computer: summary(), detach: {computer_id: "vm_" + key, drained: true, interrupted: 1, outbox: {synced: 3, deferred: 0, refused: 0}}});
    }
    match = /^\/api\/admin\/accounts\/([^/]+)\/resources$/.exec(path);
    if (match) {
      const size = adminState.sizes[match[1]] ??= {vcpus: 2, memory_mib: 4096, disk_gib: adminState.disks[match[1]]?.[0] ?? 8};
      if (method === "PUT") {
        const input = bodyOf();
        if (input.disk_gib < size.current_disk) return json(400, {error: {code: "invalid_resources", message: "disk can only grow"}});
        size.desired = input;
      }
      const desired = size.desired ?? {vcpus: size.vcpus, memory_mib: size.memory_mib, disk_gib: size.disk_gib};
      if (method === "PUT") adminState.disks[match[1]][0] = desired.disk_gib;
      const current = {vcpus: size.vcpus, memory_mib: size.memory_mib, disk_gib: size.disk_gib};
      return json(200, {current, desired, pending: desired.vcpus !== current.vcpus || desired.memory_mib !== current.memory_mib, state: "ready", host: {cpus: 8, memory_total_mib: 16384, memory_available_mib: 9000, disk_available_gib: 120}, limits: {max_vcpus: 8, max_memory_mib: 12288, max_disk_gib: current.disk_gib + 120}});
    }
    match = /^\/api\/admin\/accounts\/([^/]+)\/quota$/.exec(path);
    if (match) {
      const input = bodyOf();
      if ("server_bytes_limit" in input) {
        adminState.limits[match[1]] = input.server_bytes_limit;
        return json(200, {account_id: match[1], server_bytes_limit: input.server_bytes_limit});
      }
      adminState.disks[match[1]][0] = input.quota_gib;
      return json(200, {quota_bytes: input.quota_gib * GIB, applied: true});
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
      if (url.pathname.startsWith("/api/extensions/skill")) {
        const entry = `${method} ${url.pathname}${request.postData() ? " " + request.postData() : ""}`;
        const parts = url.pathname.split("/");
        if (method === "POST" && url.pathname === "/api/extensions/skills") {
          const payload = JSON.parse(request.postData() ?? "{}");
          lib.added.push({name: payload.name, description: "Added in the test", installed_by: {kind: "owner"}, installed_at: "2026-10-23T10:00:00Z", version: 1, enabled_bots: 0, bots_total: 3});
          libWrites.push(entry);
          await route.fulfill({status: 201, contentType: "application/json", body: JSON.stringify({ok: true})});
          return;
        }
        if (method === "POST" && parts[5] === "rollback") { lib.weeklyVersion = JSON.parse(request.postData() ?? "{}").version; libWrites.push(entry); await route.fulfill({contentType: "application/json", body: JSON.stringify({ok: true})}); return; }
        if (method === "DELETE") { lib.removed.add(parts[4]); if (parts[3] === "skill-bundles") { lib.removed.add("alpha-tool"); lib.removed.add("beta-tool"); } libWrites.push(entry); await route.fulfill({contentType: "application/json", body: JSON.stringify({ok: true})}); return; }
      }
      if (url.pathname.startsWith("/api/admin/")) {
        const answer = adminStub(url, method, request.postData());
        await route.fulfill({status: answer.status, contentType: "application/json", body: JSON.stringify(answer.body)});
        return;
      }
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""), request.postData() ?? "");
      if (body && body.__abort) { await route.abort(); return; }
      if (body === undefined) { unexpected.push(`${method} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {message: "not stubbed", code: "not_found"}})}); return; }
      if (method === "PUT" && /^\/api\/extensions\/mcp\/[^/]+\/tools\/[^/]+$/.test(url.pathname)) tierWrites.push(`${method} ${url.pathname} ${request.postData()}`);
      else if (method !== "GET" && !/\/(test|verify)$/.test(url.pathname)) writes.push(`${method} ${url.pathname}`);
      const delay = Number(new URLSearchParams(page.url().split("?")[1] ?? "").get("testdelay") ?? 0);
      if (delay && /\/test$/.test(url.pathname)) await new Promise(resolve => setTimeout(resolve, delay));
      await route.fulfill({contentType: "application/json", body: JSON.stringify(body)});
    });
    const query = new URLSearchParams({lang: "en", ...params});
    await page.goto(`${origin}/${basename(fixture)}/index.html?${query}`);
    await page.locator(".settings-shell").waitFor({timeout: 20000});
    return {page, close: () => context.close()};
  }
  const railTabs = page => page.$$eval(".settings-tabs .settings-nav-group button[data-tab]", buttons => buttons.map(button => button.dataset.tab));
  const selectedTab = page => page.locator('.settings-tabs [aria-current="page"]').getAttribute("data-tab");
  const settle = page => page.waitForTimeout(450);

  // ---- 1. Rail: exactly the ten tabs in order; admin only for a multi-account admin session.
  {
    const {page, close} = await open({admin: "0"});
    assert.deepEqual(await railTabs(page), TABS, "rail shows the ten tabs in order for a non-admin session");
    assert.deepEqual(await page.$$eval(".settings-tabs .settings-nav-group>p", items => items.map(item => item.textContent)), ["You", "Bots can use", "Workspace", "System"]);
    assert.equal(await page.locator("#settings-category").count(), 0, "no category select");
    const icons = await page.$$eval(".settings-tabs .settings-nav-group button[data-tab]", buttons => buttons.map(button => button.querySelector("svg").dataset.tofiIcon));
    assert.equal(new Set(icons).size, icons.length, "no two tabs share an icon");
    assert.deepEqual(icons, TABS.map(id => ICONS[id]), "icons match the contract");
    // The connections badge counts saved connections that need sign-in, from the saved list alone.
    assert.equal((await page.locator('.settings-tabs [data-tab="connections"] .settings-count').textContent()).trim(), "1");
    assert.equal(await page.locator('.settings-tabs [data-tab="permissions"] .settings-count').count(), 0, "permissions badge stays hidden in Phase 1");
    await page.waitForTimeout(600);
    // The storage card (S3): one bar, class legend, computer line, breakdown; version 1 of GET /api/storage.
    {
      const card = page.locator(".storage-card");
      await card.waitFor();
      const words = (await card.innerText()).replace(/\s+/g, " ");
      assert.match(words, /Server storage.*1\.5 GiB of 10 GiB/);
      assert.match(words, /Uploads.*Published by Bots.*Database/);
      assert.ok(!/Conversations/.test(words.split("Breakdown")[0]), "message text is part of the database and is not listed twice");
      assert.match(words, /Computer disk.*3 GiB used of 16 GiB/);
      assert.match(words, /2 files are still being copied from the computer to the server\./);
      const bar = card.getByRole("progressbar", {name: "Server storage"});
      assert.equal(await bar.getAttribute("aria-valuenow"), "15");
      assert.equal(await card.locator(".storage-bar.is-full").count(), 0);
      await card.locator("summary").click();
      assert.match((await card.innerText()).replace(/\s+/g, " "), /By Bot.*Research Bot.*You.*By conversation.*Weekly summary.*Untitled conversation/);
    }
    for (const forbidden of ["/api/extensions/local-mcp", "/api/computers", "/api/computer/"]) assert.ok(!requested.some(line => line.includes(forbidden)), `opening Settings on General made no ${forbidden} request`);
    // The Advanced group is folded until opened; opening lists Tool rules, Follow-ups (Beta) and Other devices (In development).
    const fold = page.locator(".settings-tabs .settings-nav-fold");
    assert.equal(await fold.getAttribute("aria-expanded"), "false", "Advanced starts folded");
    await fold.click();
    assert.equal(await fold.getAttribute("aria-expanded"), "true");
    assert.deepEqual(await railTabs(page), [...TABS, ...ADV_TABS], "the folded group lists its pages when opened");
    assert.equal((await page.locator('.settings-tabs [data-tab="followups"] .settings-nav-tag').textContent()).trim(), "Beta");
    assert.equal(await page.locator('.settings-tabs [data-tab="devices"]').count(), 0, "Other devices is hidden unless personal computers are on");
    await fold.click();
    assert.deepEqual(await railTabs(page), TABS, "folding hides them again");
    await close();
    const personal = await open({admin: "0", personal: "1"});
    await personal.page.locator(".settings-tabs .settings-nav-fold").click();
    assert.deepEqual(await railTabs(personal.page), [...TABS, ...ADV_TABS, "devices"], "personal computers on: Other devices is listed");
    assert.equal((await personal.page.locator('.settings-tabs [data-tab="devices"] .settings-nav-tag').textContent()).trim(), "In development");
    await personal.close();
    const admin = await open({admin: "1"});
    assert.deepEqual(await railTabs(admin.page), [...TABS, "admin"], "admin tab appears for a multi-account admin");
    const adminIcon = await admin.page.locator('.settings-tabs [data-tab="admin"] svg').getAttribute("data-tofi-icon");
    assert.equal(adminIcon, ICONS.admin);
    await admin.close();
    console.log("PASS rail: ten tabs in order, admin only for a multi-account admin, unique icons, badge from the saved list only");
  }

  // ---- 2. Each tab renders its heading and description; 3. deep links; 6. dirty draft.
  {
    const {page, close} = await open({admin: "1"});
    const markers = {general: "Language and region", connections: "Add", skills: "research-assistant", permissions: "Approval mode for this account", allowances: "Reviewer", followups: null, devices: "Other devices", models: "Defaults for new Bots", tools: "Search", computer: null, keys: null, usage: null, advanced: "Danger zone", admin: null};
    for (const id of [...TABS, ...ADV_TABS, "admin"]) {
      if (ADV_TABS.includes(id) && await page.locator(`.settings-tabs [data-tab="${id}"]`).count() === 0) await page.locator(".settings-tabs .settings-nav-fold").click();
      await page.locator(`.settings-tabs [data-tab="${id}"]`).click();
      await page.waitForFunction(tab => document.querySelector(`.settings-tabs [aria-current="page"]`)?.dataset.tab === tab, id);
      const heading = (await page.locator(".settings-content .settings-page-title h2").textContent()).replace(/(Beta|In development)$/, "").trim();
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
    const zone = page.locator('.settings-page-body[data-page="general"] .timezone-setting').last();
    await zone.locator(".ui-select-trigger").waitFor();
    await pickSelect(zone, "Europe/Berlin");
    await page.locator(".settings-save-bar").waitFor();
    await page.locator('.settings-tabs [data-tab="models"]').click();
    await page.locator(".settings-leave-dialog").waitFor();
    assert.equal(await selectedTab(page), "general", "still on General while the dialog is open");
    await page.getByRole("button", {name: "Keep editing"}).click();
    assert.equal(await page.locator(".settings-leave-dialog").count(), 0);
    assert.equal(await selectedTab(page), "general");
    await page.locator('.settings-tabs [data-tab="models"]').click();
    await page.locator(".settings-leave-dialog").getByRole("button", {name: "Discard changes"}).click();
    await page.waitForFunction(() => document.querySelector('.settings-tabs [aria-current="page"]')?.dataset.tab === "models");
    await close();
    console.log("PASS pages: heading and description for all eleven tabs, deep links, Models layout, dirty-draft leave dialog");
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
    assert.equal(report.length, 12);
    for (const item of report) {
      assert.equal(item.ellipsis, false, `${item.text}: no text-overflow ellipsis`);
      assert.equal(item.clipped, false, `${item.text}: scrollWidth <= clientWidth`);
      assert.ok(item.lines <= 2, `${item.text}: ${item.lines} lines`);
      assert.equal(item.inside, true, `${item.text}: inside its rail item`);
    }
    for (const text of ["Verbindungen", "Berechtigungen", "Computer-Schlüssel"]) assert.ok(report.some(item => item.text === text), `German rail shows ${text}`);
    const rail = await page.locator(".settings-tabs").evaluate(element => element.getBoundingClientRect().width);
    assert.equal(Math.round(rail), 248, "rail is 248px wide");
    await close();
    console.log("PASS german: Verbindungen, Berechtigungen and Computer-Schlüssel fit the 248px rail in at most two lines, no clipping");
  }

  // ---- 7b. Skill library (decision 0058): installer and version on each row, bundles expand, rollback, remove, one-click add.
  {
    resetLib();
    const page$ = (page) => page.locator('.settings-page-body[data-page="skills"]');
    const row = (page, name) => page$(page).locator(`[data-skill="${name}"]`);
    const {page, close} = await open({tab: "skills"});
    await row(page, "research-assistant").waitFor();
    const text = async (locator) => (await locator.innerText()).replace(/\s+/g, " ");
    assert.match(await text(row(page, "research-assistant")), /v1 · Added by you, .* · On for 1 of 3 Bots/, "owner row: version, installer, how many Bots have it");
    assert.match(await text(row(page, "weekly-report")), /Updated/, "a published version marks the row Updated");
    assert.match(await text(row(page, "weekly-report")), /v2 · Added by Research Bot/, "a Bot installer is named");
    assert.equal(await page$(page).getByRole("switch").count(), 0, "the library has no per-skill switch; Bots have their own");
    assert.equal(await page$(page).getByText("Who can use it").count(), 0, "the old access row is gone");
    // Official skills: tagged, no Remove, no switch.
    assert.equal((await row(page, "spreadsheet").locator(".skill-tag").first().textContent()).trim(), "Built-in");
    assert.equal(await row(page, "spreadsheet").getByRole("button", {name: "Remove"}).count(), 0, "an official skill has no Remove");
    assert.match(await text(row(page, "spreadsheet")), /Official/);
    // A bundle is one row; members appear on expand, with no Remove of their own.
    const bundle = page$(page).locator('[data-bundle="office-kit"]');
    assert.match(await text(bundle), /Bundle · 2 Skills/);
    assert.equal(await page$(page).locator('[data-skill="alpha-tool"]').count(), 0, "bundle members are hidden until expanded");
    await bundle.getByRole("button", {name: "Show 2 Skills"}).click();
    await row(page, "alpha-tool").waitFor();
    assert.equal(await row(page, "alpha-tool").getByRole("button", {name: "Remove"}).count(), 0, "a bundle member has no Remove of its own");
    assert.equal(await bundle.getByRole("button", {name: "Remove"}).count(), 1, "the bundle has Remove");
    assert.equal(await bundle.getByRole("button", {name: "Hide Skills"}).getAttribute("aria-expanded"), "true");
    // Rollback: the versions list, restoring the earlier one.
    await row(page, "weekly-report").getByRole("button", {name: "Versions (2)"}).click();
    await row(page, "weekly-report").getByRole("button", {name: "Roll back to this version"}).click();
    await page.waitForFunction(() => !document.querySelector('[data-skill="weekly-report"]')?.innerText.includes("Updated"));
    assert.deepEqual(libWrites.filter(entry => entry.includes("rollback")), ['POST /api/extensions/skills/weekly-report/rollback {"version":1}']);
    // Remove asks first and writes once.
    await row(page, "research-assistant").getByRole("button", {name: "Remove"}).click();
    assert.equal(libWrites.some(entry => entry.startsWith("DELETE")), false, "Remove asks before it writes");
    await row(page, "research-assistant").getByRole("button", {name: /^Remove$/}).last().click();
    await page.waitForFunction(() => !document.querySelector('[data-skill="research-assistant"]'));
    assert.deepEqual(libWrites.filter(entry => entry.startsWith("DELETE")), ["DELETE /api/extensions/skills/research-assistant"]);
    // One-click add from a starter template: no form, one POST, and the row appears (a template is a normal skill).
    const starter = page$(page).locator(".skill-templates .integration-tile").first();
    const starterTitle = (await starter.locator("strong").textContent()).trim();
    await starter.click();
    // The install flow asks which Bots get it; the default is one primary button for all of them.
    const pending = page$(page).locator(".skill-pending");
    await pending.waitFor();
    assert.equal(libWrites.filter(entry => entry.startsWith("POST /api/extensions/skills ")).length, 0, "choosing Bots comes before the write");
    assert.deepEqual(await pending.locator("button").allInnerTexts(), ["Enable for all Bots", "Choose Bots…", "Cancel"]);
    assert.equal(await pending.locator("button.primary-button").innerText(), "Enable for all Bots", "the default is one primary button");
    await pending.getByRole("button", {name: "Choose Bots…"}).click();
    assert.deepEqual(await pending.locator(".skill-enable-bots label").allInnerTexts(), ["Research Bot", "Inbox Bot", "Notes Bot"], "only active Bots are listed");
    assert.equal(await pending.getByRole("button", {name: "Add without enabling"}).count(), 1, "no Bot picked means install for none");
    await pending.getByLabel("Inbox Bot").check();
    await pending.getByRole("button", {name: "Add and enable for 1 Bot"}).click();
    await page.waitForFunction(() => document.querySelector('.settings-page-body[data-page="skills"] .extension-notice'));
    assert.match(await text(page$(page).locator(".extension-notice")), /Added to the library and turned on for 1 Bot/);
    assert.match(libWrites.filter(entry => entry.startsWith("POST /api/extensions/skills "))[0], /"enable":\["bot_inbox"\]/, "the chosen Bot ids are sent");
    const second = page$(page).locator(".skill-templates .integration-tile").first();
    await second.click();
    await pending.getByRole("button", {name: "Enable for all Bots"}).click();
    await page.waitForFunction(() => document.querySelectorAll('.settings-page-body[data-page="skills"] .skill-pending').length === 0);
    assert.match(libWrites.filter(entry => entry.startsWith("POST /api/extensions/skills "))[1], /"enable":"all"/, "one primary click installs for all Bots");
    assert.match(await text(page$(page).locator(".extension-notice")), /turned on for every Bot/);
    assert.equal(libWrites.filter(entry => entry.startsWith("POST /api/extensions/skills ")).length, 2);
    assert.ok(!(await page$(page).locator(".skill-templates").innerText()).includes(starterTitle), "an added template leaves the template list");
    await page$(page).getByRole("button", {name: "Paste SKILL.md"}).click();
    assert.equal(await page$(page).locator("form .primary-button").innerText(), "Enable for all Bots", "the paste form offers the same default");
    await page$(page).getByRole("button", {name: "Paste SKILL.md"}).click();
    // Add folder is a styled button over a hidden folder input; paste opens the small form.
    assert.equal(await page$(page).locator('input[type=file][webkitdirectory]').count(), 1);
    await page$(page).getByRole("button", {name: "Paste SKILL.md"}).click();
    await page$(page).getByLabel("Name").waitFor();
    await close();
    for (const lang of ["zh-CN", "de"]) {
      resetLib();
      const view = await open({tab: "skills", lang});
      await row(view.page, "weekly-report").waitFor();
      const copy = await text(row(view.page, "weekly-report"));
      assert.ok(!copy.includes("Updated") && !copy.includes("Added by"), `${lang}: the row is translated: ${copy}`);
      await view.close();
    }
    console.log("PASS skill library: installer and version per row, bundle expands without member Remove, rollback, confirmed Remove, one-click starter, translated");
  }

  // ---- 7d. Run health card on the Usage page: counts by status, named failure reasons, tool timeouts; no provider text.
  {
    const {page, close} = await open({tab: "usage"});
    const card = page.locator('.settings-page-body[data-page="usage"] .settings-sec').filter({hasText: "Run health"});
    await card.waitFor();
    const text = await card.innerText();
    for (const expected of ["Last 7 days", "41", "Completed", "4", "Failed", "Cancelled", "Running", "Model sign-in rejected", "Connection dropped", "Tool timeouts", "2"]) assert.ok(text.includes(expected), `run health shows ${expected}: ${text}`);
    assert.ok(!/model_auth_invalid|api error|invalid_api_key/i.test(text), "no raw codes or provider text");
    assert.equal(await card.getByRole("alert").count(), 0, "no error when the read succeeds");
    if (process.env.RUN_HEALTH_SHOT) await card.screenshot({path: process.env.RUN_HEALTH_SHOT});
    await close();
    const german = await open({tab: "usage", lang: "de"});
    await german.page.locator('.settings-page-body[data-page="usage"] .settings-sec').filter({hasText: "Ausführungsstatus"}).waitFor();
    await german.close();
    console.log("PASS run health: counts by status, named failure reasons, tool timeouts, no provider text, translated");
  }

  // ---- 7f. Connections open on the cached state: no animation, no spinner; the quiet re-check updates in place or flips to the problem.
  {
    const card = (page, name) => page.locator(`.mcp-service[aria-label="${name}"]`);
    const motion = ".web-mcp-plug, .mcp-service[data-state=\"checking\"], .mcp-service[data-state=\"authorizing\"]";
    const running = page => page.evaluate(() => document.getAnimations().filter(a => a.playState === "running" && a.effect?.target?.closest?.(".mcp-service")).length);
    // Slow background check: the first paint must already be the cached "Connected" line.
    const slow = await open({tab: "connections", testdelay: "1800"});
    const tracker = card(slow.page, "acme_tracker");
    await tracker.waitFor({timeout: 15000});
    const first = (await tracker.locator(".mcp-service-status").textContent()).trim();
    assert.match(first, /^Connected/, `cached status on first paint, got "${first}"`);
    assert.match(first, /\d+ tools?/, "the status line carries the tool count; the tools themselves are on the connection's page");
    assert.equal(await slow.page.locator(motion).count(), 0, "no test animation while the quiet check runs");
    assert.equal(await running(slow.page), 0, "nothing animates inside a connection card");
    assert.equal(await tracker.locator(".mcp-service-open").isDisabled(), false, "the row stays open-able during the quiet check");
    assert.equal(await tracker.getAttribute("data-state"), "ready");
    await slow.page.waitForFunction(() => /Checked/.test(document.querySelector('.mcp-service[aria-label="acme_tracker"] .mcp-service-status')?.textContent ?? ""), null, {timeout: 10000});
    assert.match(await tracker.locator(".mcp-service-checked").textContent(), /^Checked /, "tiny checked timestamp appears");
    assert.equal(await slow.page.locator(motion).count(), 0);
    await slow.close();
    // Failing background check: one quiet retry, then the problem state with a named reason.
    const before = requested.filter(line => /acme_tracker\/test$/.test(line)).length;
    const bad = await open({tab: "connections", testfail: "1"});
    const badTracker = card(bad.page, "acme_tracker");
    await badTracker.waitFor({timeout: 15000});
    assert.match((await badTracker.locator(".mcp-service-status").textContent()).trim(), /^Connected/, "starts from the cached state");
    await bad.page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="acme_tracker"]')?.getAttribute("data-state") === "error", null, {timeout: 12000});
    assert.match(await badTracker.locator(".mcp-service-status").textContent(), /Can.t connect/);
    assert.ok((await badTracker.locator(".mcp-service-error").textContent()).trim().length > 5, "a named reason is shown");
    assert.equal(requested.filter(line => /acme_tracker\/test$/.test(line)).length - before, 2, "one quiet retry before showing the problem");
    assert.equal(await bad.page.locator(motion).count(), 0);
    await bad.close();
    console.log("PASS connections: cached Connected on first paint, no animation or spinner, quiet re-check, failure flips after one retry");
    // Tool permissions: the connection's page groups tools by permission with markers, and the menu writes an owner override.
    const tiersPage = await open({tab: "connections"});
    const tiered = card(tiersPage.page, "acme_tracker");
    await tiered.waitFor({timeout: 15000});
    assert.equal(await tiersPage.page.locator(".mcp-tool-group").count(), 0, "a resting list shows no tool list");
    await tiered.locator(".mcp-service-open").click();
    const detail = tiersPage.page.locator(".mcp-detail");
    await detail.locator(".mcp-tool-group[data-tier=read_only]").waitFor();
    assert.deepEqual(await detail.locator(".mcp-tool-group[data-tier] h5").allTextContents(), ["Runs without asking2", "Asks when unsure2", "Always asks me2"], "three groups with counts");
    assert.deepEqual(await detail.locator(".mcp-tool[data-source=owner] code").allTextContents(), ["get_issue", "delete_issue", "post_comment"], "owner overrides are marked");
    assert.equal(await detail.locator(".mcp-tool[data-changed=true]").count(), 2, "changed tools are marked");
    assert.equal(await detail.locator(".mcp-tier-mark.is-changed").first().textContent(), "Changed");
    assert.equal(await detail.locator(".mcp-tool[data-source=provider] .mcp-tier-mark").first().textContent(), "Marked by the service");
    assert.equal(await detail.locator(".mcp-tool[data-source=default] .mcp-tier-mark:not(.is-changed)").count(), 1, "an unmarked tool carries no source marker; only the duplicated listing is marked");
    const selector = detail.getByRole("button", {name: "Permission for create_issue"});
    assert.equal(await detail.locator("select").count(), 0, "the permission is a menu, not a native select");
    await selector.click();
    assert.equal(await tiersPage.page.getByRole("menuitem", {name: /^Automatic/}).getAttribute("aria-current"), "true", "no override: Automatic is current");
    await tiersPage.page.getByRole("menuitem", {name: "Always asks me"}).click();
    await tiersPage.page.waitForTimeout(400);
    assert.deepEqual(tierWrites, [`PUT /api/extensions/mcp/acme_tracker/tools/create_issue {"tier":"high_risk"}`], "the menu writes exactly one owner override");
    assert.equal(await detail.getByRole("button", {name: "Refresh tools"}).textContent(), "Refresh tools", "the page refreshes the tool list; nothing classifies");
    assert.doesNotMatch(await tiersPage.page.locator('.mcp-service[aria-label="notes_search"] .mcp-service-status').textContent(), /Classifying/, "no classifying status anywhere");
    await detail.getByRole("button", {name: "Back to Connections"}).click();
    const docs = card(tiersPage.page, "acme_docs");
    await docs.locator(".mcp-service-open").click();
    await tiersPage.page.locator(".mcp-detail").waitFor();
    assert.equal(await tiersPage.page.locator(".mcp-detail .mcp-tool-group").count(), 0, "a connection without a tool list has a page (its source) but no tool groups");
    await tiersPage.close();
    console.log("PASS connections tools: opened from the row, permission groups, markers, overlay menu, one override write");
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
    // Server and Computer columns (S3): used / limit, red when the account reached its limit; a host disk line above the list.
    assert.match(await text(body.locator(".admin-row-head")), /Server\s*Computer/);
    assert.match(await text(row("acct_sample").locator(".admin-row-storage")), /^1\.5 GiB$/, "no limit set: used only");
    assert.match(await text(row("acct_two").locator(".admin-row-storage")), /^9\.5 GiB \/ 10 GiB$/);
    assert.match(await text(body), /80 GiB free of 500 GiB/);
    assert.match(await text(row("acct_two")), /Sam Example.*Member.*Active.*Ready · 8 GiB/);
    assert.match(await text(row("acct_three")), /Lee Example.*Member.*Deactivated.*Stopped · 8 GiB/);
    assert.match(await text(row("acct_four")), /Max Example.*No email set.*Deletion stopped/);
    assert.equal(await row("acct_two").locator(".admin-avatar").innerText(), "S");
    // The exports section: link copy for both, "Show passphrase" only while it was never acknowledged.
    assert.equal(await body.locator(".admin-export").count(), 3);
    const lost = body.locator('[data-export="exp_lost"]');
    assert.match(await text(lost), /Rae Example.*export key is missing or changed/);
    assert.equal(await lost.getByRole("button", {name: "Copy link"}).count(), 0, "no copy-link for a row whose link can't be rebuilt");
    assert.equal(await lost.getByRole("button", {name: "Show passphrase"}).count(), 0);
    assert.equal(await lost.getByRole("button", {name: "Delete export"}).count(), 1, "it can still be deleted");
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
    // Server storage limit: shows usage, saves a number of GiB, and "No limit" sends null.
    {
      const limit = detail.getByLabel("Storage limit");
      assert.equal(await limit.getAttribute("data-value"), "10");
      assert.match(await text(detail), /9\.5 GiB of 10 GiB/);
      assert.equal(await detail.getByRole("button", {name: "Save limit"}).isDisabled(), true, "nothing to save yet");
      adminWrites.length = 0;
      await limit.click();
      await page.locator('.ui-select-item[data-value="25"]').click();
      await page.locator(".ui-select-content").waitFor({ state: "detached" });
      await detail.getByRole("button", {name: "Save limit"}).click();
      await detail.getByText("Saved.").first().waitFor();
      assert.deepEqual(adminWrites, ['PATCH /api/admin/accounts/acct_two/quota {"server_bytes_limit":26843545600}']);
      await page.waitForFunction(() => { const select = document.querySelector("#admin-storage-limit"); return select && !select.disabled && select.dataset.value === "25"; });
      adminWrites.length = 0;
      await limit.click();
      await page.locator('.ui-select-item[data-value="unlimited"]').click();
      await page.locator(".ui-select-content").waitFor({ state: "detached" });
      await detail.getByRole("button", {name: "Save limit"}).click();
      await page.waitForFunction(() => document.querySelector("#admin-storage-limit")?.dataset.value === "unlimited");
      assert.deepEqual(adminWrites, ['PATCH /api/admin/accounts/acct_two/quota {"server_bytes_limit":null}']);
    }
    const deleteButton = detail.getByRole("button", {name: "Delete account…"});
    assert.equal(await deleteButton.isDisabled(), true, "Delete is disabled until the account is deactivated");
    assert.match(await text(detail), /Deactivate the account first\. Only a deactivated account can be deleted\./);
    // Computer size: the admin sets CPU, memory and disk in one sizeSheet with one Save; disk only grows.
    const sizeSheet = detail.locator(".resource-sheet");
    await sizeSheet.waitFor();
    assert.equal(await detail.getByLabel("Disk quota").count(), 0, "the old disk-only menu is gone");
    const saveSize = sizeSheet.getByRole("button", {name: "Save changes"});
    assert.equal(await saveSize.isDisabled(), true, "nothing to save yet");
    assert.match(await text(sizeSheet), /No changes yet/);
    assert.equal(await sizeSheet.getByRole("button", {name: "Decrease Disk (GB)"}).isDisabled(), true, "disk cannot shrink below the current size");
    assert.match(await text(sizeSheet), /120 GB free on host/, "host capacity is shown to the admin");
    await sizeSheet.getByRole("button", {name: "Increase CPU cores"}).click();
    await sizeSheet.getByRole("button", {name: "Increase CPU cores"}).click();
    await sizeSheet.getByRole("button", {name: "Increase Memory (GB)"}).click();
    await sizeSheet.getByRole("button", {name: "Increase Disk (GB)"}).click();
    const summary = await text(sizeSheet.locator(".resource-summary"));
    assert.match(summary, /CPU 2 → 4 cores/);
    assert.match(summary, /Memory 4 → 4\.5 GB/);
    assert.match(summary, /Allocated disk 8 → 9 GB/);
    assert.match(summary, /Takes effect after the computer restarts\./, "the restart notice");
    adminWrites.length = 0;
    await saveSize.click();
    await detail.getByText("Configuration saved. It applies after a restart.").waitFor();
    assert.deepEqual(adminWrites, ['PUT /api/admin/accounts/acct_two/resources {"vcpus":4,"memory_mib":4608,"disk_gib":9}']);
    // Computer: detach asks first, then attaches again; a failed attach shows inline.
    const attachCard = detail.getByTestId("computer-attach");
    await attachCard.getByText("Attached, running").waitFor();
    adminWrites.length = 0;
    await attachCard.getByRole("button", {name: "Detach…"}).click();
    const detachDialog = page.getByRole("alertdialog");
    await detachDialog.waitFor();
    const detachText = await text(detachDialog);
    assert.match(detachText, /stop after the current step/);
    assert.match(detachText, /copied to the server first/);
    assert.match(detachText, /keeps working without a computer/);
    await detachDialog.getByRole("button", {name: "Cancel"}).click();
    await detachDialog.waitFor({state: "detached"});
    assert.deepEqual(adminWrites, [], "cancelling the confirmation sends nothing");
    await attachCard.getByRole("button", {name: "Detach…"}).click();
    await detachDialog.getByRole("button", {name: "Detach", exact: true}).click();
    await attachCard.getByText("Computer detached. Files copied to the server: 3. Tool calls ended early: 1.").waitFor();
    assert.deepEqual(adminWrites, ['POST /api/admin/accounts/acct_two/computer/detach {"wait_seconds":60}']);
    assert.equal(await attachCard.getByTestId("computer-attach-state").textContent(), "Detached");
    adminState.failAttach = 1;
    adminWrites.length = 0;
    await attachCard.getByRole("button", {name: "Attach"}).click();
    await attachCard.getByText("The computer could not be attached.").waitFor();
    assert.equal(await attachCard.getByTestId("computer-attach-state").textContent(), "Detached", "a failed attach leaves the state");
    await attachCard.getByRole("button", {name: "Attach"}).click();
    await attachCard.getByText("Computer attached.").waitFor();
    assert.deepEqual(adminWrites, ["POST /api/admin/accounts/acct_two/computer/attach", "POST /api/admin/accounts/acct_two/computer/attach"]);
    assert.equal(await attachCard.getByTestId("computer-attach-state").textContent(), "Attached, running");
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
    assert.match(message, /^Your TOFI account was closed\. Download your Bots: http.*\/exports\/C+ \(passphrase sent separately\), available until November 8, 2026\. Import it in Tofi: Settings → Maintenance → Data transfer, then enter the passphrase\.$/);
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
    // The list refreshes after the sheet closes (AdminAccounts onClose -> refresh()); counting at once raced it.
    await body.locator('[data-export="exp_new"]').getByRole("button", {name: "Show passphrase"}).waitFor({state: "detached", timeout: 5000}).catch(() => {});
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

  // ---- 7d. Storage card states (S3): full, unlimited, translated, phone width.
  {
    const full = await open({storage: "full"});
    const fullCard = full.page.locator(".storage-card");
    await fullCard.waitFor();
    assert.match((await fullCard.innerText()).replace(/\s+/g, " "), /10 GiB of 10 GiB.*Storage is full\. Delete files or ask the administrator\./);
    assert.equal(await fullCard.locator(".storage-bar.is-full").count(), 1, "the bar shows the full state");
    await full.close();
    const open_ = await open({storage: "unlimited"});
    const card = open_.page.locator(".storage-card");
    await card.waitFor();
    assert.match((await card.innerText()).replace(/\s+/g, " "), /1\.5 GiB used\s*No limit/);
    assert.equal(await card.getByRole("progressbar").getAttribute("aria-valuenow"), null, "no limit: no percentage");
    assert.ok(!/Storage is full/.test(await card.innerText()));
    await open_.close();
    for (const lang of LANGS.filter(item => item !== "en")) {
      const view = await open({lang});
      await view.page.locator(".storage-card").waitFor();
      const heading = (await view.page.locator(".storage-card").locator("xpath=ancestor::section[1]").locator("h4").textContent()).trim();
      assert.ok(heading && heading !== "Storage", `${lang}: the storage heading is translated (got ${heading})`);
      await view.close();
    }
    const phone = await open({}, {width: 390, height: 844});
    await phone.page.locator('.settings-mhome [data-tab="general"]').click();
    await phone.page.locator(".storage-card").waitFor();
    assert.equal(await phone.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll at 390px");
    await phone.close();
    console.log("PASS storage: bar, legend, computer line and breakdown; full and unlimited states; 7 locales; 390px");
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
    assert.ok(catalogs.en.admin.result.message.includes("Settings → Maintenance → Data transfer, then enter the passphrase."));
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
    // The size of the computer is the admin's to set: a member sees CPU, memory and disk, with no way to change them and no host limits.
    await member.page.locator('.settings-tabs [data-tab="computer"]').click();
    const memberComputer = member.page.locator('.settings-page-body[data-page="computer"]');
    await memberComputer.locator(".resource-grid").waitFor({timeout: 15000});
    assert.deepEqual((await memberComputer.locator(".resource-grid > div").allInnerTexts()).map(item => item.replace(/\s+/g, " ").trim().toUpperCase()), ["CPU 2 CORES", "MEMORY 4 GB", "ALLOCATED DISK 20 GB"]);
    assert.match(await memberComputer.innerText(), /Your admin sets the size of this computer\./);
    assert.equal(await memberComputer.getByRole("button", {name: "Adjust"}).count(), 0, "no Adjust button for a member");
    assert.equal(await memberComputer.locator(".resource-sheet").count(), 0, "no edit sheet for a member");
    assert.equal(await memberComputer.getByRole("button", {name: "Open Admin console"}).count(), 0, "a member has no console link");
    await member.close();

    // An admin of a multi-account server is pointed to the Admin console; the single-account owner sets the size here.
    const consoleAdmin = await open({admin: "1", tab: "computer"});
    const adminComputer = consoleAdmin.page.locator('.settings-page-body[data-page="computer"]');
    await adminComputer.getByRole("button", {name: "Open Admin console"}).waitFor({timeout: 15000});
    assert.equal(await adminComputer.getByRole("button", {name: "Adjust"}).count(), 0, "no Adjust for a multi-account admin");
    await consoleAdmin.close();
    const soloOwner = await open({admin: "0", tab: "computer"});
    const ownerComputer = soloOwner.page.locator('.settings-page-body[data-page="computer"]');
    // The single-account owner has the same card, on the /api/admin/computer route.
    const soloCard = ownerComputer.getByTestId("computer-attach");
    await soloCard.getByText("Attached, running").waitFor({timeout: 15000});
    adminWrites.length = 0;
    await soloCard.getByRole("button", {name: "Detach…"}).click();
    await soloOwner.page.getByRole("alertdialog").getByRole("button", {name: "Detach", exact: true}).click();
    await soloCard.getByText(/Computer detached\./).waitFor();
    assert.deepEqual(adminWrites, ['POST /api/admin/computer/detach {"wait_seconds":60}']);
    await soloCard.getByRole("button", {name: "Attach"}).click();
    await soloCard.getByText("Computer attached.").waitFor();
    await ownerComputer.getByRole("button", {name: "Adjust"}).click();
    await ownerComputer.locator(".resource-sheet").waitFor();
    await soloOwner.close();

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


    // ---- 10b. Update from the page: confirm, progress through the restart, the ending, the release candidate row.
    {
      const banner = '[data-update-banner]';
      updateState.latest = "v0.1.1";
      resetUpdateFlow("done");
      const flow = await open({admin: "1", host: "1"});
      await flow.page.locator(banner).waitFor({timeout: 15000});
      assert.match((await flow.page.locator(banner).innerText()).replace(/\s+/g, " "), /Tofi v0\.1\.1 is available\./);
      assert.doesNotMatch(await flow.page.locator(banner).innerText(), /sudo tofi update/, "the button replaces the command");
      const button = flow.page.getByRole("button", {name: "Update to v0.1.1"});
      assert.equal(await flow.page.locator("[data-update-dialog]").count(), 0, "no dialog before the press");
      await button.click();
      const dialog = flow.page.locator('[data-update-dialog="stable"]');
      await dialog.waitFor();
      assert.match(await dialog.innerText(), /Update to v0\.1\.1\?/);
      assert.match(await dialog.innerText(), /Bots that are running finish first/);
      assert.equal(await dialog.locator("a").getAttribute("href"), "https://github.com/example/tofibot/releases/tag/v0.1.1");
      assert.equal(await dialog.getByText("Release candidate").count(), 0, "a stable release carries no candidate badge");
      await flow.page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      assert.equal(updateFlow.posted.length, 0, "cancelling sends nothing");
      await button.click();
      await dialog.getByRole("button", {name: "Update now"}).click();
      await flow.page.locator("[data-update-running]").waitFor();
      assert.deepEqual(updateFlow.posted.map(value => JSON.parse(value)), [{version: "v0.1.1", channel: "stable"}]);
      const seen = new Set();
      const done = flow.page.locator('[data-update-state="done"]');
      for (let i = 0; i < 80 && !(await done.count()); i++) {
        const state = await flow.page.locator("[data-update-running] [data-update-state]").first().getAttribute("data-update-state").catch(() => null);
        if (state) seen.add(state);
        await flow.page.waitForTimeout(250);
      }
      assert.ok(await done.count(), `the page reaches the done state (saw ${[...seen]})`);
      for (const state of ["waiting_for_runs", "downloading", "switching", "restarting"]) assert.ok(seen.has(state), `the progress shows ${state} (saw ${[...seen]})`);
      assert.match((await done.innerText()).replace(/\s+/g, " "), /Updated to v0\.1\.1 Reload/);
      assert.equal(await flow.page.getByRole("button", {name: "Dismiss update notice"}).count(), 0, "a finished update has no dismiss button; Reload is the way on");
      await flow.close();

      // A refusal shows its code, what it means and that nothing changed; a rejected candidate shows the rollback.
      for (const [mode, expect] of [["fail", [/Bots were still running after an hour/, /Nothing was changed\./, /Code runs_still_active/]], ["rollback", [/The new version failed its health check/, /Rolled back to v0\.1\.0\./, /Code update_rejected/]]]) {
        resetUpdateFlow(mode);
        const failed = await open({admin: "1", host: "1"});
        await failed.page.getByRole("button", {name: "Update to v0.1.1"}).click();
        await failed.page.locator('[data-update-dialog]').getByRole("button", {name: "Update now"}).click();
        const text = failed.page.locator('[data-update-state="failed"]');
        await text.waitFor({timeout: 30000});
        const copy = (await text.innerText()).replace(/\s+/g, " ");
        for (const pattern of expect) assert.match(copy, pattern, mode);
        await failed.page.getByRole("button", {name: "Dismiss"}).click();
        assert.equal(await failed.page.locator('[data-update-state="failed"]').count(), 0, "dismissed");
        await failed.close();
      }

      // Release candidates: the switch is in the Version card; with it on, the candidate has its own button and badge.
      resetUpdateFlow("done");
      updateFlow.rc = false;
      const rc = await open({admin: "1", host: "1", tab: "advanced"});
      await rc.page.locator(".version-card").waitFor({timeout: 15000});
      assert.equal(await rc.page.getByRole("button", {name: /^Try v/}).count(), 0, "no candidate offered while the setting is off");
      const toggle = rc.page.getByRole("switch", {name: "Receive release candidates"});
      assert.equal(await toggle.isChecked(), false, "off by default");
      await toggle.click();
      await rc.page.getByRole("button", {name: "Try v0.2.0-rc.1"}).waitFor();
      assert.deepEqual(updateFlow.rcWrites, ['{"channel_rc":true}']);
      assert.match((await rc.page.locator(".version-card").innerText()).replace(/\s+/g, " "), /Latest release candidate v0\.2\.0-rc\.1/);
      await rc.page.getByRole("button", {name: "Try v0.2.0-rc.1"}).click();
      const rcDialog = rc.page.locator('[data-update-dialog="rc"]');
      await rcDialog.waitFor();
      assert.match(await rcDialog.innerText(), /Release candidate/);
      assert.match(await rcDialog.innerText(), /a test build ahead of the stable release/);
      await rcDialog.getByRole("button", {name: "Cancel"}).click();
      await rcDialog.waitFor({state: "detached"});
      assert.equal(await rc.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
      await rc.close();
      updateFlow.rc = false;
      resetUpdateFlow("done");

      // 390 px: the progress and the dialog fit.
      const narrowFlow = await open({admin: "1", host: "1", tab: "advanced"}, {width: 390, height: 844});
      await narrowFlow.page.locator(".settings-mhome [data-tab=\"advanced\"]").click().catch(() => {});
      await narrowFlow.page.locator(".version-card").waitFor({timeout: 15000});
      assert.equal(await narrowFlow.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll with the Update row at 390px");
      await narrowFlow.page.locator(".version-card").getByRole("button", {name: "Update to v0.1.1"}).click();
      const narrowDialog = narrowFlow.page.locator('[data-update-dialog="stable"]');
      await narrowDialog.waitFor();
      assert.equal(await narrowFlow.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll with the dialog open at 390px");
      const box = await narrowDialog.boundingBox();
      assert.ok(box && box.x >= 0 && box.x + box.width <= 390, "the dialog fits the phone width");
      await narrowFlow.close();

      // Dark theme, ja.
      const ja = await open({admin: "1", host: "1", lang: "ja"}, {theme: "dark"});
      await ja.page.getByRole("button", {name: "v0.1.1 に更新"}).waitFor({timeout: 15000});
      await ja.close();
      updateState.latest = "v0.1.1";
      console.log("PASS update from the page: confirm dialog, progress through the restart, refusal and rollback endings, release-candidate switch and button, 390px, dark, ja");
    }

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
  // The update flow's own writes were asserted in section 10b (bodies, count, channel).
  const unexpectedWrites = writes.filter(line => !/^(POST \/api\/system\/update\/request|PUT \/api\/system\/update\/settings)$/.test(line));
  assert.deepEqual(unexpectedWrites, [], `no writes expected: ${unexpectedWrites.join("; ")}`);
  console.log("PASS no unstubbed /api request, no write, no page error");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
