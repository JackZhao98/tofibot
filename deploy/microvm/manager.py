#!/usr/bin/env python3
"""One root-owned service and Unix socket per Tofi workspace/microVM.

Only the fixed guest action endpoint is exposed to the control plane. No caller
can choose a VM, host path, kernel, command on the host, or lifecycle operation.
"""
import argparse
import contextlib
import errno
import fcntl
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import select
import shutil
import signal
import socket
import socketserver
import stat
import subprocess
import sys
import threading
import time

MAX_BODY = 2 * 1024 * 1024
MAX_RESPONSE = 3 * 1024 * 1024
MAX_BLOB = 20 * 1024 * 1024
MAX_OAUTH_BODY = 64 * 1024
# A guest that cannot answer /health within this many seconds is unresponsive.
HEALTH_PROBE_SECONDS = 5
# Consecutive failed probes the manager requires before a recovery restart.
RECOVER_PROBES = 2
# Free page reporting lets the guest return freed RAM to the host continuously
# (Firecracker madvise(MADV_DONTNEED)s each reported range), so the VM's host
# RSS follows guest usage instead of its lifetime peak. The target stays 0: the
# host never takes memory the guest is using, and deflate_on_oom lets the guest
# take back any inflated pages instead of OOM-killing. Hinting is developer
# preview in Firecracker v1.17 (documented page-corruption race) and is not used.
BALLOON_STATS_INTERVAL_S = 5
# Reported ranges are buddy blocks of at least 2**order guest pages. The kernel
# default (pageblock order 9 = 2 MiB) misses most of a fragmented browser heap;
# order 5 (128 KiB) keeps reporting work small, and the guest init compacts
# memory once the browser has stopped so nearly all free RAM is reported. The
# guest init re-applies this value: Linux 6.1 overwrites the boot parameter.
PAGE_REPORTING_ORDER = 5
# Default cap on open page tabs in the guest Chrome (config browser_max_tabs,
# boot argument tofi_browser_tabs, tofi-guest --max-browser-tabs).
DEFAULT_BROWSER_MAX_TABS = 2
# (Firecracker field, exported name, divisor). Guest byte counts become MiB.
BALLOON_STATS = (("target_mib", "target_mib", 1), ("actual_mib", "actual_mib", 1),
                 ("total_memory", "guest_total_mib", 1024**2),
                 ("free_memory", "guest_free_mib", 1024**2),
                 ("available_memory", "guest_available_mib", 1024**2),
                 ("disk_caches", "guest_cache_mib", 1024**2),
                 ("oom_kill", "guest_oom_kills", 1))
# Snapshot hibernation. After desktop_idle_seconds without a Bot lease, human
# viewer/control or running guest work, the manager pauses the VM, writes a
# Full Firecracker snapshot (guest memory + device state) beside the account's
# workspace disk and ends the Firecracker process, so a hibernated computer
# holds no host RAM. The next use restores it with Chrome and its logins intact.
# Any doubt about a snapshot (missing, corrupt, other release or machine shape,
# a workspace disk changed since) discards it and cold boots instead; the
# snapshot is never the only copy of user data, the workspace disk is.
SNAPSHOT_FORMAT = 1
HIBERNATE_POLL_SECONDS = 15
# A guest that reports work in progress is asked again only after this delay.
HIBERNATE_BUSY_BACKOFF_SECONDS = 60
# Free host disk required beyond the memory file before writing a snapshot.
HIBERNATE_DISK_MARGIN_BYTES = 2 * 1024**3
SNAPSHOT_CREATE_SECONDS = 180
RESUME_READY_SECONDS = 30
# How long a guest-bound request waits for hibernation, restore or (after a
# rejected snapshot) a cold boot before it is answered as not ready.
WAKE_WAIT_SECONDS = 150
SNAPSHOT_FILES = ("vmstate", "memory")
# Root-only guest wall-clock setter (cmd/tofi-guest --clock-sync).
GUEST_CLOCK_PORT = 1053
# Manager exit (Worker restart/deploy) hibernates only VMs whose memory can be
# written well within the Worker's 60 s stop timeout.
EXIT_HIBERNATE_MAX_MIB = 8192
OAUTH_PATHS = frozenset((
    "/v1/oauth/start", "/v1/oauth/arm", "/v1/oauth/poll", "/v1/oauth/cancel",
))


def attach_immutable_image(source, target):
    """Attach a sealed release image to a jail; returns how it was attached.

    "link": a hard link (release and jail on one mount, no copy).
    "bind": a read-only bind mount of the release file over a placeholder in
    the jail. The confined Worker sees the release and its state directory as
    separate bind mounts, where link(2) fails with EXDEV; a bind mount shares
    the release inode without writing anything, so a cold start or resume no
    longer copies the 5 GiB root image. Firecracker opens it O_RDONLY
    (is_read_only), the mount itself is read-only and the file is root-owned
    0444, so neither the guest nor the jailed Firecracker can change the
    shared release. The jailer's recursive bind of its chroot carries the
    mount into the VM's own mount namespace.
    "copy": a sparse copy, only when neither is possible.
    """
    try:
        os.link(source, target)
        return "link"
    except OSError as error:
        if error.errno not in (errno.EXDEV, errno.EROFS):
            raise
    if bind_readonly(source, target):
        return "bind"
    # Copies live only in the disposable owned jail. Admission reserves the
    # full image sizes before launch; the immutable source is never changed.
    copy_sparse(source, target)
    os.chmod(target, 0o444)
    return "copy"


def _same_inode(a, b):
    return (a.st_dev, a.st_ino) == (b.st_dev, b.st_ino)


def bind_readonly(source, target):
    """Bind-mount `source` read-only over a new placeholder `target`.

    True only when `target` now resolves to the source inode on a read-only
    mount. Any failure leaves no mount and no placeholder behind.
    """
    # O_EXCL: never mount over, or remove, a file this call did not create.
    os.close(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o444))
    mounted = False
    try:
        if run("mount", "--bind", str(source), str(target), check=False).returncode != 0:
            return False
        mounted = True
        # A bind inherits the source mount's read-only flag (the Worker's
        # release volume is read-only); remount only when it did not.
        if not os.statvfs(target).f_flag & os.ST_RDONLY:
            run("mount", "-o", "remount,bind,ro", str(target), check=False)
        if not os.statvfs(target).f_flag & os.ST_RDONLY or not _same_inode(os.stat(source), os.stat(target)):
            return False
        mounted = False
        return True
    except (OSError, subprocess.SubprocessError):
        return False
    finally:
        if mounted:
            run("umount", str(target), check=False)
        with contextlib.suppress(OSError):
            if mounted or not _same_inode(os.stat(source), os.stat(target)):
                os.unlink(target)


# Linux FICLONE: share extents when source and target are on one mount.
FICLONE = 0x40049409


def copy_sparse(source, target):
    """Copy only the allocated ranges of `source`; holes stay holes.

    The 5 GiB root image is mostly holes. A dense copy wrote all of it on
    every start and resume. The jail is disposable and rebuilt after a crash,
    so the copy is not fsynced.
    """
    with open(source, "rb") as incoming, open(target, "xb") as outgoing:
        src, dst = incoming.fileno(), outgoing.fileno()
        size = os.fstat(src).st_size
        try:
            fcntl.ioctl(dst, FICLONE, src)
            return
        except OSError:
            pass
        offset = 0
        while offset < size:
            try:
                start = os.lseek(src, offset, os.SEEK_DATA)
                end = os.lseek(src, start, os.SEEK_HOLE)
            except OSError as error:
                if error.errno == errno.ENXIO:
                    break  # only a hole remains
                if error.errno not in (errno.EINVAL, errno.EOPNOTSUPP):
                    raise
                start, end = offset, size  # no hole reporting: copy the rest
            _copy_range(src, dst, start, min(end, size))
            offset = end
        os.ftruncate(dst, size)


def _copy_range(src, dst, start, end):
    position = start
    while position < end:
        count = min(end - position, 64 * 1024 * 1024)
        copied = 0
        if hasattr(os, "copy_file_range"):
            try:
                copied = os.copy_file_range(src, dst, count, position, position)
            except OSError as error:
                if error.errno not in (errno.EXDEV, errno.ENOSYS, errno.EINVAL, errno.EOPNOTSUPP):
                    raise
        if copied <= 0:
            chunk = os.pread(src, min(count, 1024 * 1024), position)
            if not chunk:
                raise OSError(errno.EIO, "source image shrank during copy")
            # All-zero chunks stay holes in the copy.
            if chunk.count(0) != len(chunk):
                os.pwrite(dst, chunk, position)
            copied = len(chunk)
        position += copied


def mounts_under(directory, mountinfo="/proc/self/mountinfo"):
    """Mount points strictly below `directory` in this mount namespace, deepest first."""
    prefix = str(directory).rstrip("/") + "/"
    points = set()
    try:
        lines = Path(mountinfo).read_text().splitlines()
    except OSError:
        return []
    for line in lines:
        fields = line.split()
        if len(fields) < 5:
            continue
        point = re.sub(r"\\([0-7]{3})", lambda m: chr(int(m.group(1), 8)), fields[4])
        if point.startswith(prefix):
            points.add(point)
    return sorted(points, key=lambda p: (-p.count("/"), p))


def remove_jail_tree(directory):
    """Unmount attached release images, then delete a disposable jail."""
    for point in mounts_under(directory):
        result = run("umount", point, check=False)
        if result.returncode != 0:
            raise RuntimeError(command_error("umount of a jail image", result))
    if directory.exists():
        shutil.rmtree(directory)


def run(*args, check=True):
    return subprocess.run(args, check=check, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True)


def command_error(command, result):
    """Return a bounded diagnostic without discarding the tool's real cause."""
    output = (result.stderr or result.stdout or "").strip().splitlines()
    detail = output[-1][:500] if output else "exit status " + str(result.returncode)
    return command + " failed: " + detail


def restore_restart_request(root):
    held = root / "resources-desired.restart-hold.json"
    if held.exists():
        os.replace(held, root / "resources-desired.json")
        fd = os.open(root, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)


def signal_process_group(process, sig):
    """Signal a Firecracker/jailer process and every child in its session."""
    if process is None:
        return
    pid = getattr(process, "pid", None)
    if pid is None:
        process.send_signal(sig)
        return
    try:
        os.killpg(pid, sig)
    except ProcessLookupError:
        pass
    except PermissionError:
        # Keep the fallback useful for tests and unusual process supervisors.
        process.send_signal(sig)


def release_name(image_dir):
    """The Guest release a computer runs: its sealed release directory name."""
    return Path(image_dir).name


