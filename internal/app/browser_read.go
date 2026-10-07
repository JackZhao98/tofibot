package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"regexp"
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
        # Only the shared desktop Chrome, never a headless browser a Bot started.
        if m and any(x.startswith(b"--user-data-dir=") and x.rstrip(b"/").endswith(b"/browser/shared") for x in args):
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
  const budget = find && matches.length ? Math.min(max, 3000) : max;
  return {title: document.title, url: location.href, truncated: text.length > budget, chars: text.length, text: text.slice(0, budget), matches, links};
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
VISIBLE = "document.visibilityState === 'visible'"
def connect(t):
    return websocket.create_connection(t["webSocketDebuggerUrl"], timeout=10, suppress_origin=True)
def focused(pages):
    for t in pages:
        try:
            ws = connect(t)
        except Exception:
            continue
        if evaluate(ws, VISIBLE):
            return t, ws
        ws.close()
    return None, None
page_target, target = focused(pages)
if target is None:
    print(json.dumps({"error": "no_focused_page", "tabs": [{"title": t.get("title"), "url": t.get("url"), "target_id": t.get("id")} for t in pages]}, ensure_ascii=False)); sys.exit(0)
out = {}
if opts.get("probe"):
    sx, sy, sw, sh = opts["probe"]
    label = evaluate(target, r"""(() => {
      const kx = screen.width / %f, ky = screen.height / %f;
      const chromeX = (outerWidth - innerWidth) / 2, chromeY = outerHeight - innerHeight - chromeX;
      const el = document.elementFromPoint(%f * kx - screenX - chromeX, %f * ky - screenY - chromeY);
      const hit = el && el.closest('a,button,[role=button],[role=link],[role=menuitem],input[type=submit],input[type=button],[onclick]');
      return hit ? ((hit.innerText || hit.value || hit.getAttribute('aria-label') || '').trim().slice(0, 200)) : '';
    })()""" % (sw, sh, sx, sy))
    print(json.dumps({"label": label or ""}, ensure_ascii=False)); sys.exit(0)
if opts.get("click") and opts.get("dry"):
    spot = evaluate(target, LOCATE % json.dumps(opts["click"]))
    print(json.dumps({"label": spot["label"]} if spot else {"error": "click_target_not_found", "click": opts["click"]}, ensure_ascii=False)); sys.exit(0)
if opts.get("click"):
    spot = evaluate(target, LOCATE % json.dumps(opts["click"]))
    if not spot:
        print(json.dumps({"error": "click_target_not_found", "click": opts["click"]}, ensure_ascii=False)); sys.exit(0)
    time.sleep(0.2)
    for kind in ("mouseMoved", "mousePressed", "mouseReleased"):
        send(target, "Input.dispatchMouseEvent", {"type": kind, "x": spot["x"], "y": spot["y"], "button": "left", "clickCount": 1})
    out["clicked"] = spot["label"]
    before = {t.get("id") for t in pages}
    time.sleep(0.6)
    fresh = [t for t in json.load(urllib.request.urlopen("http://127.0.0.1:%d/json/list" % port, timeout=5)) if t.get("type") == "page" and t.get("webSocketDebuggerUrl") and t.get("id") not in before]
    if fresh:
        target.close()
        target = connect(fresh[0])
        out["opened_new_tab"] = True
    for _ in range(25):
        if evaluate(target, "document.readyState") == "complete":
            break
        time.sleep(0.2)
    time.sleep(0.4)
# Web apps (Gmail) re-render after a hash navigation; read once the text settles.
last = -1
for _ in range(7):
    size = evaluate(target, "(document.body && document.body.innerText || '').length + ':' + location.href")
    if size == last:
        break
    last = size
    time.sleep(0.3)
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
	// Dry locates the click target without clicking.
	Dry bool `json:"-"`
	// Probe is a screen point [x, y, screenshot_width, screenshot_height]
	// whose page element label is returned.
	Probe []float64 `json:"-"`
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
	opts, err := json.Marshal(map[string]any{"max": budget, "find": strings.TrimSpace(in.Find), "click": strings.TrimSpace(in.Click), "dry": in.Dry, "probe": in.Probe})
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
	_ = s.renewComputerHold(ctx, r) // reading still counts as using the desktop
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
	if err := s.guardConsequentialClick(ctx, r, browserReadArgs{Click: in.Click, Dry: true}); err != nil {
		return "", err
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
	if strings.Contains(page, `"error": "no_focused_page"`) {
		return "", tooloutcome.InvalidArguments("browser: no tab is in the foreground; switch to the right tab with browser.action switch (tabs: " + page + ")")
	}
	if strings.Contains(page, `"error": "click_target_not_found"`) {
		return "", tooloutcome.InvalidArguments("browser.click: no visible element contains that text; read the page and use exact visible text")
	}
	return page, nil
}

// Clicks that commit something outside the page (buy, pay, send, delete,
// submit, publish...) need the user's approval in this run, whatever tool
// performs them; reading and browsing never do. Owner rule 2026-10-07: risk is
// judged by what the action does, not by which tool does it.
var consequentialClick = regexp.MustCompile(`(?i)\b(buy|purchase|pay|checkout|check out|place order|confirm order|order now|send|submit|delete|remove|transfer|publish|post|book now|reserve|unsubscribe|cancel subscription)\b|购买|下单|支付|付款|结算|提交|发送|删除|转账|发布|预订|确认订单|确认支付`)

func consequentialLabel(label string) bool { return consequentialClick.MatchString(label) }

// guardConsequentialClick resolves the element a click would hit and refuses
// a consequential one unless a person approved that target in this run.
func (s *Server) guardConsequentialClick(ctx context.Context, r Run, probe browserReadArgs) error {
	args, err := browserReadCommand(probe)
	if err != nil {
		return err
	}
	out, err := s.microVMAction(ctx, r, "shell.exec", args)
	if err != nil {
		return nil // an unreadable page cannot be classified; the click itself reports
	}
	page, err := browserReadResult(out)
	if err != nil {
		return nil
	}
	var hit struct {
		Label string `json:"label"`
	}
	if json.Unmarshal([]byte(page), &hit) != nil || !consequentialLabel(hit.Label) || s.clickApproved(r, hit.Label) {
		return nil
	}
	label := hit.Label
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	return tooloutcome.New(tooloutcome.NeedApproval, "user_confirmation_required", "not_executed",
		fmt.Sprintf("This click on %q would commit an action outside the page (buy, pay, send, delete, submit or publish). Ask the user with request_approval (target: %q) and click again only after they approve.", label, label),
		"request_approval").Err()
}

// clickApproved reports a human-approved request_approval in this run whose
// action or target names the clicked element.
func (s *Server) clickApproved(r Run, label string) bool {
	questions, err := s.store.ListQuestions(r.ConversationID)
	if err != nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(label))
	for _, q := range questions {
		if q.RunID != r.ID || q.Approval == nil || q.Approval.Review != nil || q.Status != questionAnswered || strings.TrimSpace(string(q.Answer)) != "true" || q.AnsweredBy == autoReviewActor {
			continue
		}
		named := strings.ToLower(q.Approval.Target + " " + q.Approval.Action)
		if want != "" && (strings.Contains(named, want) || strings.Contains(want, strings.ToLower(strings.TrimSpace(q.Approval.Target)))) {
			return true
		}
	}
	return false
}
