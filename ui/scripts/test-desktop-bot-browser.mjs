// Synthetic sandboxed desktop chrome; no account, preload or server API.
import assert from "node:assert/strict";
import { startUi, launch } from "./computer-window-fixture.mjs";

const { server, origin } = await startUi();
let browser;
try {
  browser = await launch();
  const context = await browser.newContext({ locale: "en-US", viewport: { width: 760, height: 600 } });
  const page = await context.newPage();
  const api = [];
  page.on("request", request => { if (new URL(request.url()).pathname.startsWith("/api/")) api.push(request.url()); });
  await page.goto(`${origin}/desktop-bot-browser.html`);
  await page.waitForFunction(() => typeof window.renderHost === "function");
  const initial = { active: null, url: "", targets: [], phase: "closed", acting: false, loading: false, canGoBack: false, canGoForward: false };
  const render = async data => {
    await page.evaluate(data => window.renderHost(data), { ...initial, ...data });
    // Wait for React's external-store update to commit.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  await page.getByRole("heading", { name: "Bot browser", exact: true }).waitFor();
  for (const name of ["Back", "Forward", "Reload"]) assert.ok(await page.getByRole("button", { name, exact: true }).isDisabled(), `${name} disabled with no page`);
  assert.equal(await page.getByRole("tablist").count(), 0);
  const targets = [
    { target_id: "target-one", url: "https://example.com", title: "First" },
    { target_id: "target-two", url: "https://example.org", title: "Second" },
  ];
  const active = { active: "target-one", url: targets[0].url, targets, phase: "bot", canGoBack: true, canGoForward: true };
  await render({ ...active, targets: [targets[0]], canGoBack: false, canGoForward: false });
  assert.equal(await page.getByRole("tablist").count(), 0);
  for (const name of ["Back", "Forward"]) assert.ok(await page.getByRole("button", { name, exact: true }).isDisabled());
  assert.ok(await page.getByRole("button", { name: "Reload", exact: true }).isEnabled());
  await render(active);
  assert.equal(await page.locator(".bb-empty").count(), 0);
  assert.equal((await page.locator(".bb-toolbar").boundingBox()).height, 52);
  assert.equal((await page.getByRole("tablist").boundingBox()).height, 36);
  const hashClick = async (name, hash, role = "button") => {
    await page.getByRole(role, { name, exact: true }).click();
    assert.equal(await page.evaluate(() => location.hash), `#${hash}`);
  };
  await hashClick("Second", "target-two", "tab");
  for (const [name, hash] of [["Back", "back"], ["Forward", "forward"], ["Reload", "reload"], ["Close Bot browser", "close"]]) await hashClick(name, hash);
  await hashClick("Close Bot browser", "close"); // repeated signals survive hash reset
  await render({ ...active, loading: true });
  await hashClick("Stop", "stop");
  assert.equal(await page.locator(".bb-progress").count(), 1);
  for (const phase of ["idle", "bot", "human", "stopped", "closed"]) {
    for (const acting of [false, true]) {
      await render({ ...active, phase, acting });
      assert.equal(await page.getByRole("status").count(), Number(phase === "bot" && acting));
      assert.equal(await page.getByRole("button", { name: "Hand back to Bot", exact: true }).count(), Number(["human", "stopped"].includes(phase)));
      if (["human", "stopped"].includes(phase)) await hashClick("Hand back to Bot", "return");
    }
  }
  await render({ ...active, acting: true });
  await page.setViewportSize({ width: 360, height: 600 });
  assert.ok(!(await page.locator(".bb-status-text").isVisible()));
  assert.ok(await page.locator(".bb-status-dot").isVisible());
  assert.ok((await page.locator(".bb-address").boundingBox()).width >= 120);
  await page.setViewportSize({ width: 760, height: 600 });
  const field = page.getByRole("textbox");
  await field.fill("edited text");
  await render({ ...active, url: "https://example.net", loading: true });
  assert.equal(await field.inputValue(), "edited text");
  await field.press("Escape");
  assert.equal(await field.inputValue(), "https://example.net");
  assert.ok(await field.evaluate(el => document.activeElement !== el));
  await field.fill("abc");
  await field.press("ArrowLeft");
  assert.ok(await field.evaluate(el => document.activeElement === el && el.selectionStart === 2));
  await page.getByRole("button", { name: "Back", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  // Radix moves roving focus in a task after the key event: wait for the move, do not sample it.
  await page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "Forward", null, { timeout: 2000 });
  await page.getByRole("tab", { name: "First", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await page.waitForFunction(() => location.hash === "#target-two");
  assert.ok(await page.getByRole("tab", { name: "Second", exact: true }).evaluate(el => document.activeElement === el));
  const conversions = [
    ["example.com", "https://example.com"], ["localhost:5173", "http://localhost:5173"],
    ["10.0.10.56:8321", "http://10.0.10.56:8321"], ["what is tofi", "https://www.google.com/search?q=what%20is%20tofi"], ["  ", null],
    ["HTTP://example.com/a", "HTTP://example.com/a"], ["127.0.0.1:80/a", "http://127.0.0.1:80/a"],
    ["192.168.1.2/a", "http://192.168.1.2/a"], ["172.16.0.1", "http://172.16.0.1"],
    ["172.31.0.1:80", "http://172.31.0.1:80"], ["172.32.0.1", "https://www.google.com/search?q=172.32.0.1"],
  ];
  const results = await page.evaluate(async table => {
    const { addressURL } = await import("/src/desktop-bot-browser/hostBridge.ts");
    return table.map(([raw]) => addressURL(raw));
  }, conversions);
  assert.deepEqual(results, conversions.map(([, url]) => url));
  const colors = [];
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, colorScheme);
    colors.push(await page.locator(".bb-toolbar").evaluate(el => getComputedStyle(el).backgroundColor));
    assert.equal((await page.locator(".bb-toolbar").boundingBox()).height, 52);
  }
  assert.notEqual(...colors);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await render({ ...active, acting: true });
  assert.equal(await page.locator(".bb-status-dot").evaluate(el => getComputedStyle(el).animationName), "none");
  assert.deepEqual(api, []);
  // Delay the catalogs so a real host call arrives before React mounts.
  const early = await context.newPage();
  let releaseCatalogs;
  const catalogs = new Promise(resolve => { releaseCatalogs = resolve; });
  await early.route("**/src/locales/**", async route => { await catalogs; await route.continue(); });
  await early.goto(`${origin}/desktop-bot-browser.html`, { waitUntil: "commit" });
  await early.waitForFunction(() => typeof window.renderHost === "function");
  await early.evaluate(data => window.renderHost(data), { ...initial, ...active, phase: "human" });
  releaseCatalogs();
  await early.getByRole("button", { name: "Hand back to Bot", exact: true }).waitFor();
  assert.equal(await early.getByRole("textbox").inputValue(), active.url);
  assert.equal(await early.getByRole("tab").count(), 2);
  // Stand in for the main process's intercepted address navigation.
  const navigation = early.waitForRequest(request => request.isNavigationRequest() && request.url() === "https://example.com/");
  await early.route("https://example.com/", route => route.abort());
  await early.getByRole("textbox").fill("example.com");
  await early.getByRole("textbox").press("Enter");
  assert.equal((await navigation).url(), "https://example.com/");
  await early.close();
  // The toolbar has its own storage partition: it follows the chat page's language and theme sent with renderHost.
  await render({ look: { lang: "zh-CN", theme: "light" } });
  await page.waitForFunction(() => document.documentElement.lang === "zh-CN");
  assert.equal(await page.getByRole("heading", { name: "Bot browser", exact: true }).count(), 0, "the heading is no longer English");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light", "the chat page's theme wins over the system theme");
  await page.emulateMedia({ colorScheme: "dark" });
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light", "a system theme change does not undo it");
  console.log("PASS desktop Bot browser chrome, bridge, address conversion, keyboard, geometry and themes");
  await context.close();
} finally {
  await browser?.close();
  await server.close();
}
