package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestBrowserUploadPaths(t *testing.T) {
	got, err := browserUploadPaths("bot1", []string{"a/b.png", "/workspace/home/uploads/c/x.pdf", "/workspace/bots/bot1/../bot2/y.txt"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/workspace/bots/bot1/a/b.png", "/workspace/home/uploads/c/x.pdf", "/workspace/bots/bot2/y.txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	for _, bad := range []string{"/etc/passwd", "/workspace/../etc/passwd", "../../../etc/passwd", "/workspacefoo/x", "", "  ", "a\x00b", "/workspace"} {
		if _, err := browserUploadPaths("bot1", []string{bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		} else if o, ok := tooloutcome.FromError(err); !ok || o.Code != "invalid_arguments" {
			t.Errorf("%q: want invalid arguments, got %v", bad, err)
		}
	}
}

func TestBrowserUploadArgumentValidation(t *testing.T) {
	s := &Server{}
	cases := map[string]string{
		"neither selector nor click": `{"files":["/workspace/a.png"]}`,
		"both selector and click":    `{"files":["/workspace/a.png"],"selector":"input","click":"Upload"}`,
		"no files":                   `{"selector":"input"}`,
		"outside workspace":          `{"files":["/etc/passwd"],"selector":"input"}`,
		"too many":                   `{"files":["a","b","c","d","e","f","g","h","i","j","k"],"selector":"input"}`,
	}
	for name, body := range cases {
		_, err := s.browserUpload(context.Background(), Run{BotID: "b"}, json.RawMessage(body))
		if err == nil {
			t.Errorf("%s: must be refused", name)
			continue
		}
		if o, ok := tooloutcome.FromError(err); !ok || o.Code != "invalid_arguments" {
			t.Errorf("%s: want invalid arguments, got %v", name, err)
		}
	}
}

func TestBrowserUploadCommandCarriesLimitsAndOwnTag(t *testing.T) {
	raw, err := browserUploadCommand([]string{"/workspace/a.png"}, " input[type=file] ", "")
	if err != nil {
		t.Fatal(err)
	}
	var shell struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if json.Unmarshal(raw, &shell) != nil || shell.TimeoutSec != 45 || !strings.Contains(shell.Command, "<<'TOFI_BROWSER_UPLOAD'") {
		t.Fatalf("shell=%+v", shell)
	}
	// Not a page read: its heredoc tag must not trigger the tab-cap notes.
	if strings.Contains(shell.Command, "TOFI_BROWSER_READ") {
		t.Fatal("upload must not use the page-read tag")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.Fields(shell.Command)[2])
	if err != nil {
		t.Fatal(err)
	}
	var opts struct {
		Files    []string `json:"files"`
		Selector string   `json:"selector"`
		Root     string   `json:"root"`
		MaxFile  int64    `json:"max_file"`
		MaxTotal int64    `json:"max_total"`
	}
	if json.Unmarshal(decoded, &opts) != nil || opts.Selector != "input[type=file]" || opts.Root != "/workspace" || opts.MaxFile != browserUploadMaxFile || opts.MaxTotal != browserUploadMaxTotal || len(opts.Files) != 1 {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestBrowserUploadResultCodes(t *testing.T) {
	wrap := func(stdout string) string {
		b, _ := json.Marshal(map[string]any{"stdout": stdout, "exit_code": 0})
		return string(b)
	}
	if got, err := browserUploadResult(wrap(`{"mode":"input","attached":1}`)); err != nil || !strings.Contains(got, `"attached":1`) {
		t.Fatalf("got=%q err=%v", got, err)
	}
	_, err := browserUploadResult(wrap(`{"error":"upload_file_too_large","detail":"big"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "invalid_arguments" || !strings.Contains(err.Error(), "big") {
		t.Fatalf("validation errors are invalid arguments: %v", err)
	}
	_, err = browserUploadResult(wrap(`{"error":"upload_no_file_chooser","detail":"nothing opened"}`))
	if err == nil || strings.Contains(err.Error(), "invalid") {
		t.Fatalf("a click that opened nothing is not an argument error: %v", err)
	}
	if computerRecoveryIdentity("bot", microVMComputerID, "browser.upload", json.RawMessage(`{"files":["/workspace/a"],"selector":"i"}`)).Risk == "observation" {
		t.Fatal("browser.upload sends data; it must not be an observation")
	}
}

// pyHarness runs the helper's side-effect-free logic against a scripted DevTools
// socket and prints what it sent and what came back.
const pyHarness = `
import json, sys, time, os, tempfile
LOCATE = "LOCATE(%s)"
exec(open(sys.argv[1]).read())
case = json.loads(sys.argv[2])
class FakeWS:
    def __init__(self):
        self.sent, self.queue = [], []
    def send(self, raw):
        m = json.loads(raw); self.sent.append(m)
        method, res, events = m["method"], {}, []
        if method == "Runtime.evaluate":
            if "LOCATE" in m["params"]["expression"]:
                res = {"result": {"value": None if case.get("nolocate") else {"x": 10, "y": 20, "label": "Add photo"}}}
            else:
                res = {"result": {"type": "object", "objectId": "obj-1"}}
                if case.get("noinput"):
                    res = {"result": {"type": "object", "subtype": "null", "value": None}}
        elif method == "Runtime.callFunctionOn":
            fn = m["params"]["functionDeclaration"]
            res = {"result": {"value": case.get("multiple", True) if "multiple" in fn else len(case["files"])}}
        elif method == "DOM.resolveNode":
            res = {"object": {"objectId": "obj-2"}}
        elif method == "Input.dispatchMouseEvent" and m["params"]["type"] == "mouseReleased" and not case.get("nochooser"):
            events = [{"method": "Page.fileChooserOpened", "params": {"frameId": "f", "mode": case.get("mode", "selectMultiple"), "backendNodeId": 77}}]
        self.queue.extend(events)
        self.queue.append({"id": m["id"], "result": res})
    def recv(self):
        if not self.queue:
            raise TimeoutError()
        return json.dumps(self.queue.pop(0))
    def settimeout(self, t):
        pass
ws = FakeWS()
opts = {"selector": case.get("selector", ""), "click": case.get("click", ""), "wait": 0.2, "settle": 0}
marks = []
out = {}
try:
    out = run_upload(Cdp(ws), opts, case["files"], lambda: marks.append(1))
except UploadError as e:
    out = {"error": e.code}
print(json.dumps({"sent": [[m["method"], m.get("params", {})] for m in ws.sent], "out": out, "dispatched": len(marks)}))
`

type pyRun struct {
	Sent       [][]any        `json:"sent"`
	Out        map[string]any `json:"out"`
	Dispatched int            `json:"dispatched"`
}

func runUploadPython(t *testing.T, body string, args ...string) []byte {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	logic := filepath.Join(dir, "logic.py")
	if err := os.WriteFile(logic, []byte(browserUploadLogic), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "h.py")
	if err := os.WriteFile(harness, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, append([]string{harness, logic}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, out)
	}
	return out
}

func uploadCase(t *testing.T, c map[string]any) pyRun {
	t.Helper()
	enc, _ := json.Marshal(c)
	var r pyRun
	out := runUploadPython(t, pyHarness, string(enc))
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return r
}

func methods(r pyRun) []string {
	var m []string
	for _, s := range r.Sent {
		m = append(m, s[0].(string))
	}
	return m
}

func TestBrowserUploadDirectInputSequence(t *testing.T) {
	r := uploadCase(t, map[string]any{"selector": "input[type=file]", "files": []string{"/workspace/a.png", "/workspace/b.png"}})
	want := "Runtime.evaluate,Runtime.callFunctionOn,DOM.setFileInputFiles,Runtime.callFunctionOn"
	if got := strings.Join(methods(r), ","); got != want {
		t.Fatalf("sequence %s, want %s", got, want)
	}
	set := r.Sent[2][1].(map[string]any)
	if set["objectId"] != "obj-1" || len(set["files"].([]any)) != 2 {
		t.Fatalf("set files params: %v", set)
	}
	if r.Out["mode"] != "input" || r.Out["attached"] != float64(2) || r.Dispatched != 1 {
		t.Fatalf("out=%v dispatched=%d", r.Out, r.Dispatched)
	}
	// No chooser interception for a named input.
	for _, m := range methods(r) {
		if strings.HasPrefix(m, "Page.") {
			t.Fatalf("direct input must not touch Page: %v", methods(r))
		}
	}
}

func TestBrowserUploadDirectInputRefusals(t *testing.T) {
	r := uploadCase(t, map[string]any{"selector": "x", "files": []string{"/workspace/a"}, "noinput": true})
	if r.Out["error"] != "upload_input_not_found" || r.Dispatched != 0 {
		t.Fatalf("out=%v", r.Out)
	}
	r = uploadCase(t, map[string]any{"selector": "x", "files": []string{"/workspace/a", "/workspace/b"}, "multiple": false})
	if r.Out["error"] != "upload_single_file_only" || r.Dispatched != 0 {
		t.Fatalf("out=%v", r.Out)
	}
}

func TestBrowserUploadChooserSequence(t *testing.T) {
	r := uploadCase(t, map[string]any{"click": "Add photo", "files": []string{"/workspace/a.png"}})
	want := "Page.enable,Page.setInterceptFileChooserDialog,Runtime.evaluate," +
		"Input.dispatchMouseEvent,Input.dispatchMouseEvent,Input.dispatchMouseEvent," +
		"DOM.setFileInputFiles,DOM.resolveNode,Runtime.callFunctionOn,Page.setInterceptFileChooserDialog"
	if got := strings.Join(methods(r), ","); got != want {
		t.Fatalf("sequence\n got %s\nwant %s", got, want)
	}
	if r.Sent[1][1].(map[string]any)["enabled"] != true {
		t.Fatal("interception must be switched on first")
	}
	if set := r.Sent[6][1].(map[string]any); set["backendNodeId"] != float64(77) {
		t.Fatalf("files must go to the node Chrome named: %v", set)
	}
	if last := r.Sent[len(r.Sent)-1][1].(map[string]any); last["enabled"] != false {
		t.Fatal("interception must be switched off at the end")
	}
	if r.Out["mode"] != "chooser" || r.Out["clicked"] != "Add photo" || r.Dispatched != 1 {
		t.Fatalf("out=%v", r.Out)
	}
}

func TestBrowserUploadChooserAlwaysSwitchesInterceptionOff(t *testing.T) {
	cases := []map[string]any{
		{"click": "Add photo", "files": []string{"/workspace/a"}, "nochooser": true},
		{"click": "Add photo", "files": []string{"/workspace/a", "/workspace/b"}, "mode": "selectSingle"},
		{"click": "Nope", "files": []string{"/workspace/a"}, "nolocate": true},
	}
	codes := []string{"upload_no_file_chooser", "upload_single_file_only", "click_target_not_found"}
	for i, c := range cases {
		r := uploadCase(t, c)
		if r.Out["error"] != codes[i] {
			t.Errorf("case %d: out=%v", i, r.Out)
		}
		m := r.Sent[len(r.Sent)-1]
		if m[0] != "Page.setInterceptFileChooserDialog" || m[1].(map[string]any)["enabled"] != false {
			t.Errorf("case %d: interception left on, last=%v", i, m)
		}
	}
}

const pyFilesHarness = `
import json, sys, os
exec(open(sys.argv[1]).read())
root = sys.argv[2]
base = {"root": root, "max_files": 2, "max_file": 100, "max_total": 150}
def check(files, **kw):
    o = dict(base); o.update(kw); o["files"] = files
    try:
        return [os.path.relpath(f, os.path.realpath(root)) for f in check_files(o)]
    except UploadError as e:
        return e.code
os.makedirs(root + "/browser/shared"); os.makedirs(root + "/bots/b"); os.makedirs(root + "/home/.ssh")
outside = sys.argv[3]
open(outside + "/secret", "w").write("s")
def w(p, n): open(root + p, "w").write("x" * n)
w("/bots/b/ok.txt", 10); w("/bots/b/big.bin", 101); w("/bots/b/m1", 90); w("/bots/b/m2", 90)
w("/browser/shared/Cookies", 5); w("/home/.ssh/id", 5)
os.symlink(outside + "/secret", root + "/bots/b/link-out")
os.symlink(root + "/bots/b/ok.txt", root + "/bots/b/link-in")
os.symlink("/etc", root + "/bots/b/dir-out")
print(json.dumps({
  "ok": check([root + "/bots/b/ok.txt"]),
  "link_in": check([root + "/bots/b/link-in"]),
  "link_out": check([root + "/bots/b/link-out"]),
  "via_dir_link": check([root + "/bots/b/dir-out/hosts"]),
  "dotdot": check([root + "/bots/b/../../../etc/hosts"]),
  "relative": check(["bots/b/ok.txt"]),
  "missing": check([root + "/bots/b/none"]),
  "dir": check([root + "/bots"]),
  "big": check([root + "/bots/b/big.bin"]),
  "total": check([root + "/bots/b/m1", root + "/bots/b/m2"]),
  "many": check([root + "/bots/b/ok.txt"] * 3),
  "none": check([]),
  "profile": check([root + "/browser/shared/Cookies"]),
  "ssh": check([root + "/home/.ssh/id"]),
}))
`

func TestBrowserUploadFileSafety(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	outside := t.TempDir()
	out := runUploadPython(t, pyFilesHarness, root, outside)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string]any{
		"ok": []any{"bots/b/ok.txt"}, "link_in": []any{"bots/b/ok.txt"}, "relative": "upload_bad_path",
		"link_out": "upload_outside_workspace", "via_dir_link": "upload_outside_workspace", "dotdot": "upload_outside_workspace",
		"missing": "upload_file_not_found", "dir": "upload_not_a_file", "big": "upload_file_too_large", "total": "upload_file_too_large",
		"many": "upload_too_many_files", "none": "upload_no_files", "profile": "upload_forbidden_path", "ssh": "upload_forbidden_path",
	}
	for k, w := range want {
		g, _ := json.Marshal(got[k])
		e, _ := json.Marshal(w)
		if string(g) != string(e) {
			t.Errorf("%s: got %s want %s", k, g, e)
		}
	}
}

func TestBrowserUploadScriptIsOneProgram(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	// The assembled helper must at least compile: head, logic and body together.
	for name, script := range map[string]string{"upload": browserUploadScript, "read": browserReadScript} {
		cmd := exec.Command(py, "-c", "import sys; compile(sys.stdin.read(), 'helper', 'exec')")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s helper does not compile: %v\n%s", name, err, out)
		}
	}
}
