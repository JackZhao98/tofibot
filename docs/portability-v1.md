# Portable account data and standalone Bots: first review slice

This slice creates portable selected data, **not a complete account backup**.
It is private, has not been merged or deployed, and does not access a real
account, VM, vault or SSH key during verification.

## Enabled

| Category | Export/import behavior |
| --- | --- |
| Bot configuration | Name, original instructions, model, reasoning effort, archived state and timestamps. Current browser avatars travel in UI exports and are restored under new Bot IDs. |
| Conversation structure | DMs and account groups, membership, names, visibility/archive state and timestamps. Structural dependencies accompany Bot configuration even with chat text unchecked. |
| Chat text | Original message content, role, sequence, sender and timestamp. Original run IDs/action metadata remain inert provenance; imported records create no runs. Historical action cards cannot trigger actions. |
| Memories | Content, title, description, revision, timestamps and conversation/Bot scope. |
| Schedule definitions | Content, title, description, creator attribution, recurrence, timezone and timestamps. Every imported schedule is paused, including formerly active/completed/deleted schedules. No occurrences or historical runs are restored. |
| Persisted settings | Optional timezone, model/reasoning default and dictation model. Import preserves current settings by default; selecting settings explicitly replaces these values and preview discloses this. |

Account and standalone bundles use `tofi.bundle`, version 1, with `kind` of
`account` or `bot`. An independent Bot export contains its DM and chosen DM
data; it never silently includes group history. Account exports include a
group only when every member Bot is selected. The manifest reports omitted
groups. Existing `tofi.bot` version 1 config JSON remains accepted through the
same preview/apply protocol, including its avatar.

## Excluded or deferred

Attachment bytes and message/file links; private SSH keys; credential values;
provider/OAuth login; guest files, installed environment and disk; extensions
and skills; work items; mail drafts; runs, usage and tool history; reactions;
derived summaries; browser preferences other than current Bot avatars;
authentication, admin rights, sessions, device pairings and approvals.

The export manifest lists these exclusions and the known number of omitted
attachment records. Preview separately warns about missing assets. Source
declared omission counts cannot prove the source account had no other data.
There is no filesystem export, credential opt-in or vault restore UI in this
slice. Those features must remain disabled until staged asset/vault recovery
and synthetic fault tests exist. Whole Guest disk recovery is a separate,
optional product with host/version limitations, not universal VM portability.

## Flow and trust boundaries

Settings → account → data migration lets the user choose an account scope or
one Bot, choose categories, enter and confirm an encryption passphrase, then
download. The new package UI always encrypts the complete payload in the
browser with AES-256-GCM and a fresh salt/nonce; PBKDF2-SHA256 uses 310,000
iterations. The password never goes to the API, local storage or logs. This is
password encryption, not source authenticity/signing. HTTPS or a secure local
browser context is required. The existing config-only Bot sharing button
still emits the compatible plaintext v1 JSON and must only be used for
instructions/configuration the user intends to share.

Import reads/decrypts the file locally, selects categories and Bots, then asks
the current account's server for preview. Preview recomputes counts, checks
references, lists name conflicts, explains copies and paused schedules,
discloses settings replacement and exclusions, and gives a rough database
space estimate. That estimate is not a free-disk guarantee. The user then
confirms creating copies. Deselecting records skips them; no overwrite or
merge into existing Bots is offered. Raw JSON tokens survive the UI submission
so server duplicate-field rejection also applies to file imports.

The account gateway selects the destination runtime from the session. The
package cannot select an account, path, computer, socket or destination ID.
Source IDs are remapped to fresh UUIDs and retained in provenance. No import
executes instructions, schedules, skills, installers, network requests, or
historical actions. Existing credentials confer no additional rights.

## Format and limits

Only uncompressed JSON is accepted: ZIP, tar and gzip are unsupported and
rejected. No archive extraction occurs, eliminating extraction traversal,
symlink and decompression paths in this slice. Unknown fields, duplicated
JSON fields, excessive nesting, unsupported versions, forged counts, duplicate
IDs and invalid reference graphs are rejected. Limits: 16 MiB clear payload,
24 MiB encrypted file, 1,000 Bots and 20,000 total logical records. Large
accounts must choose fewer Bots/categories; this slice does not silently
truncate or claim completeness. Record arrays and source content remain data.

## Atomicity and recovery

`portability_imports` stores workspace-local preview tokens, the selected
bundle hash, destination conflict/settings fingerprint, expiry and committed
ID map. Preview expires after 15 minutes; at most 32 pending previews are
retained. Apply checks the hash/options and target state again. Tokens from
another workspace cannot be applied. All allowlisted records, provenance,
invalidation events and the applied journal transition commit in one SQLite
transaction. Nothing is visible until commit. SQL/storage failure rolls back
the whole data import; the original preview remains retryable if still valid.
After restart, repeating a committed import returns the same ID map without
creating duplicates. Existing live databases/directories are never replaced.

This atomicity covers database rows only. It must not be generalized to assets
or vaults before their dedicated recovery path is implemented.

## Verification commands

Synthetic-only focused Go tests:

```sh
GOCACHE=/tmp/tofi-portability-go-cache GOPROXY=off go test ./internal/app -run '^TestPortable' -count=1 -timeout 60s
```

UI checks:

```sh
npm --prefix ui run typecheck
npm --prefix ui run test:portability
npm --prefix ui run build
```

Covered scenarios: legacy package/avatar and stable conversion; malformed
JSON, duplicates, unsupported archives, paths/claims/version/size/nesting;
counts and references; settings preserved by default; preview without visible
data; round trip and ID closure; paused schedules and inert history;
standalone group omission and attachment disclosure; transactional injected
failure, rollback, restart and idempotent replay; stale target and changed
payload; cross-account, anonymous and cross-site rejection; encryption round
trip, wrong password, corruption, KDF/version and size rejection.

Before integration, manually verify the settings and Bot entry points in a
disposable authenticated UI: category/Bot toggles invalidate preview; legacy
file picker routes to preview; encrypted import clears the password after
decrypt; avatar restoration, archived Bot selection and completed import
refresh work; no actions run; current settings remain unless selected. No
production deployment or VM test is implied by source build success.
