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


if __name__ == "__main__":
    unittest.main()
