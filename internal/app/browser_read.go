package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// browser.read returns the focused page's text and links, so a Bot reads a
// page the way a person skims it instead of transcribing screenshots. It runs
// as a bounded shell helper beside Chrome (the image ships python3-websocket),
// finds the live DevTools port from Chrome's own command line, and only reads.
const (
	browserReadDefaultChars = 20000
	browserReadMaxChars     = 60000
)

const browserReadScript = `
import base64, glob, json, re, sys, urllib.request, websocket
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
def call(ws, expr):
    ws.send(json.dumps({"id": 1, "method": "Runtime.evaluate", "params": {"expression": expr, "returnByValue": True}}))
    while True:
        msg = json.loads(ws.recv())
        if msg.get("id") == 1:
            return msg.get("result", {}).get("result", {}).get("value")
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
  return {title: document.title, url: location.href, visible: document.visibilityState === 'visible' && document.hasFocus(),
    truncated: text.length > max, chars: text.length, text: text.slice(0, max), matches, links};
})()"""
expr = EXTRACT % (opts["max"], json.dumps(opts.get("find") or ""))
best = None
for t in pages:
    try:
        ws = websocket.create_connection(t["webSocketDebuggerUrl"], timeout=10, suppress_origin=True)
        try:
            page = call(ws, expr)
        finally:
            ws.close()
    except Exception:
        continue
    if not page:
        continue
    if page.get("visible"):
        best = page; break
    if best is None:
        best = page
print(json.dumps(best or {"error": "no_readable_page"}, ensure_ascii=False))
`

type browserReadArgs struct {
	Find     string `json:"find,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// browserReadCommand is the shell.exec payload for one bounded page read.
func browserReadCommand(in browserReadArgs) (json.RawMessage, error) {
	max := in.MaxChars
	if max <= 0 {
		max = browserReadDefaultChars
	}
	max = min(max, browserReadMaxChars)
	opts, err := json.Marshal(map[string]any{"max": max, "find": strings.TrimSpace(in.Find)})
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
	return page, nil
}
