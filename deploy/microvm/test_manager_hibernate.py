"""Snapshot hibernation: idle gating, snapshot lifecycle, restore and fallback."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tempfile
import threading
import time
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("manager", Path(__file__).with_name("manager.py"))
manager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manager)

MIB = 1024**2


class Fixture(unittest.TestCase):
    """A VM whose state, images and binaries live in a temporary directory."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        base = Path(self.temp.name)
        for name in ("state", "image", "bin"):
            (base / name).mkdir()
        (base / "image" / "vmlinux").write_bytes(b"kernel")
        (base / "image" / "rootfs.ext4").write_bytes(b"rootfs")
        (base / "bin" / "firecracker").write_bytes(b"firecracker")
        (base / "bin" / "jailer").write_bytes(b"jailer")
        self.base = base
        self.vm = manager.VM(dict(id="hib-31", slot=31, state_dir=str(base / "state"),
                                  image_dir=str(base / "image"), bin_dir=str(base / "bin"),
                                  socket_dir=str(base / "sock"), memory_mib=512, vcpus=1, disk_gib=8,
                                  desktop_idle_seconds=60))
        self.vm.snapshot_owner = os.getuid()
        (base / "state" / "workspace.ext4").write_bytes(b"\0" * 4096)
        self.chown = mock.patch.object(manager.os, "chown").start()
        self.addCleanup(mock.patch.stopall)
        self.addCleanup(self.temp.cleanup)

    def write_snapshot(self, memory=512 * MIB):
        """Persist a snapshot exactly as hibernate() would."""
        vm = self.vm
        vm.snapshot_dir.mkdir(mode=0o700)
        with (vm.snapshot_dir / "memory").open("wb") as stream:
            stream.truncate(memory)
        (vm.snapshot_dir / "vmstate").write_bytes(b"device-state")
        for name in manager.SNAPSHOT_FILES:
            os.chmod(vm.snapshot_dir / name, 0o600)
        vm.persist_meta({"format": manager.SNAPSHOT_FORMAT, "identity": vm.snapshot_identity(),
                         "disk": vm.disk_fingerprint(),
                         "sizes": {n: (vm.snapshot_dir / n).stat().st_size for n in manager.SNAPSHOT_FILES},
                         "vmstate_sha256": vm.file_digest(vm.snapshot_dir / "vmstate"),
                         "created_at": "2026-10-08T00:00:00Z"})

    def running_process(self):
        process = mock.Mock(pid=4242)
        process.poll.return_value = None
        self.vm.process = process
        return process


class IdleGateTests(Fixture):
    def test_only_an_idle_ready_running_vm_starts_hibernating(self):
        vm = self.vm
        self.running_process()
        vm.state = "ready"
        now = time.monotonic()
        vm.last_activity = now - 30
        self.assertFalse(vm.begin_hibernation(now))  # idle window not reached
        vm.last_activity = now - 61
        vm.active = 1
        self.assertFalse(vm.begin_hibernation(now))  # request in flight
        vm.active = 0
        vm.hibernate_not_before = now + 10
        self.assertFalse(vm.begin_hibernation(now))  # busy guest backoff
        vm.hibernate_not_before = 0
        vm.c["hibernate"] = False
        self.assertFalse(vm.begin_hibernation(now))
        vm.c["hibernate"] = True
        self.assertTrue(vm.begin_hibernation(now))
        self.assertEqual(vm.state, "hibernating")
        # No request is admitted while the guest is being paused.
        self.assertFalse(vm.begin_activity())

    def test_zero_idle_disables_hibernation_and_keeps_guest_cleanup(self):
        self.vm.c["desktop_idle_seconds"] = 0
        self.assertFalse(self.vm.hibernation_enabled())
        self.assertIn("tofi_desktop_idle=0", self.vm.firecracker_config()["boot-source"]["boot_args"])

    def test_guest_desktop_cleanup_is_a_later_backstop_when_hibernating(self):
        self.assertIn("tofi_desktop_idle=120", self.vm.firecracker_config()["boot-source"]["boot_args"])
        self.vm.c["hibernate"] = False
        self.assertIn("tofi_desktop_idle=60", self.vm.firecracker_config()["boot-source"]["boot_args"])

    def test_hibernate_flag_is_strict(self):
        for value in ("yes", 1, None):
            with self.assertRaises(ValueError):
                manager.validate_config(dict(self.vm.c, hibernate=value))


