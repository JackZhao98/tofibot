import os
from pathlib import Path
import sys
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).parent))
from account_capacity import AdmissionError
from worker_cgroups import PrivateCgroups, ROOT, JAILER_PARENT


class Fixture(PrivateCgroups):
    def __init__(self):
        self.run = Mock()
        self.values = {
            "/proc/self/cgroup": "0::/",
            str(ROOT / "cgroup.controllers"): "cpu memory",
            str(ROOT / "cgroup.procs"): str(os.getpid()),
        }
        self.writes = []
        self.created = []
        self.same_root = True

    def read(self, path):
        return self.values.get(str(path), "")

    def write(self, path, value):
        self.writes.append((str(path), value))
        self.values[str(path)] = value.replace("+", "")
        if Path(path) == ROOT / "managers/cgroup.procs":
            self.values[str(ROOT / "cgroup.procs")] = ""

    def inode(self, path):
        return 100 if self.same_root or str(path) == "/sys/fs/cgroup" else 200

    def mkdir(self, path):
        self.created.append(Path(path))


class CgroupTests(unittest.TestCase):
    def setUp(self):
        self.fixture = Fixture()
        self.marker = patch.object(Path, "is_file", return_value=True)
        self.marker.start()

    def tearDown(self):
        self.marker.stop()

    def test_private_initialization_moves_process_before_controllers(self):
        self.assertEqual(self.fixture.initialize(), ROOT / JAILER_PARENT)
        self.fixture.run.assert_called_once_with([
            "/bin/mount", "-t", "cgroup2", "-o",
            "rw,nosuid,nodev,noexec,silent", "none", str(ROOT)])
        self.assertEqual(self.fixture.writes[0],
                         (str(ROOT / "managers/cgroup.procs"), str(os.getpid())))
        self.assertEqual(self.fixture.writes[1],
                         (str(ROOT / "cgroup.subtree_control"), "+cpu +memory"))

    def test_host_membership_or_wrong_inode_prevents_writes(self):
        self.fixture.values["/proc/self/cgroup"] = "0::/system.slice/docker.scope"
        with self.assertRaises(AdmissionError): self.fixture.initialize()
        self.fixture.run.assert_not_called()
        self.fixture.values["/proc/self/cgroup"] = "0::/"
        self.fixture.same_root = False
        with self.assertRaises(AdmissionError): self.fixture.initialize()
        self.assertFalse(self.fixture.writes)

    def test_missing_delegation_or_foreign_process_fails_closed(self):
        self.fixture.values[str(ROOT / "cgroup.controllers")] = "cpu"
        with self.assertRaises(AdmissionError): self.fixture.initialize()
        self.assertFalse(self.fixture.writes)
        self.fixture.values[str(ROOT / "cgroup.controllers")] = "cpu memory"
        self.fixture.values[str(ROOT / "cgroup.procs")] += " 98765"
        with self.assertRaises(AdmissionError): self.fixture.initialize()
        self.assertFalse(self.fixture.writes)

    def test_policy_denial_never_falls_back_to_host_bind(self):
        self.fixture.run.side_effect = PermissionError("mount denied")
        with self.assertRaises(PermissionError): self.fixture.initialize()
        self.assertEqual(self.fixture.run.call_count, 1)
        self.assertFalse(self.fixture.writes)

    def test_jailer_exposure_requires_verified_root(self):
        self.fixture.same_root = False
        with self.assertRaises(AdmissionError): self.fixture.expose_for_jailer()
        self.fixture.run.assert_not_called()
        self.fixture.same_root = True
        self.fixture.expose_for_jailer()
        self.fixture.run.assert_called_once_with([
            "/bin/mount", "--bind", str(ROOT), "/sys/fs/cgroup"])


if __name__ == "__main__": unittest.main()
