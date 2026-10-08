"""Durable admission ledger for account computers; no host commands or sockets.

The ledger reserves physical headroom for all promised disk bytes, including
currently sparse holes. It does not enforce App-data quotas or shrink ext4.
Host-owned callers must supply trusted filesystem measurements and identities.
"""
import contextlib
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import uuid


class AdmissionError(Exception):
    pass


def account_id(value):
    if not isinstance(value, str) or str(uuid.UUID(value)) != value:
        raise ValueError("canonical account UUID required")
    return value


def filesystem_metrics(root):
    stat = os.statvfs(root)
    info = os.stat(root)
    try:
        result = subprocess.run(["findmnt", "-n", "-o", "FSTYPE", "-T", str(root)],
                                check=True, capture_output=True, text=True, timeout=5)
        filesystem_type = result.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        filesystem_type = None
    return {"total_bytes": stat.f_blocks * stat.f_frsize,
            "available_bytes": stat.f_bavail * stat.f_frsize,
            "filesystem_device": info.st_dev,
            "filesystem_type": filesystem_type}


def _ext4_size(path):
    with open(path, "rb") as stream:
        stream.seek(1024)
        superblock = stream.read(1024)
    if len(superblock) != 1024 or superblock[56:58] != b"\x53\xef":
        raise ValueError("ext4 superblock unavailable")
    blocks = int.from_bytes(superblock[4:8], "little")
    incompat = int.from_bytes(superblock[96:100], "little")
    if incompat & 0x80:
        blocks |= int.from_bytes(superblock[336:340], "little") << 32
    log_block_size = int.from_bytes(superblock[24:28], "little")
    if log_block_size > 6:
        raise ValueError("invalid ext4 block size")
    return blocks * (1024 << log_block_size)


def _trusted_regular_path(path):
    path = Path(path)
    if not path.is_absolute():
        raise ValueError("external disk paths must be absolute")
    current = Path(path.anchor)
    for component in path.parts[1:]:
        current = current / component
        info = current.lstat()
        if stat.S_ISLNK(info.st_mode):
            raise ValueError("external disk path contains a symlink")
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode):
        raise ValueError("external workspace disk is not a regular file")
    return info


