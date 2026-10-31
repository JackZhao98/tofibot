// iPad (touch, 1194 x 834, widget column): the big computer is always on top and never leaves a blocking layer behind.
// A fixed window inside the widget column (scroll container, rise animation, overflow: clip card, drag filter) painted nothing in WebKit on iPad
// while its scrim still took every tap, so FloatingDesktop moves the shell under `.workspace` while it is big. Run in DMs and groups; enlarge by the
// header-less widget button and by tapping the screen; shrink and close by tap.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:ipad-computer   (SURFACES_ENGINE=webkit [TOFI_TEST_WEBKIT=<executable>] for WebKit)
import assert from "node:assert/strict";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { newState, openWorkspace } from "./widgets-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const settled = page => page.waitForTimeout(500);

try {

  //: the big computer is always on top and never leaves a blocking layer behind. A fixed window inside the
  //    column's scroll container / animated slot painted nothing in WebKit on iPad while its scrim still took every tap, so the shell must be a child
  //    of the workspace while big. Run in DMs and groups; enlarge by the button and by tapping the screen; shrink and close by tap.
  for (const group of [false, true]) for (const how of ["button", "screen"]) {
    const label = `${group ? "group" : "DM"} / ${how}`;
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state, { width: 1194, height: 834, group, touch: true });
    const hit = (x, y) => page.evaluate(([px, py]) => { const el = document.elementFromPoint(px, py); const shell = document.querySelector(".desktop-floating-shell.is-expanded"); return { inside: Boolean(shell && el && shell.contains(el)), chat: Boolean(el?.closest(".chat-pane, .message-scroll, .composer")), tag: el?.className?.toString().slice(0, 40) ?? el?.tagName }; }, [x, y]);
    await page.locator("[data-computer-toggle]").tap();
    await page.locator(".desktop-floating-shell.is-docked.is-small").waitFor();
    await settled(page);
    await page.locator("[data-desktop-frame] video, [data-desktop-frame] img, [data-desktop-frame] canvas").first().evaluate(node => { window.__screenNode = node; }).catch(() => {});
    assert.equal((await hit(300, 300)).chat, true, `${label}: compact, the chat takes taps`);
    if (how === "button") await page.getByRole("button", { name: "Enlarge Bot computer" }).tap();
    else await page.locator('[data-card="computer"] .desktop-enlarge').first().tap();
    await page.locator(".desktop-floating-shell.is-expanded").waitFor({ timeout: 4000 });
    await settled(page);
    const big = await page.locator(".computer-detail.is-expanded").evaluate(node => {
      const box = node.getBoundingClientRect(), style = getComputedStyle(node), shell = node.closest(".desktop-floating-shell");
      const chain = []; for (let el = shell.parentElement; el; el = el.parentElement) { const cs = getComputedStyle(el); if (cs.transform !== "none" || cs.translate !== "none" || cs.filter !== "none" || cs.clipPath !== "none" || cs.contain !== "none" || cs.willChange !== "auto" || /auto|scroll|clip/.test(cs.overflowX + cs.overflowY)) chain.push(el.className.toString().slice(0, 40) || el.tagName); }
      return { x: box.x, y: box.y, w: box.width, h: box.height, visibility: style.visibility, opacity: Number(style.opacity), shellOpacity: Number(getComputedStyle(shell).opacity), inColumn: Boolean(node.closest(".widget-column")), chain, vw: innerWidth, vh: innerHeight };
    });
    assert.ok(big.w > 300 && big.h > 300 && big.x >= 0 && big.y >= 0 && big.x + big.w <= big.vw + 1 && big.y + big.h <= big.vh + 1, `${label}: the big window is inside the viewport ${JSON.stringify(big)}`);
    assert.ok(big.visibility === "visible" && big.opacity === 1 && big.shellOpacity === 1, `${label}: the big window is visible ${JSON.stringify(big)}`);
    assert.equal(big.inColumn, false, `${label}: the big window is not inside the widget column`);
    assert.deepEqual(big.chain, [], `${label}: no clipping, scrolling or transformed ancestor above the big window`);
    assert.equal((await hit(big.vw / 2, big.vh / 2)).inside, true, `${label}: the screen center is the big window`);
    assert.equal((await hit(big.vw - 8, 8)).inside, true, `${label}: the scrim takes taps outside the window`);
    if (await page.evaluate(() => Boolean(window.__screenNode))) assert.equal(await page.evaluate(() => window.__screenNode.isConnected), true, `${label}: the live screen element survived the enlarge`);
    // Shrink by tap: back to the compact widget, nothing blocking.
    await page.getByRole("button", { name: "Shrink Bot computer" }).tap();
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell.is-expanded"), null, { timeout: 4000 });
    await settled(page);
    assert.equal(await page.locator(".widget-column .desktop-floating-shell.is-docked.is-small").count(), 1, `${label}: shrinking returns the Bot computer to its widget`);
    assert.equal((await hit(300, 300)).chat, true, `${label}: after shrinking, the chat takes taps`);
    if (await page.evaluate(() => Boolean(window.__screenNode))) assert.equal(await page.evaluate(() => window.__screenNode.isConnected), true, `${label}: the live screen element survived the shrink`);
    // Enlarge again and close from the big window: the widget goes, nothing is left over the chat.
    await page.getByRole("button", { name: "Enlarge Bot computer" }).tap();
    await page.locator(".desktop-floating-shell.is-expanded").waitFor({ timeout: 4000 });
    await settled(page);
    await page.getByRole("button", { name: "Close Bot computer" }).first().tap();
    await page.waitForFunction(() => !document.querySelector(".desktop-floating-shell, .desktop-host"), null, { timeout: 4000 });
    await settled(page);
    assert.equal((await hit(300, 300)).chat, true, `${label}: after closing, the chat takes taps`);
    assert.equal(await page.evaluate(() => Boolean(document.querySelector(".desktop-host"))), false, `${label}: no host left behind`);
    assert.deepEqual(errors, []);
    results.push(`ipad touch (${label}): enlarge puts the window in the viewport above the chat (not under the column), shrink and close leave nothing blocking`);
    await context.close();
  }
  console.log(results.map(line => `PASS ${line}`).join("\n"));
  console.log("ipad computer: PASS");
} finally {
  await browser.close();
  await server.close();
}
