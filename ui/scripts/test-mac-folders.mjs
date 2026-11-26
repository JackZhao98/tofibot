// Settings > Environments > "Folders on this Mac" (Mac app only): the block renders from a stubbed window.tofiDesktop.folders in every
// state (two folders with one shared by two Bots, empty, a moved folder, commands unavailable, refused folders after an add, unsupported
// server, no bridge), the menus call the bridge with the right arguments, errors are named reasons, and every locale renders from its own
// catalog. Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed. Needs PLAYWRIGHT_MODULE.
//   MAC_FOLDERS_SHOTS=<dir>  also saves 2x screenshots (light and dark).
import assert from "node:assert/strict";
import { viteCacheDir } from "./lib/vite-cache.mjs";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.MAC_FOLDERS_SHOTS;
if (shots) await mkdir(shots, {recursive: true});
const LANGS = ["en", "de", "fr", "ja", "ko", "zh-CN", "zh-TW"];
const catalogs = {};
for (const lang of LANGS) catalogs[lang] = JSON.parse(await readFile(join(ui, "src/locales", lang, "computer.json"), "utf8")).environments.folders;
const L = (lang, path, vars = {}) => path.split(".").reduce((node, key) => node[key], catalogs[lang]).replace(/\{\{(\w+)\}\}/g, (_, name) => vars[name]);

