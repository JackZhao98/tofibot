import { readFileSync } from "node:fs";
// Widgets, sidebar and covers across conversations and the keyboard (docs/design/widgets-and-sidebar.md sections 2.2, 7), in a real browser
// against synthetic data:
//  - each conversation remembers its own widgets (open and folded) while another is on screen; a conversation with none opens empty;
//  - the sidebar stays across a switch and re-scopes to the new conversation; the address carries it (`?side=`), a reload opens it,
//    `tofi:open-sidebar` opens it, and an unknown value is ignored;
//  - Escape closes one thing per press, in the order: Radix menu, big computer -> compact, cover, sidebar, widgets bottom-up
//    (Artifacts, then Computer); a native top-layer dialog is left to itself.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> npm run test:surfaces-browser
import assert from "node:assert/strict";
import { launch, startUi } from "./computer-window-fixture.mjs";
import { BOT, DM, DM2, GROUP, botOwner, newState, openWorkspace } from "./widgets-fixture.mjs";

// V3 PR 3.2: Tasks is an item of the header's More menu, no longer a header button.
async function toggleTasks(page) {
  await page.locator(".header-more > button").click();
  await page.locator(".more-menu[data-state=open] [role=menuitem]").filter({ hasText: "Tasks" }).click();
}


const ARCHIVE_ENABLED = /export const ARCHIVE_ENABLED = true/.test(readFileSync(new URL("../src/featureFlags.ts", import.meta.url), "utf8"));
const { server, origin } = await startUi();
const browser = await launch();
const results = [];
const cards = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => node.dataset.card));
const poll = (page, fn, arg, timeout = 8000) => page.waitForFunction(fn, arg, { timeout });
const settled = page => page.waitForTimeout(500);
/** The cards top to bottom as they are on screen (the DOM keeps a fixed order; CSS `order` places them). */
const visual = page => page.locator(".widget-column [data-card]").evaluateAll(nodes => nodes.map(node => [node.dataset.card, node.getBoundingClientRect().top]).sort((a, b) => a[1] - b[1]).map(item => item[0]));
const live = async page => (await page.locator("#tofi-layers [role=status]").allTextContents()).join(" ");
const side = page => new URL(page.url()).searchParams.get("side");
const row = (page, name) => page.locator(".conversation-row", { hasText: name });
const sidebarOpen = page => page.locator(".sidebar-pane:not(.is-leaving)").count();
const escape = page => page.keyboard.press("Escape");
/** Re-runs the assertions until they hold (8 s), so a slow machine waits longer and a fast one does not wait at all; a plain sleep is a race. */
const eventually = async (check, timeout = 8000) => {
  const end = Date.now() + timeout;
  for (;;) {
    try { return await check(); } catch (error) { if (error?.code !== "ERR_ASSERTION" || Date.now() > end) throw error; }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
};

try {
  // 1. Widgets are remembered per conversation.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await page.locator("[data-computer-toggle]").click();
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]') && document.querySelector('.widget-column [data-card="artifacts"]'));
    await page.locator(".artifacts-widget-all").click();
    const tabs = page.locator('.cover-window .artifacts-kinds [role="tab"]');
    await tabs.first().waitFor();
    await tabs.first().focus();
    await page.keyboard.press("ArrowRight");
    await page.waitForFunction(() => document.querySelector('.cover-window .artifacts-kinds [role="tab"]:nth-child(2)')?.getAttribute("aria-selected") === "true");
    assert.equal(await tabs.nth(1).evaluate(tab => document.getElementById(tab.getAttribute("aria-controls"))?.getAttribute("role")), "tabpanel", "Radix trigger controls a real panel");
    await page.keyboard.press("Home");
    await page.keyboard.press("Escape");
    await page.locator(".cover-window").waitFor({ state: "detached" });
    await page.getByRole("button", { name: /^Collapse Artifacts/ }).click();
    await eventually(async () => {
      assert.equal(await page.getByRole("button", { name: /^Expand Artifacts/ }).evaluate(button => {
        // Radix omits aria-controls while closed, even with forceMount.
        const content = button.closest(".context-card").querySelector(".context-card-body");
        return button.getAttribute("aria-expanded") === "false" && content?.dataset.state === "closed" && content.inert;
      }), true, "Radix fold controls the retained inert content");
      assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    });
    await row(page, DM2.name).click();
    await eventually(async () => {
      assert.deepEqual(await cards(page), [], "another conversation opens with no widgets");
    });
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
    await row(page, DM.name).click();
    await eventually(async () => {
      assert.deepEqual(await cards(page), ["computer", "artifacts"], "back in the first one its widgets come back");
      assert.equal(await page.locator('[data-card="artifacts"]').evaluate(node => node.classList.contains("is-collapsed")), true, "a fold is remembered too");
    });
    await row(page, DM2.name).click();
    await eventually(async () => {
      assert.deepEqual(await cards(page), ["artifacts"], "the second one kept its own");
    });
    // A group has its own list; closing there does not touch the others.
    await row(page, GROUP.name).click();
    await eventually(async () => {
      assert.deepEqual(await cards(page), []);
    });
    await page.getByRole("button", { name: "Open artifacts" }).click();
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
    await row(page, DM.name).click();
    await eventually(async () => {
      assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    });
    // Back/forward restores them the same way and closes a cover.
    await page.goBack();
    await eventually(async () => {
      assert.deepEqual(await cards(page), ["artifacts"], "back to the group: its Artifacts widget");
      assert.deepEqual(errors, []);
    });
    results.push("widgets: each conversation remembers its own, folds included, across switches and back/forward");
    await context.close();
  }

  // 2. The sidebar stays and re-scopes; the address carries it.
  {
    const state = newState();
    const { context, page, errors } = await openWorkspace(browser, origin, state);
    assert.equal(side(page), null);
    await toggleTasks(page);
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "tasks", "opening the sidebar writes ?side=tasks (no history entry)");
    const entries = await page.evaluate(() => history.length);
    await row(page, DM2.name).click();
    await eventually(async () => {
      assert.equal(await sidebarOpen(page), 1, "the sidebar stays when the conversation changes");
      assert.equal(side(page), "tasks", "and so does the address");
      assert.match(page.url(), /\/b\/bot-two/);
      assert.equal(await page.evaluate(() => history.length), entries + 1, "only the switch is a history entry");
    });
    // Reload: the address opens the sidebar, widgets do not come back.
    await page.reload();
    await page.locator(".chat-header").waitFor();
    await poll(page, () => document.querySelector(".sidebar-pane"));
    assert.equal(side(page), "tasks");
    assert.deepEqual(await cards(page), []);
    // Closing removes the parameter and keeps the path.
    await toggleTasks(page);
    await eventually(async () => {
      assert.equal(side(page), null);
      assert.match(page.url(), /\/b\/bot-two$/);
    });
    // The deep-link event, and a value that is not a sidebar.
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("tofi:open-sidebar", { detail: { id: "memory" } })));
    await poll(page, () => document.querySelector(".sidebar-pane") && new URL(location.href).searchParams.get("side") === "memory");
    assert.equal(side(page), "memory");
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("tofi:open-sidebar", { detail: { id: "nonsense" } })));
    await settled(page); // a negative has no event to wait for: give a wrong handler time to act
    assert.equal(side(page), "memory", "an unknown id is ignored");
    await page.goto(origin + "/b/bot-synthetic?side=nonsense");
    await page.locator(".chat-header").waitFor();
    await page.waitForTimeout(600); // a negative has no event to wait for
    assert.equal(await sidebarOpen(page), 0, "an unknown ?side= opens nothing");
    await page.goto(origin + "/b/bot-synthetic?side=archive");
    await page.locator(".chat-header").waitFor();
    if (ARCHIVE_ENABLED) {
      await poll(page, () => document.querySelector(".sidebar-pane"));
      assert.equal(side(page), "archive");
      await row(page, DM2.name).click();
      await eventually(async () => {
        assert.equal(side(page), "archive", "Archive stays with the account, whatever the conversation");
      });
    } else {
      // The archive view is switched off (featureFlags.ts): its deep link opens nothing.
      await page.waitForTimeout(600); // a negative has no event to wait for
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
    await page.getByRole("button", { name: "Open artifacts" }).click();
    state.ownership = botOwner("run-g", GROUP);
    await poll(page, () => document.querySelector('.widget-column [data-card="computer"]'));
    await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Group shared memory" }).click();
    await poll(page, () => document.querySelector(".sidebar-pane"));
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    // A Radix menu takes the first Escape and nothing else moves.
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menu").waitFor();
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.equal(await page.getByRole("menu").count(), 0, "the menu closes first");
      assert.equal(await sidebarOpen(page), 1);
      assert.deepEqual(await cards(page), ["computer", "artifacts"], "and nothing else moved");
    });
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    // The big computer window steps back to compact before anything else.
    await page.locator("[data-desktop-frame]").hover();
    await page.locator(".tb-device-ctl").getByRole("button", { name: "Enlarge Bot computer", exact: true }).click();
    await page.locator(".desktop-floating-shell.is-expanded").waitFor();
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.equal(await page.locator(".desktop-floating-shell.is-expanded").count(), 0, "big computer -> compact");
      assert.equal(await sidebarOpen(page), 1, "the sidebar is still open");
      assert.deepEqual(await cards(page), ["computer", "artifacts"]);
    });
    // A cover is Radix: it takes its own Escape and widgets stay hidden behind it, mounted.
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Group settings" }).click();
    await page.locator(".cover-window").waitFor();
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.equal(await page.locator(".cover-window").count(), 0, "Escape closes the cover");
      assert.equal(await sidebarOpen(page), 1, "the sidebar was not closed by the cover's Escape");
    });
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    // Then the sidebar, then Artifacts, Computer.
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.equal(await sidebarOpen(page), 0, "sidebar next");
      assert.equal(side(page), null);
      assert.deepEqual(await cards(page), ["computer", "artifacts"], "no widget closed with the sidebar");
    });
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.deepEqual(await cards(page), ["computer"], "Artifacts first");
    });
    await page.keyboard.press("Escape");
    await eventually(async () => {
      assert.deepEqual(await cards(page), [], "the Bot computer last");
    });
    await page.keyboard.press("Escape");
    assert.deepEqual(errors, []);
    results.push("escape: menu, big computer, cover, sidebar, Artifacts, Computer - one per press");
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
    await settled(page); // a negative has no event to wait for
    assert.deepEqual(await cards(page), ["artifacts"], "Escape with a native dialog open does not close a widget");
    await page.evaluate(() => document.getElementById("synthetic-top-layer")?.remove());
    // Settings (opened by the deep-link event the chat cards use) closes with Escape, and the widget behind it is untouched.
    await page.evaluate(() => window.dispatchEvent(new Event("tofi:open-tool-settings")));
    await page.locator(".settings-tabs").first().waitFor({ timeout: 8000 });
    await escape(page);
    await eventually(async () => {
      assert.equal(await page.locator(".settings-tabs").count(), 0, "Escape closes Settings");
      assert.deepEqual(await cards(page), ["artifacts"], "and leaves the widget");
      assert.deepEqual(errors, []);
    });
    results.push("escape: a native dialog keeps its Escape; Settings closes and the widgets stay");
    await context.close();
  }
  // 8. Widgets reserve no space of their own (owner, 11-08: "why did this empty right side bar come in by itself?"). The widget column
  // lives inside the chat area, so opening it must leave the grid's third track at 0 and the chat pane at the window edge, at every
  // wide width; only an open sidebar owns that track. A host without a computer (config.computer.backend "none") shows no computer
  // button and no computer widget, and asks nothing of /api/computers.
  {
    const none = { backend: "none", available: false, attached: false, host_backend: "none", capabilities: [] };
    const kvm = { backend: "kvm", available: true, attached: true, host_backend: "kvm", capabilities: ["shell", "files", "browser", "desktop", "desktop_stream", "terminal"] };
    const tracks = page => page.evaluate(() => ({ columns: getComputedStyle(document.querySelector(".workspace-grid")).gridTemplateColumns.split(" ").map(Number.parseFloat), chatRight: Math.round(document.querySelector(".chat-pane").getBoundingClientRect().right), width: window.innerWidth }));
    for (const [name, computerConfig] of [["no computer", none], ["computer", kvm]]) {
      for (const width of [1122, 1280, 1440, 1728, 2000]) {
        const state = newState();
        state.computerConfig = computerConfig;
        const { context, page, errors } = await openWorkspace(browser, origin, state, { width, theme: "dark" });
        assert.equal(await page.locator("[data-computer-toggle]").count(), computerConfig === none ? 0 : 1, `${name} ${width}: the computer button follows the host`);
        if (computerConfig !== none) await page.locator("[data-computer-toggle]").click();
        await page.getByRole("button", { name: "Open artifacts" }).click();
        await poll(page, () => document.querySelector('.widget-column [data-card="artifacts"]'));
        await eventually(async () => {
          assert.deepEqual(await cards(page), computerConfig === none ? ["artifacts"] : ["computer", "artifacts"], `${name} ${width}: only the widgets this host can show`);
        });
        await eventually(async () => {
          const open = await tracks(page);
          assert.equal(open.columns.length, 3, `${name} ${width}: three grid tracks`);
          assert.equal(open.columns[2], 0, `${name} ${width}: opening widgets reserves no right-hand track (got ${open.columns[2]}px)`);
          assert.equal(open.chatRight, open.width, `${name} ${width}: the chat reaches the window edge`);
        });
        // The sidebar still owns that track while it is open, and gives it back.
        await toggleTasks(page);
        await page.locator(".sidebar-pane:not(.is-leaving)").waitFor();
        await eventually(async () => {
          assert.ok((await tracks(page)).columns[2] >= 360, `${name} ${width}: an open sidebar takes its track`);
        });
        await page.keyboard.press("Escape");
        await eventually(async () => {
          assert.equal((await tracks(page)).columns[2], 0, `${name} ${width}: the track is given back`);
        });
        if (computerConfig === none) assert.equal(state.requests.filter(path => path.startsWith("/api/computer")).length, 0, `${name} ${width}: no /api/computer request (${state.requests.filter(path => path.startsWith("/api/computer")).join(", ")})`);
        assert.deepEqual(errors, []);
        await context.close();
      }
    }
    results.push("widgets: opening them reserves no right-hand grid track at 1122-2000 px, the sidebar still does; a host with no computer shows no computer button or widget");
  }
  // Mac and browser use the same responsive surfaces. Hold the request itself, never sleep to manufacture slowness.
  for (const desktop of [false, true]) for (const width of [900, 1280]) {
    const state = newState();
    let releaseMemory;
    state.memoryGate = new Promise(resolve => { releaseMemory = resolve; });
    const initScript = desktop ? () => {
      const off = () => () => {};
      window.tofiDesktop = {
        platform: "darwin", themePreference: "dark", onCommand: off, onWindowState: off, onFlushState: off,
        appearance: async () => {}, remoteInputFocus: () => {},
        readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
      };
    } : undefined;
    const {context, page, errors} = await openWorkspace(browser, origin, state, {width, height: 700, theme: "dark", initScript});
    await page.getByRole("button", {name: "More actions"}).click();
    const memoryFrame = await page.getByRole("menuitem", {name: "This Bot's memory", exact: true}).evaluate(item => {
      item.click();
      return new Promise(resolve => requestAnimationFrame(() => {
        const surface = document.querySelector('.sidebar-pane, .detail-pane.context-panel.visible');
        resolve(Boolean(surface && surface.getBoundingClientRect().width > 0));
      }));
    });
    assert.equal(memoryFrame, true, "Memory has a surface in the first animation frame");
    const memory = width >= 1100 ? page.locator(".sidebar-pane") : page.locator(".detail-pane.context-panel.visible");
    await memory.locator(".surface-skeleton").waitFor();
    await page.keyboard.press("Escape");
    await memory.waitFor({state: "hidden"});
    releaseMemory();
    await page.getByRole("button", {name: "More actions"}).click();
    await page.getByRole("menuitem", {name: "This Bot's memory", exact: true}).click();
    await memory.getByRole("button", {name: "Add memory", exact: true}).waitFor();
    assert.equal(await memory.locator(".memory-create").count(), 0, "Memory opens on the saved notes");
    await memory.getByRole("button", {name: "Add memory", exact: true}).click();
    await memory.locator(".memory-create").waitFor();
    assert.equal(await memory.evaluate(el => el.scrollWidth > el.clientWidth + 1), false, "Memory fits the narrower panel");
    await page.keyboard.press("Escape");
    await memory.waitFor({state: "hidden"});
    assert.deepEqual(errors, []);
    await context.close();
  }
  results.push("Mac/browser at 900 and 1280: Memory loading is dismissible, Memory creation is collapsed, panels fit and Escape closes");

  console.log(results.map(line => `PASS ${line}`).join("\n"));
  console.log("surfaces browser: PASS");
} finally {
  await browser.close();
  await server.close();
}
