// Menus and popovers (docs/design/migration.md step 2): one host, one set of keyboard and dismissal rules, Radix only in a
// lazy chunk. Production bundle, synthetic data, real browser.
//  1. Bundle: the entry stays within the shell budget, Radix is only in the RadixOverlays chunk, which the entry loads dynamically.
//  2. Every menu (new, more, account, attachments, Settings > Connections: a tool's permission menu on the connection page, sidebar row): mouse open, Enter / Space /
//     ArrowDown open with focus on the first item, arrows and Home/End move, Escape closes and returns focus to the trigger,
//     Tab closes and moves on from the trigger, a press outside closes, pressing the trigger again closes; the surface sits in
//     #tofi-layers above everything at its centre.
//  3. Sidebar row menu: right click at the pointer, Shift+F10 with focus on the first item and back to the row on Escape (also
//     after the Delete dialog it opens), a click
//     on another conversation closes the menu and still opens that conversation, the menu flips/shifts inside the viewport at the
//     corners (390 and 1280 px), a 450 ms touch press opens it at 390 px (the drag engine's 380 ms, not Radix's 700 ms), a short
//     tap does not.
//  4. Layers: a hint shows above an open menu; the @ menu and the attachment menu are no longer capped by the composer and the
//     account menu not by the sidebar (Z3); at >= 1100 px the header is back at --z-chrome and its menu is above the context
//     card (Z4); a menu opened inside Settings stacks above Settings.
//  5. Popovers: the reaction menu takes focus, Escape closes it and focus returns; a group's read-by names are a quiet hint.
//  6. Cost: warm click-to-visible <= 100 ms; with the chunk held back 800 ms the first click still opens the menu once it lands;
//     opening the largest menu at 4x CPU has no frame over 50 ms.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:overlays
// OVERLAYS_ENGINE=webkit runs the browser checks in WebKit. OVERLAYS_DIST=/path/to/dist measures an existing build.
// OVERLAYS_ONLY=<substring> runs matching checks only.
import assert from "node:assert/strict";
import zlib from "node:zlib";
import { readdirSync, readFileSync } from "node:fs";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createOverlayFixture } from "./lib/overlay-fixture.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const engineName = process.env.OVERLAYS_ENGINE === "webkit" ? "webkit" : "chromium";
const fx = await createOverlayFixture({ engineName, dist: process.env.OVERLAYS_DIST });
const only = process.env.OVERLAYS_ONLY;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let failures = 0;
// TOFI_SKIP_TIMING=1 (CI): leave out the checks that read a clock or count writes per time window; shared runners are too noisy for them. Run them locally.
const timingCheck = name => process.env.TOFI_SKIP_TIMING === "1" && /the cats hold still|^cost:/.test(name);
async function check(name, fn) {
  if (only && !name.includes(only)) return;
  if (timingCheck(name)) { console.log(`skipped (timing check, CI mode) - ${name}`); return; }
  try { await fn(); console.log(`ok - ${name}`); }
  catch (error) { failures++; console.log(`FAIL - ${name}\n  ${String(error.stack || error).split("\n").slice(0, 4).join("\n  ")}`); }
}

// ---- 1. bundle ----
await check("bundle: entry within budget, Radix only in the lazy RadixOverlays chunk", async () => {
  const html = readFileSync(join(fx.outDir, "index.html"), "utf8");
  const eager = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map(match => match[1]);
  const gzip = eager.reduce((sum, file) => sum + zlib.gzipSync(readFileSync(join(fx.outDir, file)), { level: 9 }).length, 0);
  const budget = Math.floor(394_757 * 0.8); // same budget as test-shell-budget.mjs
  console.log(`  entry JS: ${eager.length} files, ${gzip} bytes gzip (budget ${budget})`);
  assert.ok(gzip <= budget, `entry ${gzip} > ${budget}`);
  const entryText = eager.map(file => readFileSync(join(fx.outDir, file), "utf8")).join("\n");
  const lazy = entryText.match(/assets\/RadixOverlays-[\w-]+\.js/);
  assert.ok(lazy, "the entry loads RadixOverlays dynamically");
  assert.ok(!eager.some(file => file.includes("/RadixOverlays-")), "RadixOverlays is not an entry file");
  for (const marker of ["data-radix-menu-content", "data-radix-popper-content-wrapper", "DismissableLayer"]) assert.ok(!entryText.includes(marker), `no Radix code (${marker}) in the entry`);
  const chunk = readFileSync(join(fx.outDir, lazy[0]), "utf8");
  // The popper (positioning) is shared with the lazy Select chunk, so Rollup may hoist it into a common non-entry chunk.
  const lazyText = [chunk, ...readdirSync(join(fx.outDir, "assets")).filter(name => name.endsWith(".js") && !eager.includes(`assets/${name}`)).map(name => readFileSync(join(fx.outDir, "assets", name), "utf8"))].join("\n");
  assert.ok(chunk.includes("data-radix-menu-content") && lazyText.includes("data-radix-popper-content-wrapper"), "Radix menu lives in RadixOverlays, the popper in a lazy chunk");
  console.log(`  RadixOverlays chunk: ${zlib.gzipSync(Buffer.from(chunk), { level: 9 }).length} bytes gzip`);
});

