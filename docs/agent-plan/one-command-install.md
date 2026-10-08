# One-command self-host install (Linux + KVM)

Status: approved plan, 2026-10-07. Executors implement every item; do not cut scope.
Paths are repo-relative.

## Owner decisions

- Server install supports only Linux x86_64 with `/dev/kvm`: the same App + Worker +
  one Firecracker computer per account as production. No container-computer mode;
  macOS/Windows get desktop clients later (non-goal here).
- Everything runs in Docker. One command:
  `curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash`.
  The script installs Docker when missing.
- Images are public on ghcr.io, pinned by digest: `ghcr.io/jackzhao98/tofi`,
  `ghcr.io/jackzhao98/tofi-worker`. Versions `vMAJOR.MINOR.PATCH`, first release
  `v0.1.0`, prereleases `-rc.N`.
- The shared `mcp-runner` container is NOT shipped in the self-host install (account
  workspaces run MCP inside their Guest: `internal/app/local_mcp.go`
  `localRunnerConfig()` -> `computer.RunnerOrigin`). Keep it in the dev `compose.yaml`.
- Minimum host: 2 vCPU, 4 GiB RAM (warn below 8), 30 GiB free under `/var/lib`
  (warn below 40). Keep production's `per_account_internal_reserved_bytes = 8 GiB`.
- Release images are built clean from Dockerfiles (production's stacked 221-layer app
  image must not be reproduced).
- End-to-end acceptance runs on a fresh cloud VM with KVM that the owner provides.

## Findings that shape the design

1. `deploy/self_host.py` on main already renders the production App+Worker contract:
   `generate()` (Worker caps `CAPS`, `/dev/kvm` + `/dev/net/tun`,
   `device_cgroup_rules c 10:257 m`, `cgroup: private`, AppArmor from
   `deploy/self-host/worker.apparmor.template`, seccomp
   `deploy/self-host/worker.seccomp.json`, tmpfiles), the App env block
   (`TOFI_OWNER_AUTH=1`, `TOFI_OWNER_ALLOW_LAN_HTTP=1`, `TOFI_MULTI_ACCOUNT=1`,
   `TOFI_ACCOUNT_MAINTENANCE=0`, `TOFI_ACCOUNT_PROVISIONER_SOCKET`,
   `TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT`, `TOFI_ACCOUNT_COMPUTER_DISK_GIB=8`,
   `TOFI_ACCOUNT_DB_MAX_BYTES`), `preflight()`, `apply()`, `upgrade()`, `uninstall()`.
   It imports `validate_release`/`_load_manifest` from
   `deploy/microvm/account_release_check.py` and `validate_config` from
   `deploy/microvm/worker_entrypoint.py`. This is the repo equivalent of production's
   server-only overlays. `deploy/microvm/setup.sh`, `install-service.sh`, `ops.py` and
   `TOFI_COMPUTER_SOCKET` are the legacy single-computer path; leave them untouched.
2. Security bug: in accounts mode `/api/auth/setup` (`internal/app/accounts.go`) is
   first-come; `owner-bootstrap.secret` is written by `initializeOwnerAuth()`
   (`internal/app/owner_auth.go`) but only single-owner mode checks it. Commit
   `7cd569e` on `codex/d100-fresh-hold-private` fixes it (bootstrap contract
   `d100-v1`). Mandatory for a public installer.
3. Transport: `ownerAuth.transportOK()` accepts TLS, loopback (with
   `TOFI_OWNER_ALLOW_LOOPBACK_HTTP`) or private peers (with `TOFI_OWNER_ALLOW_LAN_HTTP`).
   Inside Docker the peer is the bridge (private), so `TOFI_OWNER_ALLOW_LAN_HTTP=1` is
   required. CSRF `originOK()` uses `TOFI_PUBLIC_ORIGIN` when set; behind a TLS proxy
   it must be `https://<domain>`. Cookies get `Secure` only when `r.TLS != nil`.
4. Guest release = sealed dir (0555) with `manager.py, rootfs.ext4, vmlinux,
   bin/firecracker, bin/jailer, account-release.json` built by
   `TOFI_ACCOUNT_RELEASE=1 deploy/microvm/build-images.sh` ->
   `deploy/microvm/account_release_assemble.py`. `rootfs.ext4` is a 5G sparse file
   (~1.6 GB real). The Worker re-validates it at start against its own
   `/opt/tofi-worker/manager.py`, so Guest release and Worker image must come from the
   same commit; `worker.json` needs matching `expected_guest_sha256` and
   `release_manifest_sha256`.
