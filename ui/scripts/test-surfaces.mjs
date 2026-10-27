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
  const { emptySurfaces: empty, openWidget, closeWidget, toggleWidget, foldWidget, openSidebar, closeSidebar, toggleSidebar, toggleSidebarExpanded, autoOpenWidget, setWidgetFolded, openCover, closeCover, closeAll, escapeTarget, applyPanel, isEmpty, WIDGET_ORDER } = S;
  const shape = s => ({ widgets: s.widgets, folded: s.folded, sidebar: s.sidebar, cover: s.cover });
  const run = (...steps) => steps.reduce((state, step) => step(state), empty());

  // Fixed order, whatever the opening order; one of each; at most three.
  assert.deepEqual(WIDGET_ORDER, ["computer", "members", "artifacts"]);
  const orders = [["artifacts", "members", "computer"], ["members", "artifacts", "computer"], ["computer", "artifacts", "members"], ["artifacts", "artifacts", "computer", "computer"]];
  for (const opened of orders) {
    const state = run(...opened.map(id => s => openWidget(s, id)));
    assert.deepEqual(state.widgets, [...WIDGET_ORDER].filter(id => opened.includes(id)), `order for ${opened}`);
    assert.ok(state.widgets.length <= 3);
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

  // Escape order: cover, then sidebar, then the lowest widget; the computer goes last.
  state = run(s => openWidget(s, "computer"), s => openWidget(s, "members"), s => openWidget(s, "artifacts"), s => openSidebar(s, "memory"), s => openCover(s, "settings"));
  const escapes = [];
  for (let step = 0; step < 8; step++) {
    const target = escapeTarget(state);
    if (!target) break;
    escapes.push(`${target.layer}:${target.id}`);
    state = target.layer === "cover" ? closeCover(state) : target.layer === "sidebar" ? closeSidebar(state) : closeWidget(state, target.id);
  }
  assert.deepEqual(escapes, ["cover:settings", "sidebar:memory", "widget:artifacts", "widget:members", "widget:computer"]);
  assert.equal(escapeTarget(idle), null);

  // The setPanel adapter, wide web layout: old ids to tiers.
  const adapted = [
    ["desktop", s => s.widgets, ["computer"]],
    ["members", s => s.widgets, ["members"]],
    ["artifacts-group", s => s.widgets, ["artifacts"]],
    ["artifacts-bot", s => s.widgets, ["artifacts"]],
    ["artifacts", s => s.cover, "artifacts"],
    ["bot-edit", s => s.cover, "bot-settings"],
    ["group-create", s => s.cover, "new-group"],
    ["settings", s => s.cover, "settings"],
    ["schedule", s => s.sidebar?.id, "tasks"],
    ["memory", s => s.sidebar?.id, "memory"],
    ["terminal", s => s.sidebar?.id, "terminal"],
    ["archive", s => s.sidebar?.id, "archive"],
  ];
  for (const [panel, read, want] of adapted) assert.deepEqual(read(applyPanel(idle, panel, true)), want, `wide: ${panel}`);
  // Narrow window: widgets other than the computer become covers.
  assert.equal(applyPanel(idle, "members", false).cover, "group-settings");
  assert.deepEqual(applyPanel(idle, "members", false).widgets, []);
  assert.equal(applyPanel(idle, "artifacts-group", false).cover, "artifacts");
  assert.deepEqual(applyPanel(idle, "desktop", false).widgets, ["computer"], "the computer is a floating window there, still a widget in state");
  assert.equal(applyPanel(idle, "file", true), idle);
  state = run(s => openWidget(s, "computer"), s => openSidebar(s, "tasks"), s => openCover(s, "settings"));
  assert.ok(isEmpty(applyPanel(state, null, true)), "null closes every tier");

  console.log("surfaces ok");
} finally {
  await rm(output, { recursive: true, force: true });
}
