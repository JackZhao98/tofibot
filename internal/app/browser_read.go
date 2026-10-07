package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"strings"
)

// browser.read returns the focused page's text and links, so a Bot reads a
// page the way a person skims it instead of transcribing screenshots. It runs
// as a bounded shell helper beside Chrome (the image ships python3-websocket),
// finds the live DevTools port from Chrome's own command line, and only reads.
const (
	browserReadDefaultChars = 20000
	browserReadMaxChars     = 60000
	browserReadMinChars     = 12000
)

const browserReadScript = `
import base64, glob, json, re, sys, time, urllib.request, websocket
opts = json.loads(base64.b64decode(sys.argv[1]))
port = None
for path in glob.glob("/proc/[0-9]*/cmdline"):
    try:
        args = open(path, "rb").read().split(b"\0")
    except Exception:
        continue
    for a in args:
        m = re.match(rb"--remote-debugging-port=(\d+)$", a)
        if m and any(b"--user-data-dir=" in x for x in args):
            port = int(m.group(1))
    if port:
        break
if not port:
    print(json.dumps({"error": "chrome_not_running"})); sys.exit(0)
pages = [t for t in json.load(urllib.request.urlopen("http://127.0.0.1:%d/json/list" % port, timeout=5)) if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
seq = [0]
def send(ws, method, params):
    seq[0] += 1
    ws.send(json.dumps({"id": seq[0], "method": method, "params": params}))
    while True:
        msg = json.loads(ws.recv())
        if msg.get("id") == seq[0]:
            return msg.get("result", {})
def evaluate(ws, expr):
    return send(ws, "Runtime.evaluate", {"expression": expr, "returnByValue": True}).get("result", {}).get("value")
EXTRACT = r"""(() => {
  const max = %d, find = %s;
  const root = document.querySelector('main, article, [role=main]') || document.body;
  let text = (root && root.innerText || '').replace(/\n{3,}/g, '\n\n').trim();
  if (root !== document.body && text.length < 400) text = (document.body.innerText || '').replace(/\n{3,}/g, '\n\n').trim();
  let matches = [];
  if (find) {
    const lower = text.toLowerCase(), needle = find.toLowerCase();
    for (let i = lower.indexOf(needle); i >= 0 && matches.length < 20; i = lower.indexOf(needle, i + needle.length))
      matches.push(text.slice(Math.max(0, i - 300), i + needle.length + 300));
  }
  const seen = new Set(), links = [];
  for (const a of document.querySelectorAll('a[href]')) {
    const label = (a.innerText || a.getAttribute('aria-label') || '').trim().replace(/\s+/g, ' ');
    if (!label || label.length < 3 || seen.has(a.href) || !/^https?:/.test(a.href)) continue;
    seen.add(a.href); links.push({text: label.slice(0, 160), url: a.href});
    if (links.length >= 80) break;
  }
  return {title: document.title, url: location.href, truncated: text.length > max, chars: text.length, text: text.slice(0, max), matches, links};
})()"""
LOCATE = r"""((needle) => {
  needle = needle.toLowerCase();
  const sel = 'a,button,[role=button],[role=link],[role=row],[role=option],[role=tab],[role=menuitem],tr,li,summary,label,[onclick],[tabindex]';
  let best = null;
  for (const el of document.querySelectorAll(sel)) {
    const label = ((el.innerText || '') + ' ' + (el.getAttribute('aria-label') || '')).toLowerCase();
    if (!label.includes(needle)) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) continue;
    if (!best || label.length < best.len) best = {el, len: label.length};
  }
  if (!best) return null;
  best.el.scrollIntoView({block: 'center', inline: 'center'});
  const r = best.el.getBoundingClientRect();
  return {x: r.left + r.width / 2, y: r.top + r.height / 2, label: (best.el.innerText || best.el.getAttribute('aria-label') || '').trim().slice(0, 200)};
})(%s)"""
VISIBLE = "document.visibilityState === 'visible' && document.hasFocus()"
target = None
for t in pages:
    try:
        ws = websocket.create_connection(t["webSocketDebuggerUrl"], timeout=10, suppress_origin=True)
    except Exception:
        continue
    if target is None or evaluate(ws, VISIBLE):
        if target:
            target.close()
        target = ws
        if evaluate(ws, VISIBLE):
            break
    else:
        ws.close()
if target is None:
    print(json.dumps({"error": "no_readable_page"})); sys.exit(0)
out = {}
if opts.get("click"):
    spot = evaluate(target, LOCATE % json.dumps(opts["click"]))
    if not spot:
        print(json.dumps({"error": "click_target_not_found", "click": opts["click"]}, ensure_ascii=False)); sys.exit(0)
    time.sleep(0.2)
    for kind in ("mouseMoved", "mousePressed", "mouseReleased"):
        send(target, "Input.dispatchMouseEvent", {"type": kind, "x": spot["x"], "y": spot["y"], "button": "left", "clickCount": 1})
    out["clicked"] = spot["label"]
    time.sleep(1.5)
page = evaluate(target, EXTRACT % (opts["max"], json.dumps(opts.get("find") or "")))
target.close()
out.update(page or {"error": "no_readable_page"})
print(json.dumps(out, ensure_ascii=False))
`