class SnapshotValidityTests(Fixture):
    def test_valid_snapshot_is_accepted_and_files_are_private(self):
        self.write_snapshot()
        self.assertIsNone(self.vm.snapshot_problem())
        for name in ("memory", "vmstate", "meta.json"):
            self.assertEqual((self.vm.snapshot_dir / name).stat().st_mode & 0o777, 0o600)

    def test_missing_or_corrupt_files_are_rejected(self):
        self.assertEqual(self.vm.snapshot_problem(), "no snapshot")
        self.write_snapshot()
        (self.vm.snapshot_dir / "vmstate").write_bytes(b"device-stat3")
        self.assertEqual(self.vm.snapshot_problem(), "snapshot state is corrupt")
        (self.vm.snapshot_dir / "memory").unlink()
        self.assertIn("unreadable", self.vm.snapshot_problem())

    def test_other_release_machine_or_disk_is_rejected(self):
        self.write_snapshot()
        (self.base / "bin" / "firecracker").write_bytes(b"firecracker v2")
        self.assertIn("another release", self.vm.snapshot_problem())
        (self.base / "bin" / "firecracker").write_bytes(b"firecracker")
        self.assertIsNone(self.vm.snapshot_problem())
        self.vm.c["vcpus"] = 2
        self.assertIn("machine configuration", self.vm.snapshot_problem())
        self.vm.c["vcpus"] = 1
        # Guest boot arguments (idle, tab cap) live in the saved memory: a
        # different value can only take effect through a cold boot.
        self.vm.c["browser_max_tabs"] = self.vm.c.get("browser_max_tabs", manager.DEFAULT_BROWSER_MAX_TABS) + 1
        self.assertIn("machine configuration", self.vm.snapshot_problem())
        del self.vm.c["browser_max_tabs"]
        self.assertIsNone(self.vm.snapshot_problem())
        with (self.base / "state" / "workspace.ext4").open("r+b") as disk:
            disk.write(b"x")
        self.assertEqual(self.vm.snapshot_problem(), "workspace disk changed since the snapshot")

    def test_pending_resource_change_and_loose_permissions_are_rejected(self):
        self.write_snapshot()
        os.chmod(self.vm.snapshot_dir / "memory", 0o644)
        self.assertIn("not the saved file", self.vm.snapshot_problem())
        os.chmod(self.vm.snapshot_dir / "memory", 0o600)
        self.vm.persist_resources("resources-desired.json", {"vcpus": 1, "memory_mib": 1024, "disk_gib": 8})
        self.assertEqual(self.vm.snapshot_problem(), "resource change pending")

    def test_discard_removes_commit_marker_first_and_everything(self):
        self.write_snapshot()
        removed = []
        original = Path.unlink
        def unlink(path, *args, **kwargs):
            removed.append(path.name)
            return original(path, *args, **kwargs)
        with mock.patch.object(Path, "unlink", unlink):
            self.vm.discard_snapshot()
        self.assertEqual(removed[0], "meta.json")
        self.assertFalse(self.vm.snapshot_dir.exists())
        self.vm.discard_snapshot()  # idempotent


