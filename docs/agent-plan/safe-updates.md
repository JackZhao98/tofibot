# Safe Updates: Implementation Contract

Status: approved by owner 2026-10-09. This is the release gate for v0.1.0.

Goal: when we ship a fix, every self-hosted user learns about it, can install it with one command (or have it installed automatically), and loses nothing (data, settings, custom config) even when the update fails.

Owner decisions:
- Auto-update is off unless the operator says yes. The installer asks once, default yes, and it applies to patch releases only (same major.minor).
- Before every update, an automatic backup is taken. The last 3 are kept. Computer disks are not included.

## Facts found in the current code (why this exists)

- `upgrade()` (`deploy/self-host/tofi_host.py` ~1408):
  - It stops the services and starts the new images with no data backup.
  - The App runs schema migrations at start, about 51 `migrate*` / `ensureColumn` functions.
  - A failed start rolls back to the old images on the already-migrated database.
  - The `io.tofi.data-schema` label is the constant `tofi-account-data-v1` (`Dockerfile:41`, `scripts/release/make_manifest.py:27`) and has never been bumped, so the schema gate gives no protection.
- `format_env()` (~353) writes only `ENV_KEYS`, so every update drops operator-added lines. This happened in production on 2026-10-09 with `TOFI_TRUSTED_PROXIES`.
- `upgrade()` does not check for active runs before stopping services.
- `releases/latest` returns 404 while only prereleases exist. The web UI has no update notice.
- `tofi update` has no `--yes`.
- The `status` header says `healthy` while the App is still `starting`.

## Workstream A: host tool (`deploy/self-host/tofi_host.py`, its tests, `compose.yaml`, installer TUI, README)

### A1. Preserve unknown env keys

`format_env` writes known keys first (`ENV_KEYS` order). It then writes every other key from the existing file, verbatim and in its original order. Comments other than the managed header may be dropped.

Test: a file with `TOFI_TRUSTED_PROXIES=…` and `CUSTOM_X=1` survives install-resume, update and rollback.

### A2. Automatic pre-update backup and restore-on-rollback

**`backup_data(reason)`**
- Runs after services are stopped, so SQLite is quiescent. The update already stops them; the backup step goes right after `stopped()` and before `apply_host_config`.
- Writes `/var/lib/tofi/backups/<UTC timestamp>-<from_version>/` containing:
  - `data.tar.zst`, or gzip if zstd is unavailable, of `/var/lib/tofi/data`, excluding nothing inside data;
  - `etc.tar.gz` of `/etc/tofi`, including `tofi.env`, `worker.json` and `tls/`;
  - `meta.json` with `{version, app_image, worker_image, guest, created_at, reason, sizes, sha256 of each archive}`.
- Mode 0700, owner root.
- Excludes `/var/lib/tofi/worker`, which holds the computer disks.
- Before backing up, estimate the size and refuse the update if free space under `/var/lib/tofi` is less than 2× the estimate plus 1 GiB. The message names the numbers.
- Keep the last 3 backups (prune after a successful update).

**Rollback**
- If the candidate was started (step `start-candidate` reached), rollback restores the data and etc archives from the backup **before** starting the previous images.
- Restore is atomic per directory: extract to `<dir>.restore-tmp`, then rename the old directory aside, rename tmp into place, and delete the old one.
- `meta.json` sha256 is verified before restoring.

**New commands**
- `sudo tofi backup [--note TEXT]`: stop → backup → start. It honors the active-run guard (A3).
- `sudo tofi backups`: list backups (time, version, size, reason).
- `sudo tofi restore <backup-id> [--yes]`:
  - Restores data + etc and switches to that backup's version: images and bundle from `meta.json`.
  - If the bundle was pruned, it re-fetches the bundle for that version from the release.
  - It asks for confirmation and honors the active-run guard.
  - It takes a backup of the current state first (reason `pre-restore`).

### A3. Active-run guard

- `active_runs()` reads `/var/lib/tofi/data/tofi.db` read-only (`file:…?mode=ro`, `uri=True`). It counts `runs` with status in `('queued','running','waiting')`.
- If the table is missing or the query errors, it is treated as 0 with a warning, so a broken DB never blocks recovery.
- `update`, `backup`, `restore` and `stop`:
  - If the count is above 0, print the count and wait up to `--wait MINUTES` (default 10 for interactive runs, 0 for auto), polling every 15 s.
  - Then refuse with a clear message unless `--force` is given.

### A4. CLI polish

- `tofi update --yes` (non-interactive; same as stdin not a TTY).
- `tofi update --check` also prints the release notes URL from the manifest (`notes_url`, A6).
- `status` header shows `starting` while any service is starting, `healthy` only when the App health check passes, and `NOT healthy` otherwise.

### A5. Auto-update (patch only)