5. Real-host fixes exist only on `codex/installer-transaction-20261002`: `manager.py`
   `host_memory_headroom_mib` passthrough (`0877c4c`, `b58019e`), image LABELs
   (`io.tofi.data-schema`, `io.tofi.account-guest-protocol`, `io.tofi.account-worker`,
   in `1de61e3`), and the readiness race fix (probe the broker socket from the host as
   uid 10001 instead of `docker exec`).
6. No `.github/` yet. Reuse `scripts/docker-smoke.sh`, `scripts/acceptance.py`, and the
   unittest convention of `deploy/test_self_host.py`.

## Reuse verdict

| Piece | Verdict |
|---|---|
| main `self_host.py` rendering + preflight checks | Keep; move to `deploy/self-host/tofi_host.py`, expand one-liners into readable functions. Replace "no other containers / no firecracker processes" refusal with a warning. |
| branch `lifecycle_lock()` (`/run/tofi-installer.lock`, O_NOFOLLOW, flock) | Keep verbatim. |
| branch journal `install-state.json` phases (`prepared/installing/installed/upgrading/upgrade-failed/rolling-back/stopped-retained`), `transition()/failed()/complete()/resume()` | Keep, simplified. Drop staging sibling dir, renameat2 publication, review-SHA seal, plan-tamper hashing, canonical-bytes checks. |
| branch `rollback()` (previous image pair + previous Guest pin with CURRENT data), `stopped()`, retained uninstall | Keep. |
| branch `worker_ready()` + `BROKER_PROBE` (SO_PEERCRED, `{"op":"capacity"}`) | Keep; drop capability/supplementary-group assertions, keep peer PID/UID check and `remaining_bytes >= first-account promise`. |
| branch `0877c4c`, `b58019e`, LABEL hunks of `1de61e3` | Cherry-pick. |
| branch `deploy/self-host/acceptance_api.py` | Keep as e2e helper. |
| branch plan/preflight/apply three-step UX, `--accept-permissions`, local image IDs | Drop. |
| d100 `7cd569e` bootstrap-secret enforcement (Go + UI) | Port hunks manually (the branch also re-composes features main already has). |
| d100 installer schema 2, sealed contracts, claims dir, pair evidence | Drop. |
| d100 Worker startup cancellation, admin computer deletion | Out of scope. |

## Target architecture

Images (`linux/amd64`, clean builds):
- `ghcr.io/jackzhao98/tofi:<ver>` from `Dockerfile` (uid 10001; labels
  `io.tofi.account-runtime=1`, `io.tofi.data-schema=tofi-account-data-v1`,
  `io.tofi.account-guest-protocol=tofi-account-guest-v1`,
  `org.opencontainers.image.revision`).
- `ghcr.io/jackzhao98/tofi-worker:<ver>` from `deploy/microvm/Worker.Dockerfile`
  (labels `io.tofi.account-worker=1`, guest protocol, revision).
- `docker.io/library/caddy:2.x` pinned by digest (optional TLS sidecar).
- Guest release: GitHub Release asset `tofi-guest-<ver>-x86_64.tar.zst`
  (`tar --sparse | zstd`) + `.sha256`.

Host layout:
```
/usr/local/bin/tofi -> /opt/tofi/current/bin/tofi
/opt/tofi/releases/<ver>/   bin/tofi, lib/tofi_host.py, lib/microvm/{account_release_check.py,worker_entrypoint.py,account_capacity.py,manager.py,worker_cgroups.py,account_provisioner.py,account_adoption.py,worker_supervisor.py}, compose.yaml, Caddyfile.tmpl, worker.apparmor.template, worker.seccomp.json, tofi.service, manifest.json
/opt/tofi/current -> releases/<ver>
/etc/tofi/tofi.env           0600 root: TOFI_VERSION, TOFI_DOMAIN, TOFI_HTTP_PORT(8321), TOFI_BIND(127.0.0.1|0.0.0.0), TOFI_PUBLIC_ORIGIN, TOFI_APP_IMAGE@digest, TOFI_WORKER_IMAGE@digest, TOFI_CADDY_IMAGE@digest, TOFI_GUEST_VERSION, TOFI_CPU_BUDGET, TOFI_MEMORY_BUDGET_MIB
/etc/tofi/worker.json, worker.seccomp.json, Caddyfile (domain only), install-state.json
/etc/apparmor.d/tofi-worker
/etc/tmpfiles.d/tofi.conf    d /run/tofi 0750 0 10001 -
/etc/systemd/system/tofi.service  oneshot RemainAfterExit; ExecStart=tofi start; ExecStop=tofi stop; After=docker.service apparmor.service systemd-tmpfiles-setup.service
/var/lib/tofi/data/          10001:10001 0700
/var/lib/tofi/worker/{state,config,ledger,unused-units}  root 0700
/var/lib/tofi/guest/<ver>/   0555
/var/lib/tofi/caddy/
/run/tofi/{broker.sock,accounts/}
/run/tofi-installer.lock
```

