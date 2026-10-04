import importlib.util
from pathlib import Path
import sys
import hashlib
import json
import io
from contextlib import redirect_stderr
import socket
import tempfile
import unittest
from unittest.mock import Mock, ANY, patch

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
spec = importlib.util.spec_from_file_location("worker_entrypoint", HERE / "worker_entrypoint.py")
entry = importlib.util.module_from_spec(spec)
spec.loader.exec_module(entry)
import account_release_check as checker


class ControlledSignals:
    SIGTERM, SIGINT = 15, 2
    def __init__(self):
        self.original = {15: "previous-term", 2: "previous-int"}
        self.handlers = dict(self.original)
    def signal(self, sig, handler):
        previous = self.handlers[sig]
        self.handlers[sig] = handler
        return previous
    def stop(self):
        for sig in (self.SIGTERM, self.SIGINT, self.SIGTERM):
            self.handlers[sig](sig, None)


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
            expected_manifest_sha256=config["release_manifest_sha256"], cancel=ANY)

    def test_early_and_hash_cancellation_exit_zero_without_resource_factories(self):
        image = Path(self.tmp.name)/"new-synthetic-image"
        image.write_bytes(b"x" * (3*1024**2))
        config_file = Path(self.tmp.name)/"config.json"
        config_file.write_text(json.dumps(self.config))
        for phase in ("verification-entry", "hash-chunk"):
            with self.subTest(phase=phase):
                signals = ControlledSignals()
                groups, broker, server = Mock(), Mock(), Mock()
                chunks = []
                real_digest = hashlib.sha256
                class Digest:
                    def __init__(self): self.digest = real_digest()
                    def update(self, data):
                        chunks.append(len(data)); self.digest.update(data)
                        signals.stop()  # Repeated startup signals only latch.
                    def hexdigest(self): return self.digest.hexdigest()
                def verifier(*_, cancel, **__):
                    self.assertTrue(callable(signals.handlers[signals.SIGTERM]))
                    if phase == "verification-entry":
                        signals.stop(); cancel()
                    with patch.object(checker.hashlib, "sha256", Digest):
                        checker._sha256(image, cancel)
                actual_serve = entry.serve
                def serve(config):
                    return actual_serve(config, groups, broker, server,
                                        signal_api=signals, release_validator=verifier)
                with patch.object(entry, "serve", side_effect=serve):
                    self.assertEqual(entry.main(["--config", str(config_file)]), 0)
                groups.initialize.assert_not_called()
                broker.assert_not_called(); server.assert_not_called()
                self.assertEqual(chunks, [] if phase == "verification-entry" else [1024**2])
                self.assertEqual(signals.handlers, signals.original)
                self.assertFalse(Path(self.config["broker_socket"]).exists())

    def test_postverification_acquisition_boundaries_clean_up_without_readiness(self):
        for phase in ("verified", "mounted", "exposed", "broker", "socket"):
            with self.subTest(phase=phase):
                signals, events = ControlledSignals(), []
                class Groups(Cgroups):
                    def initialize(self):
                        super().initialize()
                        if phase == "mounted": signals.stop()
                    def expose_for_jailer(self):
                        super().expose_for_jailer()
                        if phase == "exposed": signals.stop()
                def verifier(*_, **__):
                    events.append("verified")
                    if phase == "verified": signals.stop()
                supervisor = Mock()
                def close_supervisor():
                    events.append("supervisor-close"); signals.stop()
                supervisor.close.side_effect = close_supervisor
                def broker(_):
                    events.append("broker")
                    if phase == "broker": signals.stop()
                    return Mock(supervisor=supervisor)
                class Server:
                    def __init__(self, path, *_):
                        self.socket = socket.socket(socket.AF_UNIX)
                        self.socket.bind(path); events.append("socket-open")
                        if phase == "socket": signals.stop()
                    def serve_forever(self, **_):
                        raise AssertionError("cancelled startup reached readiness")
                    def server_close(self):
                        events.append("server-close"); signals.stop(); self.socket.close()
                def unmount(path):
                    events.append("unmount:"+str(path)); signals.stop()
                with patch.object(entry.os, "geteuid", return_value=0), \
                     patch.object(entry.os, "chown"), patch.object(entry.os, "chmod"):
                    with self.assertRaises(KeyboardInterrupt):
                        entry.serve(self.config, Groups(events), broker, Server,
                                    signal_api=signals, release_validator=verifier, umount=unmount)
                self.assertEqual(signals.handlers, signals.original)
                self.assertFalse(Path(self.config["broker_socket"]).exists())
                if phase == "verified": self.assertEqual(events, ["verified"])
                if phase == "mounted": self.assertEqual(events[-1], "unmount:"+str(entry.ROOT))
                if phase in ("broker", "socket"):
                    self.assertLess(events.index("supervisor-close"), events.index("unmount:/sys/fs/cgroup"))
                if phase == "socket":
                    self.assertLess(events.index("server-close"), events.index("supervisor-close"))
                    # The lifetime lock is released after repeated cleanup signals.
                    with open(Path(self.config["ledger_root"])/"service.lock", "a") as lock:
                        entry.fcntl.flock(lock, entry.fcntl.LOCK_EX|entry.fcntl.LOCK_NB)

    def test_verification_failure_stays_nonzero_and_restores_handlers(self):
        signals = ControlledSignals()
        groups, broker, server = Mock(), Mock(), Mock()
        verifier = Mock(side_effect=ValueError("SHA-256 mismatch"))
        config_file = Path(self.tmp.name)/"bad-digest-config.json"
        config_file.write_text(json.dumps(self.config))
        actual_serve = entry.serve
        def serve(config):
            return actual_serve(config, groups, broker, server,
                                signal_api=signals, release_validator=verifier)
        with patch.object(entry, "serve", side_effect=serve), redirect_stderr(io.StringIO()) as errors:
            self.assertEqual(entry.main(["--config", str(config_file)]), 1)
        self.assertIn("SHA-256 mismatch", errors.getvalue())
        groups.initialize.assert_not_called()
        broker.assert_not_called(); server.assert_not_called()
        self.assertEqual(signals.handlers, signals.original)

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
