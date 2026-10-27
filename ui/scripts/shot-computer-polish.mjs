// Screenshots of the small computer widget, the big computer, the folded strip and the floating sidebar (docs/design/components.md section 20).
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-computer-polish.mjs [outDir]   Synthetic data only.
import { mkdirSync } from "node:fs";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { botOwner, newState, openWorkspace } from "./widgets-fixture.mjs";

const out = process.argv[2] || "/tmp/computer-sidebar-polish";
mkdirSync(out, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
try {
  for (const theme of ["light", "dark"]) for (const width of [1440, 1100]) {
    const T = `${theme}-${width}`;
    const state = newState();
    state.ownership = botOwner("run-1");
    const { context, page } = await openWorkspace(browser, origin, state, { width, theme });
    await page.waitForSelector(".desktop-live-screen", { timeout: 20000 });
    await page.mouse.move(5, 5); await page.waitForTimeout(500);
    const shell = page.locator(".widget-column");
    const clip = async () => { const b = await shell.boundingBox(); return b ? { x: Math.max(0, b.x - 12), y: Math.max(0, b.y - 12), width: Math.min(b.width + 24, width), height: Math.min(b.height + 24, 520) } : undefined; };
    if (width >= 1100) {
      await page.screenshot({ path: `${out}/${T}-small-idle.png`, clip: await clip() });
      await page.locator(".widget-column .desktop-live-screen").hover({ position: { x: 100, y: 60 } }); await page.waitForTimeout(500);
      await page.screenshot({ path: `${out}/${T}-small-hover.png`, clip: await clip() });
      // waking: the same computer goes back to sleep
      state.info = { kind: "firecracker", state: "resuming", phase: "hibernated" }; state.capture = "pending";
    }
    await page.mouse.move(5, 5);
    await page.locator("[data-computer-toggle]").first().focus().catch(() => undefined);
    // Big window.
    await page.locator('.widget-column .desktop-live-screen [role=button], .widget-column .desktop-live-screen').first().click({ position: { x: 100, y: 60 } }).catch(() => undefined);
    await page.waitForTimeout(900);
    await page.screenshot({ path: `${out}/${T}-big.png` });
    await page.keyboard.press("Escape"); await page.waitForTimeout(700);
    await context.close();
  }
  // Waking (no seconds) + strip.
  {
    const state = newState();
    state.info = { kind: "firecracker", state: "ready", desktop_starting: true }; state.capture = "pending"; state.ownership = botOwner("run-1");
    const { context, page } = await openWorkspace(browser, origin, state, { width: 1440 });
    await page.waitForSelector(".widget-column .desktop-placeholder", { timeout: 20000 });
    await page.waitForTimeout(3500);
    const b = await page.locator(".widget-column").boundingBox();
    await page.screenshot({ path: `${out}/light-1440-small-waking.png`, clip: { x: b.x - 12, y: b.y - 12, width: b.width + 24, height: 360 } });
    await context.close();
  }
  // Strip: the Bot lets go and the widget folds itself 30 s later (there is no fold button on the small screen).
  for (const theme of ["light", "dark"]) {
    const state = newState();
    state.ownership = botOwner("run-1");
    const { context, page } = await openWorkspace(browser, origin, state, { width: 1440, theme });
    await page.waitForSelector(".desktop-live-screen", { timeout: 20000 });
    state.ownership = { owner: null, waiting: [] };
    await page.waitForSelector(".computer-detail.is-folded", { timeout: 45000 });
    await page.mouse.move(5, 5); await page.waitForTimeout(800);
    const b = await page.locator(".widget-column").boundingBox();
    await page.screenshot({ path: `${out}/${theme}-1440-strip.png`, clip: { x: b.x - 12, y: b.y - 12, width: b.width + 24, height: 110 } });
    await context.close();
  }
  // Sidebar.
  for (const theme of ["light", "dark"]) for (const width of [1440, 1100, 900]) {
    const state = newState();
    const { context, page } = await openWorkspace(browser, origin, state, { width, theme });
    const label = text => page.getByRole("button", { name: text, exact: true });
    await label("Tasks").click(); await page.waitForTimeout(900);
    await page.screenshot({ path: `${out}/${theme}-${width}-sidebar.png` });
    if (width >= 1100) { await page.locator(".sidebar-shell-expand").click(); await page.waitForTimeout(900); await page.screenshot({ path: `${out}/${theme}-${width}-sidebar-expanded.png` }); }
    await context.close();
  }
} finally { await browser.close(); await server.close(); }
