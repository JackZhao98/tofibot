# Self-host install (Linux + KVM)

One command installs TOFI on a dedicated Linux x86_64 server with KVM: the same
App + Worker + one Firecracker computer per account as production, everything in
Docker.

```sh
curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash
```

Status: release candidate. Unit tests cover the failure paths; clean-host KVM
acceptance (docs/agent-plan/one-command-install.md, "End-to-end acceptance") is
the gate before `v0.1.0`.

## Requirements

| | Minimum | Recommended |
|---|---|---|
| CPU | Linux x86_64, 2 vCPUs, `/dev/kvm` | 4 vCPUs |
| Memory | 4 GiB | 8 GiB |
| Disk free under `/var/lib` | 30 GiB | 40 GiB |
| OS | Ubuntu 22.04/24.04, Debian 12/13 | |

Also required: `/dev/net/tun`, cgroup v2 and AppArmor enabled in the kernel. The
first account reserves an 8 GiB workspace disk plus 8 GiB internal space. Other
distributions can try `TOFI_ALLOW_UNSUPPORTED=1`.

## Options

`install.sh` accepts (after `sudo bash -s --`):

- `--version vX.Y.Z` — a specific release (default: the latest; `-rc.N`
  prereleases only when named).
- `--domain NAME [--email ADDRESS]` — public HTTPS through a Caddy sidecar with
  automatic certificates. Ports 80 and 443 must be free and DNS must point here.
  Sets `TOFI_PUBLIC_ORIGIN=https://NAME`.
- `--lan` — listen on all interfaces over plain HTTP. Passwords cross the network
  unencrypted; you are asked to confirm unless `--yes`.
- `--port PORT` — App port (default 8321).
- `--yes` — no questions.

The default listens on `127.0.0.1:8321` only. From your computer run
`ssh -L 8321:127.0.0.1:8321 user@server` and open `http://127.0.0.1:8321`, or on
the server run `tailscale serve --bg 8321` and set
`TOFI_PUBLIC_ORIGIN=https://<machine>.<tailnet>.ts.net` in `/etc/tofi/tofi.env`
(then `sudo tofi stop && sudo tofi start`), because the App checks the browser
origin on every change.

## What install.sh does

1. Root and `Linux x86_64`.
2. Supported distribution from `/etc/os-release`.
3. `/dev/kvm`, `/dev/net/tun`, cgroup v2, AppArmor.
4. CPU, memory, disk and free ports. Steps 1-4 change nothing on the host.
5. `apt-get install ca-certificates curl tar zstd python3 e2fsprogs apparmor apparmor-utils`.
6. Docker Engine + Compose plugin from Docker's apt repository when missing
   (falls back to get.docker.com for brand-new distributions); verifies Docker
   uses cgroup v2 and AppArmor.
7. Downloads `manifest.json` from GitHub Releases over HTTPS and validates it:
   images pinned as `name@sha256:...`, artifact URLs from this repository's
   releases, checksums for every file.
8. Downloads `tofi-host-<ver>.tar.gz`, verifies its sha256, unpacks it to
   `/opt/tofi/releases/<ver>`, points `/opt/tofi/current` at it and links
   `/usr/local/bin/tofi`.
