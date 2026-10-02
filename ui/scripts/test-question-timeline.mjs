import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import assert from "node:assert/strict";
const run = promisify(execFile);
const uiRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-question-timeline-"));
try {
  await run(join(uiRoot, "node_modules/.bin/tsc"), ["src/questionTimeline.ts", "src/types.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--jsx", "react-jsx", "--types", "vite/client", "--outDir", output, "--skipLibCheck", "--declaration", "false", "--pretty", "false"], { cwd: uiRoot });
  const { buildQuestionTimeline } = await import(pathToFileURL(join(output, "questionTimeline.js")));
  const message = (id, seq, created_at) => ({ id, seq, created_at, conversation_id: "c", role: "assistant", content: id });
  const question = (question_id, created_at, extra = {}) => ({ question_id, created_at, type: "question", question_type: "text", conversation_id: "c", bot_id: "b", run_id: "r", question: question_id, status: "pending", ...extra });
  const ids = timeline => timeline.map(item => item.kind === "message" ? item.message.id : item.question.question_id);
  const intro = message("intro", 1, "2026-09-17T00:00:00.000000100Z");
  const answer = message("answer", 2, "2026-09-17T00:00:00.000000900Z");
  const q = question("q", "2026-09-17T00:00:00.000000500Z");
  assert.deepEqual(ids(buildQuestionTimeline([intro, answer], [q], false)), ["intro", "q", "answer"], "question belongs before model's final answer even within the same millisecond");
  assert.deepEqual(ids(buildQuestionTimeline([intro, answer], [{ ...q, status: "answered", answer: "confirmed", updated_at: "2026-09-18T00:00:00Z" }], false)), ["intro", "q", "answer"], "answering must not move persisted question to the end");
  assert.deepEqual(ids(buildQuestionTimeline([answer], [q], true)), ["q", "answer"], "an older pending question must remain reachable from the task status");
  assert.deepEqual(ids(buildQuestionTimeline([answer], [{ ...q, status: "answered" }], true)), ["answer"], "resolved old questions stay behind pagination");
  assert.deepEqual(ids(buildQuestionTimeline([intro, answer], [q], true)), ["intro", "q", "answer"], "loading an older page restores its question boundary");
  assert.deepEqual(ids(buildQuestionTimeline([], [q], false)), ["q"], "question without a text turn remains visible");
  assert.deepEqual(ids(buildQuestionTimeline([intro], [question("z", q.created_at), question("a", q.created_at)], false)), ["intro", "a", "z"], "equal timestamps have deterministic IDs as tie breaker");
  assert.deepEqual(ids(buildQuestionTimeline([intro], [question("same", intro.created_at)], false)), ["intro", "same"], "same-time question comes after the turn that introduced it");
  console.log("question timeline checks: PASS (nanosecond boundary, answer stability, pagination, question-only history, deterministic ties)");
} finally { await rm(output, { recursive: true, force: true }); }
