# MCP tool tiers: reads run, writes are reviewed, high-risk asks a person

Branch `feat/mcp-tool-tiers`. This note is the contract a reviewer checks the code against; it is kept accurate as the code changes.

## 1. What changes for the user

Every tool of a connected service is placed in one of three tiers:

| Tier | Runtime behaviour | Example |
|---|---|---|
| `read_only` | Executes directly. No reviewer call, no card, in every AutoReview mode (off / shadow / auto). Transient failures are retried (the call has no side effect). | `search_pages`, `get_issue` |
| `write` | Unchanged from today: every call is a proposal. AutoReview `off` → human card; `shadow` → card plus advisory review; `auto` → reviewer decides allow / deny / needs_human / context_gap. | `create_page`, `update_issue` |
| `high_risk` | A person decides every call, in every mode. The reviewer model is never asked. In an unattended run (a scheduled run or a run whose ancestry roots in a schedule) the call is **not executed**; the model receives a named refusal (`mcp_high_risk_unattended`, certainty `not_executed`) and the run continues and reports. | `delete_page`, `send_message`, `place_order`, `share_with`, `rotate_key` |

Settings → Connections: each service card shows `Connected · 49 tools · 31 read only` (or `Classifying tools…` while the background pass runs) and a **Tools** disclosure listing tools grouped by tier (Read only / Can change data / High risk), each with a tier selector, an `Auto` / `Set by you` / `Not classified` marker, a `Changed` marker when the tool's schema or description moved since the tier was decided, and a **Re-check** action that refreshes the tool list and classifies what changed.

## 2. How a tier is decided

Sources, in order of authority:

1. **Owner override** (set in the UI). Always wins while it is bound to the tool's current digest.
2. **Auto tier**: one classification pass by the background review model over the tool's name, description, input schema and annotations, merged with the remote annotations as below.
3. **Fail closed**: no classification yet, classification failed, or the tool changed since it was classified → `write`.

Tool digest = sha256 of canonical JSON `{name, description, input_schema, annotations{readOnlyHint, destructiveHint}}`. Every tier decision is bound to the digest it was made for.

Merge of model tier × remote annotations (annotations are untrusted and can only tighten):

| Model says | `readOnlyHint` | explicit `destructiveHint=true` | Auto tier |
|---|---|---|---|
| read_only | absent / true | no | **read_only** |
| read_only | present and false | – | write (model and hint disagree → stricter) |
| read_only | – | yes | write (a tool that declares destruction is never read-only) |
| write | any | any | write (`readOnlyHint=true` cannot loosen) |
| high_risk | any | any | high_risk |
| unknown / missing / invalid | any | any | unclassified → write, retried on the next tool-list refresh |

"`readOnlyHint` present" means the remote sent a `ToolAnnotations` object for the tool; the SDK cannot distinguish an absent `readOnlyHint` from `false` inside a present object, so a server that annotates a read with `{readOnlyHint:false}` makes it `write`, never the other way round.

**Override across a schema or description change.** An override is bound to the digest it was set for. When the digest changes the override goes *stale*: a stale override can only tighten (`effective = stricter(auto-or-write, stale override)`), never loosen, and the tool is marked `Changed` until the owner sets it again or clears it. Rationale: an owner who trusted `fetch_page` as read-only trusted a particular tool; after the description or schema changed that trust must not transfer automatically (the change itself could be the attack), so the loosening half is dropped. An owner who demanded `high_risk` for a tool keeps that demand after the change: a changed tool never earns less scrutiny than the owner asked for. Both directions are conservative and the marker tells the owner what to revisit.

**Migration.** Existing `trusted_read_only_tools` entries become owner overrides = `read_only` bound to the first digest observed for that tool (`override_digest: "legacy"` until then). Migration runs once per server (`migrated_trusted` flag) so an owner who later changes the tier is not re-migrated.

## 3. Storage and binding

