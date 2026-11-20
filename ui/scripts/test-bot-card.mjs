// The header cat and the Bot card (PLAN section 11, rulings of 2026-11-19), in a real browser against synthetic data:
//  - A Bot's header is one big cat (60 px, no name on screen) with a halo of blur under it; the halo is gone while the cat is in the column.
//  - Tapping the cat moves the same cat to the top of the widget column and unfolds the card under it. The cat's first frame is the header cat's box,
//    its last is its resting place in the card (within 1 px, measured after the motion, not during), the card never gets shorter while it opens, and
//    closing reverses it: the cat lands on the header's box again.
//  - The card holds name and role (edited in place and saved with the Bot settings call), a status line, a menu with three rows, a link to the
//    advanced settings and a cat that opens Bot settings on the avatar. It opens the Artifacts widget when the Bot has files.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:bot-card   (BOT_CARD_SHOTS=<dir> also saves 2x screenshots and the per-frame logs)
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, newState, openWorkspace } from "./widgets-fixture.mjs";

const shots = process.env.BOT_CARD_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const near = (a, b, tolerance, message) => assert.ok(Math.abs(a - b) <= tolerance, `${message}: ${a} vs ${b}`);
const rect = (page, selector) => page.evaluate(sel => { const r = document.querySelector(sel)?.getBoundingClientRect(); return r && { x: r.x, y: r.y, w: r.width, h: r.height }; }, selector);

// A page-side sampler: one record per animation frame, until stopped.
const sampler = () => {
  window.__frames = []; window.__stop = false;
  const t0 = performance.now();
  const loop = () => {
    const travel = document.querySelector(".bot-card-cat .tb-chathead-travel") ?? document.querySelector(".chat-header .tb-chathead-travel");
    const box = travel?.getBoundingClientRect();
    const fold = document.querySelector(".bot-fold")?.getBoundingClientRect();
    const halo = document.querySelector(".tb-chathead-halo");
    window.__frames.push({ t: Math.round(performance.now() - t0), in: travel?.closest(".bot-card-cat") ? "card" : travel ? "header" : "none",
      x: box && +box.x.toFixed(2), y: box && +box.y.toFixed(2), w: box && +box.width.toFixed(2), h: box && +box.height.toFixed(2), card: fold ? +fold.height.toFixed(2) : null, halo: halo ? +getComputedStyle(halo).opacity : null });
    if (!window.__stop) requestAnimationFrame(loop);
  };
  requestAnimationFrame(loop);
};
const track = async page => { await page.evaluate(sampler); };
const collect = async page => { await page.evaluate(() => { window.__stop = true; }); await page.waitForTimeout(60); return page.evaluate(() => window.__frames); };

const manyMessages = () => {
  const lines = ["Can you pull the quarterly numbers together?", "On it. I will put the results in files you can open and check the totals against last quarter first.", "Thanks, and flag anything that moved more than ten percent.", "Two lines moved: travel up 14 percent and hosting down 11 percent. Details are in the sheet."];
  return Array.from({ length: 28 }, (_, i) => ({ id: `msg-many-${i}`, conversation_id: DM.id, seq: i + 1, role: i % 2 ? "assistant" : "user", content: lines[i % lines.length], created_at: `2026-10-26T11:${String(10 + (i % 40)).padStart(2, "0")}:00Z`, sender_bot_id: i % 2 ? BOT.id : undefined }));
};