class CapacityLedger:
    """Durable admission ledger with conservative, live physical-space credit.

    The filesystem is measured before and after scanning disk inodes, and the
    lower available-space reading is used. Background writes outside this
    ledger remain unreserved and are covered only by configured headroom.
    """
    def __init__(self, database, state_root, headroom_bytes, warning_bytes,
                 metrics=None, reserved_slots=(), external_reserved_bytes=0,
                 per_account_internal_reserved_bytes=0, external_disks=(), immutable_image_sizes=None,
                 snapshot_reserve=None):
        if not 0 <= headroom_bytes <= warning_bytes:
            raise ValueError("invalid capacity thresholds")
        for value in (external_reserved_bytes, per_account_internal_reserved_bytes):
            if type(value) is not int or value < 0:
                raise ValueError("capacity reservations must be nonnegative integer bytes")
        self.database = str(database)
        self.root = Path(state_root)
        self.headroom = headroom_bytes
        self.warning = warning_bytes
        self.metrics = metrics or (lambda: filesystem_metrics(self.root))
        self.reserved_slots = set(reserved_slots)
        self.external_reserved = external_reserved_bytes
        self.per_account_internal_reserved = per_account_internal_reserved_bytes
        self.external_disks = tuple(external_disks)
        self.immutable_image_sizes = dict(immutable_image_sizes or {})
        # Hibernation writes each computer's guest memory beside its disk.
        # snapshot_reserve(account_id) returns the bytes promised for that
        # file (its configured guest RAM); None keeps no such promise.
        self.snapshot_reserve = snapshot_reserve
        if (any(name not in ("rootfs.ext4", "vmlinux") or type(size) is not int or size <= 0
                for name, size in self.immutable_image_sizes.items())
                or sum(self.immutable_image_sizes.values()) > self.per_account_internal_reserved):
            raise ValueError("invalid trusted immutable image sizes")
        self._validate_external_config()
        with self.connection() as db:
            db.execute("""CREATE TABLE IF NOT EXISTS computers (
                account_id TEXT PRIMARY KEY, slot INTEGER NOT NULL UNIQUE,
                quota_bytes INTEGER NOT NULL CHECK(quota_bytes>0),
                state TEXT NOT NULL CHECK(state IN ('reserved','ready','disabled'))
            )""")

    def _validate_external_config(self):
        identities = set()
        for item in self.external_disks:
            if (not isinstance(item, dict)
                    or set(item) != {"asset_id", "disk_path", "quota_bytes", "alias_paths"}
                    or not isinstance(item["asset_id"], str) or not item["asset_id"]
                    or item["asset_id"] in identities
                    or type(item["quota_bytes"]) is not int
                    or not 8 * 1024**3 <= item["quota_bytes"] <= 1024 * 1024**3
                    or not isinstance(item["alias_paths"], list)
                    or any(not isinstance(path, str) for path in item["alias_paths"])):
                raise ValueError("invalid external disk inventory")
            identities.add(item["asset_id"])
            paths = [item["disk_path"], *item["alias_paths"]]
            if any(not isinstance(path, str) or not Path(path).is_absolute() for path in paths):
                raise ValueError("external disk paths must be absolute")
            if len(set(paths)) != len(paths):
                raise ValueError("duplicate external disk alias path")

    @contextlib.contextmanager
    def connection(self):
        db = sqlite3.connect(self.database, timeout=15, isolation_level=None)
        db.row_factory = sqlite3.Row
        try:
            yield db
        finally:
            db.close()

    def _snapshot(self, db):
        try:
            host = self.metrics()
            total, available = host["total_bytes"], host["available_bytes"]
            if (type(total) is not int or type(available) is not int
                    or not 0 <= available <= total or total <= 0):
                raise ValueError("invalid filesystem metrics")
            root_stat = self.root.stat()
            measured_device = host.get("filesystem_device", root_stat.st_dev)
            filesystem_type = host.get("filesystem_type")
            if type(measured_device) is not int or measured_device != root_stat.st_dev:
                raise ValueError("state filesystem device changed")
            has_resize_fences = db.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name='resize_fences'").fetchone()
            fences = {}
            if has_resize_fences:
                fences = {row["account_id"]: (row["target_bytes"], row["original_bytes"])
                          for row in db.execute("SELECT account_id,target_bytes,original_bytes FROM resize_fences")}
            accounts = []
            inode_identities = set()
            local_allocated = 0
            for row in db.execute("SELECT * FROM computers ORDER BY account_id"):
                item = dict(row)
                disk = self.root / account_id(item["account_id"]) / "workspace.ext4"
                try:
                    # Never follow an unexpected symlink into another account.
                    if disk.is_symlink() or disk.parent.is_symlink():
                        raise ValueError("unexpected workspace symlink")
                    disk_stat = disk.stat()
                    if not stat.S_ISREG(disk_stat.st_mode):
                        raise ValueError("workspace disk is not a regular file")
                    logical, allocated = disk_stat.st_size, disk_stat.st_blocks * 512
                    if logical > item["quota_bytes"]:
                        raise ValueError("disk exceeds registered quota")
                    identity = (disk_stat.st_dev, disk_stat.st_ino)
                    if identity in inode_identities:
                        raise ValueError("duplicate workspace disk inode")
                    inode_identities.add(identity)
                    if filesystem_type == "ext4":
                        ext4_bytes = _ext4_size(disk)
                        fence = fences.get(item["account_id"])
                        if fence:
                            target, original = fence
                            if (type(target) is not int or target != item["quota_bytes"]
                                    or type(original) is not int or not 8 * 1024**3 <= original <= target
                                    or logical not in (original, target)
                                    or ext4_bytes not in (original, target)
                                    or ext4_bytes > logical):
                                raise ValueError("workspace ext4 geometry conflicts with resize fence")
                        elif ext4_bytes != logical:
                            raise ValueError("workspace ext4 geometry changed")
                    jail_alias = (disk.parent / "jails" / "firecracker" /
                                  ("ac-" + item["account_id"]) / "root" / "workspace.ext4")
                    try:
                        alias_stat = _trusted_regular_path(jail_alias)
                    except FileNotFoundError:
                        alias_stat = None
                    if alias_stat and (alias_stat.st_dev, alias_stat.st_ino) != identity:
                        raise ValueError("workspace jail alias inode changed")
                    known_links = 2 if alias_stat else 1
                    if (filesystem_type != "ext4" or disk_stat.st_dev != root_stat.st_dev
                            or disk_stat.st_nlink != known_links):
                        allocated = 0
                except FileNotFoundError:
                    if item["state"] == "ready":
                        raise ValueError("ready workspace is missing")
                    logical = allocated = 0
                item.update(logical_bytes=logical, allocated_bytes=allocated)
                accounts.append(item)
                local_allocated += min(allocated, item["quota_bytes"])
            promised = sum(a["quota_bytes"] for a in accounts)
            external_promised, external_allocated = self._external_capacity(
                root_stat.st_dev, filesystem_type, inode_identities, db)
            internal_allocated = self._internal_capacity(accounts, root_stat.st_dev,
                                                        filesystem_type, inode_identities)
            snapshot_reserved, snapshot_allocated = self._snapshot_capacity(
                accounts, root_stat.st_dev, inode_identities)
            snapshot_unallocated = snapshot_reserved - snapshot_allocated
            after = self.metrics()
            after_total, after_available = after["total_bytes"], after["available_bytes"]
            after_device = after.get("filesystem_device", root_stat.st_dev)
            after_type = after.get("filesystem_type")
            if (type(after_total) is not int or type(after_available) is not int
                    or not 0 <= after_available <= after_total or after_total <= 0
                    or after_total != total or type(after_device) is not int
                    or after_device != measured_device or after_type != filesystem_type):
                raise ValueError("filesystem geometry changed during capacity scan")
            available = min(available, after_available)
            unallocated = promised - local_allocated
            external_unallocated = external_promised - external_allocated
            internal_reserved = len(accounts) * self.per_account_internal_reserved
            internal_unallocated = internal_reserved - internal_allocated
            remaining = (available - unallocated - external_unallocated - self.headroom
                         - self.external_reserved - internal_unallocated - snapshot_unallocated)
            allocated = local_allocated
            return dict(total_bytes=total, available_bytes=available,
                        allocated_bytes=allocated, promised_bytes=promised,
                        unallocated_promises_bytes=unallocated,
                        external_promised_bytes=external_promised,
                        external_allocated_bytes=external_allocated,
                        external_unallocated_promises_bytes=external_unallocated,
                        external_reserved_bytes=self.external_reserved,
                        per_account_internal_reserved_bytes=self.per_account_internal_reserved,
                        internal_reserved_bytes=internal_reserved,
                        internal_allocated_bytes=internal_allocated,
                        internal_unallocated_reserved_bytes=internal_unallocated,
                        safety_reserved_bytes=self.external_reserved + internal_reserved,
                        snapshot_reserved_bytes=snapshot_reserved,
                        snapshot_allocated_bytes=snapshot_allocated,
                        snapshot_unallocated_reserved_bytes=snapshot_unallocated,
                        admission_remaining_bytes=remaining,
                        warning=(available - unallocated - external_unallocated - self.external_reserved
                                 - internal_unallocated - snapshot_unallocated) < self.warning,
                        accounts=accounts)
        except (OSError, KeyError, TypeError, ValueError) as exc:
            raise AdmissionError("capacity metrics unavailable; admission closed") from exc

    def _snapshot_bytes(self, identity):
        if self.snapshot_reserve is None:
            return 0
        value = self.snapshot_reserve(identity)
        if type(value) is not int or not 0 <= value <= 64 * 1024**3:
            raise ValueError("invalid snapshot reservation")
        return value

    def _snapshot_capacity(self, accounts, device, known_inodes):
        """Promised and already written hibernation snapshot bytes.

        A snapshot file earns credit only up to its account's promise and only
        while it is a single-link, root-owned regular file on this filesystem;
        anything else keeps the full promise (and, being real data, already
        lowers the measured free space).
        """
        reserved = allocated = 0
        for account in accounts:
            promise = self._snapshot_bytes(account["account_id"])
            reserved += promise
            directory = self.root / account_id(account["account_id"]) / "snapshot"
            used = 0
            for name in ("memory", "vmstate"):
                try:
                    if directory.is_symlink():
                        break
                    info = (directory / name).lstat()
                    identity = (info.st_dev, info.st_ino)
                    if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1
                            or info.st_dev != device or identity in known_inodes):
                        continue
                    known_inodes.add(identity)
                    used += info.st_blocks * 512
                except OSError:
                    pass
            account["snapshot_bytes"] = used
            allocated += min(used, promise)
        return reserved, allocated

    def _internal_capacity(self, accounts, device, filesystem_type, known_inodes):
        # Only independent immutable copies consume the internal promise. Shared
        # release hardlinks and unidentified filesystems receive no credit.
        if filesystem_type != "ext4":
            return 0
        allocated = 0
        for account in accounts:
            jail = self.root / account["account_id"] / "jails" / "firecracker" / ("ac-" + account["account_id"]) / "root"
            for name, size in self.immutable_image_sizes.items():
                path = jail / name
                descriptors = []
                try:
                    if not path.is_absolute() or Path(os.path.abspath(path)) != path:
                        continue
                    descriptors.append(os.open(path.anchor, os.O_RDONLY | os.O_DIRECTORY))
                    for part in path.parts[1:-1]:
                        descriptors.append(os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                                                   dir_fd=descriptors[-1]))
                    descriptors.append(os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                                               dir_fd=descriptors[-1]))
                    info = os.fstat(descriptors[-1])
                    identity = (info.st_dev, info.st_ino)
                    if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o222
                            or info.st_dev != device or info.st_size != size or info.st_nlink != 1
                            or identity in known_inodes):
                        continue
                    current = _trusted_regular_path(path)
                    if (current.st_dev, current.st_ino, current.st_size, current.st_blocks,
                            current.st_mode, current.st_uid, current.st_nlink) != (
                            info.st_dev, info.st_ino, info.st_size, info.st_blocks,
                            info.st_mode, info.st_uid, info.st_nlink):
                        continue
                    known_inodes.add(identity)
                    allocated += min(size, info.st_blocks * 512)
                except (OSError, ValueError):
                    pass  # Unknown, removed or changing copies retain the full promise.
                finally:
                    for descriptor in reversed(descriptors):
                        os.close(descriptor)
        return allocated

    def _external_capacity(self, state_device, filesystem_type, known_inodes, db=None):
        promised = allocated = 0
        external_inodes = set()
        for item in self.external_disks:
            # An operator-committed same-inode adoption moves this promise into
            # computers. Never subtract both commitments or trust an App flag.
            adoption = None
            if db is not None and db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='legacy_adoptions'").fetchone():
                adoption = db.execute("SELECT * FROM legacy_adoptions WHERE asset_id=?", (item["asset_id"],)).fetchone()
            if adoption and adoption["phase"] in ("pending", "rollback"):
                raise ValueError("legacy ownership transition is unresolved")
            if adoption and adoption["phase"] == "worker":
                computer = db.execute("SELECT * FROM computers WHERE account_id=?", (adoption["account_id"],)).fetchone()
                disk = self.root / account_id(adoption["account_id"]) / "workspace.ext4"
                info = _trusted_regular_path(disk)
                if (not computer or adoption["source_disk"] != item["disk_path"]
                        or (info.st_dev, info.st_ino) != (adoption["device"], adoption["inode"])
                        or info.st_size != computer["quota_bytes"] or _ext4_size(disk) != info.st_size
                        or any(Path(p).exists() or Path(p).is_symlink() for p in [item["disk_path"], *item["alias_paths"]])):
                    raise ValueError("external adoption ownership proof changed")
                continue
            quota = item["quota_bytes"]
            if adoption and adoption["phase"] == "legacy":
                # A rollback can retain a grown disk; do not silently restore
                # the old quota from the original installation manifest.
                quota = adoption["quota_bytes"]
            promised += quota
            try:
                paths = [item["disk_path"], *item["alias_paths"]]
                stats = [_trusted_regular_path(path) for path in paths]
                first = stats[0]
                if adoption and adoption["phase"] == "legacy" and (first.st_dev, first.st_ino) != (adoption["device"], adoption["inode"]):
                    raise ValueError("rolled back legacy disk inode changed")
                if any((info.st_dev, info.st_ino) != (first.st_dev, first.st_ino)
                       for info in stats[1:]):
                    raise ValueError("external disk alias inode changed")
                if first.st_dev != state_device:
                    raise ValueError("external disk moved to another filesystem")
                identity = (first.st_dev, first.st_ino)
                if identity in external_inodes or identity in known_inodes:
                    raise ValueError("duplicate physical workspace disk inode")
                external_inodes.add(identity)
                if first.st_size != quota:
                    raise ValueError("external disk geometry changed")
                if filesystem_type == "ext4" and _ext4_size(item["disk_path"]) != quota:
                    raise ValueError("external ext4 geometry changed")
                # Non-ext4 or unidentified filesystems are commitments without
                # physical-allocation credit. Ext4 does not support reflinks.
                if filesystem_type == "ext4" and first.st_nlink == len(paths):
                    allocated += min(quota, first.st_blocks * 512)
            except OSError as exc:
                raise ValueError("external disk inventory unavailable") from exc
        return promised, allocated

    def snapshot(self):
        with self.connection() as db:
            db.execute("BEGIN")
            return self._snapshot(db)

    def reserve(self, identity, quota_bytes):
        identity = account_id(identity)
        if type(quota_bytes) is not int or not 8 * 1024**3 <= quota_bytes <= 1024 * 1024**3:
            raise ValueError("disk quota must be 8..1024 GiB")
        with self.connection() as db:
            db.execute("BEGIN IMMEDIATE")
            old = db.execute("SELECT * FROM computers WHERE account_id=?", (identity,)).fetchone()
            snapshot = self._snapshot(db)
            if old:
                # A lower number in a registry is not an ext4 hard limit.
                if quota_bytes < old["quota_bytes"]:
                    raise AdmissionError("quota reduction requires verified offline filesystem resize")
                delta = quota_bytes - old["quota_bytes"]
                slot = old["slot"]
                internal_delta = 0
            else:
                delta = quota_bytes
                internal_delta = self.per_account_internal_reserved + self._snapshot_bytes(identity)
                occupied = self.reserved_slots | {a["slot"] for a in snapshot["accounts"]}
                slot = next((s for s in range(1, 251) if s not in occupied), None)
                if slot is None:
                    raise AdmissionError("computer slots exhausted")
            if delta + internal_delta > snapshot["admission_remaining_bytes"]:
                raise AdmissionError("insufficient reserved disk headroom")
            db.execute("""INSERT INTO computers VALUES(?,?,?,'reserved')
                ON CONFLICT(account_id) DO UPDATE SET quota_bytes=excluded.quota_bytes""",
                       (identity, slot, quota_bytes))
            db.execute("COMMIT")
            return {"account_id": identity, "slot": slot, "quota_bytes": quota_bytes}

    def transition(self, identity, state):
        identity = account_id(identity)
        if state not in ("ready", "disabled"):
            raise ValueError("invalid state")
        with self.connection() as db:
            db.execute("BEGIN IMMEDIATE")
            if db.execute("UPDATE computers SET state=? WHERE account_id=?", (state, identity)).rowcount != 1:
                raise AdmissionError("unregistered computer")
            self._snapshot(db)
            db.execute("COMMIT")

    def abort_unprovisioned(self, identity):
        identity = account_id(identity)
        with self.connection() as db:
            db.execute("BEGIN IMMEDIATE")
            # Existing data and ready/disabled reservations are never reclaimed
            # merely because an account was disabled or a request timed out.
            if (self.root / identity).exists():
                raise AdmissionError("workspace exists; reservation retained")
            db.execute("DELETE FROM computers WHERE account_id=? AND state='reserved'", (identity,))
            db.execute("COMMIT")