type browserReadArgs struct {
	Find     string `json:"find,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
	// Click is visible text to click before reading (browser.click).
	Click string `json:"click,omitempty"`
}

// browserReadCommand is the shell.exec payload for one bounded page read.
func browserReadCommand(in browserReadArgs) (json.RawMessage, error) {
	budget := in.MaxChars
	if budget <= 0 {
		budget = browserReadDefaultChars
	}
	// Models ask for tiny budgets and then miss the part of a mail or article
	// they needed; a whole ordinary page fits in the floor.
	budget = min(max(budget, browserReadMinChars), browserReadMaxChars)
	opts, err := json.Marshal(map[string]any{"max": budget, "find": strings.TrimSpace(in.Find), "click": strings.TrimSpace(in.Click)})
	if err != nil {
		return nil, err
	}
	command := "python3 - " + base64.StdEncoding.EncodeToString(opts) + " <<'TOFI_BROWSER_READ'\n" + browserReadScript + "\nTOFI_BROWSER_READ"
	return json.Marshal(map[string]any{"command": command, "timeout_sec": 30})
}

func (s *Server) browserRead(ctx context.Context, r Run, raw json.RawMessage) (string, error) {
	var in browserReadArgs
	_ = json.Unmarshal(raw, &in)
	args, err := browserReadCommand(in)
	if err != nil {
		return "", err
	}
	out, err := s.microVMAction(ctx, r, "shell.exec", args)
	if err != nil {
		return "", err
	}
	return browserReadResult(out)
}

// browserClick clicks the smallest visible element containing the given text
// on the focused page through DevTools, then returns the resulting page like
// browser.read. It takes the shared desktop like any other page input.
func (s *Server) browserClick(ctx context.Context, r Run, raw json.RawMessage) (string, error) {
	var in browserReadArgs
	_ = json.Unmarshal(raw, &in)
	if strings.TrimSpace(in.Click) == "" {
		return "", tooloutcome.InvalidArguments("browser.click needs click text")
	}
	args, err := browserReadCommand(in)
	if err != nil {
		return "", err
	}
	release, err := s.acquireDesktop(ctx, r)
	if err != nil {
		return "", err
	}
	defer release()
	out, err := s.microVMActionOnLease(ctx, r, "shell.exec", args)
	if err != nil {
		return "", err
	}
	return browserReadResult(out)
}

// browserReadResult unwraps the shell envelope into the page JSON.
func browserReadResult(out string) (string, error) {
	var shell struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if json.Unmarshal([]byte(out), &shell) != nil {
		return out, nil
	}
	page := strings.TrimSpace(shell.Stdout)
	if shell.ExitCode != 0 || !json.Valid([]byte(page)) {
		return "", fmt.Errorf("browser.read failed: %s", strings.TrimSpace(shell.Stderr+" "+page))
	}
	if strings.Contains(page, `"error": "chrome_not_running"`) || strings.Contains(page, `"error":"chrome_not_running"`) {
		return "", fmt.Errorf("browser.read: Chrome is not running; start it with computer_desktop desktop.start")
	}
	if strings.Contains(page, `"error": "click_target_not_found"`) {
		return "", tooloutcome.InvalidArguments("browser.click: no visible element contains that text; read the page and use exact visible text")
	}
	return page, nil
}
