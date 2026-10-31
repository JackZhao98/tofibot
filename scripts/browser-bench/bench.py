#!/usr/bin/env python3
"""Engine benchmark: the same real tasks, once per engine, against a running TOFI.

Browser tasks compare the browser engines (classic, playwright; setting browser_engine). Desktop tasks compare the
desktop engines (classic, a11y; setting desktop_engine): open a folder in the file manager and report its files,
change a setting in a GTK app's Preferences, read text from a desktop app window. Desktop tasks need the
desktop_engine setting and a computer image with the accessibility engine; the browser engine stays as it is.

For each engine and each task the script
  1. sets the workspace setting of the task (and puts the old value back at the end),
  2. posts the task as a person's message into a conversation with a Bot that has a computer,
  3. waits for the run to end, then reads the run's tool calls and the Bot's reply over HTTP,
  4. decides success from what the fixture sites recorded (never from a model's opinion),
  5. records success, steps (browser and desktop tool calls), all tool calls, failed calls and seconds.

It uses only the standard library. Nothing secret is stored: the session cookie is read from the file you name,
kept in memory, sent only to --url, and never printed or written.

    python3 scripts/browser-bench/bench.py --url https://tofi.example:8321 \\
        --cookie-file ~/.tofi-session-cookie --conversation <conversation id> \\
        --fixture-base http://192.168.1.20:8765

--fixture-base is the address the TOFI computer can reach this machine on (the script serves the fixture sites on
--bind, default 0.0.0.0:8765). The cookie file is either a Netscape cookies.txt (a line for tofi_owner_session) or
one line "tofi_owner_session=<value>". A self-signed server needs --insecure or --cafile.

The conversation should be a direct message with a Bot that has the Bot computer. Every prompt starts with a line
that says it is a new, unrelated task, but the conversation still grows: use several --conversation values (tasks
are spread over them) or a fresh Bot per session for clean numbers.
"""
import argparse
import datetime
import json
import os
import re
import socket
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fixtures  # noqa: E402

ENGINES = ["classic", "playwright"]
DESKTOP_ENGINES = ["classic", "a11y"]
SETTINGS = {"browser_engine": ENGINES, "desktop_engine": DESKTOP_ENGINES}
TERMINAL = {"done", "failed", "cancelled", "canceled", "interrupted", "error", "completed"}
ACTIVE_BOT_TOOLS = re.compile(r"^(computer_(browser|desktop)|browser_[a-z_]+|desktop_[a-z_]+)$")
POLL = 2  # seconds between run checks; tests lower it
PREFIX = "New task, unrelated to anything earlier in this chat. "


# ---------------------------------------------------------------- HTTP client

def read_cookie(path):
    """Return the Cookie header value from a Netscape cookies.txt or a one-line name=value file."""
    text = open(os.path.expanduser(path), encoding="utf-8").read()
    pairs = []
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("#HttpOnly_"):
            line = line[len("#HttpOnly_"):]
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) >= 7:
            pairs.append("%s=%s" % (fields[5], fields[6]))
    if pairs:
        return "; ".join(pairs)
    one = " ".join(text.split())
    if "=" not in one:
        raise SystemExit("cookie file holds neither a Netscape cookie line nor name=value")
    return one


class Tofi:
    def __init__(self, base, cookie, insecure=False, cafile=None, timeout=30):
        self.base = base.rstrip("/")
        self._cookie = cookie
        self.timeout = timeout
        if cafile:
            self.ctx = ssl.create_default_context(cafile=cafile)
        elif insecure:
            self.ctx = ssl.create_default_context()
            self.ctx.check_hostname = False
            self.ctx.verify_mode = ssl.CERT_NONE
        else:
            self.ctx = None

    def call(self, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Cookie", self._cookie)
        req.add_header("Accept", "application/json")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=self.timeout, context=self.ctx) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", "replace")[:300]
            raise RuntimeError("%s %s -> HTTP %d %s" % (method, path, err.code, detail))
        except (urllib.error.URLError, socket.timeout) as err:
            raise RuntimeError("%s %s failed: %s" % (method, path, getattr(err, "reason", err)))
        return json.loads(raw) if raw else {}

    def get(self, path):
        return self.call("GET", path)

    def post(self, path, body):
        return self.call("POST", path, body)

    def put(self, path, body):
        return self.call("PUT", path, body)


