// Computer-decoupling S2 (docs/design/computer-decoupling.md section 13): the computer's configuration shows in Settings
// only while a computer is attached, "What is on this computer" and the wipe list say what is lost, and a file whose
// bytes are on a detached computer says so. Mounts the real SettingsShell + SettingsPages and the real attachment card
// and viewer in a Vite fixture with every /api/* call stubbed (synthetic data only). Needs PLAYWRIGHT_MODULE.
//   NO_COMPUTER_SHOTS=<dir>  also saves screenshots at 1440x900 and 390x844.
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.NO_COMPUTER_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

const LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"];
const catalog = async (lang, ns) => JSON.parse(await readFile(join(ui, "src/locales", lang, `${ns}.json`), "utf8"));

const fixture = await mkdtemp(join(ui, ".no-computer-"));
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
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {subscribeSettingsDeepLinks,OPEN_COMPUTER_SETTINGS_EVENT} from '../src/settings/deepLinks';
import {CodexPanel} from '../src/settings/AppSlots';
import {setComputerCapability} from '../src/computerCapability';
import {AttachmentList} from '../src/MessageAttachment';
import {FilePreviewHost} from '../src/FileViewer';
const query=new URLSearchParams(location.search);
setComputerCapability(query.get('computer')==='none'?{backend:'none',available:false,capabilities:[]}:{backend:'kvm',available:true,capabilities:['shell','files','browser','desktop','terminal','resources','secret_sink']});
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
const files=[
 {id:'att_detached',conversation_id:'c',name:'quarterly-report.csv',mime:'text/csv',size:2048,created_at:'2026-10-27T10:00:00Z'},
 {id:'att_missing',conversation_id:'c',name:'notes.txt',mime:'text/plain',size:512,created_at:'2026-10-27T10:00:00Z'},
 {id:'att_ok',conversation_id:'c',name:'data.csv',mime:'text/csv',size:40,created_at:'2026-10-27T10:00:00Z'},
 {id:'att_chart',conversation_id:'c',name:'chart.pdf',mime:'application/pdf',size:4096,created_at:'2026-10-27T10:00:00Z'},
] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as any)||'home'});
 const openSettings=(next:SettingsTab,view:'home'|'page'='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/></aside></div></div>;
}
function Files(){
 useEffect(()=>{const mark=()=>{(window as any).__computerSettings=((window as any).__computerSettings||0)+1};window.addEventListener(OPEN_COMPUTER_SETTINGS_EVENT,mark);return()=>window.removeEventListener(OPEN_COMPUTER_SETTINGS_EVENT,mark)},[]);
 return <div className="workspace" style={{padding:16}}><AttachmentList attachments={files}/><FilePreviewHost/></div>;
}
const Page=query.get('mode')==='files'?Files:Settings;
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Page/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const requested = [], unexpected = [], writes = [];
  const json = (route, status, body) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(body)});
  const stub = path => {
    if (path === "/api/auth/session") return {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}};
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}};
    if (path === "/api/system/update") return {current: "v0.1.0", latest: "v0.1.0", update_available: false, notes_url: "https://github.com/example/tofibot/releases/tag/v0.1.0", checked_at: "2026-10-27T09:00:00Z", auto_update: "off"};
    if (path === "/api/bots") return {bots: [{id: "bot_research", name: "Research Bot", archived: false}]};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/computers/firecracker/info") return {state: "ready", workspace_root: "/workspace", browser: "Chromium"};
    if (path === "/api/computers") return {computers: []};
    if (path === "/api/computer/resources") return {current: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, desired: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, pending: false, state: "ready", host: {cpus: 8, memory_total_mib: 16384, memory_available_mib: 8192, disk_available_gib: 100}, limits: {max_vcpus: 4, max_memory_mib: 8192, max_disk_gib: 64}};
    if (path === "/api/computer/contents") return {environment: ["WEB_API_TOKEN", "DEPLOY_REGION"], ssh_keys: ["id_work"], pending_file_copies: 2};
    if (path === "/api/computer/credentials") return {credentials: [{id: "cred_1", name: "WEB_API_TOKEN", kind: "env", target: "WEB_API_TOKEN", created_at: "2026-10-20T10:00:00Z"}]};
    if (path === "/api/computer/ssh-keys") return {keys: []};
    if (path === "/api/extensions/mcp") return {servers: []};
    if (path === "/api/command-waits") return {count: 0, bots: []};
    if (path === "/api/settings-changes") return {changes: []};
    if (path === "/api/admin/computer/state") return {state: "none"};
    if (path === "/api/storage") return {version: 1, measured_at: "2026-10-27T10:00:00Z", server_bytes: 1048576, files_bytes: 524288, database_bytes: 524288, physical_files_bytes: 524288, pending_copy_count: 0, classes: [], breakdown_measured_at: null, limit_bytes: null, remaining_bytes: null, over_limit: false, computer: null, by_bot: [], by_conversation: []};
    if (path === "/api/usage/runs") return {days: 7, since: "2026-10-20T00:00:00Z", runs: 0, by_status: {}, failures: []};
    if (path === "/api/usage") return {contexts: []};
    if (path === "/api/model-settings") return {model: "gpt-6-luna", reasoning_effort: "medium"};
    return undefined;
  };
  async function open(params = {}, {width = 1440, height = 900, theme = "light", reduced = false} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: reduced ? "reduce" : "no-preference", colorScheme: theme, acceptDownloads: true});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    const downloads = [];
    page.on("download", download => downloads.push(download.suggestedFilename()));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      requested.push(`${method} ${url.pathname}`);
      if (method !== "GET") { writes.push(`${method} ${url.pathname}`); await json(route, 200, {}); return; }
      if (url.pathname === "/api/attachments/att_detached") { await json(route, 409, {error: {code: "file_on_detached_computer", message: "This file is on the detached computer. Attach it to recover."}}); return; }
      if (url.pathname === "/api/attachments/att_missing") { await json(route, 404, {error: {code: "file_missing_on_computer", message: "This file is no longer on the computer and was not copied to the server."}}); return; }
      if (url.pathname === "/api/attachments/att_ok") { await route.fulfill({status: 200, headers: {"Content-Type": "text/csv", "Content-Disposition": 'attachment; filename="data.csv"'}, body: "a,b\n1,2\n"}); return; }
      if (url.pathname === "/api/attachments/att_chart") {
        // The viewer reads one byte of a PDF to learn why it could not open; this one is on a detached computer.
        await json(route, 409, {error: {code: "file_on_detached_computer", message: "detached"}}); return;
      }
      const body = stub(url.pathname);
      if (body === undefined) { unexpected.push(`${method} ${url.pathname}`); await json(route, 404, {error: {message: "not stubbed", code: "not_found"}}); return; }
      await json(route, 200, body);
    });
    const query = new URLSearchParams({lang: "en", ...params});
    await page.goto(`${origin}/${basename(fixture)}/index.html?${query}`);
    await page.locator(params.mode === "files" ? ".delivered-attachment" : ".settings-shell").first().waitFor({timeout: 20000});
    return {page, downloads, close: () => context.close()};
  }
  const railTabs = page => page.$$eval(".settings-tabs .settings-nav-group button", buttons => buttons.map(button => button.dataset.tab));
  const goTab = async (page, id) => { await page.locator(`.settings-tabs [data-tab="${id}"]`).click(); await page.waitForFunction(tab => document.querySelector(`.settings-tabs [aria-current="page"]`)?.dataset.tab === tab, id); };
  const apiOfComputer = () => requested.filter(line => /\/api\/computer(s)?(\/|$)/.test(line.split(" ")[1]));
  const en = await catalog("en", "computer");

  // ---- 1. No computer: no Keys page, no configuration list, no wipe list, and not one /api/computer request.
  {
    requested.length = 0;
    const {page, close} = await open({computer: "none", tab: "general", view: "page"});
    assert.ok(!(await railTabs(page)).includes("keys"), "Keys is hidden without a computer");
    await goTab(page, "computer");
    await page.getByText(en.none.title).waitFor();
    assert.equal(await page.getByText(en.contents.title).count(), 0, "no 'What is on this computer' without a computer");
    await goTab(page, "advanced");
    await page.getByText("PURGE").first().waitFor();
    assert.equal(await page.locator(".computer-loss").count(), 0, "no wipe list without a computer");
    await page.waitForTimeout(700);
    assert.deepEqual(apiOfComputer(), [], `no /api/computer request without a computer: ${apiOfComputer().join(", ")}`);
    if (shots) { await goTab(page, "computer"); await page.waitForTimeout(500); await page.screenshot({path: join(shots, "no-computer-1440.png")}); }
    await close();
    // A saved link to the Keys page lands on Computer, still without a request.
    const deep = await open({computer: "none", tab: "keys", view: "page"});
    await deep.page.waitForFunction(() => document.querySelector(`.settings-tabs [aria-current="page"]`)?.dataset.tab === "computer");
    await deep.page.waitForTimeout(500);
    assert.deepEqual(apiOfComputer(), [], "a saved Keys link makes no /api/computer request without a computer");
    await deep.close();
    const phone = await open({computer: "none", tab: "computer", view: "page"}, {width: 390, height: 844});
    await phone.page.getByText(en.none.title).waitFor();
    assert.deepEqual(apiOfComputer(), [], "phone width: no /api/computer request without a computer");
    if (shots) await phone.page.screenshot({path: join(shots, "no-computer-390.png")});
    assert.equal(await phone.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll at 390px");
    await phone.close();
    console.log("PASS no computer: no Keys page, no configuration list, no wipe list, zero /api/computer requests (desktop, saved link, 390px)");
  }

  // ---- 2. With a computer: the four rows, the Keys link, the wipe list, and honest wording in every language.
  {
    requested.length = 0;
    const {page, close} = await open({computer: "kvm", tab: "computer", view: "page"});
    assert.ok((await railTabs(page)).includes("keys"), "Keys is shown with a computer");
    await page.getByText(en.contents.title).waitFor();
    const section = page.locator(".computer-contents");
    for (const label of [en.contents.browser.name, en.contents.tools.name, en.contents.files.name, en.contents.config.name]) await section.getByText(label, {exact: true}).waitFor();
    const text = (await section.innerText()).replace(/\s+/g, " ");
    assert.match(text, /lost/i, "the section says these are lost when the computer is wiped");
    assert.match(text, /signs the Bot out of every one/, "browser sign-ins: what a wipe does");
    assert.match(text, /installs them again when it needs them/, "installed tools: what a wipe does");
    assert.match(text, /2 files are still being copied to the server/, "work files: pending copies");
    assert.match(text, /3 saved/, "environment and keys: count");
    assert.equal(await section.locator(".computer-kept").count(), 3, "three rows say they stay on the computer");
    assert.ok(requested.includes("GET /api/computer/contents"), "the list is read from /api/computer/contents");
    await section.getByRole("button", {name: en.contents.config.manage}).click();
    await page.waitForFunction(() => document.querySelector(`.settings-tabs [aria-current="page"]`)?.dataset.tab === "keys");
    await page.locator(".computer-credentials").waitFor();
    // The wipe list sits above the typed confirmation.
    await goTab(page, "advanced");
    const loss = page.locator(".computer-loss");
    await loss.getByText("WEB_API_TOKEN, DEPLOY_REGION", {exact: false}).waitFor();
    const lossText = (await loss.innerText()).replace(/\s+/g, " ");
    assert.match(lossText, /Browser sign-ins/);
    assert.match(lossText, /Installed tools/);
    assert.match(lossText, /Work files/);
    assert.match(lossText, /2 environment variables: WEB_API_TOKEN, DEPLOY_REGION/);
    assert.match(lossText, /1 SSH key: id_work/);
    assert.match(lossText, /2 files exist only on the computer and have not been copied to the server/);
    // Entry animation uses the tokens (--m-base = 220 ms); reduced motion has none.
    const duration = await loss.evaluate(node => getComputedStyle(node).animationDuration);
    assert.equal(duration, "0.22s", "the wipe list rises in over --m-base");
    if (shots) { await loss.scrollIntoViewIfNeeded(); await page.waitForTimeout(500); await page.screenshot({path: join(shots, "computer-wipe-list-1440.png")}); await goTab(page, "computer"); await page.waitForTimeout(500); await page.screenshot({path: join(shots, "computer-contents-1440.png"), fullPage: true}); }
    await close();
    const reduced = await open({computer: "kvm", tab: "advanced", view: "page"}, {reduced: true});
    await reduced.page.locator(".computer-loss").waitFor();
    assert.equal(await reduced.page.locator(".computer-loss").evaluate(node => getComputedStyle(node).animationName), "none", "reduced motion: no rise-in");
    await reduced.close();
    const phone = await open({computer: "kvm", tab: "computer", view: "page"}, {width: 390, height: 844});
    await phone.page.locator(".computer-contents").waitFor();
    await phone.page.waitForTimeout(400);
    assert.equal(await phone.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll at 390px");
    if (shots) { await phone.page.screenshot({path: join(shots, "computer-contents-390.png"), fullPage: true}); }
    await phone.close();
    const phoneLoss = await open({computer: "kvm", tab: "advanced", view: "page"}, {width: 390, height: 844});
    await phoneLoss.page.locator(".computer-loss").waitFor();
    await phoneLoss.page.locator(".computer-loss").scrollIntoViewIfNeeded();
    await phoneLoss.page.waitForTimeout(500);
    if (shots) await phoneLoss.page.screenshot({path: join(shots, "computer-wipe-list-390.png")});
    await phoneLoss.close();
    for (const lang of LANGS.slice(1)) {
      const words = await catalog(lang, "computer");
      const localized = await open({computer: "kvm", tab: "computer", view: "page", lang});
      await localized.page.getByText(words.contents.title, {exact: true}).waitFor();
      const body = (await localized.page.locator(".computer-contents").innerText()).replace(/\s+/g, " ");
      assert.ok(body.includes(words.contents.browser.name) && body.includes(words.contents.config.what), `${lang}: rows are translated`);
      assert.notEqual(words.contents.title, en.contents.title, `${lang}: title is not an English copy`);
      assert.notEqual(words.contents.browser.what, en.contents.browser.what, `${lang}: browser sign-ins sentence is not an English copy`);
      await localized.close();
    }
    console.log("PASS computer attached: four rows with honest wording, Manage opens Keys, wipe list with names and counts, token animation, reduced motion, 390px, 7 languages");
  }

  // ---- 3. A file only on a detached computer says so (card, viewer, 404 variant), and a good file still downloads.
  {
    const chat = await catalog("en", "chat");
    const detachedText = chat.attachment.problem.file_on_detached_computer;
    const missingText = chat.attachment.problem.file_missing_on_computer;
    requested.length = 0;
    const {page, downloads, close} = await open({mode: "files"});
    const card = name => page.locator(".delivered-attachment.is-file", {hasText: name});
    await card("quarterly-report.csv").getByRole("link", {name: /Download quarterly-report\.csv/}).click();
    await card("quarterly-report.csv").locator(".attachment-notice").getByText(detachedText).waitFor();
    assert.equal(downloads.length, 0, "no error page was saved as the download");
    await card("notes.txt").getByRole("link", {name: /Download notes\.txt/}).click();
    await card("notes.txt").locator(".attachment-notice").getByText(missingText).waitFor();
    assert.equal(await card("notes.txt").getByRole("button", {name: chat.attachment.problem.open_computer}).count(), 0, "a file that is gone has no attach link");
    assert.equal(await card("quarterly-report.csv").locator('.attachment-notice[role="alert"]').count(), 1, "the notice is announced")
    // Entry uses the tokens; reduced motion is covered by the catalog.
    const wrap = card("quarterly-report.csv").locator(".attachment-notice-wrap");
    assert.equal(await wrap.evaluate(node => getComputedStyle(node).animationDuration), "0.28s", "the notice grows in over --m-panel-in");
    await card("data.csv").getByRole("link", {name: /Download data\.csv/}).click();
    for (let i = 0; i < 40 && downloads.length === 0; i++) await page.waitForTimeout(50);
    assert.deepEqual(downloads, ["data.csv"], "a readable file downloads as before");
    assert.equal(await card("data.csv").locator(".attachment-notice").count(), 0, "a readable file shows no notice");
    // The viewer: a text file on a detached computer, then its link out to the computer settings.
    await card("quarterly-report.csv").getByRole("button", {name: /Preview quarterly-report\.csv/}).click();
    const viewer = page.locator("dialog.file-preview");
    await viewer.locator(".fp-body .attachment-notice").getByText(detachedText).waitFor();
    assert.equal(await viewer.getByText(chat.attachment.load_failed).count(), 0, "not the generic load failure");
    if (shots) { await page.waitForTimeout(500); await page.screenshot({path: join(shots, "file-viewer-detached-1440.png")}); }
    await viewer.getByRole("button", {name: chat.attachment.problem.open_computer}).click();
    await page.waitForFunction(() => !document.querySelector("dialog.file-preview")?.open);
    assert.equal(await page.evaluate(() => window.__computerSettings), 1, "the link asks the app to open the computer settings once");
    // A PDF is drawn by the browser, which hides the status: the viewer reads one byte first.
    await card("chart.pdf").getByRole("button", {name: /Preview chart\.pdf/}).click();
    await viewer.locator(".fp-body .attachment-notice").getByText(detachedText).waitFor();
    assert.equal(await viewer.locator("iframe").count(), 0, "no PDF frame is shown for a file that cannot be read");
    if (shots) await page.screenshot({path: join(shots, "file-viewer-pdf-detached-1440.png")});
    await close();
    const phone = await open({mode: "files"}, {width: 390, height: 844});
    await phone.page.locator(".delivered-attachment.is-file", {hasText: "quarterly-report.csv"}).getByRole("link", {name: /Download/}).click();
    await phone.page.locator(".attachment-notice").first().waitFor();
    await phone.page.waitForTimeout(500);
    assert.equal(await phone.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no horizontal scroll at 390px");
    if (shots) await phone.page.screenshot({path: join(shots, "file-detached-390.png")});
    await phone.close();
    for (const lang of LANGS.slice(1)) {
      const words = await catalog(lang, "chat");
      assert.ok(words.attachment.problem.file_on_detached_computer && words.attachment.problem.file_on_detached_computer !== detachedText, `${lang}: detached-file sentence is translated`);
      assert.ok(words.attachment.problem.file_missing_on_computer && words.attachment.problem.file_missing_on_computer !== missingText, `${lang}: missing-file sentence is translated`);
      const localized = await open({mode: "files", lang});
      await localized.page.locator(".delivered-attachment.is-file", {hasText: "quarterly-report.csv"}).locator("a.attachment-download").click();
      await localized.page.locator(".attachment-notice").getByText(words.attachment.problem.file_on_detached_computer).waitFor();
      await localized.close();
    }
    console.log("PASS files: detached and missing say so in the card and the viewer (text and PDF), the link opens the computer settings, good files download, 390px, 7 languages");
  }

  assert.deepEqual(unexpected, [], `unexpected requests or page errors: ${unexpected.join("; ")}`);
  assert.deepEqual(writes, [], `no writes expected: ${writes.join("; ")}`);
  console.log("PASS no unstubbed /api request, no write, no page error");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
