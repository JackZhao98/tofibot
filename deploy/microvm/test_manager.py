import http.client
import importlib.util
import json
from pathlib import Path
import socket
import signal
import subprocess
import tempfile
import threading
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("manager", Path(__file__).with_name("manager.py"))
manager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manager)


def config(slot=21):
    return dict(id="acceptance-" + str(slot), slot=slot, state_dir="/var/lib/tofi/" + str(slot),
                image_dir="/opt/tofi/image", bin_dir="/opt/tofi/bin", socket_dir="/run/tofi/" + str(slot))


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(3)
        self.sock.connect(self.host)


class WorkerNetworkTests(unittest.TestCase):
    def test_only_fixed_proc_files_are_remounted_inside_owned_netns(self):
        cfg=dict(config(slot=23), cgroup_parent="tofi-vms", worker_private_sysctls=True)
        vm=manager.VM(cfg)
        def result(*args,**kwargs):
            occupied_check=args[:3] in (("ip","link","show"),("iptables","-S",vm.chain))
            return mock.Mock(stdout="",returncode=1 if occupied_check else 0)
        with mock.patch.object(manager,"run",side_effect=result) as calls:
            with mock.patch.object(Path,"read_text",return_value="1"):
                vm.network_up()
        scripts=[call.args for call in calls.call_args_list if "/bin/sh" in call.args]
        self.assertEqual(len(scripts),2)
        for args,path in zip(scripts,("/proc/sys/net/ipv4/ip_forward","/proc/sys/net/ipv6/conf/all/disable_ipv6")):
            self.assertEqual(args[:6],("ip","netns","exec","tofi-fc-23","/bin/sh","-ec"))
            self.assertIn(f"mount --bind {path} {path};",args[6])
            self.assertIn(f"mount -o remount,bind,rw {path};",args[6])
        for value in ("yes",1,None):
            with self.assertRaises(ValueError): manager.validate_config(dict(cfg,worker_private_sysctls=value))
        with self.assertRaises(ValueError): manager.validate_config(dict(config(slot=23),worker_private_sysctls=True))


