import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-run-merge-"));

try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/runMerge.ts", "src/types.ts", "--ignoreConfig", "--target", "ES2022",
    "--module", "ESNext", "--moduleResolution", "Bundler", "--outDir", out,
    "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  const { mergeRunUpdates } = await import(pathToFileURL(join(out, "runMerge.js")));
  const queued = { id: "run", conversation_id: "conversation", bot_id: "bot", status: "queued", created_at: "2026-09-29T19:36:02.100000100Z", updated_at: "2026-09-29T19:36:02.100000100Z" };
  const running = { ...queued, status: "running", updated_at: "2026-09-29T19:36:02.100000101Z" };
  const completed = { ...running, status: "done", updated_at: "2026-09-29T19:36:08.000Z" };

  const afterSSE = mergeRunUpdates([queued], [running]);
  const afterLatePost = mergeRunUpdates(afterSSE, [queued]);
  assert.equal(afterLatePost[0].status, "running", "a queued POST snapshot cannot overwrite a later running SSE update");
  assert.equal(afterLatePost[0].updated_at, running.updated_at);
  assert.equal(Date.parse(queued.updated_at), Date.parse(running.updated_at), "the regression occurs inside one JavaScript millisecond");
  assert.equal(mergeRunUpdates(afterLatePost, [completed])[0].status, "done", "a later terminal update still wins");
  assert.equal(mergeRunUpdates([{ ...running, status: "waiting" }], [{ ...running, status: "running", updated_at: "2026-09-29T19:36:02.100000102Z" }])[0].status, "running", "a strictly newer waiting-to-running transition remains valid");
  assert.equal(mergeRunUpdates(afterLatePost, [{ id: "run", status: "queued", updated_at: running.updated_at }])[0].status, "running", "equal timestamps preserve the already-observed state");
  assert.deepEqual(mergeRunUpdates([queued], [{ id: "other", conversation_id: "conversation", bot_id: "bot", status: "queued", created_at: queued.created_at, updated_at: queued.updated_at }]).map(run => run.id), ["other", "run"], "new runs remain first");
  console.log("run merge checks: PASS (late queued POST, later terminal update, timestamp tie, ordering)");
} finally {
  await rm(out, { recursive: true, force: true });
}
