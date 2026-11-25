// Widgets, sidebar and covers (docs/design/widgets-and-sidebar.md section 2.4): the pure state functions, table-driven.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-surfaces-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/surfaces.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  const S = await import(pathToFileURL(join(output, "surfaces.js")));
  const { emptySurfaces: empty, openWidget, closeWidget, toggleWidget, foldWidget, openSidebar, closeSidebar, toggleSidebar, toggleSidebarExpanded, autoOpenWidget, setWidgetFolded, openCover, openPage, closeCover, closeAll, escapeTarget, isEmpty, WIDGET_ORDER, stashWidgets, suppressedRunsOf, forgetWidgets, enterConversation, sortWidgets, spaceFoldTarget } = S;
  const shape = s => ({ widgets: s.widgets, folded: s.folded, sidebar: s.sidebar, cover: s.cover });
  const run = (...steps) => steps.reduce((state, step) => step(state), empty());

  // The default order, whatever the opening order; one of each; at most four.
  assert.deepEqual(WIDGET_ORDER, ["bot", "computer", "terminal", "artifacts"]);
  const orders = [["artifacts", "terminal", "computer"], ["terminal", "artifacts", "computer"], ["computer", "artifacts", "terminal"], ["artifacts", "artifacts", "computer", "computer"], ["artifacts", "terminal", "computer"]];
  for (const opened of orders) {
    const state = run(...opened.map(id => s => openWidget(s, id)));
    assert.deepEqual(state.widgets, [...WIDGET_ORDER].filter(id => opened.includes(id)), `order for ${opened}`);
    assert.ok(state.widgets.length <= 4);
  }

  // Close and toggle.
  assert.deepEqual(run(s => openWidget(s, "computer"), s => openWidget(s, "terminal"), s => closeWidget(s, "computer")).widgets, ["terminal"]);
  assert.deepEqual(run(s => toggleWidget(s, "artifacts")).widgets, ["artifacts"]);
  assert.deepEqual(run(s => toggleWidget(s, "artifacts"), s => toggleWidget(s, "artifacts")).widgets, []);
  const idle = empty();
  assert.equal(closeWidget(idle, "computer"), idle, "closing what is not open changes nothing (same object)");

  // Fold: only open widgets, content stays open; closing clears the fold; reopening an open one unfolds it and bumps focus.
  assert.equal(foldWidget(idle, "computer"), idle, "a closed widget cannot be folded");
  let state = run(s => openWidget(s, "computer"), s => openWidget(s, "terminal"), s => foldWidget(s, "computer"));
  assert.deepEqual([state.widgets, state.folded], [["computer", "terminal"], ["computer"]]);
  assert.deepEqual(foldWidget(state, "computer").folded, [], "fold toggles");
  const refocus = openWidget(state, "computer");
  assert.deepEqual([refocus.folded, refocus.widgets], [[], ["computer", "terminal"]]);
  assert.deepEqual(refocus.focus, { id: "computer", n: state.focus.n + 1 });
  assert.deepEqual(closeWidget(state, "computer").folded, [], "closing drops the fold");
  assert.deepEqual(openWidget(closeWidget(state, "computer"), "computer").folded, [], "a reopened widget is not folded");

  // Automatic opening and folding: no focus move, a cover stays, nothing else changes, and a no-op is the same object.
  state = run(s => openWidget(s, "terminal"), s => openCover(s, "bot-settings"));
  const auto = autoOpenWidget(state, "computer");
  assert.deepEqual(auto.widgets, ["computer", "terminal"], "an automatic open keeps the fixed order");
  assert.equal(auto.cover, "bot-settings", "an open cover stays");
  assert.equal(autoOpenWidget(run(s => openPage(s, "settings")), "computer").page, "settings", "the Settings page stays when a widget opens by itself");
  assert.deepEqual(auto.focus, state.focus, "an automatic open does not bump focus");
  assert.equal(autoOpenWidget(auto, "computer"), auto, "already open: same object");
  assert.equal(setWidgetFolded(idle, "computer", true), idle, "a closed widget cannot be folded");
  const folded = setWidgetFolded(auto, "computer", true);
  assert.deepEqual(folded.folded, ["computer"]);
  assert.equal(setWidgetFolded(folded, "computer", true), folded, "folding a folded widget: same object");
  assert.deepEqual(setWidgetFolded(folded, "computer", false).folded, []);
  assert.deepEqual(folded.focus, auto.focus, "an automatic fold does not bump focus");

  // Sidebar: one at a time, replace keeps the width, expand toggles, close.
  state = run(s => openSidebar(s, "tasks"));
  assert.deepEqual(state.sidebar, { id: "tasks", expanded: false });
  state = toggleSidebarExpanded(state);
  assert.deepEqual(state.sidebar, { id: "tasks", expanded: true });
  state = openSidebar(state, "memory");
  assert.deepEqual(state.sidebar, { id: "memory", expanded: true }, "replacing keeps the expanded track");
  assert.equal(toggleSidebar(state, "memory").sidebar, null, "the same toggle closes it");
  assert.deepEqual(toggleSidebar(state, "terminal").sidebar, { id: "terminal", expanded: true }, "another toggle replaces");
  assert.equal(closeSidebar(state).sidebar, null);
  assert.deepEqual(openSidebar(closeSidebar(state), "tasks").sidebar, { id: "tasks", expanded: false }, "a fresh open starts narrow");
  assert.equal(toggleSidebarExpanded(idle), idle, "nothing to expand");
  assert.equal(closeSidebar(idle), idle);
  assert.deepEqual(run(s => openSidebar(s, "tasks"), s => openWidget(s, "computer")).sidebar, { id: "tasks", expanded: false }, "opening a widget leaves the sidebar alone");
  assert.deepEqual(run(s => openWidget(s, "computer"), s => openSidebar(s, "tasks")).widgets, ["computer"], "opening a sidebar leaves widgets alone");

  // Cover: exclusive, keeps the others for when it closes, replaces another cover, never stacks.
  state = run(s => openWidget(s, "computer"), s => openWidget(s, "artifacts"), s => foldWidget(s, "artifacts"), s => openSidebar(s, "tasks"), s => openCover(s, "bot-settings"));
  assert.equal(state.cover, "bot-settings");
  assert.deepEqual(shape(closeCover(state)), { widgets: ["computer", "artifacts"], folded: ["artifacts"], sidebar: { id: "tasks", expanded: false }, cover: null }, "closing the cover brings everything back as it was");
  assert.equal(openCover(state, "artifacts").cover, "artifacts", "from one cover to another is a replace");
  assert.equal(closeCover(idle), idle);
  assert.equal(openWidget(state, "terminal").cover, null, "asking for a widget closes the cover");
  assert.equal(openSidebar(state, "memory").cover, null, "asking for a sidebar closes the cover");
  assert.deepEqual(openSidebar(state, "memory").widgets, ["computer", "artifacts"]);

  // Close everything keeps the focus counter moving forward.
  state = run(s => openWidget(s, "computer"), s => openSidebar(s, "tasks"));
  const cleared = closeAll(state);
  assert.deepEqual(shape(cleared), shape(empty()));
  assert.equal(cleared.focus.n, state.focus.n);
  assert.equal(closeAll(idle), idle);
  assert.equal(isEmpty(cleared), true);
  assert.equal(isEmpty(state), false);

  // Escape order (section 7, after native dialogs and Radix layers, which the hook leaves to themselves): the Settings page (a full page, not a cover),
  // then the big computer steps back to compact, then the cover, then the sidebar, then the widgets from the bottom up (Artifacts, Members, Computer).
  const everything = () => run(s => openWidget(s, "computer"), s => openWidget(s, "terminal"), s => openWidget(s, "artifacts"), s => openSidebar(s, "memory"), s => openPage(s, "settings"));
  const walk = (start, options) => {
    let current = start;
    const seen = [];
    for (let step = 0; step < 10; step++) {
      const target = escapeTarget(current, options);
      if (!target) break;
      seen.push(target.layer === "computer-big" ? "computer-big" : `${target.layer}:${target.id}`);
      current = target.layer === "computer-big" ? options.afterBig(current) : target.layer === "cover" || target.layer === "page" ? closeCover(current) : target.layer === "sidebar" ? closeSidebar(current) : closeWidget(current, target.id);
    }
    return seen;
  };
  assert.deepEqual(walk(everything(), {}), ["page:settings", "sidebar:memory", "widget:artifacts", "widget:terminal", "widget:computer"]);
  const afterBig = { bigComputer: true, afterBig: current => { afterBig.bigComputer = false; return current; } };
  assert.deepEqual(walk(everything(), afterBig), ["page:settings", "computer-big", "sidebar:memory", "widget:artifacts", "widget:terminal", "widget:computer"], "the Settings page first (it covers the big computer), then the big computer, sidebar, then widgets bottom-up");
  assert.deepEqual(escapeTarget(run(s => openSidebar(s, "tasks"), s => openWidget(s, "computer")), { bigComputer: true }), { layer: "computer-big" });
  assert.deepEqual(escapeTarget(run(s => openSidebar(s, "tasks")), { bigComputer: true }), { layer: "sidebar", id: "tasks" }, "a big computer that is not an open widget is not a target");
  assert.deepEqual(escapeTarget(run(s => openWidget(s, "terminal"), s => openWidget(s, "computer"))), { layer: "widget", id: "terminal" }, "the Bot computer is last");
  assert.equal(escapeTarget(idle), null);
  // The Settings page is exclusive like a cover: anything else that opens, or a conversation that is entered, closes it; closeCover leaves it too.
  const onPage = run(s => openWidget(s, "terminal"), s => openPage(s, "settings"));
  assert.equal(onPage.page, "settings");
  assert.equal(onPage.cover, null);
  assert.equal(openCover(onPage, "bot-settings").page, null, "a cover replaces the page");
  assert.equal(openPage(openCover(onPage, "bot-settings"), "settings").cover, null, "the page replaces a cover");
  assert.equal(openWidget(onPage, "artifacts").page, null);
  assert.equal(openSidebar(onPage, "tasks").page, null);
  assert.equal(closeCover(onPage).page, null);
  assert.equal(isEmpty(openPage(idle, "settings")), false);
  assert.equal(isEmpty(closeAll(onPage)), true);

  // Widgets are remembered per conversation; the sidebar stays and re-scopes; a cover closes; suppressed runs outlive the widgets.
  const memory = new Map();
  const inA = run(s => openWidget(s, "computer"), s => openWidget(s, "artifacts"), s => foldWidget(s, "artifacts"), s => openSidebar(s, "tasks"), s => toggleSidebarExpanded(s), s => openCover(s, "bot-settings"));
  stashWidgets(memory, "A", inA);
  const inB = enterConversation(inA, memory, "B");
  assert.deepEqual(shape(inB), { widgets: [], folded: [], sidebar: { id: "tasks", expanded: true }, cover: null }, "a conversation with nothing remembered opens empty; the sidebar stays and a cover closes");
  stashWidgets(memory, "B", openWidget(inB, "terminal"));
  const backInA = enterConversation(openWidget(inB, "terminal"), memory, "A");
  assert.deepEqual(shape(backInA), { widgets: ["computer", "artifacts"], folded: ["artifacts"], sidebar: { id: "tasks", expanded: true }, cover: null }, "back in A its widgets and folds come back");
  assert.deepEqual(enterConversation(backInA, memory, "B").widgets, ["terminal"], "B kept its own");
  assert.deepEqual(enterConversation(backInA, memory, null).widgets, [], "no conversation, no widgets");
  assert.equal(enterConversation(openSidebar(idle, "archive"), memory, "A").sidebar.id, "archive", "Archive stays");
  suppressedRunsOf(memory, "C").add("run-1");
  stashWidgets(memory, "C", idle);
  assert.ok(memory.get("C").suppressedRuns.has("run-1"), "closed-in runs survive an empty conversation");
  stashWidgets(memory, "A", idle);
  assert.equal(memory.has("A"), false, "an empty record with no suppressed runs is dropped");
  forgetWidgets(memory, "C");
  assert.equal(memory.has("C"), false);
  assert.equal(suppressedRunsOf(memory, "C").size, 0);

  // The order is fixed (V3 5.3): whatever the opening order, the column draws Bot card, computer, Terminal, read-only chat, Artifacts.
  const ALL = ["artifacts", "terminal", "computer"];
  const four = run(...ALL.map(id => s => openWidget(s, id)));
  assert.deepEqual(four.widgets, ["computer", "terminal", "artifacts"], "opened in any order, drawn in the fixed one");
  assert.equal("order" in four, false, "there is no saved order");
  assert.equal("moveWidget" in S, false, "widgets are not reordered");
  assert.deepEqual(S.sortWidgets(["artifacts", "bot", "computer"]), ["bot", "computer", "artifacts"], "the Bot card is always first");
  const memo = new Map();
  stashWidgets(memo, "A", four);
  assert.deepEqual(enterConversation(idle, memo, "A").widgets, ["computer", "terminal", "artifacts"], "a remembered set comes back in the fixed order");
  // Space rule: from the bottom of the order up, never the computer, never one the person unfolded; the first fold is unconditional.
  assert.equal(spaceFoldTarget(run(s => openWidget(s, "computer"), s => openWidget(s, "terminal")), { overflow: true }), null, "two widgets: nothing to fold");
  assert.equal(spaceFoldTarget(run(s => openWidget(s, "computer"), s => openWidget(s, "terminal"), s => openWidget(s, "artifacts")), { overflow: false }), "artifacts", "three: Artifacts first");
  const artifactsFolded = setWidgetFolded(four, "artifacts", true);
  assert.equal(spaceFoldTarget(four, { overflow: false }), "artifacts", "three: Artifacts first");
  assert.equal(spaceFoldTarget(artifactsFolded, { overflow: false }), null, "the next fold waits for an overflow");
  assert.equal(spaceFoldTarget(artifactsFolded, { overflow: true }), "terminal");
  assert.equal(spaceFoldTarget(artifactsFolded, { overflow: true, skip: new Set(["terminal"]) }), null, "one the person unfolded is skipped");
  assert.equal(spaceFoldTarget(setWidgetFolded(setWidgetFolded(four, "artifacts", true), "terminal", true), { overflow: true }), null, "the Bot computer never folds this way");
  assert.deepEqual(escapeTarget(four), { layer: "widget", id: "artifacts" }, "Escape closes from the bottom up");
  assert.deepEqual(escapeTarget(run(s => openWidget(s, "computer"))), { layer: "widget", id: "computer" }, "alone, the computer closes");

  console.log("surfaces ok");
} finally {
  await rm(output, { recursive: true, force: true });
}
