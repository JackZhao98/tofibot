import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";
// Load through Vite so the module's i18n catalogs resolve like the app.
const ui = await openUiModules({ language: "zh-CN" });
try {
  const { buildQuestionTimeline, reconcileQuestion } = await ui.load("/src/questionTimeline.ts");
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
  const answered = { ...q, status: "answered", answer: true, updated_at: "2026-09-17T00:00:00.000000501Z" };
  const expired = { ...q, status: "expired", updated_at: "2026-09-17T00:00:00.000000502Z", outcome: { next_action: "renew_approval" } };
  assert.equal(reconcileQuestion(expired, answered), expired, "new backend expiry replaces the locally answered decision");
  assert.equal(reconcileQuestion({ ...q, updated_at: q.created_at }, answered), answered, "older polling response cannot reopen an answered card");
  assert.equal(reconcileQuestion({ ...expired, updated_at: undefined }, { ...answered, updated_at: undefined }).status, "expired", "unversioned legacy expiry stays visible");
  console.log("question timeline checks: PASS (nanosecond boundary, answer stability, pagination, question-only history, deterministic ties)");
} finally { await ui.close(); }