try {
  for (const theme of ["light", "dark"]) {
    const state = newState();
    state.messages[DM.id] = manyMessages();
    state.addFile(1);
    const { context, page, errors } = await openWorkspace(browser, origin, state, { theme, scale: shots ? 2 : 1 });
    // The Bot's file arrives live in the stub, so close the widget it opens by itself: the test opens it through the card.
    const cards = () => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => node.dataset.card));
    await page.waitForTimeout(500);
    if ((await cards()).includes("artifacts")) { await page.getByRole("button", { name: /^Close Artifacts/ }).click(); await page.waitForTimeout(400); }

    // 1. The header: one cat, no name on screen, a halo under it.
    const header = page.locator(".chat-header");
    assert.equal(await header.locator(".tb-chathead-halo").count(), 1, "the header draws the halo");
    const catButton = header.getByRole("button", { name: BOT.name });
    assert.equal(await catButton.count(), 1, "the cat is a button named for the Bot");
    assert.equal(await header.locator(".tb-chathead-name").evaluate(node => node.getBoundingClientRect().width <= 1), true, "no name text on screen");
    const home = await rect(page, ".chat-header .tb-chathead-travel");
    near(home.w, 60, 0.5, "the header cat is 60 px (radius 30)");
    const bar = await page.evaluate(() => { const blur = document.querySelector(".tb-pblur--top"); return blur.getBoundingClientRect().height; });
    near(bar, home.y + 30 - (await page.evaluate(() => document.querySelector(".chat-pane").getBoundingClientRect().y)), 1, "the top band ends on the cat's centre line");
    assert.equal(await page.evaluate(() => +getComputedStyle(document.querySelector(".tb-chathead-halo")).opacity), 1, "the halo shows while the cat is in the header");
    // Messages scroll under the cat.
    await page.locator(".message-scroll").evaluate(node => { node.scrollTop = 400; });
    await page.waitForTimeout(300);
    if (shots) { await page.screenshot({ path: `${shots}/header-cat-${theme}@2x.png`, scale: "device" }); await page.screenshot({ path: `${shots}/header-cat-crop-${theme}@2x.png`, scale: "device", clip: { x: home.x - 200, y: 0, width: 460, height: 190 } }); }

    // 2. Open: the cat travels, the card unfolds.
    await track(page);
    await catButton.click();
    await page.waitForTimeout(700);
    const opening = await collect(page);
    const first = opening.find(frame => frame.in === "card");
    assert.ok(first, "the cat appears in the card");
    near(first.x, home.x, 1, "first frame: the cat starts on the header cat's left edge");
    near(first.y, home.y, 1, "first frame: the cat starts on the header cat's top edge");
    near(first.w, home.w, 1, "first frame: the cat starts at the header cat's size");
    const frames = opening.filter(frame => frame.in !== "none");
    const settledBox = await rect(page, ".bot-card-cat .tb-chathead-travel");
    const last = frames.at(-1);
    near(last.x, settledBox.x, 1, "last frame: the cat is at rest (x)");
    near(last.y, settledBox.y, 1, "last frame: the cat is at rest (y)");
    near(settledBox.w, 96, 0.5, "the card's cat is 96 px");
    const card = await rect(page, ".bot-card");
    near(settledBox.x + settledBox.w / 2, card.x + card.w / 2, 1, "the cat is on the card's centre line");
    near(settledBox.y + settledBox.h / 2, card.y, 1, "the cat's middle is on the card's top edge");
    const heights = opening.map(frame => frame.card).filter(height => height !== null);
    for (let i = 1; i < heights.length; i++) assert.ok(heights[i] >= heights[i - 1] - 0.01, `the card never gets shorter while it opens (frame ${i}: ${heights[i - 1]} -> ${heights[i]})`);
    assert.ok(heights.at(-1) > 100, "the card unfolds");
    assert.equal(await page.evaluate(() => +getComputedStyle(document.querySelector(".tb-chathead-halo")).opacity), 0, "the halo is gone while the cat is in the column");
    assert.equal(await page.locator(".chat-header .tb-chathead-travel").count(), 0, "the header shows no cat while it is in the column");
    assert.deepEqual((await cards())[0], "bot", "the Bot card is the top widget");
    assert.ok((await cards()).includes("artifacts"), "opening the card opens Artifacts when the Bot has files");
    if (shots) { await page.screenshot({ path: `${shots}/card-open-${theme}@2x.png`, scale: "device" }); writeFileSync(`${shots}/open-frames-${theme}.json`, JSON.stringify(opening)); }

    // 3. The card: status, menu rows, advanced link.
    const botCard = page.locator(".bot-card");
    assert.match(await botCard.locator(".bot-card-status").innerText(), /Idle|Working|Waiting/, "one status line");
    await botCard.getByRole("button", { name: /Show computer, memory and tasks/ }).click();
    await page.waitForTimeout(400);
    assert.deepEqual(await botCard.locator(".bot-card-row > span:first-child").allInnerTexts(), ["Bot computer", "Memory", "Scheduled tasks"], "three rows, no connections, skills or browser");
    if (shots) await page.screenshot({ path: `${shots}/card-menu-${theme}@2x.png`, scale: "device" });
    await botCard.getByRole("button", { name: "Hide computer, memory and tasks" }).click();
    assert.equal(await botCard.getByRole("button", { name: "Advanced settings" }).count(), 1, "the advanced settings link");

    // 4. Close: the cat flies home.
    await track(page);
    await page.getByRole("button", { name: /^Close Bot/ }).click();
    await page.waitForTimeout(700);
    const closing = await collect(page);
    const landed = await rect(page, ".chat-header .tb-chathead-travel");
    near(landed.x, home.x, 1, "closed: the cat is back on the header's box (x)");
    near(landed.y, home.y, 1, "closed: the cat is back on the header's box (y)");
    near(landed.w, home.w, 0.5, "closed: the cat is back at 60 px");
    assert.equal(await page.evaluate(() => +getComputedStyle(document.querySelector(".tb-chathead-halo")).opacity), 1, "the halo returns with the cat");
    const closeHeights = closing.map(frame => frame.card).filter(height => height !== null);
    for (let i = 1; i < closeHeights.length; i++) assert.ok(closeHeights[i] <= closeHeights[i - 1] + 0.01, "the card never gets taller while it closes");
    assert.equal(await page.locator(".bot-card-wrap").count(), 0, "the card is gone once closed");
    if (shots) writeFileSync(`${shots}/close-frames-${theme}.json`, JSON.stringify(closing));
    assert.deepEqual(errors, []);
    results.push(`${theme}: header cat, open (${opening.length} frames), card, close (${closing.length} frames)`);
    await context.close();
  }
  console.log(results.join("\n"));
} finally {
  await browser.close();
  await server.close();
}
