# Dedicated-host installer draft checkpoint

This remains a source-validated draft, pending separately authorized clean
Linux/KVM acceptance and independently prepared artifacts. Private development
has not built/imported images, activated permissions, started services, created
accounts or changed production. The intended host is an empty, dedicated Linux
x86_64 KVM/cgroup-v2/AppArmor Docker host. Images and a sealed Guest release are
operator-provided. Plan writes a new private review directory. Preflight reads
host readiness. Apply requires both the exact reviewed plan SHA-256 and explicit
permission acknowledgement. Existing installations cannot be adopted or purged.
The existing first-Admin mechanism is reused; passwords and the initialization
secret are handled only by the matching browser UI.

For first-Admin setup, the deployment operator also supplies the one-time
`owner-bootstrap.secret` from the App data directory through the browser's
masked initialization-secret field, over the configured HTTPS or local/private
ingress. Keep this file private; do not copy it into URLs, logs or shared notes.
Successful setup consumes its persisted hash with the first account transaction
and removes the file. Restarting an initialized installation does not require
the file and cannot reopen setup. A pending setup still requires its original
file; deleting it does not generate a replacement.

## Fresh D100 installation transaction

The plan schema is integer `2`, kind `d100-fresh-install`. `plan` requires the
existing root/project/image/release/budget/port/output arguments plus sealed
`--app-contract`, `--worker-contract`, `--engine-contract` references, each with
its matching `--*-contract-sha256`. All references use private operator-owned
regular files, canonical absolute paths and lowercase SHA-256 digests. Duplicate,
unknown or malformed metadata is refused. The App contract/evidence below must
bind the exact image, D100 bootstrap contract and matching UI/source. Worker
metadata contains exactly integer `schema: 1`, `image`, `entrypoint` equal to
`["python3","/opt/tofi-worker/worker_entrypoint.py","--config","/etc/tofi-worker/config.json"]`,
and `labels` equal to the image's complete labels. App and Worker image commands
must be empty; their entrypoints are explicitly rendered and checked at runtime.
Labels and attestations require independent artifact provenance/validation.

The engine contract contains exactly integer `schema: 1`, `docker`, `compose`,
`socket`, and `engine_id`. `docker` and `compose` each contain `path` and `sha256`
of a regular operator-owned executable that is not group/world writable. The
Compose path names the standalone local Compose plugin executable. `socket`
contains `path: "/run/docker.sock"`, integer `device` and integer `inode` from its
root-owned Unix socket. `engine_id` is the local Linux daemon's exact ID. Docker
and Compose calls use these executables, the local socket, a private empty Docker
CLI configuration, explicit project/config paths, no `.env` file, and a minimal
execution environment. Ambient Docker/Compose, loader, Python and proxy overrides
are refused. A replaced executable/socket/engine requires a new reviewed plan.

All authority fields are deterministically rendered: owned root/release paths,
project/profile/socket identities, App/Worker images, contract seals, Guest
manifest/binary/manager and logical file sizes, CPU/memory budgets, loopback port,
Worker config, mounts, environment, capabilities, device and security settings,
AppArmor/seccomp/tmpfiles and Compose bytes. Apply checks the exact fixed file
set against a full re-render, beyond checksums of caller-provided files.

After independent artifact and clean-host acceptance, an operator can invoke:

```text
python3 deploy/self_host.py apply /absolute/private/review-directory \
  --plan-sha256 <SHA-256 of the exact reviewed plan.json bytes> \
  --accept-permissions
```

Missing either acknowledgement or seal has zero effects. Apply refuses an
existing root, any stopped/running Compose project resource, loaded/stored
profile, tmpfiles identity or socket root. New claims serialize through the
private `/var/lib/tofi-installer-claims` directory. An exclusive, fsynced claim
retains the full plan/contracts and root-creation intent before creating the
root or installer state. Claims persist after failure/uninstall; an orphan claim
fences retries even when a crash precedes state creation. No automatic resume,
claim reassignment, deletion or adoption exists. Upgrade/uninstall validate new
claims, root inode/device and retained rendered authority. Previously accepted
legacy migrations keep their existing path and are never silently assigned a
new claim. Fresh-marked or externally claimed states missing their claim are
refused. The root directory lock and shared registry lock serialize lifecycle
operations.