// ---- helpers ----
const state = page => page.evaluate(() => {
  const a = document.activeElement;
  return { tag: a?.tagName, text: a?.textContent?.trim() ?? "", role: a?.getAttribute("role") ?? "", inLayers: Boolean(a?.closest("#tofi-layers")), body: a === document.body };
});
const items = (page, menu) => page.locator(`${menu} [role^=menuitem]:not([data-disabled])`);
async function closed(page, menu) { await page.waitForFunction(selector => !document.querySelector(selector), menu, { timeout: 2000 }); }
// Radix moves focus between items in a timeout after the key: read focus once it has settled.
async function press(page, key) { await page.keyboard.press(key); await sleep(50); }
/** Focus comes back to `selector` (Radix restores it in a timeout after the surface unmounts). */
async function focusBack(page, selector, message) {
  const ok = await page.waitForFunction(s => document.activeElement === document.querySelector(s), selector, { timeout: 1500 }).then(() => true, () => false);
  assert.ok(ok, `${message} (focus is on ${JSON.stringify(await state(page))})`);
}
/** The menu is visible, portaled into #tofi-layers, inside the viewport, and nothing covers its centre. */
async function onTop(page, menu) {
  await page.locator(menu).waitFor({ timeout: 4000 });
  await sleep(260);
  const facts = await page.evaluate(selector => {
    const element = document.querySelector(selector), r = element.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + Math.min(r.height / 2, 24));
    return { layers: Boolean(element.closest("#tofi-layers")), hit: Boolean(hit && element.contains(hit)), hitName: hit ? `${hit.tagName}.${hit.className}` : "none",
      inside: r.left >= 0 && r.top >= 0 && r.right <= innerWidth + 0.5 && r.bottom <= innerHeight + 0.5, rect: [r.left, r.top, r.right, r.bottom].map(Math.round) };
  }, menu);
  assert.ok(facts.layers, `${menu} is portaled into #tofi-layers`);
  assert.ok(facts.hit, `${menu} is on top at its centre (got ${facts.hitName})`);
  assert.ok(facts.inside, `${menu} stays inside the viewport ${JSON.stringify(facts.rect)}`);
  return facts;
}
async function settingsMcp(page) {
  await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-tool-settings")));
  // A row opens its connection's page; the "more actions" menu lives in that page's header.
  await page.locator(".mcp-service-open").first().click({ timeout: 10000 });
  await page.locator(".mcp-tier-trigger").first().waitFor({ timeout: 10000 });
  await sleep(400);
}

// Where each menu sat before Radix (its old CSS offsets), as [anchor selector, edge relations]: the look must not move.
const MENUS = [
  { name: "new", trigger: ".create-menu-anchor > button", menu: ".create-menu", place: [".create-menu-anchor", (m, a) => [m.right - a.right, m.top - (a.bottom + 8)]] },
  { name: "more", trigger: ".header-more > button", menu: ".more-menu", place: [".chat-header", (m, a) => [m.right - (a.right - 14), m.top - (a.bottom + 8)]] },
  { name: "account", trigger: ".sidebar-account-identity", menu: ".sidebar-account-menu", place: [".sidebar-account", (m, a) => [m.left - a.left, m.bottom - (a.top - 10)]] },
  { name: "attachments", trigger: ".composer-add-wrap > button", menu: ".composer-add-menu", place: [".composer-add-wrap", (m, a) => [m.left - a.left, m.bottom - (a.top - 12)]] },
  { name: "MCP tool permission", trigger: ".mcp-tier-trigger", menu: ".mcp-tier-menu", setup: settingsMcp, place: [".mcp-tier-anchor", (m, a) => [m.right - a.right, m.top - (a.bottom + 6)]] },
];
const COLLAPSED = [
  { name: "new", trigger: ".create-menu-anchor > button", menu: ".create-menu", place: [".create-menu-anchor", (m, a) => [m.left - (a.right + 12), m.top - a.top]] },
  { name: "account", trigger: ".sidebar-account-identity", menu: ".sidebar-account-menu", place: [".sidebar-account", (m, a) => [m.left - (a.right + 10), m.bottom - a.bottom]] },
];
async function placed(page, spec) {
  const boxes = await page.evaluate(([menu, anchor]) => [document.querySelector(menu), document.querySelector(anchor)].map(e => e.getBoundingClientRect().toJSON()), [spec.menu, spec.place[0]]);
  const off = spec.place[1](...boxes);
  assert.ok(off.every(v => Math.abs(v) <= 1), `${spec.name} menu sits where it did (edge offsets ${off.map(v => v.toFixed(1)).join(", ")} px)`);
}

