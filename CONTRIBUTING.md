# Contributing to TOFI

Thank you for helping. This page tells you how to set up, test and submit a change.

Read [AGENTS.md](AGENTS.md) first. It is the entry point for code conventions.
[docs/conventions.md](docs/conventions.md) has the Go and TypeScript rules.

Note: many internal documents (`docs/`) are written in Chinese. The code,
commit messages, UI strings in `en` and the files in this top-level folder are in
English. Ask in your pull request if you need a translation of a document.

## Licenses

- Everything outside `ui/` is [Apache-2.0](LICENSE).
- `ui/` is [PolyForm Shield 1.0.0](ui/LICENSE).

By contributing, you agree that your change uses the license of the folder you
change.

## Set up

You need Go (the version in `go.mod`), Node 22, and Docker with Compose.
Python 3.10 is needed for the installer and Worker tests.

```sh
npm --prefix ui ci
npm --prefix ui run build
go build ./cmd/tofi ./cmd/tofi-guest
```

To run the full stack from this checkout:

```sh
docker compose up -d --build     # http://127.0.0.1:8321
```

This setup has no Worker and no account computers. Use synthetic data in tests.
Never put real credentials in code, tests or logs.

### Web UI with a live server

You do not need a local backend to change the UI. Point the dev server at any
running TOFI:

```sh
cd ui
TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote
```

Open `http://localhost:5173` in Chrome and sign in once. `dev:remote` accepts
the self-signed certificate of a default install. The preview uses real data
from that server. Use a test install, not a server with other people's data.
To work on components only, open `http://localhost:5173/catalog.html` with
`npm run dev`. It uses fake data and does not call the API.

## Tests to run

Run the tests that match the area you changed. CI runs everything on each pull
request.

| Area | Command |
|---|---|
| Go server | `go build ./...`, `go vet ./...`, `go test ./...`, `scripts/check-gofmt.sh` |
| Web: types, strings, build | `npm --prefix ui run typecheck`, `npm --prefix ui run test:i18n`, `npm --prefix ui run build` |
| Web: one feature | the matching `npm --prefix ui run test:<name>` script (see `ui/package.json`) |
| Installer, Worker, Guest, release scripts | `make self-host-test` |
| Shell scripts and compose files | `make self-host-lint`, `make self-host-config` |
| Built-in Skill scripts | `python3 -m unittest discover -s internal/extensions/builtin_skills/tests -p 'test_*.py'` |

Rules:

- Add a test with each fix or feature. A test must fail before the change.
- Report a test as passing only when the command exits with code 0.
- User-visible text goes in the locale files, not in components. `test:i18n` checks this.
- Keep the Web UI fast. Keep per-keystroke and per-frame cost low. See decision 0002.

### CI checks

`.github/workflows/ci.yml` runs these jobs: `go` (gofmt, build, vet, test),
`ui` (typecheck, i18n, build), `ui-guards` and `ui-catalog` (browser tests with
Playwright), `python` (Worker, Guest, installer, release scripts, built-in
Skills), `shell` (`bash -n`, shellcheck) and `compose` (compose files render).
A pull request must pass all of them.

A script, `scripts/ci-local.sh`, will run the same checks on your machine before
you push. It is not on `main` yet. Until it is, run the commands in the table.

## Commits and pull requests

- One topic for each branch. Branch names look like `feat/<topic>`, `fix/<topic>` and `docs/<topic>`.
- Commit subjects follow `type(scope): imperative summary`. Examples from the log:
  - `fix(web): security review of web tools (extractor budgets, ...)`
  - `feat(installer): computer is optional (--computer none|kvm, ...)`
  - `ui(attachments): refined file-type tiles, ...`
  - `docs(releases): v0.2.1`
  - Types in use: `feat`, `fix`, `ui`, `test`, `docs`, `ci`.
- Write the reason in the body when it is not obvious.
- Do not write AI model names in commit subjects or bodies.
- Do not push to `main` directly. Open a pull request.
- Do not tag releases. Maintainers do that.

## Decisions and design documents

TOFI keeps its reasons in the repository.

- [docs/decisions/](docs/decisions/README.md): one file for each product or process decision, numbered `NNNN-title.md`. Each file says what was decided, when, why, and which code it affects. Add a new file with the next number for a decision that changes behavior or process. The index table in the README lists them.
- [docs/design/](docs/design/README.md): design tokens, components, principles and larger design proposals. Read it before you change the UI. [docs/design/new-page.md](docs/design/new-page.md) shows how to add a page, panel or component.
- [docs/process.md](docs/process.md): how testing, previews, releases and security review work. Section 10 says which file to update for which kind of change.

The rule: a change that alters behavior updates the matching document in the same
commit. This includes `docs/product/status.md` and `docs/releases.md` for
release-level changes.

For a large change, open an issue first and describe the design. Write a
design document in `docs/design/` if the maintainers agree.

## Security

Do not report a vulnerability in a public issue. Follow [SECURITY.md](SECURITY.md).
Changes to the host tool, the Worker, authentication, OAuth, credentials or
network guards get a security review before release (see `docs/process.md`).