class LongVsockPathTests(unittest.TestCase):
    @unittest.skipUnless(__import__("sys").platform.startswith("linux") and Path("/proc/self/fd").is_dir(),
                         "requires Linux procfs fd paths")
    def test_connects_to_fixed_vsock_through_open_parent_fd(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            while len(str(parent / "v.sock").encode()) < 146:
                parent = parent / ("account-" + "x" * 24)
            parent.mkdir(parents=True)
            sock_path = parent / "v.sock"
            listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            directory_fd = __import__("os").open(parent, __import__("os").O_RDONLY | __import__("os").O_DIRECTORY)
            try:
                listener.bind(f"/proc/self/fd/{directory_fd}/v.sock")
            finally:
                __import__("os").close(directory_fd)
            listener.listen(1)
            vm = manager.VM(config())
            vm.vsock = sock_path
            received = []

            def serve_handshake():
                with listener:
                    peer, _ = listener.accept()
                    with peer:
                        received.append(peer.recv(80))
                        peer.sendall(b"OK connected\n")

            server = threading.Thread(target=serve_handshake)
            server.start()
            try:
                with vm.connect(timeout=3):
                    pass
            finally:
                server.join(3)
                listener.close()
            self.assertFalse(server.is_alive())
            self.assertEqual(received, [b"CONNECT 1052\n"])


class LongControlSocketPathTests(unittest.TestCase):
    def test_long_bind_closes_pinned_parent_even_on_failure(self):
        import os
        server = object.__new__(manager.Server)
        server.server_address = "/temporary/" + "x" * 120 + "/control.sock"
        server.socket = mock.Mock()
        server.socket.bind.side_effect = OSError("bind rejected")
        with mock.patch.object(manager.sys, "platform", "linux"), \
             mock.patch.object(manager.os, "open", return_value=41) as opened, \
             mock.patch.object(manager.os, "close") as closed:
            with self.assertRaisesRegex(OSError, "bind rejected"):
                server.server_bind()
        opened.assert_called_once_with(Path(server.server_address).parent,
                                       os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        server.socket.bind.assert_called_once_with("/proc/self/fd/41/control.sock")
        closed.assert_called_once_with(41)
        self.assertTrue(server.server_address.endswith("/control.sock"))

    @unittest.skipUnless(__import__("sys").platform.startswith("linux") and Path("/proc/self/fd").is_dir(),
                         "requires Linux procfs fd paths")
    def test_binds_fixed_control_socket_without_changing_cwd(self):
        import os
        import socketserver

        class Echo(socketserver.BaseRequestHandler):
            def handle(self):
                self.request.sendall(self.request.recv(4))

        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            while len(os.fsencode(parent / "control.sock")) < 146:
                parent /= "account-" + "x" * 24
            parent.mkdir(parents=True)
            sock_path = parent / "control.sock"
            cwd = os.getcwd()
            descriptors = len(list(Path("/proc/self/fd").iterdir()))
            server = manager.Server(str(sock_path), Echo)
            self.assertEqual(os.getcwd(), cwd)
            self.assertEqual(server.server_address, str(sock_path))
            self.assertTrue(sock_path.is_socket())
            thread = threading.Thread(target=server.handle_request)
            thread.start()
            try:
                directory_fd = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                        client.settimeout(3)
                        client.connect(f"/proc/self/fd/{directory_fd}/control.sock")
                        client.sendall(b"PING")
                        self.assertEqual(client.recv(4), b"PING")
                finally:
                    os.close(directory_fd)
                thread.join(3)
                self.assertFalse(thread.is_alive())
            finally:
                server.server_close()
            self.assertEqual(os.getcwd(), cwd)
            self.assertEqual(len(list(Path("/proc/self/fd").iterdir())), descriptors)


class ProxyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.path = str(Path(self.temp.name) / "control.sock")
        self.vm = manager.VM(config())
        self.vm.state = "ready"
        self.server = manager.Server(self.path, manager.Handler)
        self.server.vm = self.vm
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def request(self, method, path, body=None):
        c = UnixHTTP(self.path)
        try:
            c.request(method, path, body=body, headers={"Content-Type": "application/json"})
            r = c.getresponse()
            return r.status, json.loads(r.read())
        finally:
            c.close()

    def test_runner_proxy_keeps_protocol_but_never_shared_auth(self):
        proxied,guest=socket.socketpair()
        self.vm.connect=lambda **_:proxied
        seen=[]
        payload=b'{"jsonrpc":"2.0","id":1,"result":{"account":"own"}}'
        def reply():
            with guest:
                guest.settimeout(3)
                raw=b""
                while b"\r\n\r\n" not in raw:raw+=guest.recv(4096)
                headers,body=raw.split(b"\r\n\r\n",1)
                seen.append(headers)
                size=int(next(line.split(b":",1)[1] for line in headers.split(b"\r\n") if line.startswith(b"Content-Length:")))
                while len(body)<size:body+=guest.recv(4096)
                seen.append(body)
                guest.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n"+hex(len(payload))[2:].encode()+b"\r\n"+payload+b"\r\n0\r\n\r\n")
        thread=threading.Thread(target=reply);thread.start()
        c=UnixHTTP(self.path)
        c.request("POST","/v1/runner/mcp/fixture",body=b'{"method":"server/discover"}',headers={"MCP-Protocol-Version":"2026-07-28","Mcp-Method":"server/discover","Mcp-Name":"identity","Mcp-Param-Region":"isolated","Authorization":"Bearer must-not-forward","X-Unrelated":"must-not-forward"})
        r=c.getresponse();data=r.read();c.close();thread.join(3)
        self.assertEqual(r.status,200);self.assertEqual(data,payload)
        self.assertIn(b"MCP-Protocol-Version: 2026-07-28",seen[0])
        self.assertIn(b"Mcp-Method: server/discover",seen[0])
        self.assertIn(b"Mcp-Name: identity",seen[0])
        self.assertIn(b"Mcp-Param-Region: isolated",seen[0])
        self.assertNotIn(b"Authorization",seen[0])
        self.assertNotIn(b"X-Unrelated",seen[0])
        self.vm.connect=mock.Mock()
        for path in ("/v1/runner/v1/plugins/../../personal","/v1/runner/mcp/fixture?account=personal","/v1/runner/v1/shutdown"):
            self.assertEqual(self.request("GET",path)[0],404)
        self.vm.connect.assert_not_called()

    def test_runner_disconnect_closes_guest_before_response(self):
        proxied,guest=socket.socketpair();entered=threading.Event();closed=threading.Event()
        self.vm.connect=lambda **_:proxied
        def wait():
            with guest:
                guest.settimeout(3);raw=b""
                while b"\r\n\r\n" not in raw:raw+=guest.recv(4096)
                entered.set()
                while guest.recv(4096):pass
                closed.set()
        thread=threading.Thread(target=wait);thread.start()
        c=UnixHTTP(self.path);c.request("POST","/v1/runner/mcp/fixture",body=b"{}")
        self.assertTrue(entered.wait(2));c.close()
        self.assertTrue(closed.wait(2));thread.join(2)

    def test_caller_cannot_select_another_vm_or_host_lifecycle(self):
        status, _ = self.request("POST", "/v1/action", json.dumps({"action": "shell.exec", "vm_id": "victim"}))
        self.assertEqual(status, 400)
        for path in ("/v1/shutdown", "/v1/execute", "/v1/action?vm=victim"):
            status, _ = self.request("POST", path, "{}")
            self.assertEqual(status, 404)
        self.vm.connect = mock.Mock(side_effect=AssertionError("must not reach guest"))
        self.vm.state = "error"
        status, _ = self.request("POST", "/v1/action", '{"action":"shell.exec"}')
        self.assertEqual(status, 503)
        self.vm.connect.assert_not_called()

    def test_purge_requires_explicit_confirmation(self):
        self.vm.purge_workspace = mock.Mock(side_effect=AssertionError("must not purge"))
        status, body = self.request("POST", "/v1/purge", "{}")
        self.assertEqual(status, 400)
        self.assertIn("confirmation", body["error"])
        self.vm.purge_workspace.assert_not_called()

    def test_storage_is_observational_and_validates_guest_metrics(self):
        self.vm.guest_request = mock.Mock(return_value=dict(total_bytes=100,used_bytes=40,free_bytes=60,available_bytes=50))
        status,body=self.request("GET","/v1/storage")
        self.assertEqual(status,200)
        self.assertEqual(body["used_bytes"],40)
        self.vm.guest_request.assert_called_once_with("GET","/v1/storage",timeout=2)
        self.vm.state="stopped"
        self.vm.guest_request.reset_mock()
        self.assertEqual(self.request("GET","/v1/storage")[0],503)
        self.vm.guest_request.assert_not_called()
        self.vm.state="ready"
        self.vm.guest_request.return_value=dict(total_bytes=100,used_bytes=150,free_bytes=0,available_bytes=0)
        self.assertEqual(self.request("GET","/v1/storage")[0],502)

    def test_binary_blob_stream_is_bounded_and_fixed_to_own_guest(self):
        path="/v1/blobs/11111111-1111-4111-8111-111111111111"
        payload=b"x"*(4*1024*1024)  # Above the action protocol limit.
        for method in ("PUT","GET"):
            proxied,guest=socket.socketpair()
            self.vm.connect=lambda timeout:proxied
            received=[]
            def serve_guest():
                with guest:
                    guest.settimeout(5)
                    body=b""
                    while b"\r\n\r\n" not in body:body+=guest.recv(65536)
                    headers,body=body.split(b"\r\n\r\n",1)
                    received.append(headers.split(b"\r\n")[0])
                    if method=="PUT":
                        while len(body)<len(payload):body+=guest.recv(65536)
                        received.append(body)
                        guest.sendall(b"HTTP/1.1 201 Created\r\nContent-Length: 0\r\n\r\n")
                    else:
                        guest.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: "+str(len(payload)).encode()+b"\r\n\r\n"+payload)
            thread=threading.Thread(target=serve_guest);thread.start()
            client=UnixHTTP(self.path)
            client.request(method,path,body=payload if method=="PUT" else None)
            response=client.getresponse();data=response.read();client.close();thread.join(5)
            self.assertFalse(thread.is_alive())
            self.assertEqual(response.status,201 if method=="PUT" else 200)
            self.assertEqual(received[0],f"{method} {path} HTTP/1.1".encode())
            self.assertEqual(received[1] if method=="PUT" else data,payload)
        self.vm.state="stopped"
        self.vm.connect=mock.Mock()
        self.assertEqual(self.request("GET",path)[0],503)
        self.vm.connect.assert_not_called()

    def test_client_cancellation_closes_guest_transport(self):
        entered = threading.Event()
        cancelled = threading.Event()
        proxied, guest = socket.socketpair()
        self.vm.connect = lambda: proxied
        def serve_guest():
            with guest:
                guest.settimeout(3)
                data = b""
                while b"\r\n\r\n" not in data:
                    data += guest.recv(4096)
                entered.set()
                while guest.recv(4096):
                    pass
                cancelled.set()
        thread = threading.Thread(target=serve_guest, daemon=True)
        thread.start()
        client = UnixHTTP(self.path)
        client.request("POST", "/v1/action", '{"action":"shell.exec","source":"model"}')
        self.assertTrue(entered.wait(2))
        client.close()
        self.assertTrue(cancelled.wait(2), "guest request must cancel before command timeout")
        thread.join(timeout=2)

    def test_guarded_write_keeps_identity_and_guest_outcome_over_transport(self):
        identity = dict(guard_version=1, target="/workspace/bots/fixture/a",
                        object="1:2", parent="/workspace/bots/fixture",
                        parent_object="1:3", operation="files.write")
        for status, guarded in [(200, True), (409, True), (200, False)]:
            with self.subTest(status=status, guarded=guarded):
                action = dict(bot_id="00000000-0000-4000-8000-000000000001",
                              run_id="synthetic", action="files.write",
                              args=dict(path="a", content="X", append=True), source="model")
                if guarded:
                    action["write_identity"] = identity
                original = json.dumps(action).encode()
                outcome = dict(version=1, status="validation_error",
                               code="write_identity_changed", certainty="not_executed",
                               message="Synthetic identity mismatch", next_action="verify_target")
                response = dict(ok=status == 200)
                if status == 409:
                    response.update(error="Synthetic identity mismatch", outcome=outcome)
                payload = json.dumps(response).encode()
                proxied, guest = socket.socketpair()
                self.vm.connect = mock.Mock(return_value=proxied)
                received = []
                def serve_guest():
                    with guest:
                        guest.settimeout(3)
                        raw = b""
                        while b"\r\n\r\n" not in raw:
                            raw += guest.recv(4096)
                        headers, body = raw.split(b"\r\n\r\n", 1)
                        while len(body) < len(original):
                            body += guest.recv(4096)
                        received.append(body)
                        reason = b"Conflict" if status == 409 else b"OK"
                        guest.sendall(b"HTTP/1.1 " + str(status).encode() + b" " + reason
                                      + b"\r\nContent-Type: application/json\r\nContent-Length: "
                                      + str(len(payload)).encode() + b"\r\nConnection: close\r\n\r\n" + payload)
                thread = threading.Thread(target=serve_guest, daemon=True)
                thread.start()
                actual_status, actual_response = self.request("POST", "/v1/action", original)
                thread.join(3)
                self.assertFalse(thread.is_alive())
                self.assertEqual(received, [original])
                self.assertEqual((actual_status, actual_response), (status, response))
                self.vm.connect.assert_called_once_with()

    def test_invalid_write_identity_is_refused_before_guest_dispatch(self):
        self.vm.connect = mock.Mock(side_effect=AssertionError("must not reach Guest"))
        for action, identity in [("files.write", None), ("files.write", []),
                                 ("files.write", "synthetic"), ("files.read", {}),
                                 ("shell.exec", {})]:
            with self.subTest(action=action, identity=identity):
                status, _ = self.request("POST", "/v1/action", json.dumps(
                    dict(action=action, args={}, write_identity=identity)))
                self.assertEqual(status, 400)
        self.vm.connect.assert_not_called()

    def test_stream_rejects_arbitrary_endpoints_and_parameters(self):
        self.vm.connect = mock.Mock(side_effect=AssertionError("must not reach guest"))
        bot = "00000000-0000-0000-0000-000000000001"
        for path in ("/v1/desktop/stream", "/v1/desktop/stream?bot_id=invalid", "/v1/desktop/stream?bot_id=" + bot + "&display=:0", "/v1/desktop/stream?bot_id=" + bot + "&cursor=bad"):
            status, _ = self.request("GET", path)
            self.assertEqual(status, 400)
        self.vm.state = "stopped"
        status, _ = self.request("GET", "/v1/desktop/stream?bot_id=" + bot)
        self.assertEqual(status, 503)
        self.vm.connect.assert_not_called()

    def test_stream_disconnect_closes_guest_without_buffering(self):
        entered, cancelled = threading.Event(), threading.Event()
        proxied, guest = socket.socketpair()
        self.vm.connect = lambda: proxied
        def serve_guest():
            with guest:
                guest.settimeout(3)
                request = b""
                while b"\r\n\r\n" not in request:
                    request += guest.recv(4096)
                self.assertIn(b"GET /v1/desktop/stream?bot_id=00000000-0000-0000-0000-000000000001&cursor=hidden", request)
                guest.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: video/mp4\r\nConnection: close\r\n\r\nframe")
                entered.set()
                while guest.recv(4096):
                    pass
                cancelled.set()
        thread = threading.Thread(target=serve_guest, daemon=True)
        thread.start()
        client = UnixHTTP(self.path)
        client.request("GET", "/v1/desktop/stream?bot_id=00000000-0000-0000-0000-000000000001&cursor=hidden")
        response = client.getresponse()
        self.assertEqual(response.status, 200)
        self.assertEqual(response.read(5), b"frame")
        response.close()
        client.close()
        self.assertTrue(cancelled.wait(2))
        thread.join(timeout=2)

    def test_existing_network_is_never_removed_on_failed_allocation(self):
        with mock.patch.object(manager, "run") as run:
            run.return_value.stdout = self.vm.netns
            with self.assertRaises(RuntimeError):
                self.vm.network_up()
            run.reset_mock()
            self.vm.network_down()
            run.assert_not_called()

    def test_idle_timeout_config_is_bounded_and_cannot_inject_boot_arguments(self):
        for value in (-1, 86401, True, "10 init=/bin/sh"):
            with self.assertRaises(ValueError):
                manager.validate_config(dict(config(), desktop_idle_seconds=value))
        for value in (0, 2, 900, 86400):
            self.assertEqual(manager.validate_config(dict(config(), desktop_idle_seconds=value))["desktop_idle_seconds"], value)

    def test_firecracker_config_enables_reporting_balloon_by_default(self):
        cfg = self.vm.firecracker_config()
        self.assertEqual(cfg["balloon"], {"amount_mib": 0, "deflate_on_oom": True,
                                          "stats_polling_interval_s": manager.BALLOON_STATS_INTERVAL_S,
                                          "free_page_reporting": True})
        self.assertNotIn("free_page_hinting", cfg["balloon"])
        args = cfg["boot-source"]["boot_args"].split()
        self.assertIn("page_reporting.page_reporting_order=%d" % manager.PAGE_REPORTING_ORDER, args)
        self.assertIn("init=/sbin/tofi-init", args)
        self.assertEqual(cfg["machine-config"]["mem_size_mib"], self.vm.c.get("memory_mib", 4096))
        self.assertEqual({d["drive_id"] for d in cfg["drives"]}, {"rootfs", "workspace"})
        self.assertEqual(cfg["vsock"], {"guest_cid": 3, "uds_path": "/run/v.sock"})

    def test_guest_init_applies_reporting_order_and_reclaims_only_without_browser(self):
        init = Path(__file__).with_name("init.sh")
        self.assertEqual(subprocess.run(["bash", "-n", str(init)]).returncode, 0)
        text = init.read_text()
        self.assertIn("page_reporting.page_reporting_order=*) page_reporting_order=${argument#*=} ;;", text)
        self.assertIn('[[ "${page_reporting_order:-}" =~ ^[0-9]$ && -w "$reporting_order_file" ]]', text)
        self.assertRegex(str(manager.PAGE_REPORTING_ORDER), r"^[0-9]$")
        loop = text[text.index("reclaim_pid="):text.index("TOFI_GUEST_READY_START")]
        self.assertIn("pgrep -x chrome", loop)
        self.assertLess(loop.index("pgrep -x chrome"), loop.index("drop_caches"))
        self.assertNotIn("drop_caches", text.replace(loop, ""))

    def test_balloon_can_be_disabled_and_flag_is_strict(self):
        vm = manager.VM(dict(config(23), memory_balloon=False))
        cfg = vm.firecracker_config()
        self.assertNotIn("balloon", cfg)
        self.assertNotIn("page_reporting", cfg["boot-source"]["boot_args"])
        for value in ("false", 0, None):
            with self.assertRaises(ValueError):
                manager.validate_config(dict(config(), memory_balloon=value))

    def test_memory_usage_reads_rss_and_balloon_statistics(self):
        self.assertEqual(self.vm.memory_usage(), {"balloon_enabled": True, "host_rss_mib": None, "balloon": None})
        api, peer = socket.socketpair()
        stats = json.dumps({"target_pages": 0, "actual_pages": 0, "target_mib": 0, "actual_mib": 0,
                            "free_memory": 600 * 1024**2, "total_memory": 990 * 1024**2,
                            "available_memory": 700 * 1024**2, "disk_caches": 80 * 1024**2,
                            "swap_in": 0}).encode()
        def serve():
            with peer:
                request = b""
                while b"\r\n\r\n" not in request:
                    request += peer.recv(4096)
                assert request.startswith(b"GET /balloon/statistics HTTP/1.1")
                peer.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n" % len(stats) + stats)
        thread = threading.Thread(target=serve, daemon=True)
        thread.start()
        self.vm.state = "ready"
        self.vm.process = mock.Mock(pid=4242)
        self.vm.process.poll.return_value = None
        status = "Name:\tfirecracker\nVmRSS:\t  524288 kB\n"
        with mock.patch.object(manager.VM, "connect_unix", return_value=api), \
                mock.patch.object(manager.Path, "read_text", return_value=status):
            usage = self.vm.memory_usage()
        thread.join(timeout=2)
        self.assertEqual(usage["host_rss_mib"], 512)
        self.assertEqual(usage["balloon"], {"target_mib": 0, "actual_mib": 0, "guest_total_mib": 990,
                                            "guest_free_mib": 600, "guest_available_mib": 700,
                                            "guest_cache_mib": 80})
        with mock.patch.object(manager.VM, "connect_unix", side_effect=OSError("gone")), \
                mock.patch.object(manager.Path, "read_text", side_effect=OSError("gone")):
            self.assertEqual(self.vm.memory_usage(), {"balloon_enabled": True, "host_rss_mib": None, "balloon": None})
        self.vm.process = None
        self.vm.state = "stopped"

    def test_different_users_have_distinct_resources(self):
        other = manager.VM(config(22))
        for field in ("uid", "netns", "host_if", "peer_if", "chain", "link_net", "guest_net", "root", "jail", "vsock"):
            self.assertNotEqual(getattr(self.vm, field), getattr(other, field), field)

    def test_preparation_status_does_not_claim_ready(self):
        self.vm.state = "starting"
        self.vm.phase = "booting"
        status, body = self.request("GET", "/v1/info")
        self.assertEqual(status, 200)
        self.assertEqual(body["state"], "starting")
        self.assertEqual(body["phase"], "booting")
        status, _ = self.request("POST", "/v1/retry", "{}")
        self.assertEqual(status, 409)

    def test_oauth_proxy_allows_only_fixed_guest_endpoint(self):
        proxied, guest = socket.socketpair()
        self.vm.connect = lambda timeout=5: proxied
        seen = []
        def serve_guest():
            with guest:
                request = b""
                while b"\r\n\r\n" not in request:
                    request += guest.recv(4096)
                seen.append(request)
                guest.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 50\r\nConnection: close\r\n\r\n{" + b'"session_id":"sid","redirect_uri":"http://guest"' + b"}")
        thread = threading.Thread(target=serve_guest, daemon=True)
        thread.start()
        status, body = self.request("POST", "/v1/oauth/start", "{}")
        thread.join(timeout=2)
        self.assertEqual(status, 200)
        self.assertEqual(body["session_id"], "sid")
        self.assertIn(b"POST /v1/oauth/start HTTP/1.1", seen[0])
        self.vm.connect = mock.Mock(side_effect=AssertionError("must not reach guest"))
        for path in ("/v1/oauth/start?x=1", "/v1/oauth/unknown"):
            status, _ = self.request("POST", path)
            self.assertEqual(status, 404)
        self.vm.connect.assert_not_called()

    @mock.patch.object(manager.os, 'killpg')
    def test_stop_signal_targets_jailer_process_group(self, killpg):
        process = mock.Mock(pid=1234)
        manager.signal_process_group(process, signal.SIGTERM)
        killpg.assert_called_once_with(1234, signal.SIGTERM)
        process.send_signal.assert_not_called()


class StopCoordinationTests(unittest.TestCase):
    def test_changed_uid_waits_for_supervisor_then_cleans_network(self):
        vm = manager.VM(dict(config(), worker_private_sysctls=True, cgroup_parent="tofi-vms"))
        proc = mock.Mock(pid=44)
        proc.poll.return_value = None
        proc.wait.side_effect = [subprocess.TimeoutExpired("vm",10),0]
        vm.process = proc
        vm.guest_request = mock.Mock()
        vm.network_down = mock.Mock()
        with mock.patch.object(manager, "signal_process_group", side_effect=PermissionError):
            vm.stop()
        self.assertEqual(proc.wait.call_args_list, [mock.call(timeout=10),mock.call(timeout=75)])
        vm.network_down.assert_called_once()
        self.assertEqual(vm.state,"stopped")
        vm.stop()
        self.assertEqual(vm.network_down.call_count,2)

    def test_unresolved_vm_never_tears_down_network(self):
        vm = manager.VM(dict(config(), worker_private_sysctls=True, cgroup_parent="tofi-vms"))
        proc = mock.Mock(pid=44);proc.poll.return_value=None
        proc.wait.side_effect = subprocess.TimeoutExpired("vm",75)
        vm.process=proc;vm.guest_request=mock.Mock();vm.network_down=mock.Mock()
        with mock.patch.object(manager,"signal_process_group",side_effect=PermissionError):
            with self.assertRaises(subprocess.TimeoutExpired): vm.stop()
        vm.network_down.assert_not_called()
        self.assertIs(vm.process,proc)

    def test_partial_network_cleanup_retains_ownership_and_can_retry(self):
        vm=manager.VM(config());vm.network_owned=True
        def run(*args, **kwargs):
            if args==("ip","netns","list"):return mock.Mock(stdout=vm.netns+"\n",returncode=0)
            return mock.Mock(stdout="",returncode=1)
        with mock.patch.object(manager,"run",side_effect=run):
            with self.assertRaisesRegex(RuntimeError,"cleanup incomplete"):vm.network_down()
        self.assertTrue(vm.network_owned)
        with mock.patch.object(manager,"run",return_value=mock.Mock(stdout="",returncode=1)):
            vm.network_down();vm.network_down()
        self.assertFalse(vm.network_owned)


if __name__ == "__main__":
    unittest.main()
