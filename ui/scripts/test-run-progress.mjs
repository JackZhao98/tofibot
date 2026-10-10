import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();

try {
  const { foldCompletedProgress } = await modules.load("/src/runProgress.ts");
  const run = { id: "run", conversation_id: "c", bot_id: "bot", status: "done", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" };
  const msg = (seq, kind, content) => ({ id: `m${seq}`, conversation_id: "c", seq, role: "assistant", ...(kind ? { kind } : {}), run_id: "run", content, created_at: "2026-10-01T00:00:00Z" });
  const ids = (list) => list.map(m => m.id);

  // Incident shape: an untagged answer mid-run (segment), a <progress> note (stored as kind progress), then an unrelated final message.
  const incident = [msg(1, "segment", "OK, changed to 2:30 daily."), msg(2, "progress", "checking the page"), msg(3, "", "Other work finished.")];
  const folded = foldCompletedProgress(incident, [run]);
  assert.deepEqual(ids(folded.visible), ["m1", "m3"], "untagged answer stays visible in order; <progress> note folds");
  assert.deepEqual(ids(folded.progressByFinalId.get("m3")), ["m2"]);

  const legacy = foldCompletedProgress([msg(1, "progress", "legacy"), msg(2, "", "final")], [run]);
  assert.deepEqual(ids(legacy.visible), ["m2"], "legacy progress rows keep folding");

  const mixed = foldCompletedProgress([msg(1, "progress", "a"), msg(2, "segment", "b"), msg(3, "progress", "c"), msg(4, "", "final")], [run]);
  assert.deepEqual(ids(mixed.visible), ["m2", "m4"], "only progress parts fold, in any position");
  assert.deepEqual(ids(mixed.progressByFinalId.get("m4")), ["m1", "m3"]);

  const running = foldCompletedProgress(incident, [{ ...run, status: "running" }]);
  assert.equal(running.visible.length, 3, "nothing folds while the run is not done");
  console.log("run progress checks: PASS (answer visible, <progress> folded, legacy folded, incident shape)");
} finally {
  await modules.close();
}
