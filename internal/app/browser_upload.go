package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// browser.upload puts files into a page's file input through DevTools, so a Bot
// never has to drive the system file dialog. Playwright's setInputFiles and
// its filechooser event are the model: a named input gets DOM.setFileInputFiles
// directly; otherwise file chooser interception is switched on, the Bot's
// trigger element is clicked, and the chooser that opens is answered with the
// node Chrome reports (Page.fileChooserOpened.backendNodeId). Interception is
// switched off again whatever happens. Like browser.read it is a bounded
// python helper beside Chrome, so it needs no guest change: an older guest
// runs it all the same, and a guest without python3-websocket fails before
// anything is dispatched.
const (
	browserUploadMaxFiles = 10
	browserUploadMaxFile  = 50 << 20
	browserUploadMaxTotal = 100 << 20
	browserUploadRoot     = "/workspace"
	browserUploadWaitSec  = 8
	// browserUploadBotDirs is where a relative path starts, as for publish_file.
	browserUploadBotDirs = browserUploadRoot + "/bots"
)

// browserUploadLogic is the part of the helper with no side effects at import,
// so the tests can run it against a fake DevTools socket.
const browserUploadLogic = `
import os, stat
class UploadError(Exception):
    def __init__(self, code, detail):
        Exception.__init__(self, detail)
        self.code, self.detail = code, detail
# Never uploadable: the browser profile (cookies, saved logins) and key stores.
UPLOAD_DENY_TOP = (".tofi-env", "browser")
UPLOAD_DENY_ANY = (".ssh", ".gnupg", ".aws")
def check_files(opts):
    root = os.path.realpath(opts["root"])
    names = opts.get("files") or []
    if not names:
        raise UploadError("upload_no_files", "upload needs at least one file")
    if len(names) > opts["max_files"]:
        raise UploadError("upload_too_many_files", "at most %d files per upload" % opts["max_files"])
    out, total = [], 0
    for name in names:
        if not isinstance(name, str) or not name or "\0" in name or not name.startswith("/"):
            raise UploadError("upload_bad_path", "file paths must be absolute paths on the computer")
        # realpath resolves every symlink, so a link that leaves the workspace is refused.
        real = os.path.realpath(name)
        if not real.startswith(root + os.sep):
            raise UploadError("upload_outside_workspace", "%s is not inside %s" % (name, opts["root"]))
        parts = real[len(root) + 1:].split(os.sep)
        if parts[0] in UPLOAD_DENY_TOP or any(p in UPLOAD_DENY_ANY for p in parts):
            raise UploadError("upload_forbidden_path", "%s holds browser or key material and cannot be uploaded" % name)
        try:
            st = os.stat(real)
        except OSError:
            raise UploadError("upload_file_not_found", "%s does not exist" % name)
        if not stat.S_ISREG(st.st_mode):
            raise UploadError("upload_not_a_file", "%s is not a regular file" % name)
        if st.st_size > opts["max_file"]:
            raise UploadError("upload_file_too_large", "%s is %d bytes; the limit is %d per file" % (name, st.st_size, opts["max_file"]))
        total += st.st_size
        out.append(real)
    if total > opts["max_total"]:
        raise UploadError("upload_file_too_large", "the files total %d bytes; the limit is %d" % (total, opts["max_total"]))
    return out
class Cdp:
    # A DevTools session that keeps the events it sees while waiting for a reply.
    def __init__(self, ws):
        self.ws, self.n, self.events = ws, 0, []
    def call(self, method, params=None):
        self.n += 1
        self.ws.send(json.dumps({"id": self.n, "method": method, "params": params or {}}))
        while True:
            msg = json.loads(self.ws.recv())
            if msg.get("id") == self.n:
                if "error" in msg:
                    raise UploadError("upload_devtools_error", "%s: %s" % (method, (msg["error"] or {}).get("message")))
                return msg.get("result", {})
            if "method" in msg:
                self.events.append(msg)
    def wait_event(self, method, seconds):
        deadline = time.time() + seconds
        while True:
            for i, e in enumerate(self.events):
                if e.get("method") == method:
                    return self.events.pop(i)
            left = deadline - time.time()
            if left <= 0:
                return None
            self.ws.settimeout(left)
            try:
                msg = json.loads(self.ws.recv())
            except Exception:
                return None
            if "method" in msg:
                self.events.append(msg)
FIND_INPUT = r"""((sel) => {
  let el = document.querySelector(sel);
  if (el && el.tagName === 'LABEL' && el.control) el = el.control;
  if (el && !(el.tagName === 'INPUT' && el.type === 'file')) el = el.querySelector && el.querySelector('input[type=file]');
  return el || null;
})(%s)"""
def attached_count(cdp, object_id):
    try:
        r = cdp.call("Runtime.callFunctionOn", {"objectId": object_id, "functionDeclaration": "function(){return this.files ? this.files.length : -1}", "returnByValue": True})
        return r.get("result", {}).get("value")
    except UploadError:
        return None
def run_upload(cdp, opts, files, dispatched):
    if opts.get("selector"):
        r = cdp.call("Runtime.evaluate", {"expression": FIND_INPUT % json.dumps(opts["selector"]), "returnByValue": False})
        if r.get("exceptionDetails"):
            raise UploadError("upload_bad_selector", "the selector is not valid CSS")
        obj = r.get("result", {})
        if not obj.get("objectId") or obj.get("subtype") == "null":
            raise UploadError("upload_input_not_found", "no file input matches that selector; it must match an input[type=file] or a label or wrapper around one")
        if len(files) > 1:
            m = cdp.call("Runtime.callFunctionOn", {"objectId": obj["objectId"], "functionDeclaration": "function(){return !!this.multiple}", "returnByValue": True})
            if not m.get("result", {}).get("value"):
                raise UploadError("upload_single_file_only", "that file input takes one file; send files one at a time")
        dispatched()
        cdp.call("DOM.setFileInputFiles", {"files": files, "objectId": obj["objectId"]})
        return {"mode": "input", "attached": attached_count(cdp, obj["objectId"])}
    cdp.call("Page.enable")
    cdp.call("Page.setInterceptFileChooserDialog", {"enabled": True})
    try:
        spot = cdp.call("Runtime.evaluate", {"expression": LOCATE % json.dumps(opts["click"]), "returnByValue": True}).get("result", {}).get("value")
        if not spot:
            raise UploadError("click_target_not_found", opts["click"])
        time.sleep(opts.get("settle", 0.2))
        dispatched()
        for kind in ("mouseMoved", "mousePressed", "mouseReleased"):
            cdp.call("Input.dispatchMouseEvent", {"type": kind, "x": spot["x"], "y": spot["y"], "button": "left", "clickCount": 1})
        ev = cdp.wait_event("Page.fileChooserOpened", opts["wait"])
        if not ev:
            raise UploadError("upload_no_file_chooser", "clicking %r did not open a file chooser; click the element that opens it, or pass selector for the file input" % spot.get("label"))
        params = ev.get("params", {})
        node = params.get("backendNodeId")
        if not node:
            raise UploadError("upload_no_file_chooser", "the file chooser named no input element")
        if len(files) > 1 and params.get("mode") != "selectMultiple":
            raise UploadError("upload_single_file_only", "that file chooser takes one file; send files one at a time")
        cdp.call("DOM.setFileInputFiles", {"files": files, "backendNodeId": node})
        attached = None
        try:
            attached = attached_count(cdp, cdp.call("DOM.resolveNode", {"backendNodeId": node}).get("object", {}).get("objectId"))
        except UploadError:
            pass
        return {"mode": "chooser", "clicked": spot.get("label"), "attached": attached}
    finally:
        try:
            cdp.call("Page.setInterceptFileChooserDialog", {"enabled": False})
        except Exception:
            pass
`

