// Widgets, sidebar and covers across conversations and the keyboard (docs/design/widgets-and-sidebar.md sections 2.2, 7), in a real browser
// against synthetic data:
//  - each conversation remembers its own widgets (open and folded) while another is on screen; a conversation with none opens empty;
//  - the sidebar stays across a switch and re-scopes to the new conversation; the address carries it (`?side=`), a reload opens it,
//    `tofi:open-sidebar` opens it, and an unknown value is ignored;
//  - Escape closes one thing per press, in the order: Radix menu, big computer -> compact, cover, sidebar, widgets bottom-up
//    (Artifacts, then Members, then Computer); a native top-layer dialog is left to itself.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:surfaces-browser
import assert from "node:assert/strict";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, DM2, GROUP, botOwner, newState, openWorkspace } from "./widgets-fixture.mjs";

const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const cards = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => node.dataset.card));
const poll = (page, fn, arg, timeout = 8000) => page.waitForFunction(fn, arg, { timeout });
const settled = page => page.waitForTimeout(500);
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
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "archive");
    await row(page, DM2.name).click();
    await settled(page);
    assert.equal(side(page), "archive", "Archive stays with the account, whatever the conversation");
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
    await page.getByRole("button", { name: "Enlarge shared computer" }).click();
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
    assert.deepEqual(await cards(page), [], "the computer last");
    await escape(page);
    assert.deepEqual(errors, []);
    results.push("escape: menu, big computer, cover, sidebar, Artifacts, Members, Computer - one per press");
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
