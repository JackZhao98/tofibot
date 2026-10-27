// Screenshots for the widget column / header polish round (docs/design/widgets-and-sidebar.md "Polish r5"): continuous header blur over chat + widgets,
// full card shadows, glass header pills, Usage on an older server, long-press drag, scrollbar outside the blur. Synthetic data only.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> SHOT_DIR=/tmp/polish-r5 ENGINES=chromium,webkit node scripts/shot-polish-r5.mjs
import { mkdirSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, botOwner, newState, openWorkspace, talk } from "./widgets-fixture.mjs";

const out = process.env.SHOT_DIR || "/tmp/polish-r5";
mkdirSync(out, { recursive: true });
const engines = (process.env.ENGINES || "chromium,webkit").split(",");
const themes = (process.env.THEMES || "light,dark").split(",");
const pw = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || "/Users/jackzhao/Developer/sentiosurge/tofibot-web/node_modules/playwright/index.mjs"));
const { server, origin } = await startUi();
const long = Array.from({ length: 40 }, (_, i) => ({ id: `msg-long-${i}`, conversation_id: DM.id, seq: i + 1, role: i % 2 ? "assistant" : "user", content: i % 2 ? "Here is a longer reply with a few lines of text so the blurred band has something to show under the header. ".repeat(2) : "Please keep going with the quarterly numbers and the charts.", created_at: `2026-10-26T11:${String(i % 60).padStart(2, "0")}:00Z`, sender_bot_id: i % 2 ? BOT.id : undefined }));
const errors = [];
try {
  for (const engine of engines) {
    const browser = await pw[engine.split(":")[0]].launch({ headless: true, ...(engine.endsWith(":bars") ? { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--disable-features=OverlayScrollbar"] } : {}), ...(engine === "webkit" && process.env.TOFI_TEST_WEBKIT ? { executablePath: process.env.TOFI_TEST_WEBKIT } : {}) });
    for (const theme of themes) {
      const tag = `${engine.replace(':','-')}-${theme}`;
      const state = newState();
      state.messages[DM.id] = long;
      state.ownership = botOwner();
      const { context, page, errors: pageErrors } = await openWorkspace(browser, origin, state, { theme, height: 900 });
      // ":bars": a classic 15 px scrollbar (what Safari with a mouse draws), forced on the message scroller
      if (engine.endsWith(":bars")) await page.addStyleTag({ content: ".message-scroll{scrollbar-color:auto!important;scrollbar-width:auto!important} .message-scroll::-webkit-scrollbar{width:15px;background:rgba(0,0,0,.08)} .message-scroll::-webkit-scrollbar-thumb{background:rgba(0,0,0,.4);border-radius:8px}" }), await page.waitForTimeout(300);
      await page.getByRole("button", { name: "Open artifacts" }).click();
      await page.getByRole("button", { name: "More actions" }).click(); await page.getByRole("menuitem", { name: "Usage" }).click();
      await page.locator('.widget-column [data-card="computer"]').waitFor({ timeout: 4000 }).catch(() => page.locator("[data-computer-toggle]").click());
      for (const n of [1, 2, 3, 4]) state.addFile(n);
      await page.waitForTimeout(1800);
      await page.locator(".message-scroll").evaluate(node => { node.scrollTop = 400; });
      await page.waitForTimeout(500);
      await page.screenshot({ path: `${out}/${tag}-columns.png` });
      await page.screenshot({ path: `${out}/${tag}-header-glass.png`, clip: { x: 260, y: 0, width: 1180, height: 200 } });
      // an assistant bubble parked under the identity pill: the pill's glass over real content
      await page.locator(".message-scroll").evaluate(node => { const b = [...node.querySelectorAll(".bubble, [class*=bubble]")].find(x => x.getBoundingClientRect().left < 400 && x.getBoundingClientRect().top > 150); if (b) node.scrollTop += b.getBoundingClientRect().top - 22; });
      await page.waitForTimeout(400);
      await page.screenshot({ path: `${out}/${tag}-pill-over-content.png`, clip: { x: 272, y: 0, width: 500, height: 110 } });
      // the blur band stops at the scrollbar: its right edge against the measured scrollbar width
      const edge = await page.evaluate(() => { const h = document.querySelector(".chat-header"), c = getComputedStyle(h, "::before"), s = document.querySelector(".message-scroll"); return { scrollbar: s.offsetWidth - s.clientWidth, bandRight: c.right, varBarW: getComputedStyle(document.querySelector(".workspace-grid")).getPropertyValue("--scroll-bar-w") }; });
      console.log(tag, "scrollbar edge", JSON.stringify(edge));
      await page.locator(".message-scroll").evaluate(node => { node.scrollTop = 400; });
      await page.waitForTimeout(300);
      // widget column scrolled so cards pass under the header
      await page.locator(".widget-column").evaluate(node => { node.scrollTop = 120; });
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${out}/${tag}-widgets-under-header.png`, clip: { x: 900, y: 0, width: 540, height: 400 } });
      // header: at rest "Title ⌄", hovered shows grip + X, folded; title contrast
      await page.locator(".widget-column").evaluate(node => { node.scrollTop = 0; });
      await page.mouse.move(5, 5); await page.waitForTimeout(300);
      const art = await page.locator('[data-card="artifacts"]').boundingBox();
      await page.screenshot({ path: `${out}/${tag}-header-rest.png`, clip: { x: art.x - 20, y: art.y - 10, width: art.width + 40, height: 120 } });
      await page.mouse.move(art.x + art.width / 2, art.y + 20); await page.waitForTimeout(400);
      await page.screenshot({ path: `${out}/${tag}-header-hover.png`, clip: { x: art.x - 20, y: art.y - 10, width: art.width + 40, height: 120 } });
      await page.locator('[data-card="artifacts"] .context-card-fold').click(); await page.mouse.move(5, 5); await page.waitForTimeout(600);
      await page.screenshot({ path: `${out}/${tag}-header-folded.png`, clip: { x: art.x - 20, y: art.y - 10, width: art.width + 40, height: 120 } });
      await page.locator('[data-card="artifacts"] .context-card-fold').click(); await page.mouse.move(5, 5); await page.waitForTimeout(500);
      await page.screenshot({ path: `${out}/${tag}-usage-new.png`, clip: { x: 900, y: 300, width: 540, height: 600 } });
      console.log(tag, "title contrast", await page.locator('[data-card="usage"] .context-card-fold').evaluate(node => { const lum = css => { const m = css.match(/[\d.]+/g).map(Number); const [r, g, b] = m.slice(0, 3).map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; }; const fg = lum(getComputedStyle(node).color), bg = lum(getComputedStyle(node.closest(".context-card")).backgroundColor); return ((Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05)).toFixed(2); }));
      // usage 404
      state.usage = "404";
      await page.getByRole("button", { name: "Close Usage" }).first().click().catch(() => {});
      await page.waitForTimeout(500);
      await page.getByRole("button", { name: "More actions" }).click(); await page.getByRole("menuitem", { name: "Usage" }).click();
      await page.waitForTimeout(800);
      await page.screenshot({ path: `${out}/${tag}-usage-404.png`, clip: { x: 900, y: 0, width: 540, height: 900 } });
      // press-and-move on the computer screen: the widget lifts and follows the pointer
      await page.locator(".widget-column").evaluate(node => { node.scrollTop = 0; });
      const screen = await page.locator('[data-card="computer"] .desktop-enlarge').boundingBox({ timeout: 2000 }).catch(() => null);
      if (screen) {
        const cx = screen.x + screen.width / 2, cy = screen.y + screen.height / 2;
        await page.mouse.move(cx, cy); await page.mouse.down(); await page.waitForTimeout(450);
        await page.mouse.move(cx, cy + 40, { steps: 6 }); await page.mouse.move(cx, cy + 190, { steps: 10 });
        await page.waitForTimeout(500);
        await page.screenshot({ path: `${out}/${tag}-drag-by-screen.png`, clip: { x: 900, y: 0, width: 540, height: 900 } });
        await page.mouse.up();
      }
      pageErrors.forEach(e => errors.push(`${tag}: ${e}`));
      await context.close();
    }
    await browser.close();
  }
} finally { await server.close(); }
console.log("errors:", errors);
