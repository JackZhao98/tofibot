// Settings > Environments (docs/design/environments.md E1, E2, E7-E9): one list of environments, controls only from `actions`,
// the E2B add dialog, Use for Bots and Disconnect behind a confirmation, the desktop-only "This Mac" row, and /settings/devices
// landing on this page. Mounts the real SettingsShell + SettingsPages in a Vite fixture with every /api/* call stubbed
// (synthetic data only). Needs PLAYWRIGHT_MODULE.
//   ENVIRONMENTS_SHOTS=<dir>  also saves screenshots.
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.ENVIRONMENTS_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

const fixture = await mkdtemp(join(ui, ".environments-"));
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
import {subscribeSettingsDeepLinks} from '../src/settings/deepLinks';
import {setComputerCapability} from '../src/computerCapability';
const query=new URLSearchParams(location.search);
setComputerCapability((query.get('computer')==='detached'?{backend:'none',available:false,host_backend:'kvm',capabilities:[],personal_computers:true}:{backend:'kvm',available:true,capabilities:['shell','files','browser','desktop','terminal','resources'],personal_computers:true}) as any);
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as any)||'page'});
 const openSettings=(next:SettingsTab,view:'home'|'page'='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={{preference:'light',choose:()=>{}}} extensionRefresh={0} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}} openTab={openSettings}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Settings/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const errors = [];
  const json = (route, status, body) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(body)});
  const vm = (extra = {}) => ({id: "firecracker", kind: "vm", name: "", state: "ready", error_code: "", bot_computer: true, access: "all_bots", actions: ["configure"], ...extra});
  const e2b = (extra = {}) => ({id: "e2b", kind: "e2b", name: "", state: "asleep", error_code: "", bot_computer: false, access: "none", actions: ["use", "remove"], ...extra});
  const mac = (extra = {}) => ({id: "mac_ab12", kind: "mac", name: "Jack's MacBook Pro", state: "online", error_code: "", platform: "darwin", last_seen: "2026-11-05T08:01:02Z", bot_computer: false, access: "allowed_on_mac", actions: ["disconnect"], ...extra});
  const body = (environments, add = {e2b: true, mac: true, vm_command: ""}, e2bState = null, policy = null) => ({environments, add, e2b: e2bState, ...(policy ? {policy} : {})});

  async function open(params = {}, {width = 1280, height = 800, theme = "light", desktop = false, state} = {}) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme});
    await context.addInitScript(({value, desktop}) => {
      try { localStorage.setItem("tofi:appearance", value); } catch {}
      if (desktop) {
        const off = () => () => {};
        window.__openedMacSettings = 0;
        window.tofiDesktop = {platform: "darwin", themePreference: value, onCommand: off, onWindowState: off, onFlushState: off, appearance: async () => {}, openDesktopSettings: async () => { window.__openedMacSettings++; }, readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {}, remoteInputFocus: () => {}, startAccessibilityDrag: () => {}, requestMicrophoneAccess: async () => false};
      }
    }, {value: theme, desktop});
    const page = await context.newPage();
    page.on("pageerror", error => errors.push(`pageerror: ${error.message}`));
    const calls = [];
    const scenario = {environments: state?.environments ?? [vm(), e2b(), mac()], add: state?.add, e2bState: state?.e2bState ?? null, put: state?.put, policy: state?.policy, pairing: {status: "pending"}};
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method(), path = url.pathname;
      if (url.origin !== origin) { await route.abort(); return; }
      if (!path.startsWith("/api/")) { await route.continue(); return; }
      const text = request.postData() ?? "";
      if (method !== "GET") calls.push(`${method} ${path}${text ? " " + text : ""}`);
      if (path === "/api/auth/session") return json(route, 200, {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: false, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "owner"}});
      if (path === "/api/server-info") return json(route, 200, {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}});
      if (path === "/api/bots") return json(route, 200, {bots: [{id: "bot_research", name: "Research Bot", archived: false}]});
      if (path === "/api/preferences") return json(route, 200, {timezone: "America/Los_Angeles", timezone_configured: true});
      if (path === "/api/environments" && method === "GET") return json(route, 200, body(scenario.environments, scenario.add, scenario.e2bState, scenario.policy));
      if (path === "/api/environments/e2b" && method === "PUT") {
        const sent = JSON.parse(text);
        if (scenario.put === "invalid") return json(route, 400, {error: {code: "invalid_key", message: "E2B rejected the key"}});
        scenario.environments = [vm(), e2b({state: "preparing"}), mac()];
        scenario.e2bState = {configured: true, key_hint: "e2b_…" + sent.api_key.slice(-4), phase: "preparing", error_code: "", error: ""};
        return json(route, 200, body(scenario.environments, {e2b: false, mac: true, vm_command: ""}, scenario.e2bState));
      }
      if (path === "/api/environments/e2b/use" || path === "/api/environments/e2b" && method === "DELETE") {
        scenario.environments = method === "DELETE" ? [vm(), mac()] : [vm({bot_computer: false, access: "none", actions: ["use", "configure"]}), e2b({state: "ready", bot_computer: true, access: "all_bots", actions: ["wake", "remove"]}), mac()];
        return json(route, 200, body(scenario.environments));
      }
      if (path === "/api/computers/pairings" && method === "POST") return json(route, 200, {pairing_id: "pr_1", code: "7Q4K9M2", expires_at: "2026-11-05T09:00:00Z", status: "pending"});
      if (path === "/api/computers/pairings/pr_1") return json(route, 200, scenario.pairing);
      if (path === "/api/computers/mac_ab12" && method === "DELETE") { scenario.environments = scenario.environments.filter(item => item.kind !== "mac"); return json(route, 200, {}); }
      if (path === "/api/computer/resources") return json(route, 200, {current: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, desired: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, pending: false, state: "ready", host: {cpus: 8, memory_total_mib: 16384, memory_available_mib: 8192, disk_available_gib: 200}, limits: {max_vcpus: 4, max_memory_mib: 8192, max_disk_gib: 100}});
      if (path === "/api/admin/computer/state") return json(route, 200, {attached: true, id: "vm-1", kind: "vm", status: "attached", backend_available: true});
      return json(route, 200, {});
    });
    const query = new URLSearchParams({lang: "en", ...params});
    await page.goto(`${origin}/${basename(fixture)}/index.html?${query}`);
    await page.locator(".settings-shell").first().waitFor({timeout: 20000});
    return {page, calls, scenario, close: () => context.close()};
  }
  const rows = page => page.locator(".environments-page .env-row");
  const row = (page, id) => page.locator(`.environments-page .env-row[data-environment="${id}"]`);
  const names = page => page.$$eval(".environments-page .env-row .env-name-text", items => items.map(item => item.textContent));
  const buttons = (locator) => locator.locator("button[data-action]").evaluateAll(list => list.map(item => item.dataset.action));

  // ---- 1. The list: VM, E2B and a Mac, in that order; badge words, access lines, the Bot computer tag, controls from `actions` only.
  {
    const {page, close} = await open({tab: "computer"});
    await rows(page).first().waitFor();
    assert.deepEqual(await names(page), ["This server", "E2B cloud computer", "Jack's MacBook Pro"]);
    assert.equal((await page.locator(".settings-page-title h2").textContent()).trim(), "Environments");
    assert.match((await row(page, "firecracker").innerText()).replace(/\s+/g, " "), /This server Bot computer Used by all Bots Ready/);
    assert.equal(await page.locator(".env-tag").count(), 1, "one environment carries the Bot computer tag");
    assert.match((await row(page, "e2b").innerText()).replace(/\s+/g, " "), /E2B cloud computer Not in use Asleep/);
    assert.match((await row(page, "mac_ab12").innerText()).replace(/\s+/g, " "), /Bots you allow on that Mac Online/);
    assert.deepEqual(await buttons(row(page, "firecracker")), ["configure"]);
    assert.deepEqual(await buttons(row(page, "e2b")), ["use"], "remove sits in the row menu");
    assert.deepEqual(await buttons(row(page, "mac_ab12")), [], "disconnect sits in the row menu");
    assert.equal(await page.locator(".resource-grid").count(), 0, "the size grid is behind Configure");
    assert.equal(await page.locator(".computer-contents, .device-pairing-code, .environment-summary").count(), 0, "no old cards");
    assert.equal(await page.locator(".env-this-mac").count(), 0, "no This Mac card outside the desktop app");
    if (shots) await page.screenshot({path: join(shots, "list-1280.png")});
    await close();
  }

  // ---- 2. Actions come only from the server: a row without `actions` has no control, and the page offers what `add` allows.
  {
    const {page, close} = await open({tab: "computer"}, {state: {environments: [vm({actions: []}), e2b({state: "failed", error_code: "template_build_failed", actions: ["retry"]})], add: {e2b: false, mac: false, vm_command: ""}, e2bState: {configured: true, key_hint: "e2b_…a1b2", phase: "failed", error_code: "template_build_failed", error: "image pull denied"}}});
    await rows(page).first().waitFor();
    assert.deepEqual(await buttons(row(page, "firecracker")), []);
    assert.equal(await row(page, "firecracker").locator(".env-more").count(), 0);
    assert.deepEqual(await buttons(row(page, "e2b")), ["retry"]);
    assert.equal(await row(page, "e2b").locator('[data-error-code="template_build_failed"]').innerText(), "E2B couldn't build the computer image.", "a named reason, one line");
    assert.match(await row(page, "e2b").locator(".env-reason-detail").innerText(), /image pull denied/);
    assert.equal(await page.locator(".env-add").count(), 0, "nothing to add: no Add environment button");
    await close();
  }

  // ---- 3. Add environment: the menu follows `add`; E2B saves a key (invalid_key inline, then success), Use for Bots and Remove confirm first.
  {
    const {page, calls, scenario, close} = await open({tab: "computer"}, {state: {environments: [vm(), mac()], put: "invalid"}});
    await rows(page).first().waitFor();
    await page.locator(".env-add").click();
    assert.deepEqual(await page.locator(".env-add-menu button").allInnerTexts(), ["E2B cloud computer", "Your Mac"], "no host command item without add.vm_command");
    await page.locator('.env-add-menu [data-add="e2b"]').click();
    const dialog = page.getByRole("dialog", {name: "Add E2B cloud computer"});
    await dialog.waitFor();
    assert.equal(await dialog.locator("input[type=password]").evaluate(node => document.activeElement === node), true, "focus lands on the key field");
    const link = dialog.getByRole("link", {name: "Get a key"});
    assert.equal(await link.getAttribute("href"), "https://e2b.dev/dashboard?tab=keys");
    assert.equal(await link.getAttribute("target"), "_blank");
    assert.match(await dialog.innerText(), /Billed by E2B per second while running; pauses when idle\./);
    assert.equal(await dialog.getByRole("button", {name: "Save"}).isDisabled(), true, "Save waits for a key");
    await dialog.locator("input[type=password]").fill("e2b_badkey");
    await dialog.getByRole("button", {name: "Save"}).click();
    await dialog.getByRole("alert").getByText("E2B didn't accept the API key.").waitFor();
    assert.deepEqual(calls.filter(call => call.includes("/api/environments/e2b")), ['PUT /api/environments/e2b {"api_key":"e2b_badkey"}']);
    scenario.put = "ok";
    await dialog.getByRole("button", {name: "Use an existing template"}).click();
    await dialog.getByLabel("Template ID").fill("tpl_cli_1");
    await dialog.locator("input[type=password]").fill("e2b_goodkey9");
    await dialog.getByRole("button", {name: "Save"}).click();
    await dialog.waitFor({state: "detached"});
    assert.equal(calls.at(-1), 'PUT /api/environments/e2b {"api_key":"e2b_goodkey9","template_id":"tpl_cli_1"}');
    await row(page, "e2b").waitFor();
    assert.match((await row(page, "e2b").innerText()).replace(/\s+/g, " "), /Preparing/, "the E2B row shows preparing");
    assert.equal(await page.locator(".env-add").count(), 1);
    await page.locator(".env-add").click();
    assert.deepEqual(await page.locator(".env-add-menu button").allInnerTexts(), ["Your Mac"], "E2B is set up: it leaves the menu");
    await page.keyboard.press("Escape");
    await close();
  }

  // ---- 4. Use for Bots confirms (running work finishes first) and only then POSTs; Remove confirms and DELETEs.
  {
    const {page, calls, close} = await open({tab: "computer"});
    await rows(page).first().waitFor();
    await row(page, "e2b").getByRole("button", {name: "Use for Bots"}).click();
    const confirm = page.getByRole("alertdialog");
    await confirm.waitFor();
    assert.match(await confirm.innerText(), /Running work finishes first/);
    assert.equal(calls.length, 0, "nothing is sent before the confirmation");
    await confirm.getByRole("button", {name: "Cancel"}).click();
    await confirm.waitFor({state: "detached"});
    assert.equal(calls.length, 0, "Cancel sends nothing");
    await row(page, "e2b").getByRole("button", {name: "Use for Bots"}).click();
    await page.getByRole("alertdialog").getByRole("button", {name: "Use for Bots"}).click();
    await page.getByRole("alertdialog").waitFor({state: "detached"});
    assert.deepEqual(calls, ["POST /api/environments/e2b/use"]);
    await page.locator('.env-row[data-environment="e2b"] .env-tag').waitFor();
    assert.equal(await page.locator(".env-tag").count(), 1, "the tag moved to E2B");
    assert.match((await row(page, "firecracker").innerText()).replace(/\s+/g, " "), /Not in use/);
    // Remove, from the row menu.
    await row(page, "e2b").getByRole("button", {name: "More actions for E2B cloud computer"}).click();
    await page.getByRole("menuitem", {name: "Remove"}).or(page.locator(".env-menu button", {hasText: "Remove"})).first().click();
    await page.getByRole("alertdialog").getByRole("button", {name: "Remove"}).click();
    await page.getByRole("alertdialog").waitFor({state: "detached"});
    assert.equal(calls.at(-1), "DELETE /api/environments/e2b");
    await row(page, "e2b").waitFor({state: "detached"});
    await close();
  }

  // ---- 5. A Mac: Disconnect confirms, then DELETE /api/computers/{id}; the Mac dialog shows the code and closes when it is paired.
  {
    const {page, calls, scenario, close} = await open({tab: "computer"});
    await rows(page).first().waitFor();
    await row(page, "mac_ab12").getByRole("button", {name: "More actions for Jack's MacBook Pro"}).click();
    await page.locator(".env-menu button", {hasText: "Disconnect"}).click();
    const confirm = page.getByRole("alertdialog");
    await confirm.waitFor();
    assert.equal(calls.length, 0);
    await confirm.getByRole("button", {name: "Disconnect"}).click();
    await confirm.waitFor({state: "detached"});
    assert.deepEqual(calls, ["DELETE /api/computers/mac_ab12"]);
    await row(page, "mac_ab12").waitFor({state: "detached"});
    await page.locator(".env-add").click();
    await page.locator('.env-add-menu [data-add="mac"]').click();
    const dialog = page.getByRole("dialog", {name: "Connect your Mac"});
    await dialog.getByText("7Q4K9M2").waitFor();
    assert.match(await dialog.innerText(), /Open the Tofi app on your Mac\./);
    scenario.pairing = {status: "paired", device_name: "Studio iMac"};
    await dialog.waitFor({state: "detached", timeout: 10000});
    await close();
  }

  // ---- 6. Configure opens the size controls in a dialog; the host command item exists only with add.vm_command.
  {
    const {page, close} = await open({tab: "computer"}, {state: {environments: [], add: {e2b: true, mac: true, vm_command: "sudo tofi computer add kvm"}}});
    await page.locator(".env-add").waitFor();
    await page.locator(".env-add").click();
    assert.deepEqual(await page.locator(".env-add-menu button").allInnerTexts(), ["E2B cloud computer", "Your Mac", "Bot computer on this server"]);
    await page.locator('.env-add-menu [data-add="vm"]').click();
    const dialog = page.getByRole("dialog", {name: "Add a Bot computer on this server"});
    assert.equal((await dialog.locator("[data-computer-command]").innerText()).trim(), "sudo tofi computer add kvm");
    await dialog.getByRole("button", {name: "Close"}).click();
    await close();
    const configured = await open({tab: "computer"});
    await rows(configured.page).first().waitFor();
    await row(configured.page, "firecracker").getByRole("button", {name: "Configure"}).click();
    await configured.page.locator(".resource-grid").waitFor();
    await configured.close();
  }

  // ---- 7. Desktop app: a This Mac row with Open Mac settings; the web has none.
  {
    const {page, close} = await open({tab: "computer"}, {desktop: true});
    await rows(page).first().waitFor();
    assert.deepEqual(await names(page), ["This server", "E2B cloud computer", "Jack's MacBook Pro"], "This Mac is not an environment row");
    const card = page.locator(".env-this-mac");
    assert.equal(await card.locator(".status-badge").count(), 0, "no status badge on the This Mac card");
    assert.equal(await page.locator(".env-list .env-this-mac, .env-list [data-environment='this-mac']").count(), 0);
    assert.equal(await card.evaluate(node => node.compareDocumentPosition(document.querySelector(".env-list")) & Node.DOCUMENT_POSITION_FOLLOWING) > 0, true, "the card sits above the list");
    await card.getByRole("button", {name: "Open Mac settings"}).click();
    assert.equal(await page.evaluate(() => window.__openedMacSettings), 1);
    await close();
  }

  // ---- 8. Old links: /settings/devices lands here, and the rail has no Other devices item.
  {
    const {page, close} = await open({tab: "devices"});
    await page.waitForFunction(() => document.querySelector('.settings-tabs [aria-current="page"]')?.dataset.tab === "computer");
    await rows(page).first().waitFor();
    assert.equal(await page.locator('.settings-tabs [data-tab="devices"]').count(), 0);
    await close();
  }

  // ---- 9. Organization policy (user groups): provided shows whose Bot computer it is and offers no key or switch; none says so
  //         and leaves only the Mac side, with no way to attach or add a computer. A server without a policy changes nothing.
  {
    // Provided, VM: the server drops use/remove and the E2B add; the row carries the organization mark.
    const provided = await open({tab: "computer"}, {state: {environments: [vm({actions: ["configure"]}), mac()], add: {e2b: false, mac: true, vm_command: ""}, policy: {mode: "provided", source: "organization", via: "vm"}}});
    await rows(provided.page).first().waitFor();
    assert.match((await row(provided.page, "firecracker").innerText()).replace(/\s+/g, " "), /This server Bot computer Provided by your organization Used by all Bots Ready/);
    assert.equal(await row(provided.page, "firecracker").locator('[data-tag="organization"]').count(), 1);
    assert.deepEqual(await buttons(row(provided.page, "firecracker")), ["configure"], "only what the server allows: no Use for Bots, no Remove");
    assert.equal(await provided.page.locator(".env-add").count(), 1);
    await provided.page.locator(".env-add").click();
    assert.deepEqual(await provided.page.locator(".env-add-menu button").allInnerTexts(), ["Your Mac"], "no E2B to add while the organization provides the computer");
    await provided.page.keyboard.press("Escape");
    assert.equal(await provided.page.locator('[data-section="attach"]').count(), 0);
    if (shots) await provided.page.screenshot({path: join(shots, "policy-provided-1280.png")});
    await provided.close();

    // Provided, E2B, not attached yet: the page still says whose Bot computer it will be, with no control.
    const waiting = await open({tab: "computer"}, {state: {environments: [], add: {e2b: false, mac: true, vm_command: ""}, policy: {mode: "provided", source: "organization", via: "e2b"}}});
    await waiting.page.locator('.env-row[data-environment="organization"]').waitFor();
    assert.match((await waiting.page.locator('.env-row[data-environment="organization"]').innerText()).replace(/\s+/g, " "), /E2B cloud computer Bot computer Provided by your organization Your organization provides this Bot computer/);
    assert.equal(await waiting.page.locator('.env-row[data-environment="organization"] button').count(), 0, "no controls on the organization's computer");
    assert.equal(await waiting.page.locator(".environments-page .computer-options, .environments-page [data-computer-command]").count(), 0, "no 'how to add' guide");
    await waiting.close();

    // None: a banner, only the Mac, nothing to attach, the host command is not shown even to an administrator of this server.
    const none = await open({tab: "computer", computer: "detached"}, {state: {environments: [mac()], add: {e2b: false, mac: true, vm_command: ""}, policy: {mode: "none", source: "none"}}});
    await rows(none.page).first().waitFor();
    assert.match(await none.page.locator(".settings-banner").innerText(), /Your administrator has not assigned a Bot computer/);
    assert.deepEqual(await names(none.page), ["Jack's MacBook Pro"]);
    assert.equal(await none.page.locator('[data-section="attach"]').count(), 0, "no way to attach a computer under 'none'");
    assert.ok(!/sudo tofi computer add kvm/.test(await none.page.locator(".environments-page").innerText()), "no host command under 'none'");
    if (shots) await none.page.screenshot({path: join(shots, "policy-none-1280.png")});
    await none.close();

    // Control: the same detached computer under 'host' (today's behavior) still offers Attach.
    const host = await open({tab: "computer", computer: "detached"}, {state: {environments: [mac()], add: {e2b: true, mac: true, vm_command: ""}, policy: {mode: "host", source: "own"}}});
    await rows(host.page).first().waitFor();
    await host.page.locator('[data-section="attach"]').waitFor();
    assert.equal(await host.page.locator(".settings-banner").count(), 0, "no policy banner under 'host'");
    await host.close();
    console.log("PASS policy: provided names the organization with no key or switch, none leaves only the Mac side, host is unchanged");
  }

  assert.deepEqual(errors, [], `page errors: ${errors.join("; ")}`);
  console.log("PASS environments: list, actions from the server, E2B add (invalid key, template, success), confirmations, Mac pairing and disconnect, configure, desktop row, /settings/devices");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
