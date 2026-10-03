# Astra review follow-up

Private branch `codex/tool-runtime-review-fixes-20261002` starts at immutable
`517b9c488d7601dea441187bac4e293d59e80e25`. Worktree:
`/Users/jackzhao/Documents/Codex/2026-10-02/task-2/runtime-core`.
The follow-up is prepared for rereview; no push, merge or deployment is included.
The installer checkout and VM106 are untouched.

| Review issue | Final behavior and implementation | Exact regression |
| --- | --- | --- |
| c43ec03 P1: symlink retarget after lookup replayed an uncertain append | Runtime transports the resolved identity outside model arguments. Guest opens existing files without truncation, verifies the opened descriptor against that identity, and writes through the same descriptor. New files use a verified parent descriptor and exclusive creation. Identity mismatch returns typed `write_identity_changed/not_executed`; no unguarded fallback runs. | `TestReviewerUncertainWritePathChanges/lookup-before-dispatch` retains the exact reviewer schedule: one append to a, lookup of alias to b, actor retargets alias to a. Guest rejects the second request and a remains X. `TestGuardedGuestRetargetAfterOpenWritesOnlyBoundDescriptor` replaces alias and physical path after opening; only the held original inode changes. `TestGuardedGuestMismatchHasNoMutationOrLegacyFallback` covers replaced file, parent, unexpected entry and symlink. |
| c43ec03 P1: postfailure lookup replaced original effect evidence | Postfailure lookup is removed. The first dispatch identity is immutable; later observations append deduplicated evidence and never narrow its fence. Unknown create attribution and legacy/unbound evidence remain conservative for the write operation. | `TestReviewerUncertainWritePathChanges/postfailure-replaces-evidence`: append through alias to a, lose response, retarget b, retry direct a; a stays X. `TestEffectEvidenceIsCumulativeAcrossCompactionAndCheckpoint` preserves original a plus later b/c evidence, blocks all three and permits d. `TestRetargetedWriteFenceSurvivesGuestRestartAndCheckpointReload` covers repeated retargets, disk reload and fresh Guest for bound existing files, uncertain creates, older guests and failed lookups; reads/list execute in every case, distinct existing b only for bound a. |
| Rereview P1: refused reserved-repair call invented a completed receipt | Reserved-repair refusals carry trusted `ToolFailed` and `reserved_repair_refused/not_executed` metadata. The event tracker also prevents any queued, never-started call from becoming completed. The audit adds failure flags to legacy parallel, shell, skill and MCP error branches. | `TestReservedRepairRefusalCannotInventCompletionReceipt`: actual runtime and durable Store, invalid first receipt + refused second receipt => zero executions and `HasCompletedTool=false`; valid first + refused second => one execution and a real completed receipt. `TestNeverStartedToolCannotBecomeCompleted` checks the tracker backstop. |
| Rereview P2: operation-wide uncertainty fence blocked observations and distinct targets; known argument errors were uncertain | Wrapper identities distinguish effective operations and backend-owned risk. Known pre-dispatch argument/access failures carry typed validation/denied/permanent outcomes. Observations fence equivalent requests. VM file writes bind physical paths and device/inode identity to the guest descriptor. Distinct verified targets remain available after an uncertain existing-file write; unknown creates stay conservative. Equivalent paths, symlinks, hardlinks and changed/ignored arguments remain fenced. | `TestExtensionPredispatchValidationAllowsListAndCorrectedUpdate`, `TestFileObservationFailuresDoNotBlockDifferentRequests`, `TestUncertainFileMutationAllowsVerifiedDistinctTarget`, `TestUnavailableFileIdentityCannotRelaxUncertainFence`, `TestLostNewFileResponseCannotReplayThroughCreatedHardlink`, `TestActionWrappersHaveSeparateRecoveryOperations`, `TestMissingSecretReferenceDoesNotFenceCorrectedReference`, `TestResolvedRecoveryTargetsAndRiskSurviveCheckpoint`, `TestFileIdentityResolvesAliasesAndMissingTargetsWithoutMutation`, `TestFileIdentityHTTPDoesNotCreateWorkspaceOrAlias`. |
| P1: raw wrapper arguments allowed uncertain append replay using ignored `offset` or `computer_action` | `runtime.Tool.Identity` delegates to the backend's execution parser. `computerRecoveryIdentity` maps convenience wrappers and generic computer actions to shared scope/operation/argument identities. `runtime.Tool.ResolveIdentity` resolves mutable backend targets before the final recovery check, including after hooks. These identities remain in the recovery ledger across checkpoint serialization. | `TestRuntimeEffectiveComputerReplayCannotUseIgnoredFieldsOrAlias`: actual runtime and app computer parsers dispatch one append whose response is lost; ignored-offset, generic-action and alternate-path attempts remain blocked; one file read still executes. `TestUncertainEffectCannotReplayAfterCompactionAndResumedCheckpoint` checks the retained fence across compaction and suspension. |
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

