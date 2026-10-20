import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();

try {
  const { draftDeltaApplies } = await modules.load("/src/draftDelta.ts");
  assert.equal(draftDeltaApplies(undefined, 1, true), true, "the first revision carries the draft and starts it");
  assert.equal(draftDeltaApplies(1, 2, false), true, "a text-only delta applies to the revision it follows");
  assert.equal(draftDeltaApplies(2, 2, false), false, "a replayed revision is ignored");
  assert.equal(draftDeltaApplies(5, 3, true), false, "an older snapshot never rewinds the draft");
  assert.equal(draftDeltaApplies(2, 5, false), false, "a gap without the whole draft waits for a resync point");
  assert.equal(draftDeltaApplies(undefined, 7, false), false, "a draft not yet seen is not built from a fragment");
  assert.equal(draftDeltaApplies(2, 16, true), true, "a resync point repairs any gap");
  assert.equal(draftDeltaApplies(undefined, 0, false), true, "an event without revision keeps the old append behaviour");
  console.log("draft delta checks passed");
} finally {
  await modules.close();
}
