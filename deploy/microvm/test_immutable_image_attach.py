import errno
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import manager

class ImmutableAttachTests(unittest.TestCase):
    def test_cross_mount_copy_preserves_source_and_seals_target(self):
        for code in (errno.EXDEV, errno.EROFS):
            with tempfile.TemporaryDirectory(dir="/tmp") as root:
                source, target = Path(root)/"source", Path(root)/"target"
                source.write_bytes(b"immutable image")
                with patch.object(manager.os, "link", side_effect=OSError(code, "fixture")):
                    manager.attach_immutable_image(source, target)
                self.assertEqual(target.read_bytes(), source.read_bytes())
                self.assertEqual(target.stat().st_mode & 0o777, 0o444)

    def test_unexpected_error_and_existing_target_preserve_data(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            source, target = Path(root)/"source", Path(root)/"target"
            source.write_bytes(b"source"); target.write_bytes(b"unrelated")
            for code in (errno.EPERM, errno.EXDEV):
                with patch.object(manager.os, "link", side_effect=OSError(code, "fixture")):
                    with self.assertRaises(OSError): manager.attach_immutable_image(source, target)
                self.assertEqual(target.read_bytes(), b"unrelated")
