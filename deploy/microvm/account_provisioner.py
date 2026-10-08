"""Private host broker. Not installed or enabled by importing this module."""
import argparse
import fcntl
import http.server
import hashlib
import json
import os
from pathlib import Path
import socket
import socketserver
import stat
import sqlite3
import struct
import subprocess

from account_capacity import AdmissionError, CapacityLedger, account_id
import account_adoption



def runtime_memory_claim_limit(config):
    """Static memory claim ceiling for concurrently running computers.

    Claims are full VM sizes. The optional overcommit percentage (100..200,
    default 100 = none) only raises this static ceiling; every start must
    still pass the live MemAvailable + headroom check. Each VM stays capped by
    its own cgroup, but simultaneous regrowth of overcommitted VMs is NOT
    prevented, so operators enable it deliberately.
    """
    percent = config.get("runtime_memory_overcommit_percent", 100)
    if type(percent) is not int or not 100 <= percent <= 200:
        raise ValueError("runtime_memory_overcommit_percent must be an integer from 100 to 200")
    return config["runtime_memory_mib_budget"] * percent // 100

class Broker:
    def __init__(self, config, run=None, metrics=None):
        self.c = config
        self.release = Path(config["release_dir"])
        if not self.release.is_absolute() or not (self.release / "manager.py").is_file():
            raise ValueError("trusted manager release required")
        self.run = run or self._command
        for key in ("state_root", "config_root", "unit_root", "socket_root", "ledger_root"):
            path = Path(config[key])
            if not path.is_absolute() or path.is_symlink():
                raise ValueError("trusted absolute roots required")
            path.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.ledger = CapacityLedger(Path(config["ledger_root"]) / "ledger.sqlite",
            config["state_root"], config["headroom_bytes"], config["warning_bytes"],
            metrics=metrics, reserved_slots=config["reserved_slots"],
            external_reserved_bytes=config.get("external_reserved_bytes", 0),
            per_account_internal_reserved_bytes=config.get("per_account_internal_reserved_bytes", 0),
            external_disks=config.get("external_disks", ()),
            immutable_image_sizes=config.get("_validated_immutable_image_sizes"),
            snapshot_reserve=self.snapshot_reserve)
        os.chmod(self.ledger.database, 0o600)
        with self.ledger.connection() as db:
            account_adoption.initialize(db)
            db.execute("""CREATE TABLE IF NOT EXISTS owned_files(
                path TEXT PRIMARY KEY, sha256 TEXT NOT NULL, mode INTEGER NOT NULL)""")
            db.execute("""CREATE TABLE IF NOT EXISTS runtime_claims(
                account_id TEXT PRIMARY KEY REFERENCES computers(account_id),
                vcpus INTEGER NOT NULL, memory_mib INTEGER NOT NULL)""")
            db.execute("""CREATE TABLE IF NOT EXISTS resize_fences(
                account_id TEXT PRIMARY KEY REFERENCES computers(account_id),
                target_bytes INTEGER NOT NULL, original_bytes INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)""")
            columns = {row[1] for row in db.execute("PRAGMA table_info(resize_fences)")}
            if "original_bytes" not in columns:
                db.execute("ALTER TABLE resize_fences ADD COLUMN original_bytes INTEGER NOT NULL DEFAULT 0")

    @staticmethod
    def _command(args, **kwargs):
        kwargs.setdefault("check", True)
        return subprocess.run(args, timeout=60, capture_output=True, **kwargs)

    def identity(self, value):
        identity = account_id(value)
        name = "ac-" + identity
        return identity, name, "tofi-computer-" + name + ".service"

    def write_owned(self, path, content, mode):
        if path.exists() or path.is_symlink():
            # A crash retry may reuse exactly the same generated artifact.
            if path.is_symlink() or path.read_text() != content:
                raise AdmissionError("existing generated artifact differs")
            self.record_owned(path, content, mode)
            return
        temporary = path.with_suffix(path.suffix + ".pending")
        if temporary.exists() or temporary.is_symlink():
            info = temporary.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
                raise AdmissionError("unexpected pending artifact")
            # A partial write belongs to this root-owned generated location;
            # regenerate from trusted config after acquiring the broker lock.
            temporary.unlink()
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        self.record_owned(path, content, mode)

    def replace_owned(self, path, content, mode):
        """Atomically replace only a manifest-verified broker-owned regular file."""
        path = Path(path)
        try:
            info = path.lstat()
        except FileNotFoundError as exc:
            raise AdmissionError("owned manager config is missing") from exc
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o777 != mode:
            raise AdmissionError("manager config ownership or mode changed")
        with self.ledger.connection() as db:
            record = db.execute("SELECT sha256,mode FROM owned_files WHERE path=?", (str(path),)).fetchone()
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if not record or record["mode"] != mode or record["sha256"] != digest:
            raise AdmissionError("manager config does not match owned manifest")
        temporary = path.with_suffix(path.suffix + ".pending")
        if temporary.exists() or temporary.is_symlink():
            info = temporary.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
                raise AdmissionError("unexpected pending artifact")
            temporary.unlink()
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        dirfd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(dirfd)
        finally:
            os.close(dirfd)
        self.record_owned(path, content, mode)

    def _disk_path(self, identity):
        identity = account_id(identity)
        slot = self._slot(identity)
        if type(slot) is not int or not 1 <= slot <= 250:
            raise AdmissionError("invalid account computer slot")
        manager_uid = 61000 + slot
        directory = Path(self.c["state_root"]) / identity
        disk = directory / "workspace.ext4"
        if directory.is_symlink() or disk.is_symlink():
            raise AdmissionError("unexpected account disk path")
        try:
            info = disk.lstat()
        except FileNotFoundError as exc:
            raise AdmissionError("workspace disk is missing") from exc
        # Manager startup assigns the disk (and its jail hardlink) to this
        # account's fixed jailer UID. Only the trusted ledger determines that
        # UID; accepting another account's slot would cross the disk boundary.
        manager_owned = (info.st_uid == manager_uid and info.st_gid == manager_uid
                         and stat.S_IMODE(info.st_mode) == 0o600)
        if not stat.S_ISREG(info.st_mode) or (info.st_uid != os.geteuid() and not manager_owned):
            raise AdmissionError("workspace disk is not an owned regular file")
        return disk

    @staticmethod
    def _ext4_blocks(disk):
        with disk.open("rb") as stream:
            stream.seek(1024)
            sb = stream.read(1024)
        if len(sb) != 1024 or sb[56:58] != b"\x53\xef":
            raise AdmissionError("workspace ext4 superblock unavailable")
        blocks = int.from_bytes(sb[4:8], "little")
        incompat = int.from_bytes(sb[96:100], "little")
        if incompat & 0x80:  # ext4 64bit feature
            blocks |= int.from_bytes(sb[336:340], "little") << 32
        log_block = int.from_bytes(sb[24:28], "little")
        if log_block > 6:
            raise AdmissionError("invalid ext4 block size")
        return blocks, 1024 << log_block

    def _check_filesystem_size(self, disk, target_bytes):
        blocks, block_size = self._ext4_blocks(disk)
        if disk.stat().st_size != target_bytes or blocks * block_size != target_bytes:
            raise AdmissionError("workspace ext4 size does not match reserved quota")

    def _resize_account(self, identity, target_bytes, original_bytes, unit):
        disk = self._disk_path(identity)
        # Manager startup may prefer these files over its generated config.
        # Accept only valid files whose disk value agrees with the current disk.
        old_bytes = disk.stat().st_size
        resource_files = ("resources-applied.json", "resources-desired.json",
                          "resources-desired.restart-hold.json")
        for filename in resource_files:
            path = disk.parent / filename
            if path.exists() or path.is_symlink():
                if path.is_symlink() or not stat.S_ISREG(path.lstat().st_mode):
                    raise AdmissionError("unexpected manager resource override")
                try:
                    value = self._manager_resource(path, (original_bytes, target_bytes))
                except (ValueError, TypeError, json.JSONDecodeError) as exc:
                    raise AdmissionError("manager resource override conflicts with workspace") from exc
        config_path = Path(self.c["config_root"]) / (identity + ".json")
        cfg = None
        if config_path.exists() or config_path.is_symlink():
            try:
                cfg = json.loads(config_path.read_text())
            except (OSError, ValueError) as exc:
                raise AdmissionError("manager config cannot be verified") from exc
            if cfg.get("disk_gib", 0) * 1024**3 not in (original_bytes, target_bytes):
                raise AdmissionError("manager config disk quota conflicts with workspace")
            if not stat.S_ISREG(config_path.lstat().st_mode) or config_path.is_symlink():
                raise AdmissionError("manager config is not a regular owned file")
            info = config_path.lstat()
            with self.ledger.connection() as db:
                manifest = db.execute("SELECT sha256,mode FROM owned_files WHERE path=?", (str(config_path),)).fetchone()
            if (info.st_uid != os.geteuid() or info.st_mode & 0o777 != 0o600 or not manifest
                    or manifest["mode"] != 0o600
                    or manifest["sha256"] != hashlib.sha256(config_path.read_bytes()).hexdigest()):
                raise AdmissionError("manager config does not match owned manifest")
        self.stop_manager(identity, unit)
        # The supervisor has proved the VM cgroup empty; only now can its
        # durable runtime allocation be released.
        with self.ledger.connection() as db:
            db.execute("DELETE FROM runtime_claims WHERE account_id=?", (identity,))
        if old_bytes > target_bytes:
            raise AdmissionError("offline shrink required")
        check = self.run(["e2fsck", "-f", "-p", str(disk)], check=False)
        if check.returncode not in (0, 1):
            raise AdmissionError("workspace filesystem check failed")
        if old_bytes < target_bytes:
            with disk.open("r+b") as stream:
                stream.truncate(target_bytes)
                stream.flush()
                os.fsync(stream.fileno())
        blocks, block_size = self._ext4_blocks(disk)
        if blocks * block_size != target_bytes:
            resize = self.run(["resize2fs", str(disk)], check=False)
            if resize.returncode != 0:
                raise AdmissionError("workspace filesystem resize failed")
        check = self.run(["e2fsck", "-f", "-p", str(disk)], check=False)
        if check.returncode not in (0, 1):
            raise AdmissionError("workspace filesystem verification failed")
        self._check_filesystem_size(disk, target_bytes)
        if config_path.exists():
            cfg["disk_gib"] = target_bytes // 1024**3
            self.replace_owned(config_path, json.dumps(cfg, sort_keys=True) + "\n", 0o600)
        for filename in resource_files:
            path = disk.parent / filename
            if path.exists():
                value = json.loads(path.read_text())
                value["disk_gib"] = target_bytes // 1024**3
                self._replace_manager_resource(path, value)
        with self.ledger.connection() as db:
            db.execute("DELETE FROM resize_fences WHERE account_id=?", (identity,))

    @staticmethod
    def _manager_resource(path, accepted_disk_bytes):
        info = path.lstat()
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o777 != 0o600):
            raise AdmissionError("manager resource file ownership or mode changed")
        value = json.loads(path.read_text())
        bounds = {"vcpus": (1, 32), "memory_mib": (512, 32768), "disk_gib": (8, 1024)}
        if not isinstance(value, dict) or set(value) != set(bounds):
            raise ValueError("invalid manager resource file")
        for key, (low, high) in bounds.items():
            if type(value[key]) is not int or not low <= value[key] <= high:
                raise ValueError("invalid manager resource value")
        if value["disk_gib"] * 1024**3 not in accepted_disk_bytes:
            raise ValueError("manager disk setting conflicts with workspace")
        return value

    @staticmethod
    def _replace_manager_resource(path, value):
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o777 != 0o600:
            raise AdmissionError("manager resource file ownership or mode changed")
        temporary = path.with_suffix(path.suffix + ".quota-pending")
        if temporary.exists() or temporary.is_symlink():
            raise AdmissionError("unexpected manager resource temporary file")
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        dirfd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(dirfd)
        finally:
            os.close(dirfd)

    def _quota(self, identity, unit, quota_gib):
        with self.ledger.connection() as db:
            adoption = db.execute("SELECT phase FROM legacy_adoptions WHERE account_id=?", (identity,)).fetchone()
        if adoption and adoption[0] != "worker":
            raise AdmissionError("legacy ownership transition blocks quota change")
        if adoption:
            account_adoption.proof(self, identity)
        if type(quota_gib) is not int:
            raise ValueError("integer quota required")
        if not 8 <= quota_gib <= 1024:
            raise ValueError("disk quota must be 8..1024 GiB")
        target = quota_gib * 1024**3
        with self.ledger.connection() as db:
            row = db.execute("SELECT * FROM computers WHERE account_id=?", (identity,)).fetchone()
            fence = db.execute("SELECT target_bytes,original_bytes FROM resize_fences WHERE account_id=?", (identity,)).fetchone()
        if row is None:
            reserved = self.ledger.reserve(identity, target)
            return {"account_id": identity, "quota_bytes": target, "applied": True, "provisioned": False, "slot": reserved["slot"]}
        if fence and fence[0] != target:
            raise AdmissionError("another offline resize is unresolved")
        disk = Path(self.c["state_root"]) / identity / "workspace.ext4"
        if disk.parent.is_symlink() or disk.is_symlink():
            raise AdmissionError("unexpected account disk path")
        if not fence and row["state"] == "ready" and not disk.exists():
            raise AdmissionError("ready workspace is missing")
        if not fence and target < row["quota_bytes"]:
            raise AdmissionError("offline shrink required")
        if not fence and target == row["quota_bytes"]:
            # Idempotent requests still verify existing disks; unprovisioned
            # registrations have no filesystem operation to perform.
            disk = Path(self.c["state_root"]) / identity / "workspace.ext4"
            if disk.exists() or disk.is_symlink():
                self._check_filesystem_size(self._disk_path(identity), target)
            elif row["state"] == "ready":
                raise AdmissionError("ready workspace is missing")
            return {"account_id": identity, "slot": row["slot"], "quota_bytes": target,
                    "applied": True, "provisioned": (Path(self.c["state_root"]) / identity / "workspace.ext4").exists()}
        if not fence:
            # Admission and the durable fence commit together. A process crash
            # cannot leave a larger promise without also blocking startup.
            with self.ledger.connection() as db:
                db.execute("BEGIN IMMEDIATE")
                current = db.execute("SELECT * FROM computers WHERE account_id=?", (identity,)).fetchone()
                snapshot = self.ledger._snapshot(db)
                if target < current["quota_bytes"]:
                    raise AdmissionError("offline shrink required")
                if target - current["quota_bytes"] > snapshot["admission_remaining_bytes"]:
                    raise AdmissionError("insufficient reserved disk headroom")
                db.execute("UPDATE computers SET quota_bytes=? WHERE account_id=?", (target, identity))
                db.execute("INSERT INTO resize_fences(account_id,target_bytes,original_bytes) VALUES(?,?,?)",
                           (identity, target, current["quota_bytes"]))
                db.execute("COMMIT")
            original_bytes = current["quota_bytes"]
        else:
            original_bytes = fence["original_bytes"]
        if not disk.exists() and not disk.is_symlink():
            config_path = Path(self.c["config_root"]) / (identity + ".json")
            state = Path(self.c["state_root"]) / identity
            if row["state"] == "ready":
                raise AdmissionError("ready workspace is missing")
            # Validate artifacts before stopping an owner or writing anything.
            if config_path.exists() or config_path.is_symlink():
                info = config_path.lstat()
                with self.ledger.connection() as db:
                    manifest = db.execute("SELECT sha256,mode FROM owned_files WHERE path=?", (str(config_path),)).fetchone()
                if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                        or info.st_mode & 0o777 != 0o600 or not manifest
                        or manifest["mode"] != 0o600
                        or manifest["sha256"] != hashlib.sha256(config_path.read_bytes()).hexdigest()):
                    raise AdmissionError("manager config does not match owned manifest")
                cfg = json.loads(config_path.read_text())
                if (not isinstance(cfg, dict) or type(cfg.get("disk_gib")) is not int
                        or cfg["disk_gib"] * 1024**3 not in (original_bytes, target)):
                    raise AdmissionError("manager config disk quota conflicts with reservation")
            for filename in ("resources-applied.json", "resources-desired.json",
                             "resources-desired.restart-hold.json"):
                path = state / filename
                if path.exists() or path.is_symlink():
                    self._manager_resource(path, (original_bytes, target))
            with self.ledger.connection() as db:
                claimed = db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?", (identity,)).fetchone()
            # A reserved account may have a manager between ensure and disk
            # creation. Stop/prove cleanup before changing its configuration.
            if config_path.exists() or claimed:
                self.stop_manager(identity, unit)
                with self.ledger.connection() as db:
                    db.execute("DELETE FROM runtime_claims WHERE account_id=?", (identity,))
            if disk.exists() or disk.is_symlink():
                # The manager created a disk while cleanup was in progress.
                return self._quota(identity, unit, quota_gib)
            for filename in ("resources-applied.json", "resources-desired.json",
                             "resources-desired.restart-hold.json"):
                path = state / filename
                if path.exists() or path.is_symlink():
                    if path.is_symlink() or not stat.S_ISREG(path.lstat().st_mode):
                        raise AdmissionError("unexpected manager resource override")
                    value = self._manager_resource(path, (original_bytes, target))
                    value["disk_gib"] = target // 1024**3
                    self._replace_manager_resource(path, value)
            if config_path.exists():
                try:
                    cfg = json.loads(config_path.read_text())
                    if cfg.get("disk_gib", 0) * 1024**3 not in (original_bytes, target):
                        raise AdmissionError("manager config disk quota conflicts with reservation")
                    self.replace_owned(config_path, json.dumps(dict(cfg, disk_gib=target // 1024**3), sort_keys=True) + "\n", 0o600)
                except (ValueError, TypeError) as exc:
                    raise AdmissionError("manager config cannot be verified") from exc
            with self.ledger.connection() as db:
                db.execute("DELETE FROM resize_fences WHERE account_id=?", (identity,))
            return {"account_id": identity, "slot": self._slot(identity), "quota_bytes": target,
                    "applied": True, "provisioned": False}
        self._resize_account(identity, target, original_bytes, unit)
        return {"account_id": identity, "slot": self._slot(identity), "quota_bytes": target,
                "applied": True, "provisioned": True}

    def _slot(self, identity):
        with self.ledger.connection() as db:
            row = db.execute("SELECT slot FROM computers WHERE account_id=?", (identity,)).fetchone()
        if row is None:
            raise AdmissionError("unregistered account computer")
        return row[0]

    def record_owned(self, path, content, mode):
        # Future uninstall must recheck hash/ownership before removing files.
        # No delete operation is offered by this broker.
        with self.ledger.connection() as db:
            db.execute("INSERT OR REPLACE INTO owned_files VALUES(?,?,?)",
                       (str(path), hashlib.sha256(content.encode()).hexdigest(), mode))

    # Bytes of one hibernation snapshot beyond the guest RAM image: Firecracker
    # device state (tens of KiB) plus filesystem metadata, rounded up.
    SNAPSHOT_STATE_BYTES = 64 * 1024**2

    def snapshot_reserve(self, identity):
        """Disk promised for one computer's hibernation snapshot."""
        if self.c.get("hibernate", True) is False:
            return 0
        _, memory_mib = self.runtime_resources(identity)
        return memory_mib * 1024**2 + self.SNAPSHOT_STATE_BYTES

    def discard_snapshot(self, identity):
        """Remove a stopped computer's saved guest memory (never its disk)."""
        directory = Path(self.c["state_root"]) / account_id(identity) / "snapshot"
        if directory.is_symlink():
            raise AdmissionError("unexpected snapshot path")
        if not directory.exists():
            return
        (directory / "meta.json").unlink(missing_ok=True)
        for item in directory.iterdir():
            if item.is_symlink() or not item.is_dir():
                item.unlink()
        directory.rmdir()

    def runtime_resources(self, identity):
        overrides = self.c.get("account_resource_overrides", {})
        if not isinstance(overrides, dict):
            raise ValueError("account resource overrides must be a mapping")
        for key, values in overrides.items():
            account_id(key)
            if not isinstance(values, dict) or set(values) != {"vcpus", "memory_mib"}:
                raise ValueError("invalid account resource override")
            if any(type(values[k]) is not int or not lo <= values[k] <= hi
                   for k, lo, hi in (("vcpus", 1, 32), ("memory_mib", 512, 32768))):
                raise ValueError("invalid account resource override bounds")
        values = overrides.get(identity, self.c)
        return values["vcpus"], values["memory_mib"]

    def runtime_admission(self, identity):
        vcpus, memory_mib = self.runtime_resources(identity)
        # Claims survive restart and release only after a successful service stop.
        # Conservative registration budgets do not assume CPU/RAM overcommit.
        with self.ledger.connection() as db:
            db.execute("BEGIN IMMEDIATE")
            if db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?", (identity,)).fetchone():
                return
            cpu, memory = db.execute("SELECT COALESCE(SUM(vcpus),0),COALESCE(SUM(memory_mib),0) FROM runtime_claims").fetchone()
            if cpu + vcpus > self.c["runtime_vcpu_budget"] or memory + memory_mib + 512 > runtime_memory_claim_limit(self.c):
                raise AdmissionError("concurrent computer resource budget exhausted")
            # Actual available memory also gates a new start, even within budget.
            # With the virtio balloon's free page reporting, idle computers
            # return freed guest RAM to the host, so this real measurement (not
            # a static estimate) is what lets more computers start.
            available = None
            for line in Path("/proc/meminfo").read_text().splitlines():
                if line.startswith("MemAvailable:"):
                    available = int(line.split()[1]) // 1024
            if available is None or available < memory_mib + 512 + self.c["host_memory_headroom_mib"]:
                raise AdmissionError("host memory unavailable")
            db.execute("INSERT INTO runtime_claims VALUES(?,?,?)", (identity, vcpus, memory_mib + 512))
            db.execute("COMMIT")

    def dispatch(self, request):
        if not isinstance(request, dict) or set(request) - {"op", "account_id", "quota_gib"}:
            raise ValueError("unknown request field")
        op = request.get("op")
        if op == "capacity" and set(request) == {"op"}:
            with self.ledger.connection() as db:
                db.execute("BEGIN")
                snapshot = self.ledger._snapshot(db)
                pending = {row[0] for row in db.execute("SELECT account_id FROM resize_fences")}
                for computer in snapshot["accounts"]:
                    computer["pending_quota"] = computer["account_id"] in pending
                return snapshot
        identity, name, unit = self.identity(request.get("account_id"))
        if op in ("reserve", "quota") and set(request) == {"op", "account_id", "quota_gib"}:
            if op == "quota":
                return self._quota(identity, unit, request["quota_gib"])
            quota = request["quota_gib"]
            if type(quota) is not int:
                raise ValueError("integer quota required")
            with self.ledger.connection() as db:
                existing = db.execute("SELECT quota_bytes FROM computers WHERE account_id=?", (identity,)).fetchone()
            if existing and existing[0] != quota * 1024**3:
                raise AdmissionError("existing disk resize is not implemented")
            return self.ledger.reserve(identity, quota * 1024**3)
        if set(request) != {"op", "account_id"}:
            raise ValueError("unexpected operation arguments")
        if op == "adoption_status":
            return account_adoption.proof(self, identity)
        if op == "abort":
            self._assert_no_resize_fence(identity)
            self.ledger.abort_unprovisioned(identity)
            return {"aborted": True}
        if op not in ("ensure", "stop", "disable", "restore"):
            raise ValueError("operation not allowed")
        with self.ledger.connection() as db:
            row = db.execute("SELECT * FROM computers WHERE account_id=?", (identity,)).fetchone()
        if row is None:
            raise AdmissionError("unregistered account computer")
        if op in ("ensure", "restore"):
            self._assert_no_resize_fence(identity)
        if op == "restore":
            with self.ledger.connection() as db:
                db.execute("BEGIN IMMEDIATE")
                db.execute("UPDATE computers SET state='reserved' WHERE account_id=?", (identity,))
                self.ledger._snapshot(db)
                db.execute("COMMIT")
            return {"restored": True}
        if op in ("stop", "disable"):
            if op == "disable":
                # Persist the fence before stop; a failed stop/restart cannot
                # allow an ensure to resurrect a disabled computer.
                with self.ledger.connection() as db:
                    db.execute("UPDATE computers SET state='disabled' WHERE account_id=?", (identity,))
            self.stop_manager(identity, unit)
            if op == "disable":
                # A manager exit hibernates; a disabled computer keeps only
                # its workspace disk, not 1 GiB+ of guest memory.
                self.discard_snapshot(identity)
            with self.ledger.connection() as db:
                db.execute("DELETE FROM runtime_claims WHERE account_id=?", (identity,))
            return {"stopped": True}
        if row["state"] == "disabled":
            raise AdmissionError("computer disabled")
        self.ledger.snapshot()  # Fail closed before preparing new host artifacts.
        self.runtime_admission(identity)
        state = Path(self.c["state_root"]) / identity
        sockets = Path(self.c["socket_root"]) / identity
        if state.is_symlink() or sockets.is_symlink():
            raise AdmissionError("unexpected account path symlink")
        vcpus, memory_mib = self.runtime_resources(identity)
        cfg = dict(id=name, slot=row["slot"], state_dir=str(state),
                   socket_dir=str(sockets), socket_gid=self.c["socket_gid"],
                   image_dir=str(self.release), bin_dir=str(self.release / "bin"),
                   vcpus=vcpus, memory_mib=memory_mib,
                   disk_gib=row["quota_bytes"] // 1024**3)
        if self.c.get("hibernate", True) is False:
            # Only an explicit opt-out is written: generated configs of
            # existing computers stay byte-identical (the manager defaults on).
            cfg["hibernate"] = False
        cfg = self.manager_config(cfg)
        config_path = Path(self.c["config_root"]) / (identity + ".json")
        self.write_owned(config_path, json.dumps(cfg, sort_keys=True) + "\n", 0o600)
        self.start_manager(identity, unit, config_path)
        return {"account_id": identity, "socket": str(sockets / "control.sock"), "slot": row["slot"]}

    def _assert_no_resize_fence(self, identity):
        with self.ledger.connection() as db:
            adoption = db.execute("SELECT phase FROM legacy_adoptions WHERE account_id=?", (identity,)).fetchone()
            if adoption and adoption[0] != "worker":
                raise AdmissionError("legacy ownership transition blocks Worker startup")
            if db.execute("SELECT 1 FROM resize_fences WHERE account_id=?", (identity,)).fetchone():
                raise AdmissionError("offline workspace resize unresolved")
        if adoption:
            account_adoption.proof(self, identity)

    def stop_manager(self, identity, unit):
        self.run(["/usr/bin/systemctl", "stop", unit])

    def manager_config(self, config):
        return config

    def start_manager(self, identity, unit, config_path):
        # Host-service implementation retained for compatibility, not activated.
        if any(ch.isspace() for ch in str(config_path) + str(self.release)):
            raise ValueError("unit paths cannot contain whitespace")
        content = ("[Unit]\nDescription=Tofi account computer\nAfter=network-online.target\n"
                   "[Service]\nType=simple\nUMask=0077\nKillMode=control-group\nTimeoutStopSec=60\nRestart=no\n"
                   f"ExecStart=/usr/bin/python3 {self.release}/manager.py {config_path}\n")
        self.write_owned(Path(self.c["unit_root"]) / unit, content, 0o644)
        self.run(["/usr/bin/systemctl", "daemon-reload"])
        self.run(["/usr/bin/systemctl", "start", unit])


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        response, status = {"error": "request rejected"}, 400
        try:
            _, uid, _ = struct.unpack("3i", self.connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid != self.server.broker.c["app_uid"]:
                status = 403
            elif self.path != "/v1/accounts":
                status = 404
            else:
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 < length <= 4096 or self.headers.get("Transfer-Encoding"):
                    raise ValueError("invalid body")
                self.connection.settimeout(10)
                body = self.rfile.read(length)
                if len(body) != length:
                    raise ValueError("incomplete body")
                # Serialize all provision/stop transitions across handler threads.
                with open(Path(self.server.broker.c["ledger_root"]) / "broker.lock", "a") as lock:
                    fcntl.flock(lock, fcntl.LOCK_EX)
                    response = self.server.broker.dispatch(json.loads(body))
                status = 200
        except AdmissionError as error:
            response, status = {"error": str(error)}, 409
        except (ValueError, TypeError, OSError, subprocess.SubprocessError, sqlite3.Error):
            pass
        data = json.dumps(response).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_):
        pass


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


def remove_stale_socket(path):
    """Called only while holding the exclusive service lifetime lock."""
    if not path.exists() and not path.is_symlink():
        return
    info = path.lstat()
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.geteuid():
        raise AdmissionError("unexpected broker socket artifact")
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as probe:
        probe.settimeout(1)
        try:
            probe.connect(str(path))
        except ConnectionRefusedError:
            path.unlink()
        else:
            raise AdmissionError("broker socket is already active")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("root host service required")
    os.umask(0o077)
    config = json.loads(Path(args.config).read_text())
    broker = Broker(config)
    # The App must traverse this shared socket parent; account managers set the
    # individual directories/socket permissions. No account data is mounted.
    os.chown(config["socket_root"], 0, config["socket_gid"])
    os.chmod(config["socket_root"], 0o750)
    path = Path(config["broker_socket"])
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o750)
    os.chown(path.parent, 0, config["socket_gid"])
    os.chmod(path.parent, 0o750)
    with open(Path(config["ledger_root"]) / "service.lock", "a") as lifetime:
        fcntl.flock(lifetime, fcntl.LOCK_EX | fcntl.LOCK_NB)
        remove_stale_socket(path)
        with Server(str(path), Handler) as server:
            server.broker = broker
            os.chown(path, 0, config["socket_gid"])
            os.chmod(path, 0o660)
            try:
                server.serve_forever()
            finally:
                path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