// ---- 2. every menu ----
for (const spec of MENUS) {
  await check(`${spec.name} menu: mouse, keyboard, Escape, Tab, outside press, toggle`, async () => {
    const { page, context, errors } = await fx.open({});
    if (spec.setup) await spec.setup(page);
    const trigger = page.locator(spec.trigger).first();
    // Mouse.
    await trigger.click();
    await onTop(page, spec.menu);
    await placed(page, spec);
    assert.equal(await trigger.getAttribute("aria-expanded"), "true", "the trigger says it is expanded");
    assert.equal(await page.locator(spec.menu).getAttribute("role"), "menu");
    assert.equal((await state(page)).role, "menu", "a pointer open focuses the menu itself (arrows work, no item ring)");
    await trigger.click();
    await closed(page, spec.menu);
    // Keyboard: Enter, Space and ArrowDown each open with focus on the first item.
    for (const key of ["Enter", " ", "ArrowDown"]) {
      await trigger.focus();
      await page.keyboard.press(key === " " ? "Space" : key);
      await page.locator(spec.menu).waitFor({ timeout: 4000 });
      await sleep(60);
      const first = (await items(page, spec.menu).first().textContent()).trim();
      assert.equal((await state(page)).text, first, `${JSON.stringify(key)} opens with focus on the first item`);
      await page.keyboard.press("Escape");
      await closed(page, spec.menu);
      await focusBack(page, spec.trigger, `Escape after ${JSON.stringify(key)} returns focus to the trigger`);
    }
    // Arrows, Home, End.
    await trigger.focus();
    await page.keyboard.press("Enter");
    await page.locator(spec.menu).waitFor();
    await sleep(60);
    const labels = (await items(page, spec.menu).allTextContents()).map(text => text.trim());
    assert.ok(labels.length >= 2, `${spec.name} has items`);
    await press(page, "ArrowDown");
    assert.equal((await state(page)).text, labels[1], "ArrowDown moves to the next item");
    await press(page, "End");
    assert.equal((await state(page)).text, labels.at(-1), "End moves to the last item");
    await press(page, "Home");
    assert.equal((await state(page)).text, labels[0], "Home moves to the first item");
    await press(page, "ArrowUp");
    assert.ok([labels[0], labels.at(-1)].includes((await state(page)).text), "ArrowUp stays in the menu");
    // Tab closes and moves on from the trigger, never into the void.
    await press(page, "Tab");
    await closed(page, spec.menu);
    await sleep(60);
    const after = await state(page);
    assert.ok(!after.body && !after.inLayers, `Tab leaves the menu for the page (focus on ${after.tag} "${after.text}")`);
    assert.equal(await page.evaluate(selector => { const t = document.querySelector(selector); return Boolean(t.compareDocumentPosition(document.activeElement) & Node.DOCUMENT_POSITION_FOLLOWING) || t === document.activeElement; }, spec.trigger), true, "Tab lands after the trigger");
    // A press outside closes it.
    await trigger.click();
    await page.locator(spec.menu).waitFor();
    await sleep(200);
    await page.mouse.click(640, 300);
    await closed(page, spec.menu);
    assert.deepEqual(errors, [], "no page errors");
    await context.close();
  });
}

await check("reduced motion: menus open and close at once", async () => {
  const { page, context } = await fx.open({ reduced: true });
  for (const [trigger, menu] of [[".create-menu-anchor > button", ".create-menu"], [".header-more > button", ".more-menu"]]) {
    await page.locator(trigger).click();
    await page.locator(menu).waitFor();
    // Reduced motion means the menu's own animation and transitions take no time. Read the durations instead of
    // sampling opacity at a moment: on a loaded runner the document timeline may not have ticked yet.
    const seen = await page.evaluate(selector => {
      const el = document.querySelector(selector); const cs = getComputedStyle(el);
      const seconds = value => Math.max(...value.split(",").map(part => part.trim().endsWith("ms") ? parseFloat(part) / 1000 : parseFloat(part) || 0));
      return { animation: seconds(cs.animationDuration), transition: seconds(cs.transitionDuration), name: cs.animationName };
    }, menu);
    assert.ok(seen.animation <= 0.001 && seen.transition <= 0.001, `${menu} has no motion under reduced motion (${JSON.stringify(seen)})`);
    await page.waitForFunction(selector => getComputedStyle(document.querySelector(selector)).opacity === "1", menu, { timeout: 2000 });
    const t0 = Date.now();
    await page.keyboard.press("Escape");
    await closed(page, menu);
    assert.ok(Date.now() - t0 < 300, `${menu} is gone at once (${Date.now() - t0} ms)`);
  }
  await context.close();
});

await check("collapsed sidebar: the new and account menus open beside the rail, where they did", async () => {
  const { page, context } = await fx.open({});
  await page.locator(".sidebar-toggle").click();
  await sleep(500);
  for (const spec of COLLAPSED) {
    await page.locator(spec.trigger).click();
    await onTop(page, spec.menu);
    await placed(page, spec);
    await page.keyboard.press("Escape");
    await closed(page, spec.menu);
  }
  await context.close();
});

await check("account menu: the theme choices are radio items that keep the menu open", async () => {
  const { page, context } = await fx.open({});
  await page.locator(".sidebar-account-identity").click();
  await onTop(page, ".sidebar-account-menu");
  const dark = page.locator(".sidebar-account-menu [role=menuitemradio]").filter({ hasText: "Dark" });
  await dark.click();
  await sleep(300);
  assert.equal(await page.locator(".sidebar-account-menu").count(), 1, "choosing a theme keeps the menu open");
  assert.equal(await dark.getAttribute("aria-checked"), "true");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  await context.close();
});

