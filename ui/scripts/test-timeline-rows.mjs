// Timeline rows: a row is skipped when its model is unchanged (looking through containers the workspace rebuilds each
// render) and redrawn when anything it shows changes; the draft stream keeps streamed text out of workspace state.
import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();
try {
  const { looseEqual, rowModelEqual } = await modules.load("/src/timelineRow.ts");
  const message = { id: "m1", content: "hello" };
  const model = () => ({ message, timezone: "UTC", processTools: [{ id: "t1" }], errors: { r1: "x" }, receipt: { state: "read", readBy: ["a"] }, ownerNodes: undefined });
  assert.equal(rowModelEqual(model(), model()), true, "rebuilt arrays, maps and receipts of the same records are equal");
  assert.equal(rowModelEqual({ ...model(), message: { ...message } }, model()), true, "a copy of a record with the same content draws the same");
  assert.equal(rowModelEqual({ ...model(), message: { ...message, content: "hello!" } }, model()), false, "a record whose content changed is a change");
  assert.equal(rowModelEqual({ ...model(), timezone: "Asia/Tokyo" }, model()), false, "a changed scalar is a change");
  assert.equal(rowModelEqual({ ...model(), processTools: [{ id: "t1" }, { id: "t2" }] }, model()), false, "a longer list is a change");
  assert.equal(rowModelEqual({ ...model(), errors: { r1: "y" } }, model()), false, "a changed entry is a change");
  assert.equal(rowModelEqual({ ...model(), receipt: { state: "read", readBy: ["a", "b"] } }, model()), false, "a changed receipt is a change");
  assert.equal(rowModelEqual({ ...model(), extra: 1 }, model()), false, "an added field is a change");
  assert.equal(rowModelEqual({ ...model(), ownerNodes: [] }, { ...model(), ownerNodes: [] }), false, "rendered nodes are new every render: never equal");
  const records = new Map([["a", 1]]);
  assert.equal(looseEqual(records, new Map([["a", 1]])), false, "maps and sets compare by identity only");
  assert.equal(looseEqual(records, records), true);
  assert.equal(looseEqual({ a: { b: { c: 1 } } }, { a: { b: { c: 1 } } }, 2), false, "beyond its depth it compares by identity");

  const { DraftStream } = await modules.load("/src/draftStream.ts");
  const stream = new DraftStream();
  const draft = (content, revision = 1) => ({ run_id: "r", conversation_id: "c", bot_id: "b", message_id: "m", content, status: "active", revision, created_at: "t", updated_at: "t" });
  const seen = [];
  const off = stream.subscribe("m", () => seen.push(stream.get("m")?.content));
  const first = draft("a");
  stream.reconcile({ m: first });
  assert.deepEqual(seen, ["a"], "a committed draft reaches its subscriber");
  stream.setLive("m", { content: "ab", revision: 2, updated_at: "t2" });
  assert.deepEqual(seen, ["a", "ab"], "growth reaches the subscriber without a state commit");
  stream.reconcile({ m: first, other: draft("z") });
  assert.equal(stream.get("m").content, "ab", "re-committing the same draft record does not rewind the live text");
  assert.equal(stream.materialize({ m: first }).m.content, "ab", "a snapshot carries the newest text");
  stream.reconcile({ m: draft("restarted", 1), other: draft("z") });
  assert.equal(stream.get("m").content, "restarted", "a replaced draft record takes over");
  stream.reconcile({ other: draft("z") });
  assert.equal(stream.get("m"), undefined, "a removed draft is dropped");
  assert.equal(seen.at(-1), undefined, "and its subscriber is told");
  off();
  stream.setLive("m", { content: "x", revision: 1, updated_at: "t" });
  assert.equal(seen.length, 4, "an unsubscribed listener hears nothing");
  console.log("timeline row checks: PASS");
} finally {
  await modules.close();
}