const browserUploadBody = `
try:
    files = check_files(opts)
    out = run_upload(Cdp(target), opts, files, lambda: (sys.stderr.write("TOFI_DISPATCHED\n"), sys.stderr.flush()))
    time.sleep(0.4)
    out["files"] = [os.path.basename(f) for f in files]
    out["url"] = evaluate(target, "location.href")
    out["title"] = evaluate(target, "document.title")
except UploadError as e:
    out = {"error": e.code, "detail": e.detail}
target.close()
print(json.dumps(out, ensure_ascii=False))
`

const browserUploadScript = browserScriptHead + browserUploadLogic + browserUploadBody

type browserUploadArgs struct {
	// Files are paths on the computer; a relative path starts in the Bot's directory.
	Files []string `json:"files,omitempty"`
	// AttachmentIDs name files the person sent in this conversation.
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	// Selector names the file input (or a label or wrapper around it).
	Selector string `json:"selector,omitempty"`
	// Click is the visible text of the element that opens the file chooser.
	Click  string `json:"click,omitempty"`
	Effect string `json:"effect,omitempty"`
}

// browserUploadPaths turns the Bot's file list into absolute paths under the
// workspace root. The helper checks the same rule again on the real paths
// (symlinks resolved), which only the computer can do.
func browserUploadPaths(botID string, files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.TrimSpace(f) == "" || len(f) > 4096 || strings.ContainsRune(f, 0) {
			return nil, tooloutcome.InvalidArguments("browser.upload: every file needs a path of at most 4096 characters")
		}
		if !path.IsAbs(f) {
			f = path.Join(browserUploadBotDirs, botID, f)
		}
		f = path.Clean(f)
		if !strings.HasPrefix(f, browserUploadRoot+"/") {
			return nil, tooloutcome.InvalidArguments("browser.upload: files must be inside " + browserUploadRoot + "; copy the file there first")
		}
		out = append(out, f)
	}
	return out, nil
}

