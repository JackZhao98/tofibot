# Inbound conversation webhooks

Each Bot's canonical DM and each visible, active group can have one opt-in
inbound webhook. Open the conversation's **More → Webhook** panel while signed
in to its workspace, then explicitly create it. Owner authentication and a
trusted HTTPS public origin must be configured. Opening a panel creates no grant.

The panel displays a stable endpoint URL and a random bearer secret once. Copy
the secret explicitly and store it in the sending service's credential store.
Closing or leaving the panel hides it; refreshing metadata cannot reveal it.
Use **Rotate** if the original response or secret is lost. Rotation immediately
invalidates the previous secret and preserves the endpoint and dedupe history.
Revocation stops future acceptance. Previously accepted work remains in the
conversation and can be managed with its existing Stop controls.

## Send an event

This example uses synthetic placeholders and does not contact a live endpoint:

```sh
curl --request POST 'https://synthetic.invalid/api/webhooks/SYNTHETIC_HOOK_ID' \
  --header 'Authorization: Bearer SYNTHETIC_SECRET_ONLY' \
  --header 'Content-Type: application/json' \
  --header "X-Tofi-Timestamp: $(date +%s)" \
  --data '{"event_id":"synthetic-event-001","content":"Synthetic external event text"}'
```

Send a single JSON object with exactly `event_id` and `content`. The sender's
stable event ID must contain 1–128 safe ASCII characters. Content must be
nonempty UTF-8 text, at most 16 KiB. The timestamp is Unix seconds for this
delivery attempt, within five minutes of the server clock. Refresh it on retry.
The complete request body is bounded at 32 KiB. Compressed bodies, unknown or
duplicate JSON fields, attachments, target/account selectors and execution
options are unsupported. Browser cross-origin ingress and CORS are unavailable.
Never put a secret in a URL, query string or body.

After durable acceptance, the server returns HTTP 202:

```json
{"accepted":true,"delivery_id":"SYNTHETIC_DELIVERY_ID","duplicate":false}
```

An authenticated retry with the same endpoint, event ID and semantic content
returns HTTP 200 with the original delivery ID and `duplicate:true`, including
after rotation or restart. JSON spacing and a refreshed delivery timestamp do
not change event identity. Reusing an event ID with different content returns
409 without creating work. Retain your event ID when retrying a lost response.

The bound Bot queues a normal turn; a group starts an unaddressed round using a
current member. `@` text cannot select a recipient. External events are marked
**Webhook · External event** in chat and treated as untrusted data in model
context. They cannot answer pending questions or secret inputs, replace human
approvals, grant tool permissions, steer human work or authorize Bot profile
changes. Results remain in the existing authenticated conversation. There is no
synchronous model response, outbound callback or public result polling.

## Bounds and errors

New events are limited to six per minute per endpoint and 30 per minute per
account. At most 20 queued/waiting webhook roots per conversation and 100 per
workspace are admitted. Identical retries consume request capacity, but no
new-event or new-run capacity. Unauthenticated requests also have bounded
peer/process limits. A full or rate-limited queue returns 429 with `Retry-After`;
retry with the same event ID. A rejected event is never recorded as accepted.

Unknown endpoints, invalid or revoked secrets, disabled accounts and required
password resets return a generic authentication error without target metadata.
400 means an invalid envelope; 413 an oversized body; 415 an unsupported media
type; 409 a conflicting event or unavailable target; 503 temporary service,
storage or model unavailability. Wrong methods return 405 and `Allow`.
Archived/deleted conversations, unavailable members and changed workspace
identity fail closed. Receipt acceptance is atomic with its message, root run
and durable events. Rotation/revocation races have a before-or-after outcome:
work already committed remains accepted, and a stale key cannot commit later.

## Authenticated management contract

Management uses the existing session, workspace binding and CSRF protection:

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/conversations/{id}/webhook` | Metadata only; never creates or reveals a secret. |
| POST | `/api/conversations/{id}/webhook` | Body `{}`; explicit create/re-enable, fresh one-time secret. An active grant returns 409. |
| POST | `/api/conversations/{id}/webhook/rotate` | Body `{"expected_version":1}`; one-time fresh secret with immediate invalidation. |
| DELETE | `/api/conversations/{id}/webhook` | Body `{"expected_version":1}`; revoke, HTTP 204. |

Rotate and revoke compare the stored version, so stale clients cannot change a
newer grant. Bindings come from the stored endpoint; there is no retarget action
or account selector. Grant metadata and secrets are excluded from Bot packages,
ordinary events and model capabilities. This v1 envelope uses bearer
authentication, timestamp freshness and durable deduplication; it provides no
provider-specific signature verification. Possession of the bearer permits new
requests. Multiple endpoints, provider adapters, payload transforms, delivery
dashboards, secret re-reveal and outbound webhooks are deferred.
