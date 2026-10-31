"""Tests for the browser engine benchmark: the fixture sites, the success checks and the whole sweep against a
fake TOFI server whose scripted Bot does (or fails to do) each task."""
import json
import os
import re
import sys
import tempfile
import threading
import time
import unittest
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bench  # noqa: E402
import fixtures  # noqa: E402

bench.POLL = 0.05


def fetch(url, data=None, method=None, headers=None):
    req = urllib.request.Request(url, data=data, method=method or ("POST" if data is not None else "GET"), headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        return err.code, err.read().decode("utf-8", "replace")


class FixtureTests(unittest.TestCase):
    def setUp(self):
        self.server, self.state = fixtures.serve("127.0.0.1", 0)
        self.base = "http://127.0.0.1:%d" % self.server.server_address[1]

    def tearDown(self):
        self.server.shutdown()

    def test_form_records_the_posted_name_per_token(self):
        fetch(self.base + "/form?t=a1", urllib.parse.urlencode({"full_name": "Ada", "company": "x"}).encode())
        fetch(self.base + "/form?t=b2", urllib.parse.urlencode({"full_name": "Bob"}).encode())
        self.assertEqual(self.state.forms, {"a1": "Ada", "b2": "Bob"})

    def test_deep_setting_is_three_levels_down_and_saved_by_post(self):
        status, page = fetch(self.base + "/app/settings/privacy/advanced?t=a")
        self.assertIn("Data retention", page)
        self.assertNotIn("Data retention", fetch(self.base + "/app/settings?t=a")[1])
        fetch(self.base + "/app/settings/privacy/advanced?t=a", urllib.parse.urlencode({"retention": "30"}).encode())
        self.assertEqual(self.state.retention["a"], "30")

    def test_upload_keeps_name_and_bytes(self):
        fetch(self.base + "/upload?t=u&name=x.txt", b"secret-line", headers={"Content-Type": "text/plain"})
        self.assertEqual(self.state.uploads["u"], ("x.txt", b"secret-line"))

    def test_table_rows_arrive_by_script(self):
        page = fetch(self.base + "/table?t=t")[1]
        self.assertNotIn("<td>Bolt M6", page)
        self.assertIn("Bolt M6", page)

    def test_table_check_wants_every_row_and_the_total(self):
        task = bench.TableTask()
        rows = "\n".join("%s: %d" % r for r in fixtures.TABLE_ROWS)
        total = sum(q for _, q in fixtures.TABLE_ROWS)
        ok, _ = task.check({}, self.state, rows + "\nTotal: %d" % total)
        self.assertTrue(ok)
        ok, detail = task.check({}, self.state, "\n".join("%s: %d" % r for r in fixtures.TABLE_ROWS[:-1]) + "\nTotal: %d" % total)
        self.assertFalse(ok)
        self.assertIn("Strap 300mm", detail)
        ok, _ = task.check({}, self.state, rows + "\nTotal: 1")
        self.assertFalse(ok)
        ok, _ = task.check({}, self.state, rows.replace("120", "121") + "\nTotal: %d" % total)
        self.assertFalse(ok)


class CookieTests(unittest.TestCase):
    def write(self, text):
        f = tempfile.NamedTemporaryFile("w", delete=False, suffix=".txt")
        f.write(text)
        f.close()
        self.addCleanup(os.unlink, f.name)
        return f.name

    def test_netscape_and_plain_forms(self):
        net = "# Netscape HTTP Cookie File\n#HttpOnly_tofi.local\tFALSE\t/\tTRUE\t0\ttofi_owner_session\tabc123\n"
        self.assertEqual(bench.read_cookie(self.write(net)), "tofi_owner_session=abc123")
        self.assertEqual(bench.read_cookie(self.write("tofi_owner_session=zzz\n")), "tofi_owner_session=zzz")
        with self.assertRaises(SystemExit):
            bench.read_cookie(self.write("garbage"))


class FakeTofi:
    """Just enough of the TOFI HTTP API. The scripted Bot does a task through HTTP against the fixture sites; a
    task named in `fails` is left undone, so the sweep must report it as a failure."""

    def __init__(self, fixture_base, fails=()):
        self.engine = "classic"
        self.dengine = "classic"
        self.pref_on = False
        self.size = 0
        self.second = ""
        self.fixture_base = fixture_base
        self.fails = set(fails)
        self.messages, self.runs, self.tools = [], {}, {}
        self.seq = 0
        self.folder_open = False
        self.inner = ""
        self.cookie_seen = set()
        self.lock = threading.Lock()
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def reply(self, obj, status=200):
                data = json.dumps(obj).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def body(self):
                n = int(self.headers.get("Content-Length") or 0)
                return json.loads(self.rfile.read(n) or b"{}")

            def do_GET(self):
                outer.cookie_seen.add(self.headers.get("Cookie"))
                u = urllib.parse.urlparse(self.path)
                q = urllib.parse.parse_qs(u.query)
                if u.path == "/api/automation-settings":
                    return self.reply({"items": [{"key": "browser_engine", "value": outer.engine}, {"key": "desktop_engine", "value": outer.dengine}]})
                if u.path == "/api/conversations":
                    return self.reply({"conversations": [{"id": "c1", "bot_id": "bot1"}]})
                if u.path.endswith("/runs"):
                    with outer.lock:
                        return self.reply({"runs": list(outer.runs.values())[::-1]})
                if u.path.endswith("/messages"):
                    with outer.lock:
                        return self.reply({"messages": list(outer.messages)})
                if u.path.endswith("/tools"):
                    with outer.lock:
                        return self.reply({"activities": outer.tools.get(q["run_id"][0], []), "has_more": False})
                self.reply({}, 404)

            def do_PUT(self):
                if self.path == "/api/automation-settings":
                    values = self.body()["values"]
                    outer.engine = values.get("browser_engine", outer.engine)
                    outer.dengine = values.get("desktop_engine", outer.dengine)
                    return self.reply({"ok": True})
                self.reply({}, 404)

            def do_POST(self):
                body = self.body()
                if self.path == "/api/computers/firecracker/actions":
                    command = body["args"]["command"]
                    found = re.search(r"note-\w+\.txt", command)
                    if found:
                        outer.inner = found.group(0)
                        outer.second = re.search(r"memo-\w+\.txt", command).group(0)
                    size = re.search(r"head -c (\d+)", command)
                    if size:
                        outer.size = int(size.group(1))
                    if "sed -i" in command:
                        outer.pref_on = False
                    out = "4242\n" if "xdotool search --name bench" in command and outer.folder_open else ""
                    if "grep -h '^single_click='" in command and outer.pref_on:
                        out = "single_click=1\n"
                    return self.reply({"result": {"stdout": out, "exit_code": 0}})
                if self.path.endswith("/messages"):
                    return self.reply(outer.start(body), 202)
                if self.path.endswith("/cancel"):
                    return self.reply({})
                self.reply({}, 404)

        self.http = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.http.serve_forever, daemon=True).start()
        self.url = "http://127.0.0.1:%d" % self.http.server_address[1]

    def stop(self):
        self.http.shutdown()

    def start(self, body):
        with self.lock:
            self.seq += 2
            mid, rid = "m%d" % self.seq, "r%d" % self.seq
            msg = {"id": mid, "seq": self.seq, "role": "user", "content": body["content"], "client_message_id": body["client_message_id"]}
            run = {"id": rid, "status": "running", "trigger_message_id": mid, "created_at": "2026-10-30T10:00:00Z", "updated_at": "2026-10-30T10:00:00Z"}
            self.messages.append(msg)
            self.runs[rid] = run
        threading.Thread(target=self.bot, args=(msg, run), daemon=True).start()
        return {"message": msg, "run": run, "runs": [run]}

    def bot(self, msg, run):
        text = msg["content"]
        engine = self.engine
        tools = []
        reply = "done"
        url = re.search(r"(http://[^\s]+/(form|app|upload|table)\?t=\w+)", text)
        token = re.search(r"t=(\w+)", text).group(1) if url else ""
        base = self.fixture_base
        name = re.search(r'"(Ada Probe [^"]+)"', text)
        if name and "/form" in text and "form_field" not in self.fails:
            fetch(base + "/form?t=" + token, urllib.parse.urlencode({"full_name": name.group(1)}).encode())
            tools = [("browser_navigate", "completed"), ("browser_type", "completed"), ("browser_click", "completed")] if engine == "playwright" else [("computer_browser", "completed")] * 4
        elif "/app?t=" in text and "deep_setting" not in self.fails:
            fetch(base + "/app/settings/privacy/advanced?t=" + token, urllib.parse.urlencode({"retention": "30"}).encode())
            tools = [("browser_click", "completed")] * 5
        elif "/upload?t=" in text and "file_upload" not in self.fails:
            secret = re.search(r"holds exactly this line: (\S+)", text).group(1)
            fname = re.search(r"named (\S+) in your working", text).group(1)
            fetch(base + "/upload?t=%s&name=%s" % (token, fname), (secret + "\n").encode())
            tools = [("computer_shell", "completed"), ("browser_file_upload", "failed"), ("browser_file_upload", "completed")]
        elif "/table?t=" in text and "read_table" not in self.fails:
            reply = "\n".join("%s: %d" % r for r in fixtures.TABLE_ROWS) + "\nTotal: %d" % sum(q for _, q in fixtures.TABLE_ROWS)
            tools = [("browser_snapshot", "completed")]
        elif "Preferences" in text and "desktop_setting" not in self.fails:
            self.pref_on = True
            tools = [("desktop_get_app_state", "completed"), ("desktop_click", "completed"), ("desktop_click", "completed")] if self.dengine == "a11y" else [("computer_desktop", "completed")] * 6
        elif "Properties" in text and "desktop_read" not in self.fails:
            reply = "The window shows %s bytes" % format(self.size, ",")
            tools = [("desktop_get_app_state", "completed")] * 2 if self.dengine == "a11y" else [("computer_desktop", "completed")] * 4
        elif "file manager" in text and "desktop_folder" not in self.fails:
            self.folder_open = True
            reply = "I see %s and %s" % (self.inner, self.second)
            tools = [("desktop_get_app_state", "completed")] * 2 if self.dengine == "a11y" else [("computer_desktop", "completed")] * 3
        time.sleep(0.05)
        with self.lock:
            self.tools[run["id"]] = [{"name": n, "status": s} for n, s in tools] + [{"name": "say", "status": "completed"}]
            self.messages.append({"id": "a" + run["id"], "seq": msg["seq"] + 1, "role": "assistant", "run_id": run["id"], "content": reply})
            run["status"] = "done"
            run["updated_at"] = "2026-10-30T10:00:07Z"


