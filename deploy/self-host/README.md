# Dedicated-host installer draft checkpoint

This checkpoint is NOT a verified one-command distribution. No public images,
production changes or new policy loads were performed. `deploy/self_host.py`
contains plan/preflight/apply/upgrade/uninstall scaffolding using the accepted
Worker and exact seccomp/profile baseline. Plan emits a new review directory;
apply requires `--accept-permissions`. Current draft supports only an empty,
dedicated Linux x86_64 KVM/cgroup-v2/AppArmor Docker host with operator-provided
immutable images and a sealed Guest release. No existing-install adoption or
automatic data purge is offered. The existing first-Admin mechanism is reused;
passwords are entered in the browser, not handled by the installer.

Validation so far: Python syntax and seven synthetic failure-path unit checks
pass (deploy/test_self_host.py). They cover hostile paths/mutable image IDs,
permission acknowledgement, unsupported host refusal, unclean-stop fencing,
data-retaining uninstall and rollback to previous code with current data. Docker,
image checks and service lifecycle are mocked; this is not actual host acceptance.
Before use, complete plan metadata/rendered-file identity binding, ownership/locks,
occupied-identity and partial-apply recovery tests and real clean-host acceptance.
Upgrade is limited to compatible App/Worker images and an unchanged Guest release;
permission installation and clean-machine KVM acceptance are still unrun.
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
