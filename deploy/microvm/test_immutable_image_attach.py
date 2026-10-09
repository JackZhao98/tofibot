import errno
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import manager

REAL_LINK = os.link


def completed(code=0):
    return subprocess.CompletedProcess([], code, "", "")


class FakeMounts:
    """Models mount(8)/umount(8): a bind makes the target the source inode."""

    def __init__(self, bind_ok=True, readonly=True, remount_ok=True):
        self.bind_ok, self.readonly, self.remount_ok = bind_ok, readonly, remount_ok
        self.calls = []

    def run(self, *args, check=True):
        self.calls.append(args)
        if args[:2] == ("mount", "--bind"):
            if not self.bind_ok:
                return completed(32)
            source, target = args[2], args[3]
            os.unlink(target)
            REAL_LINK(source, target)
            return completed()
        if args[:3] == ("mount", "-o", "remount,bind,ro"):
            if self.remount_ok:
                self.readonly = True
                return completed()
            return completed(32)
        if args[0] == "umount":
            os.unlink(args[1])
            Path(args[1]).touch()
            return completed()
        raise AssertionError(args)

    def statvfs(self, path):
        class Result:
            f_flag = os.ST_RDONLY if self.readonly else 0
        return Result()


class ImmutableAttachTests(unittest.TestCase):
    def test_cross_mount_copy_preserves_source_and_seals_target(self):
        for code in (errno.EXDEV, errno.EROFS):
            with tempfile.TemporaryDirectory(dir="/tmp") as root:
                source, target = Path(root)/"source", Path(root)/"target"
                source.write_bytes(b"immutable image")
                with patch.object(manager.os, "link", side_effect=OSError(code, "fixture")), \
                        patch.object(manager, "bind_readonly", return_value=False):
                    self.assertEqual(manager.attach_immutable_image(source, target), "copy")
                self.assertEqual(target.read_bytes(), source.read_bytes())
                self.assertEqual(target.stat().st_mode & 0o777, 0o444)

    def test_unexpected_error_and_existing_target_preserve_data(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"source"); target.write_bytes(b"unrelated")
            fake = FakeMounts()
            for code in (errno.EPERM, errno.EXDEV):
                with patch.object(manager.os, "link", side_effect=OSError(code, "fixture")), \
                        patch.object(manager, "run", side_effect=fake.run):
                    with self.assertRaises(OSError): manager.attach_immutable_image(source, target)
                self.assertEqual(target.read_bytes(), b"unrelated")
            # An existing target is never mounted over.
            self.assertEqual(fake.calls, [])

    def test_same_mount_hard_links(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"image")
            self.assertEqual(manager.attach_immutable_image(source, target), "link")
            self.assertEqual(source.stat().st_ino, target.stat().st_ino)

    def test_cross_mount_binds_the_release_inode_read_only_without_copying(self):
        # rc.5 acceptance: EXDEV fell back to a dense 5 GiB copy plus fsync on
        # every start and resume (~7.6 s). A read-only bind writes nothing.
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"image")
            fake = FakeMounts(readonly=True)
            with patch.object(manager.os, "link", side_effect=OSError(errno.EXDEV, "fixture")), \
                    patch.object(manager, "run", side_effect=fake.run), \
                    patch.object(manager.os, "statvfs", side_effect=fake.statvfs), \
                    patch.object(manager, "copy_sparse", side_effect=AssertionError("copied")):
                self.assertEqual(manager.attach_immutable_image(source, target), "bind")
            self.assertEqual(fake.calls, [("mount", "--bind", str(source), str(target))])
            self.assertEqual(source.stat().st_ino, target.stat().st_ino)

    def test_writable_bind_is_remounted_read_only_or_abandoned(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"image")
            fake = FakeMounts(readonly=False, remount_ok=True)
            with patch.object(manager, "run", side_effect=fake.run), \
                    patch.object(manager.os, "statvfs", side_effect=fake.statvfs):
                self.assertTrue(manager.bind_readonly(source, target))
            self.assertIn(("mount", "-o", "remount,bind,ro", str(target)), fake.calls)
            target.unlink()
            fake = FakeMounts(readonly=False, remount_ok=False)
            with patch.object(manager, "run", side_effect=fake.run), \
                    patch.object(manager.os, "statvfs", side_effect=fake.statvfs):
                self.assertFalse(manager.bind_readonly(source, target))
            self.assertEqual(fake.calls[-1], ("umount", str(target)))
            self.assertFalse(target.exists(), "no writable mount or placeholder is left")
            self.assertEqual(source.read_bytes(), b"image")

    def test_failed_bind_removes_its_placeholder_and_copies(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"image")
            fake = FakeMounts(bind_ok=False)
            with patch.object(manager.os, "link", side_effect=OSError(errno.EXDEV, "fixture")), \
                    patch.object(manager, "run", side_effect=fake.run):
                self.assertEqual(manager.attach_immutable_image(source, target), "copy")
            self.assertEqual(target.read_bytes(), b"image")
            self.assertNotEqual(source.stat().st_ino, target.stat().st_ino)

    def test_sparse_copy_keeps_holes_and_content(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            size = 256 * 1024 * 1024
            with source.open("wb") as stream:
                stream.truncate(size)
                stream.seek(4096); stream.write(b"head" * 1024)
                stream.seek(128 * 1024 * 1024); stream.write(b"middle" * 4096)
                stream.seek(size - 8192); stream.write(b"t" * 8192)
            manager.copy_sparse(source, target)
            self.assertEqual(target.stat().st_size, size)
            with source.open("rb") as a, target.open("rb") as b:
                for offset in (0, 4096, 128 * 1024 * 1024, size - 8192):
                    a.seek(offset); b.seek(offset)
                    self.assertEqual(a.read(65536), b.read(65536))
            # A dense copy would allocate all 256 MiB.
            self.assertLess(target.stat().st_blocks * 512, 32 * 1024 * 1024)

    def test_sparse_copy_without_hole_reporting_still_skips_zero_chunks(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            data = b"\0" * (3 * 1024 * 1024) + b"x" * 100
            source.write_bytes(data)
            real_lseek = os.lseek
            def lseek(fd, pos, how):
                if how in (os.SEEK_DATA, os.SEEK_HOLE):
                    raise OSError(errno.EINVAL, "fixture")
                return real_lseek(fd, pos, how)
            with patch.object(manager.os, "lseek", side_effect=lseek), \
                    patch.object(manager.fcntl, "ioctl", side_effect=OSError(errno.EXDEV, "fixture")):
                manager.copy_sparse(source, target)
            self.assertEqual(target.read_bytes(), data)


class JailMountTests(unittest.TestCase):
    def test_mountinfo_points_below_the_jail_only(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            info = Path(root)/"mountinfo"
            info.write_text(
                "1 0 8:1 / / rw - ext4 /dev/sda1 rw\n"
                "2 1 8:1 /guest/v/rootfs.ext4 /s/jails/firecracker/ac-1/root/rootfs.ext4 ro - ext4 /dev/sda1 rw\n"
                "3 1 8:1 /guest/v/vmlinux /s/jails/firecracker/ac-1/root/vmlinux ro - ext4 /dev/sda1 rw\n"
                "4 1 8:1 /x /s/jails/firecracker/ac-10/root/vmlinux ro - ext4 /dev/sda1 rw\n"
                "5 1 8:1 /x /s/jails/firecracker/ac-1/root/with\\040space ro - ext4 /dev/sda1 rw\n")
            self.assertEqual(manager.mounts_under(Path("/s/jails/firecracker/ac-1"), info),
                             ["/s/jails/firecracker/ac-1/root/rootfs.ext4",
                              "/s/jails/firecracker/ac-1/root/vmlinux",
                              "/s/jails/firecracker/ac-1/root/with space"])

    def test_jail_is_removed_only_after_its_image_mounts(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            jail = Path(root)/"ac-1"
            (jail/"root").mkdir(parents=True)
            point = str(jail/"root"/"rootfs.ext4")
            calls = []
            with patch.object(manager, "mounts_under", return_value=[point]), \
                    patch.object(manager, "run", side_effect=lambda *a, check=True: calls.append(a) or completed(32)):
                with self.assertRaises(RuntimeError):
                    manager.remove_jail_tree(jail)
            self.assertTrue(jail.exists(), "a jail with a live mount is never deleted")
            with patch.object(manager, "mounts_under", return_value=[point]), \
                    patch.object(manager, "run", side_effect=lambda *a, check=True: calls.append(a) or completed()):
                manager.remove_jail_tree(jail)
            self.assertEqual(calls[-1], ("umount", point))
            self.assertFalse(jail.exists())


if __name__ == "__main__":
    unittest.main()
