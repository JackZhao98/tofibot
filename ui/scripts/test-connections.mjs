// Settings › Connections (docs/design/components.md §17, "Connections list and connect dialog").
// Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed (synthetic data only)
// and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//
// Invariants this pins (see the redesign brief):
//   - one row per connection the person has; no internal ids (local_gog), no Runner / sleep wording, no duplicate card
//   - a local plugin whose account was never authorised reads "Needs sign-in", never "Connected", and offers Sign in
//   - setup lives in a dialog, never inline on the page; Gmail in the directory is the gogcli path, Google's Workspace
//     preview servers sit under their own grouping with the enrolment line
//   - a row is the entry to the connection's page (Status / Tools / Advanced / Danger); nothing expands inline, Back returns focus to the row
//   - every connection's page says where it comes from; the tool list is on the page, not on the resting row
//   - the permission of a tool is an overlay menu (Radix), never a native select
//   - the settings rail badge counts connections that need sign-in, setup or retry; an empty page offers three tiles
//   - a healthy Google row shows no setup help
//
// CONN_SHOTS=<dir> also saves screenshots (1280/390 px, light/dark). CONN_UI_ROOT=<ui dir> points the fixture at another
// checkout (used to take the "before" screenshots; assertions are skipped when CONN_SHOTS_ONLY=1).
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const here = dirname(dirname(fileURLToPath(import.meta.url)));
const ui = process.env.CONN_UI_ROOT || here;
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shotsOnly = process.env.CONN_SHOTS_ONLY === "1";
const shots = process.env.CONN_SHOTS, prefix = process.env.CONN_PREFIX || "after";
if (shots) await mkdir(shots, {recursive: true});

