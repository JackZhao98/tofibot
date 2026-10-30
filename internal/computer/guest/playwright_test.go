package guest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(tool, args string) playwrightCallArgs {
	return playwrightCallArgs{Tool: tool, Arguments: json.RawMessage(args)}
}

func TestPlaywrightRelayRefusesToolsAndArgumentsOutsideTheAllowlist(t *testing.T) {
	root := t.TempDir()
	for name, in := range map[string]playwrightCallArgs{
		"evaluate":       call("browser_evaluate", `{"function":"1"}`),
		"run code":       call("browser_run_code_unsafe", `{"code":"x"}`),
		"unknown tool":   call("browser_install", `{}`),
		"file name":      call("browser_snapshot", `{"filename":"/etc/x"}`),
		"extra argument": call("browser_click", `{"target":"e1","script":"x"}`),
		"file url":       call("browser_navigate", `{"url":"file:///etc/passwd"}`),
		"javascript url": call("browser_navigate", `{"url":"javascript:alert(1)"}`),
		"tab file url":   call("browser_tabs", `{"action":"new","url":"file:///etc/passwd"}`),
		"not an object":  call("browser_snapshot", `[1]`),
	} {
		if _, err := checkPlaywrightCall(root, in); err == nil {
			t.Errorf("%s was relayed", name)
		}
	}
	args, err := checkPlaywrightCall(root, call("browser_click", `{"target":"e1","element":"Pay","doubleClick":true}`))
	if err != nil || args["target"] != "e1" {
		t.Fatalf("a valid click was refused: %v", err)
	}
	if _, err := checkPlaywrightCall(root, call("browser_navigate", `{"url":"https://example.org/a?b=1"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPlaywrightUploadsStayInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := mk("bots/b1/report.pdf", "pdf")
	profile := mk("browser/Default/Cookies", "secret")
	key := mk("home/.ssh/id_ed25519", "key")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bots", "b1", "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	upload := func(paths ...string) error {
		raw, _ := json.Marshal(map[string]any{"paths": paths})
		_, err := checkPlaywrightCall(root, call("browser_file_upload", string(raw)))
		return err
	}
	if err := upload(ok); err != nil {
		t.Fatalf("a workspace file was refused: %v", err)
	}
	for name, p := range map[string]string{"profile": profile, "ssh key": key, "outside": outside, "symlink out": link, "relative": "bots/b1/report.pdf", "missing": filepath.Join(root, "nope"), "directory": filepath.Join(root, "bots")} {
		if err := upload(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := upload(); err == nil {
		t.Error("an empty upload was accepted")
	}
}

func TestPlaywrightTidyInlinesTheSnapshotFileAndDropsTheScriptEcho(t *testing.T) {
	root := t.TempDir()
	dir := playwrightOutputDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "page-1.yml")
	if err := os.WriteFile(file, []byte("- button \"Pay\" [ref=e7]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "home", "secret.yml")
	if err := os.WriteFile(secret, []byte("do not read"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := "### Ran Playwright code\n```js\nawait page.click('x');\n```\n### Page\n- Page URL: http://x/\n### Snapshot\n- [Snapshot](home/.cache/playwright-mcp/page-1.yml)\n"
	out := playwrightTidy(root, in)
	if strings.Contains(out, "Ran Playwright code") || strings.Contains(out, "page.click") {
		t.Fatalf("script echo kept:\n%s", out)
	}
	if !strings.Contains(out, "### Page") || !strings.Contains(out, "[ref=e7]") || strings.Contains(out, "[Snapshot](") {
		t.Fatalf("snapshot not inlined:\n%s", out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the snapshot file was left behind")
	}
	// A link that leaves the output directory is not followed.
	link := "- [Snapshot](home/secret.yml)"
	if got := playwrightTidy(root, "### Snapshot\n"+link+"\n"); !strings.Contains(got, link) || strings.Contains(got, "do not read") {
		t.Fatalf("a link outside the output directory was followed:\n%s", got)
	}
	if got := playwrightTidy(root, "### Snapshot\n- [Snapshot](../../../etc/passwd)\n"); strings.Contains(got, "root:") {
		t.Fatal("path traversal")
	}
}

func TestPlaywrightResultKeepsTextAndCountsImages(t *testing.T) {
	raw := json.RawMessage(`{"isError":false,"content":[{"type":"text","text":"### Page"},{"type":"image","data":"AAAA","mimeType":"image/png"},{"type":"text","text":"- button [ref=e1]"}]}`)
	got, err := playwrightResult(t.TempDir(), "browser_snapshot", raw)
	if err != nil {
		t.Fatal(err)
	}
	if got["text"] != "### Page\n- button [ref=e1]" || got["images_omitted"] != 1 || got["is_error"] != false {
		t.Fatalf("result %v", got)
	}
	long := `{"content":[{"type":"text","text":"` + strings.Repeat("a", playwrightResultCap+10) + `"}]}`
	got, _ = playwrightResult(t.TempDir(), "browser_snapshot", json.RawMessage(long))
	if got["truncated"] != true || len(got["text"].(string)) > playwrightResultCap+200 {
		t.Fatalf("a long page was not cut: %v", got["truncated"])
	}
	bad, _ := playwrightResult(t.TempDir(), "browser_click", json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"Error: no"}]}`))
	if bad["is_error"] != true {
		t.Fatal("tool error lost")
	}
}