The first journal records `phase: installing`, `bootstrap_contract_floor: d100-v1`,
full contracts, artifact/render bindings, the claim/root identity and effect
intents. Each preparation, copy, permission and start intent is fsynced before
its effect. The installer creates a new Guest copy beneath `root/release` from
only manifest, manager, rootfs, kernel, Firecracker and jailer. Symlinks and
hardlinks are refused; exclusive files are streamed, hashed, sized and fsynced,
then sealed read-only with executable modes for Firecracker/jailer. The copied
manifest, manager and embedded Guest validator must pass before startup. The
external candidate is never mounted by the installed Worker.

Pre-copy destination capacity must cover the full logical release copy plus
8 GiB first workspace, 8 GiB internal immutable-copy promise and 1 GiB headroom;
post-copy checks require the remaining 17 GiB. Sparse logical rootfs/kernel sizes
must also fit the internal promise. After the exact Worker starts, an owned
container execution as UID/GID `10001` sends only `{"op":"capacity"}` to its Unix
broker. The first-account gate requires `admission_remaining_bytes >= 16 GiB`,
8 GiB internal reserve and an empty account list. The broker already subtracts
headroom and ledger commitments; the installer does not subtract them again and
does not reserve/create an account. Admission is a point-in-time readiness check;
the existing atomic broker reservation remains authoritative when setup occurs.

Before reporting success, both containers must match exact image IDs, labels,
commands/users, bind paths/read-only modes, limits/security/devices/environment
and loopback network/port authority. App health uses a proxy-free client that
refuses redirects and requires boolean `ok: true`. Only the final fsynced
`phase: installed` / completed intent permits success and ordinary D100 upgrades.
The persisted floor makes the next upgrade require the later N/B pair gate;
first-migration stop-retain is refused on a fresh D100 installation.

Preparation failure retains the claim and owned files without starting cleanup
services. Following any startup attempt, cleanup inventories and verifies every
candidate before stopping exact proven-owned IDs, then verifies clean exits.
Foreign/ambiguous containers are untouched and produce `STOP_UNPROVEN`.
Original, stop and persistence errors remain distinct; a recovery write failure
reports `DURABLE_FENCE_UNPROVEN`. Interrupted journal writes and incomplete phases
refuse apply and ordinary upgrade. There is no fallback, purge, secret reading,
secret regeneration or account creation in fresh apply. The App itself creates
its normal bootstrap secret only when a future authorized installation starts.

Ten table-driven source acceptance families cover consent/seals, full render
binding, claims/concurrency, release copying/capacity, durable ordering, exact
identity/health, preparation faults, start/commit faults, unproven recovery and
success/later D100 gating. Run only `test_d100_hold.FreshApply` for these checks;
its neighboring historical semantic test invokes Go and is a separate gate.
Docker, broker, permissions, health and Guest ext4 extraction are mocked; only
local disposable copy/modes/locks/fsync are real. Existing bounded upgrade and
failure controls remain covered. This is not clean Linux/KVM acceptance or
artifact provenance proof. No host allocation/purchase or permission activation
is authorized by this draft. Before release, independently execute exact App/UI,
Worker/Guest and N/B artifact checks and synthetic clean-host installation,
first-admin setup, reboot, retained upgrade/uninstall and failure recovery using
the unchanged seccomp/AppArmor policy.

## D100 compatibility contract for existing installations

The first migration from old33 to a D100 baseline B consumes obsolete bootstrap
authority in existing data. Unmodified old33 cannot safely restart with that data.
The reviewed first-migration plan must explicitly select `first-migration-stop-retain`:
if B fails, retain the current data and stop. No automatic old33 restart, DB restore,
or revival of a bootstrap file/hash is allowed. This new downtime/migration risk
requires future explicit operator approval before any real migration. Private
source development and these synthetic checks do not authorize it.

After B succeeds, a later N upgrade must name an independently prepared and
validated D100-compatible fallback B by exact local image ID. The fallback may
be a distinct compatible baseline from the currently installed App. Both target
and fallback must include their matching UI. A known startup/health failure may
use only that planned fallback, after another proven clean stop, with CURRENT
data including writes made under N. Successful rollback records the actual B
image and contract in both Compose and installer state. There is no general
"restore previous image" or data rollback option.

