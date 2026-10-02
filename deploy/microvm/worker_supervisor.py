"""Worker-local process supervision; no Docker socket, systemd or host PIDs."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
import time

from account_capacity import AdmissionError, account_id
from account_provisioner import Broker
from worker_cgroups import JAILER_PARENT, ROOT


class ProcessSupervisor:
    def __init__(self, release, cgroup_root, stop_timeout=60, popen=None,
                 cgroup_mount_root="/sys/fs/cgroup", proc_root="/proc"):
        self.release = Path(release)
        self.cgroups = Path(cgroup_root)
        self.cgroup_mount_root = Path(cgroup_mount_root)
        self.proc_root = Path(proc_root)
        self.timeout = stop_timeout
        self.popen = popen or subprocess.Popen
        self.processes = {}
        self.quarantined = set()
        self.lock = threading.RLock()

    def ensure(self, identity, config_path):
        identity = account_id(identity)
        if Path(config_path).name != identity + ".json" or Path(config_path).is_symlink():
            raise AdmissionError("manager config identity mismatch")
        with self.lock:
            if identity in self.quarantined:
                raise AdmissionError("unresolved VM cleanup; restart not allowed")
            old = self.processes.get(identity)
            if old and old.poll() is None:
                return
            if old:
                # An unexpected manager exit may have left a VM in its cgroup.
                self.assert_vm_stopped(identity)
                old.wait()
            self.assert_vm_stopped(identity)
            proc = self.popen([sys.executable, str(self.release / "manager.py"), str(config_path)],
                              start_new_session=True)
            self.processes[identity] = proc

    def assert_vm_stopped(self, identity):
        group = self.cgroups / ("ac-" + account_id(identity))
        if group.is_symlink():
            raise AdmissionError("unexpected VM cgroup")
        events = group / "cgroup.events"
        if group.exists() and not events.is_file():
            raise AdmissionError("VM cgroup metrics unavailable; budget retained")
        if events.exists() and "populated 0" not in events.read_text().splitlines():
            raise AdmissionError("VM processes remain; computer budget retained")

    def _verified_leaf_for_kill(self, identity):
        """Return an owned leaf only when its mount and every member are verified."""
        identity = account_id(identity)
        root = self.cgroups.parent
        leaf = self.cgroups / ("ac-" + identity)
        try:
            # The Worker mount root is overlaid onto /sys/fs/cgroup. Resolve
            # both paths and compare device+inode so a substituted directory,
            # symlink, or different cgroup mount cannot receive cgroup.kill.
            if root.is_symlink() or self.cgroups.is_symlink() or leaf.is_symlink():
                raise AdmissionError("unexpected VM cgroup path; cleanup unresolved")
            root_real = root.resolve(strict=True)
            if self.cgroup_mount_root.is_symlink():
                raise AdmissionError("non-canonical cgroup mount; cleanup unresolved")
            root_stat = root.stat()
            sys_stat = self.cgroup_mount_root.stat()
            if (root_stat.st_dev, root_stat.st_ino) != (sys_stat.st_dev, sys_stat.st_ino):
                raise AdmissionError("private cgroup root does not match /sys/fs/cgroup")
            if self.cgroups.resolve(strict=True) != root_real / "tofi-vms":
                raise AdmissionError("unexpected private VM cgroup parent")
            leaf_real = leaf.resolve(strict=True)
            if leaf_real != root_real / "tofi-vms" / ("ac-" + identity):
                raise AdmissionError("unexpected VM cgroup leaf")
            # cgroup.procs is the kernel's current direct-member list. Require
            # every listed process to report this exact leaf in its own
            # namespace-relative cgroup membership before targeting the leaf.
            procs = (leaf / "cgroup.procs").read_text().split()
            if not procs:
                raise AdmissionError("VM cgroup membership unavailable; cleanup unresolved")
            expected_membership = "/tofi-vms/ac-" + identity
            for pid in procs:
                if not pid.isdecimal() or pid == "0":
                    raise AdmissionError("invalid VM cgroup membership; cleanup unresolved")
                membership = (self.proc_root / pid / "cgroup").read_text().splitlines()
                unified = [line[3:] for line in membership if line.startswith("0::")]
                if unified != [expected_membership]:
                    raise AdmissionError("VM PID is outside verified leaf; cleanup unresolved")
            events = (leaf / "cgroup.events").read_text().splitlines()
            if "populated 1" not in events:
                raise AdmissionError("VM cgroup population unavailable; cleanup unresolved")
            return leaf
        except (OSError, RuntimeError) as exc:
            raise AdmissionError("VM cgroup verification failed; cleanup unresolved") from exc

    def _kill_verified_vm_leaf(self, identity):
        leaf = self._verified_leaf_for_kill(identity)
        kill = leaf / "cgroup.kill"
        if not kill.is_file() or kill.is_symlink():
            raise AdmissionError("verified VM cgroup.kill unavailable; cleanup unresolved")
        kill.write_text("1")

    def _wait_vm_stopped(self, identity):
        deadline = time.monotonic() + 5
        while True:
            try:
                self.assert_vm_stopped(identity)
                return
            except AdmissionError:
                if time.monotonic() >= deadline:
                    raise
                time.sleep(.05)

    def stop(self, identity):
        identity = account_id(identity)
        with self.lock:
            if identity in self.quarantined:
                raise AdmissionError("unresolved VM cleanup; budget retained")
            proc = self.processes.get(identity)
            try:
                if proc and proc.poll() is None:
                    os.killpg(proc.pid, signal.SIGTERM)
                    try:
                        proc.wait(timeout=self.timeout)
                    except subprocess.TimeoutExpired:
                        # Firecracker has its own process group and UID. Kill
                        # only the verified VM leaf before forcing its manager.
                        self._kill_verified_vm_leaf(identity)
                        self._wait_vm_stopped(identity)
                        # Let the manager observe VM exit and complete its own
                        # namespace/firewall cleanup. Never kill it first.
                        try:
                            proc.wait(timeout=15)
                        except subprocess.TimeoutExpired as exc:
                            raise AdmissionError("manager cleanup did not finish; budget retained") from exc
                elif proc:
                    proc.wait(timeout=5)
                try:
                    self.assert_vm_stopped(identity)
                except AdmissionError:
                    if not proc:
                        raise
                    # A manager can exit promptly after guest shutdown or UID
                    # signal failure while Firecracker remains in its leaf.
                    # Apply the same ownership gate as the timeout fallback.
                    self._kill_verified_vm_leaf(identity)
                    self._wait_vm_stopped(identity)
                if proc and isinstance(proc.returncode, int) and proc.returncode != 0:
                    raise AdmissionError("manager cleanup failed; budget retained")
                self.processes.pop(identity, None)
            except (AdmissionError, OSError, subprocess.SubprocessError):
                self.quarantined.add(identity)
                raise

    def close(self):
        failures = []
        for identity in list(self.processes):
            try:
                self.stop(identity)
            except (AdmissionError, OSError, subprocess.SubprocessError):
                failures.append(identity)
        if failures:
            raise AdmissionError("Worker shutdown has unresolved computer cleanup")


class WorkerBroker(Broker):
    def __init__(self, config, supervisor=None, metrics=None):
        if config.get("cgroup_root") != str(ROOT / JAILER_PARENT):
            raise ValueError("fixed private Worker VM cgroup root required")
        super().__init__(config, metrics=metrics)
        self.supervisor = supervisor or ProcessSupervisor(self.release, config["cgroup_root"])

    def manager_config(self, config):
        return dict(config, cgroup_parent=JAILER_PARENT, worker_private_sysctls=True)

    def start_manager(self, identity, unit, config_path):
        self.supervisor.ensure(identity, config_path)

    def stop_manager(self, identity, unit):
        self.supervisor.stop(identity)