Compose project `tofi`, static `deploy/self-host/compose.yaml` with `${...}` vars,
read with `--env-file /etc/tofi/tofi.env`. Services exactly as `generate()` renders
them today (worker: `user 0:0`, `read_only`, `cgroup: private`, cpus/mem from env,
`pids_limit 512`, `cap_drop ALL` + `CAPS`, devices, `device_cgroup_rules`,
`no-new-privileges`, `apparmor:tofi-worker`, `seccomp:/etc/tofi/worker.seccomp.json`,
`net.ipv4.ip_forward=1`, tmpfs `/run` `/tmp`, binds for worker state, `/run/tofi`,
`/etc/tofi/worker.json:/etc/tofi-worker/config.json:ro`, Guest dir `:ro`; app:
`user 10001:10001`, `init`, `read_only`, `cap_drop ALL`, env block from finding 1 +
`TOFI_PUBLIC_ORIGIN`, binds `/var/lib/tofi/data:/app/data`, `/run/tofi:/run/tofi:ro`,
healthcheck `wget --spider http://127.0.0.1:8321/health`). Profiles: `direct` (app
publishes `${TOFI_BIND}:${TOFI_HTTP_PORT}:8321`) and `tls` (caddy on 80/443,
`reverse_proxy app:8321`, app unpublished).

Worker config (`render_worker_config`): `release_dir=/var/lib/tofi/guest/<ver>`,
`state_root/config_root/unit_root/ledger_root` under `/var/lib/tofi/worker`,
`socket_root=/run/tofi/accounts`, `broker_socket=/run/tofi/broker.sock`,
`socket_gid=10001`, `app_uid=10001`, `headroom_bytes=1 GiB`, `warning_bytes=15 GiB`,
`reserved_slots=[]`, `vcpus=1`, `memory_mib=1024`,
`runtime_vcpu_budget=max(1,nproc-1)`,
`runtime_memory_mib_budget=max(1536, MemTotalMiB-2048)`,
`host_memory_headroom_mib=1024`, `cgroup_root=/run/tofi-worker/cgroup/tofi-vms`,
`external_reserved_bytes=0`, `per_account_internal_reserved_bytes=8 GiB`,
`external_disks=[]`, `expected_guest_sha256`/`release_manifest_sha256` from the
downloaded `account-release.json`.

Exposure:
- default: `TOFI_BIND=127.0.0.1`, URL `http://127.0.0.1:8321`; print SSH tunnel
  (`ssh -L 8321:127.0.0.1:8321 user@host`) and `tailscale serve 8321` instructions.
- `--lan`: `TOFI_BIND=0.0.0.0` with a warning (plaintext passwords on the LAN).
- `--domain D [--email E]`: Caddy profile, automatic HTTPS,
  `TOFI_PUBLIC_ORIGIN=https://D`; preflight checks 80/443 free, warns if D does not
  resolve to a host IP.

## install.sh (repo root)

Bash, `set -euo pipefail`, whole body in `main "$@"` called on the last line (truncated
download does nothing). Flags: `--version vX.Y.Z` (default latest release), `--domain`,
`--email`, `--lan`, `--port`, `--yes`. Each step prints `[n/12]`; each failure prints
one actionable sentence with actual vs required values and exits non-zero.

1. Root, `Linux x86_64`.
2. `/etc/os-release`: Ubuntu 22.04/24.04, Debian 12/13; else exit unless
   `TOFI_ALLOW_UNSUPPORTED=1`.