class HibernateTests(Fixture):
    def prepare(self, busy=()):
        vm = self.vm
        self.running_process()
        vm.state = "hibernating"
        vm.jail.mkdir(parents=True)
        (vm.jail / "workspace.ext4").write_bytes(b"alias")
        vm.quiesce_guest = mock.Mock(return_value=list(busy))
        calls = []
        def api(method, path, body=None, timeout=10):
            calls.append((method, path, body))
            if path == "/snapshot/create":
                # Firecracker writes inside its jail as the jailer UID.
                with (vm.jail / "snapshot" / "memory").open("wb") as stream:
                    stream.truncate(512 * MIB)
                (vm.jail / "snapshot" / "vmstate").write_bytes(b"device-state")
            return b""
        vm.api_request = api
        def end():
            vm.process = None
        vm.end_firecracker = mock.Mock(side_effect=end)
        vm.start = mock.Mock()
        return calls

    def test_idle_guest_is_paused_saved_and_released(self):
        calls = self.prepare()
        with mock.patch.object(manager, "run") as run:
            self.assertTrue(self.vm.hibernate())
        self.assertEqual([c[:2] for c in calls], [("PATCH", "/vm"), ("PUT", "/snapshot/create")])
        self.assertEqual(calls[0][2], {"state": "Paused"})
        self.assertEqual(calls[1][2]["snapshot_type"], "Full")
        self.vm.end_firecracker.assert_called_once()
        run.assert_called_once_with("fallocate", "--dig-holes", str(self.vm.snapshot_dir / "memory"), check=False)
        self.assertEqual((self.vm.state, self.vm.phase), ("hibernated", "hibernated"))
        self.assertIsNone(self.vm.snapshot_problem())
        self.assertFalse(self.vm.jail.parent.exists())  # jail and its disk alias removed
        self.assertTrue((self.vm.root / "workspace.ext4").exists())
        self.assertTrue(self.vm.hibernated_at)
        self.vm.start.assert_not_called()

    def test_busy_guest_stays_running_and_is_asked_again_later(self):
        calls = self.prepare(busy=["terminal_job"])
        self.assertFalse(self.vm.hibernate())
        self.assertEqual(calls, [])
        self.assertEqual(self.vm.state, "ready")
        self.assertGreater(self.vm.hibernate_not_before, time.monotonic())
        self.assertFalse(self.vm.snapshot_dir.exists())

    def test_failed_snapshot_resumes_the_paused_guest_and_keeps_no_files(self):
        calls = self.prepare()
        def api(method, path, body=None, timeout=10):
            calls.append((method, path, body))
            if path == "/snapshot/create":
                (self.vm.jail / "snapshot" / "memory").write_bytes(b"partial")
                raise RuntimeError("Firecracker PUT /snapshot/create returned 400: disk full")
            return b""
        self.vm.api_request = api
        self.assertFalse(self.vm.hibernate())
        self.assertEqual(calls[-1], ("PATCH", "/vm", {"state": "Resumed"}))
        self.assertEqual(self.vm.state, "ready")
        self.assertFalse(self.vm.snapshot_dir.exists())
        self.assertFalse((self.vm.jail / "snapshot").exists())
        self.vm.end_firecracker.assert_not_called()

    def test_insufficient_disk_never_pauses(self):
        calls = self.prepare()
        with mock.patch.object(manager.shutil, "disk_usage", return_value=mock.Mock(free=600 * MIB)):
            self.assertFalse(self.vm.hibernate())
        self.assertEqual(calls, [])
        self.assertEqual(self.vm.state, "ready")

    def test_lost_vm_after_pause_cold_boots_from_disk(self):
        self.prepare()
        def end():
            self.vm.process = None
            raise OSError("kill failed")
        self.vm.end_firecracker = mock.Mock(side_effect=end)
        self.assertFalse(self.vm.hibernate())
        self.vm.start.assert_called_once()
        self.assertFalse(self.vm.snapshot_dir.exists())

    def test_exit_hibernation_never_cold_boots(self):
        self.prepare()
        self.vm.end_firecracker = mock.Mock(side_effect=OSError("kill failed"))
        self.vm.process.poll.return_value = 0
        self.assertFalse(self.vm.hibernate(wait_for_idle=False, fallback_boot=False))
        self.vm.start.assert_not_called()
        self.assertEqual(self.vm.state, "error")


