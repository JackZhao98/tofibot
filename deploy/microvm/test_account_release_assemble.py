import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock

HERE = Path(__file__).parent
spec = importlib.util.spec_from_file_location("account_release_assemble", HERE / "account_release_assemble.py")
assembler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assembler)


class AccountReleaseAssembleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=HERE)
        self.root = Path(self.temp.name)
        self.source = self.root / "source"
        (self.source / "bin").mkdir(parents=True)
        self.guest = self.root / "tofi-guest"
        self.guest.write_bytes(b"synthetic guest executable")
        self.contents = {
            "manager.py": b"# synthetic manager\n",
            "rootfs.ext4": b"synthetic rootfs, not ext4",
            "vmlinux": b"synthetic kernel",
            "bin/firecracker": b"synthetic firecracker",
            "bin/jailer": b"synthetic jailer",
        }
        for name, value in self.contents.items():
            (self.source / name).write_bytes(value)
        self.digest = hashlib.sha256(self.guest.read_bytes()).hexdigest()
        self.debugfs_fixture = mock.patch.object(
            assembler.checker, "_embedded_guest_sha256", return_value=self.digest)
        self.debugfs_fixture.start()
        self.destination = self.root / "immutable-release"

    def tearDown(self):
        self.debugfs_fixture.stop()
        self.temp.cleanup()

    def test_sparse_copy_preserves_leading_trailing_holes_and_mixed_data(self):
        source = self.root / "sparse-source"
        destination = self.root / "sparse-copy"
        size = 6 * 1024 * 1024 + 173
        with source.open("wb") as stream:
            stream.seek(1024 * 1024 + 7)
            stream.write(b"mixed-data-with-zero\x00inside")
            stream.truncate(size)
        before = source.stat()
        expected = source.read_bytes()
        digest = assembler._copy_regular(source, destination, 0o444)
        self.assertEqual(destination.read_bytes(), expected)
        self.assertEqual(destination.stat().st_size, size)
        self.assertEqual(digest, hashlib.sha256(expected).hexdigest())
        self.assertEqual(destination.stat().st_mode & 0o777, 0o444)
        after = source.stat()
        self.assertEqual((before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns),
                         (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns))
        # Assert allocation only if this filesystem exposes and supports holes.
        if hasattr(before, "st_blocks") and before.st_blocks * 512 < size:
            self.assertLess(destination.stat().st_blocks * 512, size // 2)
        with self.assertRaises(FileExistsError):
            assembler._copy_regular(source, destination, 0o444)
        self.assertEqual(destination.read_bytes(), expected)

    def test_sparse_copy_still_rejects_changed_source_identity(self):
        source = self.root / "source-change"
        destination = self.root / "copy-change"
        source.write_bytes(bytes(1024 * 1024 + 7))
        original_fsync = assembler.os.fsync
        def change_source_on_fsync(fd):
            original_fsync(fd)
            with source.open("ab") as stream:
                stream.write(b"changed")
        with mock.patch.object(assembler.os, "fsync", change_source_on_fsync):
            with self.assertRaisesRegex(assembler.checker.ReleaseCheckError, "source changed"):
                assembler._copy_regular(source, destination, 0o444)

    def test_assembles_pinned_immutable_bundle_from_fixed_files(self):
        result = assembler.assemble_release(self.source, self.destination, guest_binary=self.guest)
        self.assertEqual(result["guest_binary_sha256"], self.digest)
        self.assertTrue(result["immutable"])
        self.assertEqual(set(result["files_sha256"]), set(assembler.checker.FILES))
        self.assertEqual(set(p.relative_to(self.destination).as_posix() for p in self.destination.rglob("*") if p.is_file()),
                         set(assembler.checker.FILES) | {"account-release.json"})
        for name in assembler.checker.FILES:
            mode = (self.destination / name).stat().st_mode
            self.assertFalse(mode & 0o222, name)
        self.assertFalse((self.destination / "bin").stat().st_mode & 0o222)
        manifest_mode = (self.destination / "account-release.json").stat().st_mode
        self.assertFalse(manifest_mode & 0o222)
        self.assertEqual(
            assembler.checker._sha256(self.destination / "account-release.json"),
            result["manifest_sha256"],
        )

    def test_refuses_existing_destinations_and_guest_provenance_mismatch(self):
        self.destination.mkdir()
        with self.assertRaisesRegex(assembler.checker.ReleaseCheckError, "must not already exist"):
            assembler.assemble_release(self.source, self.destination)
        self.destination.rmdir()
        self.guest.write_bytes(b"not the embedded guest")
        with self.assertRaisesRegex(assembler.checker.ReleaseCheckError, "does not match binary embedded"):
            assembler.assemble_release(self.source, self.destination, guest_binary=self.guest)
        self.assertFalse(self.destination.exists())

    def test_refuses_symlinked_source_artifacts(self):
        target = self.root / "outside"
        target.write_bytes(b"outside artifact")
        (self.source / "vmlinux").unlink()
        (self.source / "vmlinux").symlink_to(target)
        with self.assertRaisesRegex(assembler.checker.ReleaseCheckError, "symlink"):
            assembler.assemble_release(self.source, self.destination)
        self.assertFalse(self.destination.exists())


if __name__ == "__main__":
    unittest.main()
