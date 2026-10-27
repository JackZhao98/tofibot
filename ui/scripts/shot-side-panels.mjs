// Screenshots of the two right-hand layers (side panel + outside panel) at 1440 / 1100 / 960 / 390, light + dark.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-side-panels.mjs   (synthetic data only; SHOT_DIR, MEASURE=1)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/side-panels");
mkdirSync(out, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
const measure = process.env.MEASURE === "1";
const items = [
  { id: "att-report", name: "quarterly-report.md", mime: "text/markdown", size: 4200 },
  { id: "att-data", name: "signups.csv", mime: "text/csv", size: 18800 },
  { id: "att-chart", name: "retention-chart.png", mime: "image/png", size: 52000 },
  { id: "att-script", name: "cleanup.py", mime: "text/x-python", size: 900 },
].map(item => ({ ...item, created_at: "2026-10-09T12:00:00Z", conversation_id: "conv-synthetic", conversation_name: "Mochi", conversation_kind: "dm", conversation_archived: false, message_id: "m1", message_seq: 1, uploaded: false, version_group: item.id, version_count: 1 }));

for (const theme of ["light", "dark"]) {
  for (const width of [1440, 1100, 960, 390]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await installStubs(page, { info: { kind: "firecracker", state: "ready" }, capture: "image" });
    await page.route("**/api/artifacts*", route => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items, next_cursor: "" }) }));
    await page.route("**/api/attachments/*", route => route.fulfill({ status: 200, contentType: "text/markdown", body: "# Quarterly report\n\nSignups grew **12%** over the quarter.\n\n- Retention is flat\n- Support load is down\n" }));
    await page.goto(origin + "/");
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    if (width < 700) await page.locator(".conversation-row").first().click();
    await page.locator(".computer-button").first().waitFor();
    await page.waitForTimeout(900);
    const name = suffix => `${out}/${theme}-${width}-${suffix}.png`;
    const sizes = async label => { if (measure) console.log(theme, width, label, JSON.stringify(await page.evaluate(() => ({ chat: document.querySelector(".chat-pane")?.getBoundingClientRect().width, composer: document.querySelector(".composer")?.getBoundingClientRect().toJSON(), side: document.querySelector(".side-panel")?.getBoundingClientRect().toJSON(), outside: document.querySelector(".detail-pane.outside-panel")?.getBoundingClientRect().width })))); };
    const toggle = page.locator(".side-toggle");
    const cards = page.locator(".side-panel .side-card");
    if (width >= 1100) {
      await page.screenshot({ path: name("side-panel") });
      await sizes("side");
      for (const [view, open] of [["desktop", () => page.locator('[data-side-card="computer"] .side-row').click()], ["file", () => page.locator('[data-side-card="outputs"] .side-row').first().click()], ["bot", () => page.locator('[data-side-card="bot"] .side-row').click()]]) {
        await open();
        await page.waitForTimeout(1300);
        await page.screenshot({ path: name(`outside-${view}`) });
        await sizes(view);
        const expand = page.locator(".outside-expand");
        if (await expand.isVisible()) {
          await expand.click();
          await page.waitForTimeout(700);
          await page.screenshot({ path: name(`outside-${view}-wide`) });
          await sizes(`${view}-wide`);
          await expand.click();
          await page.waitForTimeout(500);
        }
        await page.keyboard.press("Escape");
        await page.waitForTimeout(700);
      }
      await toggle.click();
      await page.waitForTimeout(500);
      await page.screenshot({ path: name("side-hidden") });
    } else {
      await page.screenshot({ path: name("closed") });
      await toggle.click();
      await page.waitForTimeout(700);
      await page.screenshot({ path: name("side-sheet") });
      if ((await cards.count()) > 0) {
        await page.locator('[data-side-card="bot"] .side-row').click();
        await page.waitForTimeout(1200);
        await page.screenshot({ path: name("outside-bot-drawer") });
      }
    }
    if (errors.length) console.log("pageerrors", theme, width, errors);
    await context.close();
  }
}
await browser.close();
await server.close();
console.log("done", out);
