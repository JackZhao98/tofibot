// Screenshots of the context column at 1440 / 1100 / 960 / 390, light + dark, computer + Bot profile open and closed.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-context-column.mjs   (synthetic data only)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/context-column");
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
    await installStubs(page, { kind: "firecracker", state: "ready" });
    await page.goto(origin + "/");
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    if (width < 700) await page.locator(".conversation-row").first().click();
    await page.locator(".computer-button").first().waitFor();
    const name = suffix => `${out}/${theme}-${width}-${suffix}.png`;
    const sizes = async label => { if (measure) console.log(theme, width, label, JSON.stringify(await page.evaluate(() => ({ chat: document.querySelector(".chat-pane")?.getBoundingClientRect().width, composer: document.querySelector(".composer")?.getBoundingClientRect().toJSON(), column: document.querySelector(".detail-pane")?.getBoundingClientRect().width, cards: [...document.querySelectorAll(".context-card")].map(card => card.dataset.card) })))); };
    await page.screenshot({ path: name("closed") });
    await sizes("closed");
    await page.locator("[data-computer-toggle]").click();
    await page.waitForTimeout(900);
    if (width < 700) { await page.screenshot({ path: name("computer-sheet") }); await page.keyboard.press("Escape"); await page.waitForTimeout(600); }
    await page.locator(".chat-identity").click();
    await page.waitForTimeout(1200);
    await page.screenshot({ path: name("computer-and-profile") });
    await sizes("both");
    if (width >= 1100) {
      await page.locator('[data-card="desktop"] .context-card-button').first().click();
      await page.waitForTimeout(600);
      await page.screenshot({ path: name("computer-collapsed") });
    }
    if (errors.length) console.log("pageerrors", theme, width, errors);
    await context.close();
  }
}
await browser.close();
await server.close();
console.log("done", out);
