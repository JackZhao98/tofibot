// Bot settings > Tools (BotTools.tsx). Mounts the real BotTools in a Vite fixture inside the cover's markup, with every /api/* call
// stubbed (synthetic data only), and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
//
// Invariants this pins:
//   - two sections in order: Connections, Skills
//   - a connection row has a switch for this Bot (on by default), its status and tool count, and a chevron that leaves the cover for the
//     connection's page (tofi:open-connection); a paused connection shows Paused with its switch disabled; a failed save puts the switch back
//   - an open connection lists its tools with this Bot's own tier: a tool with none says Default and the connection's tier; choosing a tier
//     is one PUT for this Bot; Default sends an empty tier; a closed or paused connection has no list
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
/** This Bot's own tier per notion tool, as the stub's PUT leaves it. */
let own = {};
try {
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => `import '../src/${match[1]}';`).join("");
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
    if (path === "/api/environments") return q.get("computer") === "none" ? {environments: [], add: {e2b: true, mac: true, vm_command: ""}, e2b: null} : {environments: [{id: "firecracker", kind: "vm", name: "", state: "ready", error_code: "", bot_computer: true, access: "all_bots", actions: ["configure"]}, {id: "e2b", kind: "e2b", name: "", state: "asleep", error_code: "", bot_computer: false, access: "none", actions: ["use", "remove"]}, {id: "mac_1", kind: "mac", name: "Studio MacBook", state: "online", bot_computer: false, access: "allowed_on_mac", actions: ["disconnect"]}], add: {e2b: false, mac: true, vm_command: ""}, e2b: null};
    if (path === "/api/web-tools") return {enabled: true, computer: true, search_offered: !q.get("nosearch"), search: [{id: "native", label: "Claude native", kind: "native", paid: false, fixed: true, enabled: true, status: "always", testable: false}, ...chain(["tavily", "Tavily", "ready"], ["brave", "Brave", "off", false])], fetch: [{id: "builtin", label: "Built-in", kind: "builtin", paid: false, fixed: true, enabled: true, status: "always", testable: false}, ...chain(["firecrawl", "Firecrawl", "ready"])]};
    if (path === "/api/standing-allowances" && method === "GET") return {allowances: q.get("none") ? [] : [{id: "al-1", bot_id: "bot_a", bot_name: "Mochi", server: "notion", tool: "create_page", state: "active", created_at: "2026-10-21T16:00:00Z", use_count: 5}, {id: "al-2", bot_id: "bot_a", bot_name: "Mochi", server: "github", tool: "create_issue", state: "tool_changed", created_at: "2026-10-22T16:00:00Z", use_count: 0}], remembered: q.get("none") ? [] : [{id: "rm-1"}]};
    if (path === "/api/bots/bot_a/tool-tiers/notion" && method === "GET") return {tools: [
      {name: "create_page", tier: own.create_page ?? "write", global_tier: "write", ...(own.create_page ? {bot_tier: own.create_page} : {})},
      {name: "delete_page", tier: "high_risk", global_tier: "write", bot_tier: "high_risk", bot_changed: true},
      {name: "search", tier: "read_only", global_tier: "read_only"},
    ]};
    if (/^\/api\/bots\/bot_a\/tool-tiers\/notion\/[^/]+$/.test(path) && method === "PUT") return {ok: true};
    if (path === "/api/bots/bot_a/skills") return {skills: [{name: "web-research", description: "Search the web and read pages", builtin: true, enabled: true}, {name: "calendar-notes", description: "Keep a running calendar of launches", enabled: true, set_by: "owner", set_at: checked}], bundles: []};
    if (path === "/api/bots/bot_a/debug-preview") return {system_prompt: "x", tools: []};
    return {};
  };
  async function open(params = {}, {width = 1280, height = 900, theme = "light", reducedMotion = "no-preference"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, reducedMotion});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const writes = [];
    own = {};
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      if (method !== "GET") writes.push({method, path: url.pathname, body: request.postData() ?? ""});
      if (method === "PUT" && /\/tool-tiers\/notion\//.test(url.pathname)) { const tier = JSON.parse(request.postData() ?? "{}").tier; own[decodeURIComponent(url.pathname.split("/").pop())] = tier || undefined; }
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""));
      await route.fulfill({status: body?.__status ?? 200, contentType: "application/json", body: JSON.stringify(body ?? {})});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams({lang: "en", ...params})}`);
    try { await page.locator(".bot-tools .tb-ssec").first().waitFor({timeout: 20000}); }
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
      await conn(full.page, "notion").locator(".bt-tiers-trigger").click();
      await full.page.locator(".bt-tool").first().waitFor();
      await full.page.waitForTimeout(500);
      await shot(full.page, "tools-tiers", width, theme);
      await full.close();
      const bare = await open({none: "1"}, {width, height, theme});
      await bare.page.waitForTimeout(500);
      await shot(bare.page, "tools-empty", width, theme);
      await bare.close();
    }
    console.log(`shots saved to ${shots}`);
  }

  // ---- 1. Sections, rows, statuses, switches.
  {
    const {page, close} = await open();
    assert.deepEqual(await page.locator(".bot-tools .tb-ssec").evaluateAll(list => list.map(item => item.id)), ["connections", "skills"], "two sections, in order");
    assert.deepEqual(await page.locator(".bt-conn").evaluateAll(list => list.map(item => item.getAttribute("data-server"))), ["notion", "github", "linear", "acme_tracker"]);
    const text = server => conn(page, server).locator(".bt-row-copy").innerText();
    assert.match(await text("notion"), /Notion\s+Connected · 12 tools$/, "status and tool count");
    assert.match(await text("linear"), /Needs sign-in/);
    assert.match(await text("acme_tracker"), /Paused/);
    const sw = server => conn(page, server).getByRole("switch");
    assert.equal(await sw("notion").isChecked(), true, "a connection nobody limited is on");
    assert.equal(await sw("github").isChecked(), false, "this Bot's choice to keep github off shows");
    assert.equal(await sw("acme_tracker").isDisabled(), true, "a paused connection's switch is disabled");
    assert.equal(await sw("acme_tracker").isChecked(), false);
    assert.equal(await conn(page, "github").getAttribute("data-off"), "true", "an off connection dims");
    assert.equal(await conn(page, "github").locator(".bt-tiers-trigger").count(), 0, "a closed connection has no tool list");
    assert.equal(await conn(page, "acme_tracker").locator(".bt-tiers-trigger").count(), 0, "a paused connection has no tool list");
    assert.equal(await page.locator('.bot-skill-row').count(), 2, "Skills are the library checkboxes");
    assert.equal(await page.locator("#skills .bot-v2-subtitle").count(), 0, "the section is the Skills heading; the Skills view adds none");
    await close();
    console.log("PASS tools: two sections, rows with status and counts, switches follow this Bot's choice, paused and closed have no tool list");
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
    assert.deepEqual((await page.evaluate(() => window.left)).filter(item => item.tab).map(item => item.tab), ["skills"], "the library link opens Settings > Skills");
    await close();
    console.log("PASS switch: one PUT per change, a failed save reverts, links leave the cover and open the right page");
  }

  // ---- 3. Tool tiers per Bot: Default follows the connection, a choice is one PUT, Default sends an empty tier.
  {
    const {page, close, writes} = await open();
    assert.equal(await conn(page, "notion").locator(".bt-tool").count(), 0, "the list loads when the connection is opened");
    await conn(page, "notion").locator(".bt-tiers-trigger").click();
    await page.locator(".bt-tool").first().waitFor();
    assert.deepEqual(await page.locator(".bt-tool").evaluateAll(list => list.map(item => item.getAttribute("data-tool"))), ["create_page", "delete_page", "search"]);
    const pick = tool => page.getByRole("combobox", {name: `Mochi on ${tool}`});
    assert.match(await pick("create_page").innerText(), /Default · Asks when unsure/, "no tier of its own: Default and what the connection says");
    assert.match(await pick("search").innerText(), /Default · Runs without asking/);
    assert.match(await pick("delete_page").innerText(), /Always asks me/, "this Bot's own tier");
    assert.equal(await page.locator('.bt-tool[data-tool="delete_page"] .bt-warning').count(), 1, "a tool that changed since the choice says so");
    await pick("create_page").click();
    await page.getByRole("option", {name: "Runs without asking"}).click();
    await page.waitForFunction(() => /Runs without asking/.test(document.querySelector('.bt-tool[data-tool="create_page"]')?.textContent ?? ""));
    const put = writes.find(item => item.path === "/api/bots/bot_a/tool-tiers/notion/create_page");
    assert.deepEqual(put && JSON.parse(put.body), {tier: "read_only"}, "choosing a tier is one PUT for this Bot");
    assert.equal(await page.locator('.bt-tool[data-tool="create_page"]').getAttribute("data-own"), "true");
    await pick("create_page").click();
    await page.getByRole("option", {name: /^Default/}).click();
    await page.waitForFunction(() => /Default · Asks when unsure/.test(document.querySelector('.bt-tool[data-tool="create_page"]')?.textContent ?? ""));
    const back = writes.filter(item => item.path === "/api/bots/bot_a/tool-tiers/notion/create_page").map(item => JSON.parse(item.body));
    assert.deepEqual(back, [{tier: "read_only"}, {tier: ""}], "Default clears this Bot's own tier");
    await close();
    console.log("PASS tiers: Default follows the connection, a choice is one PUT, Default sends an empty tier");
  }

  // ---- 4. Empty and missing pieces.
  {
    const {page, close} = await open({none: "1"});
    assert.match(await page.locator(".bt-empty").innerText(), /No connections yet\.\s+Add one in Settings/);
    await page.getByRole("button", {name: "Add one in Settings"}).click();
    assert.deepEqual((await page.evaluate(() => window.left)).filter(item => item.tab), [{tab: "connections"}]);
    await close();
    console.log("PASS empty: no connections says so and where to fix it");
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
