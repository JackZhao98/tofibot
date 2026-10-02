"""Offline broker quota fixtures; filesystem commands are synthetic unless noted."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch
import uuid

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
spec = importlib.util.spec_from_file_location("account_provisioner", HERE / "account_provisioner.py")
broker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(broker)
GiB = 1024**3
ORIGINAL_PATH_READ_TEXT = Path.read_text


def ext4_image(path, size):
    """Create sparse ext4-shaped fixture with a valid primary superblock."""
    with path.open("wb") as f:
        f.truncate(size)
    sb = bytearray(1024)
    sb[4:8] = (size // 1024).to_bytes(4, "little")
    sb[24:28] = (0).to_bytes(4, "little")
    sb[56:58] = b"\x53\xef"
    with path.open("r+b") as f:
        f.seek(1024)
        f.write(sb)


class SyntheticExt4Tools:
    """Model fsck/resize effects on the sparse fixture; not real ext4 validation."""
    def __init__(self):
        self.calls = []
        self.fail = None

    def __call__(self, args, check=True):
        self.calls.append(args)
        if self.fail == args[0]:
            return subprocess.CompletedProcess(args, 8, "", "synthetic failure")
        if args[0] == "resize2fs":
            path = Path(args[1])
            blocks = path.stat().st_size // 1024
            with path.open("r+b") as f:
                f.seek(1028)
                f.write(blocks.to_bytes(4, "little"))
        if args[0] == "/usr/bin/systemctl" and args[1] == "stop" and self.fail == "stop":
            raise OSError("synthetic stop failure")
        return subprocess.CompletedProcess(args, 0, "", "")


class QuotaHTTPFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir="/tmp")
        root = Path(self.temp.name).resolve()
        release = root / "release"
        release.mkdir()
        (release / "manager.py").write_text("# test release\n")
        self.config = {k: str(root / k) for k in
                       ("state_root", "config_root", "unit_root", "socket_root", "ledger_root")}
        self.config.update(release_dir=str(release), headroom_bytes=10*GiB,
                           warning_bytes=15*GiB, reserved_slots=[1, 21, 22],
                           external_reserved_bytes=4*GiB, per_account_internal_reserved_bytes=2*GiB,
                           vcpus=2, memory_mib=2048, runtime_vcpu_budget=4,
                           runtime_memory_mib_budget=5120, host_memory_headroom_mib=2048,
                           app_uid=os.geteuid(), socket_gid=os.geteuid())
        self.metrics = lambda: {"total_bytes": 100*GiB, "available_bytes": 80*GiB}
        self.tools = SyntheticExt4Tools()
        self.b = broker.Broker(self.config, self.tools, self.metrics)
        self.identity = str(uuid.uuid4())
        self.b.dispatch({"op": "reserve", "account_id": self.identity, "quota_gib": 16})
        state = Path(self.config["state_root"]) / self.identity
        state.mkdir()
        ext4_image(state / "workspace.ext4", 16*GiB)
        with (state / "workspace.ext4").open("r+b") as stream:
            stream.seek(4*1024**2)
            stream.write(b"account-data-marker")
        cfg = dict(id="ac-" + self.identity, slot=self.b._slot(self.identity),
                   state_dir=str(state), socket_dir=str(Path(self.config["socket_root"]) / self.identity),
                   socket_gid=self.config["socket_gid"], image_dir=str(root / "release"),
                   bin_dir=str(root / "release" / "bin"), vcpus=2, memory_mib=2048, disk_gib=16)
        self.config_path = Path(self.config["config_root"]) / (self.identity + ".json")
        self.b.write_owned(self.config_path, json.dumps(cfg, sort_keys=True) + "\n", 0o600)
        with self.b.ledger.connection() as db:
            db.execute("INSERT INTO runtime_claims VALUES(?,?,?)", (self.identity, 2, 2560))

        self.sock = None

    def tearDown(self):
        self.temp.cleanup()

    def request(self, op, **extra):
        body = json.dumps({"op": op, "account_id": self.identity, **extra}).encode()
        server_side, client = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
        class RequestServer:
            broker = self.b
        def serve():
            try:
                broker.Handler(server_side, None, RequestServer())
            finally:
                server_side.close()
        server_thread = threading.Thread(target=serve, daemon=True)
        # Darwin lacks SO_PEERCRED. Exercise the real HTTP handler while
        # supplying the kernel credential tuple it receives on Linux.
        with patch.object(socket, "SO_PEERCRED", 17, create=True), patch.object(
                socket.socket, "getsockopt", return_value=struct.pack("3i", 1, os.geteuid(), os.getegid())):
            server_thread.start()
            with client:
                client.sendall(b"POST /v1/accounts HTTP/1.1\r\nHost: local\r\nContent-Type: application/json\r\nContent-Length: " + str(len(body)).encode() + b"\r\nConnection: close\r\n\r\n" + body)
                data = b""
                while True:
                    part = client.recv(65536)
                    if not part:
                        break
                    data += part
            server_thread.join(timeout=3)
        head, payload = data.split(b"\r\n\r\n", 1)
        return int(head.split(b" ")[1]), json.loads(payload)

    def test_console_quota_flow_stop_resize_verify_ensure_and_reopen(self):
        disk = Path(self.config["state_root"]) / self.identity / "workspace.ext4"
        inode_before = disk.stat().st_ino
        with patch.object(Path, "read_text", autospec=True) as read_text:
            read_text.side_effect = lambda path, *a, **kw: ("MemAvailable: 16777216 kB\n" if str(path) == "/proc/meminfo" else ORIGINAL_PATH_READ_TEXT(path, *a, **kw))
            status, result = self.request("quota", quota_gib=32)
        self.assertEqual(status, 200)
        self.assertTrue(result["applied"] and result["provisioned"])
        self.assertEqual(disk.stat().st_size, 32*GiB)
        self.assertEqual(disk.stat().st_ino, inode_before)
        with disk.open("rb") as stream:
            stream.seek(4*1024**2)
            self.assertEqual(stream.read(len(b"account-data-marker")), b"account-data-marker")
        self.assertEqual(broker.Broker._ext4_blocks(disk)[0] * 1024, 32*GiB)
        self.assertIn("stop", [call[1] for call in self.tools.calls if call[0] == "/usr/bin/systemctl"])
        with self.b.ledger.connection() as db:
            self.assertIsNone(db.execute("SELECT 1 FROM resize_fences WHERE account_id=?", (self.identity,)).fetchone())
            self.assertIsNone(db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?", (self.identity,)).fetchone())
        self.assertEqual(json.loads(self.config_path.read_text())["disk_gib"], 32)
        calls_before_idempotent = len(self.tools.calls)
        status, result = self.request("quota", quota_gib=32)
        self.assertEqual(status, 200)
        self.assertTrue(result["applied"] and result["provisioned"])
        self.assertEqual(len(self.tools.calls), calls_before_idempotent)
        reopened = broker.Broker(self.config, self.tools, self.metrics)
        with patch.object(reopened, "runtime_admission"):
            self.assertTrue(reopened.dispatch({"op": "ensure", "account_id": self.identity})["account_id"])
        self.assertEqual(json.loads(self.config_path.read_text())["disk_gib"], 32)
        status, _ = self.request("quota", quota_gib=72)
        self.assertEqual(status, 409)

    def test_capacity_rejects_before_stop_and_shrink_is_explicitly_rejected(self):
        status, result = self.request("quota", quota_gib=72)
        self.assertEqual(status, 409)
        self.assertEqual(self.tools.calls, [])
        status, result = self.request("quota", quota_gib=8)
        self.assertEqual(status, 409)
        self.assertIn("offline shrink required", result["error"])

    def test_failed_stop_retains_budget_fence_and_claim_across_reopen(self):
        self.tools.fail = "stop"
        status, _ = self.request("quota", quota_gib=32)
        self.assertEqual(status, 400)
        reopened = broker.Broker(self.config, self.tools, self.metrics)
        self.assertEqual(reopened.ledger.snapshot()["promised_bytes"], 32*GiB)
        with reopened.ledger.connection() as db:
            self.assertEqual(db.execute("SELECT target_bytes FROM resize_fences WHERE account_id=?", (self.identity,)).fetchone()[0], 32*GiB)
            self.assertIsNotNone(db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?", (self.identity,)).fetchone())
        for op in ("ensure", "restore", "abort"):
            with self.assertRaises(broker.AdmissionError):
                reopened.dispatch({"op": op, "account_id": self.identity})
        self.assertEqual((Path(self.config["state_root"]) / self.identity / "workspace.ext4").stat().st_size, 16*GiB)

    def test_resize_failure_keeps_fence_and_tampered_config_is_preserved(self):
        original = self.config_path.read_bytes()
        cfg = json.loads(original)
        cfg["tampered"] = True
        self.config_path.write_text(json.dumps(cfg) + "\n")
        preserved = self.config_path.read_bytes()
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch({"op": "quota", "account_id": self.identity, "quota_gib": 32})
        self.assertEqual(self.config_path.read_bytes(), preserved)
        self.assertEqual((Path(self.config["state_root"]) / self.identity / "workspace.ext4").stat().st_size, 16*GiB)

    def test_resize_tool_failure_persists_full_target_and_fence(self):
        state_root = Path(self.config["state_root"])
        host = {"total_bytes": 100*GiB, "available_bytes": 80*GiB,
                "filesystem_device": state_root.stat().st_dev, "filesystem_type": "ext4"}
        self.metrics = lambda: host
        self.b.ledger.metrics = self.metrics
        self.tools.fail = "resize2fs"
        status, _ = self.request("quota", quota_gib=32)
        self.assertEqual(status, 409)
        reopened = broker.Broker(self.config, self.tools, self.metrics)
        pending = reopened.ledger.snapshot()
        self.assertEqual(pending["promised_bytes"], 32*GiB)
        self.assertEqual(pending["accounts"][0]["quota_bytes"], 32*GiB)
        self.assertEqual(pending["accounts"][0]["logical_bytes"], 32*GiB)
        with reopened.ledger.connection() as db:
            fence = db.execute("SELECT target_bytes,original_bytes FROM resize_fences WHERE account_id=?", (self.identity,)).fetchone()
            self.assertEqual(tuple(fence), (32*GiB, 16*GiB))
        # The first attempt truncated the disk before resize2fs failed. A
        # same-target retry completes the filesystem and clears the fence.
        self.tools.fail = None
        result = reopened.dispatch({"op": "quota", "account_id": self.identity, "quota_gib": 32})
        self.assertTrue(result["applied"])
        self.assertEqual(broker.Broker._ext4_blocks(Path(self.config["state_root"]) / self.identity / "workspace.ext4")[0] * 1024, 32*GiB)
        with reopened.ledger.connection() as db:
            self.assertIsNone(db.execute("SELECT 1 FROM resize_fences WHERE account_id=?", (self.identity,)).fetchone())

    def test_missing_ready_disk_does_not_raise_the_reservation(self):
        disk = Path(self.config["state_root"]) / self.identity / "workspace.ext4"
        disk.unlink()
        with self.b.ledger.connection() as db:
            db.execute("UPDATE computers SET state='ready' WHERE account_id=?", (self.identity,))
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch({"op":"quota","account_id":self.identity,"quota_gib":32})
        with self.b.ledger.connection() as db:
            self.assertEqual(db.execute("SELECT quota_bytes FROM computers WHERE account_id=?",(self.identity,)).fetchone()[0],16*GiB)
            self.assertIsNone(db.execute("SELECT 1 FROM resize_fences").fetchone())
        self.assertEqual(self.tools.calls,[])

    def test_unprovisioned_manager_cleanup_failure_retains_fence_and_claim(self):
        (Path(self.config["state_root"]) / self.identity / "workspace.ext4").unlink()
        self.tools.fail = "stop"
        with self.assertRaises(OSError):
            self.b.dispatch({"op":"quota","account_id":self.identity,"quota_gib":32})
        with self.b.ledger.connection() as db:
            self.assertIsNotNone(db.execute("SELECT 1 FROM runtime_claims WHERE account_id=?",(self.identity,)).fetchone())
            self.assertIsNotNone(db.execute("SELECT 1 FROM resize_fences WHERE account_id=?",(self.identity,)).fetchone())
        self.assertEqual(json.loads(self.config_path.read_text())["disk_gib"],16)

    def test_unprovisioned_tampered_config_is_preserved_before_stop(self):
        (Path(self.config["state_root"]) / self.identity / "workspace.ext4").unlink()
        self.config_path.write_text(self.config_path.read_text()+" ")
        original = self.config_path.read_bytes()
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch({"op":"quota","account_id":self.identity,"quota_gib":32})
        self.assertEqual(self.config_path.read_bytes(),original)
        self.assertEqual(self.tools.calls,[])

    def test_manager_resource_mode_is_rejected_before_stop_or_disk_io(self):
        state = Path(self.config["state_root"]) / self.identity
        resource = state / "resources-applied.json"
        resource.write_text(json.dumps(dict(vcpus=2,memory_mib=2048,disk_gib=16)))
        resource.chmod(0o644)
        with self.assertRaises(broker.AdmissionError):
            self.b.dispatch({"op":"quota","account_id":self.identity,"quota_gib":32})
        self.assertEqual(self.tools.calls,[])
        self.assertEqual((state / "workspace.ext4").stat().st_size,16*GiB)


if __name__ == "__main__":
    unittest.main()
