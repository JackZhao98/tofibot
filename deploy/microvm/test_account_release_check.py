import hashlib
import importlib.util
import json
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "account_release_check", Path(__file__).with_name("account_release_check.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class AccountReleaseCheckTests(unittest.TestCase):
    def setUp(self):
        # Resolve macOS temporary-path aliases for the canonical-path validator.
        self.temp = tempfile.TemporaryDirectory(dir="/tmp")
        self.root = Path(self.temp.name).resolve()
        self.release = self.root / "release"
        (self.release / "bin").mkdir(parents=True)
        self.manager_source = self.root / "operator-manager.py"
        self.manager_source.write_bytes(b"# trusted synthetic manager source\n")
        self.guest_sha = hashlib.sha256(b"synthetic guest artifact").hexdigest()
        self.file_bytes = {
            "manager.py": self.manager_source.read_bytes(),
            "rootfs.ext4": b"synthetic root filesystem",
            "vmlinux": b"synthetic kernel",
            "bin/firecracker": b"synthetic firecracker executable",
            "bin/jailer": b"synthetic jailer executable",
        }
        for name, content in self.file_bytes.items():
            (self.release / name).write_bytes(content)
        self.manifest = {
            "schema": 1,
            "protocol": checker.PROTOCOL,
            "files_sha256": {name: self.digest(data) for name, data in self.file_bytes.items()},
            "guest_binary_sha256": self.guest_sha,
            "capabilities": sorted(checker.REQUIRED_CAPABILITIES),
            "features": ["synthetic acceptance metadata"],
            "operator_metadata": {"purpose": "fixture only"},
        }
        self.write_manifest()
        # These fixtures deliberately are not ext4 images. Exercise the
        # checker contract with an explicit synthetic embedded-guest fixture.
        self.real_embedded_extractor = checker._embedded_guest_sha256
        self.embedded = mock.patch.object(checker, "_embedded_guest_sha256", return_value=self.guest_sha)
        self.embedded.start()

    def tearDown(self):
        self.embedded.stop()
        self.temp.cleanup()

    @staticmethod
    def digest(data):
        return hashlib.sha256(data).hexdigest()

    def write_manifest(self):
        self.release.chmod(0o755)
        (self.release / "bin").chmod(0o755)
        manifest_path = self.release / "account-release.json"
        if manifest_path.exists():
            manifest_path.chmod(0o644)
        manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")
        manifest_path.chmod(0o444)
        for name in checker.FILES:
            (self.release / name).chmod(0o555 if name.startswith("bin/") else 0o444)
        (self.release / "bin").chmod(0o555)
        self.release.chmod(0o555)

    def validate(self, **kwargs):
        args = dict(release_dir=self.release, manager_source=self.manager_source,
                    guest_sha256=self.guest_sha)
        args.update(kwargs)
        return checker.validate_release(**args)

    def test_valid_candidate_and_explicit_runtime_evidence_boundary(self):
        result = self.validate()
        self.assertTrue(result["valid"])
        self.assertEqual(result["protocol"], checker.PROTOCOL)
        self.assertFalse(result["runtime_verified"])

    def test_rejects_manager_and_guest_digest_mismatches(self):
        other_manager = self.root / "different-manager.py"
        other_manager.write_bytes(b"different operator source\n")
        with self.assertRaisesRegex(checker.ReleaseCheckError, "manager source"):
            self.validate(manager_source=other_manager)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "guest binary SHA-256"):
            self.validate(guest_sha256=self.digest(b"wrong guest"))
        with self.assertRaisesRegex(checker.ReleaseCheckError, "64 lowercase hex"):
            self.validate(guest_sha256="../guest")

    def test_rejects_guest_metadata_that_does_not_match_extracted_image(self):
        with mock.patch.object(checker, "_embedded_guest_sha256", return_value=self.digest(b"different embedded guest")):
            with self.assertRaisesRegex(checker.ReleaseCheckError, "embedded in rootfs"):
                self.validate()

    def test_pins_exact_manifest_bytes(self):
        digest = checker._sha256(self.release / "account-release.json")
        self.assertEqual(self.validate(expected_manifest_sha256=digest)["manifest_sha256"], digest)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "manifest SHA-256"):
            self.validate(expected_manifest_sha256="0" * 64)

    def test_guest_extraction_uses_read_only_debugfs_invocation(self):
        def fake_debugfs(args, **kwargs):
            self.assertEqual(args[0:2], ["debugfs", "-R"])
            self.assertTrue(args[2].startswith("dump /usr/local/bin/tofi-guest "))
            extracted = Path(args[2].split(" ", 2)[2])
            extracted.write_bytes(b"synthetic embedded guest")
            self.assertFalse(any(arg == "-w" for arg in args))
            self.assertEqual(kwargs["stdout"], subprocess.PIPE)
            child = mock.Mock(args=args, returncode=0, stdout=None, stderr=None)
            child.poll.return_value = 0
            child.communicate.return_value = ("", "")
            return child

        with mock.patch.object(checker.subprocess, "Popen", side_effect=fake_debugfs):
            expected = self.digest(b"synthetic embedded guest")
            self.assertEqual(self.real_embedded_extractor(self.release / "rootfs.ext4"), expected)

    def test_hash_and_between_file_cancellation_stop_before_more_work(self):
        image = self.root/"new-synthetic-large-image"
        image.write_bytes(b"x"*(3*1024**2))
        real_digest = hashlib.sha256
        cancelled, chunks, handles = [], [], []
        class Digest:
            def __init__(self): self.digest = real_digest()
            def update(self, data):
                chunks.append(len(data)); self.digest.update(data); cancelled.append(True)
            def hexdigest(self): return self.digest.hexdigest()
        def cancel():
            if cancelled: raise KeyboardInterrupt
        real_open = Path.open
        def opened(path, *args, **kwargs):
            handle = real_open(path, *args, **kwargs)
            if path == image: handles.append(handle)
            return handle
        with mock.patch.object(checker.hashlib, "sha256", Digest), \
             mock.patch.object(Path, "open", opened):
            with self.assertRaises(KeyboardInterrupt): checker._sha256(image, cancel)
        self.assertEqual(chunks, [1024**2])
        self.assertTrue(handles[0].closed)
        cancelled.clear(); hashed = []
        real_hash = checker._sha256
        def hashing(path, cancel=None):
            value = real_hash(path, cancel); hashed.append(path.name)
            if path.name == "manager.py": cancelled.append(True)
            return value
        with mock.patch.object(checker, "_sha256", hashing):
            with self.assertRaises(KeyboardInterrupt): self.validate(cancel=cancel)
        self.assertEqual(hashed, ["account-release.json", "manager.py"])
        checker._embedded_guest_sha256.assert_not_called()

    def test_debugfs_cancellation_terminates_or_kills_and_reaps_before_cleanup(self):
        real_popen, real_remove = subprocess.Popen, checker.shutil.rmtree
        for ignores_term in (False, True):
            with self.subTest(ignores_term=ignores_term):
                ready = self.root/("ready-"+str(ignores_term))
                children, events = [], []
                def spawn(args, **kwargs):
                    code = ("import signal,time;from pathlib import Path;"
                            "signal.signal(signal.SIGTERM,"+
                            ("signal.SIG_IGN" if ignores_term else "signal.SIG_DFL")+
                            ");Path("+repr(str(ready))+").write_text('ready');time.sleep(10)")
                    child = real_popen([sys.executable, "-c", code], **kwargs)
                    children.append(child)
                    term, kill = child.terminate, child.kill
                    child.terminate = lambda:(events.append("terminate"), term())[-1]
                    child.kill = lambda:(events.append("kill"), kill())[-1]
                    return child
                def cancel():
                    if ready.exists(): raise KeyboardInterrupt
                def remove(path, *args, **kwargs):
                    self.assertIsNotNone(children[0].poll())
                    events.append("scratch-cleanup")
                    return real_remove(path, *args, **kwargs)
                try:
                    with mock.patch.object(checker.subprocess, "Popen", side_effect=spawn), \
                         mock.patch.object(checker.shutil, "rmtree", side_effect=remove):
                        with self.assertRaises(KeyboardInterrupt):
                            self.real_embedded_extractor(self.release/"rootfs.ext4", cancel)
                    self.assertIsNotNone(children[0].returncode)
                    self.assertEqual(events, ["terminate"]+(["kill"] if ignores_term else [])+["scratch-cleanup"])
                finally:
                    for child in children:
                        if child.poll() is None: child.kill()
                        child.wait(timeout=2)

    def test_unreaped_debugfs_is_failure_and_retains_its_scratch(self):
        scratch = self.root/"new-unreaped-synthetic-scratch"
        scratch.mkdir()
        child = mock.Mock(args=["debugfs"])
        child.poll.return_value = None
        child.communicate.side_effect = subprocess.TimeoutExpired(["debugfs"], .5)
        calls = []
        def cancel():
            calls.append(True)
            if len(calls) > 1: raise KeyboardInterrupt
        with mock.patch.object(checker.tempfile, "mkdtemp", return_value=str(scratch)), \
             mock.patch.object(checker.subprocess, "Popen", return_value=child), \
             mock.patch.object(checker.shutil, "rmtree") as remove:
            with self.assertRaisesRegex(checker.ReleaseCheckError, "cleanup could not be proven"):
                self.real_embedded_extractor(self.release/"rootfs.ext4", cancel)
            child.terminate.assert_called_once(); child.kill.assert_called_once()
            remove.assert_not_called()
        self.assertTrue(scratch.exists())

    def test_rejects_file_hash_missing_file_and_symlinks(self):
        self.manifest["files_sha256"]["vmlinux"] = "0" * 64
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "mismatch for vmlinux"):
            self.validate()
        self.manifest["files_sha256"]["vmlinux"] = self.digest(self.file_bytes["vmlinux"])
        self.write_manifest()
        self.release.chmod(0o755)
        (self.release / "bin").chmod(0o755)
        (self.release / "bin/jailer").unlink()
        (self.release / "bin").chmod(0o555)
        self.release.chmod(0o555)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "path does not exist"):
            self.validate()
        (self.release / "bin").chmod(0o755)
        self.release.chmod(0o755)
        (self.release / "bin/jailer").symlink_to(self.release / "bin/firecracker")
        (self.release / "bin").chmod(0o555)
        self.release.chmod(0o555)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "symlink path component"):
            self.validate()

    def test_rejects_mutable_bundle_entries(self):
        (self.release / "manager.py").chmod(0o644)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "must be read-only"):
            self.validate()

    def test_rejects_schema_protocol_capability_and_unsafe_manifest_paths_or_keys(self):
        self.manifest["schema"] = True
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "schema"):
            self.validate()
        self.manifest["schema"] = 1
        self.manifest["protocol"] = "legacy"
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "protocol"):
            self.validate()
        self.manifest["protocol"] = checker.PROTOCOL
        self.manifest["capabilities"] = ["runner-v1"]
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "missing required"):
            self.validate()
        self.manifest["capabilities"] = sorted(checker.REQUIRED_CAPABILITIES)
        self.manifest["operator-command"] = "do something"
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "unsupported manifest keys"):
            self.validate()
        del self.manifest["operator-command"]
        self.manifest["files_sha256"]["../outside"] = self.digest(b"unsafe")
        self.write_manifest()
        with self.assertRaisesRegex(checker.ReleaseCheckError, "exactly the required"):
            self.validate()

    def test_rejects_symlink_release_parent_and_relative_paths(self):
        link = self.root / "linked-release"
        link.symlink_to(self.release, target_is_directory=True)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "symlink path component"):
            self.validate(release_dir=link)
        with self.assertRaisesRegex(checker.ReleaseCheckError, "absolute"):
            self.validate(release_dir="relative-release")


if __name__ == "__main__":
    unittest.main()
