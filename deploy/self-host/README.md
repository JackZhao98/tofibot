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

## Interactive install

In a terminal, `curl ... | sudo bash` is interactive. Answers are read from
`/dev/tty` (stdin is the curl pipe), and nothing on the host changes until you
confirm:

1. **This machine**: a card listing OS and arch, CPUs, RAM and swap, free disk,
   the Docker version (or "will be installed"), KVM (with the reason when it is
   missing, for example "no /dev/kvm — the CPU does not expose virtualization;
   enable nested virtualization on cloud VMs"), cgroup v2, AppArmor and TUN,
   ports 8321/80/443 (and which process holds a busy one), any existing
   installation (not installed, installed vX, stopped with data retained,
   interrupted install/update/uninstall), and the LAN and public IPv4 addresses.
   Low-RAM, no-swap and disk advisories follow. A hard failure (not Linux
   x86_64, unsupported distribution without `TOFI_ALLOW_UNSUPPORTED=1`, no KVM,
   not root, too small) is reported after the whole card, with every reason,
   and exits 1.
2. **How should each account's computer run?** `KVM (Firecracker)` is the
   default. `gVisor container` and `Plain container` are listed dimmed with
   "not supported yet (coming in a later release)". Without `/dev/kvm` all three
   are dimmed and the installer explains why it cannot continue. The choice is
   stored as `TOFI_COMPUTER_BACKEND=kvm` in `/etc/tofi/tofi.env`.
3. **How will you open TOFI?** `By IP over HTTPS (self-signed)` (default, shows
   `https://<first LAN IP>:8321`), `With a domain (automatic HTTPS)` (dimmed with
   "not supported: ports 80/443 in use by <process>" when they are taken), or
   `This machine only (SSH tunnel)`.
4. Follow-ups. Domain: host name syntax is checked, and you are asked again if
   it is invalid. Its A/AAAA records are resolved, and you get a warning (not a
   failure) if they do not point at this host. Email for Let's Encrypt is
   optional. Port: 8321 by default, or the next free port when 8321 is taken
   (the warning names the process); it must be between 1024 and 65535 and free.
5. Version: the latest stable release. If only prereleases exist, the installer
   says so and asks `Install prerelease vX-rc.N? [y/N]`; it never installs a
   prerelease silently.
6. `Install fix releases automatically? (recommended)` [Y/n]: patch releases
   (same x.y) install overnight when no Bot run is active, after a backup;
   `--auto-update` / `--no-auto-update` answer it, and without a terminal
   `--yes` means yes. See "Safe updates" below.
7. A **Ready to install** card, then `Nothing has changed yet. Proceed? [Y/n]`.

After you confirm, each step is one line with a spinner that ends in ✓ (apt and
Docker output go to `/var/log/tofi-install.log`), ending with the usual banner.

Every prompt: Enter takes the default; spaces and case are ignored; a number or
a name works (`1`, `kvm`, `domain`, `local`). In a capable terminal ↑/↓ and
Enter move through the choices (set `TOFI_PROMPT=plain` for typed answers
instead). Picking a dimmed option prints its reason and asks again. After 5
invalid answers the installer stops ("Too many invalid answers — cancelled,
nothing was changed.", exit 1). Ctrl-C before Proceed prints
"Cancelled — nothing was changed." and exits 130. End of input (Ctrl-D) exits 1
without changes. Answering `n` at Proceed exits 1 without changes.

Running the installer again:

- installed, same version: prints the status and exits 0 without questions;
- `--version` different from the installed one: says to use
  `sudo tofi update --version X` (exit 1);
- uninstalled with data retained: asks only
  `Start the existing installation with its data? [Y/n]`;
- interrupted install, update or uninstall: explains what it found and resumes
  it without questions.

The installer UI is `deploy/self-host/tofi_tui.py`. `install.sh` carries an exact
copy, because it runs before anything is downloaded; refresh the copy with
`make self-host-embed`, and a test fails if they differ. The host bundle ships
the same module, so `tofi doctor` prints the same card and the banner uses the
same palette. If `python3` is missing (some minimal images), install.sh falls
back to the plain checks plus a single Proceed confirmation, and options come
from flags. `TOFI_INSTALLER_UI=plain` forces that fallback.

Output follows the banner rules. With `NO_COLOR` you get glyphs without colour.
With `TERM=dumb` or output that is not a terminal you get plain
`[n/12] step` lines, and dimmed options keep their "not supported" text. The UI
never draws wider than 76 columns, and below 60 columns it drops the art and
frames.

## Options and automation

Flags answer their question in advance; the question is shown as
"(set by --domain)". A variable works when its flag is absent. Flags win over
variables, and an exposure flag (`--domain`, `--local-only`, `--lan`) wins over
every exposure variable. With `sudo`, pass variables after it:
`curl ... | sudo TOFI_DOMAIN=tofi.example.com bash`.

| Flag | Variable | Meaning |
|---|---|---|
| `--computer kvm` | `TOFI_COMPUTER` | how computers run; `gvisor`/`container` exit "not supported yet" |
| `--domain NAME` | `TOFI_DOMAIN` | public HTTPS through Caddy (ports 80/443 free, DNS here); sets `TOFI_PUBLIC_ORIGIN` |
| `--email ADDRESS` | `TOFI_EMAIL` | Let's Encrypt contact (with `--domain`) |
| `--local-only` | `TOFI_LOCAL_ONLY=1` | `127.0.0.1` only (still HTTPS): `ssh -L 8321:127.0.0.1:8321 user@server` |
| `--port PORT` | `TOFI_PORT` | App port, 1024-65535 (default 8321) |
| `--version vX.Y.Z[-rc.N]` | `TOFI_VERSION` | a specific release (default: latest stable) |
| `--auto-update` / `--no-auto-update` | | install fix releases overnight / do not (default without a terminal: yes with `--yes`, otherwise no) |
| `--yes`, `--non-interactive` | `TOFI_YES=1`, `TOFI_NON_INTERACTIVE=1` | no questions |
| `--lan` | | accepted for compatibility; the default |
| | `CI` (any value) | no questions |
| | `TOFI_PUBLIC_IP=ADDR` or `none` | public address shown and used for the DNS check; `none` skips the one HTTPS lookup (`cloudflare.com/cdn-cgi/trace`, 3 s) |
| | `TOFI_PROMPT=plain` | typed answers instead of arrow keys |
| | `TOFI_ALLOW_UNSUPPORTED=1` | try a distribution other than Ubuntu 22.04/24.04, Debian 12/13 |

The installer is non-interactive when any of these holds: `--yes`, `CI` is set,
stdout is not a terminal, or `/dev/tty` cannot be opened. It then never blocks
or asks. It uses flags, variables and defaults, prints the card and the plan,
and stops with an error (exit 1, nothing changed) for anything it would have
asked about: the default port is taken (the error names the next free port),
or only prereleases exist (the error names the newest one to pass with
`--version`). For automation:

```sh
curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh \
  | sudo bash -s -- --yes --version v0.1.0 --domain tofi.example.com --email ops@example.com
```

### Default: self-signed HTTPS on every interface

Without `--domain` the App serves HTTPS itself on `0.0.0.0:8321`, like Proxmox on
`:8006`: open `https://<server-ip>:8321` in a browser, with no tunnel. The
installer creates the certificate once with `openssl` and keeps it across
updates, resumes and reinstalls over retained data:

- `/etc/tofi/tls/cert.pem` (0644) and `key.pem` (0640 root:10001) in a 0750
  root:10001 directory, mounted read-only at `/etc/tofi-tls` in the App;
  `TOFI_TLS_CERT_FILE`/`TOFI_TLS_KEY_FILE` in `tofi.env` point there.
- EC P-256, valid 825 days (the browser maximum), CN = hostname, SANs = the
  hostname, `localhost`, `127.0.0.1`, `::1` and every global address of the
  host's non-virtual interfaces (Docker bridges, veth and tap devices excluded).
- Renewed whenever host config is re-applied (`tofi install`, a resumed
  operation, `tofi update`) if missing or within 30 days of expiry; `sudo tofi regenerate-cert` replaces it on
  demand (for example after the server's IP changed) and restarts the App.

The end of the install (and `tofi status` in a terminal) lists
`https://<ip>:8321` for every IPv4 address, the setup key and the certificate's
SHA-256 fingerprint. The browser warns about the self-signed certificate:
compare the fingerprint, then continue. On a cloud server allow TCP 8321 in its
firewall or security group. `TOFI_PUBLIC_ORIGIN` stays empty, so the App accepts
a change only when the browser's `Origin` equals `https://` + the `Host` it used:
any of the server's addresses works. Owner sign-in accepts the connection
because it is TLS, and the session cookie is `Secure`.

Installs made before this default (plain HTTP on `127.0.0.1`) keep their
`tofi.env` on update; to switch, add `TOFI_BIND=0.0.0.0`,
`TOFI_TLS_CERT_FILE=/etc/tofi-tls/cert.pem`, `TOFI_TLS_KEY_FILE=/etc/tofi-tls/key.pem`
and `TOFI_OWNER_ALLOW_LAN_HTTP=0` to `/etc/tofi/tofi.env`, run
`sudo tofi regenerate-cert`, then `sudo tofi stop && sudo tofi start`.

### Behind another reverse proxy

Sign-in, setup and password attempts are rate limited per client address. Behind
a proxy the App only sees the proxy's address, so every visitor would share one
budget and a single abuser could lock everyone out. Tell the App which proxies to
believe with `TOFI_TRUSTED_PROXIES` (comma-separated IPs or CIDRs; default empty,
which ignores `X-Forwarded-For`). Only when the direct peer is in that list does
the App read `X-Forwarded-For`, walking it from the right and taking the first
address that is not itself a trusted proxy. The proxy must set or append the
header and must not pass a client-supplied value through unchecked (Caddy's
`reverse_proxy` and nginx's `$proxy_add_x_forwarded_for` both append).

- `--domain` installs (the bundled Caddy container) get
  `TOFI_TRUSTED_PROXIES=172.16.0.0/12,192.168.0.0/16`, Docker's bridge ranges;
  with `--domain` the App port is bound to loopback, so only Caddy can reach it.
- Direct installs trust nobody (empty).
- For a proxy on another host, add `TOFI_TRUSTED_PROXIES=10.0.10.0/24` (the
  proxy's address or subnet) to `/etc/tofi/tofi.env`, then
  `sudo tofi stop && sudo tofi start`. `tofi update` keeps the value. An invalid
  entry stops the App at start with a clear error.

## What install.sh does

1. Root and `Linux x86_64`.
2. Supported distribution from `/etc/os-release`.
3. `/dev/kvm`, `/dev/net/tun`, cgroup v2, AppArmor.
4. CPU, memory, disk and free ports (8321 on any address), the "This machine"
   card, then the questions (in a terminal) and Proceed. Steps 1-4 change
   nothing on the host; they only read (plus, for a fresh install, the latest
   release manifest from GitHub and one public-IP lookup).
5. `apt-get install ca-certificates curl tar zstd python3 e2fsprogs apparmor apparmor-utils openssl iproute2`.
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
    `account_release_check.validate_release` and sealed 0555, and (without
    `--domain`) the self-signed certificate in `/etc/tofi/tls` if none is usable.
11. Starts the Worker, waits until its broker answers on `/run/tofi/broker.sock`
    with room for the first account, starts the App (and Caddy), waits for
    `/health`, enables `tofi.service`.
12. Prints the `https://<ip>:8321` addresses, the one-time setup key and the
    certificate fingerprint (plain text when `NO_COLOR` is set, `TERM=dumb` or
    the output is not a terminal).

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
| `tofi status [--json]` | Installed and latest version, App/Worker images, Guest release, services, health, URLs, certificate fingerprint and one row per account computer (in a terminal the install summary first) |
| `tofi computers [--json]` | The account computer rows alone: state, Guest release, pending upgrade, memory, last wake |
| `tofi computers upgrade --all \| --account <id> [--force]` | Move computers on an older Guest release to the installed one (idle ones now, busy ones deferred unless `--force`), then remove Guest releases no computer uses |
| `tofi start` / `tofi stop [--wait MINUTES] [--force]` | Start (Worker first, readiness, App) / stop cleanly; never removes data. `stop` waits for active Bot runs like `update` does (the systemd unit passes `--force` so shutdown is never refused) |
| `tofi update --check [--version X] [--json]` | Report what an update would change (versions, image digests, Guest, data schema, effect on computers) without touching anything; exit 0 up to date, 10 update available, 1 error |
| `tofi update [--version X] [--yes] [--wait MINUTES] [--force] [--allow-schema-change]` | Pull and check the new images and Guest before stopping anything; refuse a different `io.tofi.data-schema` unless allowed; take a backup, then switch; on failure restore the backup, the previous images, Guest pin and host files ("update rejected; previous version restored"). Asks for confirmation on a terminal unless `--yes`; `--check` also prints the release notes link |
| `tofi update --auto` | What the daily timer runs: nothing unless `TOFI_AUTO_UPDATE=patch`; installs only a newer patch of the installed major.minor, never an rc, never when a Bot run is active (skipped, not waited for) |
| `tofi config auto-update on\|off` | Switch automatic fix releases and the systemd timer |
| `tofi backup [--note TEXT]` | Stop, back up data and settings, start again |
| `tofi backups [--json]` | List backups: id, time, version, size, reason |
| `tofi restore <id> [--yes]` | Verify the backup's checksums, back up the current state (`pre-restore`), restore data and settings, and switch to that backup's version |
| `tofi uninstall` | Stop and remove containers; keep `/etc/tofi`, `/var/lib/tofi`, images, AppArmor profile; journal `stopped-retained` |
| `tofi uninstall --purge` | After you type the hostname: remove containers, images, profile, unit and every TOFI directory including all data |
| `tofi setup-secret` | Print the pending setup key |
| `tofi regenerate-cert` | Replace the self-signed certificate (new names/IPs, new fingerprint) and restart the App |
| `tofi logs [app\|worker\|caddy]` | Follow container logs |
| `tofi doctor` | The installer's "This machine" card, then check KVM, computer backend, cgroup, profile, `/run/tofi`, Worker config, Guest release, images, disk, health |
| `tofi version` | Host tool, installed release and images |

Mutating commands (`install`, `start`, `stop`, `update`, `backup`, `restore`,
`config`, `uninstall`, `computers upgrade`, `regenerate-cert`) take the host
lock; a second one at the same time is refused.

## Safe updates

Every update is built so that nothing is lost when it fails.

- **Backup first.** After the services stop (SQLite is quiet) and before
  anything changes, `tofi update` writes `/var/lib/tofi/backups/<UTC time>-<version>/`
  (mode 0700): `data.tar.zst` (gzip when `zstd` is missing) of
  `/var/lib/tofi/data`, `etc.tar.gz` of `/etc/tofi` (without the install
  journal), `worker-meta.tar.gz` (the small Worker files under
  `/var/lib/tofi/worker/{config,ledger,state}`; disk images, snapshots and any
  file over 64 MiB are skipped with a warning; a restore copies them over the
  live ones and deletes nothing), and `meta.json` with the version, images, Guest, time, reason, sizes
  and a sha256 of each archive. Computer disks (`/var/lib/tofi/worker`) are
  never included. The last 3 automatic backups (`pre-update`, `pre-restore`) are kept, pruned
  after a good update; `manual` backups (`tofi backup`) are never pruned: remove
  one with `sudo tofi backups --remove <ID>`.
- **Room check.** Before anything is stopped the update needs 2x the size of
  data + settings plus 1 GiB free under `/var/lib/tofi`; otherwise it refuses and
  prints the numbers.
- **Rollback restores data.** If the new version was started (it may have run
  schema migrations) and then failed, rollback verifies the backup's checksums,
  restores data and settings from it and only then starts the previous images.
  If the new version never started, the data is not touched. Restore is atomic
  per directory: the archive is extracted to `<dir>.restore-tmp`, the live
  directory is renamed aside, the new one renamed in, and only then is the old
  one deleted. Data written while a rejected version was running is discarded
  by design.
- **Active-run guard.** `update`, `backup`, `restore` and `stop` count Bot runs
  in `queued`, `running` or `waiting` (the database is opened read-only). With
  any, they wait up to `--wait MINUTES` (default 10 on a terminal, 0 in scripts
  and the timer), polling every 15 s, then refuse unless `--force`. If the count
  cannot be read it is treated as 0 with a warning, so a broken database never
  blocks recovery.
- **Custom `tofi.env` lines survive.** Unknown keys are kept verbatim across
  install-resume, update, rollback and restore.
- **Automatic fix releases.** The installer asks `Install fix releases
  automatically? (recommended)` (default yes; `--auto-update` /
  `--no-auto-update`; without a terminal `--yes` means yes). It installs
  `tofi-auto-update.timer` (daily 04:00 local, up to 45 min random delay,
  `Persistent=true`) which runs `tofi update --auto`; `TOFI_AUTO_UPDATE` in
  `tofi.env` is `patch` or `off`, switched with `sudo tofi config auto-update
  on|off`. A rejected version is not retried every night. Each run appends one
  line to `/var/lib/tofi/update-history.jsonl` and one to the journal
  (`journalctl -u tofi-auto-update`). `tofi status` shows `Updates automatic
  (fix releases) · next check 04:12` or `manual`.
- **Behind a proxy.** The timer's service runs as root with no login
  environment. If the server reaches GitHub through an HTTP proxy, give the
  unit the proxy: `sudo systemctl edit tofi-auto-update.service` and add
  `[Service]` / `Environment=HTTPS_PROXY=http://proxy.example:3128`, then
  `sudo systemctl daemon-reload`. The release check and downloads honour
  `HTTPS_PROXY` / `NO_PROXY`.
- **When an automatic update fails** `tofi status` shows an `Update` row with the
  version, time and reason, and whether the previous version was restored. If a
  rollback itself did not finish, a `Run` row says to run `sudo tofi install`.
- **Release notes.** `manifest.json` carries an optional `notes_url`
  (`https://github.com/<repo>/releases/tag/<version>`), printed by
  `tofi update --check`.
- **Acceptance.** `deploy/self-host/upgrade_acceptance.py --from vX --to vY
  --broken-manifest M` (as root on a disposable VM) installs X, creates
  synthetic data, updates to Y, forces a failing update and exercises
  `tofi backups` / `tofi restore`; exit 0 is a pass.

```
$ sudo tofi backups
  ID                       CREATED (UTC)         VERSION      SIZE      REASON
  20261009T041203Z-v0.1.0  2026-10-09T04:12:03Z  v0.1.0       42.0 MiB  pre-update

  Restore one with: sudo tofi restore <ID>
  Automatic backups (pre-update, pre-restore): the last 3 are kept. Manual ones stay until you remove them: sudo tofi backups --remove <ID>
```

## Versions and account computers

`tofi status` shows what is installed and what `tofi update` would install:

```
$ sudo tofi status
TOFI v0.1.0 · installed · healthy
  Latest       v0.2.0 available · sudo tofi update --check
  App          ghcr.io/jackzhao98/tofi@sha256:111111111111
  Worker       ghcr.io/jackzhao98/tofi-worker@sha256:111111111111
  Guest        v0.1.0
  Services     app healthy · worker running

Computers (4)
  ACCOUNT   STATE       GUEST   UPGRADE  MEMORY   LAST WAKE
  3f2a9c1e  running     v0.1.0  -        612 MiB  restore 2.1s
  9b77d0aa  hibernated  v0.0.9  pending  -        -
  c0ffee00  stopped     -       -        -        -
  e1e1e1e1  running     v0.1.0  -        612 MiB  cold boot 14.2s

  1 computer is on an older Guest than v0.1.0; a hibernated one cold-boots on v0.1.0 at its next use.
  Switch now: sudo tofi computers upgrade --all
```

"Latest" is the newest published release (`releases/latest`, which never
serves `-rc` prereleases), checked at most every 6 hours and cached in
`/var/cache/tofi/latest-release.json`; offline it shows the last answer or
"unknown" and never fails. A computer's state is `running`, `starting`,
`restarting`, `hibernating`, `hibernated`, `stopped`, `unresponsive` (its
guest stopped answering), `error` or `disabled`. GUEST is the release a running
computer runs, or the one a hibernated computer's snapshot was taken with; a
stopped computer has none (its next start uses the installed Guest). MEMORY is
the Firecracker process RSS. The rows come from the Worker broker (`computers`
operation) and each computer's control socket (`/v1/info`, `/v1/resources`,
`/v1/health`), asked as uid 10001 like the readiness probe; asking never starts
or wakes a computer.

Before updating:

```
$ sudo tofi update --check
Installed    v0.1.0
Available    v0.2.0  (update available)

  COMPONENT      INSTALLED             AVAILABLE
  Host bundle    v0.1.0                v0.2.0                changes
  App image      sha256:111111111111   sha256:222222222222   changes
  Worker image   sha256:111111111111   sha256:222222222222   changes
  Guest release  v0.1.0                v0.2.0                changes
  Data schema    tofi-account-data-v1  tofi-account-data-v1  compatible

Computers    4 total: 2 running, 1 hibernated
  The update restarts the Worker: running computers are hibernated first.
  e1e1e1e1 is busy (run_lease): its current task is interrupted.
  The Guest changes to v0.2.0: each of them cold-boots on it at its next use (open pages are not kept; the workspace disk is).

Apply with: sudo tofi update
$ echo $?
10
```

`--version X` checks a specific release; `--json` prints the same report for
scripts. Exit code 0 means up to date, 10 an update is available, anything else
an error (for example no network).

What an update does to the computers: every computer runs inside the Worker
container, which only mounts the installed Guest release, so `tofi update`
(which restarts the Worker) hibernates each running computer and nothing keeps
running on the old Guest. A hibernation snapshot belongs to the release it was
taken with (Firecracker, kernel and root filesystem); under a new Guest the
computer cold-boots from its workspace disk at its next use instead of
restoring, so open pages are lost but files are kept. Busy work (a Bot run, a
viewer, a terminal job) is interrupted by the update; check with
`tofi update --check` first.

Guest releases stay on disk while anything needs them: the installed one, the
previous one (for rollback) and every release a hibernated computer's snapshot
was taken with (a rollback to it restores those computers with their pages).
`tofi computers upgrade` makes the switch explicit and frees that space:

```
$ sudo tofi computers upgrade --all
  ACCOUNT   STATE       FROM    TO      RESULT    NOTE
  3f2a9c1e  running     v0.0.9  v0.1.0  upgraded  stopped; next start boots v0.1.0
  9b77d0aa  hibernated  v0.0.9  v0.1.0  upgraded  snapshot discarded; next start boots v0.1.0
  c0ffee00  stopped     -       v0.1.0  current   next start boots v0.1.0
  e1e1e1e1  running     v0.0.9  v0.1.0  deferred  busy (run_lease); retry when idle or use --force

  1 current, 1 deferred, 2 upgraded
  Removed unused Guest releases: v0.0.9
```

For each computer on an older Guest: a hibernated one has its old snapshot
discarded (the workspace disk stays); an idle running one is stopped; a busy
one (Bot run lease, live viewer, human control, desktop operation, terminal job
or output, or no answer) is reported `deferred` and left alone. Nothing is
started: each computer boots the installed Guest when it is next used.
`--account <id>` takes a full account id or an 8+ character prefix. `--force`
also stops busy computers after you type `yes` on the terminal (without a
terminal, `--force` alone decides); their running tasks are interrupted.
Afterwards Guest releases that are neither installed nor used by a remaining
computer are removed. Exit code 0: every chosen computer is on the installed
Guest; 2: some were deferred; 1: an error. The host stays in phase `installed`
(services keep running); the outcome is recorded as `last_computers_upgrade` in
the journal.

## Files

```
/usr/local/bin/tofi -> /opt/tofi/current/bin/tofi
/opt/tofi/releases/<ver>/      host tool bundle (+ manifest.json); current and previous kept
/opt/tofi/current -> releases/<ver>
/etc/tofi/tofi.env             0600: version, exposure, pinned images, Guest version, budgets, TOFI_COMPUTER_BACKEND
/etc/tofi/worker.json          Worker config (bind-mounted read-only)
/etc/tofi/worker.seccomp.json  Worker seccomp profile
/etc/tofi/Caddyfile            only with --domain
/etc/tofi/tls/{cert,key}.pem   self-signed certificate (default mode; key 0640 root:10001)
/etc/tofi/install-state.json   journal
/etc/apparmor.d/tofi-worker
/etc/tmpfiles.d/tofi.conf      d /run/tofi 0750 0 10001 -
/etc/systemd/system/tofi.service
/etc/systemd/system/tofi-auto-update.{service,timer}   automatic fix releases (timer enabled only when TOFI_AUTO_UPDATE=patch)
/var/lib/tofi/data/            App data (uid 10001, 0700)
/var/lib/tofi/worker/          Worker state, ledger, account disks (root, 0700)
/var/lib/tofi/guest/<ver>/     sealed Guest releases (0555): installed, previous, and any a snapshot uses
/var/cache/tofi/latest-release.json  last answer of the latest-release check
/var/lib/tofi/backups/<UTC time>-<version>/   pre-update / manual / pre-restore backups (0700; last 3 automatic kept, manual ones until removed)
/var/lib/tofi/update-history.jsonl   one line per update attempt (manual and automatic)
/var/lib/tofi/caddy/           certificates (with --domain)
/run/tofi/{broker.sock,accounts/}
```

The Compose project is `tofi` from `/opt/tofi/current/compose.yaml`, read with
`--env-file /etc/tofi/tofi.env`. Services: `worker` (root in a private cgroup
namespace, read-only, `cap_drop ALL` plus the nine Worker capabilities, KVM and
TUN devices, AppArmor `tofi-worker`, the seccomp profile), `app` (uid 10001,
read-only, no capabilities) and, with `--domain`, `caddy` (profile `tls`). The
App is always published on `${TOFI_BIND}:${TOFI_HTTP_PORT}` (`0.0.0.0` by
default, HTTPS); with `--domain` that is `127.0.0.1` and plain HTTP so only Caddy
is public and `tofi status` can still check health. In that mode Caddy reaches
the App over the Docker bridge, which is why `TOFI_OWNER_ALLOW_LAN_HTTP=1` stays
for it; the default HTTPS mode sets it to `0`. The App healthcheck picks `https`
or `http` from `TOFI_TLS_CERT_FILE` (busybox `wget` with Alpine's `ssl_client`).
The shared `mcp-runner` is not part of this stack; account computers run MCP
inside their Guest.

Budgets: the Worker gets `max(1, nproc-1)` vCPUs and
`max(1536, MemTotal-2048)` MiB for account computers (1 vCPU / 1 GiB each), with
1 GiB host memory headroom. Each computer has a virtio balloon with free page
reporting: freed guest RAM goes back to the host, and once its desktop has
idled out (Chrome stopped) the guest drops its page cache and compacts, so an
idle computer holds roughly 0.3 GiB instead of its full allocation. Every start
is still gated on the host's live `MemAvailable` plus headroom. The static
claim ceiling does not overcommit; an operator can raise it with the optional
`runtime_memory_overcommit_percent` (100-200) in `worker.json`, accepting that
computers that all become busy again are not stopped by the Worker.

## Journal and recovery

`/etc/tofi/install-state.json` records `phase` (`prepared`, `installing`,
`install-failed`, `installed`, `upgrading`, `upgrade-failed`, `rolling-back`,
`rollback-failed`, `uninstalling`, `uninstall-failed`, `stopped-retained`), the
current step and the last error. An update stores the previous `tofi.env`,
`worker.json` and host bundle before it stops anything, and a backup of the data and `/etc/tofi` right after. Running `sudo tofi install`
(or install.sh again) resumes:

- an interrupted install: re-applies host config and starts services;
- an interrupted update or rollback: restores the previous version (and, if the new version had started, the pre-update backup of data and settings);
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
`restart-refusal`); run it as root so it can read the setup key. Against the
default self-signed HTTPS pass `--url https://<ip>:8321 --insecure`; `initial`
then also checks that the session cookie is `Secure`.

## Known limits

- Behind Caddy the App does not see TLS, so session cookies lack `Secure`
  (no `X-Forwarded-Proto` support yet).
- A new Guest release on update must boot accounts created under the previous
  one. The Worker regenerates each computer's manager config for the new release
  on its next start (an older Worker refused with "existing generated artifact
  differs"); covered by unit tests and only by clean-host acceptance on KVM.
- Not supported: container-only computers, macOS/Windows servers, adopting an
  existing deployment, multi-node, unattended updates, backups beyond retained data.
