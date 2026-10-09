import importlib.util
import json
import os
import stat
from pathlib import Path
import sys
import socket
import tempfile
import unittest
from unittest.mock import Mock, patch
import uuid

sys.path.insert(0, str(Path(__file__).parent))
spec = importlib.util.spec_from_file_location("account_provisioner", Path(__file__).with_name("account_provisioner.py"))
broker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(broker)


class BrokerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir="/tmp")
        root = Path(self.temp.name)
        release = root / "release"
        release.mkdir()
        (release / "manager.py").write_text("# synthetic trusted release")
        self.config = {k: str(root / k) for k in ("state_root", "config_root", "unit_root", "socket_root", "ledger_root")}
        self.config.update(release_dir=str(release), headroom_bytes=10*1024**3,
                           warning_bytes=15*1024**3, reserved_slots=[1,21,22],
                           external_reserved_bytes=4*1024**3,
                           per_account_internal_reserved_bytes=2*1024**3,
                           vcpus=2, memory_mib=2048, runtime_vcpu_budget=4,
                           runtime_memory_mib_budget=5120, host_memory_headroom_mib=2048,
                           app_uid=10001, socket_gid=10001)
        self.run = Mock()
        self.b = broker.Broker(self.config, self.run, lambda: {"total_bytes":100*1024**3,"available_bytes":80*1024**3})

    def test_disk_accepts_broker_and_only_its_ledger_slot_owner(self):
        identities = [str(uuid.uuid4()), str(uuid.uuid4())]
        for identity in identities:
            self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        identity = identities[0]
        directory = Path(self.config["state_root"]) / identity
        directory.mkdir()
        disk = directory / "workspace.ext4"
        disk.write_bytes(b"disk fixture")
        self.assertEqual(self.b._disk_path(identity), disk)
        own_uid = 61000 + self.b._slot(identity)
        foreign_uid = 61000 + self.b._slot(identities[1])
        actual_lstat = Path.lstat

        def ownership(uid, gid=None, mode=0o600):
            def lstat(path):
                original = actual_lstat(path)
                if path != disk:
                    return original
                values = list(original)
                values[0] = stat.S_IFREG | mode
                values[4] = uid
                values[5] = uid if gid is None else gid
                return os.stat_result(values)
            return patch.object(Path, "lstat", lstat)

        with ownership(own_uid):
            self.assertEqual(self.b._disk_path(identity), disk)
        for uid, gid, mode in ((foreign_uid, foreign_uid, 0o600),
                               (12345, 12345, 0o600),
                               (own_uid, foreign_uid, 0o600),
                               (own_uid, own_uid, 0o644)):
            with self.subTest(uid=uid, gid=gid, mode=mode), ownership(uid, gid, mode):
                with self.assertRaises(broker.AdmissionError):
                    self.b._disk_path(identity)
        disk.unlink()
        disk.symlink_to(Path(self.config["state_root"]) / identities[1] / "workspace.ext4")
        with self.assertRaises(broker.AdmissionError):
            self.b._disk_path(identity)

    def test_disk_rejects_unregistered_account_and_non_regular_file(self):
        identity = str(uuid.uuid4())
        directory = Path(self.config["state_root"]) / identity
        directory.mkdir()
        disk = directory / "workspace.ext4"
        disk.write_bytes(b"unregistered")
        with self.assertRaises(broker.AdmissionError):
            self.b._disk_path(identity)
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        disk.unlink()
        disk.mkdir()
        with self.assertRaises(broker.AdmissionError):
            self.b._disk_path(identity)

    def test_optional_reservation_config_is_wired_to_ledger(self):
        snapshot = self.b.ledger.snapshot()
        self.assertEqual(snapshot["external_reserved_bytes"], 4*1024**3)
        self.assertEqual(snapshot["per_account_internal_reserved_bytes"], 2*1024**3)
        self.assertEqual(snapshot["admission_remaining_bytes"], 66*1024**3)
        legacy_config = dict(self.config)
        legacy_config.pop("external_reserved_bytes")
        legacy_config.pop("per_account_internal_reserved_bytes")
        legacy = broker.Broker(legacy_config, self.run,
            lambda: {"total_bytes":100*1024**3,"available_bytes":80*1024**3})
        self.assertEqual(legacy.ledger.snapshot()["safety_reserved_bytes"], 0)

    def tearDown(self):
        self.temp.cleanup()

    def test_two_accounts_have_fixed_separate_artifacts_and_no_shell(self):
        ids = [str(uuid.uuid4()) for _ in range(2)]
        sockets = []
        with patch.object(self.b, "runtime_admission"):
            for identity in ids:
                self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
                result = self.b.dispatch(dict(op="ensure",account_id=identity))
                sockets.append(result["socket"])
                path = Path(self.config["config_root"]) / (identity + ".json")
                cfg = json.loads(path.read_text())
                self.assertEqual(cfg["state_dir"], str(Path(self.config["state_root"]) / identity))
                self.assertEqual(cfg["socket_dir"], str(Path(self.config["socket_root"]) / identity))
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertNotEqual(*sockets)
        for call in self.run.call_args_list:
            args = call.args[0]
            self.assertEqual(args[0], "/usr/bin/systemctl")
            self.assertIn(args[1], ("daemon-reload", "start"))
            if args[1] == "start":
                self.assertIn(args[2], ["tofi-computer-ac-" + i + ".service" for i in ids])
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure",account_id=ids[0]))  # crash retry
        with self.b.ledger.connection() as db:
            manifest = db.execute("SELECT path,sha256,mode FROM owned_files").fetchall()
        self.assertEqual(len(manifest),4)
        self.assertTrue(all(len(row["sha256"])==64 for row in manifest))
        self.assertTrue(all(row["mode"] in (0o600,0o644) for row in manifest))

    def test_unregistered_identity_path_injection_and_resize_are_rejected(self):
        identity = str(uuid.uuid4())
        for request in (dict(op="ensure",account_id=identity),
                        dict(op="ensure",account_id="../personal"),
                        dict(op="reserve",account_id=identity,quota_gib=8,state_dir="/victim"),
                        dict(op="purge",account_id=identity)):
            with self.assertRaises((ValueError, broker.AdmissionError)):
                self.b.dispatch(request)
        self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=16))
        self.run.assert_not_called()

    def test_resource_claims_survive_restart_and_release_only_after_stop(self):
        ids = [str(uuid.uuid4()) for _ in range(3)]
        original = Path.read_text
        def read(path, *args, **kwargs):
            if str(path) == "/proc/meminfo":
                return "MemAvailable: 16777216 kB\n"
            return original(path, *args, **kwargs)
        with patch.object(Path, "read_text", read):
            for identity in ids:
                self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
            self.b.runtime_admission(ids[0])
            self.b.runtime_admission(ids[1])
            other = broker.Broker(self.config,self.run, self.b.ledger.metrics)
            with self.assertRaises(broker.AdmissionError):
                other.runtime_admission(ids[2])
            self.run.side_effect = OSError("synthetic stop failure")
            with self.assertRaises(OSError):
                self.b.dispatch(dict(op="stop",account_id=ids[0]))
            with self.assertRaises(broker.AdmissionError):
                other.runtime_admission(ids[2])
            self.run.side_effect = None
            self.b.dispatch(dict(op="stop",account_id=ids[0]))
            other.runtime_admission(ids[2])

    def test_memory_overcommit_raises_only_static_ceiling_and_keeps_live_gate(self):
        ids = [str(uuid.uuid4()) for _ in range(3)]
        available = {"kib": 16777216}
        original = Path.read_text
        def read(path, *args, **kwargs):
            if str(path) == "/proc/meminfo":
                return "MemAvailable: %d kB\n" % available["kib"]
            return original(path, *args, **kwargs)
        for value in (99, 201, True, "150"):
            with self.assertRaises(ValueError):
                broker.runtime_memory_claim_limit(dict(self.config, runtime_memory_overcommit_percent=value))
        self.assertEqual(broker.runtime_memory_claim_limit(self.config), 5120)
        self.config["runtime_memory_overcommit_percent"] = 150
        b = broker.Broker(self.config, self.run, self.b.ledger.metrics)
        with patch.object(Path, "read_text", read):
            for identity in ids:
                b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
            b.runtime_admission(ids[0])
            b.runtime_admission(ids[1])
            # 3 x 2560 MiB claims fit 7680 MiB, but real free memory still decides.
            self.config["runtime_vcpu_budget"] = 6
            b = broker.Broker(self.config, self.run, self.b.ledger.metrics)
            available["kib"] = (2048 + 512 + 2048 - 1) * 1024
            with self.assertRaises(broker.AdmissionError):
                b.runtime_admission(ids[2])
            available["kib"] = 16777216
            b.runtime_admission(ids[2])

    def test_disable_failure_persists_fence_and_restore_retains_quota(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
        self.run.side_effect = OSError("synthetic stop failure")
        with self.assertRaises(OSError):
            self.b.dispatch(dict(op="disable",account_id=identity))
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch(dict(op="ensure",account_id=identity))
        self.assertEqual(self.b.ledger.snapshot()["promised_bytes"], 8*1024**3)
        self.run.side_effect = None
        self.b.dispatch(dict(op="disable",account_id=identity))
        self.b.dispatch(dict(op="restore",account_id=identity))
        with patch.object(self.b,"runtime_admission"):
            self.b.dispatch(dict(op="ensure",account_id=identity))

    def test_snapshot_reserve_follows_guest_memory_and_disable_discards_it(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve",account_id=identity,quota_gib=8))
        expected = 2048*1024**2 + broker.Broker.SNAPSHOT_STATE_BYTES
        self.assertEqual(self.b.ledger.snapshot()["snapshot_reserved_bytes"], expected)
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure",account_id=identity))
        cfg = json.loads((Path(self.config["config_root"]) / (identity + ".json")).read_text())
        self.assertNotIn("hibernate", cfg)  # default on; existing configs stay identical
        snapshot = Path(self.config["state_root"]) / identity / "snapshot"
        snapshot.mkdir(parents=True)
        for name in ("meta.json", "memory", "vmstate"):
            (snapshot / name).write_bytes(b"x")
        disk = snapshot.parent / "workspace.ext4"
        disk.write_bytes(b"disk")
        self.b.dispatch(dict(op="stop",account_id=identity))
        self.assertTrue(snapshot.exists())  # a stop/restart keeps the hibernated session
        self.b.dispatch(dict(op="disable",account_id=identity))
        self.assertFalse(snapshot.exists())
        self.assertEqual(disk.read_bytes(), b"disk")
        off = broker.Broker(dict(self.config, hibernate=False), self.run, self.b.ledger.metrics)
        self.assertEqual(off.snapshot_reserve(identity), 0)

    def test_stale_socket_and_partial_write_recovery_preserve_unexpected_files(self):
        path = Path(self.temp.name) / "broker.sock"
        with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as sock:
            sock.bind(str(path)); sock.listen()
            with self.assertRaises(broker.AdmissionError):
                broker.remove_stale_socket(path)
        broker.remove_stale_socket(path)
        self.assertFalse(path.exists())
        path.write_text("unexpected")
        with self.assertRaises(broker.AdmissionError):
            broker.remove_stale_socket(path)
        self.assertEqual(path.read_text(), "unexpected")
        artifact = Path(self.config["config_root"]) / "test.json"
        artifact.with_suffix(".json.pending").write_text("partial")
        self.b.write_owned(artifact,"complete\n",0o600)
        self.assertEqual(artifact.read_text(),"complete\n")
        bad = Path(self.config["config_root"]) / "bad.json"
        bad.with_suffix(".json.pending").symlink_to(path)
        with self.assertRaises(broker.AdmissionError):
            self.b.write_owned(bad,"complete\n",0o600)
        self.assertEqual(path.read_text(),"unexpected")


class GuestReleaseTests(unittest.TestCase):
    """Guest release changes (`tofi update`) and `tofi computers` broker ops."""
    setUp = BrokerTests.setUp
    tearDown = BrokerTests.tearDown

    def release(self, name):
        directory = Path(self.temp.name) / name
        directory.mkdir()
        (directory / "manager.py").write_text("# synthetic trusted release")
        return directory

    def worker(self, release_dir, running=()):
        """A broker on `release_dir` that supervises managers itself."""
        b = broker.Broker(dict(self.config, release_dir=str(release_dir)), self.run, self.b.ledger.metrics)
        b.manager_running = lambda identity: identity in running
        b.start_manager = Mock()  # the Worker supervisor, not systemd units
        return b

    def snapshot(self, identity, release, created="2026-10-08T00:00:00Z"):
        directory = Path(self.config["state_root"]) / identity / "snapshot"
        directory.mkdir(parents=True)
        for name in ("memory", "vmstate"):
            (directory / name).write_bytes(b"x")
        (directory / "meta.json").write_text(json.dumps({"format": 1, "created_at": created, "identity": {
            "rootfs": ["/var/lib/tofi/guest/%s/rootfs.ext4" % release, 1, 2, 3, 4]}}))
        return directory

    def config_path(self, identity):
        return Path(self.config["config_root"]) / (identity + ".json")

    def test_ensure_after_a_guest_release_change_regenerates_the_manager_config(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure", account_id=identity))
        old = json.loads(self.config_path(identity).read_text())
        newer = self.worker(self.release("v2"))
        with patch.object(newer, "runtime_admission"):
            newer.dispatch(dict(op="ensure", account_id=identity))
        cfg = json.loads(self.config_path(identity).read_text())
        self.assertEqual(cfg["image_dir"], str(Path(self.temp.name) / "v2"))
        self.assertEqual(cfg["bin_dir"], str(Path(self.temp.name) / "v2" / "bin"))
        self.assertEqual(dict(cfg, image_dir=old["image_dir"], bin_dir=old["bin_dir"]), old)
        self.assertEqual(newer.config_release(identity), "v2")
        # The owned manifest follows the new file; a later crash retry is idempotent.
        with patch.object(newer, "runtime_admission"):
            newer.dispatch(dict(op="ensure", account_id=identity))

    def test_regeneration_still_refuses_a_file_changed_outside_the_broker(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure", account_id=identity))
        path = self.config_path(identity)
        path.write_text(path.read_text().replace('"vcpus": 2', '"vcpus": 9'))
        newer = self.worker(self.release("v2"))
        with patch.object(newer, "runtime_admission"), self.assertRaises(broker.AdmissionError):
            newer.dispatch(dict(op="ensure", account_id=identity))
        self.assertIn('"vcpus": 9', path.read_text())

    def test_a_running_manager_keeps_its_config_until_its_next_start(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure", account_id=identity))
        before = self.config_path(identity).read_text()
        newer = self.worker(self.release("v2"), running={identity})
        with patch.object(newer, "runtime_admission"):
            newer.dispatch(dict(op="ensure", account_id=identity))
        self.assertEqual(self.config_path(identity).read_text(), before)

    def test_computers_lists_ledger_process_and_releases(self):
        ids = sorted(str(uuid.uuid4()) for _ in range(3))
        for identity in ids:
            self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure", account_id=ids[0]))
        self.snapshot(ids[1], "v1")
        current = self.worker(self.release("v2"), running={ids[0]})
        result = current.dispatch(dict(op="computers"))
        self.assertEqual(result["release"], "v2")
        rows = {row["account_id"]: row for row in result["computers"]}
        self.assertEqual(sorted(rows), ids)
        self.assertEqual((rows[ids[0]]["running"], rows[ids[0]]["config_release"], rows[ids[0]]["snapshot"]),
                         (True, "release", None))
        self.assertEqual((rows[ids[1]]["running"], rows[ids[1]]["snapshot"]),
                         (False, {"release": "v1", "created_at": "2026-10-08T00:00:00Z"}))
        self.assertEqual((rows[ids[2]]["state"], rows[ids[2]]["quota_bytes"], rows[ids[2]]["config_release"]),
                         ("reserved", 8 * 1024**3, None))
        with self.assertRaises(ValueError):
            current.dispatch(dict(op="computers", account_id=ids[0]))
        self.assertIsNone(self.b.dispatch(dict(op="computers"))["computers"][0]["running"])

    def test_upgrade_discards_only_a_snapshot_of_another_release(self):
        ids = [str(uuid.uuid4()) for _ in range(2)]
        for identity in ids:
            self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        old = self.snapshot(ids[0], "v1")
        disk = old.parent / "workspace.ext4"
        disk.write_bytes(b"workspace")
        current_snapshot = self.snapshot(ids[1], "v2")
        current = self.worker(self.release("v2"))
        result = current.dispatch(dict(op="upgrade", account_id=ids[0]))
        self.assertEqual(result, {"account_id": ids[0], "release": "v2", "previous_release": "v1",
                                  "stopped": False, "discarded_snapshot": True})
        self.assertFalse(old.exists())
        self.assertEqual(disk.read_bytes(), b"workspace")
        result = current.dispatch(dict(op="upgrade", account_id=ids[1]))
        self.assertFalse(result["discarded_snapshot"])
        self.assertTrue((current_snapshot / "meta.json").exists())
        self.run.assert_not_called()

    def test_upgrade_stops_a_manager_running_an_older_release(self):
        identity = str(uuid.uuid4())
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with patch.object(self.b, "runtime_admission"):
            self.b.dispatch(dict(op="ensure", account_id=identity))
        running = {identity}
        current = self.worker(self.release("v2"), running=running)
        with current.ledger.connection() as db:
            db.execute("INSERT INTO runtime_claims VALUES(?,?,?)", (identity, 2, 2560))
        current.stop_manager = Mock(side_effect=lambda i, unit: (running.discard(i), self.snapshot(i, "release")))
        result = current.dispatch(dict(op="upgrade", account_id=identity))
        current.stop_manager.assert_called_once()
        self.assertEqual((result["previous_release"], result["stopped"], result["discarded_snapshot"]),
                         ("release", True, True))
        with current.ledger.connection() as db:
            self.assertIsNone(db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?", (identity,)).fetchone())
        # The next start runs the current release; that manager is left running.
        with patch.object(current, "runtime_admission"):
            current.dispatch(dict(op="ensure", account_id=identity))
        running.add(identity)
        current.stop_manager.reset_mock()
        self.assertFalse(current.dispatch(dict(op="upgrade", account_id=identity))["stopped"])
        current.stop_manager.assert_not_called()

    def test_upgrade_needs_a_registered_computer_and_known_process_state(self):
        identity = str(uuid.uuid4())
        with self.assertRaises(broker.AdmissionError):
            self.worker(self.release("v2")).dispatch(dict(op="upgrade", account_id=identity))
        self.b.dispatch(dict(op="reserve", account_id=identity, quota_gib=8))
        with self.assertRaisesRegex(broker.AdmissionError, "unknown"):
            self.b.dispatch(dict(op="upgrade", account_id=identity))


if __name__ == "__main__":
    unittest.main()
