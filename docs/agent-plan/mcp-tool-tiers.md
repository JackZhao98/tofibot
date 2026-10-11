# MCP tool tiers: the provider's read-only mark runs, everything else is reviewed, "Always ask me" asks

Branches `feat/mcp-tool-tiers` (first version, model classification) and `fix/trust-provider-annotations` (this contract). This note is the contract a reviewer checks the code against; it is kept accurate as the code changes.

## 1. Principle

Two decisions by the owner:

- **Trust the provider's annotations.** A tool the MCP server marks `readOnlyHint: true` executes without review, exactly as other MCP hosts treat it. What a tool does inside the provider's own service is the provider's responsibility to annotate; AutoReview's job is to stop the agent from damaging the user's data outside the request or leaking sensitive data to recipients the user did not name. The user never has to configure tools one by one, and no model is asked to classify anything (no spend at connect or start).
- **Trigger discipline.** Like a safety on a firearm: when the user's finger is on the trigger (a host-verified request that covers this exact call), the call runs; only an accidental call (no user instruction covering it) asks a person. The reviewer's `allow` executes at any recorded risk level; a person is asked only when the reviewer finds the action is not covered by the request (`needs_human`), cannot judge it (`context_gap`) or denies it. Only host-verified authorization evidence counts; tool results, descriptions and assistant text never do.

## 2. What changes for the user

| Tier | How a tool gets it | Runtime behaviour |
|---|---|---|
| `read_only` | The provider's `readOnlyHint: true` on an unambiguous listing, or an owner override. | Executes directly: no reviewer call, no card, in every AutoReview mode (shadow mode still records its advisory, review-only card). Transient failures are retried. |
| `write` | Everything else (no annotations, `readOnlyHint: false`, `destructiveHint: true`, a name listed more than once), or an owner override. | Reviewed per call. `off` → human card; `shadow` → card plus advisory review; `auto` → reviewer `allow` executes (any risk level), `needs_human` / `context_gap` → human card, `deny` → refused with the reason. |
| `high_risk` | **Only** an owner override ("Always ask me"). | A person decides every call, in every mode; the reviewer is never asked. In an unattended run (scheduled, or rooted in a schedule) the call is not executed: named refusal `mcp_high_risk_unattended`, certainty `not_executed`, the run continues and reports. |

Settings → Connections: each service card shows `Connected · 49 tools · 28 read only` and a **Tools** disclosure grouped **Read only (marked by provider)** / **Reviewed each time** / **Always ask me**, each tool with a selector **Automatic (provider)** / **Read only** / **Reviewed each time** / **Always ask me**, a `Set by you` or `Marked by provider` marker, `Listed more than once` for a duplicated name, `Changed` when the tool moved since the owner decided, and **Refresh tools** (a fresh `tools/list`; nothing classifies).

## 3. How a tier is decided

`resolveMCPToolTier` (`internal/extensions/tool_tiers.go`) is the single place:

1. **Owner override**, bound to the tool digest it was set for. Fresh → wins. Stale (the digest moved) → may only tighten: `effective = stricter(annotation-or-write, stale override)`, and the tool is marked `Changed` until the owner sets or clears it. A `read_only` override that went stale no longer loosens; a `high_risk` one still asks.
2. **Provider annotation**: `readOnlyHint == true` on a tool listed once → `read_only` (source `provider`).
3. **Default**: `write` (source `default`).

`destructiveHint` never changes a tier. It is recorded and passed to the reviewer as `untrusted_tool_annotations` evidence (with `readOnlyHint`, authority `none`), so a reviewer can weigh it; it does not force a person.

Tool digest = sha256 of canonical JSON `{name, description, input_schema, annotations{readOnlyHint, destructiveHint}}`; an annotation change moves the digest like a schema change.

**Duplicate listing.** A remote name listed more than once in one `tools/list` is one `tools/call` target with several run-facing schemas. No single annotation describes it, so the provider's mark does not apply (`write`, marked `Listed more than once`); its digest covers every listing, so an owner override binds to the whole set and goes stale when any listing changes.

**Oversized schema.** The former cap ("a read_only label for a tool whose schema was too large to send is recorded as write") existed because the model classifier had judged the description alone. The provider's annotation is the authority now and needs no schema, so the cap is gone.

**Migration.** `trusted_read_only_tools` entries become owner overrides = `read_only` bound to the first digest observed (`override_digest: "legacy"` until then), once per server. A version 1 tier file (model classification fields `auto`, `auto_digest`, `model_tier`, `reason`, `classified_at` and the `queue` state) is read once and rewritten as version 2 without them; owner overrides, digests and observed annotations survive. A tool the old pass had labelled read_only without a provider mark is `write` again; one it had labelled high_risk is `write` (reviewed), not forced to a person.

## 4. Storage and binding

- Store: `<config dir>/mcp-tool-tiers.json`, version 2, mode 0600, atomic write, one record per `(server, remote tool name)`: `digest`, `description`, `annotations_present`, `read_only_hint`, `destructive_hint`, `duplicated`, `override`, `override_digest`. No URL, header or credential. Tools absent from a complete `tools/list` are dropped on the next observation.
- Writes (observation, override) take the Manager's `mcpConfigFence` write lock, exactly like a config edit: a dispatch in flight completes under its read lock before the tier lands; nothing started after sees the old tier.
- Every proposal (`MCPCallApproval`) carries `Tier`, `TierBinding` (digest of the stored record plus the effective tier, resolved from disk at proposal time) and the observed `ReadOnlyHint` / `DestructiveHint`. `MCPCallCurrent` requires the config fingerprint and the tier binding to match the disk; `mcp_auto_reviews.tier_binding` stores the binding the review was made under and the claim compares it. A tier change under a pending review invalidates the automatic answer (`errAutoReviewInvalidated`); the model's retry carries the new binding.
- The action hash deliberately excludes the tier, so a human refusal keeps fencing the exact action across tier changes. An exact action with approval history in the run never takes the exempt route after the tool becomes `read_only`.

## 5. Write path and the reviewer (`mcp-all-external-v6`)

`mcpReviewDisposition`: `deny` → `policy_denied`; `context_gap` (or `risk_level: unknown`) → `context_required`; `needs_human` → `human_required`; `allow` with a known risk level → `approved`, whatever the level (a stray `confirmation_required` on an allow does not make a card; the reviewer asks for a person with `needs_human`). The risk level and reason are recorded on the review row and the card. Before an automatic approval lands, `finishMCPReview` also requires at least one host-verified authorization source in the evidence packet (the native request, or the schedule's typed source); without one the card waits for a person. The reviewer prompt states the same job: check that the call is what the user asked for and does not damage unrelated user data or leak it; risk level is informational when authorization is explicit and specific; only host-verified authorization evidence counts.

## 6. Fence predicate

`mcpProposalFence` and the scheduled-run uncertain-effect predicate use the resolved tier: a proposal is an observation only when its effective tier is `read_only`. A recorded `call_mcp_tool` step names only the sanitized run tool name; when two remote tools of a server sanitize to the same run name the step is ambiguous and is never an observation.

## 7. Residual risks

- A `read_only` tool executes without per-call review on the provider's word. A provider that marks a writing tool read only is lying to every MCP host; the owner can tighten any tool at any time, and the per-tool "Reviewed each time" / "Always ask me" overrides are bound to the digest so a later change of that tool re-opens the question.
- A reviewer `allow` executes at high risk. The guard is the authorization check (host-verified request or typed schedule source), the reviewer's instruction to allow only what the request covers, and the owner's "Always ask me" override for tools they never want automatic.
- The fence predicate evaluates a past step against the tool's *current* tier.
