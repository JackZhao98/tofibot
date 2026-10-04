import assert from "node:assert/strict";
import {execFile} from "node:child_process";
import {mkdtemp, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import {dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
import {promisify} from "node:util";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-auto-review-"));
try {
  await promisify(execFile)(join(ui, "node_modules/.bin/tsc"), ["src/questionTimeline.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--jsx", "react-jsx", "--types", "vite/client", "--outDir", output, "--skipLibCheck", "--strict", "--declaration", "false", "--pretty", "false"], {cwd: ui});
  const {reconcileQuestion, buildQuestionTimeline, autoReviewPresentation} = await import(pathToFileURL(join(output, "questionTimeline.js")));
  const approved = {question_id: "synthetic", conversation_id: "c", run_id: "r", bot_id: "b", type: "question", question_type: "approval", question: "Allow?", status: "answered", answer: true, answered_by: "auto-review", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:01.000001Z", approval: {action: "read", target: "public fact", impact: "read", review: {source: "auto-review", status: "approved", reason: "synthetic <script>untrusted</script>", model: "codex-auto-review"}}};
  const invalidated = {...approved, status: "pending", answer: undefined, answered_by: undefined, updated_at: "2026-01-01T00:00:01.000002Z", approval: {...approved.approval, review: {...approved.approval.review, status: "invalidated", reason: "Mode changed; human review required."}}};
  assert.deepEqual(reconcileQuestion(invalidated, approved), invalidated, "off-switch SSE must replace an old locally cached auto approval");
  assert.deepEqual(reconcileQuestion(approved, invalidated), invalidated, "older reconnect data must not restore automatic authorization");
  const human = {...approved, answered_by: "human", updated_at: "2026-01-01T00:00:02Z", approval: {...approved.approval, review: {...approved.approval.review, status: "human_required"}}};
  assert.equal(reconcileQuestion(human, approved).answered_by, "human", "human race retains the real decision source");
  const expired = {...approved, status: "expired", updated_at: approved.updated_at};
  assert.equal(reconcileQuestion(expired, approved).status, "expired", "expiry wins even for legacy equal-version snapshots");
  const restored = JSON.parse(JSON.stringify(approved));
  const timeline = buildQuestionTimeline([], [restored], false);
  assert.equal(timeline[0].question.answered_by, "auto-review");
  assert.equal(timeline[0].question.approval.review.reason, approved.approval.review.reason, "metadata stays plain data through reconnect");
  for (const [status, label] of [["setup_required", "配置缺口"], ["context_required", "上下文缺口"], ["unavailable", "审查不可用"], ["policy_denied", "策略判决拒绝"]]) {
    const blocked = {...approved, status:"cancelled", answer:undefined, answered_by:undefined, updated_at:"2026-01-01T00:00:03Z", approval:{...approved.approval, review:{...approved.approval.review,status}}};
    assert.equal(autoReviewPresentation(blocked).label, label, `${status} is not a generic approval demand`);
    assert.equal(reconcileQuestion(approved, blocked).status,"cancelled", "reconnect cannot reopen a blocked proposal");
  }
  assert.equal(autoReviewPresentation(expired).label,"提案已失效");
  assert.equal(autoReviewPresentation({...approved,status:"cancelled"}).label,"不可执行");
  assert.equal(autoReviewPresentation(human).label,"人工决定有效");
  const awaitingHuman = {...human,status:"pending",answered_by:undefined,answer:undefined};
  assert.equal(autoReviewPresentation(awaitingHuman).label,"策略需要人工决定");
  assert.equal(autoReviewPresentation({...awaitingHuman,approval:{...human.approval,review:{...human.approval.review,status:"not_eligible"}}}).label,"未获自动执行资格");
  console.log("PASS AutoReview UI reconnect, off switch, expiry and human decision provenance");
} finally {await rm(output, {recursive: true, force: true});}
