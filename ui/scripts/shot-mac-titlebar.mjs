// Screenshots of the Mac app's title area (owner 2026-11-14, option C: the sidebar is a floating card, a capsule when collapsed,
// an overlay on a narrow window). The desktop bridge is stubbed; the traffic lights are drawn where the app places them
// (TRAFFIC=x,y, default 22,26) so the shot shows the whole title row. Synthetic data only.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-mac-titlebar.mjs   (SHOT_DIR, THEMES=dark,light, TAG=name prefix)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/mac-titlebar");
mkdirSync(out, { recursive: true });
const themes = (process.env.THEMES || "dark,light").split(",");
const tag = process.env.TAG || "c";
const [tx, ty] = (process.env.TRAFFIC || "22,26").split(",").map(Number);
const NOW = "2026-11-14T12:00:00Z";
const BOTS = ["Mochi", "Atlas", "Probe", "Nori"].map((name, i) => ({ id: `bot-${i}`, name, instructions: "Synthetic bot", model: "default", dm_conversation_id: `conv-${i}`, created_at: NOW }));
const CONVS = [
  ...BOTS.map((bot, i) => ({ id: `conv-${i}`, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: `2026-11-14T1${i}:00:00Z` })),
  { id: "conv-g", kind: "group", name: "Editorial", bot_ids: [BOTS[0].id, BOTS[1].id], updated_at: "2026-11-14T09:00:00Z" },
];
const MESSAGES = [
  { id: "m1", conversation_id: "conv-3", seq: 1, role: "user", content: "Draw a diagram that explains breadth-first search in the style of an office.", created_at: "2026-11-14T11:00:00Z" },
  { id: "m2", conversation_id: "conv-3", seq: 2, role: "assistant", sender_bot_id: BOTS[3].id, content: "Done: I picked breadth-first search and drew it as passing a message down the office, floor by floor.", created_at: "2026-11-14T11:01:00Z" },
];

const { server, origin } = await startUi();
const browser = await launch();
const errors = [];
const lights = (x, y) => `(() => { const add = () => { if (!document.body || document.getElementById("fake-lights")) return; const d = document.createElement("div"); d.id = "fake-lights";
  d.style.cssText = "position:fixed;left:${x}px;top:${y}px;display:flex;gap:8px;z-index:2147483647;pointer-events:none";
  for (const c of ["#ff5f57", "#febc2e", "#28c840"]) { const i = document.createElement("i"); i.style.cssText = "display:block;width:12px;height:12px;border-radius:50%;background:" + c; d.append(i); }
  document.body.append(d); }; document.readyState === "loading" ? addEventListener("DOMContentLoaded", add) : add(); })()`;

async function open(theme, width, height = 860) {
  const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 2, colorScheme: theme });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push(`${theme} ${width}: ${error.message}`));
  await installStubs(page, { info: { kind: "firecracker", state: "hibernated" }, messages: MESSAGES, conversation: CONVS[3] });
  await page.route("**/api/**", async route => {
    const p = new URL(route.request().url()).pathname;
    const json = body => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/api/bots") return json({ bots: BOTS });
    if (p === "/api/conversations") return json({ conversations: CONVS });
    return route.fallback();
  });
  await page.addInitScript(themeName => {
    const off = () => () => {};
    window.tofiDesktop = {
      platform: "darwin", themePreference: themeName,
      onCommand: off, onWindowState: off, onFlushState: off,
      appearance: async () => {}, requestMicrophoneAccess: async () => false,
      getPermissionStatus: async () => ({ supported: true, microphone: "not-determined", screen: "denied", accessibility: false }),
      requestPermission: async () => false, openPrivacySettings: async () => true, openDesktopSettings: async () => true,
      startAccessibilityDrag: () => {}, remoteInputFocus: () => {},
      readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
      prepareAttachment: async () => ({ handle: "h", name: "n" }), saveAttachment: async () => true, revealAttachment: async () => {}, dragAttachment: () => {},
    };
  }, theme);
  await page.addInitScript(lights(tx, ty));
  await page.goto(origin + "/");
  await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
  await page.locator(".contact-pane").waitFor({ timeout: 10000 }).catch(async error => { await page.screenshot({ path: resolve(out, "debug.png") }); throw error; });
  await page.waitForTimeout(900);
  return { context, page };
}

for (const theme of themes) {
  {
    const { context, page } = await open(theme, 1280);
    await page.locator(".conversation-row", { hasText: "Nori" }).first().click().catch(() => {});
    await page.waitForTimeout(600);
    await page.screenshot({ path: resolve(out, `${tag}-open-${theme}@2x.png`) });
    await page.locator(".sidebar-toggle").click();
    await page.mouse.move(700, 500);
    await page.waitForTimeout(700);
    // The capsule holds the toggle, Search and New, each reachable (not under the drag strip or another layer).
    for (const selector of [".sidebar-logo-expand", ".search-trigger", ".create-menu-anchor > .tb-iconbtn"]) {
      const hit = await page.locator(selector).evaluate(el => { const r = el.getBoundingClientRect(); return el.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)); });
      if (!hit) errors.push(`${theme}: ${selector} is covered in the capsule`);
    }
    await page.screenshot({ path: resolve(out, `${tag}-collapsed-${theme}@2x.png`) });
    await page.mouse.move(700, 500);
    await page.evaluate(() => { document.documentElement.dataset.fullscreen = "true"; document.getElementById("fake-lights")?.remove(); });
    await page.waitForTimeout(500);
    await page.screenshot({ path: resolve(out, `${tag}-fullscreen-collapsed-${theme}@2x.png`), clip: { x: 0, y: 0, width: 1280, height: 200 } });
    await context.close();
  }
  {
    const { context, page } = await open(theme, 860, 720);
    await page.screenshot({ path: resolve(out, `${tag}-narrow-${theme}@2x.png`) });
    const toggle = page.locator(".sidebar-logo-expand, .sidebar-toggle").first();
    if (await toggle.count()) {
      await toggle.click(); await page.mouse.move(700, 600); await page.waitForTimeout(700);
      await page.screenshot({ path: resolve(out, `${tag}-narrow-open-${theme}@2x.png`) });
      const top = await page.evaluate(() => document.elementFromPoint(400, 32)?.className || "");
      if (!String(top).includes("sidebar-scrim")) errors.push(`${theme}: the scrim does not cover the chat header (${top})`);
      await page.mouse.click(700, 600); await page.waitForTimeout(500);
      if (!(await page.locator(".workspace-grid.sidebar-collapsed").count())) errors.push(`${theme}: a click on the scrim does not close the card`);
    }
    await context.close();
  }
}
await browser.close();
await server.close();
if (errors.length) { console.error(errors.join("\n")); process.exit(1); }
console.log(out);