# ---------------------------------------------------------------- TOFI operations

def get_engine(tofi, key="browser_engine"):
    for item in tofi.get("/api/automation-settings").get("items", []):
        if item.get("key") == key:
            return item.get("value")
    return None


def set_engine(tofi, engine, key="browser_engine"):
    tofi.put("/api/automation-settings", {"values": {key: engine}})
    got = get_engine(tofi, key)
    if got != engine:
        raise RuntimeError("%s is %r after setting %r (is TOFI_%s set on the server?)" % (key, got, engine, key.upper()))


def conversation_bot(tofi, conv):
    for item in tofi.get("/api/conversations").get("conversations", []):
        if item.get("id") == conv:
            return item.get("bot_id") or (item.get("bot_ids") or [""])[0]
    raise RuntimeError("conversation %s not found" % conv)


def shell(tofi, bot, command, timeout_sec=30):
    out = tofi.post("/api/computers/firecracker/actions", {"action": "shell.exec", "bot_id": bot, "args": {"command": command, "timeout_sec": timeout_sec}})
    result = out.get("result", out)
    if isinstance(result, str):
        try:
            result = json.loads(result)
        except ValueError:
            return result
    return (result.get("stdout") or "") if isinstance(result, dict) else str(result)


def wait_idle(tofi, conv, limit=120):
    deadline = time.time() + limit
    while time.time() < deadline:
        runs = tofi.get("/api/conversations/%s/runs?limit=5" % conv).get("runs", [])
        if all(r.get("status") in TERMINAL for r in runs):
            return
        time.sleep(POLL)
    raise RuntimeError("the conversation still has a running run")


def post_task(tofi, conv, text):
    client_id = str(uuid.uuid4())
    resp = tofi.post("/api/conversations/%s/messages" % conv, {"content": text, "client_message_id": client_id})
    msg, run = resp.get("message") or {}, resp.get("run") or {}
    deadline = time.time() + 60
    while time.time() < deadline:
        if msg.get("id") and run.get("id"):
            return msg, run
        # The answer carried no run (queued behind another): find the run the message started.
        msgs = tofi.get("/api/conversations/%s/messages?limit=40" % conv).get("messages", [])
        msg = next((m for m in msgs if m.get("client_message_id") == client_id), msg)
        runs = tofi.get("/api/conversations/%s/runs?limit=10" % conv).get("runs", [])
        run = next((r for r in runs if msg.get("id") and r.get("trigger_message_id") == msg.get("id")), run)
        time.sleep(1)
    raise RuntimeError("the message produced no run within 60 s")


def await_run(tofi, conv, run_id, timeout):
    start = time.time()
    while True:
        runs = tofi.get("/api/conversations/%s/runs?limit=10" % conv).get("runs", [])
        run = next((r for r in runs if r.get("id") == run_id), None)
        if run and run.get("status") in TERMINAL:
            return run, False
        if time.time() - start > timeout:
            try:
                tofi.post("/api/runs/%s/cancel" % run_id, {})
            except RuntimeError:
                pass
            return run or {}, True
        time.sleep(POLL)


def tool_calls(tofi, conv, run_id):
    out, offset = [], 0
    while True:
        page = tofi.get("/api/conversations/%s/tools?run_id=%s&offset=%d&limit=200" % (conv, run_id, offset))
        got = page.get("activities", [])
        out.extend(got)
        offset += len(got)
        if not page.get("has_more") or not got:
            return out


def final_reply(tofi, conv, run_id, after_seq):
    msgs = tofi.get("/api/conversations/%s/messages?limit=60" % conv).get("messages", [])
    parts = [m.get("content", "") for m in msgs if m.get("role") == "assistant" and m.get("seq", 0) > after_seq and m.get("run_id", run_id) == run_id]
    return "\n".join(p for p in parts if p)


