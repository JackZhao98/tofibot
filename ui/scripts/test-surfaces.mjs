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
  const { emptySurfaces: empty, openWidget, closeWidget, toggleWidget, foldWidget, openSidebar, closeSidebar, toggleSidebar, toggleSidebarExpanded, autoOpenWidget, setWidgetFolded, openCover, closeCover, closeAll, escapeTarget, isEmpty, WIDGET_ORDER, stashWidgets, suppressedRunsOf, forgetWidgets, enterConversation, moveWidget, normalizeWidgetOrder, sortWidgets, spaceFoldTarget } = S;
  const shape = s => ({ widgets: s.widgets, folded: s.folded, sidebar: s.sidebar, cover: s.cover });
  const run = (...steps) => steps.reduce((state, step) => step(state), empty());

  // The default order, whatever the opening order; one of each; at most four.
  assert.deepEqual(WIDGET_ORDER, ["computer", "members", "artifacts", "usage"]);
  assert.deepEqual(empty().order, [...WIDGET_ORDER], "a fresh state follows the default order");
  const orders = [["artifacts", "members", "computer"], ["members", "artifacts", "computer"], ["computer", "artifacts", "members"], ["artifacts", "artifacts", "computer", "computer"], ["usage", "artifacts", "members", "computer"]];
  for (const opened of orders) {
    const state = run(...opened.map(id => s => openWidget(s, id)));
    assert.deepEqual(state.widgets, [...WIDGET_ORDER].filter(id => opened.includes(id)), `order for ${opened}`);
    assert.ok(state.widgets.length <= 4);
  }

  // Close and toggle.
  assert.deepEqual(run(s => openWidget(s, "computer"), s => openWidget(s, "members"), s => closeWidget(s, "computer")).widgets, ["members"]);
  assert.deepEqual(run(s => toggleWidget(s, "artifacts")).widgets, ["artifacts"]);
  assert.deepEqual(run(s => toggleWidget(s, "artifacts"), s => toggleWidget(s, "artifacts")).widgets, []);
  const idle = empty();
  assert.equal(closeWidget(idle, "computer"), idle, "closing what is not open changes nothing (same object)");

  // Fold: only open widgets, content stays open; closing clears the fold; reopening an open one unfolds it and bumps focus.
  assert.equal(foldWidget(idle, "computer"), idle, "a closed widget cannot be folded");
  let state = run(s => openWidget(s, "computer"), s => openWidget(s, "members"), s => foldWidget(s, "computer"));
  assert.deepEqual([state.widgets, state.folded], [["computer", "members"], ["computer"]]);
  assert.deepEqual(foldWidget(state, "computer").folded, [], "fold toggles");
  const refocus = openWidget(state, "computer");
  assert.deepEqual([refocus.folded, refocus.widgets], [[], ["computer", "members"]]);
  assert.deepEqual(refocus.focus, { id: "computer", n: state.focus.n + 1 });
  assert.deepEqual(closeWidget(state, "computer").folded, [], "closing drops the fold");
  assert.deepEqual(openWidget(closeWidget(state, "computer"), "computer").folded, [], "a reopened widget is not folded");

  // Automatic opening and folding: no focus move, a cover stays, nothing else changes, and a no-op is the same object.
  state = run(s => openWidget(s, "members"), s => openCover(s, "settings"));
  const auto = autoOpenWidget(state, "computer");
  assert.deepEqual(auto.widgets, ["computer", "members"], "an automatic open keeps the fixed order");
  assert.equal(auto.cover, "settings", "an open cover stays");
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
  assert.equal(openWidget(state, "members").cover, null, "asking for a widget closes the cover");
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

  // Escape order (section 7, after native dialogs and Radix layers, which the hook leaves to themselves): the big computer steps back to
  // compact, then the cover, then the sidebar, then the widgets from the bottom up (Artifacts, Members, Computer).
  const everything = () => run(s => openWidget(s, "computer"), s => openWidget(s, "members"), s => openWidget(s, "artifacts"), s => openSidebar(s, "memory"), s => openCover(s, "settings"));
  const walk = (start, options) => {
    let current = start;
    const seen = [];
    for (let step = 0; step < 10; step++) {
      const target = escapeTarget(current, options);
      if (!target) break;
      seen.push(target.layer === "computer-big" ? "computer-big" : `${target.layer}:${target.id}`);
      current = target.layer === "computer-big" ? options.afterBig(current) : target.layer === "cover" ? closeCover(current) : target.layer === "sidebar" ? closeSidebar(current) : closeWidget(current, target.id);
    }
    return seen;
  };
  assert.deepEqual(walk(everything(), {}), ["cover:settings", "sidebar:memory", "widget:artifacts", "widget:members", "widget:computer"]);
  const afterBig = { bigComputer: true, afterBig: current => { afterBig.bigComputer = false; return current; } };
  assert.deepEqual(walk(everything(), afterBig), ["computer-big", "cover:settings", "sidebar:memory", "widget:artifacts", "widget:members", "widget:computer"], "big computer, cover, sidebar, then widgets bottom-up");
  assert.deepEqual(escapeTarget(run(s => openSidebar(s, "tasks"), s => openWidget(s, "computer")), { bigComputer: true }), { layer: "computer-big" });
  assert.deepEqual(escapeTarget(run(s => openSidebar(s, "tasks")), { bigComputer: true }), { layer: "sidebar", id: "tasks" }, "a big computer that is not an open widget is not a target");
  assert.deepEqual(escapeTarget(run(s => openWidget(s, "members"), s => openWidget(s, "computer"))), { layer: "widget", id: "members" }, "the computer is last");
  assert.equal(escapeTarget(idle), null);

  // Widgets are remembered per conversation; the sidebar stays and re-scopes; a cover closes; suppressed runs outlive the widgets.
  const memory = new Map();
  const inA = run(s => openWidget(s, "computer"), s => openWidget(s, "artifacts"), s => foldWidget(s, "artifacts"), s => openSidebar(s, "tasks"), s => toggleSidebarExpanded(s), s => openCover(s, "bot-settings"));
  stashWidgets(memory, "A", inA);
  const inB = enterConversation(inA, memory, "B");
  assert.deepEqual(shape(inB), { widgets: [], folded: [], sidebar: { id: "tasks", expanded: true }, cover: null }, "a conversation with nothing remembered opens empty; the sidebar stays and a cover closes");
  stashWidgets(memory, "B", openWidget(inB, "members"));
  const backInA = enterConversation(openWidget(inB, "members"), memory, "A");
  assert.deepEqual(shape(backInA), { widgets: ["computer", "artifacts"], folded: ["artifacts"], sidebar: { id: "tasks", expanded: true }, cover: null }, "back in A its widgets and folds come back");
  assert.deepEqual(enterConversation(backInA, memory, "B").widgets, ["members"], "B kept its own");
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

  // Reordering (D67 addendum): the order is the person's, applies to every open and every later open, and keeps the slots of closed widgets.
  const four = run(...WIDGET_ORDER.map(id => s => openWidget(s, id)));
  assert.deepEqual(four.widgets, [...WIDGET_ORDER]);
  const moved = moveWidget(four, "usage", 0);
  assert.deepEqual(moved.widgets, ["usage", "computer", "members", "artifacts"], "to the top");
  assert.deepEqual(moved.order, ["usage", "computer", "members", "artifacts"], "the saved order follows");
  assert.equal(moveWidget(moved, "usage", 0), moved, "already there: same object");
  assert.equal(moveWidget(idle, "usage", 0), idle, "a closed widget cannot be moved");
  assert.deepEqual(moveWidget(four, "computer", 99).widgets, ["members", "artifacts", "usage", "computer"], "past the end clamps to the bottom");
  assert.deepEqual(moveWidget(four, "members", -5).widgets, ["members", "computer", "artifacts", "usage"], "before the start clamps to the top");
  assert.deepEqual(moveWidget(four, "computer", 1).widgets, ["members", "computer", "artifacts", "usage"], "one step down");
  assert.deepEqual(moveWidget(four, "artifacts", 1).widgets, ["computer", "artifacts", "members", "usage"], "one step up");
  // Closed widgets keep their slots: with Members closed, moving Usage above Computer leaves Members where it was.
  let sparse = run(s => openWidget(s, "computer"), s => openWidget(s, "artifacts"), s => openWidget(s, "usage"));
  sparse = moveWidget(sparse, "usage", 0);
  assert.deepEqual(sparse.widgets, ["usage", "computer", "artifacts"]);
  assert.deepEqual(sparse.order, ["usage", "members", "computer", "artifacts"], "the closed Members keeps its slot between the open ones");
  const reopened = openWidget(sparse, "members");
  assert.deepEqual(reopened.widgets, ["usage", "members", "computer", "artifacts"], "a widget that opens later lands at its saved place");
  assert.deepEqual(autoOpenWidget(sparse, "members").widgets, ["usage", "members", "computer", "artifacts"], "so does one that opens by itself");
  assert.deepEqual(closeAll(moved).order, moved.order, "closing everything keeps the order");
  assert.deepEqual(closeWidget(moved, "usage").order, moved.order, "closing a widget keeps the order");
  // The order is the same in every conversation: entering one sorts what it remembers by the person's order.
  const memo = new Map();
  stashWidgets(memo, "A", four);
  assert.deepEqual(enterConversation(moved, memo, "A").widgets, ["usage", "computer", "members", "artifacts"], "a remembered set comes back in the current order");
  // A saved order from storage: junk dropped, repeats dropped, missing widgets appended in the default order.
  assert.deepEqual(normalizeWidgetOrder(null), [...WIDGET_ORDER]);
  assert.deepEqual(normalizeWidgetOrder("members"), [...WIDGET_ORDER]);
  assert.deepEqual(normalizeWidgetOrder(["artifacts", "bogus", "artifacts", 3, "computer"]), ["artifacts", "computer", "members", "usage"]);
  assert.deepEqual(normalizeWidgetOrder(["usage", "computer", "members", "artifacts"]), ["usage", "computer", "members", "artifacts"]);
  assert.deepEqual(sortWidgets(["members", "usage", "computer"], ["usage", "computer", "members", "artifacts"]), ["usage", "computer", "members"]);
  // Escape follows the person's order from the bottom up, and the computer still goes last wherever it sits.
  const flipped = moveWidget(moveWidget(four, "computer", 3), "usage", 0);
  assert.deepEqual(flipped.widgets, ["usage", "members", "artifacts", "computer"]);
  assert.deepEqual(escapeTarget(flipped), { layer: "widget", id: "artifacts" }, "the computer at the bottom is not the first to close");
  assert.deepEqual(escapeTarget(closeWidget(closeWidget(closeWidget(flipped, "usage"), "members"), "artifacts")), { layer: "widget", id: "computer" }, "alone, it closes");
  // Space rule: from the bottom of the order up, never the computer, never one the person unfolded; the first fold is unconditional.
  assert.equal(spaceFoldTarget(run(s => openWidget(s, "computer"), s => openWidget(s, "members")), { overflow: true }), null, "two widgets: nothing to fold");
  assert.equal(spaceFoldTarget(run(s => openWidget(s, "computer"), s => openWidget(s, "members"), s => openWidget(s, "artifacts")), { overflow: false }), "artifacts", "three, default order: Artifacts first");
  assert.equal(spaceFoldTarget(four, { overflow: false }), "usage", "four: Usage first, before Artifacts and Members");
  const usageFolded = setWidgetFolded(four, "usage", true);
  assert.equal(spaceFoldTarget(usageFolded, { overflow: false }), null, "the next fold waits for an overflow");
  assert.equal(spaceFoldTarget(usageFolded, { overflow: true }), "artifacts");
  assert.equal(spaceFoldTarget(usageFolded, { overflow: true, skip: new Set(["artifacts"]) }), "members", "one the person unfolded is skipped");
  assert.equal(spaceFoldTarget(setWidgetFolded(setWidgetFolded(usageFolded, "artifacts", true), "members", true), { overflow: true }), null, "the computer never folds this way");
  assert.equal(spaceFoldTarget(flipped, { overflow: false }), "artifacts", "follows the saved order: Artifacts is the lowest non-computer widget");
  assert.equal(spaceFoldTarget(moveWidget(four, "usage", 0), { overflow: false }), "artifacts", "Usage moved to the top is not the first to fold");

  console.log("surfaces ok");
} finally {
  await rm(output, { recursive: true, force: true });
}
