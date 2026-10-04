# Selected vault environment recovery

Account bundles can explicitly include selected persistent environment records
from the current account's vault and selected previously recovered inactive
records. The category `vault_environment` is off by default in the UI and in both
legacy empty-selection API paths. Selecting the category without explicit IDs
fails. Standalone Bot bundles reject this account-wide category.

Only persistent `env` records with stored ciphertext and no run, conversation or
Bot binding qualify. Run inputs, SSH keys, OAuth/provider sessions, source vault
keys/ciphertext, installed Guest `.tofi-env`, host/process environment and arbitrary
files are excluded. An environment token deliberately saved as a persistent env
record is eligible: this adapter does not infer a provider/login inventory from
its name or value. No complete account, computer, SSH or environment backup is
claimed.

## Format and limits

Selected environment records produce `tofi.bundle` version 3. Otherwise exports
remain version 1 or 2; legacy `tofi.bot` readers remain available. Existing older
readers reject v3. New readers reject unknown versions, fields and duplicate
fields. Records have a UUID, name, fixed kind `env`, allowlisted target variable,
original timestamp, exact UTF-8 value, source category and inert source provenance.
Values are not trimmed or translated. No action, executable binding or authority
is serialized. Records must have nonempty values without NUL, up to 64 KiB each.
At most 64 selected records and 1 MiB aggregate value bytes fit in the unchanged
16 MiB clear / 24 MiB encrypted bundle and 20,000 logical-record limits.

Recovery has a separate per-account cap of 256 rows and 4 MiB value bytes; the
existing database quota still applies. Over-limit selections fail, never truncate.
Vault, recovered rows and ordinary database data are independently captured
snapshots, not one global cross-store snapshot. Metadata and byte counts can be
sensitive too; the implementation adds no telemetry or value-reveal UI.

## Key custody and transport

The official UI reuses the existing whole-file AES-GCM / PBKDF2 encrypted envelope
unchanged. Its passphrase stays in the browser and is never an API argument or
persisted by the UI. The UI accepts sensitive records only from an encrypted file;
plain sensitive bundles are rejected, including through legacy file entrypoints.
The passphrase has no recovery escrow. Clearing local buffers is not a guarantee
that JavaScript strings have been erased from memory.

The source API intentionally returns plaintext to the authenticated browser. The
browser encrypts downloads and decrypts imports. The existing import API sends
the decrypted bundle to the destination; selection controls which records are
stored, rather than hiding unselected records from that server. A direct API
client cannot prove that its input file was previously encrypted. Sensitive
export/import/list/delete operations require an authenticated owner and TLS, or
explicitly configured loopback ingress based on the actual socket peer. They
reject foreign origins/cross-site requests; writes require an Origin header.
Arbitrary forwarded HTTPS headers or permissive LAN HTTP do not authorize this
slice. AccountGateway supplies an internal authenticated workspace marker after
its existing session registration and cancellation checks, never a client account
override. Responses use no-store. Request bodies and values are not logged here.

The account server still handles plaintext and holds its vault key. Host root can
read keys, runtime data and delivered browser code. Application admins can reset
account passwords, which may allow takeover. Account isolation and browser file
encryption do not make this an operator-blind or zero-knowledge hosted runtime.
Anyone with the downloaded file and passphrase can read its contents.

## Inactive recovery and idempotency

`portability_secret_recovery` stores a destination-key encrypted capsule containing
the value and metadata. Its plaintext columns contain only fresh/source UUIDs,
import UUID, byte count and local creation time. AEAD additional data binds the
capsule to a distinct recovery purpose, destination account identity and target
UUID. The imported account never adopts source keys or vault ciphertext.

Sensitive preview bundle/destination digests are sealed with the same established
vault AEAD and a separate preview purpose/account/preview UUID. Bare secret bundle
digests do not enter SQL or responses. Apply verifies selection/content, expiry,
destination metadata and key availability before attachment staging, when present,
and again in the publication transaction. The ordinary database, active vault and
inactive recovery inventory participate in destination checks. Applied retries
verify the sealed bundle digest and return the original receipt, including after
restart; they do not duplicate or resurrect deleted recovery rows.

New capsules and ordinary imported data/provenance/receipt publish in the existing
SQL transaction. On a failure, new rows roll back. Recovered values never enter
`secretVault.records`, `vault.json`, installed Guest environment, normal credential
apply routes, connections or execution runs. Active same-target credentials stay
unchanged; preview shows the conflict as information. Instructions/history remain
inert and imported schedules stay paused. Attachment compensation remains the
existing separate Guest journal protocol. Credential-only v3 imports leave older
asset cleanup journals untouched for startup or a subsequent attachment import,
so recovery-only publication does not require Guest availability.

`GET /api/portability/environment` returns allowlisted active/inactive metadata,
not values, value hashes or ciphertext. `DELETE /api/portability/recovery/:uuid`
removes only the current account's inactive capsule. Explicitly selected inactive
UUIDs can be re-exported through the authenticated encrypted export UI. Promotion,
installation or connection is a separate future user action and is not provided
by this slice. No ComputerCredentials save-and-apply path is reused.

## Acceptance boundaries

Focused synthetic Go checks cover defaults, record/source restrictions, limits,
distinct source/destination keys, inactive import/restart/re-export, redacted
responses and durable SQL, UUID/account/corruption binding, preview invalidation,
rollback/capacity/cancellation, account/session isolation, secure ingress and
response lock release. UI checks cover v1/v2/v3 encrypted roundtrips, wrong
password/corruption, plaintext-sensitive rejection and defaults. These checks are
not actual browser acceptance. The pre-existing attachment browser upload gap
remains separate and must not be worked around with unsupported browser tools.
