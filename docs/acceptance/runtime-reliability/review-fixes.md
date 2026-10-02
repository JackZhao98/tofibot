# Astra review follow-up

Private branch `codex/tool-runtime-review-fixes-20261002` starts at immutable
`517b9c488d7601dea441187bac4e293d59e80e25`. Worktree:
`/Users/jackzhao/Documents/Codex/2026-10-02/task-2/runtime-core`.
The follow-up is prepared for rereview; no push, merge or deployment is included.
The installer checkout and VM106 are untouched.

| Review issue | Final behavior and implementation | Exact regression |
| --- | --- | --- |
| P1: raw wrapper arguments allowed uncertain append replay using ignored `offset` or `computer_action` | `runtime.Tool.Identity` delegates to the backend's execution parser. `computerRecoveryIdentity` maps convenience wrappers and generic computer actions to shared scope/operation/argument identities. The agent checks this identity immediately before dispatch, including after hooks, and persists it in the recovery ledger. Unresolved uncertainty fences the operation regardless of changed arguments or path aliases. | `TestRuntimeEffectiveComputerReplayCannotUseIgnoredFieldsOrAlias`: actual runtime and app computer parsers dispatch one append whose response is lost; ignored-offset, generic-action and alternate-path attempts remain blocked; one file read still executes. `TestUncertainEffectCannotReplayAfterCompactionAndResumedCheckpoint` checks the retained fence across compaction and suspension. |
| P2: locally answered question masked newer backend expiry | `QuestionCard` exposes `updated_at`. `reconcileQuestion` compares nanosecond backend/local versions, so later expiry supersedes the local answer and shows renewal. Older polling responses cannot reopen an answered card. | `test-question-timeline.mjs` covers ordering and legacy expiry. The actual App browser case `local-answer-then-backend-expiry-renews-fresh-card` submits an answer, receives expiry, shows renewal and creates the fresh card at both widths. |
| P2: wrapper-wide validation budget blocked independent observations | Repair records are keyed by backend scope and effective operation. Files read/write, desktop capture/type and browser snapshot/navigation have independent scopes. Guard-produced budget errors do not count as additional validation attempts. | `TestRepairBudgetScopesComputerOperationAndPreservesObservations`: three invalid writes followed by a valid read, then three invalid desktop types followed by a capture; both observations execute, while further write/type repairs remain blocked. |
| P2: successful outcome-shaped JSON became authoritative failure/recovery state | Only trusted executor error metadata supplies `Message.ToolOutcome` and `ToolFailed`. Agent recovery and runtime activity status do not parse successful text or JSON. Resume metadata comes from the backend approval binding, never the user's answer body. | `TestSuccessfulOutcomeShapedJSONNeverBlocksOrFailsToolCalls`: four repeated successful calls containing uncertain/denied/validation/permanent-looking JSON all execute and complete with no outcome metadata. `TestSuccessfulRemoteJSONCannotCreateRecoveryState` checks ledger construction. |
| P2: activity truncation destroyed the serialized outcome | Runtime events carry bounded structured `Outcome` separately. Activity migration adds `outcome_json`; recording, list/detail reads and restart recovery preserve it independently from truncated result text. The metadata envelope remains valid JSON, with explanation capped at 2,048 runes plus truncation marker. | `TestToolActivityOutcomeSurvivesLongResultAndReload`: a 50,000-character Unicode error becomes a truncated, invalid raw result envelope, but its valid uncertainty/verification metadata survives database reload. Successful JSON text does not acquire metadata. |
| Budget: duration wrap-up could be recorded as success | `AgentResult` and runtime `Result` carry `BudgetExhausted`/`BudgetReason`. The app atomically publishes partial output and sets the run to failed, producing runtime-owned `budget_exhausted` feedback and skipping success follow-ups. | `TestAssistantTurnCallbackCoversBudgetWrapUp` verifies the flag on a model wrap-up. `TestRunDurationExhaustionPreservesPartialAndDurableFailure` verifies ignored wrap-up, zero over-budget tool dispatches, one retained partial answer, failed status and duplicate-finish protection. Browser case `budget-failure-visible-with-preserved-partial-output` verifies the notice and partial text at both widths. |

Implementation entry points:

- `internal/app/computer_recovery.go`; identity providers in `microvm.go`,
  `computers.go` and `terminal.go`; `internal/agent/tool_recovery.go`.
- `internal/provider/provider.go`, `internal/agent/agent.go` and
  `internal/agent/continuation.go`; `internal/runtime/types.go` and `trace.go`.
- `internal/app/tool_activity.go`, restart activity scans in `app.go`, and
  `internal/tooloutcome/identity.go`.
- `internal/app/questions.go`, `ui/src/questionTimeline.ts` and `QuestionCard.tsx`.
- Budget flag propagation in `internal/runtime/runtime.go`, transactional
  `finishRunBudget` in `internal/app/app.go`, and `ui/src/runFamily.ts`.

The unresolved-effect fence is deliberately conservative: observation text
alone cannot establish a backend-verified absence of the effect or automatically
clear the current checkpoint's fence. This prevents a model from converting
untrusted returned prose into retry permission. It does not infer equivalence
between arbitrary shell programs and structured computer operations.

## Verification

- Full Go suite: `GOCACHE=/tmp/tofi-runtime-go-cache go test -timeout=120s ./...`.
  All packages pass; local test listeners required sandbox escalation.
- Web typecheck and both production bundles pass. Vite reports bundle-size
  warnings; there are no build failures.
- Question timeline, tool timeline, run-family, run-merge and user-form checks pass.
- Full-App Chromium matrix: 20/20, ten cases at each of 390×844 and 1280×900.
  [Exact case names](results.json), [matrix and screenshots](README.md),
  [Go output](go-test.txt), [Web output](web-checks.txt).
- `git diff --check` passes.

The browser fixture uses the canonical `App` and `OwnerSessionGate` with
in-memory API/SSE responses. It does not establish production relogin, an
Electron-client upgrade, Linux/KVM acceptance or deployment readiness.

## Authorized Auto Review availability check

The active TOFI Mac client's existing authorized session reported provider
`openai_codex`, Codex connected and actual model ID `codex-auto-review`.
The user explicitly approved current production session inspection and one
synthetic, tool-free shadow request. After a nonprivileged snapshot read could
not access the protected file (zero model requests), existing passwordless sudo
read only the current account's unexpired snapshot in the service-host process.
No credential was copied to the Mac, displayed, refreshed or written.

The probe sent the fixed [candidate request](auto-review-candidate-request.json)
to the adapter's existing endpoint and headers in memory. It was a one-shot
adapter-wire request, not a production run and not execution of the Go probe CLI.
The [sanitized actual result](auto-review-production-shadow-probe.json) records:

- HTTP 200, one request, zero tool calls, 97 input / 61 output tokens.
- Parsed model fields: `recommendation:"allow"` and the returned rationale.
- Separate guard fields: `execution_approved:false` in both the observation
  and shadow report, regardless of the model's recommendation.

Returned model fields and their validated schema are preserved in normalized
JSON. Raw SSE frames, headers, account identity and credentials are not retained.
This proves ordinary tool-free availability for one fixed fixture, not support
for an internal Codex boundary-review protocol or automatic approval quality.
No production permissions, settings, real approvals or allowlists changed, and
no further live calls are included.
