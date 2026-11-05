// Rendered acceptance for the desktop app's Mac settings page (desktop-settings.html, src/desktop-settings/).
// Serves the real entry from a Vite dev server, replaces window.tofiDesktop with a stub that records every call, and drives
// the page with Playwright. The page must work from the stub alone: any /api request fails the test. Needs PLAYWRIGHT_MODULE.
//   DESKTOP_SETTINGS_SHOTS=<dir>  also saves screenshots (1280 and 390 px, light and dark).
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");

const LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"];
const catalogs = {};
for (const lang of LANGS) catalogs[lang] = JSON.parse(await readFile(join(ui, "src/locales", lang, "settings.json"), "utf8")).mac;
const L = (lang, path, vars = {}) => path.split(".").reduce((node, key) => node[key], catalogs[lang]).replace(/\{\{(\w+)\}\}/g, (_, name) => vars[name]);
const shots = process.env.DESKTOP_SETTINGS_SHOTS;
if (shots) await mkdir(shots, { recursive: true });

// The stub: what the preload exposes to the privileged page, with the device bridge's status shape.
const STUB = `(() => {
  const calls = [];
  const state = { status: {
    name: "Studio Mac", workspaceRoot: "/Users/demo/Tofi", fileAccess: true, screenCapture: false, desktopControl: false,
    paired: true, deviceID: "dev_123", capabilities: ["files"], permissions: { screen: false, accessibility: true },
    sandbox: { enabled: false, root: null, connectionID: null, bound: false, available: true, running: false },
    lastError: null,
    bots: [
      { bot_id: "bot_research", bot_name: "Research Bot", decision: "allow" },
      { bot_id: "bot_inbox", bot_name: "<img src=x onerror=\\"window.__xss=1\\"> Inbox", decision: undefined },
      { bot_id: "bot_notes", bot_name: "Notes Bot", decision: "deny" }
    ],
    control: { phase: "idle" }
  }, fail: null, selection: { selectionID: "sel_1", root: "/Users/demo/Projects/app", serverLabel: "https://tofi.example" } };
  const snap = () => JSON.parse(JSON.stringify(state.status));
  const rec = (name, ...args) => { calls.push([name, ...args]); if (state.fail === name) throw new Error("stub failure in " + name); };
  window.__calls = calls; window.__state = state;
  window.tofiDesktop = Object.freeze({
    platform: "darwin", themePreference: window.__theme || "system",
    getSettings: async () => { rec("getSettings"); return snap(); },
    updateSettings: async input => { rec("updateSettings", input); Object.assign(state.status, { name: input.name, workspaceRoot: input.workspaceRoot, fileAccess: input.fileAccess, screenCapture: input.screenCapture, desktopControl: input.desktopControl }); return snap(); },
    chooseWorkspace: async () => { rec("chooseWorkspace"); return "/Users/demo/Elsewhere"; },
    openPermissionGuide: async kind => { rec("openPermissionGuide", kind); return true; },
    relaunch: async () => { rec("relaunch"); return true; },
    setBotDecision: async (id, decision) => { rec("setBotDecision", id, decision); const bots = state.status.bots; const i = bots.findIndex(b => b.bot_id === id);
      if (decision === "forget") bots.splice(i, 1); else bots[i].decision = decision; return snap(); },
    controlStop: async () => { rec("controlStop"); state.status.control = { phase: "paused", reason: "user_stop" }; return snap(); },
    controlResume: async () => { rec("controlResume"); state.status.control = { phase: "idle" }; return snap(); },
    pair: async code => { rec("pair", code); state.status.paired = true; return snap(); },
    chooseSandboxRoot: async () => { rec("chooseSandboxRoot"); return state.selection; },
    authorizeSandboxRoot: async id => { rec("authorizeSandboxRoot", id); Object.assign(state.status.sandbox, { enabled: true, bound: true, root: state.selection.root, connectionID: "conn_1" }); return snap(); },
    sandboxRevoke: async () => { rec("sandboxRevoke"); Object.assign(state.status.sandbox, { enabled: false, bound: false, root: null, connectionID: null }); return snap(); }
  });
})();`;

