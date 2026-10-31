import { readFileSync } from "node:fs";
// Widgets, sidebar and covers across conversations and the keyboard (docs/design/widgets-and-sidebar.md sections 2.2, 7), in a real browser
// against synthetic data:
//  - each conversation remembers its own widgets (open and folded) while another is on screen; a conversation with none opens empty;
//  - the sidebar stays across a switch and re-scopes to the new conversation; the address carries it (`?side=`), a reload opens it,
//    `tofi:open-sidebar` opens it, and an unknown value is ignored;
//  - Usage widget: More > Usage opens it, it reads the conversation's usage once, again (throttled) when a run finishes and never while idle,
//    says why a tool split is missing instead of inventing one, and links to Settings > Usage;
//  - widgets reorder: Alt+Up / Down and the grip's arrow keys (announced, focus stays), dragging by the header or by pressing and moving on the computer's screen (live
//    reflow, Escape puts it back, no click-to-enlarge), the order is the person's (every conversation, a reload, auto-open lands in place);
//  - Escape closes one thing per press, in the order: Radix menu, big computer -> compact, cover, sidebar, widgets bottom-up
//    (Artifacts, then Members, then Computer); a native top-layer dialog is left to itself.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:surfaces-browser
import assert from "node:assert/strict";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, DM2, GROUP, botOwner, newState, openWorkspace, usageFor } from "./widgets-fixture.mjs";

const ARCHIVE_ENABLED = /export const ARCHIVE_ENABLED = true/.test(readFileSync(new URL("../src/featureFlags.ts", import.meta.url), "utf8"));
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const cards = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => node.dataset.card));
const poll = (page, fn, arg, timeout = 8000) => page.waitForFunction(fn, arg, { timeout });
const settled = page => page.waitForTimeout(500);
/** The cards top to bottom as they are on screen (the DOM keeps a fixed order; CSS `order` places them). */
const visual = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => [node.dataset.card, node.getBoundingClientRect().top]).sort((a, b) => a[1] - b[1]).map(item => item[0]));
const live = page => page.locator(".widget-column [role=status]").last().textContent();
const openUsage = async page => { await page.getByRole("button", { name: "More actions" }).click(); await page.getByRole("menuitem", { name: "Usage" }).click(); await page.locator('.widget-column [data-card="usage"] .usage-now').waitFor(); };
const grip = (page, card) => page.locator(`.widget-column [data-card="${card}"] .widget-grip`);
const header = (page, card) => page.locator(`.widget-column [data-card="${card}"] .context-card-header`);
const side = page => new URL(page.url()).searchParams.get("side");
const row = (page, name) => page.locator(".conversation-row", { hasText: name });
const sidebarOpen = page => page.locator(".sidebar-pane:not(.is-leaving)").count();
const escape = async page => { await page.keyboard.press("Escape"); await settled(page); };