Prepare private, operator-owned JSON files with canonical absolute paths. Review
their exact bytes and SHA-256 seals before invoking upgrade. Unknown contracts,
missing evidence, changed seals, mismatched source/UI labels, changed Worker,
interrupted state or an unspecified fallback are refused before stopping services.
All artifact references have the shape `{"path":"/absolute/file.json","sha256":"<64 lowercase hex>"}`.
An App contract contains exactly:

```json
{
  "schema": 1,
  "app_image": "sha256:<64 lowercase hex local image ID>",
  "source_commit": "<40 lowercase hex>",
  "ui_source_commit": "<same source commit>",
  "ui_sha256": "<64 lowercase hex UI manifest digest>",
  "bootstrap_contract": "d100-v1",
  "evidence": {"path": "/absolute/evidence.json", "sha256": "<sealed digest>"}
}
```

The evidence file contains exactly `schema: 1`, `kind: "d100-app-validation"`,
`bindings` equal to the five App contract fields from `app_image` through
`bootstrap_contract`, and `checks` with all four entries set to `"pass"`:
`first_admin_secret`, `initialized_restart`, `consumed_replay`, and
`pending_setup_matching_ui`. Retain the underlying independent validation logs;
the record is an operator-reviewed attestation, not a substitute for executing
those checks against the actual artifact. Include initialized old-account login
and closed setup after migration/restart, plus pending setup through the matching
masked UI. Never put real bootstrap secrets, credentials or account data in records.

The image must carry `io.tofi.account-runtime=1`,
`io.tofi.bootstrap-contract=d100-v1`, `org.opencontainers.image.revision`,
`io.tofi.ui.source`, and `io.tofi.ui.sha256` matching that contract. The Dockerfile
accepts `TOFI_SOURCE_COMMIT` and `TOFI_UI_SHA256` build arguments for these bindings;
empty bindings are refused by upgrade. Define `ui_sha256` as the SHA-256 of the
retained exact UI file-manifest bytes, whose sorted relative paths and individual
file digests cover the complete `/app/ui` tree. Independent artifact validation
must compare that manifest to the image's files and verify the source provenance.
Labels and self-authored passing records alone cannot establish those facts.
No image was built, imported or independently certified by these source checks.

The reviewed upgrade plan contains exactly:

```json
{
  "schema": 1,
  "kind": "d100-app-upgrade",
  "root": "/absolute/owned/install-root",
  "current_app_image": "sha256:<currently installed App ID>",
  "worker_image": "sha256:<unchanged Worker ID>",
  "target": {"path": "/absolute/N-contract.json", "sha256": "<sealed digest>"},
  "failure": {
    "mode": "compatible-fallback",
    "fallback": {"path": "/absolute/B-contract.json", "sha256": "<sealed digest>"},
    "pair_evidence": {"path": "/absolute/N-to-B-pair.json", "sha256": "<sealed digest>"}
  }
}
```

The sealed pair file contains exactly `schema: 1`,
`kind: "d100-current-data-pair-validation"`, `bindings`, `checks` and a sealed
`semantic_evidence` reference. Its bindings are exactly `target_image`,
`fallback_image`, `target_contract_sha256` and `fallback_contract_sha256`, matching
the plan's exact N/B IDs and contract seals. Both the pair file and semantic result
require all six checks set to `"pass"`: `n_data_written`, `current_data_retained`,
`b_data_read`, `b_login`, `b_setup_closed` and `b_consumed_replay_rejected`.
Single-version B evidence cannot replace this exact pair proof.

The semantic result contains exactly `schema: 1`,
`kind: "d100-current-data-semantic-result"`,
`model: "account-workspace-messages-v1"`, `n_written`, `b_read` and `checks`.
Both observations must match and contain `account_id`, `bot_id`,
`conversation_id`, `message_id`, positive integer `message_seq`,
`message_role: "user"` and nonempty `message_content`. These are actual structured
account and relational workspace records, read through B after password login
against the CURRENT databases written by N. A retained file/string marker alone
is refused. Validate the pair against the exact independently prepared artifacts
with synthetic data, then review and seal its observations and underlying logs.
The installer verifies sealed evidence bindings, not the truth of self-authored
claims. No actual distinct-image pair has been executed by these source checks.

