// Screenshots of the on-demand card column at 1440 / 1100 / 960 / 390, light + dark (docs/design/side-panels.md):
// no card, computer, Bot settings, both stacked, one folded, one moved to the outside panel.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-cards.mjs   (synthetic data only; SHOT_DIR, MEASURE=1)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/cards-on-demand");
mkdirSync(out, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
const measure = process.env.MEASURE === "1";

for (const theme of ["light", "dark"]) {
  for (const width of [1440, 1100, 960, 390]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await installStubs(page, { info: { kind: "firecracker", state: "ready" }, capture: "image" });
    await page.goto(origin + "/");
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    if (width < 700) await page.locator(".conversation-row").first().click();
    await page.locator(".computer-button").first().waitFor();
    await page.waitForTimeout(900);
    const name = suffix => `${out}/${theme}-${width}-${suffix}.png`;
    const sizes = async label => { if (measure) console.log(theme, width, label, JSON.stringify(await page.evaluate(() => ({ chat: document.querySelector(".chat-pane")?.getBoundingClientRect().width, composer: document.querySelector(".composer")?.getBoundingClientRect().toJSON(), column: document.querySelector(".detail-pane.context-column")?.getBoundingClientRect().width, cards: [...document.querySelectorAll(".context-card")].map(card => card.getAttribute("data-card")) })))); };
    const shot = async (suffix, wait = 1300) => { await page.waitForTimeout(wait); await page.screenshot({ path: name(suffix) }); await sizes(suffix); };
    const computer = () => page.locator("[data-computer-toggle]").click();
    const bot = () => page.locator(".chat-identity").click();
    await shot("none", 0);
    await computer();
    await shot("computer");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(700);
    await bot();
    await shot("bot");
    if (width >= 1100) {
      await computer();
      await shot("both");
      await page.locator('[data-card="desktop"] .context-card-fold').click();
      await shot("folded", 700);
      await page.locator('[data-card="desktop"] .context-card-fold').click();
      await page.waitForTimeout(500);
      await page.locator('[data-card="bot-edit"] .context-card-wide').click();
      await shot("outside", 800);
      await page.locator('[data-card="bot-edit"] .context-card-wide').click();
      await page.waitForTimeout(500);
      await page.keyboard.press("Escape");
      await page.keyboard.press("Escape");
      await shot("closed", 800);
    }
    if (errors.length) console.log("pageerrors", theme, width, errors);
    await context.close();
  }
}
await browser.close();
await server.close();
console.log("done", out);
