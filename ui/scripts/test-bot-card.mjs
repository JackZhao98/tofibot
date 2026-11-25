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
import { roleOf, withRole } from "../src/botRole.ts";

const shots = process.env.BOT_CARD_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const near = (a, b, tolerance, message) => assert.ok(Math.abs(a - b) <= tolerance, `${message}: ${a} vs ${b}`);
/** cubic-bezier(x1, y1, x2, y2) as a function of time fraction (the CSS definition, solved by bisection). */
const bezier = (x1, y1, x2, y2) => t => {
  const at = (u, a, b) => 3 * a * u * (1 - u) ** 2 + 3 * b * u * u * (1 - u) + u ** 3;
  let lo = 0, hi = 1;
  for (let i = 0; i < 40; i++) { const mid = (lo + hi) / 2; if (at(mid, x1, x2) < t) lo = mid; else hi = mid; }
  return at((lo + hi) / 2, y1, y2);
};
const ease = bezier(0.45, 0, 0.2, 1); // --ease-inout
/** Smoothness of one flight, per series (the cat's path and the card's height). From the first frame that moved to the last:
 *  - no frame may come more than two frames late (a hitch is a long frame, then a big step);
 *  - the card's height must follow the ease-in-out curve (the cat is a spring: see the cat check below) over the motion's duration: at no frame may it be more than 10% of its travel away from the curve
 *    (the start time is the best fit within a frame of the first move). A stall then a jump, or half the path in one frame, is far over that.
 *  The report also carries the plain ratio of the biggest step to the median step, and of the peak speed to the mean speed, which the curve itself
 *  puts near 5 and 2.8 (a step at the middle of an ease is several times the steps in its tails), so those two are not asserted. */
const smooth = (frames, label, duration) => {
  const problems = [];
  const live = frames.filter(frame => frame.in !== "none").filter((frame, i, all) => !i || frame.t - all[i - 1].t >= 8);
  const out = {};
  const series = { cat: live.map(frame => frame.x), card: live.map(frame => frame.card) };
  for (const [name, values] of Object.entries(series)) {
    if (values.some(value => value === null)) continue;
    const moved = values.findIndex(value => Math.abs(value - values[0]) > 0.05);
    if (moved < 0) { out[name] = null; continue; }
    const end = values.at(-1), start = values[moved - 1], travel = end - start;
    if (Math.abs(travel) < 20) continue;
    const t1 = live.findLastIndex((frame, i) => i > 0 && Math.abs(values[i] - values[i - 1]) > 0.05);
    const window = live.slice(moved - 1, t1 + 1);
    const late = Math.max(...window.slice(1).map((frame, i) => frame.t - window[i].t));
    if (late > 40) problems.push(`${label} ${name}: a frame came ${late} ms after the one before (over two frames) while the motion ran`);
    if (name === "cat") {
      // The cat travels on a spring with a slight overshoot (owner 2026-11-23): it must land where it ends, pass that place by at most 6% of the
      // travel, and never move back past its start.
      const over = Math.max(0, ...window.map((frame, i) => (values[moved - 1 + i] - end) * Math.sign(travel))) / Math.abs(travel);
      const under = Math.max(0, ...window.map((frame, i) => (start - values[moved - 1 + i]) * Math.sign(travel))) / Math.abs(travel);
      if (over > 0.06) problems.push(`${label} cat: overshoots its place by ${(over * 100).toFixed(1)}% of the travel (over 6%)`);
      if (under > 0.02) problems.push(`${label} cat: moves back past its start by ${(under * 100).toFixed(1)}% of the travel`);
      out[name] = { overshoot: +(over * 100).toFixed(1) + "%", lateMs: late };
      continue;
    }
    let best = Infinity;
    for (let t0 = live[moved].t - 30; t0 <= live[moved].t; t0 += 1) {
      const worst = Math.max(...window.map((frame, i) => Math.abs(values[moved - 1 + i] - (start + travel * ease(Math.min(1, Math.max(0, (frame.t - t0) / duration))))))) / Math.abs(travel);
      best = Math.min(best, worst);
    }
    if (best > 0.1) problems.push(`${label} ${name}: the motion is ${(best * 100).toFixed(1)}% of its travel away from the ease-in-out curve at some frame (over 10%)`);
    const steps = window.slice(1).map((frame, i) => ({ step: Math.abs(values[moved + i] - values[moved - 1 + i]), dt: frame.t - window[i].t }));
    const moving = steps.map(item => item.step).filter(step => step > 0.05).sort((a, b) => a - b), median = moving[Math.floor(moving.length / 2)];
    const mean = steps.reduce((sum, item) => sum + item.step, 0) / steps.reduce((sum, item) => sum + item.dt, 0);
    out[name] = { offCurve: +(best * 100).toFixed(1) + "%", stepOverMedian: +(Math.max(...steps.map(item => item.step)) / median).toFixed(2), speedOverMean: +(Math.max(...steps.map(item => item.step / item.dt)) / mean).toFixed(2), lateMs: late };
  }
  return { out, problems };
};
/** The machine this runs on is shared and often loaded, so one flight can lose a frame for reasons that are not the code's. A hitch the code causes comes
 *  back on every flight; so open and close each get up to three flights and pass when one of them is smooth. */
