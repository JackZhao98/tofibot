# Memory and schedule metadata acceptance

Base: `19a7c4db02c75e6bfd4d3b61c5d4fc2f894bcf34` (fresh `origin/main`, verified by fetch).
Candidate branch: `codex/memory-schedule-metadata-20261003`.

## Data mapping and migration

- `memories.content` remains a factual memory body, consumed as factual data by context construction. The existing model has no memory execution prompt. This change adds `title` and `description`; it does not invent an executable memory prompt or translate facts.
- `schedules.content` remains the complete execution instruction. A separate `description` joins the existing `title`; `schedule_occurrences.description` stores an immutable claim-time snapshot alongside its title/cadence snapshot.
- Migration only adds text columns with empty defaults. No backfill, bulk translation, ID change, schedule recreation, or time/recurrence rewrite occurs.
- Existing records and older content-only API clients remain usable. Compact views use bounded neutral localized labels when metadata is absent; they never derive labels/tooltips from the prompt or factual body.

## Contracts and editing

New AI `save_memory`, `update_memory`, `create_schedule`, and `update_schedule` tool contracts require title and description and reject whitespace-only/missing metadata. Titles allow up to 120 Unicode code points, descriptions up to 280. Localized metadata and multilingual factual/quoted data are accepted.

Instruction prompts authored by the AI are required to be English by shared model guidance and content-specific tool descriptions. Language is semantic: no unreliable ASCII ban or silent translation is applied. English prompts may contain exact multilingual names, quotes and output requirements. Actual natural-language compliance requires model adherence; the server deterministically validates metadata presence/bounds and content validity.

`PATCH /api/memories/:id` and `PATCH /api/schedules/:id` preserve omitted fields. Explicit empty title/description clears that metadata and activates the safe display fallback; empty factual bodies/execution instructions are rejected. Existing content-only memory edits preserve metadata. Schedule edits change title/description and optionally future content only: IDs, kind, timezone, next fire, cadence, creation timestamp and past trigger messages/snapshots stay unchanged. Timing editing is intentionally not added by this change.

The memory panel offers creation, search across metadata and factual bodies, full-body disclosure and editing. Schedule creation/edit forms expose separate title, description and full instruction fields. Agenda lists, preview rows, web execution tickets and native desktop row labels/tooltips show metadata. Full schedule instructions appear in management details/edit; execution tickets use an explicit nested management disclosure. Historical tickets never borrow edited live metadata.

## Verification

- `GOCACHE=/tmp/tofi-metadata-gocache go test ./... -count=1`: passed, including all app tests and new migration/edit/snapshot/retry/HTTP/tool-contract tests.
- `npm run typecheck` and `npm run build`: passed (web and motion-lab builds). Existing Vite large-chunk advisory remains.
- `node scripts/test-display-metadata.mjs`: passed Unicode bounds, empty/legacy fallback, localization and no content/prompt fallback.
- `node scripts/test-scheduled-run-metadata.mjs`: passed exact occurrence provenance and historical labels.
- `node scripts/test-schedule-occurrences.mjs`: passed occurrence parsing/batching/status integrity.
- `test-display-metadata-browser.mjs`: real Chrome, production components against a disposable synthetic SQLite server. Mobile (390 px), tablet (820 px), web desktop (1280 px), native desktop row (1280 px). Verified compact labels/descriptions/tooltips, management-only instruction disclosure, previews, once/interval/daily/legacy records, multilingual memory search/details, UI create/edit, body/prompt preservation, timing invariance, reload and SQLite restart. Results and screenshots are in this directory.

Browser fixtures are opt-in tests with a stub engine that cannot call models or perform external work. They add no production routes. No user database, production deployment, credentials or live model calls were used. This candidate does not contain AutoReview/admin-delete branch work.

## Reproduce browser acceptance

1. Run `TOFI_METADATA_BROWSER_URL=/tmp/tofi-metadata-browser-url go test ./internal/app -run '^TestDisplayMetadataBrowserFixture$' -count=1 -timeout=15m`.
2. Use the generated fixture origin as `TOFI_DEV_API_ORIGIN` when running the UI Vite server on port 5198.
3. Run `TOFI_METADATA_API=<fixture-origin> TOFI_METADATA_ORIGIN=http://127.0.0.1:5198 PLAYWRIGHT_MODULE=<installed-playwright-index.mjs> CHROME_EXECUTABLE=<chrome-executable> node scripts/test-display-metadata-browser.mjs` from `ui`.
4. POST to `<fixture-origin>/acceptance/stop` to stop the disposable fixture.

Build and synthetic acceptance demonstrate source behavior, not production deployment. Parent coordination and Astra review remain required before merge/deployment.