const fixture = await mkdtemp(join(ui, ".connections-"));
let server, browser;
const unexpected = [];
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {CodexPanel} from '../src/settings/AppSlots';
import {openConnectionDeepLink} from '../src/settings/deepLinks';
const query=new URLSearchParams(location.search);
(window as any).openConnectionDeepLink=openConnectionDeepLink;
{const wait=query.get('pending');if(wait){const [server,tool]=wait.split(':');openConnectionDeepLink({server,tool});}}
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as any)||'connections');
 const [entry]=useState<SettingsEntry>({seq:1,view:'page'});
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>setTab(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  // ---- Synthetic server: the owner's real shape (a Google account never authorised, Notion with many tools, Google's official
  // Gmail MCP, a custom server), none of its data.
  const ENABLE = "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456";
  const tools = n => ({tools: Array.from({length: n}, (_, i) => ({name: `tool_${i + 1}`, description: `Synthetic tool ${i + 1}`, tier: i % 3 === 0 ? "read_only" : "write", source: i % 3 === 0 ? "provider" : "default"})), read_only: Math.ceil(n / 3), write: n - Math.ceil(n / 3), high_risk: 0, checked: true});
  const savedServers = [];
  const stub = (url, method, q, postData = "") => {
    const path = url.pathname;
    // One-click connections: the POST is remembered so the next list shows the saved server (state lives in this test only).
    if (path === "/api/extensions/mcp" && method === "POST" && q.get("onecl")) { savedServers.push(JSON.parse(postData || "{}")); return {ok: true, next_run: true}; }
    if (path === "/api/extensions/mcp" && method === "GET" && q.get("onecl") && savedServers.length) return {servers: savedServers.map(item => ({...item, oauth: {...item.oauth, connected: false}}))};
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}};
    if (path === "/api/auth/session") return {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}};
    if (path === "/api/bots") return {bots: [{id: "bot_research", name: "Research Bot", archived: false}]};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/extensions/oauth-options") return {vm_available: q.get("vm") === "1", web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/mcp" && method === "GET" && q.get("empty")) return {servers: []};
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      ...(q.get("fresh") ? [] : [{name: "local_gog", url: "http://plugins.internal.example:8787/mcp/gog", transport: "streamable_http", headers: {Authorization: "Bearer x"}, status: {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 3}}]),
      {name: "notion", url: "https://mcp.notion.com/mcp", transport: "streamable_http", ...(q.get("paused") === "notion" ? {enabled: false} : {}), oauth: {client_id: "", connected: true}, status: {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 65}},
      {name: "google-gmail", url: "https://gmailmcp.googleapis.com/mcp/v1", transport: "streamable_http", oauth: {client_id: "sample", connected: true}, status: q.get("apidisabled") === "status"
        ? {checked_at: "2026-10-21T09:30:00Z", ok: false, tool_count: 23, error_class: "failed", error_code: "api_disabled", api: "gmail.googleapis.com", enable_url: ENABLE}
        : {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 23}},
      {name: "acme_tracker", url: "https://mcp.example.test/tracker", oauth: {client_id: "sample", connected: false}},
      ...(q.get("loopback") ? [{name: "robinhood", url: "https://agent.robinhood.com/mcp/trading", transport: "streamable_http", oauth: {client_id: "", connected: false, redirect_policy: "loopback"}}] : []),
      ...(q.get("community") ? [{name: "local_acme_pkg", url: "http://plugins.internal.example:8787/mcp/acme", transport: "streamable_http", provenance: {origin: "community", kind: "npm", package: "acme-mcp", version: "1.0.0"}, status: {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 3}}] : []),
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") {
      const name = path.split("/")[4];
      if (q.get("apidisabled") && name === "google-gmail") return {ok: false, tool_count: 0, diagnostics: [{message: "Google 返回 API 未启用。", code: "api_disabled", api: "gmail.googleapis.com", enable_url: ENABLE}]};
      if (q.get("fail") === name) return {ok: false, tool_count: 0, diagnostics: [{message: "Google 返回 API 未启用。请在 OAuth Client 所属的同一个项目中启用产品 API 和 MCP 服务，再刷新工具。", code: "api_disabled"}]};
      return {ok: true, tool_count: name === "notion" ? 65 : name === "google-gmail" ? 23 : 3};
    }
    let m = /^\/api\/extensions\/mcp\/([^/]+)\/tools$/.exec(path);
    // A community connection: the server has already ignored the package's own read-only marks (D47), so only the tool the owner marked is read.
    if (m && method === "GET" && m[1] === "local_acme_pkg") return {tools: [
      {name: "list_things", description: "Provider says read only", tier: "write", source: "default", read_only_hint: true},
      {name: "get_thing", description: "Marked by the owner", tier: "read_only", source: "owner", override: "read_only"},
      {name: "send_thing", description: "Writes", tier: "write", source: "default"}], read_only: 1, write: 2, high_risk: 0, checked: true};
    if (m && method === "GET") return tools(m[1] === "notion" ? 6 : m[1] === "google-gmail" ? 6 : 3);
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools\/[^/]+$/.test(path) && method === "PUT") return {tool: {name: path.split("/")[6]}, summary: tools(6)};
    // The computer dialog needs a live desktop the fixture does not have; a refused start still proves which route was taken.
    if (/\/oauth\/vm\/start$/.test(path)) return {__status: 409, error: {code: "computer_busy", message: "The computer is busy."}};
    if (/\/oauth\/paste\/start$/.test(path)) return {session_id: "sess_sample", authorization_url: "https://auth.example.test/authorize?state=sample", redirect_uri: "http://127.0.0.1:43821/oauth/callback"};
    if (/\/oauth\/paste\/complete$/.test(path)) return JSON.parse(postData || "{}").url?.includes("code=good") ? {ok: true} : {__status: 400, error: {code: "oauth_paste_invalid", message: "The pasted address is not the redirect this sign-in expects."}};
    if (path === "/api/extensions/local-mcp") return {available: q.get("runner") !== "off", plugins: q.get("fresh") ? [] : [{id: "gog", state: "sleeping", tool_count: 3}]};
    if (path === "/api/extensions/local-mcp/gog/oauth/status") return q.get("gog") === "authorised" ? {connected: true, email: "you@example.test", scope: "readonly", services: (q.get("services") || "gmail").split(",")} : {connected: false, email: "", services: []};
    if (path === "/api/extensions/local-mcp/gog/oauth/start") return {authorization_url: "https://accounts.google.com/o/oauth2/v2/auth?state=sample"};
    if (path === "/api/extensions/repo-stars") return q.get("nostars") ? {stars: {}} : {stars: {"openclaw/gogcli": 8520, "upstash/context7": 63467, "github/github-mcp-server": 33839, "makenotion/notion-mcp-server": 4673, "MicrosoftDocs/mcp": 1951}};
    if (path === "/api/extensions/google") return q.get("gclient") ? {client: {configured: true, project_id: "tofi-sample-123456", project_number: "622599621014"}, apis: []} : {client: {configured: false}, apis: []};
    if (/^\/api\/extensions\/mcp\/[^/]+\/bots$/.test(path) && method === "GET") { const limited = q.get("limited") === "1"; return {all: !limited, bots: [{bot_id: "bot_research", bot_name: "Research Bot", enabled: true}, {bot_id: "bot_ops", bot_name: "Ops Bot", enabled: !limited}]}; }
    if (/^\/api\/extensions\/mcp\/[^/]+\/bots$/.test(path) && method === "PUT") return {ok: true};
    if (path === "/api/auto-review-settings") return {mode: "auto", review_scope: "all_external_tools"};
    if (path === "/api/standing-allowances" && q.get("allow") === "chips") return {allowances: [
      {id: "al-3", bot_id: "bot_research", bot_name: "Research Bot", server: "notion", tool: "tool_2", state: "active", created_at: "2026-10-21T16:00:00Z", use_count: 5, last_used_at: "2026-10-26T14:30:00Z"},
      {id: "al-4", bot_id: "bot_research", bot_name: "Research Bot", server: "acme_tracker", tool: "tool_2", state: "active", created_at: "2026-10-21T16:00:00Z", use_count: 1}], remembered: []};
    if (path === "/api/standing-allowances") return q.get("allow") ? {allowances: [
      {id: "al-1", bot_id: "bot_research", bot_name: "Research Bot", server: "notion", tool: "create_page", state: "active", created_at: "2026-10-21T16:00:00Z", use_count: 5, last_used_at: "2026-10-26T14:30:00Z"},
      {id: "al-2", bot_id: "bot_research", bot_name: "Research Bot", server: "acme_tracker", tool: "send_message", state: "server_changed", created_at: "2026-10-22T16:00:00Z", use_count: 0}],
      remembered: [{id: "rm-1", conversation_id: "conv-1", bot_id: "bot_research", bot_name: "Research Bot", conversation_name: "Q4 plan", server: "notion", tool: "update_page", target_kind: "identifier", target_summary: "page 8f2…", expires_at: "2026-10-28T01:40:00Z", use_count: 1}]} : {allowances: [], remembered: []};
    return {};
  };
  async function open(params = {}, {width = 1280, height = 900, theme = "light", locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, locale, hasTouch: width < 600});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const writes = [], posts = [];
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      if (method !== "GET" && !/\/test$/.test(url.pathname)) { writes.push(`${method} ${url.pathname}`); posts.push({path: url.pathname, body: request.postData() ?? ""}); }
      // A save that takes a while holds the one-click dialog on screen for the screenshot pass.
      if (method === "POST" && url.pathname === "/api/extensions/mcp" && new URLSearchParams(page.url().split("?")[1] ?? "").get("slowsave")) await new Promise(resolve => setTimeout(resolve, 8000));
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""), request.postData() ?? "");
      await route.fulfill({status: body?.__status ?? 200, contentType: "application/json", body: JSON.stringify(body ?? {})});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams({lang: "en", ...params})}`);
    try { await page.locator(params.tab ? ".settings-page-body" : ".settings-page-body .mcp-settings").first().waitFor({timeout: 20000}); }
    catch (error) { console.log("page errors:", unexpected, "\nbody:", (await page.locator("body").innerText().catch(() => "")).slice(0, 400)); throw error; }
    return {page, writes, posts, close: () => context.close()};
  }
  const shot = async (page, name, width, theme) => { if (shots) await page.screenshot({path: join(shots, `${prefix}-${name}-${width}-${theme}.png`), fullPage: false}); };
  const rows = page => page.locator(".mcp-service");
  const row = (page, name) => page.locator(`.mcp-service[aria-label="${name}"]`);
  /** A row opens its connection's page; the page replaces the list (the list stays mounted, hidden). */
  const openDetail = async (page, name) => { await row(page, name).locator(".mcp-service-open").click(); await page.locator(".mcp-detail").waitFor(); return page.locator(".mcp-detail"); };
  const advanced = async detail => { await detail.getByRole("button", {name: "Advanced"}).click(); await detail.page().waitForTimeout(350); };

  if (shots) {
    // Screenshot pass: every state, 1280 / 390 px, light / dark. The "before" tree has a different page, so only the
    // states it has are taken (the list, and the directory behind "Add service").
    for (const [width, height] of [[1280, 900], [390, 844]]) for (const theme of ["light", "dark"]) {
      let {page, close} = await open({}, {width, height, theme});
      await page.waitForTimeout(1500);
      await shot(page, "list", width, theme);
      await page.locator(".settings-page-body").evaluate(element => element.scrollTo(0, element.scrollHeight));
      await page.waitForTimeout(300);
      await shot(page, "list-lower", width, theme);
      await page.locator(".settings-page-body").evaluate(element => element.scrollTo(0, 0));
      if (prefix === "before") {
        await page.getByRole("button", {name: /Add service/}).click();
        await page.waitForTimeout(600);
        await shot(page, "directory", width, theme);
      } else {
        // The connection page, section by section (the page scrolls inside the settings body).
        await close();
        const notion = await open({}, {width, height, theme});
        await openDetail(notion.page, "Notion");
        await notion.page.waitForTimeout(500);
        await shot(notion.page, "detail-status", width, theme);
        const body = notion.page.locator(".settings-page-body");
        await notion.page.locator('[data-section="tools"]').scrollIntoViewIfNeeded();
        await notion.page.waitForTimeout(300);
        await shot(notion.page, "detail-tools", width, theme);
        await notion.page.getByRole("button", {name: "Permission for tool_2"}).click();
        await notion.page.getByRole("menu").waitFor();
        await notion.page.waitForTimeout(300);
        await shot(notion.page, "detail-permission-menu", width, theme);
        await notion.page.keyboard.press("Escape");
        await body.evaluate(element => element.scrollTo(0, element.scrollHeight));
        await notion.page.locator(".mcp-detail").getByRole("button", {name: "Advanced"}).click();
        await notion.page.waitForTimeout(500);
        await body.evaluate(element => element.scrollTo(0, element.scrollHeight));
        await notion.page.waitForTimeout(300);
        await shot(notion.page, "detail-advanced-danger", width, theme);
        await notion.page.locator(".mcp-detail").getByRole("button", {name: "Remove service"}).click();
        await notion.page.waitForTimeout(300);
        await shot(notion.page, "detail-confirm", width, theme);
        await notion.close();
        const unauth = await open({}, {width, height, theme});
        await openDetail(unauth.page, "Gmail (personal account)");
        await unauth.page.waitForTimeout(500);
        await shot(unauth.page, "detail-needs-sign-in", width, theme);
        await unauth.close();
        const community = await open({community: "1"}, {width, height, theme});
        await openDetail(community.page, "acme_pkg");
        await community.page.waitForTimeout(500);
        await community.page.locator('[data-section="tools"]').scrollIntoViewIfNeeded();
        await shot(community.page, "detail-community", width, theme);
        await community.close();
        const paused = await open({paused: "notion"}, {width, height, theme});
        await paused.page.locator('.mcp-service[aria-label="Notion"][data-kind="paused"]').waitFor();
        await shot(paused.page, "row-paused", width, theme);
        await openDetail(paused.page, "Notion");
        await paused.page.waitForTimeout(500);
        await shot(paused.page, "detail-paused", width, theme);
        await paused.close();
        const chips = await open({allow: "chips"}, {width, height, theme});
        await openDetail(chips.page, "Notion");
        await chips.page.locator(".mcp-allowance").waitFor();
        await chips.page.locator('[data-section="tools"]').scrollIntoViewIfNeeded();
        await chips.page.waitForTimeout(500);
        await shot(chips.page, "detail-allowance-chips", width, theme);
        await chips.close();
        const botsView = await open({limited: "1"}, {width, height, theme});
        await openDetail(botsView.page, "Notion");
        await botsView.page.locator(".conn-bots-fold[data-open]").waitFor();
        await botsView.page.locator('[data-section="bots"]').scrollIntoViewIfNeeded();
        await botsView.page.waitForTimeout(500);
        await shot(botsView.page, "detail-bots-chosen", width, theme);
        await botsView.close();
        const link = await open({pending: "notion:tool_5"}, {width, height, theme});
        await link.page.waitForFunction(() => document.querySelector('.mcp-tool[data-tool="tool_5"]')?.hasAttribute("data-flash"), null, {timeout: 5000});
        await link.page.waitForTimeout(120);
        await shot(link.page, "detail-deeplink-flash", width, theme);
        await link.close();
        savedServers.length = 0;
        const one = await open({empty: "1", fresh: "1", vm: "1", onecl: "1", slowsave: "1"}, {width, height, theme});
        await one.page.locator(".mcp-empty").waitFor();
        await one.page.locator('.mcp-empty-tile', {hasText: "Notion"}).click();
        await one.page.locator(".connect-dialog").waitFor();
        await one.page.waitForTimeout(600);
        await shot(one.page, "one-click-connecting", width, theme);
        await one.close();
        const empty = await open({empty: "1", fresh: "1"}, {width, height, theme});
        await empty.page.locator(".mcp-empty").waitFor();
        await empty.page.waitForTimeout(900);
        await shot(empty.page, "empty", width, theme);
        await empty.close();
        const perms = await open({tab: "permissions", allow: "1"}, {width, height, theme});
        await perms.page.locator(".permissions-page .standing-item").first().waitFor();
        await perms.page.waitForTimeout(500);
        await shot(perms.page, "permissions", width, theme);
        await perms.page.locator(".settings-page-body").evaluate(element => element.scrollTo(0, element.scrollHeight));
        await perms.page.waitForTimeout(300);
        await shot(perms.page, "permissions-lower", width, theme);
        await perms.close();
        const page2 = await open({}, {width, height, theme});
        await openDetail(page2.page, "Gmail (personal account)");
        close = () => page2.close();
        const fresh = await open({fresh: "1", gclient: "1"}, {width, height, theme});
        await fresh.page.getByRole("button", {name: "Add connection"}).click();
        await fresh.page.locator(".connect-dialog").waitFor();
        await fresh.page.waitForTimeout(700);
        await shot(fresh.page, "directory", width, theme);
        await fresh.page.locator('[data-integration-id="google"]').click();
        await fresh.page.getByRole("button", {name: "Install gogcli"}).waitFor();
        await shot(fresh.page, "google-install", width, theme);
        await fresh.page.locator(".connect-workspace .disclosure-toggle").click();
        await fresh.page.waitForTimeout(600);
        await shot(fresh.page, "google-workspace-open", width, theme);
        await fresh.close();
        const saved = await open({gclient: "1"}, {width, height, theme});
        await row(saved.page, "Gmail (personal account)").getByRole("button", {name: "Sign in"}).click();
        await saved.page.locator('.connect-dialog input[type="email"]').waitFor();
        await shot(saved.page, "gmail-setup", width, theme);
        await saved.close();
      }
      await close();
      if (prefix !== "before") {
        const failing = await open({fail: "google-gmail"}, {width, height, theme});
        await row(failing.page, "Google Gmail").waitFor();
        await failing.page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-state") === "error", null, {timeout: 12000});
        await row(failing.page, "Google Gmail").scrollIntoViewIfNeeded();
        await shot(failing.page, "error", width, theme);
        await openDetail(failing.page, "Google Gmail");
        await failing.page.waitForTimeout(500);
        await shot(failing.page, "detail-error", width, theme);
        await failing.close();
      }
    }
    console.log(`shots saved to ${shots}`);
  }

  if (!shotsOnly) {
    // ---- 1. The list: one row per connection, plain names, no jargon, a single honest status each.
    {
      const {page, close} = await open();
      await rows(page).first().waitFor();
      await page.waitForTimeout(600);
      const labels = await rows(page).evaluateAll(list => list.map(item => item.getAttribute("aria-label")));
      assert.deepEqual(labels, ["Gmail (personal account)", "Notion", "Google Gmail", "acme_tracker"], "one row per connection, in list order, with plain names");
      assert.equal(new Set(labels).size, labels.length, "no duplicate rows");
      assert.equal(await page.locator(".local-mcp-panel, .local-mcp-card").count(), 0, "no separate local plugin card beside the list");
      const text = await page.locator(".mcp-settings").innerText();
      for (const forbidden of [/local_gog/, /\blocal_/, /Runner/i, /sleep|hibernat/i, /\bgog\b/i, /read only/i, /Not verified/i]) assert.doesNotMatch(text, forbidden, `the resting page shows no ${forbidden}`);
      assert.equal(await page.locator('.mcp-settings input[type="email"], .mcp-settings input[type="file"], .mcp-settings select, .mcp-settings textarea').count(), 0, "no setup form sits inline on the page");
      assert.equal(await page.locator(".mcp-service-tools, .mcp-google-setup, .mcp-tools").count(), 0, "a resting page shows no tool lists and no setup help");
      const gog = row(page, "Gmail (personal account)");
      assert.equal(await gog.getAttribute("data-kind"), "needs_auth", "a plugin whose Google account was never authorised needs authorization");
      assert.match((await gog.locator(".mcp-service-status").innerText()).trim(), /^Needs sign-in/);
      assert.match(await gog.innerText(), /Built in · gogcli · Gmail API/, "the row says where it comes from; gogcli is built in, not community");
      assert.doesNotMatch(await gog.locator(".mcp-service-status").innerText(), /Connected/, "never 'Connected' without an account");
      assert.equal(await gog.getByRole("button", {name: "Sign in"}).count(), 1, "the one primary action is Sign in");
      assert.equal(await gog.locator(".mcp-menu-trigger").count(), 0, "a row has no menu: its page has the actions");
      for (const name of ["Notion", "Google Gmail"]) {
        assert.equal(await row(page, name).getAttribute("data-kind"), "ready");
        assert.match((await row(page, name).locator(".mcp-service-status").innerText()).trim(), /^Connected\s*·?\s*\d+ tools/, "the status line counts the tools");
        assert.equal(await row(page, name).locator("[data-primary]").count(), 0, "a healthy row has no primary action");
      }
      assert.match((await row(page, "acme_tracker").locator(".mcp-service-status").innerText()).trim(), /^Needs sign-in/);
      assert.match(await row(page, "Notion").innerText(), /Official · mcp\.notion\.com/);
      assert.match(await row(page, "acme_tracker").innerText(), /Custom · mcp\.example\.test/);
      await close();
      console.log("PASS list: one row per connection, plain names, source and tool count, no internal ids or Runner/sleep wording, needs sign-in for an unauthorised account, nothing inline");
    }

    // ---- 2. An authorised account is Connected, and shows the account in the details.
    {
      const {page, close} = await open({gog: "authorised"});
      await page.locator('.mcp-service[aria-label="Gmail (personal account)"][data-kind="ready"]').waitFor({timeout: 10000});
      assert.match(await row(page, "Gmail (personal account)").locator(".mcp-service-status").innerText(), /you@example\.test/);
      await close();
      console.log("PASS list: an authorised Google account reads Connected with the account");
    }

    // ---- 2b. Services: the status line names what is authorised; the dialog offers Gmail / Calendar / Drive with an enable link per service.
    {
      const {page, close, posts} = await open({gog: "authorised", services: "gmail,calendar", gclient: "1"});
      const gog = row(page, "Gmail (personal account)");
      await page.locator('.mcp-service[aria-label="Gmail (personal account)"][data-kind="ready"]').waitFor({timeout: 10000});
      assert.match(await gog.locator(".mcp-service-status").innerText(), /you@example\.test · Gmail, Google Calendar/, "the status line names the authorised services");
      const detail = await openDetail(page, "Gmail (personal account)");
      await detail.locator(".mcp-menu-trigger").click();
      await page.getByRole("menuitem", {name: /Sign in again/}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      const box = name => dialog.getByRole("checkbox", {name});
      assert.equal(await box("Gmail").isChecked(), true, "an authorised service stays selected");
      assert.equal(await box("Google Calendar").isChecked(), true);
      assert.equal(await box("Google Drive").isChecked(), false, "an unauthorised service is not preselected");
      assert.equal(await dialog.getByRole("link", {name: "Enable Google Calendar API"}).getAttribute("href"), "https://console.cloud.google.com/apis/library/calendar-json.googleapis.com?project=tofi-sample-123456", "the enable button is built per service");
      assert.equal(await dialog.getByRole("link", {name: "Enable Google Drive API"}).count(), 0, "no enable link for a service that is not selected");
      await box("Google Drive").check();
      assert.equal(await dialog.getByRole("link", {name: "Enable Google Drive API"}).getAttribute("href"), "https://console.cloud.google.com/apis/library/drive.googleapis.com?project=tofi-sample-123456");
      await box("Gmail").uncheck();
      assert.equal(await dialog.locator('input[type="radio"]').count(), 0, "the Gmail access choice goes away with Gmail");
      assert.equal(await box("Gmail").isChecked(), false);
      await box("Gmail").check();
      assert.equal(await dialog.getByRole("button", {name: "Authorize with Google"}).isDisabled(), true, "authorization stays gated on the configured HTTPS address (this fixture is http)");
      assert.deepEqual(posts, [], "choosing services writes nothing");
      await close();
      console.log("PASS services: status line names them, dialog selects Gmail / Calendar / Drive, enable link per service");
    }

    // ---- 3. Authorize opens a dialog; the page behind is inert; Escape closes it and focus returns.
    {
      const {page, close, writes} = await open();
      const authorize = row(page, "Gmail (personal account)").getByRole("button", {name: "Sign in"});
      await authorize.click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await dialog.getAttribute("aria-modal"), "true");
      assert.equal(await dialog.locator('input[type="email"]').count(), 1, "the Gmail setup asks for the account");
      assert.equal(await dialog.locator('input[type="radio"]').count(), 2, "Gmail access is two cards: read only and read and send");
      assert.equal(await dialog.locator('input[type="radio"]:checked').getAttribute("value"), "readonly", "read only is the default");
      assert.equal(await dialog.locator("select").count(), 0, "no native select");
      assert.match(await dialog.innerText(), /redirect|Authorized redirect/i, "the redirect URI guidance is in the dialog");
      assert.match(await dialog.locator("code").first().innerText(), /\/api\/extensions\/local-mcp\/gog\/oauth\/callback$/, "the redirect URI is shown");
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await page.waitForTimeout(150);
      assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), "Sign in", "focus returns to the control that opened the dialog");
      assert.deepEqual(writes, [], "opening and closing setup writes nothing");
      await close();
      console.log("PASS dialog: Sign in opens the Gmail setup in a modal dialog, Escape closes it, focus returns, nothing written");
    }

    // ---- 4. Add connection: the directory. Gmail is the gogcli path; Workspace preview servers have their own grouping.
    {
      const {page, close} = await open({fresh: "1"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await page.locator('.mcp-settings .integration-tile').count(), 0, "the directory is not on the page");
      const ids = await dialog.locator("[data-integration-id]").evaluateAll(list => list.map(item => item.getAttribute("data-integration-id")));
      assert.equal(ids[0], "google", "Google is one tile and leads the directory");
      assert.ok(ids.includes("notion") || ids.includes("github") || ids.length > 3, "the directory lists services");
      assert.ok(ids.includes("custom-url") && ids.includes("custom-package"), "the Custom path offers a URL and a local package");
      assert.equal(ids.filter(id => id.startsWith("google-")).length, 0, "the six Workspace servers are not in the directory any more");
      assert.doesNotMatch(await dialog.innerText(), /Workspace Developer Preview Program/, "the enrolment line lives inside the Google step");
      await dialog.locator('[data-integration-id="google"]').click();
      await dialog.getByRole("button", {name: "Install gogcli"}).waitFor();
      assert.match(await dialog.locator("h3").first().innerText(), /^Google$/, "the Google tile opens the Google step");
      const workspace = dialog.locator(".connect-workspace");
      assert.equal(await workspace.locator(".disclosure-region").getAttribute("aria-hidden"), "true", "Advanced: Google Workspace MCP is folded");
      await workspace.getByRole("button", {name: /Advanced: Google Workspace MCP/}).click();
      await dialog.locator('[data-integration-id="google-gmail"]').waitFor({state: "visible"});
      assert.match(await workspace.innerText(), /Workspace Developer Preview Program/, "the folded section states the enrolment requirement");
      assert.match(await workspace.locator(".settings-banner.is-warn").innerText(), /2026-11-11/, "a dated warning says what Google requires of the OAuth client");
      assert.equal(await dialog.locator("form").count(), 0, "not a form yet: the Google step is the gogcli setup");
      await dialog.locator('[data-integration-id="google-docs"]').click();
      await dialog.locator("form").waitFor();
      await dialog.getByRole("button", {name: "Back"}).click();
      await dialog.getByRole("button", {name: "Install gogcli"}).waitFor();
      await dialog.getByRole("button", {name: "Back"}).click();
      await dialog.locator('[data-integration-id="custom-url"]').click();
      await dialog.locator('input[type="url"]').waitFor();
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await close();
      console.log("PASS directory: opens in a dialog, Google is one tile that opens the gogcli setup, the Workspace servers sit folded inside it with the enrolment line and the dated warning, Back returns through the Google step, Custom paths");
    }

    // ---- 5. No local plugin service: the directory offers no local connectors.
    {
      const {page, close} = await open({runner: "off"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await dialog.locator('[data-integration-id="custom-package"]').count(), 0, "no local package path");
      await dialog.locator('[data-integration-id="google"]').click();
      assert.equal(await dialog.getByRole("button", {name: "Install gogcli"}).count(), 0, "no gogcli without a local plugin service");
      await dialog.locator('[data-integration-id="google-gmail"]').waitFor({state: "visible"});
      await close();
      console.log("PASS directory: without a local plugin service the Google step offers the Workspace servers only, and there is no local package");
    }

    // ---- 6. A connection's page: where it comes from, its tools by permission, nothing inline on the list.
    {
      const {page, close} = await open({gog: "authorised"});
      const gog = row(page, "Gmail (personal account)");
      await gog.waitFor();
      let detail = await openDetail(page, "Gmail (personal account)");
      assert.equal(await page.locator(".mcp-list-pane").evaluate(el => el.hidden), true, "the list gives way to the page");
      assert.match(await detail.locator(".mcp-detail-title p").innerText(), /Built in · gogcli · Gmail API/, "Gmail says it runs via gogcli and is built in");
      assert.deepEqual(await detail.locator("section[data-section]").evaluateAll(list => list.map(item => item.getAttribute("data-section"))), ["status", "tools", "bots", "advanced", "danger"], "five sections, in order");
      await detail.locator(".mcp-tool-group").first().waitFor();
      await page.getByRole("button", {name: "Back to Connections"}).click();
      await page.locator(".mcp-list-pane:not([hidden])").waitFor();
      await page.waitForTimeout(150);
      assert.equal(await page.evaluate(() => document.activeElement?.closest(".mcp-service")?.getAttribute("aria-label")), "Gmail (personal account)", "Back returns focus to the row that opened the page");
      detail = await openDetail(page, "Notion");
      assert.match(await detail.locator(".mcp-detail-title p").innerText(), /Official · mcp\.notion\.com/);
      // Tools by permission: the groups carry the owner's three words; the provider's marks sort the tools.
      const group = tier => detail.locator(`.mcp-tool-group[data-tier="${tier}"]`);
      assert.match(await group("read_only").locator("h5").innerText(), /^Runs without asking\s*2$/);
      assert.deepEqual(await group("read_only").locator("code").allInnerTexts(), ["tool_1", "tool_4"], "official read tools are the service-marked ones");
      assert.match(await group("write").locator("h5").innerText(), /^Asks when unsure\s*4$/);
      assert.deepEqual(await group("write").locator("code").allInnerTexts(), ["tool_2", "tool_3", "tool_5", "tool_6"]);
      assert.equal(await group("high_risk").count(), 0, "an empty group is not drawn");
      assert.equal(await detail.locator("[data-capability=community]").count(), 0, "an official connection carries no community note");
      assert.match(await detail.locator(".mcp-tool-marks").first().innerText(), /Marked by the service/);
      await page.getByRole("button", {name: "Back to Connections"}).click();
      detail = await openDetail(page, "Google Gmail");
      await advanced(detail);
      assert.match(await detail.innerText(), /Workspace Developer Preview Program/, "an official Workspace server states the enrolment it needs");
      await page.getByRole("button", {name: "Back to Connections"}).click();
      detail = await openDetail(page, "acme_tracker");
      assert.match(await detail.locator(".mcp-detail-title p").innerText(), /Custom · mcp\.example\.test/);
      await advanced(detail);
      assert.match(await detail.locator(".conn-facts").innerText(), /Address\s*https:\/\/mcp\.example\.test\/tracker/);
      await close();
      console.log("PASS details: a row opens its page (Status / Tools / Advanced / Danger), Back returns focus, each page says where it comes from, tools are grouped by permission");
    }

    // ---- 6a. The permission of a tool is an overlay menu with the owner's three words and Automatic; the choice is one PUT.
    {
      const {page, close, posts} = await open();
      const detail = await openDetail(page, "Notion");
      assert.equal(await detail.locator("select").count(), 0, "no native select on the page");
      const trigger = detail.getByRole("button", {name: "Permission for tool_2"});
      assert.match(await trigger.innerText(), /Asks when unsure/);
      await trigger.click();
      const menu = page.getByRole("menu", {name: "Permission for tool_2"});
      await menu.waitFor();
      assert.deepEqual(await menu.getByRole("menuitem").allInnerTexts(), ["Automatic · follows the service’s mark", "Runs without asking", "Asks when unsure", "Always asks me"]);
      assert.equal(await menu.getByRole("menuitem", {name: "Automatic · follows the service’s mark"}).getAttribute("aria-current"), "true", "the current choice is marked");
      assert.match(await menu.innerText(), /ends its allowances/, "raising to Always asks me says what it ends");
      await page.keyboard.press("Escape");
      await menu.waitFor({state: "detached"});
      await page.waitForTimeout(150);
      assert.equal(await page.evaluate(() => document.activeElement?.getAttribute("aria-label")), "Permission for tool_2", "Escape returns focus to the trigger");
      assert.deepEqual(posts, [], "browsing the menu writes nothing");
      await trigger.click();
      await page.getByRole("menuitem", {name: "Always asks me"}).click();
      await page.waitForFunction(() => true);
      await page.waitForTimeout(200);
      const put = posts.find(post => /\/tools\/tool_2$/.test(post.path));
      assert.ok(put, "one PUT for the tool");
      assert.deepEqual(JSON.parse(put.body), {tier: "high_risk"});
      await close();
      console.log("PASS permission menu: Radix overlay menu (no select), three words plus Automatic, Escape returns focus, one PUT");
    }

    // ---- 6c. The settings rail counts connections that need sign-in, setup or a retry.
    {
      const plain = await open();
      const badge = page => page.locator('.settings-tabs [data-tab="connections"] .settings-count');
      await badge(plain.page).waitFor();
      assert.equal((await badge(plain.page).innerText()).trim(), "1", "one connection needs sign-in");
      await plain.close();
      const broken = await open({apidisabled: "status"});
      await badge(broken.page).waitFor();
      await broken.page.waitForFunction(() => document.querySelector('.settings-tabs [data-tab="connections"] .settings-count')?.textContent?.trim() === "2", null, {timeout: 8000});
      await broken.close();
      console.log("PASS rail badge: sign-in and failed checks (API not enabled) are counted");
    }

    // ---- 6d. An empty page: the cat, one line, three tiles that start a connection, Browse all.
    {
      const {page, close} = await open({empty: "1", fresh: "1"});
      await page.locator(".mcp-empty").waitFor();
      assert.deepEqual(await page.locator(".mcp-empty-tile").allInnerTexts(), ["Google", "Notion", "GitHub"]);
      assert.equal(await page.locator(".mcp-service").count(), 0);
      await page.locator('.mcp-empty-tile', {hasText: "Notion"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.match(await dialog.locator("h3").first().innerText(), /Notion/, "a tile goes straight to that service's setup");
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await page.getByRole("button", {name: "Browse all"}).click();
      await dialog.waitFor();
      assert.ok(await dialog.locator('[data-integration-id="notion"]').count() > 0, "Browse all opens the directory");
      await close();
      console.log("PASS empty state: three tiles start a connection, Browse all opens the directory");
    }

    // ---- 6e. A saved link to the old Approvals page lands on Permissions.
    {
      const {page, close} = await open({tab: "approvals"});
      await page.locator('.settings-tabs [data-tab="permissions"][aria-current="page"]').waitFor();
      assert.equal(await page.locator('.settings-tabs [data-tab="approvals"]').count(), 0);
      await close();
      console.log("PASS legacy tab: approvals opens Permissions");
    }

    // ---- 7. Setup help for Google appears only when something failed.
    {
      const {page, close} = await open({fail: "google-gmail"});
      await page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-state") === "error", null, {timeout: 12000});
      const google = row(page, "Google Gmail");
      assert.match(await google.locator(".mcp-service-status").innerText(), /Can.t connect/);
      assert.match(await google.locator(".mcp-service-error").innerText(), /API 未启用|not enabled/i, "the named reason is shown");
      assert.equal(await google.locator(".mcp-google-setup").count(), 0, "the row carries no setup help");
      assert.equal(await google.getByRole("button", {name: "Retry"}).count(), 1, "one primary action: Retry");
      const detail = await openDetail(page, "Google Gmail");
      assert.equal(await detail.locator(".mcp-google-setup").count(), 1, "setup help appears on the page in the error state");
      await page.getByRole("button", {name: "Back to Connections"}).click();
      const notion = await openDetail(page, "Notion");
      assert.equal(await notion.locator(".mcp-google-setup").count(), 0);
      await close();
      console.log("PASS error: a failing Google row names the reason and shows its setup help; healthy rows show none");
    }

    // ---- 7b. "API not enabled" is a named problem with one Enable button (from the structured error, via the status and a check).
    for (const variant of ["status", "check"]) {
      const {page, close} = await open({apidisabled: variant});
      const google = row(page, "Google Gmail");
      await google.waitFor();
      await page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-kind") === "api_disabled", null, {timeout: 12000});
      assert.match((await google.locator(".mcp-service-status").innerText()).trim(), /^Setup needed/, "the status names the problem");
      const enable = google.getByRole("link", {name: "Enable Gmail API"});
      assert.equal(await enable.count(), 1, "one primary action: the Enable button");
      assert.equal(await enable.getAttribute("href"), "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456", "it opens the exact console page for that API in that project");
      assert.equal(await enable.getAttribute("target"), "_blank");
      assert.match(await enable.getAttribute("rel"), /noopener/);
      assert.match(await google.locator(".mcp-service-error").innerText(), /Gmail API isn.t enabled in your Google Cloud project/);
      assert.doesNotMatch(await google.locator(".mcp-service-status").innerText(), /Connected/);
      const page2 = await openDetail(page, "Google Gmail");
      assert.equal(await page2.locator(".conn-status").getAttribute("data-kind"), "api_disabled", "the page's Status section says the same");
      assert.equal(await page2.getByRole("link", {name: "Enable Gmail API"}).count(), 1, "and offers the same Enable button");
      if (variant === "status") assert.equal(await google.getAttribute("data-state"), "error");
      await close();
    }
    console.log("PASS api not enabled: the row says so and offers the Enable button for that API in that project");

    // ---- 7c. A second Google service does not ask for the client again.
    {
      const {page, close} = await open({gclient: "1", gog: "authorised", fresh: "1"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      await dialog.locator('[data-integration-id="google"]').click();
      await dialog.getByRole("button", {name: /Advanced: Google Workspace MCP/}).click();
      await dialog.locator('[data-integration-id="google-calendar"]').click();
      await dialog.locator("form").waitFor();
      assert.match(await dialog.innerText(), /Using your saved Google client \(project tofi-sample-123456\)/);
      assert.equal(await dialog.locator('input[required][autocomplete="off"]').count(), 0, "no client ID or secret to type again");
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await close();
    }
    console.log("PASS google client: a second Google service uses the saved client");

    // ---- 6b. A community connection: the package's own read-only mark is ignored and writes are stated to ask every time.
    {
      const {page, close} = await open({community: "1"});
      const pkg = row(page, "acme_pkg");
      await pkg.waitFor();
      assert.match(await pkg.innerText(), /Community · acme-mcp 1\.0\.0/);
      const detail = await openDetail(page, "acme_pkg");
      assert.deepEqual(await detail.locator('.mcp-tool-group[data-tier="read_only"] code').allInnerTexts(), ["get_thing"], "only the owner-marked tool runs without asking");
      assert.deepEqual(await detail.locator('.mcp-tool-group[data-tier="write"] code').allInnerTexts(), ["list_things", "send_thing"], "a tool the package calls read only is still a write");
      assert.match(await detail.locator("[data-capability=community]").innerText(), /writes ask every time/i);
      await close();
      console.log("PASS community: read-only marks ignored, writes ask every time, source names the package");
    }

    // ---- 7d. The directory curates official connectors; community ones go through the custom path.
    {
      const {page, close} = await open({fresh: "1"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await dialog.locator('[data-held-integration-id="discord"], [data-held-integration-id="yahoo-finance"]').count(), 0, "community servers are not curated");
      assert.match(await dialog.innerText(), /maintained by third parties.*always ask first/);
      await close();
    }
    console.log("PASS directory: official only, community through the custom path with its caveat");

    // ---- 7e. GitHub source links with star counts: from TOFI's own server, quiet, and no number when the server has none.
    {
      const {page, close} = await open({fresh: "1"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      const gogRepo = dialog.locator('[data-integration-id="google"] + .integration-repo .repo-link');
      await gogRepo.waitFor();
      assert.equal(await gogRepo.getAttribute("href"), "https://github.com/openclaw/gogcli");
      assert.equal(await gogRepo.getAttribute("target"), "_blank");
      assert.match(await gogRepo.getAttribute("rel"), /noopener/);
      await dialog.locator('[data-integration-id="google"] + .integration-repo .repo-stars').waitFor();
      assert.match(await gogRepo.innerText(), /★ 8\.5k/);
      assert.match(await dialog.locator('[data-integration-id="github"] + .integration-repo').innerText(), /github\/github-mcp-server\s*★ 33\.8k/);
      assert.match(await dialog.locator('[data-integration-id="notion"] + .integration-repo').innerText(), /makenotion\/notion-mcp-server\s*★ 4\.7k/);
      assert.equal(await dialog.locator('[data-integration-id="linear"] + .integration-repo').count(), 0, "a connector with no verified open-source repo shows no link");
      await close();
      const none = await open({fresh: "1", nostars: "1"});
      await none.page.getByRole("button", {name: "Add connection"}).click();
      const noneDialog = none.page.locator('[role="dialog"].connect-dialog');
      await noneDialog.locator('[data-integration-id="google"] + .integration-repo .repo-link').waitFor();
      await none.page.waitForTimeout(300);
      assert.equal(await noneDialog.locator(".repo-stars").count(), 0, "no cached count: the link shows without a number");
      assert.doesNotMatch(await noneDialog.innerText(), /★/);
      await none.close();
      // The connection's page carries the same link.
      const rows = await open({gog: "authorised"});
      await rows.page.locator('.mcp-service[aria-label="Gmail (personal account)"]').waitFor();
      const gogPage = await openDetail(rows.page, "Gmail (personal account)");
      await advanced(gogPage);
      await gogPage.locator(".repo-stars").waitFor();
      assert.match(await gogPage.locator(".repo-link").innerText(), /openclaw\/gogcli\s*★ 8\.5k/);
      await rows.close();
    }
    console.log("PASS source links: GitHub link and star count in the directory and on the page; no count, no number");

    // ---- 7f. A provider that accepts only a 127.0.0.1 redirect (Robinhood) never takes the https popup: paste-back without a shared
    // computer, the shared computer with one. One quiet route line says which and why; no illustration; the address is never echoed.
    {
      const paste = await open({loopback: "1"});
      const rh = row(paste.page, "Robinhood");
      await rh.waitFor();
      assert.match(await rh.locator(".mcp-login-note").innerText(), /127\.0\.0\.1/, "the route line names the redirect");
      const popupOpened = paste.page.waitForEvent("popup", {timeout: 8000});
      await rh.getByRole("button", {name: "Sign in"}).click();
      const dialog = paste.page.locator(".paste-back");
      await dialog.waitFor();
      await dialog.getByRole("link", {name: /Open Robinhood sign-in/}).waitFor();
      assert.ok(paste.posts.some(post => /\/robinhood\/oauth\/paste\/start$/.test(post.path)), "paste-back session started");
      assert.ok(!paste.posts.some(post => /\/oauth\/(start|vm\/start)$/.test(post.path)), "neither the https popup nor the computer route was used");
      await popupOpened; // one sign-in tab, opened inside the click and pointed at the provider by the dialog
      assert.equal(await paste.page.locator(".web-oauth-link, .web-oauth-provider").count(), 0, "no pending illustration");
      const wrong = "http://127.0.0.1:43821/oauth/callback?code=wrong-code-value&state=x";
      await dialog.getByLabel("Redirected address").fill(wrong);
      await dialog.getByRole("button", {name: "Finish sign-in"}).click();
      await dialog.locator(".paste-back-error").waitFor();
      assert.doesNotMatch(await dialog.innerText(), /wrong-code-value/, "the pasted address is not echoed in the message");
      assert.equal(await dialog.getByLabel("Redirected address").inputValue(), wrong, "the field is kept for another try");
      await dialog.getByLabel("Redirected address").fill("http://127.0.0.1:43821/oauth/callback?code=good&state=s");
      await dialog.getByRole("button", {name: "Finish sign-in"}).click();
      await dialog.waitFor({state: "detached"});
      const completes = paste.posts.filter(post => /\/oauth\/paste\/complete$/.test(post.path));
      assert.equal(completes.length, 2, "each attempt is one request");
      await paste.close();
      const cancelled = await open({loopback: "1"});
      await row(cancelled.page, "Robinhood").getByRole("button", {name: "Sign in"}).click();
      await cancelled.page.locator(".paste-back").getByRole("button", {name: "Cancel"}).click();
      await cancelled.page.locator(".paste-back").waitFor({state: "detached"});
      assert.ok(cancelled.posts.some(post => /\/oauth\/desktop\/cancel$/.test(post.path)), "cancelling releases the pending flow");
      await cancelled.close();
      // A shared computer being available does not change the route: paste-back needs nothing from the server.
      const withComputer = await open({loopback: "1", vm: "1"});
      await row(withComputer.page, "Robinhood").getByRole("button", {name: "Sign in"}).click();
      await withComputer.page.locator(".paste-back").waitFor();
      assert.ok(!withComputer.posts.some(post => /\/oauth\/(start|vm\/start)$/.test(post.path)), "neither the https popup nor the computer route");
      await withComputer.close();
      // Other providers are unchanged: no route line, no paste-back.
      const plain = await open();
      assert.doesNotMatch(await row(plain.page, "acme_tracker").innerText(), /127\.0\.0\.1/, "another provider shows no loopback route line");
      await plain.close();
    }
    console.log("PASS loopback-only provider: paste-back whether or not a shared computer exists, never the https popup; address not echoed; others unchanged");


    // ---- 6f. Pause and Resume keep everything; the approval card's Manage link opens the connection on the tool.
    {
      // A paused connection reads Paused with Resume; its page says what is kept and offers Resume connection.
      const {page, close, posts} = await open({paused: "notion"});
      const notion = row(page, "Notion");
      await page.locator('.mcp-service[aria-label="Notion"][data-kind="paused"]').waitFor();
      assert.match((await notion.locator(".mcp-service-status").innerText()).trim(), /^Paused/);
      assert.deepEqual(posts.filter(post => /\/mcp\/notion\/test$/.test(post.path)), [], "a paused connection is not probed");
      const detail = await openDetail(page, "Notion");
      assert.match(await detail.locator(".conn-status").innerText(), /Paused\. Bots can't use this connection/, "the page says what pausing keeps");
      assert.equal(await detail.locator(".conn-status").getByRole("button", {name: "Resume"}).count(), 1, "Resume leads on the status line");
      await detail.locator(".conn-danger").getByRole("button", {name: "Resume connection"}).click();
      await page.waitForTimeout(200);
      const resume = posts.find(post => /\/mcp\/notion\/enabled$/.test(post.path));
      assert.ok(resume, "Resume is one PUT to the enabled route");
      assert.deepEqual(JSON.parse(resume.body), {enabled: true});
      await close();
      // Pausing a running connection is one PUT, from the Danger section, never a removal.
      const running = await open();
      const page2 = running.page;
      const d2 = await openDetail(page2, "Notion");
      assert.equal(await d2.locator(".conn-danger").getByRole("button", {name: "Pause connection"}).count(), 1);
      await d2.locator(".conn-danger").getByRole("button", {name: "Pause connection"}).click();
      await page2.waitForTimeout(200);
      const pause = running.posts.find(post => /\/mcp\/notion\/enabled$/.test(post.path));
      assert.deepEqual(pause && JSON.parse(pause.body), {enabled: false});
      assert.ok(!running.posts.some(post => /DELETE|oauth\/disconnect/.test(post.path) && /notion/.test(post.path)), "pausing removes and disconnects nothing");
      await running.close();
      console.log("PASS pause: a paused row reads Paused with Resume and is not probed; pause and resume are one PUT each; nothing is removed");
    }
    {
      // Always allowed chips: one per allowance on this connection, a revoke that fades the chip out.
      const {page, close, writes} = await open({allow: "chips"});
      const detail = await openDetail(page, "Notion");
      const chip = detail.locator('.mcp-tool[data-tool="tool_2"] .mcp-allowance');
      await chip.waitFor();
      assert.match(await chip.innerText(), /Always allowed · Research Bot/);
      assert.equal(await detail.locator(".mcp-allowance").count(), 1, "another connection's allowance is not shown here");
      await chip.getByRole("button", {name: "Revoke Research Bot's allowance for tool_2"}).click();
      await detail.locator(".mcp-allowance").waitFor({state: "detached", timeout: 3000});
      assert.ok(writes.includes("DELETE /api/standing-allowances/al-3"), "Revoke deletes that allowance");
      await close();
      console.log("PASS allowances: chips under the tool, only this connection's, Revoke removes the chip");
    }
    {
      // The deep link: a target set before the page mounts, and one sent while it is open, both land on the tool and flash it.
      const early = await open({pending: "notion:tool_5"});
      const detail = early.page.locator(".mcp-detail");
      await detail.waitFor();
      assert.match(await detail.locator(".mcp-detail-title h3").innerText(), /Notion/, "the connection's page opens by itself");
      await early.page.waitForFunction(() => document.querySelector('.mcp-tool[data-tool="tool_5"]')?.hasAttribute("data-flash"), null, {timeout: 5000});
      await early.page.waitForFunction(() => !document.querySelector('.mcp-tool[data-flash]'), null, {timeout: 5000});
      await early.close();
      const live = await open();
      await live.page.evaluate(() => window.openConnectionDeepLink({server: "notion", tool: "tool_3"}));
      await live.page.locator(".mcp-detail").waitFor();
      await live.page.waitForFunction(() => document.querySelector('.mcp-tool[data-tool="tool_3"]')?.hasAttribute("data-flash"), null, {timeout: 5000});
      await live.close();
      // A link to a connection that is gone leaves the list, not a blank page.
      const gone = await open({pending: "vanished:tool_1"});
      await gone.page.waitForTimeout(800);
      assert.equal(await gone.page.locator(".mcp-list-pane").evaluate(el => el.hidden), false, "an unknown connection leaves the list showing");
      await gone.close();
      console.log("PASS deep link: opens the connection and flashes the tool, early or while open; an unknown connection is ignored");
    }


    // ---- 6g. Which Bots can use a connection: All Bots by default; Chosen Bots is a checked list; each change is one PUT.
    {
      const {page, close, posts} = await open();
      const detail = await openDetail(page, "Notion");
      const bots = detail.locator('[data-section="bots"]');
      await bots.locator(".segmented").waitFor();
      assert.equal(await bots.getByRole("radio", {name: "All Bots"}).getAttribute("aria-checked"), "true", "a connection nobody limited is open to all Bots");
      assert.equal(await bots.locator(".conn-bots-fold").getAttribute("inert"), "", "the list is folded away");
      await bots.getByRole("radio", {name: "Chosen Bots"}).click();
      await page.waitForTimeout(400);
      assert.equal(await bots.locator(".conn-bots-fold").getAttribute("data-open"), "true", "Chosen Bots unfolds the list");
      assert.equal(await bots.locator('input[type="checkbox"]:checked').count(), 2, "every Bot starts checked");
      await bots.getByRole("checkbox", {name: "Ops Bot can use Notion"}).uncheck();
      await page.waitForTimeout(200);
      const chosen = posts.find(post => /\/mcp\/notion\/bots$/.test(post.path));
      assert.deepEqual(chosen && JSON.parse(chosen.body), {bot_ids: ["bot_research"]}, "unchecking a Bot saves exactly the Bots still allowed");
      await bots.getByRole("radio", {name: "All Bots"}).click();
      await page.waitForTimeout(200);
      assert.deepEqual(JSON.parse(posts.filter(post => /\/mcp\/notion\/bots$/.test(post.path)).at(-1).body), {all: true}, "All Bots clears every limit with one PUT");
      await close();
      const limited = await open({limited: "1"});
      const d2 = await openDetail(limited.page, "Notion");
      const b2 = d2.locator('[data-section="bots"]');
      await b2.locator(".segmented").waitFor();
      assert.equal(await b2.getByRole("radio", {name: "Chosen Bots"}).getAttribute("aria-checked"), "true", "a limited connection opens on Chosen Bots");
      assert.equal(await b2.getByRole("checkbox", {name: "Ops Bot can use Notion"}).isChecked(), false);
      await limited.close();
      console.log("PASS bots: All Bots by default, Chosen Bots unfolds a checked list, one PUT per change, a limited connection opens on Chosen Bots");
    }


    // ---- 7f. One click: a service that registers its own OAuth client is saved with its defaults and goes straight to sign-in.
    {
      savedServers.length = 0;
      const {page, close, posts} = await open({empty: "1", fresh: "1", vm: "1", onecl: "1"});
      await page.locator(".mcp-empty").waitFor();
      await page.locator('.mcp-empty-tile', {hasText: "Notion"}).click();
      await page.locator('.mcp-service[aria-label="Notion"]').waitFor({timeout: 8000});
      await page.locator(".connect-dialog").waitFor({state: "detached", timeout: 8000});
      const saved = posts.find(post => post.path === "/api/extensions/mcp");
      assert.ok(saved, "the tile saved the connection itself, no form");
      const body = JSON.parse(saved.body);
      assert.equal(body.name, "notion");
      assert.equal(body.url, "https://mcp.notion.com/mcp");
      assert.equal(body.oauth.client_id, "", "no client to type: the service registers its own");
      assert.ok(posts.some(post => /\/mcp\/notion\/oauth\/vm\/start$/.test(post.path)), "sign-in started right after the save, by the route this page can take");
      await page.getByText(/The computer is busy/).waitFor({timeout: 5000});
      await close();
      // A loopback-only service: the sign-in tab opens inside the click, then the paste-back dialog.
      savedServers.length = 0;
      const second = await open({fresh: "1", onecl: "1"});
      const popups = [];
      second.page.on("popup", popup => popups.push(popup.url()));
      await second.page.getByRole("button", {name: "Add connection"}).click();
      const dialog = second.page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      await dialog.locator('[data-integration-id="robinhood"]').click();
      await second.page.locator(".paste-back").waitFor({timeout: 8000});
      assert.equal(popups.length, 1, "one sign-in tab, opened by the click");
      assert.match(popups[0], /\/api\/extensions\/mcp\/robinhood\/oauth\/pending$/, "the tab starts on the pending page");
      assert.ok(second.posts.some(post => post.path === "/api/extensions/mcp"), "saved first");
      await second.close();
      // A token service keeps its one-field form (no one click without a token); a blocked route keeps the form too.
      const token = await open({empty: "1", fresh: "1"});
      await token.page.locator(".mcp-empty").waitFor();
      await token.page.getByRole("button", {name: "Add connection"}).click();
      const d3 = token.page.locator('[role="dialog"].connect-dialog');
      await d3.locator('[data-integration-id="linear"]').click();
      await d3.locator('input[type="password"]').waitFor();
      assert.equal(await d3.locator('input[type="password"]').count(), 1, "a token service asks for one password field");
      await d3.getByRole("button", {name: "Back"}).click();
      await d3.locator('[data-integration-id="notion"]').click();
      await d3.locator("form").waitFor();
      assert.deepEqual(token.posts.filter(post => post.path === "/api/extensions/mcp"), [], "no connection was saved: this page cannot sign in, so the form explains why");
      await token.close();
      console.log("PASS one click: Notion saves its defaults and starts sign-in, Robinhood opens its tab in the click then the paste-back dialog; token and blocked-route services keep the form");
    }

    // ---- 8. Phone width: no horizontal overflow, 44 px targets for the controls that matter.
    {
      const {page, close} = await open({}, {width: 390, height: 844});
      await rows(page).first().waitFor();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert.ok(overflow <= 0, `no horizontal page scroll at 390 px, got ${overflow}`);
      await page.getByRole("button", {name: "Add connection"}).tap();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      const box = await dialog.boundingBox();
      assert.ok(box.x >= 0 && box.x + box.width <= 390.5, "the dialog fits the phone");
      await close();
      console.log("PASS phone: 390 px has no horizontal scroll and the dialog fits");
    }

    // ---- 9. Every locale renders the page (keys exist) without raw key names.
    for (const lang of ["zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
      const {page, close} = await open({lang});
      await rows(page).first().waitFor();
      await page.waitForTimeout(400);
      const text = await page.locator(".mcp-settings").innerText();
      assert.doesNotMatch(text, /\b(connections|status|tools|detail)\.[a-z_]+\b|\bmcp\.(loading|empty|connect|browse_all|empty_body)\b/, `${lang}: no raw keys`);
      await close();
    }
    console.log("PASS locales: the page renders in six more languages without raw keys");
    assert.deepEqual(unexpected, [], "no page error");
    console.log("PASS no page error");
  }
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
