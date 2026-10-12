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

**Migration.** `trusted_read_only_tools` entries become owner overrides = `read_only` bound to the first digest observed (`override_digest: "legacy"` until then), once per server. A version 1 tier file (model classification fields `auto`, `auto_digest`, `model_tier`, `reason`, `classified_at` and the `queue` state) is read once and rewritten as version 2 without them; owner overrides, digests and descriptions survive. A version 1 record's observed annotations do **not** survive: version 1 recorded the first listing's hint and never said whether the name was listed more than once, so carrying its `readOnlyHint` forward would make a duplicated listing `read_only` until the next `tools/list`, and a call through a cached schema (recent capability context) dispatches without one. Every migrated tool is therefore `write` until the server is listed again under version 2, which re-records the provider's mark. A tool the old pass had labelled read_only without a provider mark is `write` again; one it had labelled high_risk is `write` (reviewed), not forced to a person.

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

## 8. Remembered approvals ("Approve and remember in this chat")

Branch `feat/remember-approval`. A human approval card for an MCP call carries a second action, **Approve and remember in this chat**. Later calls in the **same conversation** to the **same server and tool** on the **same target** skip the human card. The memory replaces exactly one thing: the human decision a card would otherwise wait for. It replaces nothing else: the reviewer still runs where it runs, the claim is still one-shot per call, every binding check still applies, and the owner's "Always ask me" tier still asks.

### 8.1 What a grant binds to

A grant row (`mcp_remembered_approvals`) is bound to: account, conversation, Bot, server, remote tool name, **config fingerprint**, **tier binding** (§4) and a **target key**. All eight must match for a later call to use it. A grant is live until it is revoked, until its 24-hour validity ends, or until any binding moves.

**Target key.** Tools have arbitrary schemas, so "same target" is derived conservatively from the tool's own input schema and the exact arguments of the approved call (`mcpRememberTargetFor`, `mcp_remembered_approval.go`):

- An argument is an *identifier* when the tool's `input_schema.properties` names it and the last token of its name (snake, kebab or camel case) is one of `id, ids, uid, uuid, guid, key, url, uri, path, channel, recipient, recipients, to, cc, bcc, identifier, slug, handle` or a container name `owner, org, organization, repo, repository, project, workspace, team, branch, database, db, table, collection, bucket, folder, parent, space, site, calendar, account, user, email, phone`, and its value is a string, number, bool or a non-empty list of those. An object-valued argument is never an identifier, and **when any argument is an object (or an array holding one) the call binds exact arguments** (`kind: exact`): a nested object may carry the real target (a recipient inside a `message`) where no identifier name can see it. Numbers are compared by their exact JSON digits (`json.Number`), never as float64: two snowflake ids that round to the same double are different targets.
- When at least one identifier argument is present, the key is the canonical JSON of **every** present identifier with its exact value(s) (`kind: identifier`; the card and the list show it as `page_id = page-q4`, `issueKey = OPS-1, to = ["a@example.com"]`). The other arguments (content, title, body) are free: that is what the owner asked for ("remember this target, not this exact text").
- Otherwise the key is the canonical JSON of the **exact full arguments** (`kind: exact`): only an identical call matches. The key never widens to "any arguments", and a tool with no derivable target (schema missing, non-object arguments) offers no memory at all.

Why this rule and not a model: the decision must be reproducible at claim time from the same inputs, must not spend a request, and must fail closed. Identifier names are the one schema convention stable enough across providers to rely on; everything else falls back to exact arguments.

### 8.2 Where a grant decides, and where it never does

| Mode / tier | Without a grant | With a matching grant |
|---|---|---|
| `write`, AutoReview `off` | human card | card created already decided (`answered_by = remembered-approval`), published once, claimed, executed |
| `write`, `shadow` | human card + advisory review | same as off; the advisory still runs and its display keeps the grant id |
| `write`, `auto`, reviewer `allow` | executes | executes (the grant is not consulted) |
| `write`, `auto`, reviewer `needs_human` or no host-verified request | human card | **human card**: the reviewer's `needs_human` means "the user's request does not cover this call" (`mcp_review_contract.go`), which is exactly the decision a memory must not take. In auto mode a grant is never consulted and a card never offers the action; a grant made in another mode stays unused until the mode changes back |
| `write`, `auto`, reviewer `deny` / `context_gap` / failure | refused / closed | refused / closed (a memory never overrides the reviewer) |
| `read_only` | executes, no card | n/a |
| `high_risk` ("Always ask me") | human card, reviewer not asked | **human card**: the card offers no remember action, the answer route refuses `remember`, and a planted grant is ignored (`rememberedApprovalUsable`) |
| unattended run (scheduled, or rooted in a schedule) | card waits / `mcp_high_risk_unattended` | **never used, never offered**; a scheduled run cannot create one either |
| run not triggered by the owner's own message (a task, follow-up, triage, team or message run; a run with a parent outside a group round; a trigger without host-ingress provenance, such as an imported or Bot-sent message) | human card | **never used, never offered** (`mcpRunHumanTriggered`): the same provenance rule the reviewer's evidence applies. A memory replaces the owner's click on a call the owner's own turn produced, not a call something else started |

