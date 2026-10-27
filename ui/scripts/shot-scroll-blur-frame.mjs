// Screenshots for the scrollbar / bottom-blur / computer-frame change (docs/design/side-panels.md):
// a long chat with the computer and Artifacts widgets open (scrollbar at the right edge of the chat), the bottom of a group chat
// (no blur above the composer) and the computer card close-up. 1440 and 1100, light and dark. Synthetic data only.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> SHOT_DIR=/tmp/scroll-blur-frame node scripts/shot-scroll-blur-frame.mjs
import { mkdirSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { installStubs, startUi } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || "/tmp/scroll-blur-frame";
mkdirSync(out, { recursive: true });
const { server, origin } = await startUi();
// Classic (always visible) scrollbars: headless hides them by default, and the point of the shots is where the scrollbar sits.
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || "/Users/jackzhao/Developer/sentiosurge/tofibot-web/node_modules/playwright/index.mjs"));
const browser = await chromium.launch({ headless: true, ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--disable-features=OverlayScrollbar"] });
// If this Chromium still draws overlay scrollbars, force a 15px classic one on the message pane so its position is visible and measurable.
const CLASSIC = ".message-scroll{scrollbar-color:auto!important}.message-scroll::-webkit-scrollbar{width:15px}.message-scroll::-webkit-scrollbar-thumb{background:#8a8a8a;border-radius:8px;border:3px solid transparent;background-clip:padding-box}.message-scroll::-webkit-scrollbar-track{background:rgba(128,128,128,.15)}";
const iso = n => new Date(Date.UTC(2026, 9, 9, 12, 0, n)).toISOString();
const text = i => `Message ${i}. ${"The quick brown fox jumps over the lazy dog and keeps going so lines wrap in the narrower column. ".repeat(1 + (i % 3))}`;
const messages = conv => Array.from({ length: 60 }, (_, i) => ({ id: `m${i}`, conversation_id: conv, role: i % 2 ? "assistant" : "user", content: text(i), seq: i + 1, created_at: iso(i), ...(i % 2 ? { bot_id: "bot-synthetic" } : {}) }));
const group = { id: "grp-synthetic", kind: "group", name: "Synthetic Group", bot_ids: ["bot-synthetic"], updated_at: iso(0), read_seq: 0, user_visible: true };

for (const theme of ["light", "dark"]) {
  for (const width of [1440, 1100]) {
    for (const kind of ["dm", "group"]) {
      const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme });
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", error => errors.push(error.message));
      const state = { info: { kind: "firecracker", state: "ready" }, capture: "image", messages: messages(kind === "group" ? "grp-synthetic" : "conv-synthetic"), ...(kind === "group" ? { conversation: group } : {}) };
      await installStubs(page, state);
      await page.goto(origin + "/");
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      await page.addStyleTag({ content: CLASSIC });
      await page.locator(".computer-button").first().waitFor();
      await page.waitForTimeout(1200);
      const name = suffix => `${out}/${theme}-${width}-${kind}-${suffix}.png`;
      await page.screenshot({ path: name("bottom") });
      if (kind === "dm") {
        await page.locator("[data-computer-toggle]").click();
        await page.waitForTimeout(1500);
        await page.getByRole("button", { name: "Open artifacts", exact: true }).click();
        await page.waitForTimeout(1500);
        await page.screenshot({ path: name("cards") });
        const geometry = await page.evaluate(() => {
          const r = selector => document.querySelector(selector)?.getBoundingClientRect().toJSON();
          const scroll = document.querySelector(".message-scroll");
          return { chat: r(".chat-pane"), scroller: r(".message-scroll"), scrollBar: scroll.offsetWidth - scroll.clientWidth, column: r(".widget-column"), composer: r(".composer"), list: r(".message-list") };
        });
        console.log(theme, width, JSON.stringify(geometry));
        const card = page.locator('[data-card="computer"]');
        await card.scrollIntoViewIfNeeded();
        const box = await card.boundingBox();
        await page.screenshot({ path: name("computer-card"), clip: { x: box.x - 8, y: box.y - 8, width: box.width + 16, height: Math.min(box.height + 60, 900 - box.y + 8) } });
      }
      if (errors.length) console.log("pageerrors", theme, width, kind, errors);
      await context.close();
    }
  }
}
await browser.close();
await server.close();
console.log("done", out);