def parse_ts(value):
    try:
        return datetime.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except (ValueError, AttributeError):
        return None


# ---------------------------------------------------------------- tasks

def short(token):
    return token[:6]


class Task:
    id = ""
    title = ""
    setting = "browser_engine"  # the engine setting this task compares

    def prompt(self, ctx):
        raise NotImplementedError

    def setup(self, ctx):
        pass

    def check(self, ctx, state, reply):
        raise NotImplementedError

    def cleanup(self, ctx):
        pass


class FormTask(Task):
    id = "form_field"
    title = "Open a site and fill a form field"

    def prompt(self, ctx):
        ctx["name"] = "Ada Probe %s" % short(ctx["token"])
        return "Open %s/form?t=%s in the browser, type \"%s\" into the Full name field and press Send. Tell me when the page says the registration was received." % (ctx["base"], ctx["token"], ctx["name"])

    def check(self, ctx, state, reply):
        got = state.forms.get(ctx["token"])
        return got == ctx["name"], "server saw %r" % got


class DeepSettingTask(Task):
    id = "deep_setting"
    title = "Find a setting deep in a web app"

    def prompt(self, ctx):
        return "The operations console is at %s/app?t=%s. Find the Data retention setting, change it to 30 days and save it. Then tell me which pages you went through." % (ctx["base"], ctx["token"])

    def check(self, ctx, state, reply):
        got = state.retention.get(ctx["token"])
        return got == "30", "saved retention %r" % got


class UploadTask(Task):
    id = "file_upload"
    title = "Upload a file through a site's file picker"

    def prompt(self, ctx):
        ctx["secret"] = "intake-%s" % uuid.uuid4().hex[:10]
        ctx["file"] = "intake-%s.txt" % short(ctx["token"])
        return "Create a text file named %s in your working directory that holds exactly this line: %s . Then open %s/upload?t=%s, choose that file with the page's document picker and submit it. Tell me when the page confirms it received the document." % (ctx["file"], ctx["secret"], ctx["base"], ctx["token"])

    def check(self, ctx, state, reply):
        got = state.uploads.get(ctx["token"])
        if not got:
            return False, "no upload arrived"
        name, body = got
        ok = name == ctx["file"] and ctx["secret"].encode() in body
        return ok, "upload %r, %d bytes, secret %s" % (name, len(body), "present" if ctx["secret"].encode() in body else "missing")


class TableTask(Task):
    id = "read_table"
    title = "Read a table into text"

    def prompt(self, ctx):
        return "Open %s/table?t=%s and write the whole table in your reply as plain text, one line per row in the form \"item: quantity\". Add one last line that starts with \"Total:\" and gives the sum of all quantities." % (ctx["base"], ctx["token"])

    def check(self, ctx, state, reply):
        text = reply.replace(",", "")
        missing = [name for name, qty in fixtures.TABLE_ROWS if not re.search(re.escape(name) + r"\W{0,6}\**\s*:?\s*\**\s*" + str(qty) + r"\b", text)]
        total = sum(q for _, q in fixtures.TABLE_ROWS)
        has_total = re.search(r"Total\W{0,6}\s*" + str(total) + r"\b", text) is not None
        return (not missing and has_total), "missing rows %s, total %s" % (missing or "none", "ok" if has_total else "wrong or absent (expected %d)" % total)