class SweepTests(unittest.TestCase):
    def sweep(self, fails=()):
        server, state = fixtures.serve("127.0.0.1", 0)
        self.addCleanup(server.shutdown)
        fake = FakeTofi("http://127.0.0.1:%d" % server.server_address[1], fails)
        self.addCleanup(fake.stop)
        tofi = bench.Tofi(fake.url, "tofi_owner_session=SECRET")
        bots = {"c1": bench.conversation_bot(tofi, "c1")}
        base = "http://127.0.0.1:%d" % server.server_address[1]
        rows = []
        for setting, engines in bench.SETTINGS.items():
            for engine in engines:
                bench.set_engine(tofi, engine, setting)
                for i, task in enumerate([t for t in bench.TASKS if t.setting == setting]):
                    rows.append(bench.run_trial(tofi, state, base, bots, ["c1"], engine, task, i, 20))
        return rows, fake

    def test_every_task_succeeds_for_a_bot_that_does_it(self):
        rows, fake = self.sweep()
        self.assertEqual(len(rows), 14)
        for r in rows:
            self.assertTrue(r["success"], r)
        self.assertEqual(fake.cookie_seen, {"tofi_owner_session=SECRET"})
        form = [r for r in rows if r["task"] == "form_field"]
        self.assertEqual([r["steps"] for r in form], [4, 3])
        upload = next(r for r in rows if r["task"] == "file_upload" and r["engine"] == "playwright")
        self.assertEqual((upload["steps"], upload["tool_calls"], upload["failed_calls"], upload["seconds"]), (2, 4, 1, 7.0))
        # The accessibility engine's desktop_ tools count as steps.
        pref = {r["engine"]: r["steps"] for r in rows if r["task"] == "desktop_setting"}
        self.assertEqual(pref, {"classic": 6, "a11y": 3})

    def test_an_undone_task_is_reported_as_failed(self):
        rows, _ = self.sweep(fails=("file_upload", "read_table", "desktop_folder", "desktop_setting", "desktop_read"))
        by = {(r["setting"], r["engine"], r["task"]): r["success"] for r in rows}
        self.assertTrue(by[("browser_engine", "classic", "form_field")] and by[("browser_engine", "playwright", "deep_setting")])
        for task in ("file_upload", "read_table"):
            self.assertFalse(by[("browser_engine", "classic", task)])
            self.assertFalse(by[("browser_engine", "playwright", task)])
        for task in ("desktop_folder", "desktop_setting", "desktop_read"):
            self.assertFalse(by[("desktop_engine", "classic", task)])
            self.assertFalse(by[("desktop_engine", "a11y", task)])

    def test_summary_names_every_engine(self):
        rows, _ = self.sweep()
        table = bench.summarize(rows)
        self.assertIn("| classic | all | 4/4 |", table)
        self.assertIn("| playwright | all | 4/4 |", table)
        self.assertIn("| desktop classic | all | 3/3 |", table)
        self.assertIn("| desktop a11y | all | 3/3 |", table)

    def test_desktop_checks_read_the_file_manager_not_the_reply_alone(self):
        server, state = fixtures.serve("127.0.0.1", 0)
        self.addCleanup(server.shutdown)
        fake = FakeTofi("http://127.0.0.1:1")
        self.addCleanup(fake.stop)
        tofi = bench.Tofi(fake.url, "x=y")
        ctx = {"tofi": tofi, "bot": "bot1", "token": "abcdef123456", "base": ""}
        read = bench.ReadTextTask()
        read.setup(ctx)
        self.assertEqual(ctx["size"], fake.size)
        self.assertTrue(read.check(ctx, state, "It says %s bytes" % format(ctx["size"], ","))[0])
        self.assertFalse(read.check(ctx, state, "It says %d bytes" % (ctx["size"] + 1))[0])
        self.assertFalse(read.check(ctx, state, "12%d34" % ctx["size"])[0])
        pref = bench.PreferenceTask()
        self.assertFalse(pref.check(ctx, state, "It is on now.")[0], "a claim without the saved file is not success")
        fake.pref_on = True
        self.assertTrue(pref.check(ctx, state, "")[0])

    def test_engine_the_server_refuses_is_an_error(self):
        server, _ = fixtures.serve("127.0.0.1", 0)
        self.addCleanup(server.shutdown)
        fake = FakeTofi("http://127.0.0.1:1")
        self.addCleanup(fake.stop)
        tofi = bench.Tofi(fake.url, "x=y")
        fake.engine = "classic"
        original = fake.http.RequestHandlerClass.do_PUT
        fake.http.RequestHandlerClass.do_PUT = lambda self: self.reply({"ok": True})  # accepts but changes nothing
        try:
            with self.assertRaises(RuntimeError):
                bench.set_engine(tofi, "playwright")
        finally:
            fake.http.RequestHandlerClass.do_PUT = original


if __name__ == "__main__":
    unittest.main()
