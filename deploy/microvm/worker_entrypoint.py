"""Runnable foreground entrypoint for the isolated account-computer Worker."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import sys

from account_release_check import validate_release, SHA256
from account_capacity import AdmissionError
from account_provisioner import Handler, Server, remove_stale_socket
from worker_cgroups import JAILER_PARENT, PrivateCgroups, ROOT
from worker_supervisor import WorkerBroker


REQUIRED = {
    "release_dir", "state_root", "config_root", "unit_root", "socket_root",
    "ledger_root", "broker_socket", "socket_gid", "app_uid", "headroom_bytes",
    "warning_bytes", "reserved_slots", "vcpus", "memory_mib",
    "runtime_vcpu_budget", "runtime_memory_mib_budget", "host_memory_headroom_mib",
    "cgroup_root",
    "external_reserved_bytes", "per_account_internal_reserved_bytes",
    "external_disks", "expected_guest_sha256", "release_manifest_sha256",
}

OPTIONAL = {"account_resource_overrides", "runtime_memory_overcommit_percent"}


def validate_config(config):
    if not isinstance(config, dict) or not REQUIRED <= set(config) or set(config) - REQUIRED - OPTIONAL:
        raise ValueError("Worker configuration fields do not match the supported schema")
    overcommit = config.get("runtime_memory_overcommit_percent", 100)
    if type(overcommit) is not int or not 100 <= overcommit <= 200:
        raise ValueError("runtime_memory_overcommit_percent must be an integer from 100 to 200")
    overrides = config.get("account_resource_overrides", {})
    if not isinstance(overrides, dict):
        raise ValueError("account resource overrides must be a mapping")
    from account_capacity import account_id
    for identity, values in overrides.items():
        account_id(identity)
        if not isinstance(values, dict) or set(values) != {"vcpus", "memory_mib"}:
            raise ValueError("invalid account resource override")
        if any(type(values[k]) is not int or not lo <= values[k] <= hi
               for k, lo, hi in (("vcpus", 1, 32), ("memory_mib", 512, 32768))):
            raise ValueError("invalid account resource override bounds")
    numeric = {"socket_gid", "app_uid", "headroom_bytes", "warning_bytes",
               "vcpus", "memory_mib", "runtime_vcpu_budget", "runtime_memory_mib_budget",
               "host_memory_headroom_mib", "external_reserved_bytes",
               "per_account_internal_reserved_bytes"}
    for name in numeric:
        if type(config[name]) is not int or config[name] < 0:
            raise ValueError("Worker numeric configuration is invalid")
    if any(config[name] <= 0 for name in ("socket_gid", "app_uid", "vcpus", "memory_mib",
                                           "runtime_memory_mib_budget")):
        raise ValueError("Worker positive numeric configuration is invalid")
    if (not isinstance(config["reserved_slots"], list) or
            any(type(slot) is not int or not 1 <= slot <= 250 for slot in config["reserved_slots"]) or
            len(set(config["reserved_slots"])) != len(config["reserved_slots"])):
        raise ValueError("Worker reserved slot list is invalid")
    if not config["per_account_internal_reserved_bytes"]:
        raise ValueError("Worker capacity reservations must be explicitly populated")
    for name in ("expected_guest_sha256", "release_manifest_sha256"):
        if not isinstance(config[name], str) or not SHA256.fullmatch(config[name]):
            raise ValueError("Worker release digests must be pinned SHA-256 values")
    if not isinstance(config["external_disks"], list):
        raise ValueError("Worker external disk inventory must be explicitly supplied")
    for disk in config["external_disks"]:
        if (not isinstance(disk, dict) or set(disk) != {"asset_id", "disk_path", "quota_bytes", "alias_paths"}
                or not isinstance(disk["asset_id"], str) or not disk["asset_id"]
                or type(disk["quota_bytes"]) is not int or not 8*1024**3 <= disk["quota_bytes"] <= 1024*1024**3
                or not isinstance(disk["alias_paths"], list)):
            raise ValueError("Worker external disk inventory is invalid")
        for value in [disk["disk_path"], *disk["alias_paths"]]:
            if not isinstance(value, str) or not Path(value).is_absolute():
                raise ValueError("Worker external disk paths must be absolute")
    for name in ("release_dir", "state_root", "config_root", "unit_root", "socket_root",
                 "ledger_root", "broker_socket"):
        value = config[name]
        if not isinstance(value, str) or not Path(value).is_absolute():
            raise ValueError("Worker paths must be absolute")
    if config["cgroup_root"] != str(ROOT / JAILER_PARENT):
        raise ValueError("Worker cgroup root is fixed by the implementation")
    return config


def _umount(path):
    subprocess.run(["/bin/umount", str(path)], check=True, capture_output=True)


def serve(config, cgroups=None, broker_factory=WorkerBroker, server_factory=Server,
          signal_api=signal, umount=_umount, release_validator=validate_release):
    """Initialize isolation, serve the Unix API, then prove process cleanup."""
    validate_config(config)
    # Fail before creating cgroups, broker directories, sockets or processes.
    release_validator(config["release_dir"], str(Path(__file__).with_name("manager.py").resolve()),
                      config["expected_guest_sha256"],
                      expected_manifest_sha256=config["release_manifest_sha256"])
    image_sizes = {name: (Path(config["release_dir"])/name).stat().st_size
                   for name in ("rootfs.ext4", "vmlinux")}
    image_bytes = sum(image_sizes.values())
    if config["per_account_internal_reserved_bytes"] < image_bytes:
        raise AdmissionError("per-account reserve does not cover immutable jail image copies")
    if config["runtime_vcpu_budget"] == 0:
        raise AdmissionError("conservative configured CPU commitments leave no Worker budget")
    if os.geteuid() != 0:
        raise AdmissionError("Worker container must run as root")
    os.umask(0o077)
    cgroups = cgroups or PrivateCgroups()
    mounted = False
    bound = False
    server = broker = path = None
    socket_inode = None
    lifetime = None
    old_handlers = {}
    def request_stop(_signum, _frame):
        raise KeyboardInterrupt

    try:
        cgroups.initialize()
        mounted = bool(getattr(cgroups, "mounted", True))
        cgroups.expose_for_jailer()
        bound = bool(getattr(cgroups, "exposed", True))
        # This private field is derived only after release validation; the
        # operator/public configuration schema cannot supply allocation credit.
        broker = broker_factory(dict(config, _validated_immutable_image_sizes=image_sizes))
        socket_root = Path(config["socket_root"])
        os.chown(socket_root, 0, config["socket_gid"])
        os.chmod(socket_root, 0o750)
        path = Path(config["broker_socket"])
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o750)
        os.chown(path.parent, 0, config["socket_gid"])
        os.chmod(path.parent, 0o750)
        lifetime = open(Path(config["ledger_root"]) / "service.lock", "a")
        fcntl.flock(lifetime, fcntl.LOCK_EX | fcntl.LOCK_NB)
        remove_stale_socket(path)
        server = server_factory(str(path), Handler)
        socket_inode = path.lstat().st_ino
        server.broker = broker
        server.daemon_threads = False
        server.block_on_close = True
        os.chown(path, 0, config["socket_gid"])
        os.chmod(path, 0o660)
        for sig in (signal_api.SIGTERM, signal_api.SIGINT):
            old_handlers[sig] = signal_api.signal(sig, request_stop)
        server.serve_forever(poll_interval=.25)
    finally:
        failures = []
        processes_clean = True
        mounted = mounted or bool(getattr(cgroups, "mounted", False))
        bound = bound or bool(getattr(cgroups, "exposed", False))
        if server is not None:
            try:
                server.server_close()
            except OSError:
                failures.append("socket server close")
        if broker is not None:
            try:
                broker.supervisor.close()
            except Exception:
                failures.append("managed computer cleanup")
                processes_clean = False
        for sig, handler in old_handlers.items():
            signal_api.signal(sig, handler)
        if path is not None and socket_inode is not None:
            try:
                info = path.lstat()
                if info.st_ino != socket_inode or not stat.S_ISSOCK(info.st_mode):
                    raise AdmissionError("broker socket changed during shutdown")
                path.unlink()
            except (OSError, AdmissionError):
                failures.append("broker socket removal")
        if processes_clean and not failures and bound:
            try:
                umount("/sys/fs/cgroup")
            except (OSError, subprocess.SubprocessError):
                failures.append("private cgroup bind cleanup")
        if processes_clean and not failures and mounted:
            try:
                umount(ROOT)
            except (OSError, subprocess.SubprocessError):
                failures.append("private cgroup mount cleanup")
        if lifetime is not None:
            fcntl.flock(lifetime, fcntl.LOCK_UN)
            lifetime.close()
        if failures:
            raise AdmissionError("Worker shutdown cleanup could not be proven: " + ", ".join(failures))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="/etc/tofi-worker/config.json")
    args = parser.parse_args(argv)
    try:
        config = json.loads(Path(args.config).read_text())
        serve(config)
    except (ValueError, OSError, AdmissionError, subprocess.SubprocessError) as error:
        print("Worker startup/shutdown failed: " + str(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 0
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