await check("account menu: Language is a submenu of radio items; ArrowRight opens, ArrowLeft and Escape return to the item", async () => {
  const { page, context } = await fx.open({});
  await page.locator(".sidebar-account-identity").click();
  await onTop(page, ".sidebar-account-menu");
  const trigger = page.locator(".sidebar-account-menu .menu-subtrigger");
  await trigger.focus();
  await page.keyboard.press("ArrowRight");
  await sleep(200);
  const sub = page.locator(".sidebar-account-submenu");
  assert.equal(await sub.count(), 1, "ArrowRight opens the submenu");
  const items = sub.locator("[role=menuitemradio]");
  assert.ok(await items.count() >= 2, "Auto and every language are listed");
  assert.equal(await sub.locator("[role=menuitemradio][aria-checked=true]").count(), 1, "exactly one choice is marked");
  assert.ok(await items.filter({ hasText: "English" }).count() === 1, "languages carry their own names");
  await page.keyboard.press("ArrowLeft");
  await sleep(200);
  assert.equal(await sub.count(), 0, "ArrowLeft closes the submenu");
  assert.equal(await page.evaluate(() => document.activeElement?.classList.contains("menu-subtrigger")), true, "focus returns to the Language item");
  await page.keyboard.press("ArrowRight"); await sleep(200);
  await page.keyboard.press("Escape"); await sleep(200);
  assert.equal(await sub.count(), 0, "Escape closes the submenu");
  assert.equal(await page.locator(".sidebar-account-menu").count(), 1, "Escape in the submenu leaves the parent menu open");
  await context.close();
});

await check("MCP menu inside Settings: stacks above Settings, Escape closes only the menu", async () => {
  const { page, context } = await fx.open({});
  await settingsMcp(page);
  await page.locator(".mcp-menu-trigger").first().click();
  await onTop(page, ".mcp-menu");
  const z = await page.evaluate(() => getComputedStyle(document.querySelector(".mcp-menu").parentElement).zIndex);
  assert.equal(z, "81", "the menu's layer is --z-dialog + 1 (Settings is --z-dialog)");
  await page.keyboard.press("Escape");
  await closed(page, ".mcp-menu");
  await sleep(300);
  assert.ok(await page.locator(".settings-tabs").count() > 0, "Settings is still open after the menu's Escape");
  await context.close();
});

// ---- 3. sidebar row menu ----
const row = (page, name) => page.locator(`.conversation-row[aria-label^="${name}"]`).first();
await check("sidebar row menu: right click at the pointer, Shift+F10, Escape back to the row, a click elsewhere still lands", async () => {
  const { page, context, errors } = await fx.open({});
  const box = await row(page, "Simile Cat").boundingBox();
  await page.mouse.click(box.x + 40, box.y + 20, { button: "right" });
  const facts = await onTop(page, ".sidebar-context-menu");
  assert.ok(Math.abs(facts.rect[0] - (box.x + 40)) <= 2 && Math.abs(facts.rect[1] - (box.y + 20)) <= 2, `the menu opens at the pointer (${facts.rect} vs ${box.x + 40},${box.y + 20})`);
  // A release on an item that was not pressed there (Radix's press-drag-release, or a right button let go over the menu)
  // chooses nothing: the menu stays and nothing is pinned.
  await page.evaluate(() => document.querySelector(".sidebar-context-menu [role=menuitem]").dispatchEvent(new PointerEvent("pointerup", { bubbles: true, cancelable: true, pointerType: "mouse", button: 2 })));
  await sleep(200);
  assert.equal(await page.locator(".sidebar-context-menu").count(), 1, "a release that did not start on the item chooses nothing");
  // A click on another conversation closes the menu and still opens that conversation (on its avatar, beside the menu).
  const other = await row(page, "Ops Room").boundingBox();
  await page.mouse.click(other.x + 14, other.y + other.height / 2);
  await closed(page, ".sidebar-context-menu");
  await page.waitForFunction(() => location.pathname === "/g/grp", null, { timeout: 3000 });
  // Keyboard.
  await row(page, "Simile Cat").focus();
  await page.keyboard.press("Shift+F10");
  await page.locator(".sidebar-context-menu").waitFor();
  await sleep(60);
  assert.equal((await state(page)).text, "Pin", "the keyboard open focuses the first item");
  await press(page, "ArrowDown");
  assert.equal((await state(page)).text, "Move to new section");
  await page.keyboard.press("Escape");
  await closed(page, ".sidebar-context-menu");
  await focusBack(page, '.conversation-row[aria-label^="Simile Cat"]', "Escape returns focus to the row");
  assert.deepEqual(errors, []);
  await context.close();
});

