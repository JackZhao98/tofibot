// Bot settings > Tools (docs/design/tools-and-connections.md section 3.4). Mounts the real BotTools in a Vite fixture inside the cover's
// markup, with every /api/* call stubbed (synthetic data only), and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//
// Invariants this pins:
//   - six groups in order: Connections, Skills, Web, Computer, Built in, Permissions for this Bot
//   - a connection row has a switch for this Bot (on by default), its status and counts, and a chevron that leaves the cover for the
//     connection's page (tofi:open-connection); a paused connection shows Paused with its switch disabled; a failed save puts the switch back
//   - Web and Computer are read-only facts that follow workspace settings; Built in is folded
//   - a revoked allowance folds away (data-leaving) and is gone; reduced motion removes it at once
//   - 390 px has no horizontal scroll; every locale renders without raw keys
//
// BT_SHOTS=<dir> also saves screenshots (1280 / 390 px, light / dark).
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.BT_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

const fixture = await mkdtemp(join(ui, ".bot-tools-"));
let server, browser;
const unexpected = [];
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import '../src/bot-settings.css';
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {setComputerCapability} from '../src/computerCapability';
import {BotTools} from '../src/BotTools';
const query=new URLSearchParams(location.search);
if(query.get('computer')==='none')setComputerCapability({backend:'none',available:false,capabilities:[]} as any);
(window as any).left=[];
window.addEventListener('tofi:open-connection',event=>(window as any).left.push({server:(event as CustomEvent).detail.server}));
function Fixture(){
 return <div className="workspace"><div className="cover-body"><div className="bot-settings" data-view="page" style={{display:'block',height:'auto'}}><div className="bs-page"><div className="bs-scroll" style={{overflow:'visible'}}><div className="bs-sections"><section className="bs-section" data-section="tools">
  <BotTools bot={{id:'bot_a',name:'Mochi'}} leaveTo={go=>{(window as any).left.push('cover-close');go();}} onOpenSettings={tab=>(window as any).left.push({tab})}/>
 </section></div></div></div></div></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const checked = "2026-10-26T12:00:00Z";
  const servers = [
    {name: "notion", url: "https://mcp.notion.com/mcp", oauth: {client_id: "", connected: true}, status: {checked_at: checked, ok: true, tool_count: 12}},
    {name: "github", url: "https://api.githubcopilot.com/mcp/", status: {checked_at: checked, ok: true, tool_count: 9}},
    {name: "linear", url: "https://mcp.linear.app/mcp", oauth: {client_id: "", connected: false}},
    {name: "acme_tracker", url: "https://mcp.example.test/tracker", enabled: false, status: {checked_at: checked, ok: true, tool_count: 4}},
  ];
  const chain = (...items) => items.map(([id, label, status, enabled = true]) => ({id, label, kind: "api", paid: false, fixed: false, enabled, status, testable: false}));
  const stub = (url, method, q) => {
    const path = url.pathname;
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}};
    if (path === "/api/auth/session") return {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/bots") return {bots: [{id: "bot_a", name: "Mochi", archived: false}]};
    if (path === "/api/extensions/mcp") return {servers: q.get("none") ? [] : servers};
    if (path === "/api/bots/bot_a/connections" && method === "GET") return {connections: q.get("none") ? [] : servers.map(item => ({server: item.name, enabled: item.name !== "github"}))};
    if (path === "/api/bots/bot_a/connections/linear" && method === "PUT") return {__status: 500, error: {code: "internal", message: "connection access not saved"}};
    if (/^\/api\/bots\/bot_a\/connections\/[^/]+$/.test(path) && method === "PUT") return {ok: true};
    if (path === "/api/web-tools") return {enabled: true, computer: true, search_offered: !q.get("nosearch"), search: [{id: "native", label: "Claude native", kind: "native", paid: false, fixed: true, enabled: true, status: "always", testable: false}, ...chain(["tavily", "Tavily", "ready"], ["brave", "Brave", "off", false])], fetch: [{id: "builtin", label: "Built-in", kind: "builtin", paid: false, fixed: true, enabled: true, status: "always", testable: false}, ...chain(["firecrawl", "Firecrawl", "ready"])]};
    if (path === "/api/standing-allowances" && method === "GET") return {allowances: q.get("none") ? [] : [{id: "al-1", bot_id: "bot_a", bot_name: "Mochi", server: "notion", tool: "create_page", state: "active", created_at: "2026-10-21T16:00:00Z", use_count: 5}, {id: "al-2", bot_id: "bot_a", bot_name: "Mochi", server: "github", tool: "create_issue", state: "tool_changed", created_at: "2026-10-22T16:00:00Z", use_count: 0}], remembered: q.get("none") ? [] : [{id: "rm-1"}]};
    if (path === "/api/bots/bot_a/skills") return {skills: [{name: "web-research", description: "Search the web and read pages", builtin: true, enabled: true}, {name: "calendar-notes", description: "Keep a running calendar of launches", enabled: true, set_by: "owner", set_at: checked}], bundles: []};
    if (path === "/api/bots/bot_a/debug-preview") return {system_prompt: "x", tools: []};
    return {};
  };
  async function open(params = {}, {width = 1280, height = 900, theme = "light", reducedMotion = "no-preference"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, reducedMotion});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const writes = [];
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      if (method !== "GET") writes.push({method, path: url.pathname, body: request.postData() ?? ""});
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""));
      await route.fulfill({status: body?.__status ?? 200, contentType: "application/json", body: JSON.stringify(body ?? {})});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams({lang: "en", ...params})}`);
    try { await page.locator(".bot-tools .bt-group").first().waitFor({timeout: 20000}); }
    catch (error) { console.log("page errors:", unexpected, "\nbody:", (await page.locator("body").innerText().catch(() => "")).slice(0, 400)); throw error; }
    await page.locator(".bot-tools .bt-conn, .bot-tools .bt-empty").first().waitFor();
    return {page, writes, close: () => context.close()};
  }
  const shot = async (page, name, width, theme) => { if (shots) await page.screenshot({path: join(shots, `${name}-${width}-${theme}.png`), fullPage: true}); };
  const conn = (page, server) => page.locator(`.bt-conn[data-server="${server}"]`);

  if (shots) {
    for (const [width, height] of [[1280, 900], [390, 844]]) for (const theme of ["light", "dark"]) {
      const full = await open({}, {width, height, theme});
      await full.page.waitForTimeout(700);
      await shot(full.page, "tools", width, theme);
      await full.page.getByRole("button", {name: /What every Bot can do/}).click();
      await full.page.waitForTimeout(500);
      await shot(full.page, "tools-builtin-open", width, theme);
      await full.close();
      const bare = await open({none: "1", computer: "none"}, {width, height, theme});
      await bare.page.waitForTimeout(500);
      await shot(bare.page, "tools-empty", width, theme);
      await bare.close();
    }
    console.log(`shots saved to ${shots}`);
  }

  // ---- 1. Groups, rows, statuses, switches.
  {
    const {page, close} = await open();
    assert.deepEqual(await page.locator(".bot-tools .bt-group").evaluateAll(list => list.map(item => item.getAttribute("data-group"))), ["connections", "skills", "web", "computer", "builtin", "permissions"], "six groups, in order");
    assert.deepEqual(await page.locator(".bt-conn").evaluateAll(list => list.map(item => item.getAttribute("data-server"))), ["notion", "github", "linear", "acme_tracker"]);
    const text = server => conn(page, server).locator(".bt-row-copy").innerText();
    assert.match(await text("notion"), /Notion\s+Connected · 12 tools · 1 always allowed/, "status, tool count and this Bot's allowances");
    assert.match(await text("linear"), /Needs sign-in/);
    assert.match(await text("acme_tracker"), /Paused/);
    const sw = server => conn(page, server).getByRole("switch");
    assert.equal(await sw("notion").isChecked(), true, "a connection nobody limited is on");
    assert.equal(await sw("github").isChecked(), false, "this Bot's choice to keep github off shows");
    assert.equal(await sw("acme_tracker").isDisabled(), true, "a paused connection's switch is disabled");
    assert.equal(await sw("acme_tracker").isChecked(), false);
    assert.equal(await conn(page, "github").getAttribute("data-off"), "true", "an off connection dims");
    assert.match(await page.locator('[data-group="web"] dl').innerText(), /Search\s+On · Claude native → Tavily\s+Fetch\s+On · Built-in → Firecrawl/, "the web chains in order, only the sources that are on");
    assert.match(await page.locator('[data-group="computer"]').innerText(), /Every Bot uses the Bot computer/);
    assert.equal(await page.locator('[data-group="builtin"] .disclosure-region').getAttribute("aria-hidden"), "true", "Built in is folded");
    assert.equal(await page.locator('.bot-skill-row').count(), 2, "Skills are the library checkboxes");
    assert.equal(await page.locator('[data-group="skills"] h2').count(), 0, "the group is the Skills heading; the Skills view adds none");
    assert.equal(await page.locator('[data-allowance-id]').count(), 2);
    assert.match(await page.locator('[data-group="permissions"] > .bt-remembered').innerText(), /Remembered in 1 chat/);
    await close();
    console.log("PASS tools: six groups, rows with status and counts, switches follow this Bot's choice, paused disabled, web and computer facts, built in folded");
  }

  // ---- 2. The switch is one PUT; a failed save puts it back; the chevron leaves the cover for the connection's page.
  {
    const {page, close, writes} = await open();
    await conn(page, "notion").getByRole("switch").click();
    await page.waitForFunction(() => document.querySelector('.bt-conn[data-server="notion"]')?.getAttribute("data-off") === "true");
    const put = writes.find(item => item.path === "/api/bots/bot_a/connections/notion");
    assert.deepEqual(put && JSON.parse(put.body), {enabled: false}, "turning a connection off is one PUT for this Bot");
    await conn(page, "github").getByRole("switch").click();
    await page.waitForFunction(() => document.querySelector('.bt-conn[data-server="github"]')?.getAttribute("data-off") === null);
    assert.equal(await conn(page, "github").getByRole("switch").isChecked(), true);
    await conn(page, "linear").getByRole("switch").click();
    await page.getByRole("alert").filter({hasText: /not saved/}).waitFor();
    assert.equal(await conn(page, "linear").getByRole("switch").isChecked(), true, "a failed save puts the switch back");
    await conn(page, "notion").getByRole("button", {name: "Open Notion in Settings"}).click();
    assert.deepEqual(await page.evaluate(() => window.left), ["cover-close", {server: "notion"}], "the cover closes first, then the connection's page is opened");
    await page.getByRole("button", {name: "Manage library"}).click();
    await page.getByRole("button", {name: "Configure sources"}).click();
    await page.getByRole("button", {name: "All permissions"}).click();
    assert.deepEqual((await page.evaluate(() => window.left)).filter(item => item.tab).map(item => item.tab), ["skills", "tools", "permissions"], "each link opens its Settings page");
    await close();
    console.log("PASS switch: one PUT per change, a failed save reverts, links leave the cover and open the right page");
  }

  // ---- 3. Revoking an allowance folds the row away; reduced motion removes it at once.
  {
    const {page, close, writes} = await open();
    await page.locator('[data-allowance-id="al-1"]').getByRole("button", {name: "Revoke"}).click();
    await page.locator('[data-allowance-id="al-1"][data-leaving]').waitFor({timeout: 2000});
    await page.locator('[data-allowance-id="al-1"]').waitFor({state: "detached", timeout: 3000});
    assert.ok(writes.some(item => item.method === "DELETE" && item.path === "/api/standing-allowances/al-1"), "Revoke deletes that allowance");
    assert.equal(await page.locator('[data-allowance-id="al-2"] .bt-warning').count(), 1, "a voided allowance says it asks again");
    assert.match(await conn(page, "notion").locator(".bt-row-copy").innerText(), /Connected · 12 tools\s*$/, "the connection's count drops with the allowance");
    await close();
    const reduced = await open({}, {reducedMotion: "reduce"});
    await reduced.page.locator('[data-allowance-id="al-2"]').getByRole("button", {name: "Revoke"}).click();
    await reduced.page.locator('[data-allowance-id="al-2"]').waitFor({state: "detached", timeout: 1000});
    await reduced.close();
    console.log("PASS allowances: Revoke folds the row away (instantly with reduced motion), the count follows");
  }

  // ---- 4. Empty and missing pieces.
  {
    const {page, close} = await open({none: "1", computer: "none", nosearch: "1"});
    assert.match(await page.locator(".bt-empty").innerText(), /No connections yet\.\s+Add one in Settings/);
    assert.match(await page.locator('[data-group="web"] dl').innerText(), /Off · no source configured/);
    assert.match(await page.locator('[data-group="computer"]').innerText(), /No Bot computer yet/);
    assert.match(await page.locator('[data-group="permissions"]').innerText(), /Nothing is allowed without asking/);
    await page.getByRole("button", {name: "Add one in Settings"}).click();
    assert.deepEqual((await page.evaluate(() => window.left)).filter(item => item.tab), [{tab: "connections"}]);
    await close();
    console.log("PASS empty: no connections, no search source, no Bot computer, no allowances each say so and where to fix it");
  }

  // ---- 5. Phone width and every locale.
  {
    const {page, close} = await open({}, {width: 390, height: 844});
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert.ok(overflow <= 0, `no horizontal page scroll at 390 px, got ${overflow}`);
    await close();
    for (const lang of ["zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
      const {page: p, close: c} = await open({lang});
      const text = await p.locator(".bot-tools").innerText();
      assert.doesNotMatch(text, /\b(tools|extensions|settings|bots)[.:][a-z_.]+\b/, `${lang}: no raw keys`);
      await c();
    }
    console.log("PASS phone and locales: no horizontal scroll at 390 px; six more languages render without raw keys");
  }
  assert.deepEqual(unexpected, [], "no page error");
  console.log("PASS no page error");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