// browserUploadCommand is the shell.exec payload for one upload.
func browserUploadCommand(files []string, selector, click string) (json.RawMessage, error) {
	opts, err := json.Marshal(map[string]any{
		"files": files, "selector": strings.TrimSpace(selector), "click": strings.TrimSpace(click),
		"root": browserUploadRoot, "max_files": browserUploadMaxFiles, "max_file": browserUploadMaxFile,
		"max_total": browserUploadMaxTotal, "wait": browserUploadWaitSec,
	})
	if err != nil {
		return nil, err
	}
	const tag = "TOFI_BROWSER_UPLOAD"
	command := "python3 - " + base64.StdEncoding.EncodeToString(opts) + " <<'" + tag + "'\n" + browserUploadScript + "\n" + tag
	return json.Marshal(map[string]any{"command": command, "timeout_sec": 45})
}

// browserUploadResult unwraps the shell envelope and turns the helper's
// error codes into outcomes the model can act on.
func browserUploadResult(out string) (string, error) {
	page, err := browserReadResult(out)
	if err != nil {
		return "", err
	}
	var res struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(page), &res) != nil || res.Error == "" {
		return page, nil
	}
	switch res.Error {
	case "upload_no_file_chooser", "upload_devtools_error":
		return "", fmt.Errorf("browser.upload: %s", res.Detail)
	}
	return "", tooloutcome.InvalidArguments("browser.upload: " + res.Detail)
}

// browserUpload checks the request, stages attachments on the computer, has an
// outgoing-data action reviewed like any other send, and runs the helper.
func (s *Server) browserUpload(ctx context.Context, r Run, raw json.RawMessage) (page string, err error) {
	var in browserUploadArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", tooloutcome.InvalidArguments("browser.upload: invalid arguments")
	}
	if (strings.TrimSpace(in.Selector) == "") == (strings.TrimSpace(in.Click) == "") {
		return "", tooloutcome.InvalidArguments("browser.upload needs exactly one of selector (the file input) or click (the visible text of the element that opens the file chooser)")
	}
	if len(in.Files)+len(in.AttachmentIDs) == 0 {
		return "", tooloutcome.InvalidArguments("browser.upload needs files or attachment_ids")
	}
	if len(in.Files)+len(in.AttachmentIDs) > browserUploadMaxFiles {
		return "", tooloutcome.InvalidArguments(fmt.Sprintf("browser.upload takes at most %d files", browserUploadMaxFiles))
	}
	files, err := browserUploadPaths(r.BotID, in.Files)
	if err != nil {
		return "", err
	}
	staged, err := s.stageUploadAttachments(ctx, r, in.AttachmentIDs)
	if err != nil {
		return "", err
	}
	files = append(files, staged...)
	// Sending a file to a site is outgoing data whatever the model says: it is
	// reviewed as a send unless the model declared something stronger.
	effect := in.Effect
	if !consequentialEffect(effect) {
		effect = "send"
	}
	var target actionTarget
	label := strings.TrimSpace(in.Click)
	if label != "" {
		target = s.locateAction(ctx, r, browserReadArgs{Click: in.Click, Dry: true})
	} else {
		target = s.locateAction(ctx, r, browserReadArgs{Probe: []float64{-1, -1, 1, 1}})
		label = "file input " + strings.TrimSpace(in.Selector)
	}
	if target.Label == "" {
		target.Label = label
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = path.Base(f)
	}
	settle, err := s.guardAction(ctx, r, actionReview{Kind: "upload", Effect: effect, Element: target.Label, Text: "upload " + strings.Join(names, ", "), URL: target.URL, Title: target.Title})
	if err != nil {
		return "", err
	}
	defer func() { settle(err) }()
	args, err := browserUploadCommand(files, in.Selector, in.Click)
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
	return browserUploadResult(out)
}

// stageUploadAttachments copies the named chat attachments onto the computer,
// where the conversation's other uploads already are, and returns their paths.
func (s *Server) stageUploadAttachments(ctx context.Context, r Run, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list, err := s.store.guestInputCandidates(r.ConversationID, r.TriggerMessageID)
	if err != nil {
		return nil, err
	}
	paths := guestInputPaths(r.ConversationID, list)
	byID := map[string]guestInputCandidate{}
	for _, c := range list {
		byID[c.ID] = c
	}
	var out []string
	for _, id := range ids {
		c, ok := byID[strings.TrimSpace(id)]
		if !ok {
			return nil, tooloutcome.InvalidArguments("browser.upload: attachment " + id + " is not a file sent in this conversation")
		}
		if c.Size > browserUploadMaxFile {
			return nil, tooloutcome.InvalidArguments(fmt.Sprintf("browser.upload: attachment %s is larger than %d MB", id, browserUploadMaxFile>>20))
		}
		if _, err := s.pushGuestInput(ctx, r, c.Attachment, paths[c.ID]); err != nil {
			return nil, fmt.Errorf("browser.upload: could not copy attachment %s to the computer: %w", id, err)
		}
		out = append(out, paths[c.ID])
	}
	return out, nil
}