// Steps 2 + 3 together (ds/integrate): a dialog opened from the sidebar row menu gives focus back to the row the menu belongs
// to. The menu item that had focus is gone with the menu, and the workspace turns inert in the same commit as the dialog.
await check("sidebar row menu > Delete: the dialog takes focus, Escape returns it to the row, not to <body>", async () => {
  const { page, context, errors } = await fx.open({});
  await row(page, "Simile Cat").focus();
  await page.keyboard.press("Shift+F10");
  await page.locator(".sidebar-context-menu").waitFor();
  await sleep(60);
  await press(page, "End");
  assert.match((await state(page)).text, /Delete/, "End reaches the Delete item");
  await page.keyboard.press("Enter");
  await page.locator(".delete-dialog").waitFor({ timeout: 4000 });
  await closed(page, ".sidebar-context-menu");
  await page.waitForFunction(() => document.querySelector(".delete-dialog")?.contains(document.activeElement), null, { timeout: 2000 });
  await page.keyboard.press("Escape");
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  await focusBack(page, '.conversation-row[aria-label^="Simile Cat"]', "closing the dialog returns focus to the row the menu was opened on");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("sidebar row menu: opening and closing it renders the menu only, not the workspace around it; the cats hold still while it opens", async () => {
  const { page, context } = await fx.open({ renders: true });
  await sleep(600);
  // The probe sees the workspace render at all (a conversation switch does).
  const before = await page.evaluate(() => window.__workspaceRenders);
  await row(page, "Simile Cat").click();
  await page.waitForFunction(n => window.__workspaceRenders > n, before, { timeout: 3000 });
  await sleep(800);
  const settled = await page.evaluate(() => window.__workspaceRenders);
  // The cats' idle life writes attributes on their SVGs every few frames; a menu opening holds it (holdAvatarLife).
  await page.evaluate(() => { window.__catWrites = 0; window.__catObserver = new MutationObserver(list => { window.__catWrites += list.length; }); for (const svg of document.querySelectorAll(".avatar svg")) window.__catObserver.observe(svg, { attributes: true, subtree: true }); });
  await sleep(400);
  const idleWrites = await page.evaluate(() => { const n = window.__catWrites; window.__catWrites = 0; return n; });
  const box = await row(page, "Ledger Cat").boundingBox();
  await page.mouse.click(box.x + 40, box.y + 20, { button: "right" });
  await page.locator(".sidebar-context-menu").waitFor();
  await sleep(250);
  const openWrites = await page.evaluate(() => window.__catWrites);
  await page.keyboard.press("Escape");
  await closed(page, ".sidebar-context-menu");
  await sleep(300);
  const after = await page.evaluate(() => window.__workspaceRenders);
  console.log(`  workspace renders while the row menu opened and closed: ${after - settled}; cat attribute writes: ${idleWrites} in 400 ms idle, ${openWrites} in the 250 ms after the open`);
  assert.equal(after - settled, 0, "the workspace did not render for the row menu");
  assert.ok(idleWrites >= 5, `the cats animate when idle (${idleWrites} writes), so the hold is observable`);
  // The frame or two before the menu commits may still animate (WebKit delivers the press a frame later than Chromium).
  const expected = idleWrites * 250 / 400;
  assert.ok(openWrites <= expected * 0.2, `the cats hold still while the menu opens (${openWrites} writes; ${Math.round(expected)} when not held)`);
  await context.close();
});

for (const [width, height] of [[1280, 860], [390, 844]]) {
  await check(`sidebar row menu stays inside the viewport at the corners (${width} px)`, async () => {
    const { page, context } = await fx.open({ width, height, path: "/", waitFor: ".conversation-row" });
    for (const [x, y] of [[width - 4, height - 4], [width - 4, 4], [4, height - 4]]) {
      await page.evaluate(([x, y]) => document.querySelector('.conversation-row[aria-label^="Simile Cat"]').dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 2 })), [x, y]);
      await onTop(page, ".sidebar-context-menu");
      await page.keyboard.press("Escape");
      await closed(page, ".sidebar-context-menu");
    }
    await context.close();
  });
}

await check("sidebar row menu: a 450 ms touch press opens it at 390 px, a short tap opens the chat instead", async () => {
  if (engineName !== "chromium") return console.log("  (touch through CDP: Chromium only)");
  // The drag engine is a lazy chunk loaded in idle time. A slow machine (a shared CI runner) loads it late; hold it back 2.5 s so
  // that is the test's every run, not a coin flip. The press must wait for the engine to be there, not for a guessed time.
  fx.delays.pattern = /sidebarDndEngine-[\w-]+\.js$/; fx.delays.ms = 2500;
  let opened;
  try { opened = await fx.open({ width: 390, height: 844, path: "/", waitFor: ".conversation-row", touch: true }); } finally { fx.delays.pattern = null; }
  const { page, context } = opened;
  const cdp = await context.newCDPSession(page);
  const touchAt = async (type, x, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x, y, id: 1 }] });
  await page.locator("[data-dnd-ready]").waitFor({ state: "attached", timeout: 30000 }); // the engine marks the list when it listens
  const b = await row(page, "Simile Cat").boundingBox();
  await touchAt("touchStart", b.x + b.width / 2, b.y + b.height / 2);
  await sleep(450);
  await touchAt("touchEnd");
  await page.locator(".sidebar-context-menu").waitFor({ timeout: 3000 });
  await sleep(500);
  assert.equal(await page.locator(".sidebar-context-menu").count(), 1, "the release did not choose an item or close the menu");
  await page.keyboard.press("Escape");
  await closed(page, ".sidebar-context-menu");
  await sleep(500);
  const c = await row(page, "Ops Room").boundingBox();
  await touchAt("touchStart", c.x + c.width / 2, c.y + c.height / 2);
  await sleep(120);
  await touchAt("touchEnd");
  await page.waitForFunction(() => location.pathname === "/g/grp", null, { timeout: 3000 });
  assert.equal(await page.locator(".sidebar-context-menu").count(), 0, "a tap opens no menu");
  await context.close();
});

