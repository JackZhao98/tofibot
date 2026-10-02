#!/usr/bin/env python3
"""One root-owned service and Unix socket per Tofi workspace/microVM.

Only the fixed guest action endpoint is exposed to the control plane. No caller
can choose a VM, host path, kernel, command on the host, or lifecycle operation.
"""
import argparse
import errno
import fcntl
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
import subprocess
import sys
import threading
import time

MAX_BODY = 2 * 1024 * 1024
MAX_RESPONSE = 3 * 1024 * 1024
MAX_BLOB = 20 * 1024 * 1024
MAX_OAUTH_BODY = 64 * 1024
OAUTH_PATHS = frozenset((
    "/v1/oauth/start", "/v1/oauth/arm", "/v1/oauth/poll", "/v1/oauth/cancel",
))


def attach_immutable_image(source, target):
    """Attach a sealed release image across separate read-only bind mounts."""
    try:
        os.link(source, target)
    except OSError as error:
        if error.errno not in (errno.EXDEV, errno.EROFS):
            raise
        # Copies live only in the disposable owned jail. Admission reserves the
        # full image sizes before launch; the immutable source is never changed.
        with open(source, "rb") as incoming, open(target, "xb") as outgoing:
            shutil.copyfileobj(incoming, outgoing, 1024 * 1024)
            outgoing.flush()
            os.fsync(outgoing.fileno())
        os.chmod(target, 0o444)


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
    if "cgroup_parent" in c and c["cgroup_parent"] != "tofi-vms":
        raise ValueError("invalid Worker cgroup parent")
    if type(c.get("worker_private_sysctls", False)) is not bool:
        raise ValueError("invalid Worker sysctl mode")
    if c.get("worker_private_sysctls") and c.get("cgroup_parent") != "tofi-vms":
        raise ValueError("Worker sysctl mode requires private cgroup configuration")
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
        self.root = Path(self.c["state_dir"])
        self.jail = self.root / "jails" / "firecracker" / self.c["id"] / "root"
        self.vsock = self.jail / "run" / "v.sock"
        self.log = None
        self.network_owned = False
        self.abort = threading.Event()
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
        if desired["vcpus"] > host["cpus"] or desired["memory_mib"] + 512 + 2048 > host["memory_available_mib"]:
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

    def connect(self, timeout=5):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        parent_fd = None
        try:
            sock.settimeout(timeout)
            path = str(self.vsock)
            if len(os.fsencode(path)) >= 108:
                # Linux pathname sockets have a 108-byte sun_path including the
                # terminating NUL. The fixed service path can exceed that when
                # state_dir is under a long per-account root. Resolve it through
                # an already-open parent directory without changing cwd or
                # changing the configured socket name.
                parent_fd = os.open(
                    self.vsock.parent,
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                )
                path = f"/proc/self/fd/{parent_fd}/{self.vsock.name}"
            try:
                sock.connect(path)
            finally:
                if parent_fd is not None:
                    os.close(parent_fd)
                    parent_fd = None
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
            if parent_fd is not None:
                os.close(parent_fd)
            sock.close()
            raise

    def guest_request(self, method, path, body=b"", timeout=5):
        if isinstance(body, str):
            body = body.encode()
        with self.connect(timeout=timeout) as sock:
            sock.sendall((f"{method} {path} HTTP/1.1\r\nHost: guest\r\nConnection: close\r\n"
                          f"Content-Type: application/json\r\nContent-Length: {len(body)}\r\n\r\n").encode() + body)
            response = http.client.HTTPResponse(sock)
            response.begin()
            body = response.read(MAX_RESPONSE)
            if response.status != 200:
                raise RuntimeError("guest is not ready")
            return json.loads(body)

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
            try:
                self.apply_resources()
                # Jailer creates device nodes which cannot be reused on the
                # next launch. Only its disposable jail is replaced; the
                # workspace disk lives outside this directory.
                if self.jail.parent.exists():
                    shutil.rmtree(self.jail.parent)
                self.jail.mkdir(parents=True, exist_ok=True)
                (self.jail / "run").mkdir(exist_ok=True)
                for item in (self.jail, self.jail / "run"):
                    os.chown(item, self.uid, self.uid)
                for name in ("vmlinux", "rootfs.ext4"):
                    target = self.jail / name
                    if target.exists():
                        target.unlink()
                    # Shared immutable images are mounted read-only by Firecracker.
                    attach_immutable_image(Path(self.c["image_dir"]) / name, target)
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
                os.chown(data, self.uid, self.uid)
                os.chmod(data, 0o600)
                target = self.jail / "workspace.ext4"
                if target.exists():
                    target.unlink()
                os.link(data, target)
                for name in ("v.sock", "api.sock"):
                    (self.jail / "run" / name).unlink(missing_ok=True)
                self.phase = "network"
                self.network_up()
                config = {
                    "boot-source": {"kernel_image_path": "/vmlinux", "boot_args": f"console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda ro init=/sbin/tofi-init tofi_ip={self.guest_net}.2 tofi_gateway={self.guest_net}.1 tofi_desktop_idle={self.c.get('desktop_idle_seconds', 900)}"},
                    "drives": [
                        {"drive_id": "rootfs", "path_on_host": "/rootfs.ext4", "is_root_device": True, "is_read_only": True},
                        {"drive_id": "workspace", "path_on_host": "/workspace.ext4", "is_root_device": False, "is_read_only": False},
                    ],
                    "machine-config": {"vcpu_count": self.c.get("vcpus", 2), "mem_size_mib": self.c.get("memory_mib", 4096), "smt": False},
                    "network-interfaces": [{"iface_id": "eth0", "guest_mac": f"06:00:00:00:{self.slot:02x}:02", "host_dev_name": "tap0"}],
                    "vsock": {"guest_cid": 3, "uds_path": "/run/v.sock"},
                }
                config_path = self.jail / "config.json"
                config_path.write_text(json.dumps(config))
                os.chown(config_path, self.uid, self.uid)
                cmd = [str(Path(self.c["bin_dir"]) / "jailer"), "--id", self.c["id"],
                       "--exec-file", str(Path(self.c["bin_dir"]) / "firecracker"),
                       "--uid", str(self.uid), "--gid", str(self.uid),
                       "--chroot-base-dir", str(self.root / "jails"),
                       "--netns", "/var/run/netns/" + self.netns,
                       "--cgroup-version", "2", "--cgroup", f"memory.max={(self.c.get('memory_mib',4096)+512)*1024**2}",
                       "--cgroup", f"cpu.max={self.c.get('vcpus',2)*100000} 100000",
                       "--resource-limit", "no-file=2048",
                       "--", "--api-sock", "/run/api.sock", "--config-file", "/config.json"]
                if self.c.get("cgroup_parent"):
                    cmd[1:1] = ["--parent-cgroup", self.c["cgroup_parent"]]
                self.phase = "booting"
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
                deadline = time.monotonic() + 90
                while time.monotonic() < deadline:
                    if self.abort.is_set():
                        raise RuntimeError("preparation stopped")
                    if self.process.poll() is not None:
                        raise RuntimeError("Firecracker exited; inspect the bounded console log")
                    try:
                        self.phase = "verifying"
                        self.guest_request("GET", "/health")
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
                        if not self.c.get("worker_private_sysctls") or self.c.get("cgroup_parent") != "tofi-vms":
                            raise
                        # The confined Worker cannot signal the changed UID.
                        # Its supervisor verifies and empties only this VM leaf.
                        # Stay alive so its bounded fallback can release wait()
                        # and this manager can clean its own network afterward.
                        self.process.wait(timeout=75)
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
            if self.state not in ("ready", "stopped", "error"):
                raise RuntimeError("computer is busy; retry after it is ready")
            self.state = "purging"
            self.phase = "stopping"
            self.error = ""
            self.stop()
            disk = self.root / "workspace.ext4"
            checkpoint = self.root / ("workspace-before-purge-" + str(time.time_ns()) + ".ext4")
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
        if self.process and self.process.poll() is not None:
            self.state = "error"
            self.error = "microVM stopped unexpectedly"
        return {"kind": "firecracker", "id": self.c["id"], "state": self.state, "phase": self.phase,
                "workspace_root": "/workspace", "browser": "Google Chrome",
                "error": self.error, "vcpus": self.c.get("vcpus", 2),
                "memory_mib": self.c.get("memory_mib", 4096),
                "disk_gib": self.current_resources()["disk_gib"],
                "desktop_idle_seconds": self.c.get("desktop_idle_seconds", 900)}


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
        if self.server.vm.info()["state"] != "ready":
            return self.json(503, {"error": "microVM is not ready"})
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
            if self.path == "/v1/oauth/start":
                if not self.server.ensure_ready():
                    return self.json(503, {"error": "microVM is not ready"})
            elif self.server.vm.info()["state"] != "ready":
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
        if self.server.vm.state != "ready":
            return self.json(503, {"error": "account Runner guest not ready"})
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
        if self.server.vm.state != "ready":
            return self.json(503, {"error": "guest file storage not ready"})
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
            if not isinstance(value, dict) or set(value) - {"bot_id", "bot_name", "run_id", "action", "args", "source"}:
                return self.json(400, {"error": "invalid request"})
            if self.server.vm.info()["state"] != "ready":
                return self.json(503, {"ok": False, "error": "microVM is not ready"})
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

    def ensure_ready(self, timeout=35):
        """Return when this server's existing VM is ready.

        Preparation is serialized by the same lock used by retry/restart, so
        concurrent OAuth requests wait for one VM rather than creating one.
        """
        if self.vm.info()["state"] == "ready":
            return True
        if self.vm.state in ("stopped", "error"):
            self.prepare()
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            state = self.vm.info()["state"]
            if state == "ready":
                return True
            if state == "error":
                return False
            time.sleep(.05)
        return False

    def restart(self):
        with self.preparation_lock:
            if getattr(self, "preparation", None) is not None and self.preparation.is_alive():
                return False
            if self.vm.state not in ("ready", "error", "stopped"):
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
    try:
        server.prepare()
        server.serve_forever(poll_interval=.25)
    finally:
        server.server_close()
        vm.stop()
        socket_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
