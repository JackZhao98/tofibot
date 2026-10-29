// Browser acceptance for links into the settings (docs/design/bot-settings-and-manual.md, slice S2). Mounts the real
// MessageMarkdown and SettingsShell in a Vite fixture with every /api/* call answered with an empty body, then checks:
//   a settings link in a message opens its tab, scrolls to the section and flashes it (data-jump-target);
//   a modified click and a link to another origin are left to the browser;
//   an unknown section opens the page without a section; an old tab id lands on the page that replaced it;
//   the address `/settings/models#defaults` (typed or reloaded) is read by the route reader the app uses.
// Needs PLAYWRIGHT_MODULE.
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".settings-deeplinks-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useEffect,useRef,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';

import {MessageMarkdown} from '../src/MessageMarkdown';
import {readConversationRoute} from '../src/conversationRoute';
import {getLinkDeps,linkRuntime,provideLinkDeps,subscribeSettingsDeepLinks} from '../src/settings/deepLinks';
const bots=[{id:'bot_research',name:'Research Bot',dm_conversation_id:'dm_research',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}];
const content=[
 '[Language](/settings/general#region)',
 '[Defaults](/settings/models#defaults)',
 '[Nowhere](/settings/models#nowhere)',
 '[Old id](/settings/approvals)',
 '[Acme](/settings/connections/acme_tracker?tool=create_issue)',
 '[Bot model](/b/bot_research/settings#model)',
 '[Elsewhere](https://elsewhere.example.test/settings/general#region)',
].join('\\n\\n');
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>('general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:'page'});
 const [open,setOpen]=useState(false);
 const seen=useRef<any[]>([]);
 const openSettings=(next:SettingsTab)=>{setTab(next);setEntry(current=>({seq:current.seq+1,view:'page'}));setOpen(true)};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 useEffect(()=>{
  const log=(entry:any)=>{seen.current.push(entry);(window as any).__links=seen.current};
  const onConnection=(event:Event)=>log({connection:(event as CustomEvent).detail});
  window.addEventListener('tofi:open-connection',onConnection);
  const off=provideLinkDeps(()=>({bots:bots as any,conversations:[{id:'dm_research',kind:'dm',bot_id:'bot_research'} as any],openSettings:next=>openSettings(next),selectConversation:id=>log({select:id}),setMobileList:()=>{},openCover:id=>log({cover:id})}));
  return()=>{off();window.removeEventListener('tofi:open-connection',onConnection)};
 },[]);
 useEffect(()=>{(window as any).__openLink=(link:any)=>linkRuntime().then(module=>module.openSettingsLink(link,getLinkDeps()!))},[]);
 useEffect(()=>{(window as any).__route=(path:string)=>readConversationRoute(...(()=>{const u=new URL(path,location.origin);return [u.pathname,u.hash,u.search] as [string,string,string]})())},[]);
 return <div className="workspace"><div style={{padding:16,maxWidth:480}}><MessageMarkdown content={content}/></div>{open&&<div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>setOpen(false)} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots as any} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/></aside></div>}</div>;
}
void i18nReady.then(()=>setLanguage('en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});
  const context = await browser.newContext({viewport: {width: 1440, height: 900}});
  const page = await context.newPage();
  page.on("pageerror", error => console.log("pageerror:", error.message));
  const json = (status, body) => ({status, contentType: "application/json", body: JSON.stringify(body)});
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) { await route.abort(); return; }
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    if (url.pathname === "/api/server-info") { await route.fulfill(json(200, {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}})); return; }
    if (url.pathname === "/api/auth/session") { await route.fulfill(json(200, {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}})); return; }
    await route.fulfill(json(404, {error: {code: "not_found", message: "not stubbed"}}));
  });
  await page.goto(`${origin}/${basename(fixture)}/index.html`);
  await page.getByRole("link", {name: "Language"}).waitFor();

  // A link in a message opens its tab, finds the section and frames it.
  await page.getByRole("link", {name: "Defaults"}).click();
  await page.locator('.settings-content [data-page="models"] [data-section="defaults"][data-jump-target]').waitFor({timeout: 4000});
  assert.equal(await page.locator('.settings-tabs [data-tab="models"]').getAttribute("aria-current"), "page", "the Models tab is selected");
  await page.waitForFunction(() => !document.querySelector("[data-jump-target]"), null, {timeout: 4000});
  console.log("PASS a settings link opens its tab, scrolls to the section and flashes it once");

  // The same link twice flashes again (the attribute restarts).
  await page.getByRole("link", {name: "Defaults"}).click();
  await page.locator('[data-section="defaults"][data-jump-target]').waitFor({timeout: 4000});
  console.log("PASS jumping to the same section twice flashes twice");

  await page.getByRole("link", {name: "Language"}).click();
  await page.locator('.settings-content [data-page="general"] [data-section="region"][data-jump-target]').waitFor({timeout: 4000});
  console.log("PASS general > region");

  // An unknown section opens the page and nothing else.
  await page.waitForFunction(() => !document.querySelector("[data-jump-target]"), null, {timeout: 4000});
  await page.getByRole("link", {name: "Nowhere"}).click();
  await page.locator('.settings-tabs [data-tab="models"][aria-current="page"]').waitFor();
  assert.equal(await page.locator("[data-jump-target]").count(), 0, "an unknown section frames nothing");
  console.log("PASS an unknown section opens the page without a jump");

  // The old tab id lands on the page that replaced it.
  await page.getByRole("link", {name: "Old id"}).click();
  await page.locator('.settings-tabs [data-tab="permissions"][aria-current="page"]').waitFor();
  console.log("PASS /settings/approvals opens Permissions");

  // A modified click is the browser's; a link to another origin is not ours.
  const prevented = await page.evaluate(() => new Promise(resolve => {
    const link = [...document.querySelectorAll("a")].find(a => a.textContent === "Language");
    const stop = event => { event.preventDefault(); resolve(false); };
    link.addEventListener("click", event => { resolve(event.defaultPrevented); event.preventDefault(); }, {once: true});
    link.dispatchEvent(new MouseEvent("click", {bubbles: true, cancelable: true, ctrlKey: true, button: 0}));
    void stop;
  }));
  assert.equal(prevented, false, "a Ctrl-click is left to the browser");
  assert.equal(await page.getByRole("link", {name: "Elsewhere"}).getAttribute("target"), "_blank", "another origin opens as an ordinary link");
  console.log("PASS modified click and other origins are not intercepted");

  // Links that name a Bot or a connection are handed to the app, which resolves them against its own index.
  await page.getByRole("link", {name: "Bot model"}).click();
  await page.getByRole("link", {name: "Acme"}).click();
  await page.waitForFunction(() => (window.__links ?? []).length >= 3);
  const links = await page.evaluate(() => window.__links);
  assert.deepEqual(links, [
    {select: "dm_research"},
    {cover: "bot-settings"},
    {connection: {server: "acme_tracker", tool: "create_issue"}},
  ]);
  console.log("PASS bot-settings and connection links open the cover of a Bot in the index and the connection page");
  // A Bot that is not in the account's index opens nothing.
  await page.evaluate(() => { window.__links.length = 0; });
  await page.evaluate(() => window.__openLink({kind: "bot-settings", botId: "someone_elses_bot", section: "model"}));
  await page.waitForTimeout(400);
  assert.deepEqual(await page.evaluate(() => window.__links), [], "an unknown Bot opens nothing");
  console.log("PASS an id outside the index opens nothing");

  // The route reader the app uses at start: the address is read once, covers stay out of the address afterwards.
  const routes = await page.evaluate(() => ["/settings/models#defaults", "/b/bot_research/settings#model", "/g/team/settings#members", "/settings/nowhere", "/b//settings"].map(path => window.__route(path)));
  assert.deepEqual(routes[0], {kind: "home", settings: "/settings/models#defaults"});
  assert.deepEqual(routes[1], {kind: "bot", id: "bot_research", settings: "/b/bot_research/settings#model"});
  assert.deepEqual(routes[2], {kind: "group", id: "team", settings: "/g/team/settings#members"});
  assert.equal(routes[3].kind, "home", "an unknown settings page is read as an address and resolves to nothing");
  assert.deepEqual(routes[4], {kind: "unavailable"});
  console.log("PASS addresses are read as settings routes");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