try {
  // 1. Widgets are remembered per conversation.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await page.locator("[data-computer-toggle]").click();
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]') && document.querySelector('.widget-column [data-card="artifacts"]'));
    await page.getByRole("button", { name: /^Collapse Artifacts/ }).click();
    await settled(page);
    assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    await row(page, DM2.name).click();
    await settled(page);
    assert.deepEqual(await cards(page), [], "another conversation opens with no widgets");
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
    await row(page, DM.name).click();
    await settled(page);
    assert.deepEqual(await cards(page), ["computer", "artifacts"], "back in the first one its widgets come back");
    assert.equal(await page.locator('[data-card="artifacts"]').evaluate(node => node.classList.contains("is-collapsed")), true, "a fold is remembered too");
    await row(page, DM2.name).click();
    await settled(page);
    assert.deepEqual(await cards(page), ["artifacts"], "the second one kept its own");
    // A group has its own list; closing there does not touch the others.
    await row(page, GROUP.name).click();
    await settled(page);
    assert.deepEqual(await cards(page), []);
    await page.locator(".chat-identity").click();
    await poll(page, () => document.querySelector('.widget-column [data-card="members"]'));
    await row(page, DM.name).click();
    await settled(page);
    assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    // Back/forward restores them the same way and closes a cover.
    await page.goBack();
    await settled(page);
    assert.deepEqual(await cards(page), ["members"], "back to the group: its Members widget");
    assert.deepEqual(errors, []);
    results.push("widgets: each conversation remembers its own, folds included, across switches and back/forward");
    await context.close();
  }

  // 2. The sidebar stays and re-scopes; the address carries it.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    assert.equal(side(page), null);
    await page.getByRole("button", { name: "Tasks", exact: true }).click();
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "tasks", "opening the sidebar writes ?side=tasks (no history entry)");
    const entries = await page.evaluate(() => history.length);
    await row(page, DM2.name).click();
    await settled(page);
    assert.equal(await sidebarOpen(page), 1, "the sidebar stays when the conversation changes");
    assert.equal(side(page), "tasks", "and so does the address");
    assert.match(page.url(), /\/b\/bot-two/);
    assert.equal(await page.evaluate(() => history.length), entries + 1, "only the switch is a history entry");
    // Reload: the address opens the sidebar, widgets do not come back.
    await page.reload();
    await page.locator(".chat-header").waitFor();
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "tasks");
    assert.deepEqual(await cards(page), []);
    // Closing removes the parameter and keeps the path.
    await page.getByRole("button", { name: "Tasks", exact: true }).click();
    await settled(page);
    assert.equal(side(page), null);
    assert.match(page.url(), /\/b\/bot-two$/);
    // The deep-link event, and a value that is not a sidebar.
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("tofi:open-sidebar", { detail: { id: "memory" } })));
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "memory");
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("tofi:open-sidebar", { detail: { id: "nonsense" } })));
    await settled(page);
    assert.equal(side(page), "memory", "an unknown id is ignored");
    await page.goto(origin + "/b/bot-synthetic?side=nonsense");
    await page.locator(".chat-header").waitFor();
    await page.waitForTimeout(600);
    assert.equal(await sidebarOpen(page), 0, "an unknown ?side= opens nothing");
    await page.goto(origin + "/b/bot-synthetic?side=archive");
    await page.locator(".chat-header").waitFor();
    if (ARCHIVE_ENABLED) {
      await poll(page, () => document.querySelector(".sidebar-pane"));
      assert.equal(side(page), "archive");
      await row(page, DM2.name).click();
      await settled(page);
      assert.equal(side(page), "archive", "Archive stays with the account, whatever the conversation");
    } else {
      // The archive view is switched off (featureFlags.ts): its deep link opens nothing.
      await page.waitForTimeout(600);
      assert.equal(await sidebarOpen(page), 0, "?side=archive opens nothing while the archive view is off");
    }
    assert.deepEqual(errors, []);
    results.push("sidebar: stays and re-scopes on a switch, lives in ?side=, reload and tofi:open-sidebar open it, unknown values are ignored");
    await context.close();
  }

  // 3. Escape, one surface per press, in order.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state, { group: true });
    await page.locator(".chat-identity").click();
    await page.getByRole("button", { name: "Open artifacts" }).click();
    state.ownership = botOwner("run-g", GROUP);
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await poll(page, () => document.querySelector('.widget-column [data-card="members"]') && document.querySelector('.widget-column [data-card="artifacts"]'));
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Group shared memory" }).click();
    await poll(page, () => document.querySelector(".sidebar-pane"));
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    assert.deepEqual(await cards(page), ["computer", "members", "artifacts"]);
    // A Radix menu takes the first Escape and nothing else moves.
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menu").waitFor();
    await page.keyboard.press("Escape");
    await settled(page);
    assert.equal(await page.getByRole("menu").count(), 0, "the menu closes first");
    assert.equal(await sidebarOpen(page), 1);
    assert.deepEqual(await cards(page), ["computer", "members", "artifacts"], "and nothing else moved");
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    // The big computer window steps back to compact before anything else.
    await page.locator("[data-desktop-frame]").hover();
    await page.getByRole("button", { name: "Enlarge Bot computer" }).click();
    await page.locator(".desktop-floating-shell.is-expanded").waitFor();
    await escape(page);
    assert.equal(await page.locator(".desktop-floating-shell.is-expanded").count(), 0, "big computer -> compact");
    assert.equal(await sidebarOpen(page), 1, "the sidebar is still open");
    assert.deepEqual(await cards(page), ["computer", "members", "artifacts"]);
    // A cover is Radix: it takes its own Escape and widgets stay hidden behind it, mounted.
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Group settings" }).click();
    await page.locator(".cover-window").waitFor();
    await escape(page);
    assert.equal(await page.locator(".cover-window").count(), 0, "Escape closes the cover");
    assert.equal(await sidebarOpen(page), 1, "the sidebar was not closed by the cover's Escape");
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    // Then the sidebar, then Artifacts, Members, Computer.
    await escape(page);
    assert.equal(await sidebarOpen(page), 0, "sidebar next");
    assert.equal(side(page), null);
    assert.deepEqual(await cards(page), ["computer", "members", "artifacts"], "no widget closed with the sidebar");
    await escape(page);
    assert.deepEqual(await cards(page), ["computer", "members"], "Artifacts first");
    await escape(page);
    assert.deepEqual(await cards(page), ["computer"], "then Members");
    await escape(page);
    assert.deepEqual(await cards(page), [], "the Bot computer last");
    await escape(page);
    assert.deepEqual(errors, []);
    results.push("escape: menu, big computer, cover, sidebar, Artifacts, Members, Computer - one per press");
    await context.close();
  }


  // 5. The Usage widget.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    await openUsage(page);
    assert.deepEqual(await cards(page), ["usage"], "More > Usage opens the widget (not Settings)");
    assert.equal(await page.locator(".settings-tabs").count(), 0);
    const body = page.locator('[data-card="usage"]');
    assert.match(await body.locator(".usage-now").textContent(), /61\.4K.*200K/, "used / window");
    assert.match(await body.locator(".usage-used").textContent(), /31% used/, "one summary line: percent used, then used / window");
    assert.equal(await body.locator(".usage-parts-bar i.usage-part").count(), 7, "one segment per context part");
    assert.equal(await body.locator(".usage-parts-bar i.usage-rest").count(), 1, "the rest of the window is the empty end of the bar");
    assert.match(await body.locator(".usage-legend li").first().textContent(), /Instructions.*≈6\.8K.*3%/, "name, tokens, share of the window");
    assert.match(await body.locator(".usage-legend").textContent(), /≈6\.8K/, "estimated parts are marked");
    assert.match(await body.locator(".usage-split-legend").textContent(), /Tools.*24 calls/s, "tools against conversation, with counts");
    assert.match(await body.locator(".usage-totals").textContent(), /1\.3M.*52\.3K.*41.*≈ \$1\.86/s, "totals and the equivalent cost");
    // One read on open (the dev server's StrictMode mounts effects twice, so allow that second one), then none while the chat is idle.
    const opened = state.usageReads;
    assert.ok(opened >= 1 && opened <= 2, `a read on open (${opened})`);
    await page.waitForTimeout(2500);
    assert.equal(state.usageReads, opened, "nothing is read while the chat is idle");
    // A run in this conversation starts and finishes: one more read, after the throttle.
    state.push(DM.id, "run", { id: "run-usage-1", conversation_id: DM.id, bot_id: BOT.id, status: "running", created_at: "2026-10-26T12:00:00Z" });
    await page.waitForTimeout(1200);
    assert.equal(state.usageReads, opened, "a run starting does not read");
    state.usage = usageFor(DM.id, { total: 120_000 });
    state.push(DM.id, "run", { id: "run-usage-1", conversation_id: DM.id, bot_id: BOT.id, status: "done", created_at: "2026-10-26T12:00:00Z" });
    await poll(page, () => /60% used/.test(document.querySelector('[data-card="usage"] .usage-used')?.textContent ?? ""), null, 12000);
    assert.equal(state.usageReads, opened + 1, "a finished run reads once more");
    // No tool split: one quiet line, no bar and no numbers.
    state.usage = usageFor(DM.id, { split: false, snapshot: false });
    await page.getByRole("button", { name: /^Close Usage/ }).click();
    await settled(page);
    await openUsage(page);
    const lines = (await body.locator(".usage-widget-line").allTextContents()).join(" | ");
    assert.match(lines, /Per-call detail is kept for 7 days/);
    assert.match(lines, /No breakdown for the latest run/);
    assert.equal(await body.locator(".usage-split-bar").count(), 0, "no invented split");
    assert.equal(await body.locator(".usage-parts-bar i.usage-part").count(), 1, "no snapshot: the bar is one segment of what is used");
    assert.match(await body.locator(".usage-now").textContent(), /61\.4K \/ 200K/, "the summary still shows used / window");
    // A failed read says so and can be retried.
    state.usage = "fail";
    await page.getByRole("button", { name: /^Close Usage/ }).click();
    await settled(page);
    await openUsage(page).catch(() => undefined);
    await page.locator('[data-card="usage"] [role=alert]').waitFor();
    state.usage = undefined;
    await page.getByRole("button", { name: "Try again" }).click();
    await page.locator('[data-card="usage"] .usage-now').waitFor();
    // An older server has no such route (404): a calm line, the All usage link, and no Try again loop.
    state.usage = "404";
    await page.getByRole("button", { name: /^Close Usage/ }).click();
    await settled(page);
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Usage" }).click();
    await page.locator('[data-card="usage"]').getByText("Usage for this chat needs a newer server.").waitFor();
    assert.equal(await page.locator('[data-card="usage"]').getByRole("button", { name: "Try again" }).count(), 0, "no retry for a missing route");
    assert.equal(await page.locator('[data-card="usage"] [role=alert]').count(), 0, "not an alarm");
    assert.equal(await page.getByRole("button", { name: "All usage" }).count(), 1, "the link is still there");
    state.usage = undefined;
    await page.getByRole("button", { name: /^Close Usage/ }).click();
    await settled(page);
    await openUsage(page);
    // All usage -> Settings > Usage.
    await page.getByRole("button", { name: "All usage" }).click();
    await page.locator(".settings-tabs").first().waitFor({ timeout: 8000 });
    assert.deepEqual(errors, []);
    results.push("usage: opens from More, reads once, re-reads when a run finishes (never idle), names why a split is missing, retries a failed read, a 404 (older server) is one calm line without retry, links to Settings");
    await context.close();
  }

  // 6. Reordering: keyboard, drag, persistence.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state, { height: 1500 });
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await openUsage(page);
    await page.locator("[data-computer-toggle]").click();
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await settled(page);
    assert.deepEqual(await visual(page), ["computer", "artifacts", "usage"], "the default order");
    // Keyboard on the grip: Up / Down move, focus stays, the move is announced.
    await grip(page, "usage").focus();
    await page.keyboard.press("ArrowUp");
    await settled(page);
    assert.deepEqual(await visual(page), ["computer", "usage", "artifacts"], "ArrowUp on the grip moves it up");
    assert.equal(await page.evaluate(() => document.activeElement?.className.includes("widget-grip")), true, "focus stays on the grip");
    assert.match(await live(page), /Usage is now 2 of 3/, "announced");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");
    await settled(page);
    assert.deepEqual(await visual(page), ["usage", "computer", "artifacts"]);
    assert.match(await live(page), /already first/, "the top says so");
    await page.keyboard.press("End");
    await settled(page);
    assert.deepEqual(await visual(page), ["computer", "artifacts", "usage"], "End sends it to the bottom");
    // Alt+Up / Alt+Down from any control in the header.
    await page.getByRole("button", { name: /^Collapse Artifacts/ }).focus();
    await page.keyboard.press("Alt+ArrowDown");
    await settled(page);
    assert.deepEqual(await visual(page), ["computer", "usage", "artifacts"], "Alt+Down from the fold button moves Artifacts down");
    await page.getByRole("button", { name: /^Collapse Artifacts/ }).click();
    await page.getByRole("button", { name: /^Expand Artifacts/ }).click();
    // The computer has no header and no handle: Alt+Down on its screen button moves it, and the screen does not enlarge.
    assert.equal(await page.locator('[data-card="computer"] .widget-grip').count(), 0, "the Bot computer has no separate grip any more");
    await page.locator('[data-card="computer"] .desktop-enlarge').focus();
    await page.keyboard.press("Alt+ArrowDown");
    await settled(page);
    assert.deepEqual(await visual(page), ["usage", "computer", "artifacts"], "the Bot computer moves by keyboard too");
    assert.equal(await page.locator(".desktop-floating-shell.is-expanded").count(), 0, "moving it does not enlarge it");
    // Press and move on the screen drags it (no handle); a plain press is still the click that enlarges.
    {
      const screen = () => page.locator('[data-card="computer"] .desktop-enlarge').boundingBox();
      let at = await screen();
      const cx = at.x + at.width / 2, cy = at.y + at.height / 2;
      await page.mouse.move(cx, cy);
      await page.mouse.down();
      await page.mouse.move(cx, cy + 3, { steps: 2 });
      assert.equal(await page.locator("[data-dragging]").count(), 0, "under the slop a press has not become a drag");
      await page.mouse.move(cx, cy + 12, { steps: 3 });
      assert.equal(await page.locator("[data-dragging]").count(), 1, "moving past the slop lifts the widget");
      await page.mouse.move(cx, cy + 700, { steps: 24 });
      await page.waitForTimeout(350);
      await page.mouse.up();
      await settled(page);
      assert.equal((await visual(page)).at(-1), "computer", "dragged by the screen to the bottom");
      assert.equal(await page.locator(".computer-detail.is-expanded").count(), 0, "a drag is not a click: nothing enlarged");
      // Held, then a small move also drags (Escape cancels).
      at = await screen();
      await page.mouse.move(at.x + at.width / 2, at.y + at.height / 2);
      await page.mouse.down();
      await page.waitForTimeout(450);
      await page.mouse.move(at.x + at.width / 2, at.y + at.height / 2 - 3, { steps: 2 });
      assert.equal(await page.locator("[data-dragging]").count(), 1, "held, a small move drags");
      await page.keyboard.press("Escape");
      await page.mouse.up();
      await settled(page);
      assert.equal(await page.locator(".computer-detail.is-expanded").count(), 0, "cancelled: nothing enlarged");
      // A short press without movement is the click.
      at = await screen();
      await page.mouse.click(at.x + at.width / 2, at.y + at.height / 2);
      await page.locator(".computer-detail.is-expanded").waitFor({ timeout: 4000 });
      await page.keyboard.press("Escape");
      await settled(page);
      assert.equal(await page.locator(".computer-detail.is-expanded").count(), 0, "Escape shrinks it again");
      await page.locator('[data-card="computer"] .desktop-enlarge').focus();
      await page.keyboard.press("Alt+ArrowUp");
      await settled(page);
      assert.deepEqual(await visual(page), ["usage", "computer", "artifacts"], "back where the next steps expect it");
    }
    // Dragging by the header: the card follows the pointer, the others make room as its middle crosses theirs, it settles where it is let go.
    const box = async card => header(page, card).boundingBox();
    const from = await box("usage");
    await page.mouse.move(from.x + 60, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(from.x + 60, from.y + from.height / 2 + 6, { steps: 2 });
    assert.equal(await page.locator("[data-dragging]").count(), 1, "a lifted card while dragging");
    assert.equal(await page.evaluate(() => document.documentElement.hasAttribute("data-widget-dragging")), true);
    await page.mouse.move(from.x + 60, from.y + from.height / 2 + 700, { steps: 24 });
    await page.waitForTimeout(350);
    await page.mouse.up();
    await settled(page);
    const dragged = await visual(page);
    assert.equal(dragged.at(-1), "usage", "dragged to the bottom");
    assert.equal(await page.locator("[data-dragging]").count(), 0, "the lift ends");
    assert.equal(await page.evaluate(() => [...document.querySelectorAll(".widget-slot")].every(node => !node.style.translate)), true, "no leftover offset");
    assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector(".widget-slot")).cursor), "auto", "no hand cursor");
    // Escape while dragging puts it back and closes nothing.
    const before = await visual(page);
    const top = await box(before.at(-1));
    await page.mouse.move(top.x + 60, top.y + top.height / 2);
    await page.mouse.down();
    await page.mouse.move(top.x + 60, top.y - 400, { steps: 14 });
    await page.waitForTimeout(300);
    assert.notDeepEqual(await visual(page), before, "the others made room");
    await page.keyboard.press("Escape");
    await settled(page);
    await page.mouse.up();
    await settled(page);
    assert.deepEqual(await visual(page), before, "Escape puts it back");
    assert.equal((await cards(page)).length, 3, "and closes nothing");
    // The order is the person's: every conversation, and a reload (a non-default one is stored; the default is not).
    assert.equal(await page.evaluate(() => localStorage.getItem("tofi:widget-order:local")), null, "the default order is not stored");
    await grip(page, "usage").focus();
    await page.keyboard.press("ArrowUp");
    await settled(page);
    const chosen = await visual(page);
    assert.deepEqual(chosen, ["computer", "usage", "artifacts"]);
    await row(page, DM2.name).click();
    await settled(page);
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await openUsage(page);
    await settled(page);
    assert.deepEqual(await visual(page), chosen.filter(id => id !== "computer"), "another conversation lays its widgets out in the same order");
    assert.equal(await page.evaluate(() => localStorage.getItem("tofi:widget-order:local") !== null), true, "kept for this browser and account");
    await page.reload();
    await page.locator(".chat-header").waitFor();
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await openUsage(page);
    await settled(page);
    assert.deepEqual(await visual(page), chosen.filter(id => id !== "computer"), "a reload keeps it");
    // A widget that opens by itself lands at its place in the saved order.
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await row(page, DM.name).click();
    await settled(page);
    await openUsage(page);
    state.addFile(1);
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'), null, 12000);
    await settled(page);
    assert.deepEqual(await visual(page), ["usage", "artifacts"], "Artifacts opened by itself lands below Usage, where the person put it");
    // The header reads "Title ⌄": the title is the fold button with the chevron right after the words, the close X shows only while the card is hovered or focused,
    // a click on the title folds, and a press that travels reorders without folding. The title colour passes AA on the card in this theme.
    {
      const title = page.locator('[data-card="usage"] .context-card-fold');
      assert.equal(await title.getAttribute("aria-expanded"), "true");
      const geometry = await title.evaluate(node => { const t = node.querySelector("span").getBoundingClientRect(), c = node.querySelector("svg").getBoundingClientRect(), h = node.closest(".context-card-header").getBoundingClientRect(); return { gap: c.left - t.right, titleLeft: t.left - h.left, chevronRight: h.right - c.right }; });
      assert.ok(geometry.gap >= 0 && geometry.gap < 10, `the chevron follows the title words (gap ${geometry.gap})`);
      assert.ok(geometry.chevronRight > 120, "the chevron is not at the far right");
      await page.evaluate(() => document.activeElement?.blur());
      await page.mouse.move(5, 5);
      await page.waitForTimeout(300);
      assert.equal(await page.locator('[data-card="usage"] .context-card-actions').evaluate(node => getComputedStyle(node).opacity), "0", "at rest the header is just the title and the chevron");
      await title.hover();
      await page.waitForTimeout(300);
      assert.equal(await page.locator('[data-card="usage"] .context-card-actions').evaluate(node => getComputedStyle(node).opacity), "1", "the grip and the close X appear on hover");
      const contrast = await title.evaluate(node => {
        const lum = css => { const m = css.match(/[\d.]+/g).map(Number); const [r, g, b] = m.slice(0, 3).map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
        node.style.transition = "none"; node.style.color = getComputedStyle(node).color;
        const fg = lum(getComputedStyle(node.querySelector("span")).color); const bg = lum(getComputedStyle(node.closest(".context-card")).backgroundColor);
        return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
      });
      assert.ok(contrast >= 4.5, `the title passes AA on the card (${contrast.toFixed(2)})`);
      await title.click();
      await page.locator('[data-card="usage"].is-collapsed').waitFor();
      await title.click();
      await page.locator('[data-card="usage"]:not(.is-collapsed)').waitFor();
      // Press on the title and move: the card is dragged (to the bottom), not folded.
      const at = await title.boundingBox();
      await page.mouse.move(at.x + 12, at.y + at.height / 2);
      await page.mouse.down();
      await page.mouse.move(at.x + 12, at.y + at.height / 2 + 14, { steps: 3 });
      assert.equal(await page.locator("[data-dragging]").count(), 1, "a press that travels on the title lifts the card");
      await page.mouse.move(at.x + 12, at.y + 600, { steps: 20 });
      await page.waitForTimeout(350);
      await page.mouse.up();
      await settled(page);
      assert.deepEqual(await visual(page), ["artifacts", "usage"], "dragged by its title");
      assert.equal(await page.locator('[data-card="usage"].is-collapsed').count(), 0, "a drag does not fold it");
    }
    assert.deepEqual(errors, []);
    results.push("reorder: grip arrows, Alt+Up/Down, End, the Bot computer's screen (Alt+arrows, press-and-move, hold-then-move, plain press enlarges), header and title drag with live reflow, Escape restores, order kept across conversations and reloads");
    await context.close();
  }

  // 7. Reduced motion: a move is instant (no slide left running), and the dragged card still follows the pointer.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state, { reducedMotion: true });
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await openUsage(page);
    await settled(page);
    await grip(page, "usage").focus();
    await page.keyboard.press("ArrowUp");
    const running = await page.evaluate(() => document.getAnimations().filter(animation => animation.transitionProperty === "translate").length);
    assert.equal(running, 0, "no slide under reduced motion");
    assert.deepEqual(await visual(page), ["usage", "artifacts"]);
    assert.deepEqual(errors, []);
    results.push("reorder: reduced motion moves at once");
    await context.close();
  }

  // 4. A native top-layer dialog is left to itself, and Settings closes with Escape.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
    await page.evaluate(() => { const dialog = document.createElement("dialog"); dialog.id = "synthetic-top-layer"; dialog.textContent = "preview"; document.body.append(dialog); dialog.showModal(); });
    await page.keyboard.press("Escape");
    await settled(page);
    assert.deepEqual(await cards(page), ["artifacts"], "Escape with a native dialog open does not close a widget");
    await page.evaluate(() => document.getElementById("synthetic-top-layer")?.remove());
    // Settings (opened by the deep-link event the chat cards use) closes with Escape, and the widget behind it is untouched.
    await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-tool-settings")));
    await page.locator(".settings-tabs").first().waitFor({ timeout: 8000 });
    await escape(page);
    await page.waitForTimeout(400);
    assert.equal(await page.locator(".settings-tabs").count(), 0, "Escape closes Settings");
    assert.deepEqual(await cards(page), ["artifacts"], "and leaves the widget");
    assert.deepEqual(errors, []);
    results.push("escape: a native dialog keeps its Escape; Settings closes and the widgets stay");
    await context.close();
  }
  console.log(results.map(line => `PASS ${line}`).join("\n"));
  console.log("surfaces browser: PASS");
} finally {
  await browser.close();
  await server.close();
}