3. `/dev/kvm` ("KVM is not available (/dev/kvm). Use bare metal or a VM with nested
   virtualization enabled."), `/dev/net/tun`, cgroup v2, AppArmor.
4. `apt-get install -y ca-certificates curl tar zstd python3 e2fsprogs apparmor apparmor-utils`.
5. Docker missing or no compose v2 -> Docker's official apt repo (`docker-ce
   docker-ce-cli containerd.io docker-compose-plugin`; fall back to get.docker.com if
   the distro is not in the repo yet). Verify `docker info` cgroup v2 + apparmor.
6. Resources per owner decisions; ports.
7. Manifest `https://github.com/JackZhao98/tofibot/releases/{latest/download|download/<tag>}/manifest.json`,
   validated with python3: `schema`, `version`, `images{app,worker,caddy}` as
   `name@sha256:...`, `guest{version,url,sha256,manifest_sha256,guest_binary_sha256}`,
   `bundle{url,sha256}`, `min_host`, `data_schema`.
8. Bundle `tofi-host-<ver>.tar.gz`: verify sha256, extract to `/opt/tofi/releases/<ver>`,
   switch `current`, link `/usr/local/bin/tofi`.
9. `exec tofi install --manifest ... [flags]`; the rest is Python under `lifecycle_lock()`.
10. Idempotent: phase `installed` -> print status, exit 0; `stopped-retained` -> start
    with existing data; mid-transaction -> `resume()`. Fresh: dirs and ownership,
    `tofi.env`, `worker.json` (`validate_config`), AppArmor (`apparmor_parser -Q -T`
    then `-r`), tmpfiles, seccomp, pull images by digest + `inspect_image()` label
    checks, Guest download to `.<ver>.partial`, sha256, extract, `validate_release`,
    rename into place 0555. Journal `prepared`.
11. `compose up -d worker` -> `worker_ready()` (needs `remaining_bytes >= 8 GiB
    workspace + 8 GiB internal`) -> `up -d app` (+caddy) -> `/health` ok within 60 s.
    Journal `installed`; `systemctl enable tofi.service`.
12. Print URL and the one-time setup key from
    `/var/lib/tofi/data/owner-bootstrap.secret`, then "Settings -> Model provider".
    On failure after step 10 print `tofi status` and the journal path; never delete data.

`tofi` CLI (bash shim -> `python3 /opt/tofi/current/lib/tofi_host.py`): `install`,
`status`, `start`, `stop` (no `-v`), `update [--version X]` (refuse when
`io.tofi.data-schema` differs unless `--allow-schema-change`; journal `upgrading` with
previous images + Guest; on failure `rollback()` with current data and print "update
rejected; previous version restored"), `uninstall [--purge]` (default keeps
`/etc/tofi`, `/var/lib/tofi`, images, profile, journal `stopped-retained`; `--purge`
requires typing the hostname, then removes dirs, images, AppArmor profile),
`setup-secret`, `logs [app|worker|caddy]`, `doctor`, `version`.

## Release workflow

- `.github/workflows/ci.yml` (PR + main): `go test ./...`; `npm --prefix ui ci`,
  typecheck, build; `python3 -m unittest discover` in `deploy` and `deploy/microvm`
  and `deploy/self-host`; `bash -n` + shellcheck `install.sh`; `docker compose config`
  of `deploy/self-host/compose.yaml` with a fixture env.
- `.github/workflows/release.yml` (tags `v*`, `workflow_dispatch`), ubuntu-24.04:
  1. test (as ci).
  2. images: buildx, login ghcr with `GITHUB_TOKEN` (`packages: write`), build+push app
     and worker (`linux/amd64`, `provenance: false`, no cache, build-arg
     `TOFI_SOURCE_COMMIT`), output digests. Also push `tofi-mcp-runner` for dev use
     (not in the manifest). Make packages public.
  3. guest: free disk, guest-artifact build, `sudo TOFI_ACCOUNT_RELEASE=1 bash
     deploy/microvm/build-images.sh /opt/tofi-guest-build/<tag>`, sparse tar + zstd,
     sha256.
  4. bundle: `scripts/release/make_bundle.sh` -> `tofi-host-<tag>.tar.gz`;
     `scripts/release/make_manifest.py` -> `manifest.json` (caddy digest via
     `docker buildx imagetools inspect`).
  5. publish: GitHub Release with manifest, guest tarball + sha, bundle, install.sh;
     prerelease when the tag contains `-rc`.
- `install.sh` trusts only the manifest from GitHub Releases over HTTPS and verifies
  every artifact sha256; images pulled by digest.

## Workstreams

- WS-A Host installer core: `deploy/self-host/tofi_host.py` (replaces
  `deploy/self_host.py` and `deploy/test_self_host.py`), `deploy/self-host/bin/tofi`,
  `compose.yaml`, `Caddyfile.tmpl`, `tofi.conf`, `tofi.service`; update
  `deploy/open-source-allowlist.json` and `EXPORT-MANIFEST.json`. Functions:
  `preflight`, `render_worker_config`, `inspect_image`, `lifecycle_lock`,
  `worker_ready`, `health`, `stopped`, `install`, `upgrade`, `rollback`, `uninstall`,
  `resume`, `status`. Tests `deploy/self-host/test_tofi_host.py` (unittest, mock
  `run`): each preflight refusal; rendered worker.json passes `validate_config`;
  upgrade failure rolls back and restores `tofi.env` + journal; schema-label mismatch
  refuses before stop; uninstall retains data; reinstall over `stopped-retained`
  keeps data; lock contention refused; Guest sha mismatch leaves no dir; `resume()`
  from each mid phase.
- WS-B `install.sh` + `scripts/release/make_bundle.sh`. Tests: shellcheck, `bash -n`,
  `deploy/self-host/test_install_sh.py` with stubbed PATH (`apt-get`, `docker`,
  `curl`, `id`), `TOFI_OS_RELEASE`/`TOFI_DEV_KVM` test overrides: unsupported distro,
  missing KVM, truncated script has no side effects, manifest sha mismatch aborts,
  hand-off command line.
- WS-C Bootstrap secret (Go + UI), ported from `7cd569e`: `internal/app/accounts.go`
  (`create()` with bootstrap secret, `accountBootstrapState()`, setup handler
  `bootstrap_secret` + `invalid_bootstrap` 401, gateway requires owner auth),
  `internal/app/owner_auth.go`, `internal/app/account_bootstrap_test.go`,
  `ui/src/OwnerSession.tsx` (setup key field when `setup_required`),
  `ui/scripts/test-owner-bootstrap.mjs`, fixture, `package.json` script. Tests: wrong
  secret 401; correct secret creates admin and removes the file; replay refused;
  restart after setup does not recreate the file; existing accounts DB still opens.
  Production already has an admin, so this ships to production safely.
- WS-D Worker/image fixes: cherry-pick `0877c4c`, `b58019e`; LABEL hunks; `ARG
  TOFI_SOURCE_COMMIT` + revision label. Tests: existing `deploy/microvm/test_*.py`
  plus the branch's test additions.
- WS-E Release pipeline: `.github/workflows/{ci,release}.yml`,
  `scripts/release/make_manifest.py` + test, Makefile targets. Acceptance: a
  `v0.1.0-rc.1` tag publishes a prerelease that `install.sh --version v0.1.0-rc.1`
  consumes.
- WS-F Docs: README "Install on your own server"; rewrite
  `deploy/self-host/README.md`; AGENTS.md installer line.

## End-to-end acceptance (owner-provided fresh KVM VM, Ubuntu 24.04, 4 vCPU/8 GiB/40 GiB)

1. No Docker installed. Run the one command. Verify Docker, cgroup v2 + apparmor,
   `aa-status | grep tofi-worker`, `/run/tofi` 0750 root:10001, Guest dir 0555, both
   containers healthy, `tofi status` = installed, URL + setup key printed.
2. Setup page requires the key; wrong key rejected; correct key creates admin; secret
   file gone; second setup returns `setup_unavailable`.
3. Connect a provider; model catalog loads.
4. Account computer reaches ready; bot task "Open https://example.com and tell me the
   heading" shows `browser.read` and the heading.
5. Reboot: everything returns; same account/bot/conversation
   (`acceptance_api.py verify`).
6. `tofi update` to a newer release keeps data; a deliberately broken release rolls
   back to the previous digests; a different `data_schema` is refused before stop.
7. `tofi uninstall` keeps data; rerun install.sh -> login with the existing admin,
   history and computer disk intact; `tofi uninstall --purge` removes everything.
8. Non-KVM VM and too-small disk produce the exact preflight messages and leave no
   files.

## Risks

- Guest release change on update for existing accounts: verify an account created
  under Guest N boots under N+1 with `/workspace` intact.
- Actions runner disk/time for the Guest build: free-disk step, lower zstd level if
  needed.
- Docker apt repo for Debian 13 may lag: fall back to get.docker.com.
- Behind Caddy cookies lack `Secure` (no `X-Forwarded-Proto` support): follow-up.
- `--lan` sends passwords in plaintext on the LAN: opt-in with warning.

## Non-goals

Container-computer mode; macOS/Windows servers; adopting an existing deployment or
legacy personal disk; multi-node; image signing; unattended updates; admin computer
deletion; d100 contract/evidence tooling; backups beyond retained data.
