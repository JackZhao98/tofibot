import concurrent.futures
import importlib.util
import os
from pathlib import Path
import struct
import tempfile
import unittest
import uuid

spec = importlib.util.spec_from_file_location("account_capacity", Path(__file__).with_name("account_capacity.py"))
capacity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capacity)
GiB = 1024**3


class CapacityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.host = {"total_bytes": 100 * GiB, "available_bytes": 30 * GiB}
        self.ledger = self.open()

    def tearDown(self):
        self.temp.cleanup()

    def open(self):
        return capacity.CapacityLedger(self.root / "ledger.sqlite", self.root,
                                       10 * GiB, 15 * GiB, lambda: self.host,
                                       reserved_slots=(1, 21, 22))

    def test_concurrent_admission_and_restart_preserve_promises(self):
        identities = [str(uuid.uuid4()) for _ in range(4)]
        def reserve(identity):
            try:
                return self.open().reserve(identity, 8 * GiB)
            except capacity.AdmissionError:
                return None
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            accepted = [r for r in pool.map(reserve, identities) if r]
        self.assertEqual(len(accepted), 2)
        self.assertEqual(len({r["slot"] for r in accepted}), 2)
        self.assertTrue(all(r["slot"] not in (1, 21, 22) for r in accepted))
        snapshot = self.open().snapshot()
        self.assertEqual(snapshot["promised_bytes"], 16 * GiB)
        self.assertEqual(snapshot["admission_remaining_bytes"], 4 * GiB)
        self.assertTrue(snapshot["warning"])

    def test_external_and_internal_reservations_survive_growth_and_reopen(self):
        self.host["available_bytes"] = 32 * GiB
        ledger = capacity.CapacityLedger(self.root / "reserved.sqlite", self.root,
            2 * GiB, 5 * GiB, lambda: self.host, external_reserved_bytes=6 * GiB,
            per_account_internal_reserved_bytes=3 * GiB)
        first, second = str(uuid.uuid4()), str(uuid.uuid4())
        # Two concurrent new accounts cannot spend the same fixed external reserve
        # or the per-account internal allowance.
        def reserve(identity):
            try:
                return ledger.reserve(identity, 19 * GiB)
            except capacity.AdmissionError:
                return None
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(reserve, (first, second)))
        self.assertEqual(sum(result is not None for result in results), 1)
        first = first if results[0] else second
        snap = ledger.snapshot()
        self.assertEqual(snap["external_reserved_bytes"], 6 * GiB)
        self.assertEqual(snap["internal_reserved_bytes"], 3 * GiB)
        self.assertEqual(snap["safety_reserved_bytes"], 9 * GiB)
        self.assertEqual(snap["admission_remaining_bytes"], 2 * GiB)
        # Existing-account growth uses the already counted per-account reserve.
        ledger.reserve(first, 20 * GiB)
        with self.assertRaises(capacity.AdmissionError):
            ledger.reserve(first, 22 * GiB)
        reopened = capacity.CapacityLedger(self.root / "reserved.sqlite", self.root,
            2 * GiB, 5 * GiB, lambda: self.host, external_reserved_bytes=6 * GiB,
            per_account_internal_reserved_bytes=3 * GiB)
        self.assertEqual(reopened.snapshot()["promised_bytes"], 20 * GiB)
        self.assertEqual(reopened.snapshot()["internal_reserved_bytes"], 3 * GiB)

    def test_reservation_config_rejects_negative_non_integer_and_bool(self):
        for field in ("external", "internal"):
            for invalid in (-1, 1.5, True, "4"):
                kwargs = {"external_reserved_bytes": 0,
                          "per_account_internal_reserved_bytes": 0}
                kwargs["external_reserved_bytes" if field == "external" else
                       "per_account_internal_reserved_bytes"] = invalid
                with self.assertRaises(ValueError):
                    capacity.CapacityLedger(self.root / "invalid.sqlite", self.root,
                        1, 2, lambda: self.host, **kwargs)

    def test_sparse_disk_reports_actual_and_logical_separately(self):
        identity = str(uuid.uuid4())
        self.ledger.reserve(identity, 8 * GiB)
        folder = self.root / identity
        folder.mkdir()
        disk = folder / "workspace.ext4"
        with disk.open("wb") as stream:
            stream.truncate(8 * GiB)
            stream.write(b"synthetic account data")
        self.ledger.transition(identity, "ready")
        snapshot = self.ledger.snapshot()
        self.assertEqual(snapshot["accounts"][0]["logical_bytes"], 8 * GiB)
        self.assertLess(snapshot["allocated_bytes"], GiB)
        self.assertEqual(snapshot["unallocated_promises_bytes"], 8 * GiB - snapshot["allocated_bytes"])
        self.ledger.transition(identity, "disabled")
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.abort_unprovisioned(identity)
        self.assertEqual(self.open().snapshot()["promised_bytes"], 8 * GiB)

    def test_fail_closed_and_no_fictional_shrink(self):
        identity = str(uuid.uuid4())
        self.ledger.reserve(identity, 16 * GiB)
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.reserve(identity, 8 * GiB)
        self.host = None
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.reserve(str(uuid.uuid4()), 8 * GiB)
        self.host = {"total_bytes": 100 * GiB, "available_bytes": 9 * GiB}
        self.assertLess(self.ledger.snapshot()["admission_remaining_bytes"], 0)
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.reserve(identity, 17 * GiB)
        self.ledger.abort_unprovisioned(identity)
        self.assertEqual(self.ledger.snapshot()["promised_bytes"], 0)

    def test_paths_and_missing_ready_data_fail_closed(self):
        for identity in ("../personal", "personal", str(uuid.uuid4()).upper()):
            with self.assertRaises(ValueError):
                self.ledger.reserve(identity, 8 * GiB)
        identity = str(uuid.uuid4())
        self.ledger.reserve(identity, 8 * GiB)
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.transition(identity, "ready")
        (self.root / identity).symlink_to(self.root)
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.snapshot()

    def test_non_regular_disk_never_supplies_capacity_evidence(self):
        identity = str(uuid.uuid4())
        self.ledger.reserve(identity, 8 * GiB)
        folder = self.root / identity
        folder.mkdir()
        disk = folder / "workspace.ext4"
        disk.mkdir()
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.transition(identity, "ready")
        disk.rmdir()
        os.mkfifo(disk)
        with self.assertRaises(capacity.AdmissionError):
            self.ledger.reserve(str(uuid.uuid4()), 8 * GiB)
        with self.ledger.connection() as db:
            state = db.execute("SELECT state FROM computers WHERE account_id=?", (identity,)).fetchone()[0]
        self.assertEqual(state, "reserved")

    def local_ext4_disk(self, identity):
        folder = self.root / identity
        folder.mkdir(parents=True, exist_ok=True)
        disk = folder / "workspace.ext4"
        with disk.open("wb") as stream:
            stream.truncate(8 * GiB)
            superblock = bytearray(1024)
            struct.pack_into("<I", superblock, 4, 8 * GiB // 1024)
            struct.pack_into("<I", superblock, 24, 0)
            superblock[56:58] = b"\x53\xef"
            stream.seek(1024)
            stream.write(superblock)
        with disk.open("r+b") as stream:
            stream.seek(64 * 1024**2)
            stream.write(b"x" * (4 * 1024**2))
        return disk

    def test_managed_jail_hardlink_is_known_but_additional_links_get_no_credit(self):
        identity = str(uuid.uuid4())
        self.ledger.reserve(identity, 8 * GiB)
        disk = self.local_ext4_disk(identity)
        alias = (disk.parent / "jails" / "firecracker" / ("ac-" + identity) /
                 "root" / "workspace.ext4")
        alias.parent.mkdir(parents=True)
        os.link(disk, alias)
        host = {"total_bytes": 100 * GiB, "available_bytes": 30 * GiB,
                "filesystem_device": self.root.stat().st_dev, "filesystem_type": "ext4"}
        ledger = capacity.CapacityLedger(self.root / "managed.sqlite", self.root,
            10 * GiB, 15 * GiB, lambda: host)
        ledger.reserve(identity, 8 * GiB)
        credited = ledger.snapshot()
        self.assertGreater(credited["allocated_bytes"], 0)
        unknown = self.root / identity / "untracked-workspace-link"
        os.link(disk, unknown)
        uncredited = ledger.snapshot()
        self.assertEqual(uncredited["allocated_bytes"], 0)
        self.assertEqual(uncredited["accounts"][0]["allocated_bytes"], 0)

    def test_capacity_scan_uses_lower_available_space_reading(self):
        identity = str(uuid.uuid4())
        host = {"total_bytes": 100 * GiB, "available_bytes": 40 * GiB,
                "filesystem_device": self.root.stat().st_dev,
                "filesystem_type": "ext4"}
        metrics = lambda: host
        ledger = capacity.CapacityLedger(self.root / "bracketed.sqlite", self.root,
            10 * GiB, 15 * GiB, metrics)
        ledger.reserve(identity, 8 * GiB)
        self.local_ext4_disk(identity)
        samples = iter((40 * GiB, 32 * GiB))
        ledger.metrics = lambda: dict(host, available_bytes=next(samples))
        snapshot = ledger.snapshot()
        self.assertEqual(snapshot["available_bytes"], 32 * GiB)
        self.assertGreater(snapshot["accounts"][0]["allocated_bytes"], 0)
        self.assertLess(snapshot["admission_remaining_bytes"], 15 * GiB)
        ledger.metrics = lambda: dict(host, available_bytes=32 * GiB)
        with self.assertRaises(capacity.AdmissionError):
            ledger.reserve(str(uuid.uuid4()), 15 * GiB)

    def make_external_disk(self, folder, quota=8 * GiB):
        folder.mkdir(parents=True, exist_ok=True)
        disk = folder / "workspace.ext4"
        with disk.open("wb") as stream:
            stream.truncate(quota)
            superblock = bytearray(1024)
            struct.pack_into("<I", superblock, 4, quota // 1024)
            struct.pack_into("<I", superblock, 24, 0)
            superblock[56:58] = b"\x53\xef"
            stream.seek(1024)
            stream.write(superblock)
        alias = folder / "jail-workspace.ext4"
        os.link(disk, alias)
        return disk, alias

    def external_ledger(self, external_disks, host=None):
        host = host or self.host
        metrics = lambda: dict(host, filesystem_device=self.root.stat().st_dev,
                               filesystem_type="ext4")
        return capacity.CapacityLedger(self.root / "external.sqlite", self.root,
            10 * GiB, 15 * GiB, metrics, external_disks=external_disks)

    def test_external_promises_are_reconciled_dynamically_and_survive_reopen(self):
        disk, alias = self.make_external_disk(self.root / "external")
        config = [dict(asset_id="legacy-a", disk_path=str(disk), quota_bytes=8 * GiB,
                       alias_paths=[str(alias)])]
        ledger = self.external_ledger(config)
        first = ledger.snapshot()
        self.assertEqual(first["external_promised_bytes"], 8 * GiB)
        self.assertEqual(first["external_unallocated_promises_bytes"],
                         8 * GiB - first["external_allocated_bytes"])
        self.assertGreater(first["external_allocated_bytes"], 0)
        with disk.open("r+b") as stream:
            stream.seek(64 * 1024**2)
            stream.write(b"x" * (4 * 1024**2))
        grown = ledger.snapshot()
        self.assertGreater(grown["external_allocated_bytes"], first["external_allocated_bytes"])
        self.assertGreater(grown["admission_remaining_bytes"], first["admission_remaining_bytes"])
        reopened = self.external_ledger(config)
        self.assertEqual(reopened.snapshot()["external_allocated_bytes"], grown["external_allocated_bytes"])

    def test_external_unknown_links_get_zero_credit_and_bad_geometry_fails_closed(self):
        disk, alias = self.make_external_disk(self.root / "external")
        unknown = self.root / "external" / "unknown-link"
        os.link(disk, unknown)
        config = [dict(asset_id="legacy-a", disk_path=str(disk), quota_bytes=8 * GiB,
                       alias_paths=[str(alias)])]
        snapshot = self.external_ledger(config).snapshot()
        self.assertEqual(snapshot["external_allocated_bytes"], 0)
        self.assertEqual(snapshot["external_unallocated_promises_bytes"], 8 * GiB)
        disk.unlink()
        disk.write_bytes(b"changed")
        with self.assertRaises(capacity.AdmissionError):
            self.external_ledger(config).snapshot()

    def test_external_device_and_duplicate_inode_fail_closed(self):
        disk, alias = self.make_external_disk(self.root / "external")
        one = dict(asset_id="legacy-a", disk_path=str(disk), quota_bytes=8 * GiB,
                   alias_paths=[str(alias)])
        wrong_device = dict(total_bytes=100 * GiB, available_bytes=30 * GiB,
                            filesystem_device=self.root.stat().st_dev + 1,
                            filesystem_type="ext4")
        ledger = capacity.CapacityLedger(self.root / "wrong-device.sqlite", self.root,
            10 * GiB, 15 * GiB, lambda: wrong_device, external_disks=[one])
        with self.assertRaises(capacity.AdmissionError):
            ledger.snapshot()
        two = dict(asset_id="legacy-b", disk_path=str(alias), quota_bytes=8 * GiB,
                   alias_paths=[str(disk)])
        duplicate = self.external_ledger([one, two])
        with self.assertRaises(capacity.AdmissionError):
            duplicate.snapshot()

    def test_external_dynamic_promises_are_reserved_atomically_across_reopen(self):
        disk, alias = self.make_external_disk(self.root / "external")
        config = [dict(asset_id="legacy-a", disk_path=str(disk), quota_bytes=8 * GiB,
                       alias_paths=[str(alias)])]
        ledger = self.external_ledger(config)
        identities = [str(uuid.uuid4()) for _ in range(2)]
        def reserve(identity):
            try:
                return self.external_ledger(config).reserve(identity, 8 * GiB)
            except capacity.AdmissionError:
                return None
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(reserve, identities))
        self.assertEqual(sum(result is not None for result in results), 1)
        self.assertEqual(ledger.snapshot()["external_promised_bytes"], 8 * GiB)
        self.assertEqual(ledger.snapshot()["promised_bytes"], 8 * GiB)

    def test_hibernation_snapshots_are_promised_and_credited_only_when_trusted(self):
        self.host["available_bytes"] = 30 * GiB
        ledger = capacity.CapacityLedger(self.root / "snap.sqlite", self.root, 2 * GiB, 5 * GiB,
                                         lambda: self.host, snapshot_reserve=lambda _: 2 * GiB)
        first, second = str(uuid.uuid4()), str(uuid.uuid4())
        ledger.reserve(first, 8 * GiB)
        base = ledger.snapshot()
        self.assertEqual(base["snapshot_reserved_bytes"], 2 * GiB)
        self.assertEqual(base["snapshot_unallocated_reserved_bytes"], 2 * GiB)
        self.assertEqual(base["admission_remaining_bytes"], 30 * GiB - 8 * GiB - 2 * GiB - 2 * GiB)
        # A new account must fit its disk AND its snapshot promise.
        self.host["available_bytes"] = 21 * GiB
        with self.assertRaises(capacity.AdmissionError):
            ledger.reserve(second, 8 * GiB)
        self.host["available_bytes"] = 30 * GiB
        snapshot = self.root / first / "snapshot"
        snapshot.mkdir(parents=True)
        memory = snapshot / "memory"
        memory.write_bytes(b"\1" * (1024 * 1024))
        written = memory.stat().st_blocks * 512
        if os.getuid() == 0:
            measured = ledger.snapshot()
            self.assertEqual(measured["snapshot_allocated_bytes"], written)
            self.assertEqual(measured["snapshot_unallocated_reserved_bytes"], 2 * GiB - written)
        else:
            # Not root-owned: real data, but never credited against the promise.
            self.assertEqual(ledger.snapshot()["snapshot_allocated_bytes"], 0)
        os.link(memory, snapshot / "extra-link")
        self.assertEqual(ledger.snapshot()["snapshot_allocated_bytes"], 0)  # multi-link: no credit
        self.assertEqual(ledger.snapshot()["accounts"][0]["account_id"], first)

    def test_no_snapshot_promise_without_hibernation(self):
        ledger = capacity.CapacityLedger(self.root / "plain.sqlite", self.root, 2 * GiB, 5 * GiB,
                                         lambda: self.host)
        ledger.reserve(str(uuid.uuid4()), 8 * GiB)
        self.assertEqual(ledger.snapshot()["snapshot_reserved_bytes"], 0)
        # An invalid promise closes admission instead of counting as zero.
        with self.assertRaises(capacity.AdmissionError):
            capacity.CapacityLedger(self.root / "plain.sqlite", self.root, 2 * GiB, 5 * GiB,
                                    lambda: self.host, snapshot_reserve=lambda _: -1).snapshot()


if __name__ == "__main__":
    unittest.main()

class ImmutableReserveTests(unittest.TestCase):
    def test_known_copy_growth_removal_and_untrusted_files(self):
        from unittest.mock import patch
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            host = {"total_bytes": 100*GiB, "available_bytes": 40*GiB,
                    "filesystem_type": "ext4", "filesystem_device": root.stat().st_dev}
            ledger = capacity.CapacityLedger(root/"ledger.sqlite", root, GiB, 2*GiB,
                metrics=lambda: dict(host), per_account_internal_reserved_bytes=8*GiB,
                immutable_image_sizes={"rootfs.ext4": 1024*1024})
            identity = str(uuid.uuid4())
            ledger.reserve(identity, 8*GiB)
            baseline = ledger.snapshot()["admission_remaining_bytes"]
            jail = root/identity/"jails"/"firecracker"/("ac-"+identity)/"root"
            jail.mkdir(parents=True)
            image = jail/"rootfs.ext4"
            image.touch()
            with image.open("r+b") as stream:
                stream.truncate(1024*1024)
            image.chmod(0o444)
            # The local test runner need not be root. Model only UID ownership;
            # actual inode, size, blocks, mode, links and path checks remain live.
            real_fstat, real_path = capacity.os.fstat, capacity._trusted_regular_path
            def owned(info):
                return SimpleNamespace(**{name: (0 if name == "st_uid" else getattr(info, name))
                    for name in ("st_dev", "st_ino", "st_uid", "st_mode", "st_size", "st_nlink", "st_blocks")})
            with patch.object(capacity.os, "fstat", side_effect=lambda fd: owned(real_fstat(fd))), \
                    patch.object(capacity, "_trusted_regular_path", side_effect=lambda path: owned(real_path(path))):
                for content in (b"x"*4096, b"x"*(128*1024)):
                    image.chmod(0o644)
                    with image.open("r+b") as stream:
                        stream.write(content)
                    image.chmod(0o444)
                    blocks = image.stat().st_blocks*512
                    host["available_bytes"] = 40*GiB-blocks
                    snap = ledger.snapshot()
                    self.assertEqual(snap["internal_allocated_bytes"], blocks)
                    self.assertEqual(snap["internal_reserved_bytes"], 8*GiB)
                    self.assertEqual(snap["internal_unallocated_reserved_bytes"], 8*GiB-blocks)
                    self.assertEqual(snap["admission_remaining_bytes"], baseline)
                alias = root/"unknown-hardlink"
                os.link(image, alias)
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
                alias.unlink()
                host["filesystem_type"] = "unknown"
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
                host["filesystem_type"] = "ext4"
                image.chmod(0o644)
                with image.open("r+b") as stream:
                    stream.truncate(1024*1024-1)
                image.chmod(0o444)
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
                image.chmod(0o644)
                with image.open("r+b") as stream:
                    stream.truncate(1024*1024)
                image.chmod(0o644)
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
                image.chmod(0o444)
                image.unlink()
                host["available_bytes"] = 40*GiB
                self.assertEqual(ledger.snapshot()["admission_remaining_bytes"], baseline)
                image.symlink_to(root/"other")
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
                image.unlink()
                jail.rename(jail.with_name("elsewhere"))
                jail.symlink_to(jail.with_name("elsewhere"), target_is_directory=True)
                self.assertEqual(ledger.snapshot()["internal_allocated_bytes"], 0)