For the first migration only, replace `failure` with exactly
`{"mode":"first-migration-stop-retain"}` and select B as the target. Once the
persisted compatibility floor is `d100-v1`, this mode is refused. Invoke only
after operator approval and the remaining installer/host gates above:

```text
python3 deploy/self_host.py upgrade /absolute/owned/install-root \
  --app-image sha256:<target-ID> --worker-image sha256:<unchanged-Worker-ID> \
  --upgrade-plan /absolute/reviewed-upgrade.json --upgrade-plan-sha256 <sealed-digest>
```

Upgrade and uninstall serialize on the existing private install directory.
After a clean stop, upgrade atomically replaces and fsyncs installer state
(file and parent directory) with `bootstrap_contract_floor=d100-v1`, the sealed
plan's full target/fallback contracts and migration intent, before changing Compose
or starting any new binary. Interrupted phases or pending metadata files prohibit
automatic resume. An unclean stop, unknown identity, timeout, interruption,
metadata failure or fallback failure attempts to stop the owned services and
retains their data for operator recovery. If stop verification fails, state records
`STOP_UNPROVEN` with the original, target/fallback and stop errors; it never claims
the services are stopped. A recovery write failure surfaces `DURABLE_FENCE_UNPROVEN`
through the CLI, including the persistence error. No further image starts occur
in either case. Existing D100-labelled installations lacking a persisted floor
are also refused for explicit operator recovery, rather than treated as old33.
A hard process/host interruption cannot
perform cleanup; the persisted intent fences the next installer run. If storage
cannot persist recovery metadata, the command fails and a durable recovery fence
is unproven, including failures before a pending file can be created. Operator
inspection is mandatory. Never infer safe recovery from a health response alone.

This fence applies to starts mediated by this installer. It cannot stop a root
operator from manually invoking Docker or an older installer against the same
data. Retire incompatible scripts/images operationally; direct restarts need
separate review. Fresh-install integrity/recovery now has bounded synthetic source coverage;
real Linux/KVM acceptance and independent artifact provenance remain required.

## Admin computer deletion acceptance gate

The accounts-mode admin panel now has a separate permanent computer deletion
control. Its App journal and Worker tombstone preserve the account, login,
server-side credentials and conversations. The exact selected computer's Guest
disk, runtime files, generated configuration and owned in-directory recovery
copies are removed only after stop/ownership/cleanup proofs. External backups
and shared releases are retained. See `contracts/alpha-api.md` for confirmation,
generation, retry, attachment availability and explicit recreation semantics.
Legacy adopted computers and unresolved offline resize are refused before stop.
The systemd host Broker reports deletion unsupported and refuses before stop; this feature requires the
isolated Worker implementation.

Source checks use only freshly generated mock accounts and disposable files:
`python3 -m unittest discover -s deploy/microvm -p test_account_deletion.py -v`,
`go test -race ./internal/app -run TestAccountComputer -count=1`, and
`cd ui && npm run test:admin-computer` (install Playwright Chromium first, or set
`TOFI_TEST_CHROME` to an installed Chrome executable). The browser test mocks
every API route and creates an isolated browser profile; it cannot issue a live
computer deletion. It covers loss disclosure, typed confirmation, cancel/Escape,
failed cleanup, same-operation retry, account restore and explicit recreation.

Deployment still requires a separately authorized clean Linux/KVM test using
new synthetic accounts only. Verify a running and a disabled computer, manager
and Firecracker exit, empty selected cgroup, removed namespace/veth/filter/NAT
rules, no selected mount/open file, actual disk blocks reclaimed, reservation
and slot release, other account/session/chat/credential retention, Worker/App
restart during partial cleanup, and a new generation after explicit recreation.
Status reads, login and account restore must not allocate or start a deleted
computer. Exercise the existing seccomp/AppArmor policy without broadening it.
Do not use a retained production/test computer or an adopted legacy disk for
this destructive acceptance. Source/browser tests are not Linux/KVM evidence.