The new guest `files.identity` action is internal and is absent from the public
tool catalog. Its HTTP lifecycle bypasses workspace/alias creation; tests verify
that the lookup creates neither a Bot directory nor an alias and reads no file
content. The actual guest HTTP integration appends a new file, loses the response,
creates a hardlink, and blocks both that alias and further writes because the
original inode attribution is unknown. The stable-file Guest test writes existing
and new files once. A new guest artifact is required for descriptor binding.
An older/offline guest permits ordinary first calls but cannot establish distinct
targets after uncertainty, so that operation remains fenced. Paired-Mac mutations,
arbitrary shell/secret operations and unreviewed MCP tools also remain opaque.
Existing exact owner-reviewed MCP read-only entries retain observation risk;
remote annotations and unseen capabilities do not gain it. No settings or
allowlists are modified by this change.

The reviewer overlay's race schedules are preserved in
`internal/app/runtime_reviewer_race_test.go`. Its original counter incremented
for every HTTP write request, including a rejected request. The committed test
counts successful Guest mutations separately: lookup-before-dispatch produces
two requests, one HTTP 409 rejection, one mutation, and a remains X. The second
race produces one request/mutation because the retained ledger blocks replay
before dispatch. This distinguishes a failed request from a duplicate effect.

## Finite descriptor/evidence gate

| Acceptance | Evidence |
| --- | --- |
| Invalid first receipt + refused second: zero executions, false completion | `TestReservedRepairRefusalCannotInventCompletionReceipt` |
| Stable existing and new files write once | `TestGuardedGuestStableExistingAndNewFileWriteOnce` |
| Lost response: aliases, ignored arguments, symlinks and hardlinks stay fenced | Existing effective-identity regressions; actual Guest hardlink and restart tests |
| Both exact reviewer races retain original a=X | `TestReviewerUncertainWritePathChanges` |
| Changed file/parent/entry rejects with no content mutation or fallback | `TestGuardedGuestMismatchHasNoMutationOrLegacyFallback` |
| Distinct verified existing target and independent reads remain usable | `TestRetargetedWriteFenceSurvivesGuestRestartAndCheckpointReload/bound-existing` |
| Uncertain create with unknown inode stays conservative | Same test `/uncertain-create`; actual new-file hardlink test |
| Older guest, failed lookup, cumulative evidence and checkpoint retain fence | Same test `/older-guest` and `/failed-lookup`; `TestEffectEvidenceIsCumulativeAcrossCompactionAndCheckpoint` |

## Verification

- Full Go suite: `GOCACHE=/tmp/tofi-runtime-go-cache go test -timeout=120s ./...`.
  All packages pass; local test listeners required sandbox escalation.
- [Finite descriptor/evidence gate and affected package checks](descriptor-gate.txt)
  pass. Final Linux amd64 guest cross-build passes; no Linux/KVM execution is claimed.
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