class ResumeTests(Fixture):
    def prepare(self):
        vm = self.vm
        vm.state = "resuming"
        vm.network_owned = True
        vm.host_resources = mock.Mock(return_value={"memory_available_mib": 8192})
        calls = []
        def api(method, path, body=None, timeout=10):
            calls.append((method, path, body, (vm.snapshot_dir / "meta.json").exists()))
            return b""
        vm.api_request = api
        def launch(cmd):
            self.assertNotIn("--config-file", cmd)
            self.running_process()
        vm.launch = mock.Mock(side_effect=launch)
        vm.guest_request = mock.Mock(return_value={"ok": True})
        vm.sync_guest_clock = mock.Mock(return_value=True)
        vm.start = mock.Mock()
        return calls

    def test_valid_snapshot_restores_and_is_consumed_before_the_guest_runs(self):
        self.write_snapshot()
        calls = self.prepare()
        with mock.patch.object(manager, "attach_immutable_image"):
            self.vm.resume()
        load = [c for c in calls if c[1] == "/snapshot/load"]
        self.assertEqual(len(load), 1)
        self.assertEqual(load[0][2], {"snapshot_path": "/snapshot/vmstate",
                                      "mem_backend": {"backend_type": "File", "backend_path": "/snapshot/memory"},
                                      "resume_vm": True})
        self.assertFalse(load[0][3], "commit marker must be gone before the guest resumes")
        self.vm.sync_guest_clock.assert_called_once()
        self.assertEqual(self.vm.state, "ready")
        self.assertEqual(self.vm.last_wake["kind"], "restore")
        self.assertFalse(self.vm.snapshot_dir.exists())
        self.vm.start.assert_not_called()
        # The workspace disk was attached, never checked or reformatted.
        self.assertTrue((self.vm.jail / "workspace.ext4").exists())

    def test_rejected_snapshot_cold_boots_and_reports_a_path_free_reason(self):
        self.write_snapshot()
        (self.base / "image" / "vmlinux").write_bytes(b"new kernel")
        self.prepare()
        def start():
            self.vm.last_wake = {"kind": "cold_boot", "seconds": 1.0}
            self.vm.state = "ready"
        self.vm.start.side_effect = start
        self.vm.resume()
        self.vm.start.assert_called_once()
        self.vm.launch.assert_not_called()
        self.assertIn("another release", self.vm.last_wake["fallback_reason"])
        self.assertNotIn(self.temp.name, json.dumps(self.vm.info()))

    def test_failed_load_cold_boots_with_generic_reason(self):
        self.write_snapshot()
        calls = self.prepare()
        def api(method, path, body=None, timeout=10):
            if path == "/snapshot/load":
                raise RuntimeError("Firecracker PUT /snapshot/load returned 400: " + str(self.base))
            return b""
        self.vm.api_request = api
        self.vm.end_firecracker = mock.Mock()
        def start():
            self.vm.last_wake = {"kind": "cold_boot", "seconds": 1.0}
        self.vm.start.side_effect = start
        with mock.patch.object(manager, "attach_immutable_image"):
            self.vm.resume()
        self.vm.end_firecracker.assert_called()
        self.vm.start.assert_called_once()
        self.assertEqual(self.vm.last_wake["fallback_reason"], "restore failed")

    def test_insufficient_host_memory_keeps_the_snapshot(self):
        self.write_snapshot()
        self.prepare()
        self.vm.host_resources.return_value = {"memory_available_mib": 600}
        self.vm.resume()
        self.assertEqual(self.vm.state, "hibernated")
        self.assertIsNone(self.vm.snapshot_problem())
        self.vm.start.assert_not_called()

    def test_cold_boot_discards_any_snapshot_first(self):
        self.write_snapshot()
        self.vm.apply_resources = mock.Mock(side_effect=RuntimeError("stop here"))
        self.vm.stop = mock.Mock()
        with self.assertRaises(RuntimeError):
            self.vm.start()
        self.assertFalse(self.vm.snapshot_dir.exists())

    def test_purge_discards_the_snapshot_of_the_replaced_disk(self):
        self.write_snapshot()
        self.vm.state = "hibernated"
        self.vm.stop = mock.Mock()
        with mock.patch.object(manager, "run", side_effect=RuntimeError("stop here")):
            with self.assertRaises(RuntimeError):
                self.vm.purge_workspace()
        self.assertFalse(self.vm.snapshot_dir.exists())