9. Runs `tofi install --manifest ...`; the rest is `tofi_host.py`:
10. Under `/run/tofi-installer.lock`: directories, `/etc/tofi/tofi.env`,
    `worker.json` (validated by the Worker's own `validate_config`), AppArmor
    profile `tofi-worker` (`apparmor_parser -Q -T`, then `-r`), tmpfiles, seccomp,
    images pulled by digest and their `io.tofi.*` labels checked, the Guest
    release downloaded, checksummed, unpacked, validated with
    `account_release_check.validate_release` and sealed 0555.
11. Starts the Worker, waits until its broker answers on `/run/tofi/broker.sock`
    with room for the first account, starts the App (and Caddy), waits for
    `/health`, enables `tofi.service`.
12. Prints the URL and the one-time setup key.

Re-running is safe. An installed host is reported, an interrupted operation is
resumed (see below), and a host uninstalled with data retained starts again with
its existing data. Any failure prints one sentence with the actual and required
values; after step 10 it also points at `sudo tofi status` and the journal.

## First sign-in

The App writes a one-time setup key to `/var/lib/tofi/data/owner-bootstrap.secret`.
The installer prints it; `sudo tofi setup-secret` prints it again. Enter it on the
setup page to create the Admin account; the key is consumed and the file removed.
Later accounts are created by the Admin. Then open Settings -> Model provider.

## The `tofi` command

| Command | Effect |
|---|---|
| `tofi status` | Journal phase, container states, health (JSON) |
| `tofi start` / `tofi stop` | Start (Worker first, readiness, App) / stop cleanly; never removes data |
| `tofi update [--version X] [--allow-schema-change]` | Pull and check the new images and Guest before stopping anything; refuse a different `io.tofi.data-schema` unless allowed; on failure restore the previous images, Guest pin and host files with the current data ("update rejected; previous version restored") |
| `tofi uninstall` | Stop and remove containers; keep `/etc/tofi`, `/var/lib/tofi`, images, AppArmor profile; journal `stopped-retained` |
| `tofi uninstall --purge` | After you type the hostname: remove containers, images, profile, unit and every TOFI directory including all data |
| `tofi setup-secret` | Print the pending setup key |
| `tofi logs [app\|worker\|caddy]` | Follow container logs |
| `tofi doctor` | Check KVM, cgroup, profile, `/run/tofi`, Worker config, Guest release, images, disk, health |
| `tofi version` | Host tool, installed release and images |

Mutating commands (`install`, `start`, `stop`, `update`, `uninstall`) take the
host lock; a second one at the same time is refused.

## Files

```
/usr/local/bin/tofi -> /opt/tofi/current/bin/tofi
/opt/tofi/releases/<ver>/      host tool bundle (+ manifest.json); current and previous kept
/opt/tofi/current -> releases/<ver>
/etc/tofi/tofi.env             0600: version, exposure, pinned images, Guest version, budgets
/etc/tofi/worker.json          Worker config (bind-mounted read-only)
/etc/tofi/worker.seccomp.json  Worker seccomp profile
/etc/tofi/Caddyfile            only with --domain
/etc/tofi/install-state.json   journal
/etc/apparmor.d/tofi-worker
/etc/tmpfiles.d/tofi.conf      d /run/tofi 0750 0 10001 -
/etc/systemd/system/tofi.service
/var/lib/tofi/data/            App data (uid 10001, 0700)
/var/lib/tofi/worker/          Worker state, ledger, account disks (root, 0700)
/var/lib/tofi/guest/<ver>/     sealed Guest release (0555)
/var/lib/tofi/caddy/           certificates (with --domain)
/run/tofi/{broker.sock,accounts/}
```

The Compose project is `tofi` from `/opt/tofi/current/compose.yaml`, read with
`--env-file /etc/tofi/tofi.env`. Services: `worker` (root in a private cgroup
namespace, read-only, `cap_drop ALL` plus the nine Worker capabilities, KVM and
TUN devices, AppArmor `tofi-worker`, the seccomp profile), `app` (uid 10001,
read-only, no capabilities) and, with `--domain`, `caddy` (profile `tls`). The
App is always published on `${TOFI_BIND}:${TOFI_HTTP_PORT}`; with `--domain` that
is `127.0.0.1` so only Caddy is public and `tofi status` can still check health.
The shared `mcp-runner` is not part of this stack; account computers run MCP
inside their Guest.

Budgets: the Worker gets `max(1, nproc-1)` vCPUs and
`max(1536, MemTotal-2048)` MiB for account computers (1 vCPU / 1 GiB each), with
1 GiB host memory headroom.

## Journal and recovery

`/etc/tofi/install-state.json` records `phase` (`prepared`, `installing`,
`install-failed`, `installed`, `upgrading`, `upgrade-failed`, `rolling-back`,
`rollback-failed`, `uninstalling`, `uninstall-failed`, `stopped-retained`), the
current step and the last error. An update stores the previous `tofi.env`,
`worker.json` and host bundle before it stops anything. Running `sudo tofi install`
(or install.sh again) resumes:

- an interrupted install: re-applies host config and starts services;
- an interrupted update or rollback: restores the previous version with current data;
- an interrupted uninstall: finishes it, keeping data.

`tofi start` refuses while a transaction is pending. Nothing in this flow deletes
`/var/lib/tofi`; only `tofi uninstall --purge` does.

## Publishing a release

`.github/workflows/release.yml` runs on a `v*` tag (or manually with a tag):
tests, then clean `linux/amd64` builds of `ghcr.io/jackzhao98/tofi`,
`tofi-worker` (and `tofi-mcp-runner` for development) with
`TOFI_SOURCE_COMMIT`, the Guest release
(`TOFI_ACCOUNT_RELEASE=1 deploy/microvm/build-images.sh`, packaged as
`tofi-guest-<tag>-x86_64.tar.zst` with `tar --sparse`), the host bundle
(`scripts/release/make_bundle.sh`) and `manifest.json`
(`scripts/release/make_manifest.py`, Caddy pinned by digest). Tags containing
`-rc` become prereleases, which `releases/latest` never serves.

One-time step: GitHub Actions' token cannot change package visibility. After the
first image push, open GitHub -> your profile -> Packages -> `tofi`,
`tofi-worker`, `tofi-mcp-runner` -> Package settings -> Change visibility ->
Public. The publish job checks anonymous pulls and stops with that instruction
until it is done; re-run the failed job afterwards.

## Tests

```sh
python3 -m unittest discover -s deploy/self-host -p 'test_*.py'   # tofi_host.py + install.sh (stubbed)
python3 -m unittest discover -s scripts/release -p 'test_*.py'    # bundle + manifest
make self-host-lint self-host-config
```

`acceptance_api.py` drives the synthetic end-to-end API checks on a disposable
acceptance host (`initial`, `files`, `verify`, `auth-capacity`,
`restart-refusal`); run it as root so it can read the setup key.

## Known limits

- Behind Caddy the App does not see TLS, so session cookies lack `Secure`
  (no `X-Forwarded-Proto` support yet).
- A new Guest release on update must boot accounts created under the previous
  one; covered only by clean-host acceptance.
- Not supported: container-only computers, macOS/Windows servers, adopting an
  existing deployment, multi-node, unattended updates, backups beyond retained data.