class FolderTask(Task):
    id = "desktop_folder"
    title = "Open a folder in the desktop file manager and report its files"
    setting = "desktop_engine"

    def setup(self, ctx):
        ctx["folder"] = "bench-%s" % short(ctx["token"])
        ctx["inner"] = "note-%s.txt" % short(ctx["token"])
        ctx["second"] = "memo-%s.txt" % short(ctx["token"])
        path = "/workspace/shared/%s" % ctx["folder"]
        shell(ctx["tofi"], ctx["bot"], "mkdir -p %s && echo hello > %s/%s && echo world > %s/%s" % (path, path, ctx["inner"], path, ctx["second"]))

    def prompt(self, ctx):
        return "On the computer's desktop (not in the browser), open the folder /workspace/shared/%s in the file manager window and tell me the names of the files you see inside it." % ctx["folder"]

    def check(self, ctx, state, reply):
        probe = ('for s in /tmp/.X11-unix/X*; do d=":${s##*X}"; DISPLAY=$d xdotool search --name %s 2>/dev/null; done | head -1' % ctx["folder"])
        found = shell(ctx["tofi"], ctx["bot"], probe).strip()
        named = [n for n in (ctx["inner"], ctx["second"]) if n in reply]
        return bool(found) and len(named) == 2, "window %s, %d of 2 file names in reply" % ("open" if found else "not found", len(named))

    def cleanup(self, ctx):
        try:
            shell(ctx["tofi"], ctx["bot"], "pkill -f 'pcmanfm.*%s' ; rm -rf /workspace/shared/%s" % (ctx["folder"], ctx["folder"]))
        except RuntimeError:
            pass


LIBFM_CONF = "/workspace/home/.config/libfm/libfm.conf"


class PreferenceTask(Task):
    id = "desktop_setting"
    title = "Change a setting in a GTK app's Preferences"
    setting = "desktop_engine"

    def reset(self, ctx):
        shell(ctx["tofi"], ctx["bot"], "[ -f %s ] && sed -i 's/^single_click=.*/single_click=0/' %s; true" % (LIBFM_CONF, LIBFM_CONF))

    def setup(self, ctx):
        self.reset(ctx)

    def prompt(self, ctx):
        return "On the computer's desktop (not in the browser), open the file manager's Preferences, turn on the option \"Open files with a single left-click\" and close the Preferences window. Tell me when it is on."

    def check(self, ctx, state, reply):
        got = shell(ctx["tofi"], ctx["bot"], "grep -h '^single_click=' %s 2>/dev/null | tail -1" % LIBFM_CONF).strip()
        return got == "single_click=1", "file manager saved %r" % (got or "nothing")

    def cleanup(self, ctx):
        try:
            self.reset(ctx)
        except RuntimeError:
            pass


class ReadTextTask(Task):
    id = "desktop_read"
    title = "Read text from a desktop app window"
    setting = "desktop_engine"

    def setup(self, ctx):
        ctx["folder"] = "bench-%s" % short(ctx["token"])
        ctx["file"] = "data-%s.bin" % short(ctx["token"])
        ctx["size"] = 3000 + int(ctx["token"][:6], 16) % 6000
        path = "/workspace/shared/%s" % ctx["folder"]
        shell(ctx["tofi"], ctx["bot"], "mkdir -p %s && head -c %d /dev/zero | tr '\\0' x > %s/%s" % (path, ctx["size"], path, ctx["file"]))

    def prompt(self, ctx):
        return "On the computer's desktop (not in the browser), use the file manager to open the Properties of the file /workspace/shared/%s/%s and tell me the exact size in bytes that the Properties window shows." % (ctx["folder"], ctx["file"])

    def check(self, ctx, state, reply):
        text = reply.replace(",", "").replace("\u202f", "").replace("\u00a0", "")
        ok = re.search(r"(?<!\d)%d(?!\d)" % ctx["size"], text) is not None
        return ok, "expected %d bytes, %s in reply" % (ctx["size"], "found" if ok else "not found")

    def cleanup(self, ctx):
        try:
            shell(ctx["tofi"], ctx["bot"], "for s in /tmp/.X11-unix/X*; do d=\":${s##*X}\"; for w in $(DISPLAY=$d xdotool search --name Properties 2>/dev/null); do DISPLAY=$d xdotool windowclose $w; done; done; rm -rf /workspace/shared/%s" % ctx["folder"])
        except RuntimeError:
            pass


TASKS = [FormTask(), DeepSettingTask(), UploadTask(), TableTask(), FolderTask(), PreferenceTask(), ReadTextTask()]


# ---------------------------------------------------------------- run

