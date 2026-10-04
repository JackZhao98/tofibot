# Dedicated-host installer draft checkpoint

This checkpoint is NOT a verified one-command distribution. No public images,
production changes or new policy loads were performed. `deploy/self_host.py`
contains plan/preflight/upgrade/uninstall scaffolding using the accepted
Worker and exact seccomp/profile baseline. Plan emits a new review directory.
Fresh `apply` is blocked even with `--accept-permissions`: its D100 contract/floor
transaction and broader fresh-install recovery are not implemented. Plan and
preflight remain read-only with respect to the host. The intended host is an empty,
dedicated Linux x86_64 KVM/cgroup-v2/AppArmor Docker host with operator-provided
immutable images and a sealed Guest release. No existing-install adoption or
automatic data purge is offered. The existing first-Admin mechanism is reused;
passwords are entered in the browser, not handled by the installer.

For first-Admin setup, the deployment operator also supplies the one-time
`owner-bootstrap.secret` from the App data directory through the browser's
masked initialization-secret field, over the configured HTTPS or local/private
ingress. Keep this file private; do not copy it into URLs, logs or shared notes.
Successful setup consumes its persisted hash with the first account transaction
and removes the file. Restarting an initialized installation does not require
the file and cannot reopen setup. A pending setup still requires its original
file; deleting it does not generate a replacement.

Validation so far: Python syntax, seven existing synthetic failure-path checks
(deploy/test_self_host.py) and six bounded D100 contract/failure groups
(deploy/test_d100_upgrade.py) pass. They cover hostile paths/mutable image IDs,
permission acknowledgement, unsupported host refusal, unclean-stop fencing,
data-retaining uninstall, bootstrap compatibility preflight, first-migration
stop-retain and planned rollback with current data. Docker, image checks and
service lifecycle are mocked; this is not actual host acceptance. Three held-item
groups (deploy/test_d100_hold.py) additionally cover exact N-to-B pair evidence,
fresh-apply refusal and unproven stop/persistence errors. Their Go source fixture
uses real synthetic account/SQLite stores and handlers across reopen to prove
password login and typed account/bot/message preservation, closed setup and
consumed-secret rejection. It certifies source semantics only; actual N/B image
pair execution and provenance remain required before use.
Before use, complete plan metadata/rendered-file identity binding, ownership/locks,
occupied-identity and partial-apply recovery tests and real clean-host acceptance.
The D100 upgrade path changes only the App image. Worker, Guest and mounts remain
unchanged; permission installation and clean-machine KVM acceptance are still unrun.
Installer integrity checks must bind all plan metadata to rendered files; do not
assume the current checksum list alone establishes that relationship. No operator
should apply this draft before those gates pass.

A clean integration host must be separate from production, expose KVM/tun and
have Docker Compose/cgroup v2/AppArmor/systemd-tmpfiles/e2fsprogs; reserve >=17GiB
for the first8GiB guest plus8GiB internal promise and1GiB headroom, plus source
release/build blocks,1CPU and1GiB host safety. Test only synthetic accounts and
owned project resources; explicitly review generated profile/caps/device/mount
paths before loading. Verify empty first-Admin bootstrap, forced passwords for
Admin-created users, isolation/quota, clean reboot, same-data upgrade and retained
uninstall. No new host allocation/purchase or permission activation is authorized
by the presence of this draft.

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
separate review. This bounded contract does not complete fresh-install plan
integrity, partial-apply recovery, real Linux/KVM acceptance or release readiness.