def snapshot_release(state_dir):
    """(Guest release, created_at) of a computer's saved snapshot, or None.

    Read-only and path-safe: the host CLI and the Worker broker use it to see
    which release a hibernated computer would wake on. Snapshots written
    before meta.json carried "release" name it through their rootfs path.
    """
    directory = Path(state_dir) / "snapshot"
    meta_path = directory / "meta.json"
    try:
        if directory.is_symlink() or meta_path.is_symlink() or not meta_path.is_file():
            return None
        meta = json.loads(meta_path.read_text())
        release = meta.get("release")
        if not isinstance(release, str) or not release:
            release = release_name(Path(meta["identity"]["rootfs"][0]).parent)
        created = meta.get("created_at")
        return release, created if isinstance(created, str) else ""
    except (OSError, ValueError, KeyError, IndexError, TypeError, AttributeError):
        return None


class SnapshotRejected(RuntimeError):
    """A saved snapshot that must not be restored; the reason is path-free."""


def validate_config(c):
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,39}", c.get("id", "")):
        raise ValueError("invalid computer id")
    for name in ("state_dir", "image_dir", "bin_dir", "socket_dir"):
        p = Path(c[name])
        if not p.is_absolute() or ".." in p.parts or str(p) == "/":
            raise ValueError("invalid " + name)
    if not 1 <= c["slot"] <= 250:
        raise ValueError("slot must be 1..250")
    if type(c.get("memory_mib", 4096)) is not int or not 512 <= c.get("memory_mib", 4096) <= 32768:
        raise ValueError("invalid memory size")
    if type(c.get("vcpus", 2)) is not int or not 1 <= c.get("vcpus", 2) <= 32:
        raise ValueError("invalid vCPU count")
    if type(c.get("disk_gib", 8)) is not int or not 8 <= c.get("disk_gib", 8) <= 1024:
        raise ValueError("invalid workspace disk size")
    idle = c.get("desktop_idle_seconds", 900)
    if type(idle) is not int or not 0 <= idle <= 86400:
        raise ValueError("desktop_idle_seconds must be an integer from 0 to 86400")
    # Open page tabs the guest Chrome keeps; beyond it the least recently used
    # tab closes (1 GiB guests hang with about six heavy sites). 0 disables.
    tabs = c.get("browser_max_tabs", DEFAULT_BROWSER_MAX_TABS)
    if type(tabs) is not int or not 0 <= tabs <= 20:
        raise ValueError("browser_max_tabs must be an integer from 0 to 20")
    if type(c.get("memory_balloon", True)) is not bool:
        raise ValueError("memory_balloon must be a boolean")
    if type(c.get("hibernate", True)) is not bool:
        raise ValueError("hibernate must be a boolean")
    if "cgroup_parent" in c and c["cgroup_parent"] != "tofi-vms":
        raise ValueError("invalid Worker cgroup parent")
    if type(c.get("worker_private_sysctls", False)) is not bool:
        raise ValueError("invalid Worker sysctl mode")
    if c.get("worker_private_sysctls") and c.get("cgroup_parent") != "tofi-vms":
        raise ValueError("Worker sysctl mode requires private cgroup configuration")
    if "host_memory_headroom_mib" in c:
        if (not c.get("worker_private_sysctls") or c.get("cgroup_parent") != "tofi-vms"
                or type(c["host_memory_headroom_mib"]) is not int
                or not 512 <= c["host_memory_headroom_mib"] <= 1024*1024):
            raise ValueError("memory headroom requires trusted private Worker configuration")
    return c


