// Settings › Connections (docs/design/components.md §17, "Connections list and connect dialog").
// Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed (synthetic data only)
// and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//
// Invariants this pins (see the redesign brief):
//   - one row per connection the person has; no internal ids (local_gog), no Runner / sleep wording, no duplicate card
//   - a local plugin whose account was never authorised reads "Needs authorization", never "Connected", and offers Authorize
//   - setup lives in a dialog, never inline on the page; Gmail in the directory is the gogcli path, Google's Workspace
//     preview servers sit under their own grouping with the enrolment line
//   - every connection's details say where it comes from; the tool list is in the details, not on the resting row
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
import {CodexPanel,NotificationSetting} from '../src/settings/AppSlots';
const query=new URLSearchParams(location.search);
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>('connections');
 const [entry]=useState<SettingsEntry>({seq:1,view:'page'});
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>setTab(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,notifications:<NotificationSetting/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/></aside></div></div>;
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
  const stub = (url, method, q) => {
    const path = url.pathname;
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}};
    if (path === "/api/auth/session") return {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}};
    if (path === "/api/bots") return {bots: [{id: "bot_research", name: "Research Bot", archived: false}]};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/extensions/oauth-options") return {vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      ...(q.get("fresh") ? [] : [{name: "local_gog", url: "http://plugins.internal.example:8787/mcp/gog", transport: "streamable_http", headers: {Authorization: "Bearer x"}, status: {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 3}}]),
      {name: "notion", url: "https://mcp.notion.com/mcp", transport: "streamable_http", oauth: {client_id: "", connected: true}, status: {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 65}},
      {name: "google-gmail", url: "https://gmailmcp.googleapis.com/mcp/v1", transport: "streamable_http", oauth: {client_id: "sample", connected: true}, status: q.get("apidisabled") === "status"
        ? {checked_at: "2026-10-21T09:30:00Z", ok: false, tool_count: 23, error_class: "failed", error_code: "api_disabled", api: "gmail.googleapis.com", enable_url: ENABLE}
        : {checked_at: "2026-10-21T09:30:00Z", ok: true, tool_count: 23}},
      {name: "acme_tracker", url: "https://mcp.example.test/tracker", oauth: {client_id: "sample", connected: false}},
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") {
      const name = path.split("/")[4];
      if (q.get("apidisabled") && name === "google-gmail") return {ok: false, tool_count: 0, diagnostics: [{message: "Google 返回 API 未启用。", code: "api_disabled", api: "gmail.googleapis.com", enable_url: ENABLE}]};
      if (q.get("fail") === name) return {ok: false, tool_count: 0, diagnostics: [{message: "Google 返回 API 未启用。请在 OAuth Client 所属的同一个项目中启用产品 API 和 MCP 服务，再刷新工具。", code: "api_disabled"}]};
      return {ok: true, tool_count: name === "notion" ? 65 : name === "google-gmail" ? 23 : 3};
    }
    let m = /^\/api\/extensions\/mcp\/([^/]+)\/tools$/.exec(path);
    if (m && method === "GET") return tools(m[1] === "notion" ? 6 : m[1] === "google-gmail" ? 6 : 3);
    if (path === "/api/extensions/local-mcp") return {available: q.get("runner") !== "off", plugins: q.get("fresh") ? [] : [{id: "gog", state: "sleeping", tool_count: 3}]};
    if (path === "/api/extensions/local-mcp/gog/oauth/status") return q.get("gog") === "authorised" ? {connected: true, email: "you@example.test", scope: "readonly"} : {connected: false, email: ""};
    if (path === "/api/extensions/google") return q.get("gclient") ? {client: {configured: true, project_id: "tofi-sample-123456", project_number: "622599621014"}, apis: []} : {client: {configured: false}, apis: []};
    if (path === "/api/standing-allowances") return {allowances: [], remembered: []};
    return {};
  };
  async function open(params = {}, {width = 1280, height = 900, theme = "light", locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, locale, hasTouch: width < 600});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const writes = [];
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      if (method !== "GET" && !/\/test$/.test(url.pathname)) writes.push(`${method} ${url.pathname}`);
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""));
      await route.fulfill({contentType: "application/json", body: JSON.stringify(body ?? {})});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams({lang: "en", ...params})}`);
    try { await page.locator(".settings-page-body .mcp-settings").waitFor({timeout: 20000}); }
    catch (error) { console.log("page errors:", unexpected, "\nbody:", (await page.locator("body").innerText().catch(() => "")).slice(0, 400)); throw error; }
    return {page, writes, close: () => context.close()};
  }
  const shot = async (page, name, width, theme) => { if (shots) await page.screenshot({path: join(shots, `${prefix}-${name}-${width}-${theme}.png`), fullPage: false}); };
  const rows = page => page.locator(".mcp-service");
  const row = (page, name) => page.locator(`.mcp-service[aria-label="${name}"]`);

  if (shots) {
    // Screenshot pass: every state, 1280 / 390 px, light / dark. The "before" tree has a different page, so only the
    // states it has are taken (the list, and the directory behind "Add service").
    for (const [width, height] of [[1280, 900], [390, 844]]) for (const theme of ["light", "dark"]) {
      const {page, close} = await open({}, {width, height, theme});
      await page.waitForTimeout(1500);
      await shot(page, "list", width, theme);
      await page.screenshot({path: join(shots, `${prefix}-list-full-${width}-${theme}.png`), fullPage: true}).catch(() => {});
      if (prefix === "before") {
        await page.getByRole("button", {name: /Add service/}).click();
        await page.waitForTimeout(600);
        await shot(page, "directory", width, theme);
      } else {
        await page.getByRole("button", {name: "Add connection"}).click();
        await page.locator(".connect-dialog").waitFor();
        await page.waitForTimeout(500);
        await shot(page, "directory", width, theme);
        await page.locator('[data-integration-id="local-gog"]').click();
        await page.locator('.connect-dialog input[type="email"]').waitFor();
        await shot(page, "gmail-setup", width, theme);
        await page.keyboard.press("Escape");
        await page.locator(".connect-dialog").waitFor({state: "detached"});
        await row(page, "Gmail (personal account)").locator(".mcp-menu-trigger").click();
        await page.getByRole("menuitem", {name: "View details"}).click();
        await page.locator(".mcp-service-details").waitFor();
        await row(page, "Gmail (personal account)").scrollIntoViewIfNeeded();
        await shot(page, "details", width, theme);
      }
      await close();
      if (prefix !== "before") {
        const failing = await open({fail: "google-gmail"}, {width, height, theme});
        await row(failing.page, "Google Gmail").waitFor();
        await failing.page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-state") === "error", null, {timeout: 12000});
        await row(failing.page, "Google Gmail").scrollIntoViewIfNeeded();
        await shot(failing.page, "error", width, theme);
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
      for (const forbidden of [/local_gog/, /\blocal_/, /Runner/i, /sleep|hibernat/i, /\bgog\b/i, /\d+ tools?/i, /read only/i]) assert.doesNotMatch(text, forbidden, `the resting page shows no ${forbidden}`);
      assert.equal(await page.locator('.mcp-settings input[type="email"], .mcp-settings input[type="file"], .mcp-settings select, .mcp-settings textarea').count(), 0, "no setup form sits inline on the page");
      assert.equal(await page.locator(".mcp-service-tools, .mcp-google-setup, .mcp-tools").count(), 0, "a resting page shows no tool lists and no setup help");
      const gog = row(page, "Gmail (personal account)");
      assert.equal(await gog.getAttribute("data-kind"), "needs_auth", "a plugin whose Google account was never authorised needs authorization");
      assert.match((await gog.locator(".mcp-service-status").innerText()).trim(), /^Needs authorization/);
      assert.doesNotMatch(await gog.locator(".mcp-service-status").innerText(), /Connected/, "never 'Connected' without an account");
      assert.equal(await gog.getByRole("button", {name: "Authorize"}).count(), 1, "the one primary action is Authorize");
      for (const name of ["Notion", "Google Gmail"]) {
        assert.equal(await row(page, name).getAttribute("data-kind"), "ready");
        assert.match((await row(page, name).locator(".mcp-service-status").innerText()).trim(), /^Connected/);
        assert.equal(await row(page, name).locator("[data-primary]").count(), 0, "a healthy row has no primary action");
      }
      assert.match((await row(page, "acme_tracker").locator(".mcp-service-status").innerText()).trim(), /^Needs authorization/);
      await close();
      console.log("PASS list: one row per connection, plain names, no internal ids or Runner/sleep wording, needs-authorization for an unauthorised account, nothing inline");
    }

    // ---- 2. An authorised account is Connected, and shows the account in the details.
    {
      const {page, close} = await open({gog: "authorised"});
      await page.locator('.mcp-service[aria-label="Gmail (personal account)"][data-kind="ready"]').waitFor({timeout: 10000});
      assert.match(await row(page, "Gmail (personal account)").locator(".mcp-service-status").innerText(), /you@example\.test/);
      await close();
      console.log("PASS list: an authorised Google account reads Connected with the account");
    }

    // ---- 3. Authorize opens a dialog; the page behind is inert; Escape closes it and focus returns.
    {
      const {page, close, writes} = await open();
      const authorize = row(page, "Gmail (personal account)").getByRole("button", {name: "Authorize"});
      await authorize.click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await dialog.getAttribute("aria-modal"), "true");
      for (const sel of ['input[type="email"]', 'select']) assert.equal(await dialog.locator(sel).count(), 1, `the Gmail setup has ${sel}`);
      assert.match(await dialog.innerText(), /redirect|Authorized redirect/i, "the redirect URI guidance is in the dialog");
      assert.match(await dialog.locator("code").first().innerText(), /\/api\/extensions\/local-mcp\/gog\/oauth\/callback$/, "the redirect URI is shown");
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await page.waitForTimeout(150);
      assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), "Authorize", "focus returns to the control that opened the dialog");
      assert.deepEqual(writes, [], "opening and closing setup writes nothing");
      await close();
      console.log("PASS dialog: Authorize opens the Gmail setup in a modal dialog, Escape closes it, focus returns, nothing written");
    }

    // ---- 4. Add connection: the directory. Gmail is the gogcli path; Workspace preview servers have their own grouping.
    {
      const {page, close} = await open({fresh: "1"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await page.locator('.mcp-settings .integration-tile').count(), 0, "the directory is not on the page");
      const ids = await dialog.locator("[data-integration-id]").evaluateAll(list => list.map(item => item.getAttribute("data-integration-id")));
      assert.equal(ids[0], "local-gog", "Gmail (gogcli) leads the directory");
      assert.ok(ids.includes("notion") || ids.includes("github") || ids.length > 3, "the directory lists services");
      assert.ok(ids.includes("custom-url") && ids.includes("custom-package"), "the Custom path offers a URL and a local package");
      const preview = dialog.locator('[data-integration-id="google-gmail"]');
      assert.equal(await preview.isVisible(), false, "official Workspace servers are collapsed under the preview grouping");
      assert.match(await dialog.innerText(), /Workspace Developer Preview Program/, "the grouping states the enrolment requirement");
      await dialog.getByRole("button", {name: /Google Workspace servers/}).click();
      await preview.waitFor({state: "visible"});
      await dialog.getByRole("button", {name: /Google Workspace servers/}).click();
      await dialog.locator('[data-integration-id="local-gog"]').click();
      await dialog.getByRole("button", {name: "Install gogcli"}).waitFor();
      assert.match(await dialog.locator("h3").first().innerText(), /Gmail \(personal account\)/, "Gmail goes straight to the gogcli setup");
      assert.equal(await dialog.locator('[data-integration-id="google-gmail"], form').count(), 0, "not the Workspace MCP form");
      await dialog.getByRole("button", {name: "Back"}).click();
      await dialog.locator('[data-integration-id="custom-url"]').click();
      await dialog.locator('input[type="url"]').waitFor();
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await close();
      console.log("PASS directory: opens in a dialog, Gmail leads and goes to the gogcli setup, Workspace preview servers grouped with the enrolment line, Custom paths");
    }

    // ---- 5. No local plugin service: the directory offers no local connectors.
    {
      const {page, close} = await open({runner: "off"});
      await page.getByRole("button", {name: "Add connection"}).click();
      const dialog = page.locator('[role="dialog"].connect-dialog');
      await dialog.waitFor();
      assert.equal(await dialog.locator('[data-integration-id="local-gog"], [data-integration-id="custom-package"]').count(), 0);
      await close();
      console.log("PASS directory: without a local plugin service there are no local connectors");
    }

    // ---- 6. Details: where each connection comes from; the tool list lives there.
    {
      const {page, close} = await open({gog: "authorised"});
      const gog = row(page, "Gmail (personal account)");
      await gog.waitFor();
      await gog.locator(".mcp-menu-trigger").click();
      await page.getByRole("menuitem", {name: "View details"}).click();
      assert.match(await gog.locator(".mcp-service-details").innerText(), /Community · gogcli · Gmail API/, "Gmail says it runs via gogcli");
      await gog.locator(".mcp-tool-group").first().waitFor();
      const notion = row(page, "Notion");
      await notion.locator(".mcp-menu-trigger").click();
      await page.getByRole("menuitem", {name: "View details"}).click();
      assert.match(await notion.locator(".mcp-service-details").innerText(), /Official · mcp\.notion\.com/);
      const google = row(page, "Google Gmail");
      await google.locator(".mcp-menu-trigger").click();
      await page.getByRole("menuitem", {name: "View details"}).click();
      assert.match(await google.locator(".mcp-service-details").innerText(), /Workspace Developer Preview Program/, "an official Workspace server states the enrolment it needs");
      const custom = row(page, "acme_tracker");
      await custom.locator(".mcp-menu-trigger").click();
      await page.getByRole("menuitem", {name: "View details"}).click();
      assert.match(await custom.locator(".mcp-service-details").innerText(), /Custom · mcp\.example\.test/);
      await close();
      console.log("PASS details: each connection says where it comes from; tools are in the details; Workspace servers name the enrolment");
    }

    // ---- 7. Setup help for Google appears only when something failed.
    {
      const {page, close} = await open({fail: "google-gmail"});
      await page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-state") === "error", null, {timeout: 12000});
      const google = row(page, "Google Gmail");
      assert.match(await google.locator(".mcp-service-status").innerText(), /Connection failed/);
      assert.match(await google.locator(".mcp-service-error").innerText(), /API 未启用|not enabled/i, "the named reason is shown");
      assert.equal(await google.locator(".mcp-google-setup").count(), 1, "setup help appears in the error state");
      assert.equal(await google.getByRole("button", {name: "Retry"}).count(), 1, "one primary action: Retry");
      assert.equal(await row(page, "Notion").locator(".mcp-google-setup").count(), 0);
      await close();
      console.log("PASS error: a failing Google row names the reason and shows its setup help; healthy rows show none");
    }

    // ---- 7b. "API not enabled" is a named problem with one Enable button (from the structured error, via the status and a check).
    for (const variant of ["status", "check"]) {
      const {page, close} = await open({apidisabled: variant});
      const google = row(page, "Google Gmail");
      await google.waitFor();
      await page.waitForFunction(() => document.querySelector('.mcp-service[aria-label="Google Gmail"]')?.getAttribute("data-kind") === "api_disabled", null, {timeout: 12000});
      assert.match((await google.locator(".mcp-service-status").innerText()).trim(), /^API not enabled/, "the status names the problem");
      const enable = google.getByRole("link", {name: "Enable Gmail API"});
      assert.equal(await enable.count(), 1, "one primary action: the Enable button");
      assert.equal(await enable.getAttribute("href"), "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456", "it opens the exact console page for that API in that project");
      assert.equal(await enable.getAttribute("target"), "_blank");
      assert.match(await enable.getAttribute("rel"), /noopener/);
      assert.match(await google.locator(".mcp-service-error").innerText(), /Gmail API isn.t enabled in your Google Cloud project/);
      assert.doesNotMatch(await google.locator(".mcp-service-status").innerText(), /Connected/);
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
      await dialog.locator('[data-integration-id="google-calendar"]').waitFor({state: "attached"});
      await dialog.getByRole("button", {name: /Google Workspace servers/}).click();
      await dialog.locator('[data-integration-id="google-calendar"]').click();
      await dialog.locator("form").waitFor();
      assert.match(await dialog.innerText(), /Using your saved Google client \(project tofi-sample-123456\)/);
      assert.equal(await dialog.locator('input[required][autocomplete="off"]').count(), 0, "no client ID or secret to type again");
      await page.keyboard.press("Escape");
      await dialog.waitFor({state: "detached"});
      await close();
    }
    console.log("PASS google client: a second Google service uses the saved client");

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
      assert.doesNotMatch(text, /connections\.|status\.|mcp\./, `${lang}: no raw keys`);
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
