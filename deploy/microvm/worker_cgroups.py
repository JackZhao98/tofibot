"""Initialize only a Docker-private cgroup v2 subtree before Worker threads start.

Requires a separately approved scoped mount policy; never changes host delegation.
An initialization failure is fatal to Worker startup, not permission to fall back.
"""
import os
from pathlib import Path
import subprocess

from account_capacity import AdmissionError

ROOT = Path("/run/tofi-worker/cgroup")
JAILER_PARENT = "tofi-vms"


class PrivateCgroups:
    def __init__(self, run=None):
        self.run = run or (lambda argv: subprocess.run(argv, check=True, capture_output=True))
        self.mounted = False
        self.exposed = False

    def read(self, path):
        return Path(path).read_text().strip()

    def write(self, path, value):
        Path(path).write_text(value)

    def inode(self, path):
        return Path(path).stat().st_ino

    def mkdir(self, path):
        Path(path).mkdir(exist_ok=True)

    def initialize(self):
        if not Path("/.dockerenv").is_file() or self.read("/proc/self/cgroup") != "0::/":
            raise AdmissionError("private Docker cgroup namespace required")
        for path in (ROOT.parent, ROOT):
            if path.is_symlink():
                raise AdmissionError("unexpected Worker cgroup path")
            self.mkdir(path)
        # Docker's preexisting RO mount already reflects its private namespace.
        # Match its inode before writing anything through a fresh writable view.
        expected = self.inode("/sys/fs/cgroup")
        self.run(["/bin/mount", "-t", "cgroup2", "-o",
                  "rw,nosuid,nodev,noexec,silent", "none", str(ROOT)])
        self.mounted = True
        if self.inode(ROOT) != expected:
            raise AdmissionError("private cgroup root differs from Docker view")
        if not {"cpu", "memory"} <= set(self.read(ROOT / "cgroup.controllers").split()):
            raise AdmissionError("cpu/memory cgroup delegation unavailable")
        if set(self.read(ROOT / "cgroup.procs").split()) != {str(os.getpid())}:
            raise AdmissionError("Worker cgroup initialization must precede other processes")
        managers = ROOT / "managers"
        self.mkdir(managers)
        self.write(managers / "cgroup.procs", str(os.getpid()))
        if self.read(ROOT / "cgroup.procs"):
            raise AdmissionError("Worker root has unexpected processes")
        self.write(ROOT / "cgroup.subtree_control", "+cpu +memory")
        vms = ROOT / JAILER_PARENT
        self.mkdir(vms)
        self.write(vms / "cgroup.subtree_control", "+cpu +memory")
        for path in (ROOT, vms):
            if not {"cpu", "memory"} <= set(self.read(path / "cgroup.subtree_control").split()):
                raise AdmissionError("required cgroup controllers did not enable")
        return vms

    def expose_for_jailer(self):
        # Jailer discovers unified cgroups via /proc/mounts. Overlay Docker's RO
        # path using only the verified private view, within Worker mount namespace.
        # This extra bind rule is NOT covered by the earlier cgroup-only approval.
        if self.inode(ROOT) != self.inode("/sys/fs/cgroup"):
            raise AdmissionError("unverified private cgroup bind source")
        self.run(["/bin/mount", "--bind", str(ROOT), "/sys/fs/cgroup"])
        self.exposed = True
        if self.inode(ROOT) != self.inode("/sys/fs/cgroup"):
            raise AdmissionError("jailer cgroup exposure mismatch")