class ServerTests(Fixture):
    def server(self):
        server = manager.Server.__new__(manager.Server)
        server.preparation_lock = threading.Lock()
        server.vm = self.vm
        return server

    def test_a_request_wakes_a_hibernated_computer_and_waits_for_it(self):
        server = self.server()
        self.vm.state = "hibernated"
        def resume():
            time.sleep(.1)
            self.vm.state = "ready"
        self.vm.resume = mock.Mock(side_effect=resume)
        with mock.patch.object(manager, "Path", wraps=manager.Path) as path:
            path.side_effect = lambda *a: Path(self.temp.name) / "admission.lock" if a == ("/run/tofi-firecracker-locks/boot-admission.lock",) else Path(*a)
            with server.guest_use(timeout=5) as ready:
                self.assertTrue(ready)
                self.assertEqual(self.vm.active, 1)
        self.assertEqual(self.vm.active, 0)
        self.vm.resume.assert_called_once()

    def test_stopped_computer_is_not_started_by_ordinary_requests(self):
        server = self.server()
        self.vm.state = "stopped"
        server.prepare = mock.Mock()
        self.assertFalse(server.acquire_guest(timeout=1))
        server.prepare.assert_not_called()

    def test_health_and_recovery_leave_a_hibernated_computer_alone(self):
        server = self.server()
        self.vm.state = "hibernated"
        self.vm.guest_request = mock.Mock(side_effect=AssertionError("must not probe"))
        server.restart = mock.Mock(side_effect=AssertionError("must not restart"))
        self.assertEqual(server.guest_health(), {"state": "hibernated", "guest": "hibernated"})
        self.assertEqual(server.recover(), "responsive")

    def test_boot_keeps_a_valid_snapshot_hibernated_without_starting(self):
        self.write_snapshot()
        server = self.server()
        server.prepare = mock.Mock()
        server.boot()
        self.assertEqual(self.vm.state, "hibernated")
        self.assertEqual(self.vm.info()["hibernated_at"], "2026-10-08T00:00:00Z")
        server.prepare.assert_not_called()

    def test_boot_discards_a_rejected_snapshot_and_cold_boots(self):
        self.write_snapshot()
        (self.base / "image" / "rootfs.ext4").write_bytes(b"new rootfs")
        server = self.server()
        server.prepare = mock.Mock()
        server.boot()
        server.prepare.assert_called_once()
        self.assertFalse(self.vm.snapshot_dir.exists())

    def test_idle_monitor_hibernates_through_the_maintenance_lease(self):
        server = self.server()
        self.vm.state = "ready"
        self.running_process()
        self.vm.last_activity = time.monotonic() - 120
        done = threading.Event()
        self.vm.hibernate = mock.Mock(side_effect=lambda **_: done.set())
        self.assertTrue(server.maybe_hibernate())
        self.assertTrue(done.wait(2))
        server.preparation.join(2)
        self.vm.state = "ready"
        self.vm.last_activity = time.monotonic()
        self.assertFalse(server.maybe_hibernate())


if __name__ == "__main__":
    unittest.main()