- Store: `<config dir>/mcp-tool-tiers.json`, mode 0600, atomic write, one record per `(server, remote tool name)`: `digest`, `auto`, `auto_digest`, `model_tier`, `reason` (bounded), `read_only_hint`, `destructive_hint`, `override`, `override_digest`, `classified_at`, `seen_at`. It holds no URL, header or credential. Tools absent from a complete `tools/list` are dropped on the next observation.
- **A remote name listed more than once** in one `tools/list` is one `tools/call` target with several run-facing schemas. No single descriptor describes it, so it is never sent for classification and has no auto tier (`write`, reason `listed more than once by the server`); its digest covers every listing, so an owner override binds to the whole set and goes stale when any listing changes.
- Writes (observation, classification, override) take the Manager's `mcpConfigFence` write lock, exactly like a config edit: a dispatch in flight completes under its read lock before the tier lands; nothing started after sees the old tier.
- Every proposal (`MCPCallApproval`) carries `Tier` and `TierBinding` = digest of the stored record plus the effective tier, resolved from disk at proposal time (not from the run's config snapshot, so a tier change does not stall a running task: the model's retry simply carries the new binding).
- `MCPCallCurrent` (and so `executeCurrentMCPCall`, the reviewer's setup checks and the claim binding) now requires both the config fingerprint and the tier binding to match the disk. `mcp_auto_reviews.tier_binding` stores the binding the review was made under; the claim compares it. Consequences:
  - write → read_only while a reviewed proposal is pending: its claim fails the binding check and the model retries; the retry resolves as read_only and executes without review (the proposal was reviewable anyway; nothing escapes review that needed it).
  - write → high_risk after an auto-review `approved` card: the claim's binding no longer matches → `errAutoReviewInvalidated` → the automatic answer is withdrawn and the card waits for a person. The approved-but-unclaimed write can never run as read_only, and a read_only→write change re-gates because the next proposal resolves as write.
- The action hash (`server, tool, config fingerprint, arguments`) deliberately does **not** include the tier, so a human refusal of an exact action keeps fencing it across tier changes.
- An exact action that already has approval history in the run (it was proposed while the tool was `write`) never takes the exempt route after the tool becomes `read_only`: it is reviewed afresh on its own card, as read-only proposals always were, and never replayed on a used approval.
- A proposal built by hand without a tier binding (tests, maintenance) is treated as `write` and is current only in its configuration part; discovery always sets the binding.

## 4. Classification pass

`internal/app/mcp_tool_tiers.go`. Trigger: whenever a complete `tools/list` is observed (settings check, Re-check, or a run's discovery), the Manager reports the server and the app's single worker classifies the tools whose `auto_digest` differs from their `digest`. Batches of at most 20 tools / 48 KiB; the model answers one JSON object `{"tools":[{"name","tier","reason"}]}`; strict parse (only names sent in *that batch*, valid tiers, bounded reason); a tool missing from the answer stays unclassified, and a label for a tool of another batch is dropped (the model never saw that tool's schema; a description in this batch cannot vouch for it). An input schema larger than 8 KiB is not sent (a marker replaces it); a `read_only` label for such a tool rests on the remote's description alone and is recorded as `write` (the owner can still set `read_only`). Model: the review model (`backgroundReview`), the same seam tests use for the reviewer. Failure → nothing is written, the tools stay `write`, the next observation retries. Results are only recorded for the digest that was classified; a tool that changed during the pass is left unclassified.

### 4a. Background queue and upgrade backfill

Existing installations have connected services whose tools were never classified. Per account (each `Server` owns one extensions Manager and one tier store) a single worker drains a classification queue:

- **Start**: `TierBackfillCandidates` lists configured services that were never observed, have any tool whose auto tier is missing or stale, or whose queue entry is not `done`. Each is enqueued with `refresh=true` (the tool list is fetched first; pending descriptors live only in memory). Startup returns at once.
- **Refresh / save**: a `tools/list` that leaves tools pending enqueues the service (no refresh needed); saving a service enqueues it with a refresh.
- **Entry state** is persisted in the tier store (`servers.<name>.queue`: `queued` → `running` → `done` | `failed` with `attempts`, `next_attempt_at`, `last_error` token). Results are written per batch, so a crash mid-pass resumes on the next start and only the tools still pending are sent again.
- **Failure** (no reviewer model, provider error, unusable service, tool list unavailable) leaves every pending tool `write`, records `failed` and retries with exponential backoff (1 min doubling, capped at 30 min) up to five attempts per process lifetime; after that the service waits for the next start or the next tool-list refresh. No failure path can produce `read_only`.
- **Visibility**: the tool list endpoint carries the queue entry; the Connections card shows a plain-text `Classifying tools…` while queued or running and the tier counts when done. Nothing animates.
- **Usage**: each classification request is recorded in `usage_background` (kind `mcp_tool_tiers`, model, tokens, priced) since no run owns it.

## 5. Fence predicate

`mcpProposalFence` and the scheduled-run uncertain-effect predicate use the tier: a proposal is an observation only when its effective tier is `read_only`. The raw `readOnlyHint` is still captured but no longer decides anything. A recorded `call_mcp_tool` step names only the sanitized run tool name (`mcp_<server>__<tool>`, case-folded, punctuation mapped to `_`); when two remote tools of a server sanitize to the same run name the step is ambiguous and is never an observation.

## 6. Residual risks

- A `read_only` tool executes without per-call review, so the classification (model + annotations) is trusted to say "this tool cannot change anything". The owner can tighten any tool at any time; the stricter-wins merge and fail-closed default limit the damage of a wrong model answer to "a read-looking tool that writes".
- Classification costs one model request per 20 new or changed tools; a server that rotates descriptions constantly is re-classified on every refresh and its tools fall back to `write` in between.
- The fence predicate evaluates a past step against the tool's *current* tier.
- Classification usage is recorded in `usage_background` but not yet shown on the Usage page.
