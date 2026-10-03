# Portability review fixes and synthetic browser acceptance

This checkpoint follows the held `7799200` slice. No attachment, vault,
environment, VM or production category is enabled here.

## Review fixes

- Historical sender, memory and paused schedule references require an existing
  bundled Bot, not current group membership. Selected group history brings
  required Bot configurations and empty DM structure; it never adds a former
  member back or copies the dependency Bot's other chat text. Deleted source
  senders and standalone foreign senders remain inert provenance. Preview
  names the actual Bots to be created and explains dependency closure.
- Completed apply retries return the immutable receipt and do not republish
  requested settings. Settings import and ordinary model settings writes hold
  the same mutex across database commit and live default publication.
- Protocol fields must use their exact canonical JSON names, including nested
  records and request options. Go's case-insensitive struct aliases are rejected.
  Opaque avatar and original notice/provenance metadata remain user data.
- Successful imports refresh available export Bots and explicitly imported
  timezone preferences in the open settings screen.

The three independently supplied repros are retained in
`internal/app/portability_review_regression_test.go`, with additional selected
historical dependency, orphan/standalone provenance, canonical nested fields
and concurrent ordinary settings write coverage.

## Actual browser acceptance

Used `cmd/account-acceptance`, loopback only, a dedicated synthetic temp data
directory and real-worker mode disabled. The existing harness's isolated
control-plane startup omitted owner-auth schema, so that schema was initialized
only in the fixture through a temporary test. No product authentication bypass
or production source change was used. The temporary fixture generator was
removed before final tests.

Browser actions at desktop and 390 px narrow width verified:

- Authenticated login; account export from settings; browser-only encryption
  password entry; downloaded encrypted file containing no plaintext Bot name.
- Wrong password and corrupt ciphertext rejection, with no preview/apply
  control becoming available.
- Successful decryption, counts, same-name conflicts, selected Bot and category
  changes invalidating preview, partial-group omission and missing attachment
  disclosure.
- Apply creates copies and clears the apply control. Settings remain unchecked
  by default. Explicit settings selection invalidates preview and discloses
  replacement; after apply the visible timezone refreshes to UTC and export
  choices include the new Bot copies.
- Independent Bot export and import preview use the same account import flow;
  the decrypted fixture has exactly one Bot/DM, no group history and no account
  settings. Desktop and narrow screenshots were captured in the browser tool
  transcript; narrow document width equals its 390 px viewport.
- Model settings page displays the current synthetic ordinary choice after
  duplicate replay and process restart.

Authenticated HTTP and fixture database checks supplement browser evidence:
repeated apply returns the same receipt and leaves six Bots unchanged; all six
schedules are paused; runs and active action-card history are absent; an
ordinary model edit remains current in durable settings and runtime config
after replay; a second synthetic account rejects the first account's token
with 409 and retains zero Bots; process restart preserves the receipt and
current model choice. These cross-account/restart assertions are HTTP checks,
not browser network mocking.

## Response-writing mutex follow-up

Commit/cache publication now uses small helpers that return with the runtime
mutex released. All success and error HTTP writes occur afterward, so response
backpressure cannot retain the runtime cancellation/shutdown mutex. The
supplied blocking-response repro is retained with four branches: import
success/failure and ordinary settings success/SQL failure. Receipt idempotency
and settings commit/cache ordering keep their existing regression coverage.

## Final verification

Full Go suite, focused portability/review race suite, UI typecheck, encryption
tests, UI build and motion-lab build pass. Tests use the explicit tracked source
snapshot in `/tmp`, module networking off, live inputs disabled and local
fixture sockets only. Existing Vite chunk-size warnings remain. The first full
run caught a pre-existing selected-sender normalization path; it was corrected
and final full/race runs passed. Final implementation files match the tested
snapshot byte-for-byte.

Outer task evidence contains final logs, encrypted synthetic artifacts,
`browser-http-results.json` and `browser-bot-export-results.json`. These do not
prove production deployment, external worker behavior or asset/vault recovery.

Next category remains attachment bytes, as a separate reviewed commit. Its
planned bounded account-local storage must commit bytes, metadata, remapped
message links and the import journal together; check content hashes, logical
file IDs, source paths and quota limits; report every unavailable/unsupported
file; and test rollback/restart without external downloads or real VM access.
