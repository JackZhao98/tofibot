# Tool runtime recovery

Source acceptance, 2026-10-02. This work is based on private core commit
`5aa8ab59fca42d4f600c98c8957e2aa8671851c3`, on branch
`codex/tool-runtime-reliability-20261002`. It does not establish deployment,
desktop-client, VM106 or real-account acceptance.

## Recovery contract

Backend-owned outcomes carry a version, status, code, execution certainty,
explanation and next action. They survive in tool activity and the provider
transcript. Recovery records are also retained separately from model context,
so compaction, human-input suspension and restart cannot erase retry/repair
limits or uncertainty.

| Outcome | Behavior |
| --- | --- |
| `need_approval` / `need_information` | Visible question card and durable parked continuation. Waiting does not consume active execution time or repair attempts. |
| `approval_expired` | Distinct from denial. The parked run remains waiting; renewal creates a fresh pending card with identical proposal scope. The old ID remains invalid. |
| `validation_error` | Return schema/argument feedback to the model before requesting approval or dispatching. Allow three failed repairs per effective tool, then explain the blocker. |
| `transient_failure` | Retry only tools already explicitly trusted as read-only by the owner: at most two retries (three attempts), within the existing tool deadline. Repeating an exhausted identical request is blocked. |
| `denied` / `permanent_failure` | Explain the failure and block an equivalent repeated request in the current run. |
| `uncertain_effect` | No automatic replay. Explain that the action may have happened and require target-state verification. An independent inspection tool remains available. |

Approval is still bound to the exact run, server configuration, remote tool and
argument payload. Approval claim precedes dispatch and checks the validity
window atomically. A recorded answer is not evidence that the action executed.
If it expires before dispatch, the consumed checkpoint can park again on the
expired card. Duplicate renewal and answer requests cannot create duplicate
effects. A checkpoint already claimed at a crash is interrupted, never replayed.

Missing model narration after 15 calls no longer discards useful calls or kills
the turn after three missing replies. The 300-step, active-duration, empty-response
and repeated-discovery bounds remain. Exhausted execution and stream failures
produce runtime-owned notices independent of a final model response. Stream
`INTERNAL_ERROR` retries are bounded and stop as soon as any response prefix has
been delivered.

The active conversation refreshes question state on question SSE events and on
reconnect. Initial/history snapshots do not replay failure notifications.

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

The official [Auto Review documentation](https://learn.chatgpt.com/docs/sandboxing/auto-review)
describes a sandbox-boundary reviewer. It does not establish a supported public
invocation contract for this TOFI provider adapter. Live model availability and
request/response compatibility remain unverified. The immediate blocker is a
current already-authorized TOFI auth snapshot/path; the existing snapshot must
not be refreshed by this probe. With one supplied, the safe next check is:

```sh
go run ./cmd/tofi-shadow-review -data-dir /existing/tofi/data -fixture public_search -timeout=30s
```

It makes at most one ordinary tool-free request and reports sanitized availability,
response dimensions, usage and schema-valid shadow advice. It does not establish
support for Codex's internal boundary-review protocol. No execution gate delegates
authorization to this model, and no trusted-read-only configuration was added.

## Verification

Regression tests use synthetic model endpoints, MCP servers, SQLite state or
in-memory HTTP/SSE responses. They use no user credentials, production state or
real approvals. The separate opt-in availability probe accesses an existing
credential only internally through the read-only accessor and never exports it.

- `GOCACHE=/tmp/tofi-runtime-go-cache go test -timeout=120s ./...` passes.
- Web `npm --prefix ui run build` and `typecheck` pass.
- Timeline, run-family, run-merge and user-form scripts pass. The user-form
  harness now includes the existing project's JSX and Vite type settings.
- Full production `App` and `OwnerSessionGate` render in Chromium at 390×844
  and 1280×900. All 16 cases pass with no page errors, stale alerts,
  snapshot notifications, horizontal overflow or execution endpoint requests.
- Go regressions cover expiry before/after answer, waiting restart, fresh-card
  renewal, duplicate submissions with exactly one synthetic remote effect,
  schema repair and exhaustion, bounded read retries, a lost write response,
  stream prefix protection and uncertainty across compaction plus restart.

UI results and screenshots: [acceptance evidence](acceptance/runtime-reliability/README.md).
The historical desktop toast flood is not reproduced as a current-core cause;
this evidence verifies canonical Web behavior with synthetic relogin/reconnect.