class VM:
    def __init__(self, config):
        self.c = validate_config(config)
        self.state = "stopped"
        self.phase = "checking"
        self.error = ""
        self.lock = threading.RLock()
        self.process = None
        self.slot = self.c["slot"]
        self.uid = 61000 + self.slot
        self.netns = "tofi-fc-" + str(self.slot)
        self.host_if = "tfh" + str(self.slot)
        self.peer_if = "tfp" + str(self.slot)
        self.chain = "TOFI_FC_" + str(self.slot)
        self.link_net = f"10.246.{self.slot}"
        self.guest_net = f"10.247.{self.slot}"
        self.tap_mac = f"06:00:00:00:{self.slot:02x}:01"
        self.root = Path(self.c["state_dir"])
        self.jail = self.root / "jails" / "firecracker" / self.c["id"] / "root"
        self.vsock = self.jail / "run" / "v.sock"
        self.log = None
        # How each release image reached the jail ("link", "bind", "copy").
        self.image_attach = {}
        self.network_owned = False
        self.abort = threading.Event()
        # Snapshot files live beside the workspace disk, root-owned, never in
        # the disposable jail and never shared with another account or host.
        self.snapshot_dir = self.root / "snapshot"
        self.snapshot_owner = 0  # root; tests substitute their own UID
        # Guest-bound requests in flight and the last time one ended. The
        # ready -> hibernating transition happens under the same lock, so no
        # request can reach a guest that is being paused.
        self.activity = threading.Lock()
        self.active = 0
        self.last_activity = time.monotonic()
        self.hibernate_not_before = 0.0
        self.hibernated_at = ""
        self.last_wake = None
        applied = self.root / "resources-applied.json"
        if applied.exists():
            self.c.update(self.validate_resources(json.loads(applied.read_text())))

    @staticmethod
    def validate_resources(value):
        bounds = {"vcpus": (1, 32), "memory_mib": (512, 32768), "disk_gib": (8, 1024)}
        if not isinstance(value, dict) or set(value) != set(bounds):
            raise ValueError("provide vcpus, memory_mib and disk_gib")
        for key, (low, high) in bounds.items():
            if type(value[key]) is not int or not low <= value[key] <= high:
                raise ValueError("invalid " + key)
        return value

    def current_resources(self):
        values = {"vcpus": self.c.get("vcpus", 2), "memory_mib": self.c.get("memory_mib", 4096), "disk_gib": self.c.get("disk_gib", 8)}
        disk = self.root / "workspace.ext4"
        if disk.exists():
            values["disk_gib"] = (disk.stat().st_size + 1024**3 - 1) // 1024**3
        return values

    def host_resources(self):
        memory = {}
        for line in Path("/proc/meminfo").read_text().splitlines():
            key, value = line.split(":", 1)
            if key in ("MemTotal", "MemAvailable"):
                memory[key] = int(value.split()[0]) // 1024
        return {"cpus": os.cpu_count() or 1, "memory_total_mib": memory["MemTotal"],
                "memory_available_mib": memory["MemAvailable"],
                "disk_available_gib": shutil.disk_usage(self.root).free // 1024**3}

    def desired_resources(self):
        path = self.root / "resources-desired.json"
        return self.validate_resources(json.loads(path.read_text())) if path.exists() else self.current_resources()

    def resources(self):
        # Status must remain observable while start holds the lifecycle lock.
        current, desired, host = self.current_resources(), self.desired_resources(), self.host_resources()
        held = self.root / "resources-desired.restart-hold.json"
        if held.exists():
            desired = self.validate_resources(json.loads(held.read_text()))
        return {"scope": "workspace", "state": self.state, "error": self.error, "current": current, "desired": desired,
                    "pending": current != desired, "apply_policy": "next_vm_start", "host": host,
                    "memory": self.memory_usage(),
                    "limits": {"max_vcpus": min(32, host["cpus"]),
                               "max_memory_mib": min(32768, max(512, host["memory_total_mib"] - 2048 - 512)),
                               "max_disk_gib": min(1024, current["disk_gib"] + max(0, host["disk_available_gib"] - current["disk_gib"] - 2))}}

    def persist_resources(self, name, value):
        path = self.root / name
        temporary = path.with_suffix(".tmp")
        with temporary.open("w") as stream:
            os.chmod(temporary, 0o600)
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        fd = os.open(self.root, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def configure_resources(self, value):
        if (self.root / "resources-desired.restart-hold.json").exists():
            raise ValueError("computer recovery must finish before changing resources")
        if self.state in ("starting", "restarting"):
            raise ValueError("computer is restarting; retry after it is ready")
        with self.lock:
            value = self.validate_resources(value)
            info = self.resources()
            for key in value:
                if value[key] > info["limits"]["max_" + key]:
                    raise ValueError(key + " exceeds available host capacity")
            if value["disk_gib"] < info["current"]["disk_gib"]:
                raise ValueError("workspace disks can only grow")
            self.persist_resources("resources-desired.json", value)
            return self.resources()

    def apply_resources(self):
        desired = self.desired_resources()
        current = self.current_resources()
        host = self.host_resources()
        # The account Worker supplies the SAME reviewed host reserve used by
        # its runtime ledger; standalone managers retain the historical 2 GiB.
        headroom = self.c.get("host_memory_headroom_mib", 2048)
        if desired["vcpus"] > host["cpus"] or desired["memory_mib"] + 512 + headroom > host["memory_available_mib"]:
            raise RuntimeError("insufficient host capacity; requested resources remain pending")
        if desired == current:
            return
        if desired["disk_gib"] < current["disk_gib"]:
            raise RuntimeError("workspace disks cannot shrink")
        disk = self.root / "workspace.ext4"
        if disk.exists() and desired["disk_gib"] > current["disk_gib"]:
            # Grow a private offline copy. The original stays intact even if
            # fsck/resize fails; retain it as a rollback point after success.
            if host["disk_available_gib"] < desired["disk_gib"] + 2:
                raise RuntimeError("insufficient disk space for safe offline growth")
            temporary = self.root / "workspace-growing.ext4"
            backup = self.root / ("workspace-before-growth-" + str(time.time_ns()) + ".ext4")
            try:
                run("cp", "--sparse=always", "--reflink=auto", "--preserve=mode,ownership", str(disk), str(temporary))
                # resize2fs requires a full filesystem check before an offline
                # resize. Plain -p may only replay the journal and then exits
                # cleanly, leaving resize2fs to reject the image with
                # "Please run 'e2fsck -f ...' first."
                check = run("e2fsck", "-f", "-p", str(temporary), check=False)
                if check.returncode not in (0, 1, 2):
                    raise RuntimeError(command_error("workspace filesystem check", check) + "; original disk preserved")
                with temporary.open("r+b") as stream:
                    stream.truncate(desired["disk_gib"] * 1024**3)
                resize = run("resize2fs", str(temporary), check=False)
                if resize.returncode != 0:
                    raise RuntimeError(command_error("workspace resize", resize) + "; original disk preserved")
                with temporary.open("rb") as stream:
                    os.fsync(stream.fileno())
                os.link(disk, backup)
                os.replace(temporary, disk)
            finally:
                temporary.unlink(missing_ok=True)
        self.persist_resources("resources-applied.json", desired)
        self.c.update(desired)

    def network_up(self):
        # Names and address space are reserved by the installer, never supplied
        # through the application's socket. Network cleanup is namespace-scoped.
        if self.netns in run("ip", "netns", "list").stdout.split():
            raise RuntimeError("network namespace already exists; stop the owning service first")
        if run("ip", "link", "show", self.host_if, check=False).returncode == 0 or run("iptables", "-S", self.chain, check=False).returncode == 0:
            raise RuntimeError("reserved network names are occupied")
        run("ip", "netns", "add", self.netns)
        self.network_owned = True
        run("ip", "link", "add", self.host_if, "type", "veth", "peer", "name", self.peer_if)
        run("ip", "link", "set", self.peer_if, "netns", self.netns)
        run("ip", "addr", "add", self.link_net + ".1/30", "dev", self.host_if)
        run("ip", "link", "set", self.host_if, "up")
        def ns(*cmd):
            if self.c.get("worker_private_sysctls") and cmd[:3] == ("sysctl", "-q", "-w"):
                paths = {"net.ipv4.ip_forward=1": "/proc/sys/net/ipv4/ip_forward",
                         "net.ipv6.conf.all.disable_ipv6=1": "/proc/sys/net/ipv6/conf/all/disable_ipv6"}
                if len(cmd) != 4 or cmd[3] not in paths:
                    raise RuntimeError("unsupported Worker network sysctl")
                path = paths[cmd[3]]
                # ip netns exec owns a fresh mount namespace. Only this proc
                # file is made writable there; Docker's parent mount stays RO.
                script = f"mount --bind {path} {path}; mount -o remount,bind,rw {path}; exec sysctl -q -w {cmd[3]}"
                return run("ip", "netns", "exec", self.netns, "/bin/sh", "-ec", script)
            return run("ip", "netns", "exec", self.netns, *cmd)
        ns("ip", "link", "set", "lo", "up")
        ns("ip", "addr", "add", self.link_net + ".2/30", "dev", self.peer_if)
        ns("ip", "link", "set", self.peer_if, "up")
        ns("ip", "route", "add", "default", "via", self.link_net + ".1")
        ns("ip", "tuntap", "add", "dev", "tap0", "mode", "tap", "user", str(self.uid))
        # A fixed gateway MAC: a guest restored from a snapshot keeps its ARP
        # entry for the gateway, which must still match a recreated tap0.
        ns("ip", "link", "set", "dev", "tap0", "address", self.tap_mac)
        ns("ip", "addr", "add", self.guest_net + ".1/30", "dev", "tap0")
        ns("ip", "link", "set", "tap0", "up")
        ns("sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
        ns("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", self.guest_net + ".0/30", "-o", self.peer_if, "-j", "MASQUERADE")
        # Docker already enables host forwarding on the supported host. Refuse
        # to silently change its global network policy.
        if Path("/proc/sys/net/ipv4/ip_forward").read_text().strip() != "1":
            raise RuntimeError("host IPv4 forwarding is disabled")
        run("iptables", "-N", self.chain)
        for network in ("0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
                        "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16",
                        "224.0.0.0/4", "240.0.0.0/4"):
            run("iptables", "-A", self.chain, "-d", network, "-j", "REJECT")
        run("iptables", "-A", self.chain, "-j", "ACCEPT")
        run("iptables", "-I", "FORWARD", "1", "-i", self.host_if, "-j", self.chain)
        run("iptables", "-I", "FORWARD", "1", "-o", self.host_if, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT")
        run("iptables", "-I", "INPUT", "1", "-i", self.host_if, "-j", "REJECT")
        run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", self.link_net + ".2/32", "-j", "MASQUERADE")
        # Prevent IPv6 from bypassing the explicitly scoped IPv4 policy.
        ns("sysctl", "-q", "-w", "net.ipv6.conf.all.disable_ipv6=1")

    def network_down(self):
        if not self.network_owned:
            return
        for args in [
            ("-D", "FORWARD", "-i", self.host_if, "-j", self.chain),
            ("-D", "FORWARD", "-o", self.host_if, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"),
            ("-D", "INPUT", "-i", self.host_if, "-j", "REJECT"),
            ("-t", "nat", "-D", "POSTROUTING", "-s", self.link_net + ".2/32", "-j", "MASQUERADE"),
            ("-F", self.chain), ("-X", self.chain),
        ]:
            run("iptables", *args, check=False)
        run("ip", "link", "del", self.host_if, check=False)
        run("ip", "netns", "del", self.netns, check=False)
        if self.netns in run("ip", "netns", "list").stdout.split():
            raise RuntimeError("owned network namespace cleanup incomplete")
        link = run("ip", "link", "show", "dev", self.host_if, check=False)
        if link.returncode == 0:
            raise RuntimeError("owned network interface cleanup incomplete")
        chains = run("iptables", "-S").stdout.splitlines()
        nat = run("iptables", "-t", "nat", "-S").stdout.splitlines()
        if any(self.chain in row.split() or self.host_if in row.split() for row in chains) or any(self.link_net + ".2/32" in row.split() for row in nat):
            raise RuntimeError("owned network firewall cleanup incomplete")
        self.network_owned = False

    @staticmethod
    def connect_unix(target, timeout):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        parent_fd = None
        try:
            sock.settimeout(timeout)
            path = str(target)
            if len(os.fsencode(path)) >= 108:
                # Linux pathname sockets have a 108-byte sun_path including the
                # terminating NUL. The fixed service path can exceed that when
                # state_dir is under a long per-account root. Resolve it through
                # an already-open parent directory without changing cwd or
                # changing the configured socket name.
                parent_fd = os.open(
                    target.parent,
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                )
                path = f"/proc/self/fd/{parent_fd}/{target.name}"
            sock.connect(path)
            return sock
        except BaseException:
            sock.close()
            raise
        finally:
            if parent_fd is not None:
                os.close(parent_fd)

    def memory_usage(self):
        """Observational host/guest memory of a running VM; never raises.

        Balloon statistics come from the guest driver and are only an
        indication. Host RSS is measured from the Firecracker process itself.
        """
        result = {"balloon_enabled": bool(self.c.get("memory_balloon", True)),
                  "host_rss_mib": None, "balloon": None}
        process = self.process
        if self.state != "ready" or process is None or process.poll() is not None:
            return result
        try:
            for line in Path(f"/proc/{process.pid}/status").read_text().splitlines():
                if line.startswith("VmRSS:"):
                    result["host_rss_mib"] = int(line.split()[1]) // 1024
        except (OSError, ValueError, IndexError):
            pass
        if not result["balloon_enabled"]:
            return result
        try:
            with self.connect_unix(self.jail / "run" / "api.sock", 1) as sock:
                sock.sendall(b"GET /balloon/statistics HTTP/1.1\r\nHost: localhost\r\n"
                             b"Accept: application/json\r\nConnection: close\r\n\r\n")
                response = http.client.HTTPResponse(sock)
                response.begin()
                body = response.read(65536)
            value = json.loads(body) if response.status == 200 else None
            if isinstance(value, dict):
                stats = {}
                for source, target, divisor in BALLOON_STATS:
                    item = value.get(source)
                    if type(item) is int and item >= 0:
                        stats[target] = item // divisor
                result["balloon"] = stats
        except (OSError, ValueError, http.client.HTTPException):
            pass
        return result

    def connect(self, timeout=5):
        sock = self.connect_unix(self.vsock, timeout)
        try:
            sock.sendall(b"CONNECT 1052\n")
            ack = bytearray()
            while not ack.endswith(b"\n") and len(ack) < 80:
                part = sock.recv(1)
                if not part:
                    break
                ack.extend(part)
            if not ack.startswith(b"OK ") or not ack.endswith(b"\n"):
                raise RuntimeError("guest vsock handshake failed")
            return sock
        except BaseException:
            sock.close()
            raise

    def guest_call(self, method, path, body=b"", timeout=5):
        """One guest request; returns (status, decoded JSON body or None)."""
        if isinstance(body, str):
            body = body.encode()
        with self.connect(timeout=timeout) as sock:
            sock.sendall((f"{method} {path} HTTP/1.1\r\nHost: guest\r\nConnection: close\r\n"
                          f"Content-Type: application/json\r\nContent-Length: {len(body)}\r\n\r\n").encode() + body)
            response = http.client.HTTPResponse(sock)
            response.begin()
            data = response.read(MAX_RESPONSE)
        try:
            value = json.loads(data)
        except ValueError:
            value = None
        return response.status, value

    def guest_request(self, method, path, body=b"", timeout=5):
        status, value = self.guest_call(method, path, body, timeout)
        if status != 200:
            raise RuntimeError("guest is not ready")
        if value is None:
            raise ValueError("invalid guest response")
        return value

    def firecracker_config(self):
        boot_args = ("console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda ro init=/sbin/tofi-init "
                     f"tofi_ip={self.guest_net}.2 tofi_gateway={self.guest_net}.1 "
                     f"tofi_desktop_idle={self.guest_desktop_idle()} "
                     f"tofi_browser_tabs={self.c.get('browser_max_tabs', DEFAULT_BROWSER_MAX_TABS)}")
        config = {
            "boot-source": {"kernel_image_path": "/vmlinux", "boot_args": boot_args},
            "drives": [
                {"drive_id": "rootfs", "path_on_host": "/rootfs.ext4", "is_root_device": True, "is_read_only": True},
                {"drive_id": "workspace", "path_on_host": "/workspace.ext4", "is_root_device": False, "is_read_only": False},
            ],
            "machine-config": {"vcpu_count": self.c.get("vcpus", 2), "mem_size_mib": self.c.get("memory_mib", 4096), "smt": False},
            "network-interfaces": [{"iface_id": "eth0", "guest_mac": f"06:00:00:00:{self.slot:02x}:02", "host_dev_name": "tap0"}],
            "vsock": {"guest_cid": 3, "uds_path": "/run/v.sock"},
        }
        if self.c.get("memory_balloon", True):
            config["boot-source"]["boot_args"] += f" page_reporting.page_reporting_order={PAGE_REPORTING_ORDER}"
            config["balloon"] = {"amount_mib": 0, "deflate_on_oom": True,
                                 "stats_polling_interval_s": BALLOON_STATS_INTERVAL_S,
                                 "free_page_reporting": True}
        return config

    def hibernation_enabled(self):
        return bool(self.c.get("hibernate", True)) and self.c.get("desktop_idle_seconds", 900) > 0

    def guest_desktop_idle(self):
        # With hibernation the manager owns idle: it snapshots the VM with
        # Chrome still running. The guest's own desktop cleanup remains as a
        # later backstop for a computer that cannot hibernate (no disk room).
        idle = self.c.get("desktop_idle_seconds", 900)
        return min(2 * idle, 86400) if self.hibernation_enabled() else idle

    def prepare_jail(self):
        """Replace the disposable jail and attach the shared immutable images."""
        # Jailer creates device nodes which cannot be reused on the next
        # launch. Only its disposable jail is replaced; the workspace disk and
        # any snapshot live outside this directory.
        remove_jail_tree(self.jail.parent)
        self.jail.mkdir(parents=True, exist_ok=True)
        (self.jail / "run").mkdir(exist_ok=True)
        for item in (self.jail, self.jail / "run"):
            os.chown(item, self.uid, self.uid)
        for name in ("vmlinux", "rootfs.ext4"):
            target = self.jail / name
            if target.exists():
                target.unlink()
            # Shared immutable images are opened read-only by Firecracker.
            self.image_attach[name] = attach_immutable_image(Path(self.c["image_dir"]) / name, target)

    def attach_workspace(self):
        data = self.root / "workspace.ext4"
        os.chown(data, self.uid, self.uid)
        os.chmod(data, 0o600)
        target = self.jail / "workspace.ext4"
        if target.exists():
            target.unlink()
        os.link(data, target)
        for name in ("v.sock", "api.sock"):
            (self.jail / "run" / name).unlink(missing_ok=True)

    def jailer_command(self, config_file=True):
        cmd = [str(Path(self.c["bin_dir"]) / "jailer"), "--id", self.c["id"],
               "--exec-file", str(Path(self.c["bin_dir"]) / "firecracker"),
               "--uid", str(self.uid), "--gid", str(self.uid),
               "--chroot-base-dir", str(self.root / "jails"),
               "--netns", "/var/run/netns/" + self.netns,
               "--cgroup-version", "2", "--cgroup", f"memory.max={(self.c.get('memory_mib',4096)+512)*1024**2}",
               "--cgroup", f"cpu.max={self.c.get('vcpus',2)*100000} 100000",
               "--resource-limit", "no-file=2048",
               "--", "--api-sock", "/run/api.sock"]
        if config_file:
            # A snapshot restore must start an unconfigured Firecracker.
            cmd += ["--config-file", "/config.json"]
        if self.c.get("cgroup_parent"):
            cmd[1:1] = ["--parent-cgroup", self.c["cgroup_parent"]]
        return cmd

    def launch(self, cmd):
        self.process = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
        def drain_console(pipe):
            recent = b""
            with pipe:
                while True:
                    chunk = os.read(pipe.fileno(), 65536)
                    if not chunk:
                        return
                    recent = (recent + chunk)[-262144:]
                    (self.root / "console.log").write_bytes(recent)
        threading.Thread(target=drain_console, args=(self.process.stdout,), daemon=True).start()

    def start(self):
        with self.lock:
            if self.process and self.process.poll() is None:
                return
            if self.network_owned:
                self.stop()
            self.abort.clear()
            self.state = "starting"
            self.phase = "storage"
            self.error = ""
            storage_started = time.monotonic()
            try:
                # A cold boot changes the workspace disk under any saved guest
                # memory, so that snapshot can never be restored afterwards.
                self.discard_snapshot()
                self.apply_resources()
                self.prepare_jail()
                data = self.root / "workspace.ext4"
                if not data.exists():
                    with data.open("xb") as f:
                        f.truncate(self.c.get("disk_gib", 8) * 1024**3)
                    run("mkfs.ext4", "-q", "-F", str(data))
                else:
                    # Replay a journal after an unclean host/VM shutdown before
                    # attaching the disk. Never reformat an existing workspace.
                    check = run("e2fsck", "-p", str(data), check=False)
                    if check.returncode not in (0, 1, 2):
                        raise RuntimeError("workspace filesystem needs offline repair; existing disk preserved")
                self.attach_workspace()
                storage_seconds = round(time.monotonic() - storage_started, 2)
                self.phase = "network"
                self.network_up()
                config = self.firecracker_config()
                config_path = self.jail / "config.json"
                config_path.write_text(json.dumps(config))
                os.chown(config_path, self.uid, self.uid)
                self.phase = "booting"
                started = time.monotonic()
                self.launch(self.jailer_command(config_file=True))
                deadline = time.monotonic() + 90
                while time.monotonic() < deadline:
                    if self.abort.is_set():
                        raise RuntimeError("preparation stopped")
                    if self.process.poll() is not None:
                        raise RuntimeError("Firecracker exited; inspect the bounded console log")
                    try:
                        self.phase = "verifying"
                        self.guest_request("GET", "/health")
                        self.last_wake = {"kind": "cold_boot", "seconds": round(time.monotonic() - started, 2),
                                          "storage_seconds": storage_seconds,
                                          "image_attach": dict(self.image_attach)}
                        self.touch()
                        self.state = "ready"
                        self.phase = "ready"
                        return
                    except (OSError, ValueError, RuntimeError, http.client.HTTPException):
                        time.sleep(.25)
                raise RuntimeError("guest readiness timed out")
            except BaseException as e:
                self.error = str(e)
                self.stop()
                self.state = "error"
                raise

    # ---- activity accounting -------------------------------------------

    def touch(self):
        with self.activity:
            self.last_activity = time.monotonic()

    def begin_activity(self):
        """Count one guest-bound request; only a ready VM accepts it."""
        with self.activity:
            if self.state != "ready":
                return False
            self.active += 1
            self.last_activity = time.monotonic()
            return True

    def end_activity(self):
        with self.activity:
            self.active = max(0, self.active - 1)
            self.last_activity = time.monotonic()

    def begin_hibernation(self, now=None):
        """Atomically move an idle ready VM to hibernating; False otherwise."""
        if not self.hibernation_enabled():
            return False
        now = time.monotonic() if now is None else now
        with self.activity:
            if (self.state != "ready" or self.active or now < self.hibernate_not_before
                    or now - self.last_activity < self.c.get("desktop_idle_seconds", 900)
                    or (self.root / "resources-desired.restart-hold.json").exists()):
                return False
            if self.process is None or self.process.poll() is not None:
                return False
            self.state = "hibernating"
            self.phase = "quiescing"
            return True

    # ---- snapshot files --------------------------------------------------

    _digests = {}

    @classmethod
    def file_digest(cls, path):
        info = os.stat(path)
        key = (str(path), info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)
        digest = cls._digests.get(key)
        if digest is None:
            value = hashlib.sha256()
            with open(path, "rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    value.update(chunk)
            digest = cls._digests[key] = value.hexdigest()
        return digest

    def snapshot_identity(self):
        """Everything a restored guest depends on beside the workspace disk.

        Firecracker restores only on the same binary, kernel image, machine
        shape and host CPU; the read-only rootfs must also be the same file,
        or the guest's page cache would no longer match its root disk.
        """
        image, bins = Path(self.c["image_dir"]), Path(self.c["bin_dir"])
        rootfs = (image / "rootfs.ext4").stat()
        cpu = []
        try:
            for line in Path("/proc/cpuinfo").read_text().splitlines():
                key = line.split(":", 1)[0].strip()
                if key in ("vendor_id", "model name", "flags") and line not in cpu:
                    cpu.append(line)
                if key == "processor" and cpu:
                    break
        except OSError:
            pass
        return {"format": SNAPSHOT_FORMAT,
                "firecracker": self.file_digest(bins / "firecracker"),
                "jailer": self.file_digest(bins / "jailer"),
                "vmlinux": self.file_digest(image / "vmlinux"),
                "rootfs": [str(image / "rootfs.ext4"), rootfs.st_dev, rootfs.st_ino,
                           rootfs.st_size, rootfs.st_mtime_ns],
                "machine": self.firecracker_config(),
                "host_kernel": os.uname().release,
                "cpu": hashlib.sha256("\n".join(cpu).encode()).hexdigest()}

    def disk_fingerprint(self):
        info = (self.root / "workspace.ext4").stat()
        return [info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns]

    def sync_dir(self, path):
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def discard_snapshot(self):
        """Remove saved guest memory. The commit marker goes first."""
        directory = self.snapshot_dir
        if directory.is_symlink():
            directory.unlink()
            return
        if not directory.exists():
            return
        (directory / "meta.json").unlink(missing_ok=True)
        self.sync_dir(directory)
        for item in directory.iterdir():
            if item.is_dir() and not item.is_symlink():
                shutil.rmtree(item)
            else:
                item.unlink(missing_ok=True)
        directory.rmdir()
        self.sync_dir(self.root)
        self.hibernated_at = ""

    def snapshot_problem(self):
        """None when the saved snapshot may be restored, else the reason."""
        directory = self.snapshot_dir
        meta_path = directory / "meta.json"
        try:
            if directory.is_symlink() or not directory.is_dir():
                return "no snapshot"
            if meta_path.is_symlink() or not meta_path.is_file():
                return "no snapshot"
            meta = json.loads(meta_path.read_text())
            if not isinstance(meta, dict) or meta.get("format") != SNAPSHOT_FORMAT:
                return "unsupported snapshot format"
            if meta.get("identity") != self.snapshot_identity():
                return "snapshot belongs to another release or machine configuration"
            if self.desired_resources() != self.current_resources():
                return "resource change pending"
            if meta.get("disk") != self.disk_fingerprint():
                return "workspace disk changed since the snapshot"
            for name in SNAPSHOT_FILES:
                info = (directory / name).lstat()
                if (not stat.S_ISREG(info.st_mode) or info.st_uid != self.snapshot_owner or info.st_mode & 0o077
                        or info.st_nlink != 1 or info.st_size != meta.get("sizes", {}).get(name)):
                    return "snapshot file " + name + " is not the saved file"
            if meta["sizes"]["memory"] != self.c.get("memory_mib", 4096) * 1024**2:
                return "snapshot memory size differs"
            if self.file_digest(directory / "vmstate") != meta.get("vmstate_sha256"):
                return "snapshot state is corrupt"
            return None
        except (OSError, ValueError, KeyError, TypeError) as error:
            return "snapshot unreadable: " + type(error).__name__

    def persist_meta(self, value):
        path = self.snapshot_dir / "meta.json"
        temporary = self.snapshot_dir / "meta.json.tmp"
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        self.sync_dir(self.snapshot_dir)

    # ---- Firecracker API -------------------------------------------------

    def api_request(self, method, path, body=None, timeout=10):
        payload = json.dumps(body).encode() if body is not None else b""
        with self.connect_unix(self.jail / "run" / "api.sock", timeout) as sock:
            sock.sendall((f"{method} {path} HTTP/1.1\r\nHost: localhost\r\nAccept: application/json\r\n"
                          f"Content-Type: application/json\r\nContent-Length: {len(payload)}\r\n"
                          "Connection: close\r\n\r\n").encode() + payload)
            response = http.client.HTTPResponse(sock)
            response.begin()
            data = response.read(65536)
        if response.status not in (200, 204):
            detail = data[:300].decode("utf-8", "replace").strip()
            raise RuntimeError(f"Firecracker {method} {path} returned {response.status}: {detail}")
        return data

    def end_firecracker(self):
        """Kill this VM's Firecracker (paused or failed) and wait for it."""
        process = self.process
        if process is None:
            return
        if process.poll() is None:
            try:
                signal_process_group(process, signal.SIGKILL)
            except PermissionError:
                self.kill_vm_leaf(process)
            process.wait(timeout=15)
        self.process = None

    def kill_vm_leaf(self, process):
        """Kill this VM through its cgroup leaf when signals are refused.

        The confined Worker cannot signal the jailer UID, but it owns this VM's
        cgroup leaf. Kill only a leaf that holds this PID; cgroup.kill also
        ends a stopped (SIGSTOP) or wedged Firecracker.
        """
        if not self.c.get("worker_private_sysctls") or self.c.get("cgroup_parent") != "tofi-vms":
            raise PermissionError("cannot signal the VM process")
        leaf = Path("/sys/fs/cgroup") / "tofi-vms" / self.c["id"]
        if leaf.is_symlink() or not leaf.is_dir():
            raise RuntimeError("VM cgroup leaf unavailable")
        if str(process.pid) not in (leaf / "cgroup.procs").read_text().split():
            raise RuntimeError("VM process is outside its cgroup leaf")
        (leaf / "cgroup.kill").write_text("1")

    def quiesce_guest(self):
        """Ask the guest for in-flight work and flush its filesystems.

        Returns a list of busy reasons; an empty list means idle and synced.
        """
        body = json.dumps({"idle_seconds": self.c.get("desktop_idle_seconds", 900)})
        status, value = self.guest_call("POST", "/v1/quiesce", body, timeout=30)
        if status == 200 and isinstance(value, dict) and value.get("idle") is True:
            return []
        if status == 409 and isinstance(value, dict) and isinstance(value.get("busy"), list):
            return [str(item)[:64] for item in value["busy"][:16]] or ["busy"]
        raise RuntimeError("guest does not support hibernation")

    # ---- hibernate / restore --------------------------------------------

    def hibernate(self, wait_for_idle=True, fallback_boot=True):
        """Snapshot a VM in state "hibernating" and end its Firecracker.

        Returns True when the computer is hibernated. A busy guest or a
        failure before the VM was paused returns it to ready untouched.
        """
        with self.lock:
            if self.state != "hibernating":
                return False
            started = time.monotonic()
            paused = False
            jail_snapshot = self.jail / "snapshot"
            try:
                self.phase = "quiescing"
                busy = self.quiesce_guest()
                if busy and wait_for_idle:
                    self.hibernate_not_before = time.monotonic() + HIBERNATE_BUSY_BACKOFF_SECONDS
                    self.state, self.phase = "ready", "ready"
                    return False
                memory = self.c.get("memory_mib", 4096) * 1024**2
                if shutil.disk_usage(self.root).free < memory + HIBERNATE_DISK_MARGIN_BYTES:
                    raise RuntimeError("insufficient disk space for a snapshot")
                identity = self.snapshot_identity()
                self.discard_snapshot()
                if jail_snapshot.exists():
                    shutil.rmtree(jail_snapshot)
                jail_snapshot.mkdir(mode=0o700)
                os.chown(jail_snapshot, self.uid, self.uid)
                self.phase = "pausing"
                self.api_request("PATCH", "/vm", {"state": "Paused"})
                paused = True
                self.phase = "saving"
                self.api_request("PUT", "/snapshot/create",
                                 {"snapshot_type": "Full", "snapshot_path": "/snapshot/vmstate",
                                  "mem_file_path": "/snapshot/memory"},
                                 timeout=SNAPSHOT_CREATE_SECONDS)
                self.phase = "releasing"
                # From here the paused VM is never resumed: the snapshot owns
                # its memory, and the workspace disk stays exactly as saved.
                self.end_firecracker()
                paused = False
                self.snapshot_dir.mkdir(mode=0o700, exist_ok=True)
                os.chown(self.snapshot_dir, self.snapshot_owner, self.snapshot_owner)
                os.chmod(self.snapshot_dir, 0o700)
                sizes = {}
                for name in SNAPSHOT_FILES:
                    source = jail_snapshot / name
                    info = source.lstat()
                    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                        raise RuntimeError("Firecracker wrote an unexpected snapshot file")
                    target = self.snapshot_dir / name
                    os.replace(source, target)
                    os.chown(target, self.snapshot_owner, self.snapshot_owner)
                    os.chmod(target, 0o600)
                # Unwritten guest pages are zero; keep them as holes on disk.
                try:
                    run("fallocate", "--dig-holes", str(self.snapshot_dir / "memory"), check=False)
                except OSError:
                    pass
                for name in SNAPSHOT_FILES:
                    with (self.snapshot_dir / name).open("rb") as stream:
                        os.fsync(stream.fileno())
                    sizes[name] = (self.snapshot_dir / name).stat().st_size
                try:
                    remove_jail_tree(self.jail.parent)
                except (OSError, RuntimeError) as error:
                    # The saved snapshot is complete; prepare_jail retries the
                    # removal (and refuses to launch) before the next use.
                    print("jail cleanup deferred: " + str(error)[:200], file=sys.stderr, flush=True)
                with (self.root / "workspace.ext4").open("rb") as stream:
                    os.fsync(stream.fileno())
                created = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                self.persist_meta({"format": SNAPSHOT_FORMAT, "identity": identity,
                                   "disk": self.disk_fingerprint(), "sizes": sizes,
                                   "vmstate_sha256": self.file_digest(self.snapshot_dir / "vmstate"),
                                   "created_at": created,
                                   "release": release_name(self.c["image_dir"])})
                self.hibernated_at = created
                self.state, self.phase = "hibernated", "hibernated"
                print(f"hibernated in {time.monotonic() - started:.2f}s", file=sys.stderr, flush=True)
                return True
            except BaseException as error:
                failure = str(error)[:300]
                print("hibernation failed: " + failure, file=sys.stderr, flush=True)
                self.hibernate_not_before = time.monotonic() + 10 * HIBERNATE_BUSY_BACKOFF_SECONDS
                try:
                    self.discard_snapshot()
                    if jail_snapshot.exists():
                        shutil.rmtree(jail_snapshot)
                except OSError:
                    pass
                if self.process is not None and self.process.poll() is None:
                    try:
                        if paused:
                            self.api_request("PATCH", "/vm", {"state": "Resumed"})
                        self.state, self.phase = "ready", "ready"
                        self.touch()
                        return False
                    except (OSError, ValueError, RuntimeError, http.client.HTTPException):
                        pass
                if not fallback_boot:
                    self.state = "error"
                    return False
                # The VM is gone or cannot run again: boot it from its disk.
                self.state = "starting"
                try:
                    self.start()
                except Exception:
                    pass
                return False

    def resume(self):
        """Restore a hibernated VM; any snapshot doubt cold boots instead."""
        with self.lock:
            if self.state != "resuming":
                return
            started = time.monotonic()
            self.error = ""
            try:
                self.phase = "checking"
                problem = self.snapshot_problem()
                if problem:
                    raise SnapshotRejected(problem)
                host = self.host_resources()
                headroom = self.c.get("host_memory_headroom_mib", 2048)
                if self.c.get("memory_mib", 4096) + 512 + headroom > host["memory_available_mib"]:
                    # A cold boot would need the same memory; keep the snapshot.
                    self.state, self.phase = "hibernated", "hibernated"
                    self.error = "insufficient host memory to resume"
                    return
                self.phase = "storage"
                storage_started = time.monotonic()
                self.prepare_jail()
                self.attach_workspace()
                jail_snapshot = self.jail / "snapshot"
                jail_snapshot.mkdir(mode=0o700)
                os.chown(jail_snapshot, self.uid, self.uid)
                for name in SNAPSHOT_FILES:
                    target = jail_snapshot / name
                    os.link(self.snapshot_dir / name, target)
                    os.chown(target, self.uid, self.uid)
                # Consume the snapshot before the guest can run: once resumed it
                # writes the workspace disk and this memory may never return.
                (self.snapshot_dir / "meta.json").unlink()
                self.sync_dir(self.snapshot_dir)
                storage_seconds = round(time.monotonic() - storage_started, 2)
                self.phase = "network"
                if not self.network_owned:
                    self.network_up()
                self.phase = "restoring"
                self.launch(self.jailer_command(config_file=False))
                deadline = time.monotonic() + 10
                while True:
                    if self.process.poll() is not None:
                        raise RuntimeError("Firecracker exited before restore")
                    try:
                        self.api_request("GET", "/", timeout=1)
                        break
                    except (OSError, RuntimeError, http.client.HTTPException):
                        if time.monotonic() > deadline:
                            raise RuntimeError("Firecracker API did not start")
                        time.sleep(.02)
                self.api_request("PUT", "/snapshot/load",
                                 {"snapshot_path": "/snapshot/vmstate",
                                  "mem_backend": {"backend_type": "File", "backend_path": "/snapshot/memory"},
                                  "resume_vm": True},
                                 timeout=60)
                self.phase = "verifying"
                # KVM_CLOCK_REALTIME is absent on some hosts (nested KVM), so
                # the guest learns the elapsed hibernation from the host.
                clock_synced = self.sync_guest_clock()
                deadline = time.monotonic() + RESUME_READY_SECONDS
                while True:
                    if self.process.poll() is not None:
                        raise RuntimeError("Firecracker exited after restore")
                    try:
                        self.guest_request("GET", "/health", timeout=3)
                        break
                    except (OSError, ValueError, RuntimeError, http.client.HTTPException):
                        if time.monotonic() > deadline:
                            raise RuntimeError("restored guest did not answer")
                        time.sleep(.05)
                # The mapped memory file stays allocated until Firecracker
                # exits; only its names are removed now.
                self.discard_snapshot()
                shutil.rmtree(jail_snapshot, ignore_errors=True)
                self.last_wake = {"kind": "restore", "seconds": round(time.monotonic() - started, 2),
                                  "clock_synced": clock_synced, "storage_seconds": storage_seconds,
                                  "image_attach": dict(self.image_attach)}
                print(f"restored in {self.last_wake['seconds']}s", file=sys.stderr, flush=True)
                self.touch()
                self.state, self.phase = "ready", "ready"
            except BaseException as error:
                print("restore rejected, cold booting: " + str(error)[:300], file=sys.stderr, flush=True)
                # Only fixed reasons reach the App; host paths stay in the log.
                reason = str(error) if isinstance(error, SnapshotRejected) else "restore failed"
                try:
                    self.end_firecracker()
                except (OSError, RuntimeError, subprocess.SubprocessError):
                    pass
                self.state = "starting"
                try:
                    self.start()
                finally:
                    if self.last_wake and self.last_wake.get("kind") == "cold_boot":
                        self.last_wake["fallback_reason"] = reason

    def sync_guest_clock(self):
        """Set the restored guest's wall clock to the host's; True when set.

        The restored guest resumes with the time of the snapshot. Its root
        clock setter (guest port GUEST_CLOCK_PORT) accepts only the host.
        """
        deadline = time.monotonic() + 5
        while True:
            try:
                with self.connect_unix(self.vsock, 2) as sock:
                    sock.sendall(f"CONNECT {GUEST_CLOCK_PORT}\n".encode())
                    reply = b""
                    while not reply.endswith(b"\n") and len(reply) < 200:
                        part = sock.recv(64)
                        if not part:
                            break
                        reply += part
                    if not reply.startswith(b"OK "):
                        raise OSError("guest clock setter unavailable")
                    sock.sendall(f"{time.time_ns()}\n".encode())
                    answer = b""
                    while not answer.endswith(b"\n") and len(answer) < 200:
                        part = sock.recv(64)
                        if not part:
                            break
                        answer += part
                    if answer.strip() == b"OK":
                        return True
                    raise OSError("guest clock setter refused: " + answer.decode("utf-8", "replace").strip()[:80])
            except OSError as error:
                if time.monotonic() > deadline:
                    print("guest clock not synchronized: " + str(error)[:200], file=sys.stderr, flush=True)
                    return False
                time.sleep(.1)

    def stop(self):
        with self.lock:
            if self.process and self.process.poll() is None:
                try:
                    self.guest_request("POST", "/v1/shutdown", timeout=35)
                    self.process.wait(timeout=10)
                except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired, http.client.HTTPException):
                    try:
                        signal_process_group(self.process, signal.SIGTERM)
                        try:
                            self.process.wait(timeout=5)
                        except subprocess.TimeoutExpired:
                            signal_process_group(self.process, signal.SIGKILL)
                            self.process.wait(timeout=5)
                    except PermissionError:
                        # The confined Worker cannot signal the changed UID.
                        # Empty this VM's own verified cgroup leaf instead: an
                        # in-manager restart (watchdog recovery, Retry) has no
                        # supervisor fallback, which only runs on manager exit.
                        self.kill_vm_leaf(self.process)
                        self.process.wait(timeout=15)
            self.process = None
            if self.log:
                self.log.close()
                self.log = None
            self.network_down()
            self.state = "stopped"

    def purge_workspace(self):
        """Replace the persistent guest disk with a fresh filesystem.

        The old disk is copied to a timestamped sibling before replacement so
        an accidental or failed purge remains recoverable by the host operator.
        The immutable guest image, VM resources, and manager configuration are
        intentionally preserved.
        """
        with self.lock:
            if self.state not in ("ready", "stopped", "error", "hibernated"):
                raise RuntimeError("computer is busy; retry after it is ready")
            self.state = "purging"
            self.phase = "stopping"
            self.error = ""
            self.stop()
            # Saved guest memory describes the disk being replaced.
            self.discard_snapshot()
            disk = self.root / "workspace.ext4"
            checkpoint =self.root / ("workspace-before-purge-" + str(time.time_ns()) + ".ext4")
            temporary = self.root / ("workspace-purge-" + str(time.time_ns()) + ".ext4")
            try:
                if disk.exists():
                    self.phase = "checkpoint"
                    run("cp", "--sparse=always", "--reflink=auto", "--preserve=mode,ownership", str(disk), str(checkpoint))
                    with checkpoint.open("rb") as stream:
                        os.fsync(stream.fileno())
                self.phase = "formatting"
                with temporary.open("xb") as stream:
                    stream.truncate(self.current_resources()["disk_gib"] * 1024**3)
                run("mkfs.ext4", "-q", "-F", str(temporary))
                os.chown(temporary, self.uid, self.uid)
                os.chmod(temporary, 0o600)
                os.replace(temporary, disk)
                fd = os.open(self.root, os.O_RDONLY)
                try:
                    os.fsync(fd)
                finally:
                    os.close(fd)
                self.state = "starting"
                self.phase = "booting"
                with Path("/run/tofi-firecracker-locks/boot-admission.lock").open("a") as admission:
                    fcntl.flock(admission, fcntl.LOCK_EX)
                    self.start()
                    restore_restart_request(self.root)
                result = self.info()
                result["purge_checkpoint"] = str(checkpoint) if checkpoint.exists() else ""
                return result
            except BaseException as error:
                temporary.unlink(missing_ok=True)
                self.error = str(error)
                self.state = "error"
                raise

    def info(self):
        # Hibernation, restore, a restart (watchdog recovery, Retry, resource
        # change) and a purge end or replace Firecracker on purpose: between
        # its kill and stop() clearing the process, the exit is expected.
        if (self.process and self.process.poll() is not None
                and self.state not in ("hibernating", "resuming", "restarting", "purging")):
            self.state = "error"
            self.error = "microVM stopped unexpectedly"
        value = {"kind": "firecracker", "id": self.c["id"], "state": self.state, "phase": self.phase,
                 "workspace_root": "/workspace", "browser": "Google Chrome",
                 "error": self.error, "vcpus": self.c.get("vcpus", 2),
                 "memory_mib": self.c.get("memory_mib", 4096),
                 "disk_gib": self.current_resources()["disk_gib"],
                 "desktop_idle_seconds": self.c.get("desktop_idle_seconds", 900),
                 "hibernation": self.hibernation_enabled(),
                 "release": release_name(self.c["image_dir"])}
        if self.state == "hibernated" and self.hibernated_at:
            value["hibernated_at"] = self.hibernated_at
        if self.last_wake:
            value["last_wake"] = dict(self.last_wake)
        return value


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def json(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True

    def stream_desktop(self):
        # Fixed guest endpoint plus canonical Bot ID; no user-selected address,
        # protocol, desktop allocation, lifecycle action or host command.
        if not re.fullmatch(r"/v1/desktop/stream\?bot_id=[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(?:&cursor=hidden)?", self.path):
            return self.json(400, {"error": "invalid stream request"})
        with self.server.guest_use() as ready:
            if not ready:
                return self.json(503, {"error": "microVM is not ready"})
            # A live viewer keeps the computer awake for the whole stream.
            return self.forward_stream()

    def forward_stream(self):
        forwarded = False
        try:
            with self.server.vm.connect() as guest:
                guest.sendall(f"GET {self.path} HTTP/1.1\r\nHost: guest\r\nConnection: close\r\n\r\n".encode())
                guest.settimeout(10)
                self.connection.settimeout(10)
                deadline = time.monotonic() + 1805
                while time.monotonic() < deadline:
                    ready, _, _ = select.select([guest, self.connection], [], [], 1)
                    if self.connection in ready:
                        return
                    if guest in ready:
                        part = guest.recv(32768)
                        if not part:
                            return
                        forwarded = True
                        self.connection.sendall(part)
        except (OSError, ValueError, RuntimeError):
            if not forwarded:
                try:
                    self.json(502, {"error": "computer transport failed"})
                except OSError:
                    pass
        finally:
            self.close_connection = True

    def oauth(self, body):
        # OAuth is deliberately a fixed private-vsock bridge. The guest owns
        # the provider flow and returns only its small JSON result envelope.
        if self.path not in OAUTH_PATHS:
            return self.json(404, {"error": "not found"})
        if len(body) > MAX_OAUTH_BODY:
            return self.json(400, {"error": "invalid body"})
        try:
            value = json.loads(body)
            if not isinstance(value, dict):
                return self.json(400, {"error": "invalid request"})
            # Validate JSON before crossing the privilege boundary, while
            # preserving the guest's endpoint-specific request schema.
            body = json.dumps(value, separators=(",", ":")).encode()
            # Only starting a flow may boot a stopped computer; every OAuth
            # step wakes a hibernated one.
            with self.server.guest_use(timeout=35, start_stopped=self.path == "/v1/oauth/start") as ready:
                if not ready:
                    return self.json(503, {"error": "microVM is not ready"})
                result = self.server.vm.guest_request("POST", self.path, body, timeout=10)
            if not isinstance(result, dict):
                raise ValueError("invalid OAuth response")
            return self.json(200, result)
        except (OSError, ValueError, RuntimeError, http.client.HTTPException):
            return self.json(502, {"error": "computer transport failed"})

    def do_GET(self):
        if self.path.startswith("/v1/runner/"):
            return self.runner("GET")
        if self.path.startswith("/v1/blobs/"):
            return self.blob("GET")
        if self.path == "/v1/storage":
            # Observational only: never start a stopped VM to collect statistics.
            if self.server.vm.state != "ready":
                return self.json(503, {"error": "guest storage metrics unavailable"})
            try:
                result = self.server.vm.guest_request("GET", "/v1/storage", timeout=2)
                if not isinstance(result, dict) or set(result) != {"total_bytes", "used_bytes", "free_bytes", "available_bytes"}:
                    raise ValueError("invalid guest storage response")
                if any(type(v) is not int or v < 0 for v in result.values()):
                    raise ValueError("invalid guest storage metrics")
                if result["used_bytes"] + result["free_bytes"] != result["total_bytes"] or result["available_bytes"] > result["free_bytes"]:
                    raise ValueError("inconsistent guest storage metrics")
                return self.json(200, result)
            except (OSError, ValueError, RuntimeError, http.client.HTTPException):
                return self.json(502, {"error": "guest storage metrics unavailable"})
        if self.path == "/v1/resources":
            return self.json(200, self.server.vm.resources())
        if self.path == "/v1/health":
            # Observational only: probes the guest agent, never starts a VM.
            return self.json(200, self.server.guest_health())
        if self.path == "/v1/busy":
            # Observational only: never starts, wakes or keeps a VM awake.
            return self.json(200, self.server.activity_report())
        if self.path.startswith("/v1/desktop/stream"):
            return self.stream_desktop()
        if self.path != "/v1/info":
            return self.json(404, {"error": "not found"})
        self.json(200, self.server.vm.info())

    def do_PUT(self):
        return self.blob("PUT")

    def do_DELETE(self):
        if self.path.startswith("/v1/runner/"):
            return self.runner("DELETE")
        return self.blob("DELETE")

    def runner(self, method):
        if not re.fullmatch(r"/v1/runner/(?:v1/plugins(?:/[a-z0-9_-]{1,64}(?:/(?:tools|call|gog/(?:start|finish|status|send|check|disconnect)))?)?|mcp/[a-z0-9_-]{1,64})", self.path):
            return self.json(404, {"error": "Runner endpoint not found"})
        with self.server.guest_use() as ready:
            if not ready:
                return self.json(503, {"error": "account Runner guest not ready"})
            return self.runner_forward(method)

    def runner_forward(self, method):
        sent = False
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 <= length <= MAX_BODY or self.headers.get("Transfer-Encoding") or (method != "POST" and length):
                return self.json(413, {"error": "invalid Runner body"})
            self.connection.settimeout(180)
            body = self.rfile.read(length)
            if len(body) != length:
                raise OSError("incomplete Runner request")
            with self.server.vm.connect(timeout=180) as guest:
                headers = [f"{method} {self.path} HTTP/1.1", "Host: guest", "Connection: close", f"Content-Length: {length}"]
                # MCP 2026-07-28 binds method/name and annotated scalar params
                # in standard headers. Forward only this bounded protocol set.
                names = ["Content-Type", "Accept", "MCP-Protocol-Version", "MCP-Session-Id", "Mcp-Method", "Mcp-Name"]
                params = [name for name in self.headers if re.fullmatch(r"mcp-param-[a-z0-9_-]{1,128}", name, re.IGNORECASE)]
                if len(params) > 32:
                    raise ValueError("too many Runner parameter headers")
                names.extend(params)
                for name in names:
                    value = self.headers.get(name)
                    if value is not None:
                        if len(value)>2048 or "\r" in value or "\n" in value:
                            raise ValueError("invalid Runner header")
                        headers.append(name+": "+value)
                guest.sendall(("\r\n".join(headers)+"\r\n\r\n").encode()+body)
                # Cancellation before a tool response closes the private guest
                # socket, so the guest HTTP context can cancel its Runner call.
                ready, _, _ = select.select([guest,self.connection], [], [], 180)
                if self.connection in ready:
                    return
                if guest not in ready:
                    raise OSError("Runner response timeout")
                response = http.client.HTTPResponse(guest)
                response.begin()
                self.send_response(response.status)
                self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
                self.send_header("Connection", "close")
                self.end_headers()
                sent = True
                copied=0
                while True:
                    part=response.read1(min(65536,MAX_RESPONSE+1-copied))
                    if not part:break
                    copied+=len(part)
                    if copied>MAX_RESPONSE:raise ValueError("Runner response too large")
                    self.wfile.write(part)
        except (OSError, ValueError, RuntimeError, http.client.HTTPException):
            if not sent:self.json(502, {"error": "account Runner transport failed"})
        finally:
            self.close_connection=True

    def blob(self, method):
        sent = False
        if not re.fullmatch(r"/v1/blobs/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", self.path):
            return self.json(404, {"error": "not found"})
        with self.server.guest_use() as ready:
            if not ready:
                return self.json(503, {"error": "guest file storage not ready"})
            return self.blob_forward(method)

    def blob_forward(self, method):
        sent = False
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > MAX_BLOB or self.headers.get("Transfer-Encoding") or (method != "PUT" and length != 0):
                return self.json(413, {"error": "invalid blob body"})
            self.connection.settimeout(120)
            with self.server.vm.connect(timeout=120) as guest:
                guest.sendall((f"{method} {self.path} HTTP/1.1\r\nHost: guest\r\nConnection: close\r\n"
                               f"Content-Type: application/octet-stream\r\nContent-Length: {length}\r\n\r\n").encode())
                remaining = length
                while remaining:
                    chunk = self.rfile.read(min(65536,remaining))
                    if not chunk:
                        raise OSError("incomplete blob request")
                    guest.sendall(chunk)
                    remaining -= len(chunk)
                response = http.client.HTTPResponse(guest)
                response.begin()
                size = response.getheader("Content-Length")
                if size is not None and (int(size)<0 or int(size)>MAX_BLOB):
                    raise ValueError("invalid blob response size")
                self.send_response(response.status)
                self.send_header("Content-Type", "application/octet-stream")
                self.send_header("Connection", "close")
                if size is not None:
                    self.send_header("Content-Length", size)
                self.end_headers()
                sent = True
                self.close_connection = True
                copied = 0
                while True:
                    chunk = response.read(min(65536, MAX_BLOB+1-copied))
                    if not chunk:
                        break
                    copied += len(chunk)
                    if copied > MAX_BLOB:
                        raise ValueError("blob response too large")
                    self.wfile.write(chunk)
        except (OSError, ValueError, RuntimeError, http.client.HTTPException):
            if not sent:
                self.json(502, {"error": "guest file transport failed"})
            self.close_connection = True

    def do_POST(self):
        if self.path.startswith("/v1/runner/"):
            return self.runner("POST")
        if self.path.startswith("/v1/oauth/") and self.path not in OAUTH_PATHS:
            return self.json(404, {"error": "not found"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > MAX_BODY or self.headers.get("Transfer-Encoding"):
                return self.json(400, {"error": "invalid body"})
            self.connection.settimeout(10)
            body = self.rfile.read(length)
            if len(body) != length:
                return
        except (OSError, ValueError):
            return self.json(400, {"error": "invalid body"})
        if self.path in OAUTH_PATHS:
            return self.oauth(body)
        if self.path == "/v1/resources":
            try:
                return self.json(200, self.server.vm.configure_resources(json.loads(body)))
            except ValueError as error:
                return self.json(400, {"error": str(error)})
            except OSError:
                return self.json(500, {"error": "resource configuration could not be saved"})
        if self.path == "/v1/resources/apply":
            try:
                confirmed = json.loads(body)
            except (ValueError, TypeError):
                confirmed = None
            if confirmed != {"confirm": True} or type(confirmed.get("confirm") if isinstance(confirmed, dict) else None) is not bool:
                return self.json(400, {"error": "explicit restart confirmation required"})
            if not self.server.restart():
                return self.json(409, {"error": "computer is preparing or another maintenance operation is active"})
            return self.json(202, {"state": "restarting"})
        if self.path == "/v1/recover":
            try:
                confirmed = json.loads(body)
            except (ValueError, TypeError):
                confirmed = None
            if confirmed != {"confirm": True} or type(confirmed.get("confirm") if isinstance(confirmed, dict) else None) is not bool:
                return self.json(400, {"error": "explicit recovery confirmation required"})
            result = self.server.recover()
            if result == "responsive":
                return self.json(409, {"error": "guest is responsive; not restarting"})
            if result is None:
                return self.json(409, {"error": "computer is preparing or another maintenance operation is active"})
            return self.json(202, {"state": result})
        if self.path == "/v1/retry":
            if not self.server.prepare():
                return self.json(409, {"error": "computer is already active or preparing"})
            return self.json(202, self.server.vm.info())
        if self.path == "/v1/purge":
            try:
                confirmed = json.loads(body)
            except (ValueError, TypeError):
                confirmed = None
            if confirmed != {"confirm": True} or type(confirmed.get("confirm") if isinstance(confirmed, dict) else None) is not bool:
                return self.json(400, {"error": "explicit purge confirmation required"})
            try:
                return self.json(200, self.server.purge())
            except RuntimeError as error:
                return self.json(409, {"error": str(error)})
            except (OSError, ValueError, subprocess.SubprocessError) as error:
                return self.json(500, {"error": "workspace purge failed: " + str(error)})
        if self.path != "/v1/action":
            return self.json(404, {"error": "not found"})
        try:
            value = json.loads(body)
            if not isinstance(value, dict) or set(value) - {"bot_id", "bot_name", "run_id", "action", "args", "source", "write_identity"}:
                return self.json(400, {"error": "invalid request"})
            # The App's recovery boundary owns this separate envelope. Forward
            # it unchanged only for file writes; the Guest binds and validates
            # the actual descriptors. Never strip it or fall back to an
            # unguarded write after a Guest identity rejection.
            if "write_identity" in value and (value.get("action") != "files.write"
                                              or not isinstance(value["write_identity"], dict)):
                return self.json(400, {"error": "invalid write identity"})
        except (ValueError, TypeError):
            return self.json(400, {"error": "invalid request"})
        with self.server.guest_use() as ready:
            if not ready:
                return self.json(503, {"ok": False, "error": "microVM is not ready"})
            return self.action_forward(body)

    def action_forward(self, body):
        try:
            with self.server.vm.connect() as guest:
                guest.sendall(f"POST /v1/action HTTP/1.1\r\nHost: guest\r\nContent-Type: application/json\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n".encode() + body)
                self.connection.settimeout(None)
                guest.settimeout(None)
                deadline = time.monotonic() + 150
                size = 0
                # Propagate caller disconnect immediately. The guest's request
                # context then cancels the command and its whole process group.
                while time.monotonic() < deadline:
                    ready, _, _ = select.select([guest, self.connection], [], [], 1)
                    if self.connection in ready:
                        return
                    if guest in ready:
                        part = guest.recv(65536)
                        if not part:
                            return
                        size += len(part)
                        if size > MAX_RESPONSE:
                            return
                        self.connection.sendall(part)
        except (OSError, ValueError, RuntimeError) as e:
            # No filesystem, shell, or credential contents are returned here.
            try:
                self.json(502, {"ok": False, "error": "computer transport failed"})
            except OSError:
                pass
        finally:
            self.close_connection = True


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
    request_queue_size = 32

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.preparation_lock = threading.Lock()

    def server_bind(self):
        path = self.server_address
        if not sys.platform.startswith("linux") or len(os.fsencode(path)) < 108:
            return super().server_bind()
        # Bind the configured pathname through its pinned parent, preserving
        # account identity and process cwd even under a long per-account root.
        parent = Path(path).parent
        parent_fd = os.open(
            parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
        )
        try:
            self.socket.bind(f"/proc/self/fd/{parent_fd}/{Path(path).name}")
        finally:
            os.close(parent_fd)
        # Keep the canonical configured address; the temporary fd path stops
        # being usable once bind completes and must never become service state.

    def acquire_guest(self, timeout=WAKE_WAIT_SECONDS, start_stopped=False):
        """Count one guest-bound request once the VM is ready.

        A hibernated computer is restored (or cold booted when its snapshot is
        rejected); a request arriving while it hibernates waits for that to
        finish and then restores it. A stopped or failed computer is started
        only when start_stopped is set. Preparation is serialized by the same
        lock used by retry/restart, so concurrent requests share one VM.
        """
        deadline = time.monotonic() + timeout
        woke = False
        while True:
            if self.vm.begin_activity():
                return True
            state = self.vm.info()["state"]
            if state == "hibernated":
                if self.vm.error and woke:
                    return False  # restore refused, e.g. no host memory
                woke = self.wake() or woke
            elif state in ("stopped", "error") and start_stopped and not woke:
                self.prepare()
                woke = True
            elif state == "error" or (state not in ("hibernating", "resuming") and not woke):
                return False
            if time.monotonic() >= deadline:
                return False
            time.sleep(.05)

    @contextlib.contextmanager
    def guest_use(self, timeout=WAKE_WAIT_SECONDS, start_stopped=False):
        ready = self.acquire_guest(timeout, start_stopped)
        try:
            yield ready
        finally:
            if ready:
                self.vm.end_activity()

    def ensure_ready(self, timeout=35):
        """Return when this server's existing VM is ready (or restored)."""
        if self.acquire_guest(timeout, start_stopped=True):
            self.vm.end_activity()
            return True
        return False

    def busy(self):
        return getattr(self, "preparation", None) is not None and self.preparation.is_alive()

    def activity_report(self):
        """What a restart would interrupt now: {"state", "release", "busy": [...]}.

        Read-only for the host operator (`tofi computers upgrade`). A ready
        guest is asked through the same /v1/quiesce check hibernation uses
        (Bot run lease, human viewer or control, desktop operation, terminal
        job). The probe holds off hibernation while it runs but, unlike a
        guest request, does not count as use: it never keeps a computer awake.
        """
        vm = self.vm
        state = vm.info()["state"]
        report = {"state": state, "release": release_name(vm.c["image_dir"]), "busy": []}
        if state in ("stopped", "hibernated", "error"):
            return report
        if state != "ready" or self.busy():
            report["busy"] = ["maintenance"]
            return report
        with vm.activity:
            if vm.state != "ready":
                report["busy"] = ["maintenance"]
                return report
            requests = vm.active
            vm.active += 1
        try:
            reasons = ["guest_request"] if requests else []
            try:
                reasons += vm.quiesce_guest()
            except (OSError, ValueError, RuntimeError, http.client.HTTPException):
                report["guest"] = "unresponsive"
            report["busy"] = sorted(set(reasons))
            return report
        finally:
            with vm.activity:
                vm.active = max(0, vm.active - 1)

    def wake(self):
        """Restore a hibernated computer in the background."""
        with self.preparation_lock:
            if self.busy() or self.vm.state != "hibernated":
                return False
            self.vm.state, self.vm.phase, self.vm.error = "resuming", "checking", ""
            def work():
                try:
                    with Path("/run/tofi-firecracker-locks/boot-admission.lock").open("a") as admission:
                        fcntl.flock(admission, fcntl.LOCK_EX)
                        self.vm.resume()
                except Exception:
                    pass  # resume/start retain the concrete error for the UI.
            self.preparation = threading.Thread(target=work, daemon=True)
            self.preparation.start()
            return True

    def maybe_hibernate(self, wait_for_idle=True):
        """Start hibernating an idle ready computer; True when started."""
        with self.preparation_lock:
            if self.busy():
                return False
            lease = (self.vm.root / "ops.lock").open("a")
            try:
                fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                lease.close()
                return False
            if not self.vm.begin_hibernation():
                lease.close()
                return False
            def work():
                with lease:
                    try:
                        self.vm.hibernate(wait_for_idle=wait_for_idle)
                    except Exception as error:
                        print("hibernation error: " + str(error)[:300], file=sys.stderr, flush=True)
            self.preparation = threading.Thread(target=work, daemon=True)
            self.preparation.start()
            return True

    def idle_monitor(self, stopping):
        while not stopping.wait(HIBERNATE_POLL_SECONDS):
            try:
                self.maybe_hibernate()
            except Exception as error:
                print("idle monitor: " + str(error)[:300], file=sys.stderr, flush=True)

    def hibernate_for_exit(self):
        vm = self.vm
        if not vm.hibernation_enabled() or vm.c.get("memory_mib", 4096) > EXIT_HIBERNATE_MAX_MIB or self.busy():
            return False
        with vm.activity:
            if vm.state != "ready" or vm.process is None or vm.process.poll() is not None:
                return False
            vm.state, vm.phase = "hibernating", "quiescing"
        return vm.hibernate(wait_for_idle=False, fallback_boot=False)

    def boot(self):
        """Initial manager start: keep a valid snapshot hibernated (lazy)."""
        if self.vm.hibernation_enabled() and self.vm.snapshot_problem() is None:
            with self.vm.activity:
                self.vm.state, self.vm.phase = "hibernated", "hibernated"
                meta = self.vm.snapshot_dir / "meta.json"
                self.vm.hibernated_at = json.loads(meta.read_text()).get("created_at", "")
            return
        # A rejected snapshot can never be restored: free its disk now.
        self.vm.discard_snapshot()
        self.prepare()

    def restart(self):
        with self.preparation_lock:
            if getattr(self, "preparation", None) is not None and self.preparation.is_alive():
                return False
            if self.vm.state not in ("ready", "error", "stopped", "hibernated"):
                return False
            original = self.vm.current_resources()
            desired = self.vm.resources()["desired"]
            lease = (self.vm.root / "ops.lock").open("a")
            try:
                fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                lease.close()
                return False
            self.vm.state = "restarting"
            self.vm.phase = "stopping"
            self.vm.error = ""
            def boot():
                with Path("/run/tofi-firecracker-locks/boot-admission.lock").open("a") as admission:
                    fcntl.flock(admission, fcntl.LOCK_EX)
                    self.vm.start()
                    restore_restart_request(self.vm.root)
            def work():
                with lease:
                    try:
                        self.vm.stop()
                        boot()
                    except Exception as error:
                        failure = str(error)
                        # A failed expansion must not strand a previously usable
                        # computer. Never shrink a successfully enlarged disk.
                        try:
                            self.vm.stop()
                            fallback = dict(original)
                            fallback["disk_gib"] = max(original["disk_gib"], self.vm.current_resources()["disk_gib"])
                            self.vm.persist_resources("resources-desired.restart-hold.json", desired)
                            self.vm.persist_resources("resources-desired.json", fallback)
                            self.vm.persist_resources("resources-applied.json", fallback)
                            self.vm.c.update(fallback)
                            boot()
                            self.vm.error = "Resource change failed; previous allocation restored: " + failure
                        except Exception as recovery:
                            self.vm.error = failure + "; recovery failed: " + str(recovery)
                            self.vm.state = "error"
            self.preparation = threading.Thread(target=work, daemon=True)
            self.preparation.start()
            return True

    def guest_health(self):
        """Liveness of the guest agent over vsock; /v1/info only sees the VM process.

        The guest's static /v1/info is used rather than /health, which also
        checks disk and Chrome: a full disk is unhealthy but responsive, and a
        restart would neither fix it nor spare the work in progress.
        """
        state = self.vm.info()["state"]
        if state in ("hibernating", "hibernated"):
            # Deliberately not running: never a reason for a recovery restart.
            return {"state": state, "guest": "hibernated"}
        if state != "ready":
            return {"state": state, "guest": "not_ready"}
        try:
            self.vm.guest_request("GET", "/v1/info", timeout=HEALTH_PROBE_SECONDS)
            return {"state": state, "guest": "ok"}
        except (OSError, ValueError, RuntimeError, http.client.HTTPException) as error:
            # The class name only: no guest output crosses this boundary.
            return {"state": state, "guest": "unresponsive", "error": type(error).__name__}

    def recover(self):
        """Restart only this computer after its guest stopped answering.

        The manager re-probes the guest itself, so the control plane cannot
        restart a responsive computer through this endpoint. Pending resource
        changes follow the existing next-VM-start policy.
        """
        state = self.vm.info()["state"]
        if state in ("error", "stopped"):
            return "starting" if self.prepare() else None
        if state in ("hibernating", "hibernated"):
            return "responsive"
        if state != "ready":
            return None
        for attempt in range(RECOVER_PROBES):
            if self.guest_health()["guest"] == "ok":
                return "responsive"
        return "restarting" if self.restart() else None

    def purge(self):
        with self.preparation_lock:
            if getattr(self, "preparation", None) is not None and self.preparation.is_alive():
                raise RuntimeError("computer is preparing or another maintenance operation is active")
            lease = (self.vm.root / "ops.lock").open("a")
            try:
                fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                lease.close()
                raise RuntimeError("computer maintenance is already active")
            with lease:
                return self.vm.purge_workspace()

    def prepare(self):
        if self.vm.state == "hibernated":
            return self.wake()
        with self.preparation_lock:
            if getattr(self, "preparation", None) is not None and self.preparation.is_alive():
                return False
            if self.vm.state not in ("error", "stopped"):
                return False
            self.vm.state = "starting"
            self.vm.phase = "checking"
            def work():
                try:
                    # Serialize host admission through guest readiness, so a
                    # concurrent workspace sees already allocated guest RAM.
                    with Path("/run/tofi-firecracker-locks/boot-admission.lock").open("a") as admission:
                        fcntl.flock(admission, fcntl.LOCK_EX)
                        self.vm.start()
                        restore_restart_request(self.vm.root)
                except Exception:
                    pass  # start retains the concrete preparation error for the UI.
            self.preparation = threading.Thread(target=work, daemon=True)
            self.preparation.start()
            return True


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("manager requires its root-owned service")
    config_path = Path(args.config)
    st = config_path.stat()
    if st.st_uid != 0 or st.st_mode & 0o022 or config_path.is_symlink():
        raise SystemExit("config must be root-owned and not writable by other users")
    c = validate_config(json.loads(config_path.read_text()))
    vm = VM(c)
    vm.root.mkdir(parents=True, exist_ok=True, mode=0o700)
    lease = (vm.root / "manager.lock").open("a")
    fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
    slot_locks = Path("/run/tofi-firecracker-locks")
    slot_locks.mkdir(mode=0o700, exist_ok=True)
    slot_lease = (slot_locks / (str(c["slot"]) + ".lock")).open("a")
    fcntl.flock(slot_lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
    socket_dir = Path(c["socket_dir"])
    socket_dir.mkdir(parents=True, exist_ok=True, mode=0o750)
    os.chown(socket_dir, 0, c.get("socket_gid", 10001))
    os.chmod(socket_dir, 0o750)
    socket_path = socket_dir / "control.sock"
    socket_path.unlink(missing_ok=True)
    server = Server(str(socket_path), Handler)
    server.vm = vm
    os.chown(socket_path, 0, c.get("socket_gid", 10001))
    os.chmod(socket_path, 0o660)
    stopping = threading.Event()
    def stop(signum, frame):
        stopping.set()
        vm.abort.set()
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    threading.Thread(target=server.idle_monitor, args=(stopping,), daemon=True).start()
    try:
        server.boot()
        server.serve_forever(poll_interval=.25)
    finally:
        server.server_close()
        try:
            # A Worker restart or deploy keeps the computer's tabs and
            # logins: hibernate instead of shutting the guest down.
            server.hibernate_for_exit()
        finally:
            vm.stop()
            socket_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