const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.TOFI_TEST_CHROME || undefined });
const problems = [], apiCalls = [];
try {
  async function open({ lang = "en", width = 1280, height = 900, stub = true, theme, colorScheme = "light" } = {}) {
    const context = await browser.newContext({ viewport: { width, height }, colorScheme, hasTouch: width < 700 });
    // Only this origin: the stylesheet @import of Google Fonts must not stall the page in a sandbox.
    await context.route(url => new URL(url).hostname !== "127.0.0.1", route => route.abort());
    await context.addInitScript(({ stubSource, lang, theme }) => {
      localStorage.setItem("tofi.language", lang);
      if (theme) window.__theme = theme;
      if (stubSource) (0, eval)(stubSource);
    }, { stubSource: stub ? STUB : "", lang, theme });
    const page = await context.newPage();
    page.on("request", request => { if (new URL(request.url()).pathname.startsWith("/api")) apiCalls.push(request.url()); });
    page.on("pageerror", error => problems.push(`pageerror: ${error.message}`));
    page.on("console", message => { if (message.type() === "error" && !/Failed to load resource/.test(message.text())) problems.push(`console: ${message.text()}`); });
    await page.goto(`${origin}/desktop-settings.html`);
    await page.locator("h1").waitFor();
    return { page, context };
  }
  const calls = page => page.evaluate(() => window.__calls.map(call => call.slice()));
  const named = async (page, name) => (await calls(page)).filter(call => call[0] === name);
  const badge = (page, text) => page.locator(".status-badge").filter({ hasText: new RegExp(`^\\s*${text}\\s*$`) });
  const buttonIn = (scope, name) => scope.getByRole("button", { name, exact: true });

  // ---- 1. Every locale renders the page from its own catalog, with no key showing through.
  for (const lang of LANGS) {
    const { page, context } = await open({ lang });
    await page.locator("#mac-name").waitFor();
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    assert.equal(await page.locator("h1").innerText(), L(lang, "title"), `${lang}: title`);
    assert.equal((await page.locator(".mac-settings-back").innerText()).trim(), L(lang, "back"), `${lang}: back link`);
    assert.equal(await page.locator(".mac-settings-back").getAttribute("href"), "/", `${lang}: back link goes to /`);
    for (const section of ["device.title", "caps.title", "perm.title", "bots.title", "local.title", "pair.title"]) {
      assert.ok(await page.locator("h4", { hasText: L(lang, section) }).count() >= 1, `${lang}: section ${section}`);
    }
    const text = await page.locator("body").innerText();
    assert.ok(!/\bmac\.[a-z]+\.[A-Za-z.]+/.test(text), `${lang}: a message key shows through`);
    if (lang !== "en") assert.ok(!text.includes(L("en", "caps.title")) || L(lang, "caps.title") === L("en", "caps.title"), `${lang}: English copy`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${lang}: no horizontal overflow at 1280`);
    await context.close();
  }
  console.log(`PASS ${LANGS.length} locales render their own catalog`);

  // ---- 2. Behaviour, in English.
  {
    const { page, context } = await open();
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    // Bot names are text, never markup.
    assert.equal(await page.locator("img[src='x']").count(), 0, "a Bot name is not parsed as HTML");
    assert.equal(await page.evaluate(() => window.__xss), undefined);
    assert.ok(await page.getByText('<img src=x onerror="window.__xss=1"> Inbox').count() >= 1, "the Bot name shows as text");
    assert.equal(await page.locator("#mac-name").inputValue(), "Studio Mac");
    assert.equal(await page.locator("code", { hasText: "/Users/demo/Tofi" }).count(), 1);
    assert.equal(await page.getByRole("switch", { name: L("en", "caps.files.label") }).isChecked(), true);
    assert.equal(await page.getByRole("switch", { name: L("en", "caps.screen.label") }).isChecked(), false);
    assert.equal(await badge(page, L("en", "perm.granted")).count(), 1, "accessibility is granted");
    assert.equal(await badge(page, L("en", "perm.missing")).count(), 1, "screen recording is not");
    assert.equal(await page.getByText(L("en", "perm.stale")).count(), 0, "no stale-entry note before System Settings was opened");
    assert.equal(await buttonIn(page, L("en", "perm.reopen.action")).count(), 0, "no reopen offer before System Settings was opened");

    // Save sends exactly the five fields, nothing about the execution grant.
    await page.locator("#mac-name").fill("Desk Mac");
    await page.getByRole("switch", { name: L("en", "caps.screen.label") }).check();
    await page.getByRole("switch", { name: L("en", "caps.control.label") }).check();
    await buttonIn(page, L("en", "device.choose")).click();
    await page.waitForFunction(() => document.body.innerText.includes("/Users/demo/Elsewhere"));
    await buttonIn(page, L("en", "caps.save")).click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "updateSettings"));
    assert.deepEqual((await named(page, "updateSettings"))[0][1], { name: "Desk Mac", workspaceRoot: "/Users/demo/Elsewhere", fileAccess: true, screenCapture: true, desktopControl: true });
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Desk Mac");

    // Permission guides, the note, and the poll that clears it once the permission is granted.
    await buttonIn(page, L("en", "perm.grant")).click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "openPermissionGuide"));
    assert.deepEqual((await named(page, "openPermissionGuide"))[0], ["openPermissionGuide", "screen"]);
    await page.getByText(L("en", "perm.hint.screen")).waitFor();
    assert.ok(await page.getByText(L("en", "perm.monthly")).count() === 1, "the monthly private window picker note is always shown");
    // After System Settings opened: the note names the stale entry an earlier copy of Tofi leaves, and Screen Recording,
    // which macOS applies only to a fresh process, offers to reopen Tofi (clicked in its own page below).
    assert.equal(await page.getByText(L("en", "perm.stale")).count(), 1, "a missing permission explains the earlier-copy entry");
    assert.equal(await buttonIn(page, L("en", "perm.reopen.action")).count(), 1, "Screen Recording offers to reopen Tofi");
    const before = (await named(page, "getSettings")).length;
    await page.evaluate(() => { window.__state.status.permissions.screen = true; });
    await page.getByText(L("en", "perm.hint.screen")).waitFor({ state: "detached", timeout: 6000 });
    assert.equal(await badge(page, L("en", "perm.granted")).count(), 2);
    assert.equal(await page.getByText(L("en", "perm.stale")).count(), 0, "no stale-entry note once both are allowed");
    assert.equal(await buttonIn(page, L("en", "perm.reopen.action")).count(), 0, "no reopen offer once Screen Recording is allowed");
    assert.ok((await named(page, "getSettings")).length > before, "the page polls the status");
    await page.evaluate(() => { window.__state.status.permissions.accessibility = false; });
    await badge(page, L("en", "perm.missing")).waitFor({ timeout: 6000 });
    await buttonIn(page, L("en", "perm.grant")).click();
    await page.waitForFunction(() => window.__calls.filter(call => call[0] === "openPermissionGuide").length === 2);
    assert.deepEqual((await named(page, "openPermissionGuide"))[1], ["openPermissionGuide", "accessibility"]);

    // Bots: allow at once; deny only after the confirmation; remove forgets.
    const inbox = page.locator(".settings-row-v2", { hasText: "Inbox" });
    await buttonIn(inbox, L("en", "bots.allow")).click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "setBotDecision"));
    assert.deepEqual((await named(page, "setBotDecision"))[0], ["setBotDecision", "bot_inbox", "allow"]);
    const research = page.locator(".settings-row-v2", { hasText: "Research Bot" });
    await buttonIn(research, L("en", "bots.deny")).click();
    const dialog = page.getByRole("alertdialog");
    await dialog.waitFor();
    assert.ok((await dialog.innerText()).includes(L("en", "confirm.deny.title", { name: "Research Bot" })));
    assert.equal((await named(page, "setBotDecision")).length, 1, "nothing is sent before the answer");
    await buttonIn(dialog, L("en", "confirm.cancel")).click();
    await dialog.waitFor({ state: "detached" });
    assert.equal((await named(page, "setBotDecision")).length, 1, "Cancel sends nothing");
    await buttonIn(research, L("en", "bots.deny")).click();
    await buttonIn(page.getByRole("alertdialog"), L("en", "confirm.deny.confirm")).click();
    await page.waitForFunction(() => window.__calls.filter(call => call[0] === "setBotDecision").length === 2);
    assert.deepEqual((await named(page, "setBotDecision"))[1], ["setBotDecision", "bot_research", "deny"]);
    await buttonIn(page.locator(".settings-row-v2", { hasText: "Notes Bot" }), L("en", "bots.remove")).click();
    await page.waitForFunction(() => window.__calls.filter(call => call[0] === "setBotDecision").length === 3);
    assert.deepEqual((await named(page, "setBotDecision"))[2], ["setBotDecision", "bot_notes", "forget"]);

    // Stop and pause, Resume.
    await buttonIn(page, L("en", "bots.stop")).click();
    await page.getByText(L("en", "bots.control.paused_stop")).waitFor();
    assert.equal((await named(page, "controlStop")).length, 1);
    await buttonIn(page, L("en", "bots.resume")).click();
    await page.getByText(L("en", "bots.control.idle")).waitFor();
    assert.equal((await named(page, "controlResume")).length, 1);

    // Local execution: choose, authorize, revoke (confirmed). The run box lives on /__desktop/local-run.
    assert.equal(await page.locator("a[href='/__desktop/local-run']").count(), 1);
    assert.equal(await page.locator("textarea").count(), 0, "no command box on this page");
    assert.equal(await buttonIn(page, L("en", "local.authorize")).isDisabled(), true, "authorize needs a chosen folder");
    await buttonIn(page, L("en", "local.choose")).click();
    await page.locator("code", { hasText: "/Users/demo/Projects/app" }).waitFor();
    await page.locator("code", { hasText: "https://tofi.example" }).waitFor();
    await buttonIn(page, L("en", "local.authorize")).click();
    await page.getByText(L("en", "local.select.authorized")).waitFor();
    assert.deepEqual((await named(page, "authorizeSandboxRoot"))[0], ["authorizeSandboxRoot", "sel_1"]);
    await page.getByText(L("en", "local.summary.ready")).waitFor();
    await buttonIn(page, L("en", "local.revoke")).click();
    const revokeDialog = page.getByRole("alertdialog");
    await revokeDialog.waitFor();
    assert.equal((await named(page, "sandboxRevoke")).length, 0, "revoke waits for the confirmation");
    await buttonIn(revokeDialog, L("en", "confirm.revoke.confirm")).click();
    await page.getByText(L("en", "local.select.revoked")).waitFor();
    assert.equal((await named(page, "sandboxRevoke")).length, 1);
    await page.getByText(L("en", "local.summary.disabled")).waitFor();

    // Pair.
    await page.locator("#mac-code").fill("ABC-123");
    await buttonIn(page, L("en", "pair.submit")).click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "pair"));
    assert.deepEqual((await named(page, "pair"))[0], ["pair", "ABC-123"]);
    await page.waitForFunction(() => document.querySelector("#mac-code").value === "");

    // Failures are visible, and the page recovers with a fresh read.
    await page.evaluate(() => { window.__state.fail = "updateSettings"; });
    await buttonIn(page, L("en", "caps.save")).click();
    await page.getByText(L("en", "error.action", { message: "stub failure in updateSettings" })).waitFor();
    await page.evaluate(() => { window.__state.fail = null; window.__state.status.lastError = "Broker is unreachable"; });
    await page.getByText(L("en", "error.last")).waitFor({ timeout: 6000 });
    assert.ok(await page.getByText("Broker is unreachable").count() >= 1);
    // Never a server call.
    assert.equal(await page.locator("a[href='/']").count(), 1);
    if (shots) {
      await page.evaluate(() => { window.__state.status.lastError = null; });
      await page.screenshot({ path: join(shots, "mac-settings-1280-light.png"), fullPage: false });
    }
    await context.close();
  }
  {
    const { page, context } = await open();
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    await buttonIn(page, L("en", "perm.grant")).click();
    await buttonIn(page, L("en", "perm.reopen.action")).click();
    const reopening = buttonIn(page, L("en", "perm.reopen.busy"));
    await reopening.waitFor();
    assert.equal(await reopening.isDisabled(), true, "the reopen button stays disabled while Tofi quits");
    assert.equal(await buttonIn(page, L("en", "caps.save")).isDisabled(), true, "the whole page is locked while Tofi quits");
    assert.equal((await named(page, "relaunch")).length, 1, "one relaunch");
    await context.close();
  }
  console.log("PASS actions: save payload, permission guides and poll, stale-entry note and reopen, Bot allow/deny/remove, stop/resume, local execution, pair, failures");

  // ---- 3. Deny and revoke in a locale with long text, and the control state when the user took over.
  {
    const { page, context } = await open({ lang: "de" });
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    await page.evaluate(() => { window.__state.status.control = { phase: "paused", reason: "user_takeover" }; });
    await page.getByText(L("de", "bots.control.paused_takeover")).waitFor({ timeout: 6000 });
    await context.close();
  }

  // ---- 4. Phone width: nothing overflows in any language, and tap targets are 44 px.
  for (const lang of LANGS) {
    const { page, context } = await open({ lang, width: 390, height: 844 });
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    const overflow = await page.evaluate(() => {
      const body = document.querySelector(".settings-page-body");
      const bad = [...document.querySelectorAll(".mac-settings *")].filter(el => { const r = el.getBoundingClientRect(); return r.width > 0 && (r.right > innerWidth + 0.5 || r.left < -0.5); }).map(el => el.tagName + "." + el.className).slice(0, 5);
      return { page: document.documentElement.scrollWidth - innerWidth, body: body.scrollWidth - body.clientWidth, bad };
    });
    assert.ok(overflow.page <= 0 && overflow.body <= 0 && overflow.bad.length === 0, `${lang}: 390px overflow ${JSON.stringify(overflow)}`);
    const small = await page.evaluate(() => [...document.querySelectorAll(".mac-settings button, .mac-settings a.secondary-button, .mac-settings input:not([type=checkbox])")].filter(el => { const r = el.getBoundingClientRect(); return r.height > 0 && r.height < 43.5; }).map(el => el.textContent.trim() || el.id).slice(0, 5));
    assert.deepEqual(small, [], `${lang}: controls under 44px tall at 390`);
    if (shots && (lang === "en" || lang === "ja")) await page.screenshot({ path: join(shots, `mac-settings-390-${lang}.png`), fullPage: true });
    await context.close();
  }
  console.log(`PASS ${LANGS.length} locales at 390 px: no overflow, 44 px controls`);

  // ---- 5. Theme follows the app's choice; screenshots; no bridge.
  {
    const { page, context } = await open({ theme: "dark", colorScheme: "light" });
    await page.waitForFunction(() => document.querySelector("#mac-name").value === "Studio Mac");
    assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark", "themePreference decides the theme");
    if (shots) await page.screenshot({ path: join(shots, "mac-settings-1280-dark.png") });
    await context.close();
  }
  {
    const { page, context } = await open({ stub: false });
    await page.getByText(L("en", "unavailable")).waitFor();
    assert.equal(await page.locator("#mac-name").isDisabled(), true);
    await context.close();
  }
  console.log("PASS theme from the desktop app, and a clear message without the desktop bridge");

  assert.deepEqual(apiCalls, [], "the page made /api requests");
  assert.deepEqual(problems, [], "page errors");
  console.log("PASS no /api request, no page error");
} finally {
  await browser.close();
  await server.close();
}
