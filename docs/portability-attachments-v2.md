# Portable attachment bytes (bundle version 2)

This slice follows reviewed baseline `d3001a6596a29b185524d74da0966a4f731e0dab`. It is a local source candidate; it does not establish production or VM acceptance.

## Enabled scope

Selectable account and independent Bot bundles include Bot configuration, conversation/chat data, memories, paused schedules, optional account settings, and bounded attachment bytes. Exports without the attachment category remain version 1. Readers accept versions 1 and 2 and legacy version 1 `tofi.bot` configurations. A standalone Bot carries its DM history and eligible forwarded attachment references; no owner group history is added merely to retrieve a forwarded file.

Version 2 attachment records carry canonical base64 bytes, SHA256, byte count, detected safe MIME, display name, logical owner conversation, timestamp, and source provenance. Message bindings remap to fresh IDs. Missing, unavailable, unsafe, oversized and bundle-limited assets are listed individually. These omission records remain on subsequent exports rather than disappearing after import. Forwarded file ownership can move to a selected conversation, while source ownership remains inert provenance.

Limits are 4 MiB per file, 8 MiB total and 256 included files, within the existing 16 MiB clear bundle/24 MiB encrypted file and 20,000 logical-record limits. Files beyond limits are explicitly omitted; a bundle over the overall limits fails export. Local legacy exports accept only exact managed UUID filenames, regular non-symlink single-link files; account exports never fall back to host files. Archives are inert attachments only: nothing extracts archives or fetches a manifest URL.

The browser encrypts the whole export with the existing password envelope. Passwords remain browser-only and timestamped filenames distinguish repeated downloads. Import preview includes included/binding/missing counts, file bytes and sensitivity/storage warnings. A destination with no configured guest blob backend cannot apply the selected attachment bytes; other categories can be imported after deselection.

## Storage and failure behavior

Imports use only the current authenticated account guest disk and its existing quota. They require durable blob protocol 1 (`X-Tofi-Blob-Durability: 1`) for staged PUT and digest-aware DELETE. Older Guest responses are rejected rather than treated as durable. Server, Web and Guest changes belong to the same source candidate; this does not promise older-Guest compatibility for attachment imports.

A FULL-synchronous SQLite journal records newly generated target UUIDs, sizes and hashes before any guest write; it stores no file bytes. Files are staged and read back through the fixed account socket. All account rows, attachment metadata/bindings, provenance, omission records, receipt and staging-journal deletion commit in one SQL transaction. This provides application-visible atomicity with compensating file cleanup, not a distributed filesystem/SQLite transaction.

A crash or failure before SQL publication exposes no attachment through the authenticated download API. Failed cleanup keeps durable intent; startup/next import retries cleanup. Digest-aware cleanup can remove an unlinked content object created before its alias, while preserving content that still has a live alias. Guest file and directory entries are synced before durable acknowledgement. Transient storage failure can retain hidden staged files and consume quota until cleanup succeeds. One workspace Store owns imports; the per-workspace mutex serializes staging, while the reviewed runtime settings mutex covers only final SQL commit/cache publication. Guest I/O and HTTP response writing occur after that mutex is released.

Existing destination records are never overwritten. Duplicate/restart retries return the saved receipt. Imported schedules remain paused and historical messages/instructions remain data. No authentication, sessions, admin claims, execution claims or approvals are restored.

## Still excluded/deferred

Credentials, private SSH keys, environment/secrets, arbitrary guest files/disk images, extensions/skills, work items, mail drafts, run/tool history, reactions, summaries and other browser preferences remain outside the portable category set. A selected data bundle is not a complete account or disk backup. No production change, VM106 execution, publication, license decision, push or merge is part of this work.

## Validation and remaining blocker

At baseline commit `d1c207a`, all 15 implementation/test files matched the preserved SHA256 manifest `/tmp/tofi-attachment-checkpoint-files.json`; archive SHA256 `860d479eb7680e0b29ee80a392ed654c48be66fd5fc6bc9e4a88616e3d60537d` was verified on resumption. The earlier full Go aggregate passed on the exact final Go source (app 54.789s, computer 0.472s, guest 2.283s), and final TypeScript/encryption/Vite checks passed. These were reused rather than repeated. The earlier focused race evidence (app 23.053s, guest 1.457s) predates only the final durable response acknowledgement delta; do not present it as a fresh race run on every final line.

Attachment regressions in `internal/app/portability_attachments_test.go` cover byte/hash/ID/binding/provenance roundtrip, duplicate/restart receipt, quota, lost acknowledgement, SQL rollback, crash-after-staging recovery, missing/unsafe assets, forged metadata/URLs/references, forwarded standalone scope, account isolation/CSRF and settings-lock release during file I/O. `internal/computer/blobs_staging_test.go` and Guest blob tests cover durable protocol acknowledgement and orphan-object cleanup.

An authenticated API integration on 2026-10-04 used the previously browser-generated synthetic encrypted account export and disposable Guest services (no VM). It verified corrupted preview rejection, named missing-file warning, deselection counts, forced SQL failure with zero visible rows/aliases/objects, successful ID/binding remap, identical 46-byte SHA256 download, duplicate receipt, foreign-account apply/read rejection and standalone Bot re-export with original byte/missing provenance. Results: `/tmp/tofi-attachment-api-results-20261004.json` and workspace `evidence/attachment-api-roundtrip-20261004.json`.

Browser export had succeeded on 2026-10-03. Browser import acceptance remains incomplete: the prior filechooser stalled 8,361 seconds despite timeout; no CUA/browser/Playwright tools are available in the resumed execution environment. The API integration is backend evidence, not UI acceptance. The parent must arrange that remaining browser acceptance and independent review before integration.

## Attachment review corrections

The review of `d1c207a` found an untracked temporary-file crash window and a single-connection export stall. Guest PUT now writes `objects/.<target UUID>.pending`, persisting that journal-bound name before file bytes. DELETE removes and confirms absence of that exact pending entry before durable acknowledgement; cleanup failure returns an error so the staging journal remains for retry. This covers partial and fully synced pre-rename writes without deleting another staging identity.

Export now captures attachment sources, bindings, omission metadata and database path in the same SQLite snapshot as Bots/history/settings, then commits the read transaction before Guest/local file reads. The byte phase uses the captured owner/name/size/provenance, hashes the bytes read and reports missing or mismatched-size files. Slow Guest reads no longer occupy the Store's only database connection.

The two review repros and adjacent recovery/export/Guest checks passed in one targeted `-race` run: internal/app 8.047s, Guest 1.782s. The crash repro fixture uses the new journal-bound pending filename instead of the former random temporary filename; the independent Guest PUT regression verifies that the writer actually uses that name. Partial writes, cleanup-error journal retention, later metadata updates, missing files and resized bytes are also covered. No full suite or UI build was repeated. Browser import acceptance remains explicitly unverified.