- Env key `TOFI_AUTO_UPDATE`, value `patch` or `off`. It is in `ENV_KEYS`. Missing means off.
- The installer TUI asks: "Install fix releases automatically? (recommended)", default yes. The non-interactive default is `patch` with `--yes`, `off` otherwise. Flags: `--auto-update` / `--no-auto-update`.
- systemd units installed by the installer:
  - `tofi-auto-update.service` (oneshot) runs `tofi update --auto`.
  - `tofi-auto-update.timer` runs daily at 04:00 local with `RandomizedDelaySec=45min` and `Persistent=true`.
  - Both are removed by uninstall.
- `tofi update --auto`:
  - Exits 0 doing nothing if `TOFI_AUTO_UPDATE` != `patch`.
  - Fetches latest; installs only if same major.minor and newer patch (rc versions never auto-install).
  - Skips, without waiting, if active runs > 0.
  - Logs one line to the journal and appends a JSON line to `/var/lib/tofi/update-history.jsonl`.
- `sudo tofi config auto-update on|off` toggles the env key and enables or disables the timer.
- `status` shows `Updates   automatic (fix releases) · next check 04:12` or `manual`.

### A6. Release manifest `notes_url`

`scripts/release/make_manifest.py` adds `notes_url = https://github.com/<repo>/releases/tag/<version>`. The release workflow passes it. Manifest validation treats it as optional.

### A7. Upgrade acceptance script

`deploy/self-host/upgrade_acceptance.py`, modelled on `acceptance_api.py`. It runs on a test VM as root. Given `--from vX --to vY`:
1. Install X fresh.
2. Create an admin and synthetic data through the HTTP API: a Bot, a skill restricted to that Bot, a preference, and an env var in Keys.
3. Add a custom line to `tofi.env`.
4. `tofi update --version Y --yes`.
5. Assert that all data and the custom env line survive, and that `status` is healthy.
6. Run a forced-failure scenario: an update to a manifest whose App image fails its health check (use a local manifest with a deliberately broken image reference or healthcheck override). Assert that rollback restores X, the data written before the update is intact, and the custom env line is still there.
7. Assert that `tofi backups` lists the pre-update backups and `tofi restore` round-trips.

Synthetic data only. Exit code 0 means pass.

### A acceptance

- `python3 -m unittest discover -s deploy/self-host -p 'test_*.py'` exits 0 with new tests for A1-A6, including backup/restore with a fake data dir, the disk-space refusal, the active-run guard (sqlite fixture), auto-update version gating, and the env round trip.
- `shellcheck install.sh` exits 0. Run `make self-host-embed` if `tofi_tui.py` changes.
- The CI python job is green.

## Workstream B: App update notice (`internal/app`, `ui/src`)

### B1. Server

- `GET /api/system/update` is admin only (single-owner: the owner). It returns `{current, latest, update_available, notes_url, checked_at, auto_update}`.
  - `current` is the build version (ldflags, already used by `/api/server-info`; verify).
  - `latest` comes from the release manifest at `TOFI_UPDATE_MANIFEST_URL`, default `https://github.com/JackZhao98/tofibot/releases/latest/download/manifest.json`.
  - The result is cached 6 h in memory, with failures retried after 10 min.
  - It never blocks a request on the network: serve the cache and refresh in the background.
  - It honors `HTTPS_PROXY`.
  - `auto_update` comes from the env `TOFI_AUTO_UPDATE`; pass it through `compose.yaml`.
  - If `current` is an rc and `latest` is a stable release that is newer, `update_available` is true. Use the same version ordering as the host tool (`version_key`).
- Tests use an httptest manifest server.

### B2. UI

- For admins only, show a dismissible banner at the top of the main view when `update_available`:
  - "Tofi vX is available — run `sudo tofi update` on the server", with a "What's new" link (`notes_url`).
  - Dismissing it hides the banner for that version (localStorage, try/catch).
- If `auto_update` is patch and the new version is a patch release, the text says it will install automatically overnight.
- Settings → Advanced gets a "Version" card showing current, latest, last checked, and auto-update on/off. Toggling it is not possible from the web, so the card shows the CLI command instead.
- i18n in all 7 locales. `test:i18n` must pass with an empty baseline.

### B acceptance

`go build ./... && go test ./...`, `npm --prefix ui run typecheck`, `test:i18n`, `build`, and `test:settings-shell` (extended to cover the Version card and the banner with stubbed `/api/system/update`) all exit 0. Provide screenshots light and dark.

## Release steps after A and B merge (operator: me)

1. Tag rc, then run `upgrade_acceptance.py --from <previous rc> --to <new rc>` on VM 106, and also the forced-failure scenario. Both must exit 0.
2. Upgrade production with the new tool. Its env keeps `TOFI_TRUSTED_PROXIES`; verify after the update.
3. Tag `v0.1.0` (stable) from the same commit. `releases/latest` then resolves, and both `tofi status` and the web banner show "up to date".
4. From then on, fixes ship as `v0.1.x` stable patch releases (rc only for risky changes).

## Review

A2, A3 and A5 handle data and run unattended. Sonnet writes them, and then Fable reviews the diff separately before merge.