Other conversations, other Bots and the same Bot in a group are different bindings. A previous human **refusal** of the exact action in the conversation or its chain still wins over a remembered answer at claim time (the same rule as automatic answers).

### 8.3 Creation is a human act on the owner's route only

A grant is created only by `POST /api/questions/{id}/answer` with `{"value": true, "remember": true}`, which runs under owner authentication and the same-origin / cross-site checks like every `/api` write. The route refuses `remember` before answering when the card is not an eligible MCP proposal (`mcp_call_approvals.remember_eligible`), so a refused remember leaves the card pending. The grant binds to what the gate recorded on the approval row from the real proposal (server, tool, config fingerprint, tier binding, target key), never to anything in the request body, and the tool's current tier and binding, the AutoReview mode and the run's human trigger are re-resolved at that moment (`rememberMCPApproval`). The model-visible `request_approval` tool cannot carry a remember offer (`normalizeQuestionInput` rejects it), `AnswerQuestion` rejects the host actor `remembered-approval`, and a tool result has no path to any of this: a prompt-injected proposal is a pending card with no grant (tested).

### 8.4 Claim, dispatch and audit

- Each call still has its own card, its own `mcp_call_approvals` row and its own one-shot claim; a grant decides a card, it never replays an execution.
- `claimMCPApproval` re-validates a remembered answer against its grant (live, same conversation and Bot, same server, tool, config fingerprint, tier binding, target key, tool still not high_risk, mode not auto, run still human-triggered). Every query of that re-check runs on the claim transaction: the store has one connection, so a query on the store from inside the transaction would wait forever. A grant revoked or expired between decision and claim withdraws the answer: the card returns to pending for a person (`withdrawRememberedAnswer`), and nothing executes. In `auto` mode the review-row binding check (§4) applies on top, as for any non-automatic answer.
- Dispatch runs under the configuration fence and re-checks config fingerprint and tier binding (`executeCurrentMCPCall`), the same values the grant is bound to.
- Audit on every auto-approved call: `questions.answered_by = remembered-approval`, the card display's `decision_source = remembered_approval` and `grant_id`, `mcp_call_approvals.grant_id`, and `use_count` / `last_used_at` on the grant. A later display rewrite (shadow advice) carries the grant id over.

### 8.5 Visible and revocable

The conversation menu has **Remembered approvals**: a sheet listing each live grant (tool, server, target, end time, use count) with **Revoke**. `GET /api/conversations/{id}/remembered-approvals` lists live grants; `DELETE /api/conversations/{id}/remembered-approvals/{grant}` revokes one. Expiry is 24 hours from creation (`rememberedApprovalValidity`); the card hint and the sheet say so. No argument payload, URL, header or credential is stored on or shown for a grant.

### 8.6 Residual risks

- Identifier binding is by name convention. A tool whose target lives in an argument not named like an identifier (say `title`) falls back to exact arguments (safe); a tool whose identifier-named argument is *not* the target (say `sort_key`) binds that value and leaves the rest free. The card shows the binding before the person chooses, and the owner's "Always ask me" override removes the option for any tool.
- A grant lives up to 24 hours across runs of one conversation. Revoke is one click; a config edit, a tier change or a tool schema change ends it on its own (the tier binding digests the tool's input schema).
- Outside auto mode no reviewer sees the call, so a memory cannot tell the owner's repeat from a prompt-injected call on the same target made inside a turn the owner did start (the content is free by design). The human-trigger rule removes every other starter (tasks, follow-ups, Bot messages, schedules, imported history); the residual is the injected write to a target the owner already chose to trust for 24 hours in this chat, with the card and the sheet naming that target.
- Identifier binding is by name: containers (`owner`, `repo`, `branch`, `database`…) bind together with the target, and a call carrying any object-valued argument binds exact arguments. What remains is a scalar argument that names the target under a name outside the vocabulary (say `title` or `name`): it is then free, and the card shows exactly what binds before the person chooses.
