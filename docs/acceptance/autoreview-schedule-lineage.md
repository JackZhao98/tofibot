# Scheduled AutoReview authorization lineage

This source slice captures original schedule creation/edit evidence, lifecycle
revisions, and immutable authorization snapshots for actual occurrences. It adds
bounded scheduled context to the existing AutoReview completion and claim checks.
The production operation registry remains empty and AutoReview remains off by
default. Schedule lineage proves evidence origin; it does not qualify an operation
or establish semantic approval of every generated scheduled instruction.

## Evidence boundaries

Native chat sources retain original user text, host ingress provenance, exact run
binding, and complete bounded captured context. Native schedule forms retain the
submitted fields, host request ID, action, and effective execution scope. Form
request IDs are typed schedule references, never message IDs. Bot assignments,
generated scheduled-task triggers, assistant replies, tool results, memories, and
summaries remain untrusted context. Existing source import markers override native
labels. Generic Store callers, legacy records, and missing/oversized sources do not
gain native authority.

Occurrence snapshots freeze the source revision chain and stable execution scope
in the schedule claim transaction. The recurring cursor is excluded from stable
scope, so advancing it does not revoke a current occurrence. Material content
changes and pause/resume/delete/archive-pause advance the authorization token.
Original creation and edit constraints remain available in every eligible later
snapshot. Resume cannot revive an old occurrence or its automatic answer.

The resolver admits one actual parentless schedule occurrence and ordinary durable
delegations/returns in the same account. It checks active membership and exact
assignment/result bindings, including shared-trigger fanout and nested logical
returns. A waiting or done ancestor can represent legitimate delegation; the
current execution target must be running. Failed, interrupted, cancelled,
retry-shaped, cyclic, ambiguous, incomplete, imported, or oversized ancestry is
ineligible. Necessary attachments, truncated/uncertain tool results, malformed tool
outcomes, and mismatched tool context fail closed. Bounds do not truncate authority
to force eligibility.

## Focused synthetic acceptance

`internal/app/schedule_authorization_test.go` covers journal migration, native
source capture, revision semantics, and atomic occurrence snapshots. Four families
in `internal/app/mcp_schedule_context_test.go` exercise acceptance groups 5–8:

| Family | Evidence |
| --- | --- |
| `TestScheduledMCPContextBoundedLineage` | Native chat/form roots; group, production single-recipient shared-trace message, legacy single-message/forward, and fanout delegates; waiting/done roots; nested returns; a cross-team/group return preserving inherited origin. Wrong account/bot/conversation/root, ambiguous single-recipient notice/trace bindings, missing provenance/parent/snapshot, cycles, ambiguous roots, retry/failure/Stop-shaped ancestry, limits, attachments, and invalid tool evidence spend zero reviewer requests. |
| `TestScheduledMCPQualifiedPolicyModesAndOneUse` | A synthetic qualifier explicitly requires the scheduled lineage and exact occurrence/target. Qualified allow claims once; shadow, off, and an empty registry produce zero effects. |
| `TestScheduledMCPRevisionAndProvenanceRecheck` | Content edit, pause, delete, archive, source/target membership removal, Stop, source import/reclassification/text/run changes, and pause/resume invalidate at completion and claim. A fresh native-resume occurrence is distinct and cannot revive the old card. Invalidations retain durable question events and clear automatic authority. |
| `TestScheduledMCPClaimOrderingAndExistingFences` | Revocation-first, claim-first, and concurrent transactions; no replay after claim/revocation; human decisions, expiry, settings epoch, restart, and interrupted review preserve existing fences. |

`TestScheduledMCPGateExecutesSyntheticToolOnce` additionally enters the actual
`approveMCPCall` gate with the static proposal, increments the synthetic effect
only after the gate returns successfully, and proves a second call spends neither
another reviewer request nor another execution claim. It opens no transport.

These fixtures use temporary Stores, real native input/schedule/assignment/return
writers, a static synthetic MCP configuration, and an in-process `reviewStub`.
Message delegation uses `AddBotMessages` with one recipient, so nested returns
and mutation cases exercise the production shared-trace notice shape. The legacy
`AddBotMessage` shape has a separate compatibility case.
They open no listener, prepare no MCP transport, dispatch no real tool, and access
no provider or credentials. The synthetic effect counter increments only after
the real durable execution claim returns one affected row. This establishes local
source/transaction behavior, not operation qualification, deployment readiness, or
live model quality.

The parent coordinates one focused race run of new/affected families after source
compilation. This acceptance record does not claim a test pass before that run;
the final handoff must record its command, result, and immutable commit/tree/patch
identifiers. Existing human-approval, expiry, Stop, imported-chat, schedule timing,
and transactional scheduling regressions remain required where affected.

## Revocation boundary

Completion and atomic claim reread the same account, revision, scope, snapshot,
source proofs, memberships, and current-run fence. Revocation before claim prevents
execution. An already-committed claim remains recorded; subsequent cancellation
cannot promise to undo its effect, and the decision cannot authorize replay.
Ordinary human approvals and historical results retain their existing behavior.

This change does not enable AutoReview, register a real operation, alter scheduling
modes or timing, import authority records, publish a release, or deploy a service.
Independent composition review is required before release consideration.