const bestOf = (label, results) => {
  const ok = results.find(item => !item.problems.length);
  assert.ok(ok, `${label}: ${results.map(item => item.problems.join("; ")).join(" | ")}`);
  return { ...ok.out, flights: results.length };
};
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
    if (shots) writeFileSync(`${shots}/open-frames-${theme}.json`, JSON.stringify(opening));
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
    const openFlight = smooth(opening, `${theme} open`, 280);
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
    if (shots) writeFileSync(`${shots}/close-frames-${theme}.json`, JSON.stringify(closing));
    const landed = await rect(page, ".chat-header .tb-chathead-travel");
    near(landed.x, home.x, 1, "closed: the cat is back on the header's box (x)");
    near(landed.y, home.y, 1, "closed: the cat is back on the header's box (y)");
    near(landed.w, home.w, 0.5, "closed: the cat is back at 60 px");
    assert.equal(await page.evaluate(() => +getComputedStyle(document.querySelector(".tb-chathead-halo")).opacity), 1, "the halo returns with the cat");
    const closeFlight = smooth(closing, `${theme} close`, 180);
    const closeHeights = closing.map(frame => frame.card).filter(height => height !== null);
    for (let i = 1; i < closeHeights.length; i++) assert.ok(closeHeights[i] <= closeHeights[i - 1] + 0.01, "the card never gets taller while it closes");
    assert.equal(await page.locator(".bot-card-wrap").count(), 0, "the card is gone once closed");
    const attempts = [{ open: openFlight, close: closeFlight }];
    while (attempts.length < 3 && (attempts.every(item => item.open.problems.length) || attempts.every(item => item.close.problems.length))) {
      await page.waitForTimeout(400);
      await track(page); await catButton.click(); await page.waitForTimeout(700); const o = await collect(page);
      await track(page); await page.getByRole("button", { name: /^Close Bot/ }).click(); await page.waitForTimeout(700); const c = await collect(page);
      attempts.push({ open: smooth(o, `${theme} open`, 280), close: smooth(c, `${theme} close`, 180) });
    }
    const openRatio = bestOf(`${theme} open`, attempts.map(item => item.open));
    const closeRatio = bestOf(`${theme} close`, attempts.map(item => item.close));
    if (shots) writeFileSync(`${shots}/close-frames-${theme}.json`, JSON.stringify(closing));
    assert.deepEqual(errors, []);
    results.push(`${theme}: header cat, open (${opening.length} frames, max/median step ${JSON.stringify(openRatio)}), card, close (${closing.length} frames, ${JSON.stringify(closeRatio)})`);
    await context.close();

    // Editing in place: Escape cancels, an IME confirmation does not save, nothing is sent for either.
    {
      const edit = newState();
      const { context: ctx, page: p2 } = await openWorkspace(browser, origin, edit, { theme });
      const writes = [];
      p2.on("request", request => { if (request.method() !== "GET" && /\/api\/bots\//.test(request.url())) writes.push(request.method()); });
      await p2.locator(".chat-header .tb-chathead-cat").click();
      await p2.locator(".bot-card").waitFor();
      await p2.waitForTimeout(500);
      const name = p2.getByRole("textbox", { name: "Bot name" });
      await name.fill("Zed");
      await name.press("Escape");
      assert.equal(await name.inputValue(), BOT.name, "Escape puts the old name back");
      await p2.waitForTimeout(200);
      assert.deepEqual(writes, [], "Escape saves nothing");
      await name.focus();
      await name.fill("Zed");
      await name.evaluate(input => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true })));
      assert.equal(await name.evaluate(input => document.activeElement === input), true, "Enter that confirms an IME candidate does not leave the field");
      await p2.waitForTimeout(200);
      assert.deepEqual(writes, [], "an IME confirmation saves nothing");
      await ctx.close();
    }
  }

  // The role is the first line of the instructions in the card and in Bot settings; clearing it removes the line and keeps the rest exactly.
  assert.equal(roleOf("Keeps books\nLine two\n\nLine four"), "Keeps books");
  assert.equal(roleOf("\n\n  Keeps books  \nLine two"), "Keeps books");
  assert.equal(withRole("Keeps books\nLine two\n\nLine four", ""), "Line two\n\nLine four");
  assert.equal(roleOf(withRole("Keeps books\nLine two", "")), "Line two");
  assert.equal(withRole("Keeps books\nLine two", "Does taxes"), "Does taxes\nLine two");
  assert.equal(withRole("Keeps books", ""), "");
  assert.equal(withRole("", "Does taxes"), "Does taxes");

  // A phone: the tap opens the Bot settings cover, and the cat and its halo are not drawn over it.
  for (const theme of ["light", "dark"]) {
    const { context, page, errors } = await openWorkspace(browser, origin, newState(), { width: 390, height: 844, theme, scale: shots ? 2 : 1 });
    const cat = page.locator(".chat-header .tb-chathead-cat");
    near((await rect(page, ".chat-header .tb-chathead-travel")).w, 60, 0.5, "the phone's header cat is 60 px");
    assert.equal(await page.locator(".tb-chathead-halo").count(), 1, "the phone's header has the halo");
    if (shots) await page.screenshot({ path: `${shots}/narrow-header-${theme}@2x.png`, scale: "device" });
    await cat.click();
    await page.locator(".cover-window .bot-settings").waitFor();
    await page.waitForTimeout(700);
    assert.equal(await page.locator(".bot-card").count(), 0, "a phone opens no Bot card");
    const covered = await page.evaluate(() => {
      const box = document.querySelector(".chat-header .tb-chathead-travel")?.getBoundingClientRect();
      const halo = document.querySelector(".tb-chathead-halo")?.getBoundingClientRect();
      const top = (x, y) => document.elementFromPoint(x, y)?.closest(".cover-window") !== null;
      return [box && top(box.x + box.width / 2, box.y + box.height / 2), halo && top(halo.x + halo.width / 2, halo.y + halo.height * 0.75)];
    });
    assert.deepEqual(covered, [true, true], "the cover is on top of the cat and the halo");
    if (shots) await page.screenshot({ path: `${shots}/narrow-${theme}@2x.png`, scale: "device" });
    assert.deepEqual(errors, []);
    await context.close();
  }

  // A group chat is unchanged: the name shows, no halo, and the cat opens the group settings (no Bot card).
  {
    const { context, page, errors } = await openWorkspace(browser, origin, newState(), { group: true });
    assert.equal(await page.locator(".chat-header .tb-chathead-halo").count(), 0, "a group header has no halo");
    assert.equal(await page.locator(".chat-header .tb-chathead-name").evaluate(node => node.getBoundingClientRect().width > 20), true, "a group header shows its name");
    await page.locator(".chat-header .tb-chathead-cat").click();
    await page.locator(".cover-window").waitFor();
    assert.equal(await page.locator(".bot-card").count(), 0, "a group opens no Bot card");
    assert.equal(await page.locator(".widget-column [data-card]").count(), 0, "and no widget");
    assert.deepEqual(errors, []);
    await context.close();
  }
  console.log(results.join("\n"));
} finally {
  await browser.close();
  await server.close();
}