// ---- 4. layers ----
await check("a hint shows above an open menu", async () => {
  const { page, context } = await fx.open({});
  await page.locator(".header-more > button").click();
  await onTop(page, ".more-menu");
  await page.evaluate(() => document.querySelectorAll(".more-menu [role=menuitem]")[2].setAttribute("data-hint", "Synthetic hint"));
  const item = await page.locator(".more-menu [role=menuitem]").nth(2).boundingBox();
  await page.mouse.move(item.x + 20, item.y + item.height / 2);
  await page.locator(".action-hint").waitFor({ timeout: 3000 });
  await sleep(200);
  // Hints ignore the pointer (pointer-events:none); let this one be hit-tested for the check.
  const top = await page.evaluate(() => { const h = document.querySelector(".action-hint"), r = h.getBoundingClientRect(); h.style.pointerEvents = "auto"; const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return { hit: hit === h || h.contains(hit), overMenu: Boolean(document.elementsFromPoint(r.left + r.width / 2, r.top + r.height / 2).find(e => e.classList.contains("more-menu"))) }; });
  assert.ok(top.overMenu, "the hint overlaps the menu in this setup");
  assert.ok(top.hit, "the hint is above the menu");
  await context.close();
});

await check("layers: @ menu, attachment and account menus escape their containers; header back at --z-chrome (Z3, Z4)", async () => {
  const { page, context } = await fx.open({ path: "/g/grp" });
  await page.locator(".composer textarea").click();
  await page.keyboard.type("@");
  const mention = await onTop(page, ".mention-menu");
  const caret = await page.locator(".composer textarea").boundingBox();
  assert.ok(mention.rect[3] <= caret.y + caret.height, "the @ menu sits above the input");
  for (const option of await page.locator(".mention-menu [role=option]").all()) {
    const b = await option.boundingBox();
    assert.equal(await page.evaluate(([x, y]) => Boolean(document.elementFromPoint(x, y)?.closest(".mention-menu")), [b.x + b.width / 2, b.y + b.height / 2]), true, "every @ option is on top");
  }
  await page.keyboard.press("Escape");
  for (const [trigger, menu] of [[".composer-add-wrap > button", ".composer-add-menu"], [".sidebar-account-identity", ".sidebar-account-menu"]]) {
    await page.locator(trigger).click();
    await onTop(page, menu);
    const contexts = await page.evaluate(selector => { const out = []; for (let e = document.querySelector(selector).parentElement; e && e !== document.documentElement; e = e.parentElement) { const s = getComputedStyle(e); if ((s.zIndex !== "auto" && s.position !== "static") || s.isolation === "isolate" || s.transform !== "none") out.push(`${e.id || e.tagName}:${s.zIndex}`); } return out; }, menu);
    assert.equal(contexts.length, 1, `${menu}: one stacking context above it, its layer (${contexts.join(" > ")})`);
    assert.match(contexts[0], /^DIV:60$/, `${menu}: the layer is --z-menu`);
    await page.keyboard.press("Escape");
    await closed(page, menu);
  }
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector(".chat-header")).zIndex), "20", "the header is --z-chrome at 1280 px");
  await context.close();
});

await check("Z4: at 1280 px the header menu opens above the sidebar (it no longer reaches under it)", async () => {
  const { page, context } = await fx.open({});
  await page.getByRole("button", { name: "Tasks", exact: true }).first().click();
  await page.locator(".sidebar-pane").waitFor({ timeout: 4000 });
  await sleep(400);
  await page.locator(".header-more > button").click();
  await onTop(page, ".more-menu");
  const over = await page.evaluate(() => { const m = document.querySelector(".more-menu").getBoundingClientRect(), c = document.querySelector(".sidebar-pane").getBoundingClientRect(); const x = Math.max(m.left, c.left) + 10, y = Math.max(m.top, c.top) + 10; return { overlap: x < Math.min(m.right, c.right) && y < Math.min(m.bottom, c.bottom), hit: Boolean(document.elementFromPoint(x, y)?.closest(".more-menu")) }; });
  // The panel is a real grid column since the context-card column change, so the menu normally stays beside it; if the two ever meet, the menu must win.
  assert.ok(!over.overlap || over.hit, "the menu is above the column where they overlap");
  await context.close();
});

