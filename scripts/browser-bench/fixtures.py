"""Fixture sites and checks for the browser engine benchmark.

One small HTTP server (standard library only) serves five pages. Every page reports what the Bot did to the
server, per run token, so success is decided by the server's own record and never by a model's opinion.

Pages (all take ?t=<run token>):
  /form     a form with a "Full name" field and a Send button          -> records the posted name
  /app      a small console: Home, Settings > Privacy > Advanced, with a
            "Data retention" select that is saved by its own button     -> records the saved retention value
  /upload   a page whose file input is hidden behind a styled button,
            so the system file chooser has to be used                   -> records the uploaded bytes
  /table    a table of 14 rows rendered by script after load           -> checked against the Bot's reply text
The desktop task has no page: the harness makes a folder on the computer and asks the computer for window titles.
"""
import hashlib
import html
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

TABLE_ROWS = [
    ("Bolt M6", 120), ("Nut M6", 340), ("Washer 6mm", 515), ("Screw 4x20", 98), ("Anchor 8mm", 41), ("Rivet 3mm", 760),
    ("Clamp 25mm", 33), ("Hinge 60mm", 57), ("Bracket L", 82), ("Spring 12mm", 206), ("Pin 5x30", 149), ("Cap 22mm", 311),
    ("Hook 40mm", 64), ("Strap 300mm", 27),
]

COMMON = "<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width'><style>body{font:16px sans-serif;margin:2rem;max-width:44rem}label{display:block;margin:.7rem 0}nav a{margin-right:1rem}</style>"


def token_of(path_qs):
    return (parse_qs(urlparse(path_qs).query).get("t") or [""])[0]


class State:
    """What the Bot did, per run token."""

    def __init__(self):
        self.lock = threading.Lock()
        self.forms = {}
        self.retention = {}
        self.uploads = {}
        self.hits = {}

    def hit(self, token, page):
        with self.lock:
            self.hits.setdefault(token, set()).add(page)


def make_handler(state):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def send(self, body, status=200, ctype="text/html; charset=utf-8"):
            data = body.encode() if isinstance(body, str) else body
            self.send_response(status)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(data)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            path = urlparse(self.path).path
            t = html.escape(token_of(self.path))
            if path == "/health":
                return self.send("ok", ctype="text/plain")
            state.hit(token_of(self.path), path)
            if path == "/form":
                return self.send(COMMON + f"<title>Visitor registration</title><h1>Visitor registration</h1><form method=post action='/form?t={t}'><label>Full name <input name=full_name autocomplete=off></label><label>Company <input name=company></label><button type=submit>Send</button></form>")
            if path == "/form/done":
                return self.send(COMMON + "<title>Thanks</title><h1>Registration received</h1>")
            if path == "/app":
                return self.send(COMMON + f"<title>Ops console</title><h1>Ops console</h1><nav><a href='/app?t={t}'>Home</a><a href='/app/settings?t={t}'>Settings</a><a href='/app/reports?t={t}'>Reports</a></nav><p>Welcome back. Pick a section.</p>")
            if path == "/app/reports":
                return self.send(COMMON + f"<title>Reports</title><h1>Reports</h1><nav><a href='/app?t={t}'>Home</a><a href='/app/settings?t={t}'>Settings</a></nav><p>No reports yet.</p>")
            if path == "/app/settings":
                return self.send(COMMON + f"<title>Settings</title><h1>Settings</h1><nav><a href='/app?t={t}'>Home</a></nav><ul><li><a href='/app/settings/profile?t={t}'>Profile</a></li><li><a href='/app/settings/privacy?t={t}'>Privacy</a></li><li><a href='/app/settings/billing?t={t}'>Billing</a></li></ul>")
            if path in ("/app/settings/profile", "/app/settings/billing"):
                return self.send(COMMON + f"<title>Settings</title><h1>{html.escape(path.rsplit('/', 1)[1].title())}</h1><p>Nothing to change here.</p><a href='/app/settings?t={t}'>Back</a>")
            if path == "/app/settings/privacy":
                return self.send(COMMON + f"<title>Privacy</title><h1>Privacy</h1><p>Cookies: essential only.</p><p><a href='/app/settings/privacy/advanced?t={t}'>Advanced privacy options</a></p><a href='/app/settings?t={t}'>Back</a>")
            if path == "/app/settings/privacy/advanced":
                return self.send(COMMON + f"<title>Advanced privacy</title><h1>Advanced privacy</h1><form method=post action='/app/settings/privacy/advanced?t={t}'><label>Data retention <select name=retention><option value=7>7 days</option><option value=14>14 days</option><option value=30>30 days</option><option value=90 selected>90 days</option></select></label><button type=submit>Save changes</button></form>")
            if path == "/upload":
                return self.send(COMMON + f"""<title>Document intake</title><h1>Document intake</h1>
<input type=file id=f hidden>
<button type=button id=pick onclick="document.getElementById('f').click()">Choose document</button>
<span id=name>No document chosen</span>
<button type=button id=send disabled>Submit document</button><p id=out></p>
<script>
const f=document.getElementById('f'),send=document.getElementById('send');
f.addEventListener('change',()=>{{document.getElementById('name').textContent=f.files[0]?f.files[0].name:'No document chosen';send.disabled=!f.files[0]}});
send.addEventListener('click',async()=>{{const r=await fetch('/upload?t={t}&name='+encodeURIComponent(f.files[0].name),{{method:'POST',body:f.files[0]}});document.getElementById('out').textContent=r.ok?'Document received':'Upload failed'}});
</script>""")
            if path == "/table":
                rows = json.dumps(TABLE_ROWS)
                return self.send(COMMON + f"<title>Stock levels</title><h1>Stock levels</h1><table id=t border=1 cellpadding=6><thead><tr><th>Item</th><th>On hand</th></tr></thead><tbody></tbody></table><script>setTimeout(()=>{{const b=document.querySelector('tbody');for(const [n,q] of {rows}){{const r=b.insertRow();r.insertCell().textContent=n;r.insertCell().textContent=q}}}},400)</script>")
            self.send("not found", 404, "text/plain")

        def do_POST(self):
            parsed = urlparse(self.path)
            token = token_of(self.path)
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(min(length, 8 << 20))
            if parsed.path == "/form":
                data = parse_qs(body.decode("utf-8", "replace"))
                with state.lock:
                    state.forms[token] = (data.get("full_name") or [""])[0]
                self.send_response(303)
                self.send_header("Location", "/form/done")
                self.end_headers()
                return
            if parsed.path == "/app/settings/privacy/advanced":
                data = parse_qs(body.decode("utf-8", "replace"))
                with state.lock:
                    state.retention[token] = (data.get("retention") or [""])[0]
                return self.send(COMMON + "<title>Saved</title><h1>Saved</h1><p>Your privacy settings were saved.</p>")
            if parsed.path == "/upload":
                name = (parse_qs(parsed.query).get("name") or [""])[0]
                with state.lock:
                    state.uploads[token] = (name, body)
                return self.send("ok", ctype="text/plain")
            self.send("not found", 404, "text/plain")

    return Handler


def serve(bind_host, port):
    state = State()
    server = ThreadingHTTPServer((bind_host, port), make_handler(state))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, state


def digest(data):
    return hashlib.sha256(data).hexdigest()
