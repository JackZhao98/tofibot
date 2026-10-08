# Stable Bot and group URL acceptance

Base: `2d304632558b4497d98ec7759be6a181d7362a9c`, verified against remote
`refs/heads/main` before worktree creation on 2026-10-03.

Bot links use `/b/<bot-id>`; group links use `/g/<conversation-id>`. The existing
Go SPA fallback serves direct paths. A read-only inspection of the native
client's extensionless-path fallback and same-origin navigation policy confirmed
compatibility; no native source is imported or changed.

Selection waits for the login gate and authenticated Bot/conversation index.
Explicit targets beat native saved selection. Sidebar, notification, creation,
archive/member/work navigation and browser history share the same selection
function. Default root selection replaces the initial history entry. Repeated
selection adds no entry. Copy-link actions copy the current origin and target
path without query strings, fragments or access grants.

Missing, deleted, wrong-kind, internal and cross-account targets keep their URL
and show one generic unavailable state. They do not fall back to another chat or
fetch a snapshot/SSE for an unlisted ID. Archived Bot links remain read-only.
Existing per-conversation drafts and message loading are preserved.

## Results

- `npm run typecheck`: PASS.
- `npm run build`: PASS (main UI and motion lab; existing bundle-size warnings).
- `npm run test:conversation-route`: PASS.
- `npm run test:composer-draft`: PASS.
- `TestConversationRouteBrowserFixture`: PASS; fixture stopped and temporary
  account databases removed after acceptance.
- 57 rendered Chrome cases: PASS, at 1280×900 and 390×844, plus a synthetic
  native bridge at 1280×900. Production-built App with real HTTP, AccountGateway,
  login, SSE and disposable SQLite; no network engine, Worker or live credentials.
- Existing `TestAccountPublicInfoDoesNotDisableAuthAndUsesOwnInstance`,
  `TestAccountProtectedSurfacesRejectInvalidSessionStates` and
  `TestAccountAdminCreatesSeparateEmptyWorkspaces`: PASS.

The rendered matrix covers direct links, login return, native saved-selection
precedence, reload, existing history, Bot/group draft retention, repeated
selection, back/forward, copied links in another tab, missing/deleted/internal/
malformed/wrong-kind/cross-account links, sidebar recovery, notification
navigation, archived access, same-tab account switching, current-account root
fallback, new Bot/group selection and reload, and active deletion through
back/forward/reload. Navigation writes are restricted to authentication and the
existing read markers. Deliberate synthetic create/delete actions are tested
separately. Both accounts finish with **zero runs, schedules, work items and
engine calls**.

`browser-results.json` contains per-case results. PNGs capture the actual rendered
group and unavailable views. `SHA256SUMS` binds implementation/tests and these
artifacts. `build-sha256.txt` records the production UI artifact used for testing.

Native validation uses a synthetic bridge and source inspection; a packaged
Electron application was not launched. Nothing was merged, deployed or published.

## Reproduce

Build the UI, then start the opt-in fixture in one terminal:

```sh
npm --prefix ui ci
npm --prefix ui run build
TOFI_ROUTE_BROWSER_MANIFEST=/tmp/tofi-route-manifest.json \
  go test ./internal/app -run '^TestConversationRouteBrowserFixture$' \
  -v -count=1 -timeout=20m
```

Once the manifest exists, run acceptance in another terminal:

```sh
cd ui
TOFI_ROUTE_BROWSER_MANIFEST=/tmp/tofi-route-manifest.json \
PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs \
CHROME_EXECUTABLE=/absolute/path/to/Chrome \
  npm run test:conversation-route-browser
```

POST to the manifest's loopback origin at `/acceptance/stop` when finished. The
fixture permits no production host and exposes controls only in the test binary.

The active isolated worktree is `/tmp/tofibot-routing-20261003`. Documents/iCloud
marked base files dataless and blocked reads, so edits were preserved and applied
to a fresh `/tmp` worktree from the same verified base. Git checks use the
per-command `-c core.fsmonitor=false` override because the host's filesystem
monitor stalled; no shared Git configuration changed.