// ---- 5. popovers ----
await check("reaction menu: focus goes in, Escape closes it and focus returns to the button", async () => {
  const { page, context, errors } = await fx.open({});
  const block = page.locator('[data-message-id="dm_a_m11"]');
  const b = await block.locator(".message-content").boundingBox();
  await page.mouse.move(b.x + 30, b.y + 10);
  await block.locator('[data-action="react"]').click();
  await onTop(page, ".reaction-context-menu");
  assert.equal(await page.locator(".reaction-context-menu").getAttribute("role"), "dialog");
  assert.match((await state(page)).text, /👍/, "focus is on the first reaction");
  await page.keyboard.press("Escape");
  await closed(page, ".reaction-context-menu");
  await focusBack(page, '[data-message-id="dm_a_m11"] [data-action="react"]', "focus returns to the react button");
  assert.deepEqual(errors, []);
  await context.close();
});

await check("group read-by names are a quiet hint in #tofi-layers", async () => {
  const { page, context } = await fx.open({ path: "/g/grp" });
  const block = page.locator('[data-message-id="grp_m3"]');
  const b = await block.locator(".message-content").boundingBox();
  await page.mouse.move(b.x + 20, b.y + 10);
  const mark = block.locator(".send-receipt-mark.has-tip");
  await mark.waitFor({ timeout: 3000 });
  await mark.hover();
  await page.locator(".action-hint.is-quiet").waitFor({ timeout: 3000 });
  assert.equal(await page.locator(".action-hint.is-quiet").textContent(), "Read by Ledger Cat and Simile Cat");
  assert.equal(await page.evaluate(() => Boolean(document.querySelector(".action-hint").closest("#tofi-layers"))), true);
  assert.equal(await block.locator(".send-receipt-tip").count(), 0, "no tooltip inside the row any more");
  await context.close();
});

