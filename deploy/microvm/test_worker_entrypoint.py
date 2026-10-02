import importlib.util
from pathlib import Path
import sys
import socket
import tempfile
import unittest
from unittest.mock import Mock

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
spec = importlib.util.spec_from_file_location("worker_entrypoint", HERE / "worker_entrypoint.py")
entry = importlib.util.module_from_spec(spec)
spec.loader.exec_module(entry)


class Cgroups:
    def __init__(self, events): self.events = events; self.mounted = False; self.exposed = False
    def initialize(self): self.events.append("initialize"); self.mounted = True
    def expose_for_jailer(self): self.events.append("expose"); self.exposed = True


class WorkerEntryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir="/tmp")
        root = Path(self.tmp.name)
        self.config = {
            "release_dir": str(root / "release"), "state_root": str(root / "state"),
            "config_root": str(root / "configs"), "unit_root": str(root / "unused"),
            "socket_root": str(root / "sockets"), "ledger_root": str(root / "ledger"),
            "broker_socket": str(root / "sockets" / "broker.sock"), "socket_gid": 10001,
            "app_uid": 10001, "headroom_bytes": 0, "warning_bytes": 0,
            "reserved_slots": [1, 21], "vcpus": 1, "memory_mib": 1024,
            "runtime_vcpu_budget": 8, "runtime_memory_mib_budget": 8192,
            "host_memory_headroom_mib": 512,
            "cgroup_root": str(entry.ROOT / entry.JAILER_PARENT),
            "external_reserved_bytes": 1024, "per_account_internal_reserved_bytes": 512,
            "external_disks": [], "expected_guest_sha256": "a"*64,
            "release_manifest_sha256": "b"*64,
        }
        release = Path(self.config["release_dir"])
        release.mkdir()
        for name in ("rootfs.ext4", "vmlinux"): (release/name).write_bytes(b"fixture")
        self.release_validator = Mock()
        Path(self.config["socket_root"]).mkdir()
        Path(self.config["ledger_root"]).mkdir()

    def tearDown(self): self.tmp.cleanup()

    def test_config_is_strict_and_private_cgroup_root_is_fixed(self):
        entry.validate_config(self.config)
        with self.assertRaises(ValueError): entry.validate_config(dict(self.config, secret="x"))
        with self.assertRaises(ValueError): entry.validate_config(dict(self.config, cgroup_root="/sys/fs/cgroup"))
        with self.assertRaises(ValueError): entry.validate_config(dict(self.config, reserved_slots=1))
        with self.assertRaises(ValueError): entry.validate_config(dict(self.config, expected_guest_sha256="unpinned"))

    def test_scoped_resource_override_and_invalid_bounds(self):
        import uuid
        entry.validate_config(dict(self.config, account_resource_overrides={str(uuid.uuid4()):dict(vcpus=2,memory_mib=4096)}))
        with self.assertRaises(ValueError):
            entry.validate_config(dict(self.config, account_resource_overrides={str(uuid.uuid4()):dict(vcpus=True,memory_mib=4096)}))
        with self.assertRaises(ValueError):
            entry.validate_config(dict(self.config, account_resource_overrides={'personal':dict(vcpus=2,memory_mib=4096)}))

    def test_candidate_json_matches_required_config_schema(self):
        import json
        candidate = __import__("json").loads((HERE / "worker-account-candidate.json").read_text())
        entry.validate_config(candidate)

    def test_initializes_before_socket_and_closes_supervisor_before_unmount(self):
        events = []
        supervisor = Mock()
        supervisor.close.side_effect = lambda: events.append("supervisor-close")
        broker = Mock(supervisor=supervisor)
        class FakeServer:
            def __init__(self, path, *_):
                events.append("socket-open")
                self.socket = socket.socket(socket.AF_UNIX)
                self.socket.bind(path)
            def serve_forever(self, **_):
                events.append("serve")
                signals.handlers[signals.SIGTERM](signals.SIGTERM, None)
            def server_close(self):
                self.socket.close()
                events.append("server-close")
        class Signals:
            SIGTERM, SIGINT = 15, 2
            handlers = {}
            @classmethod
            def signal(cls, sig, handler):
                previous = cls.handlers.get(sig)
                cls.handlers[sig] = handler
                return previous
        signals = Signals
        factory = Mock(return_value=broker)
        def unmount(path): events.append("unmount:" + str(path))
        with unittest.mock.patch.object(entry.os, "geteuid", return_value=0), \
             unittest.mock.patch.object(entry.os, "chown"), \
             unittest.mock.patch.object(entry.os, "chmod"):
            with self.assertRaises(KeyboardInterrupt):
                entry.serve(self.config, Cgroups(events), factory, FakeServer, release_validator=self.release_validator,
                            signal_api=signals, umount=unmount)
        trusted = factory.call_args[0][0]["_validated_immutable_image_sizes"]
        self.assertEqual(trusted, {name: (Path(self.config["release_dir"])/name).stat().st_size
                                  for name in ("rootfs.ext4", "vmlinux")})
        self.assertNotIn("_validated_immutable_image_sizes", self.config)
        with self.assertRaises(ValueError):
            entry.validate_config(dict(self.config, _validated_immutable_image_sizes=trusted))
        self.assertLess(events.index("initialize"), events.index("expose"))
        self.assertLess(events.index("expose"), events.index("socket-open"))
        self.assertLess(events.index("supervisor-close"), events.index("unmount:/sys/fs/cgroup"))
        self.assertLess(events.index("unmount:/sys/fs/cgroup"), events.index("unmount:" + str(entry.ROOT)))

    def test_release_failure_and_zero_cpu_fail_before_isolation(self):
        groups = Cgroups([])
        validator = Mock(side_effect=ValueError("manifest mismatch"))
        with self.assertRaises(ValueError):
            entry.serve(self.config, groups, release_validator=validator)
        self.assertEqual(groups.events, [])
        config = dict(self.config, runtime_vcpu_budget=0)
        with self.assertRaises(entry.AdmissionError):
            entry.serve(config, groups, release_validator=self.release_validator)
        self.assertEqual(groups.events, [])
        self.release_validator.assert_called_with(config["release_dir"],
            str((HERE / "manager.py").resolve()), config["expected_guest_sha256"],
            expected_manifest_sha256=config["release_manifest_sha256"])

    def test_startup_failure_does_not_open_socket_or_continue(self):
        events = []
        class Broken(Cgroups):
            def expose_for_jailer(self): self.mounted = True; raise RuntimeError("denied")
        with unittest.mock.patch.object(entry.os, "geteuid", return_value=0):
            with self.assertRaises(RuntimeError):
                entry.serve(self.config, Broken(events), release_validator=self.release_validator, server_factory=Mock(), umount=lambda _: None)
        self.assertEqual(events, ["initialize"])

    def test_unresolved_process_cleanup_retains_private_cgroup_mounts(self):
        events = []
        supervisor = Mock()
        supervisor.close.side_effect = RuntimeError("still populated")
        broker = Mock(supervisor=supervisor)
        class FakeServer:
            def __init__(self, path, *_):
                self.socket = socket.socket(socket.AF_UNIX)
                self.socket.bind(path)
            def serve_forever(self, **_): raise KeyboardInterrupt
            def server_close(self):
                self.socket.close()
                events.append("server-close")
        class Signals:
            SIGTERM, SIGINT = 15, 2
            @staticmethod
            def signal(*_): return None
        with unittest.mock.patch.object(entry.os, "geteuid", return_value=0), \
             unittest.mock.patch.object(entry.os, "chown"), \
             unittest.mock.patch.object(entry.os, "chmod"):
            with self.assertRaises(entry.AdmissionError):
                entry.serve(self.config, Cgroups(events), lambda _: broker, FakeServer, release_validator=self.release_validator,
                            signal_api=Signals, umount=lambda path: events.append("unmount:" + str(path)))
        self.assertFalse(any(event.startswith("unmount:") for event in events))

    def test_unexpected_socket_artifact_is_preserved_on_startup_failure(self):
        path = Path(self.config["broker_socket"])
        path.write_text("unrelated data")
        broker = Mock()
        with unittest.mock.patch.object(entry.os, "geteuid", return_value=0), \
             unittest.mock.patch.object(entry.os, "chown"), \
             unittest.mock.patch.object(entry.os, "chmod"):
            with self.assertRaises(entry.AdmissionError):
                entry.serve(self.config, Cgroups([]), lambda _: broker, release_validator=self.release_validator, umount=lambda _: None)
        self.assertEqual(path.read_text(), "unrelated data")


if __name__ == "__main__": unittest.main()