const fixture = await mkdtemp(join(ui, ".macfolders-"));
let server, browser;
try {
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {setComputerCapability} from '../src/computerCapability';
const query=new URLSearchParams(location.search);
setComputerCapability({backend:'kvm',available:true,capabilities:['shell','files','browser','desktop','terminal','resources'],personal_computers:true} as any);
const bots=[{id:'bot_tofi',name:'Tofi',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_momo',name:'Momo',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_old',name:'Retired',archived:true,model:'default',reasoning_effort:'default'}] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>('computer');
 const [entry]=useState<SettingsEntry>({seq:0,view:'page'});
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={{preference:'light',choose:()=>{}}} extensionRefresh={0} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}} openTab={setTab}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Settings/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({cacheDir: viteCacheDir(), configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const errors = [];
  const json = (route, status, body) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(body)});
  const folder = (id, name, path, bots, state = "ok") => ({id, name, path, bots, state});
  const TOFI = {id: "bot_tofi", name: "Tofi"}, MOMO = {id: "bot_momo", name: "Momo"};
  const status = (extra = {}) => ({supported: true, reason: null, exec: {available: true, reason: null}, folders: [], tools: ["/opt/homebrew/bin", "~/.cargo/bin", "~/.nvm/versions/node"], ...extra});
  const two = () => status({folders: [folder("f1", "tofibot", "/Users/jackzhao/Developer/sentiosurge/tofibot", [TOFI, MOMO]), folder("f2", "notes", "/Users/jackzhao/Documents/notes", [TOFI])]});

  /** `scenario`: the first FoldersStatus, and what the stub answers to add()/remove(). */
  async function open({scenario = two(), addResult, removeResult, bridge = true, lang = "en", theme = "light", width = 1280, height = 900} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, deviceScaleFactor: shots ? 2 : 1});
    await context.addInitScript(({theme, scenario, addResult, removeResult, bridge}) => {
      try { localStorage.setItem("tofi:appearance", theme); } catch {}
      const off = () => () => {};
      const calls = [], listeners = new Set();
      let current = JSON.parse(JSON.stringify(scenario));
      window.__calls = calls;
      window.__push = next => { current = next; listeners.forEach(listener => listener(JSON.parse(JSON.stringify(next)))); };
      const snap = () => JSON.parse(JSON.stringify(current));
      const folders = {
        status: async () => { calls.push(["status"]); return snap(); },
        add: async botID => { calls.push(["add", botID]); const result = addResult || {ok: true, added: ["/x"]}; if (result.ok) current = result.status || current; return {...result, status: result.status || snap()}; },
        remove: async (id, botID) => {
          calls.push(botID === undefined ? ["remove", id] : ["remove", id, botID]);
          if (removeResult) return {...removeResult, status: snap()};
          current = {...current, folders: current.folders.flatMap(item => item.id !== id ? [item] : botID && item.bots.length > 1 ? [{...item, bots: item.bots.filter(bot => bot.id !== botID)}] : [])};
          return {ok: true, status: snap()};
        },
        reveal: async id => { calls.push(["reveal", id]); },
        onStatus: listener => { listeners.add(listener); return () => listeners.delete(listener); },
      };
      window.tofiDesktop = {platform: "darwin", themePreference: theme, onCommand: off, onWindowState: off, onFlushState: off, appearance: async () => {}, readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {}, remoteInputFocus: () => {}, startAccessibilityDrag: () => {}, requestMicrophoneAccess: async () => false, ...(bridge ? {folders} : {})};
    }, {theme, scenario, addResult, removeResult, bridge});
    const page = await context.newPage();
    page.on("pageerror", error => errors.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), path = url.pathname;
      if (url.origin !== origin) { await route.abort(); return; }
      if (!path.startsWith("/api/")) { await route.continue(); return; }
      if (path === "/api/auth/session") return json(route, 200, {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}});
      if (path === "/api/server-info") return json(route, 200, {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}});
      if (path === "/api/bots") return json(route, 200, {bots: []});
      if (path === "/api/automation-settings") return json(route, 200, {items: []});
      if (path === "/api/preferences") return json(route, 200, {timezone: "America/Los_Angeles", timezone_configured: true});
      if (path === "/api/environments") return json(route, 200, {environments: [{id: "firecracker", kind: "vm", name: "", state: "ready", error_code: "", bot_computer: true, access: "all_bots", actions: ["configure"]}], add: {e2b: true, mac: true, vm_command: ""}, e2b: null});
      return json(route, 200, {});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?lang=${lang}`);
    await page.locator(".environments-page").waitFor({timeout: 20000});
    // Devices: folders live in this Mac's detail view, opened from its row under My devices.
    await page.locator("[data-this-mac] button").click();
    await page.locator('[data-mac-detail="this-mac"]').waitFor();
    return {page, close: () => context.close(), calls: () => page.evaluate(() => window.__calls.filter(call => call[0] !== "status"))};
  }
  const block = page => page.locator('[data-section="mac-folders"]');
  const shot = async (page, name) => { if (shots) { await page.waitForTimeout(400); await block(page).scrollIntoViewIfNeeded(); await page.screenshot({path: join(shots, name)}); } };
  const menuItem = (page, name) => page.getByRole("menuitem", {name, exact: true}).or(page.locator(".env-menu button", {hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`)})).first();

  // ---- 1. Two folders: icon, name, path, Bot names; the row menu and the add menu call the bridge with the right arguments.
  {
    const {page, close, calls} = await open();
    await block(page).waitFor();
    assert.equal(await block(page).locator(".mf-row").count(), 2);
    assert.equal((await block(page).locator("h4").innerText()).trim(), L("en", "title"));
    assert.equal(await block(page).locator(".mf-head-copy p").innerText(), L("en", "description"));
    const first = block(page).locator('[data-folder="f1"]');
    assert.equal(await first.locator(".env-name-text").innerText(), "tofibot");
    assert.equal(await first.locator(".mf-path").innerText(), "/Users/jackzhao/Developer/sentiosurge/tofibot");
    assert.equal(await first.locator(".mf-bots").innerText(), "Tofi, Momo");
    assert.equal(await block(page).locator('[data-folder="f2"] .mf-bots').innerText(), "Tofi");
    assert.equal(await block(page).locator("[data-folder-state]").count(), 0, "an ok folder has no state line");
    assert.equal(await block(page).locator("[data-folders-exec], [data-folders-error], [data-folders-empty]").count(), 0);
    const menuOf = id => block(page).locator(`[data-folder="${id}"] .env-more`);
    // Row menu of a two-Bot folder: Show in Finder, one Remove per Bot, Remove folder.
    await menuOf("f1").click();
    assert.deepEqual(await page.locator(".env-menu button[data-action]").evaluateAll(list => list.map(item => item.textContent.trim())), [L("en", "reveal"), L("en", "remove_bot", {name: "Tofi"}), L("en", "remove_bot", {name: "Momo"}), L("en", "remove")]);
    await shot(page, "folders-two-menu-1280-light.png");
    await page.locator('.env-menu button[data-action="reveal"]').click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "reveal"));
    await menuOf("f1").click();
    await page.locator('.env-menu button[data-action="remove-bot"][data-bot="bot_momo"]').click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "remove"));
    await page.waitForFunction(() => document.querySelector('[data-folder="f1"] .mf-bots')?.textContent === "Tofi");
    // A one-Bot folder: no per-Bot entry (it would repeat Remove folder).
    await menuOf("f2").click();
    assert.deepEqual(await page.locator(".env-menu button[data-action]").evaluateAll(list => list.map(item => item.dataset.action)), ["reveal", "remove"]);
    await page.locator('.env-menu button[data-action="remove"]').click();
    await page.waitForFunction(() => !document.querySelector('[data-folder="f2"]'));
    assert.deepEqual(await calls(), [["reveal", "f1"], ["remove", "f1", "bot_momo"], ["remove", "f2"]]);
    // Removing the last folders shows the empty line.
    await menuOf("f1").click();
    await page.locator('.env-menu button[data-action="remove"]').click();
    await block(page).locator("[data-folders-empty]").waitFor();
    assert.equal(await block(page).locator("[data-folders-empty]").innerText(), L("en", "empty"));
    await close();
  }

  // ---- 2. Add: the menu lists the active Bots only; choosing one calls add(bot.id); cancelled shows nothing.
  {
    const {page, close, calls} = await open({scenario: status(), addResult: {ok: false, code: "cancelled"}});
    await block(page).locator("[data-folders-empty]").waitFor();
    await block(page).locator('[data-action="add"]').click();
    assert.equal((await page.locator(".mf-menu-title").innerText()).trim(), L("en", "add_for"));
    assert.deepEqual(await page.locator("[data-add-bot]").evaluateAll(list => list.map(item => item.textContent.trim())), ["Tofi", "Momo"], "an archived Bot is not offered");
    await page.locator('[data-add-bot="bot_momo"]').click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "add"));
    await page.waitForFunction(() => !document.querySelector('[data-action="add"]').disabled);
    assert.deepEqual(await calls(), [["add", "bot_momo"]]);
    assert.equal(await block(page).locator("[data-folders-error], [data-folders-refused]").count(), 0, "cancel shows nothing");
    await close();
  }

  // ---- 3. Refused folders after an add: one named line each; a failure is a named reason, never raw text.
  {
    const refusedStatus = two();
    const {page, close} = await open({scenario: status(), addResult: {ok: true, added: ["/Users/jackzhao/Code"], refused: [{path: "/System", code: "folder_protected"}, {path: "/Volumes/Share", code: "network_volume"}], status: refusedStatus}});
    await block(page).locator('[data-action="add"]').click();
    await page.locator('[data-add-bot="bot_tofi"]').click();
    await block(page).locator("[data-folders-refused]").first().waitFor();
    assert.deepEqual(await block(page).locator("[data-folders-refused]").allInnerTexts(), [
      L("en", "refused", {path: "/System", reason: L("en", "reason.folder_protected")}),
      L("en", "refused", {path: "/Volumes/Share", reason: L("en", "reason.network_volume")})]);
    assert.equal(await block(page).locator(".mf-row").count(), 2, "the returned status replaces the list");
    await shot(page, "folders-refused-1280-light.png");
    await close();
    const failing = await open({addResult: {ok: false, code: "too_many_folders", message: "raw upstream text"}});
    await failing.page.locator('[data-action="add"]').click();
    await failing.page.locator('[data-add-bot="bot_tofi"]').click();
    await failing.page.locator("[data-folders-error]").waitFor();
    const text = await failing.page.locator("[data-folders-error]").innerText();
    assert.equal(text, L("en", "reason.too_many_folders").replace(/^./, c => c.toUpperCase()));
    assert.ok(!text.includes("raw upstream"));
    await failing.close();
    const unknown = await open({addResult: {ok: false, code: "something_new"}});
    await unknown.page.locator('[data-action="add"]').click();
    await unknown.page.locator('[data-add-bot="bot_tofi"]').click();
    await unknown.page.locator("[data-folders-error]").waitFor();
    assert.equal(await unknown.page.locator("[data-folders-error]").innerText(), L("en", "reason.failed"), "an unknown code reads as the generic reason");
    await unknown.close();
  }

  // ---- 4. A moved folder carries a named reason; commands unavailable has one line; the tools disclosure is collapsed and read-only.
  {
    const scenario = status({exec: {available: false, reason: "sandbox_missing"}, folders: [folder("f1", "tofibot", "/Users/jackzhao/tofibot", [TOFI], "moved"), folder("f2", "gone", "/Users/jackzhao/gone", [TOFI, MOMO], "missing")]});
    const {page, close} = await open({scenario});
    await block(page).waitFor();
    assert.equal(await block(page).locator('[data-folder="f1"] [data-folder-state]').innerText(), L("en", "state.moved"));
    assert.equal(await block(page).locator('[data-folder="f2"] [data-folder-state]').innerText(), L("en", "state.missing"));
    assert.equal(await block(page).locator("[data-folders-exec]").innerText(), L("en", "exec_unavailable"));
    const tools = block(page).locator("[data-folders-tools]");
    assert.equal(await tools.locator("code").count() > 0 && await tools.locator("code").first().isVisible(), false, "the tool list starts collapsed");
    await tools.getByRole("button", {name: L("en", "tools")}).click();
    assert.deepEqual(await tools.locator("code").allInnerTexts(), ["/opt/homebrew/bin", "~/.cargo/bin", "~/.nvm/versions/node"]);
    assert.equal(await tools.locator("button, input, a").count(), 1, "the disclosure has no controls beyond its trigger");
    await page.waitForTimeout(300);
    await shot(page, "folders-moved-exec-1280-light.png");
    await close();
  }

  // ---- 5. Nothing renders without a bridge, or when the server does not support folders; status events replace the list; one action at a time.
  {
    const none = await open({bridge: false});
    assert.equal(await block(none.page).count(), 0, "no bridge, no block");
    await none.close();
    const unsupported = await open({scenario: status({supported: false, reason: "server_unsupported"})});
    await unsupported.page.waitForFunction(() => window.__calls.some(call => call[0] === "status"));
    assert.equal(await block(unsupported.page).count(), 0, "unsupported server, no block");
    await unsupported.page.evaluate(next => window.__push(next), two());
    await block(unsupported.page).waitFor();
    assert.equal(await block(unsupported.page).locator(".mf-row").count(), 2, "onStatus makes it appear");
    assert.equal((await unsupported.calls()).length, 0, "no polling, no action");
    await unsupported.close();
  }

  // ---- 6. Controls are disabled while an action runs.
  {
    const {page, close} = await open();
    await page.evaluate(() => { const real = window.tofiDesktop.folders.add; window.__release = null; window.tofiDesktop.folders.add = id => new Promise(resolve => { window.__release = () => resolve({ok: false, code: "cancelled", status: {supported: true, reason: null, exec: {available: true, reason: null}, folders: [], tools: []}}); }); });
    await block(page).locator('[data-action="add"]').click();
    await page.locator('[data-add-bot="bot_tofi"]').click();
    await page.waitForFunction(() => document.querySelector('[data-action="add"]').disabled);
    assert.equal(await block(page).locator(".mf-row .env-more").evaluateAll(list => list.every(item => item.disabled)), true);
    await page.evaluate(() => window.__release());
    await page.waitForFunction(() => !document.querySelector('[data-action="add"]').disabled);
    await close();
  }

  // ---- 7. Every locale renders the block from its own catalog, with no key showing through and no overflow at 390 px.
  for (const lang of LANGS) {
    const {page, close} = await open({lang, width: 390, height: 900, scenario: status({exec: {available: false, reason: null}, folders: [folder("f1", "tofibot", "/Users/jackzhao/Developer/sentiosurge/tofibot", [TOFI, MOMO], "moved")]})});
    await block(page).waitFor();
    const text = await block(page).innerText();
    for (const part of [L(lang, "title"), L(lang, "description"), L(lang, "add"), L(lang, "state.moved"), L(lang, "exec_unavailable"), L(lang, "tools")]) assert.ok(text.replace(/\s+/g, " ").includes(part), `${lang}: ${part}`);
    assert.ok(!/environments\.folders\./.test(text), `${lang}: a key shows through`);
    const overflow = await page.evaluate(() => { const el = document.querySelector('[data-section="mac-folders"]'); return [...el.querySelectorAll("*")].filter(node => { const r = node.getBoundingClientRect(); return r.width > 0 && r.right > innerWidth + 0.5; }).length; });
    assert.equal(overflow, 0, `${lang}: nothing overflows at 390 px`);
    if (shots && (lang === "en" || lang === "ja")) { await block(page).scrollIntoViewIfNeeded(); await page.screenshot({path: join(shots, `folders-390-${lang}-light.png`)}); }
    await close();
  }

  // ---- 8. Dark theme, with a menu open.
  {
    const {page, close} = await open({theme: "dark"});
    await block(page).waitFor();
    await block(page).locator('[data-action="add"]').click();
    await shot(page, "folders-two-add-menu-1280-dark.png");
    await page.keyboard.press("Escape");
    await shot(page, "folders-two-1280-dark.png");
    await close();
  }

  assert.deepEqual(errors, [], "page errors");
  console.log("PASS mac folders block: states, menus, bridge arguments, named reasons, locales, dark");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
