// Rendered acceptance for the desktop app's slim Mac settings page: files and local execution, marked In development, reached
// only from the this-Mac row's menu (desktop-settings.html, src/desktop-settings/). Pairing, the screen and control switches,
// the macOS permissions and the Bot list are gone from it (remote control is the workspace switch).
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
    name: "Studio Mac", workspaceRoot: "/Users/demo/Tofi", fileAccess: true,
    paired: true, deviceID: "dev_123", capabilities: ["files"], permissions: { screen: false, accessibility: true },
    sandbox: { enabled: false, root: null, connectionID: null, bound: false, available: true, running: false },
    lastError: null, control: { phase: "idle" }
  }, fail: null, selection: { selectionID: "sel_1", root: "/Users/demo/Projects/app", serverLabel: "https://tofi.example" } };
  const snap = () => JSON.parse(JSON.stringify(state.status));
  const rec = (name, ...args) => { calls.push([name, ...args]); if (state.fail === name) throw new Error("stub failure in " + name); };
  window.__calls = calls; window.__state = state;
  window.tofiDesktop = Object.freeze({
    platform: "darwin", themePreference: window.__theme || "system",
    getSettings: async () => { rec("getSettings"); return snap(); },
    updateSettings: async input => { rec("updateSettings", input); Object.assign(state.status, { workspaceRoot: input.workspaceRoot, fileAccess: input.fileAccess }); return snap(); },
    chooseWorkspace: async () => { rec("chooseWorkspace"); return "/Users/demo/Elsewhere"; },
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
  const buttonIn = (scope, name) => scope.getByRole("button", { name, exact: true });
  const ready = page => page.waitForFunction(() => document.querySelector("code")?.textContent === "/Users/demo/Tofi");

  // ---- 1. Every locale renders the page from its own catalog, with no key showing through, marked In development.
  for (const lang of LANGS) {
    const { page, context } = await open({ lang });
    await ready(page);
    assert.equal((await page.locator("h1").first().evaluate(node => node.firstChild.textContent)).trim(), L(lang, "title"), `${lang}: title text`);
    assert.equal((await page.locator(".mac-dev-tag").innerText()).trim(), L(lang, "inDevelopment"), `${lang}: In development mark`);
    assert.equal((await page.locator(".mac-settings-back").innerText()).trim(), L(lang, "back"), `${lang}: back link`);
    assert.equal(await page.locator(".mac-settings-back").getAttribute("href"), "/", `${lang}: back link goes to /`);
    for (const section of ["caps.title", "local.title"]) {
      assert.ok(await page.locator("h4", { hasText: L(lang, section) }).count() >= 1, `${lang}: section ${section}`);
    }
    const text = await page.locator("body").innerText();
    assert.ok(!/\bmac\.[a-z]+\.[A-Za-z.]+/.test(text), `${lang}: a message key shows through`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${lang}: no horizontal overflow at 1280`);
    await context.close();
  }
  console.log(`PASS ${LANGS.length} locales render their own catalog`);

  // ---- 2. Behaviour, in English: only files and local execution are left.
  {
    const { page, context } = await open();
    await ready(page);
    const body = (await page.locator("body").innerText());
    for (const gone of ["Pairing code", "pairing code", "Screen capture", "Desktop control", "macOS permissions", "Bots on this Mac", "Open System Settings", "Refresh status", "Device name"]) assert.ok(!body.includes(gone), `no "${gone}" on the slim page`);
    assert.equal(await page.locator("#mac-name, #mac-code, input[type=text]").count(), 0, "no name or code field");
    assert.deepEqual(await page.getByRole("switch").evaluateAll(list => list.map(node => node.getAttribute("aria-label"))), [L("en", "caps.files.label")], "the files switch is the only switch");
    assert.equal(await page.getByRole("switch", { name: L("en", "caps.files.label") }).isChecked(), true);
    assert.equal(await page.locator("code", { hasText: "/Users/demo/Tofi" }).count(), 1);

    // Save sends exactly two fields, nothing about screen, control or the execution grant.
    await page.getByRole("switch", { name: L("en", "caps.files.label") }).uncheck();
    await buttonIn(page, L("en", "device.choose")).click();
    await page.waitForFunction(() => document.body.innerText.includes("/Users/demo/Elsewhere"));
    await buttonIn(page, L("en", "caps.save")).click();
    await page.waitForFunction(() => window.__calls.some(call => call[0] === "updateSettings"));
    assert.deepEqual((await named(page, "updateSettings"))[0][1], { workspaceRoot: "/Users/demo/Elsewhere", fileAccess: false });
    const before = (await named(page, "getSettings")).length;
    await page.waitForFunction(count => window.__calls.filter(call => call[0] === "getSettings").length > count, before, { timeout: 6000 });

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

    // Failures are visible, and the page recovers with a fresh read.
    await page.evaluate(() => { window.__state.fail = "updateSettings"; });
    await buttonIn(page, L("en", "caps.save")).click();
    await page.getByText(L("en", "error.action", { message: "stub failure in updateSettings" })).waitFor();
    await page.evaluate(() => { window.__state.fail = null; window.__state.status.lastError = "Broker is unreachable"; });
    await page.getByText(L("en", "error.last")).waitFor({ timeout: 6000 });
    assert.ok(await page.getByText("Broker is unreachable").count() >= 1);
    assert.equal(await page.locator("a[href='/']").count(), 1);
    if (shots) {
      await page.evaluate(() => { window.__state.status.lastError = null; });
      await page.screenshot({ path: join(shots, "mac-settings-1280-light.png"), fullPage: false });
    }
    await context.close();
  }
  console.log("PASS actions: save payload, local execution choose/authorize/revoke, failures; nothing of pairing, screen, control, permissions or Bots");

  // ---- 3. Phone width: nothing overflows in any language, and tap targets are 44 px.
  for (const lang of LANGS) {
    const { page, context } = await open({ lang, width: 390, height: 844 });
    await ready(page);
    const overflow = await page.evaluate(() => {
      const body = document.querySelector(".settings-page-body");
      const bad = [...document.querySelectorAll(".mac-settings *")].filter(el => { const r = el.getBoundingClientRect(); return r.width > 0 && (r.right > innerWidth + 0.5 || r.left < -0.5); }).map(el => el.tagName + "." + el.className).slice(0, 5);
      return { page: document.documentElement.scrollWidth - innerWidth, body: body.scrollWidth - body.clientWidth, bad };
    });
    assert.ok(overflow.page <= 0 && overflow.body <= 0 && overflow.bad.length === 0, `${lang}: 390px overflow ${JSON.stringify(overflow)}`);
    const small = await page.evaluate(() => [...document.querySelectorAll(".mac-settings button, .mac-settings a.secondary-button, .mac-settings input:not([type=checkbox])")].filter(el => { const r = el.getBoundingClientRect(); return r.height > 0 && r.height < 43.5; }).map(el => el.textContent.trim()));
    assert.deepEqual(small, [], `${lang}: controls under 44px tall at 390`);
    if (shots && (lang === "en" || lang === "ja")) await page.screenshot({ path: join(shots, `mac-settings-390-${lang}.png`), fullPage: true });
    await context.close();
  }
  console.log(`PASS ${LANGS.length} locales at 390 px: no overflow, 44 px controls`);

  // ---- 4. Theme follows the app's choice; screenshots; no bridge.
  {
    const { page, context } = await open({ theme: "dark", colorScheme: "light" });
    await ready(page);
    assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark", "themePreference decides the theme");
    if (shots) await page.screenshot({ path: join(shots, "mac-settings-1280-dark.png") });
    await context.close();
  }
  {
    const { page, context } = await open({ stub: false });
    await page.getByText(L("en", "unavailable")).waitFor();
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
