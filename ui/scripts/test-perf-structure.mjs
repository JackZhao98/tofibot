// Structural guards for performance fixes that are invisible to behavior tests:
// - connectEvents must keep one identity for the Workspace's lifetime (a changing identity chained every old
//   render's message arrays together through keepActivityRun: ~89 KB retained per conversation switch);
// - the 1 s desktop-ownership poll and the computer-info poll must not run when no computer is in play.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const app = readFileSync(join(root, "src/App.tsx"), "utf8");

const start = app.indexOf("const connectEvents = useCallback(");
assert.ok(start > 0, "connectEvents is a useCallback");
const end = app.indexOf("\n  }, [", start);
const deps = /^\n  \}, \[([^\]]*)\]\);/.exec(app.slice(end));
assert.ok(deps, "connectEvents has a dependency list");
assert.equal(deps[1].trim(), "", "connectEvents must have empty deps and read handlers through a ref");

const presence = /useDesktopPresence\(([^;]*)\);/.exec(app);
assert.ok(presence, "App calls useDesktopPresence");
assert.ok(/computerInfo|desktopReady/.test(presence[1]), "desktop ownership polling is gated on the computer being in play");

const poll = app.slice(app.indexOf("const refreshComputer = async"), app.indexOf("const refreshComputer = async") + 900);
assert.ok(poll.includes("document.hidden"), "computer info polling skips hidden tabs");
assert.ok(!/setInterval\(\(\) => void refreshComputer\(\), 4_000\)/.test(app), "computer info is not a fixed 4 s interval");
const mapAt = app.indexOf("conversationItems.map((item, index) =>");
const anchorsAt = app.indexOf("const ownerAnchors = ");
const ownedAt = app.indexOf("const ownedBy = ");
assert.ok(mapAt > 0 && anchorsAt > 0 && ownedAt > 0, "timeline lookups exist");
assert.ok(anchorsAt < mapAt && ownedAt < mapAt, "owner anchors and ownedBy are derived once per render, outside the per-row map");
assert.equal(app.split("const ownerAnchors = ").length, 2, "ownerAnchors is defined once");

// Timeline rows are memoized and a streamed chunk never goes through workspace state.
const rowAt = app.indexOf("const MessageRow = memo(");
assert.ok(rowAt > 0, "the timeline row is a memo component");
const rowEnd = app.indexOf("rowModelEqual(previous.model, next.model))", rowAt);
assert.ok(rowEnd > rowAt, "the row is compared through rowModelEqual on its model, its handlers and its stream");
assert.ok(app.slice(rowAt, rowEnd).includes("useLiveDraft("), "the row follows its draft's live text itself");
const mapBody = app.slice(mapAt, app.indexOf("<ToolActivityList activities={toolTimeline.fallback}", mapAt));
assert.ok(mapBody.includes("<MessageRow ") && !mapBody.includes("<MessageBubble"), "the per-row map renders memoized rows, not bubbles");
assert.ok(!/drafts=\{drafts\}|stream=\{drafts\}/.test(mapBody), "the drafts record is not passed down to rows");
const deltaAt = app.indexOf('} else if (type === "delta") {');
const deltaBody = app.slice(deltaAt, app.indexOf('} else if (type === "run") {', deltaAt));
assert.ok(deltaBody.includes("draftStream.setLive("), "plain growth of a draft goes to the draft stream");
assert.ok((deltaBody.match(/setDrafts\(/g) ?? []).length === 1, "only a structural change of a draft goes through workspace state");
const handlersAt = app.indexOf("const rowHandlers = useMemo<RowHandlers>(");
assert.ok(handlersAt > 0 && /\}, \[\]\);/.test(app.slice(handlersAt, handlersAt + 1200)), "row handlers keep one identity for the Workspace's lifetime");

// A conversation that opens at its newest message draws the newest blocks first and the rest right after.
assert.ok(/const FIRST_PAINT_BLOCKS = \d+;/.test(app), "the first paint draws a fixed number of newest blocks");
assert.ok(mapBody.includes("if (index < timelineFrom) return null;"), "blocks before the first-paint window are skipped, not removed from the derivations");
assert.ok(app.includes("fullTimelineFor !== active.id && viewport.opensAtBottom && !taskFocus"), "only a visit that lands at the bottom is windowed");
console.log("perf structure checks: PASS (stable connectEvents, gated ownership poll, slow computer-info poll)");