// The Bot identity card is a Radix Popover too. Nothing in the app opens it today (App.tsx builds it only for a delegation
// notice, which returns earlier), so it is mounted on its own in a dev-server page with the app's CSS.
await check("identity card: hover opens it in #tofi-layers without taking focus; moving into it keeps it; leaving and Escape close it", async () => {
  if (engineName !== "chromium") return console.log("  (dev-server page: Chromium only)");
  const fixture = await mkdtemp(join(ui, ".overlays-fixture-"));
  let server;
  try {
    const main = await readFile(join(ui, "src/main.tsx"), "utf8");
    const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
    await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
    const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
    await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><div id="root"></div><div id="tofi-layers"></div><script type="module" src="./main.tsx"></script>');
    await writeFile(join(fixture, "main.tsx"), `${styles}
import {createRoot} from 'react-dom/client';
import {i18nReady} from '../src/i18n';
import {installFocusModality} from '../src/focusModality';
import {BotIdentityCard} from '../src/BotIdentityCard';
installFocusModality();
const bot = {id: 'bot_a', name: 'Ledger Cat', instructions: 'Synthetic role that keeps the weekly ledger tidy.'} as any;
(window as any).__mentioned = 0;
void i18nReady.then(() => createRoot(document.getElementById('root')!).render(<div className="workspace" style={{padding: 80}}><p>Handed to <BotIdentityCard bot={bot} onMention={() => (window as any).__mentioned++} onOpen={() => {}}><span className="message-mention">@Ledger Cat</span></BotIdentityCard> for review.</p><button id="after">After</button></div>));
`);
    const { createServer } = await import("vite");
    const { default: react } = await import("@vitejs/plugin-react");
    server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
    await server.listen();
    const context = await fx.browser.newContext({ viewport: { width: 900, height: 600 }, locale: "en-US" });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/${basename(fixture)}/index.html`);
    const anchor = page.locator(".identity-card-anchor");
    await anchor.waitFor({ timeout: 20000 });
    await sleep(800);
    const box = await anchor.boundingBox();
    await page.mouse.move(box.x + 10, box.y + box.height / 2);
    await page.locator(".bot-identity-card").waitFor({ timeout: 5000 });
    await sleep(300);
    const card = await page.evaluate(() => { const c = document.querySelector(".bot-identity-card"), r = c.getBoundingClientRect(); return { layers: Boolean(c.closest("#tofi-layers")), role: c.getAttribute("role"), focusOutside: !c.contains(document.activeElement), left: r.left, top: r.top, label: c.getAttribute("aria-label") }; });
    assert.ok(card.layers, "the card is in #tofi-layers");
    assert.equal(card.role, "dialog");
    assert.match(card.label ?? "", /Ledger Cat/, "the card is labelled with the Bot's name");
    assert.ok(card.focusOutside, "hover does not move focus into the card");
    assert.ok(Math.abs(card.left - box.x) <= 1 && Math.abs(card.top - (box.y + box.height + 8)) <= 1, `the card sits 8 px under the name (${card.left},${card.top} vs ${box.x},${box.y + box.height + 8})`);
    const inside = await page.locator(".bot-identity-card").boundingBox();
    await page.mouse.move(box.x + 10, inside.y + 4, { steps: 4 });
    await page.mouse.move(inside.x + inside.width / 2, inside.y + inside.height / 2, { steps: 4 });
    await sleep(500);
    assert.equal(await page.locator(".bot-identity-card").count(), 1, "moving into the card keeps it open");
    await page.mouse.move(5, 590);
    await page.waitForFunction(() => !document.querySelector(".bot-identity-card"), null, { timeout: 2000 });
    await page.mouse.move(box.x + 10, box.y + box.height / 2);
    await page.locator(".bot-identity-card").waitFor({ timeout: 5000 });
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector(".bot-identity-card"), null, { timeout: 2000 });
    assert.deepEqual(errors, []);
    await context.close();
  } finally {
    await server?.close();
    await rm(fixture, { recursive: true, force: true });
  }
});

// ---- 6. cost ----
await check("cost: warm click-to-visible, a cold chunk, and frames while the largest menu opens at 4x CPU", async () => {
  // A fresh browser on the same build: frame timing should not inherit the dozens of pages opened above.
  const cx = await createOverlayFixture({ engineName, dist: fx.outDir });
  try {
  const visibleAfterClick = async page => {
    await page.evaluate(() => { window.__dt = null; document.querySelector(".create-menu-anchor > button").addEventListener("click", () => { const t0 = performance.now(); const tick = () => { const m = document.querySelector(".create-menu"); if (m && Number(getComputedStyle(m).opacity) > 0.05) { window.__dt = performance.now() - t0; return; } requestAnimationFrame(tick); }; requestAnimationFrame(tick); }, { capture: true, once: true }); });
    await page.locator(".create-menu-anchor > button").click();
    await page.waitForFunction(() => window.__dt != null, null, { timeout: 5000 });
    return page.evaluate(() => window.__dt);
  };
  {
    const { page, context } = await cx.open({});
    await page.waitForFunction(() => performance.getEntriesByType("resource").some(e => /RadixOverlays/.test(e.name)), null, { timeout: 5000 });
    await sleep(200);
    const warm = await visibleAfterClick(page);
    console.log(`  warm click-to-visible: ${warm.toFixed(1)} ms`);
    assert.ok(warm <= 100, `warm click-to-visible ${warm.toFixed(1)} ms > 100`);
    await context.close();
  }
  {
    cx.delays.pattern = /RadixOverlays-[\w-]+\.js$/; cx.delays.ms = 800;
    const { page, context, errors } = await cx.open({ waitFor: ".create-menu-anchor > button" });
    const cold = await visibleAfterClick(page);
    cx.delays.pattern = null;
    console.log(`  cold click-to-visible (chunk held 800 ms from page load): ${cold.toFixed(1)} ms`);
    assert.ok(cold <= 1400, `the cold menu opened ${cold.toFixed(1)} ms after the click`);
    assert.deepEqual(errors, []);
    await context.close();
  }
  // The first open of the largest menu after the app settles, three fresh pages per CPU rate. rAF deltas come in whole vsync
  // intervals (16.7 ms): the typical (median) worst frame may be three intervals (50 ms) at 1x and six (100 ms) at 4x. The
  // pre-migration menu measured the same way, interleaved on the same machine: 1x median 33 ms, 4x median 67 ms (migration.md).
  if (engineName === "chromium") for (const rate of [1, 4]) {
    const worsts = [];
    const paints = [];
    for (let run = 0; run < 3; run++) {
      const { page, context } = await cx.open({});
      await page.waitForFunction(() => performance.getEntriesByType("resource").some(e => /RadixOverlays/.test(e.name)), null, { timeout: 5000 });
      await sleep(2000); // loaded, then warmed in idle time (OverlayHost.tsx)
      await (await context.newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate });
      const box = await row(page, "Simile Cat").boundingBox();
      await page.evaluate(() => { window.__paint = null; new PerformanceObserver(list => { for (const e of list.getEntries()) if (e.name === "contextmenu") window.__paint = e.duration; }).observe({ type: "event", durationThreshold: 16 }); window.__frames = []; let last = performance.now(); const end = last + 700; const tick = now => { window.__frames.push(now - last); last = now; if (now < end) requestAnimationFrame(tick); }; requestAnimationFrame(tick); });
      await page.mouse.click(box.x + 40, box.y + 20, { button: "right" });
      await page.locator(".sidebar-context-menu").waitFor();
      await sleep(800);
      worsts.push(Math.max(...await page.evaluate(() => window.__frames.slice(1))));
      paints.push(await page.evaluate(() => window.__paint ?? 0));
      await context.close();
    }
    const median = [...worsts].sort((a, b) => a - b)[1];
    console.log(`  ${rate}x CPU: largest menu, first open, worst frame per run ${worsts.map(v => v.toFixed(1)).join(" / ")} ms (median ${median.toFixed(1)}); right click to next paint ${paints.map(v => v.toFixed(0)).join(" / ")} ms`);
    assert.ok(Math.round(median / (1000 / 60)) <= (rate === 1 ? 3 : 6), `opening the largest menu at ${rate}x CPU: median worst frame ${median.toFixed(1)} ms`);
  }
  } finally { await cx.close(); }
});

await fx.close();
console.log(failures ? `overlays (${engineName}): ${failures} FAILED` : `overlays (${engineName}): PASS`);
process.exit(failures ? 1 : 0);