def run_trial(tofi, state, base, bots, convs, engine, task, index, timeout):
    conv = convs[index % len(convs)]
    ctx = {"tofi": tofi, "bot": bots[conv], "base": base, "token": uuid.uuid4().hex[:12], "engine": engine}
    row = {"engine": engine, "setting": task.setting, "task": task.id, "conversation": conv, "token": ctx["token"], "success": False, "detail": "", "steps": 0, "tool_calls": 0, "failed_calls": 0, "seconds": None, "timed_out": False, "tools": {}}
    try:
        wait_idle(tofi, conv)
        task.setup(ctx)
        started = time.time()
        msg, run = post_task(tofi, conv, PREFIX + task.prompt(ctx))
        run, timed_out = await_run(tofi, conv, run.get("id"), timeout)
        wall = time.time() - started
        t0, t1 = parse_ts(run.get("created_at")), parse_ts(run.get("updated_at"))
        row["seconds"] = round(t1 - t0, 1) if t0 and t1 and t1 >= t0 and not timed_out else round(wall, 1)
        row["timed_out"] = timed_out
        row["run_status"] = run.get("status", "")
        calls = tool_calls(tofi, conv, run.get("id"))
        counts = {}
        for call in calls:
            counts[call.get("name", "")] = counts.get(call.get("name", ""), 0) + 1
        row["tools"] = counts
        row["tool_calls"] = len(calls)
        row["steps"] = sum(1 for c in calls if ACTIVE_BOT_TOOLS.match(c.get("name", "")))
        row["failed_calls"] = sum(1 for c in calls if c.get("status") not in ("completed", "done", "running"))
        reply = final_reply(tofi, conv, run.get("id"), msg.get("seq", 0))
        ok, detail = task.check(ctx, state, reply)
        row["success"], row["detail"] = bool(ok) and not timed_out, detail + ("; timed out" if timed_out else "")
        row["reply_chars"] = len(reply)
    except Exception as err:  # keep the sweep going; the row records the failure
        row["detail"] = "harness error: %s" % err
    finally:
        task.cleanup(ctx)
    return row


def engine_label(setting, engine):
    return engine if setting == "browser_engine" else "desktop " + engine


def summarize(rows):
    lines = ["| engine | task | success | steps | tool calls | failed calls | seconds |", "|---|---|---|---|---|---|---|"]
    groups = [(setting, engine) for setting, engines in SETTINGS.items() for engine in engines]
    for setting, engine in groups:
        label = engine_label(setting, engine)
        for task in TASKS:
            sel = [r for r in rows if r["engine"] == engine and r.get("setting", "browser_engine") == setting and r["task"] == task.id]
            if not sel:
                continue
            ok = sum(1 for r in sel if r["success"])
            avg = lambda key: ("%.1f" % (sum(r[key] or 0 for r in sel) / len(sel)))
            lines.append("| %s | %s | %d/%d | %s | %s | %s | %s |" % (label, task.id, ok, len(sel), avg("steps"), avg("tool_calls"), avg("failed_calls"), avg("seconds")))
    for setting, engine in groups:
        sel = [r for r in rows if r["engine"] == engine and r.get("setting", "browser_engine") == setting]
        if sel:
            ok = sum(1 for r in sel if r["success"])
            lines.append("| %s | all | %d/%d | %.1f | %.1f | %.1f | %.1f |" % (engine_label(setting, engine), ok, len(sel), sum(r["steps"] for r in sel) / len(sel), sum(r["tool_calls"] for r in sel) / len(sel), sum(r["failed_calls"] for r in sel) / len(sel), sum(r["seconds"] or 0 for r in sel) / len(sel)))
    return "\n".join(lines)


