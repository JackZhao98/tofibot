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
console.log("perf structure checks: PASS (stable connectEvents, gated ownership poll, slow computer-info poll)");
