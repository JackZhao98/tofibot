# Tool runtime recovery

Source acceptance, 2026-10-02. The original implementation was committed as
`517b9c488d7601dea441187bac4e293d59e80e25`, based on private core
`5aa8ab59fca42d4f600c98c8957e2aa8671851c3`. The review fixes are isolated on
`codex/tool-runtime-review-fixes-20261002`. These tests establish source and
canonical Web behavior; they do not establish deployment, desktop-client or
VM106 acceptance. The separately authorized live shadow probe below establishes
one tool-free Auto Review request's availability.

## Recovery contract

Backend-owned outcomes carry a version, status, code, execution certainty,
explanation and next action. Trusted executor errors carry this metadata
separately from result text through the provider transcript, runtime events and
the activity table's `outcome_json` column. A successful result, including JSON
that resembles an outcome, cannot classify a failure or create recovery state.
Activity metadata has its own bounded, valid JSON envelope after result truncation. Recovery records are also retained separately from model context,
so compaction, human-input suspension and restart cannot erase retry/repair
limits or uncertainty.

| Outcome | Behavior |
| --- | --- |
| `need_approval` / `need_information` | Visible question card and durable parked continuation. Waiting does not consume active execution time or repair attempts. |
| `approval_expired` | Distinct from denial. The parked run remains waiting; renewal creates a fresh pending card with identical proposal scope. The old ID remains invalid. |
| `validation_error` | Return schema/argument feedback to the model before requesting approval or dispatching. Allow three failed repairs per effective backend operation, then explain the blocker. |
| `transient_failure` | Retry only tools already explicitly trusted as read-only by the owner: at most two retries (three attempts), within the existing tool deadline. Repeating an exhausted identical request is blocked. |
| `denied` / `permanent_failure` | Explain the failure and block an equivalent repeated request in the current run. |
| `uncertain_effect` | No automatic replay. Explain that the action may have happened and require target-state verification. Independent inspection operations remain available. The current checkpoint cannot repeat an unresolved operation merely by changing arguments or path aliases; observations alone do not automatically clear the fence. |

Approval is still bound to the exact run, server configuration, remote tool and
argument payload. Approval claim precedes dispatch and checks the validity
window atomically. A recorded answer is not evidence that the action executed.
If it expires before dispatch, the consumed checkpoint can park again on the
expired card. Duplicate renewal and answer requests cannot create duplicate
effects. A checkpoint already claimed at a crash is interrupted, never replayed.

Missing model narration after 15 calls no longer discards useful calls or kills
the turn after three missing replies. The 300-step, active-duration, empty-response
and repeated-discovery bounds remain. Exhausted execution and stream failures
produce runtime-owned notices independent of a final model response. A budget
wrap-up carries an explicit exhaustion flag through the runtime. The backend
atomically publishes partial output and sets the run to `failed` with a durable
`budget_exhausted` explanation, without scheduling success follow-ups. Stream
`INTERNAL_ERROR` retries are bounded and stop as soon as any response prefix has
been delivered.

Computer convenience wrappers and `computer_action` share backend identities
for effective operations and arguments. Replay checks run again immediately
before dispatch after hooks, and the ledger retains these identities across
compaction and suspension. File reads, desktop captures and browser snapshots
have separate repair scopes from their corresponding mutations.

Question cards compare backend `updated_at` versions with the local answer
response. A later backend expiry replaces an answered decision and exposes
fresh approval renewal. The active conversation refreshes question state on
question SSE events and on reconnect. Initial/history snapshots do not replay failure notifications.

## Auto Review scope

Credential-free local model metadata identifies raw provider slug
`codex-auto-review`, displayed as **Codex Auto Review**. The Codex adapter used to
strip the prefix and send `auto-review`; it now preserves this exact slug while
keeping existing Sol/Astra mappings.

`internal/shadowreview` is an unregistered, tool-free evaluation seam accepting
only three fixed synthetic fixtures. Reports always set
`execution_approved:false`, including when a reviewer tries to return `true`.
Tests use a mock reviewer. The explicit `cmd/tofi-shadow-review` probe uses
the existing Codex adapter without custom endpoints, retries or token refresh.
Its credential accessor does not create directories or change permissions.
A local attempt stopped before HTTP because the discovered existing TOFI access
snapshot was expired: zero model requests, availability still `not_tested`.
See the [sanitized probe result](acceptance/runtime-reliability/auto-review-live-probe.json)
and [candidate request](acceptance/runtime-reliability/auto-review-candidate-request.json).

After the user explicitly authorized current-production session inspection and
one synthetic request, the existing Mac client session was read through its
local proxy. It reported an authenticated session, provider `openai_codex`, Codex
connected, and model ID `codex-auto-review`. A one-shot in-memory probe on the
existing TOFI service host read only the current account's protected, unexpired
snapshot. It sent the fixed candidate body to the adapter's existing
`https://chatgpt.com/backend-api/codex/responses` route with the existing adapter
headers. This was an adapter-wire probe, not execution of the Go probe CLI.

The request succeeded with HTTP 200: **one model request, zero tool calls,
97 input tokens and 61 output tokens**. The parsed output was:

```json
{"recommendation":"allow","rationale":"Searching a public documentation index is low-risk, has no stated side effects, and is not yet executed."}
```

The [sanitized production probe](acceptance/runtime-reliability/auto-review-production-shadow-probe.json)
preserves the normalized returned fields, schema, usage and observation. The
separate report and top-level record retain `execution_approved:false`.
Credentials were not exported or refreshed, permissions and persistent
production settings were not changed, and no real action or approval was
executed. No further live calls are part of this acceptance.

This establishes ordinary tool-free request availability for this model and
fixture. It does not establish support for Codex's internal boundary-review
protocol, approval-policy quality across real actions, or automatic execution
authorization. The evaluation seam remains unregistered, and no trusted-read-only
configuration or blanket allowlist was added.

## Verification

Regression tests use synthetic model endpoints, MCP servers, SQLite state or
in-memory HTTP/SSE responses. They use no user credentials, production state or
real approvals. The separate opt-in availability probe accesses an existing
credential only within the service host process and never exports it. The Go CLI tests use synthetic auth snapshots.

- `GOCACHE=/tmp/tofi-runtime-go-cache go test -timeout=120s ./...` passes.
- Web `npm --prefix ui run build` and `typecheck` pass.
- Timeline, run-family, run-merge and user-form scripts pass. The user-form
  harness now includes the existing project's JSX and Vite type settings.
- Full production `App` and `OwnerSessionGate` render in Chromium at 390×844
  and 1280×900. All 20 cases pass with no page errors, stale alerts,
  snapshot notifications, horizontal overflow or execution endpoint requests.
- Go regressions cover expiry before/after answer, waiting restart, fresh-card
  renewal, duplicate submissions with exactly one synthetic remote effect,
  schema repair and exhaustion, bounded read retries, a lost write response,
  stream prefix protection and uncertainty across compaction plus restart.
- Review regressions also cover ignored write arguments, `computer_action` and
  path-alias replay, independent reads/captures after repair exhaustion,
  successful outcome-shaped JSON, 50,000-character Unicode error truncation and
  reload, and duration wrap-up retaining partial output with terminal failure.

The [review mapping](acceptance/runtime-reliability/review-fixes.md) ties all five
Astra findings and the budget issue to implementation and exact tests.

UI results and screenshots: [acceptance evidence](acceptance/runtime-reliability/README.md).
The historical desktop toast flood is not reproduced as a current-core cause;
this evidence verifies canonical Web behavior with synthetic relogin/reconnect.