def local_address():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("10.255.255.255", 1))
        return s.getsockname()[0]
    except OSError:
        return "127.0.0.1"
    finally:
        s.close()


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--url", required=True, help="TOFI address, for example https://10.0.10.8:8321")
    ap.add_argument("--cookie-file", required=True)
    ap.add_argument("--conversation", action="append", required=True, help="conversation id with a Bot that has a computer; repeat to spread tasks")
    ap.add_argument("--engines", default=",".join(ENGINES), help="browser engines for the browser tasks")
    ap.add_argument("--desktop-engines", default=",".join(DESKTOP_ENGINES), help="desktop engines for the desktop tasks")
    ap.add_argument("--tasks", default="", help="comma separated task ids (default all): " + ",".join(t.id for t in TASKS))
    ap.add_argument("--runs", type=int, default=1, help="trials per engine and task")
    ap.add_argument("--bind", default="0.0.0.0:8765", help="where the fixture sites listen")
    ap.add_argument("--fixture-base", default="", help="address of the fixture sites as the TOFI computer sees them (default http://<this machine>:<port>)")
    ap.add_argument("--timeout", type=int, default=600, help="seconds per trial before the run is cancelled")
    ap.add_argument("--out", default="", help="output directory (default bench-out/<time>)")
    ap.add_argument("--insecure", action="store_true", help="do not verify the server certificate")
    ap.add_argument("--cafile", default="")
    ap.add_argument("--keep-engine", action="store_true", help="leave the engine settings as the last trial set them")
    ap.add_argument("--dry-run", action="store_true", help="check access, setting and fixtures, then stop")
    args = ap.parse_args(argv)

    engines = [e.strip() for e in args.engines.split(",") if e.strip()]
    for e in engines:
        if e not in ENGINES:
            ap.error("unknown engine " + e)
    desktop_engines = [e.strip() for e in args.desktop_engines.split(",") if e.strip()]
    for e in desktop_engines:
        if e not in DESKTOP_ENGINES:
            ap.error("unknown desktop engine " + e)
    chosen = {"browser_engine": engines, "desktop_engine": desktop_engines}
    wanted = {t.strip() for t in args.tasks.split(",") if t.strip()}
    tasks = [t for t in TASKS if not wanted or t.id in wanted]
    if not tasks:
        ap.error("no task selected")

    host, _, port = args.bind.rpartition(":")
    server, state = fixtures.serve(host or "0.0.0.0", int(port))
    base = (args.fixture_base or "http://%s:%s" % (local_address(), port)).rstrip("/")
    tofi = Tofi(args.url, read_cookie(args.cookie_file), insecure=args.insecure, cafile=args.cafile or None)
    out_dir = args.out or os.path.join("bench-out", datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
    os.makedirs(out_dir, exist_ok=True)
    rows = []
    originals = {}
    try:
        for setting in sorted({t.setting for t in tasks}):
            originals[setting] = get_engine(tofi, setting)
            if originals[setting] is None:
                raise SystemExit("this TOFI has no %s setting (needs a build with that engine)" % setting)
        bots = {c: conversation_bot(tofi, c) for c in args.conversation}
        print("TOFI %s, settings %s, fixtures at %s, output %s" % (args.url, originals, base, out_dir), flush=True)
        if args.dry_run:
            return 0
        index = 0
        with open(os.path.join(out_dir, "trials.jsonl"), "w", encoding="utf-8") as sink:
            for setting in originals:
                for engine in chosen[setting]:
                    set_engine(tofi, engine, setting)
                    for _ in range(args.runs):
                        for task in [t for t in tasks if t.setting == setting]:
                            row = run_trial(tofi, state, base, bots, args.conversation, engine, task, index, args.timeout)
                            index += 1
                            rows.append(row)
                            sink.write(json.dumps(row) + "\n")
                            sink.flush()
                            print("%-14s %-15s %s steps=%d calls=%d failed=%d %ss  %s" % (engine_label(setting, engine), task.id, "OK  " if row["success"] else "FAIL", row["steps"], row["tool_calls"], row["failed_calls"], row["seconds"], row["detail"]), flush=True)
    finally:
        if not args.keep_engine:
            for setting, original in originals.items():
                try:
                    set_engine(tofi, original, setting)
                except Exception as err:
                    print("could not restore %s=%s: %s" % (setting, original, err), file=sys.stderr)
        server.shutdown()
    table = summarize(rows)
    with open(os.path.join(out_dir, "summary.md"), "w", encoding="utf-8") as f:
        f.write(table + "\n")
    print("\n" + table)
    return 0


if __name__ == "__main__":
    sys.exit(main())
