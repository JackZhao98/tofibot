package guest

// Playwright MCP engine (decision 2026-10-29): the guest runs Microsoft's
// @playwright/mcp as a stdio MCP server, started on the first call and attached
// to the shared Chrome through its DevTools endpoint (--cdp-endpoint), so the
// browser profile and its logins are the ones the person already uses. This
// file holds the parts with no OS dependency: the tool allowlist, the argument
// checks and the result shape. The process lives in playwright_linux.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// playwrightMCPDir holds the pinned package, installed by the image build
	// from deploy/microvm/playwright-mcp (package.json + lock file).
	playwrightMCPDirDefault = "/usr/local/lib/tofi-playwright"
	// playwrightResultCap bounds one tool result; a longer page is cut with a note.
	playwrightResultCap = 200 * 1024

	playwrightUploadMaxFiles = 10
	playwrightUploadMaxFile  = 50 << 20
	playwrightUploadMaxTotal = 100 << 20
)

// playwrightTools is the allowlist of server tools the guest relays and, for
// each, the argument names it passes on. Everything else is refused here, so
// a newer package cannot widen the surface by itself: no script evaluation, no
// network or console dump, no file-name arguments (the server would write
// where the Bot names).
var playwrightTools = map[string][]string{
	"browser_navigate":      {"url"},
	"browser_navigate_back": {},
	"browser_snapshot":      {"target", "depth"},
	"browser_click":         {"element", "target", "doubleClick", "button", "modifiers"},
	"browser_type":          {"element", "target", "text", "submit", "slowly"},
	"browser_fill_form":     {"fields"},
	"browser_select_option": {"element", "target", "values"},
	"browser_press_key":     {"key"},
	"browser_hover":         {"element", "target"},
	"browser_drag":          {"startElement", "startTarget", "endElement", "endTarget"},
	"browser_wait_for":      {"time", "text", "textGone"},
	"browser_tabs":          {"action", "index", "url"},
	"browser_file_upload":   {"paths"},
	"browser_handle_dialog": {"accept", "promptText"},
}

// playwrightMCPDir is replaced only by tests.
var playwrightMCPDir = playwrightMCPDirDefault

type playwrightCallArgs struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// checkPlaywrightCall validates one relayed call and returns the arguments to
// send. root is the workspace root, used to confine uploads.
func checkPlaywrightCall(root string, in playwrightCallArgs) (map[string]any, error) {
	allowed, ok := playwrightTools[in.Tool]
	if !ok {
		return nil, rejectedf("unsupported browser tool %q", in.Tool)
	}
	args := map[string]any{}
	if len(in.Arguments) > 0 && string(in.Arguments) != "null" {
		if err := json.Unmarshal(in.Arguments, &args); err != nil {
			return nil, rejectedf("arguments must be a JSON object")
		}
	}
	for key := range args {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return nil, rejectedf("%s does not take %q", in.Tool, key)
		}
	}
	switch in.Tool {
	case "browser_navigate":
		raw, _ := args["url"].(string)
		if _, err := validateURL(raw); err != nil {
			return nil, rejected(err)
		}
	case "browser_tabs":
		if raw, ok := args["url"].(string); ok && raw != "" {
			if _, err := validateURL(raw); err != nil {
				return nil, rejected(err)
			}
		}
	case "browser_file_upload":
		paths, err := checkPlaywrightUploads(root, args["paths"])
		if err != nil {
			return nil, err
		}
		args["paths"] = paths
	}
	return args, nil
}

// checkPlaywrightUploads applies the rules of the DevTools upload helper:
// absolute paths that resolve, symlinks followed, inside the workspace and
// outside the browser profile and key stores; regular files within the size
// limits.
func checkPlaywrightUploads(root string, value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return nil, rejectedf("upload needs at least one file path")
	}
	if len(list) > playwrightUploadMaxFiles {
		return nil, rejectedf("at most %d files per upload", playwrightUploadMaxFiles)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	var out []string
	var total int64
	for _, item := range list {
		name, _ := item.(string)
		if name == "" || strings.ContainsRune(name, 0) || !filepath.IsAbs(name) {
			return nil, rejectedf("file paths must be absolute paths on the Bot computer")
		}
		real, err := filepath.EvalSymlinks(name)
		if err != nil {
			return nil, rejectedf("%s does not exist", name)
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, rejectedf("%s is not inside %s", name, root)
		}
		parts := strings.Split(rel, string(filepath.Separator))
		denied := parts[0] == ".tofi-env" || parts[0] == "browser"
		for _, p := range parts {
			if p == ".ssh" || p == ".gnupg" || p == ".aws" {
				denied = true
			}
		}
		if denied {
			return nil, rejectedf("%s holds browser or key material and cannot be uploaded", name)
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, rejectedf("%s does not exist", name)
		}
		if !info.Mode().IsRegular() {
			return nil, rejectedf("%s is not a regular file", name)
		}
		if info.Size() > playwrightUploadMaxFile {
			return nil, rejectedf("%s is %d bytes; the limit is %d per file", name, info.Size(), playwrightUploadMaxFile)
		}
		total += info.Size()
		out = append(out, real)
	}
	if total > playwrightUploadMaxTotal {
		return nil, rejectedf("the files total %d bytes; the limit is %d", total, playwrightUploadMaxTotal)
	}
	return out, nil
}

// playwrightResult is what the host receives: the server's text (the page
// snapshot with element references, or the action's note), whether the server
// called it an error, and how many images were left out.
func playwrightResult(root, tool string, raw json.RawMessage) (map[string]any, error) {
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("browser engine returned an invalid result: %w", err)
	}
	var text strings.Builder
	images := 0
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(c.Text)
		case "image":
			images++
		}
	}
	out := playwrightTidy(root, text.String())
	truncated := false
	if len(out) > playwrightResultCap {
		out = strings.ToValidUTF8(out[:playwrightResultCap], "") + "\n[result cut: the page is longer than the limit; narrow it with target or depth]"
		truncated = true
	}
	result := map[string]any{"tool": tool, "text": out, "is_error": res.IsError}
	if truncated {
		result["truncated"] = true
	}
	if images > 0 {
		result["images_omitted"] = images
	}
	return result, nil
}

// playwrightOutputDir is where the server writes automatically named files
// (the page snapshot after an action), below the workspace root.
func playwrightOutputDir(root string) string {
	return filepath.Join(root, "home", ".cache", "playwright-mcp")
}

var (
	playwrightSnapshotLink = regexp.MustCompile(`(?m)^- \[Snapshot\]\(([^)\n]+)\)[ \t]*$`)
	playwrightCodeSection  = regexp.MustCompile(`(?s)### Ran Playwright code\n.*?(\n### |\z)`)
)

const playwrightSnapshotMax = 1 << 20

// playwrightTidy makes the text self-contained for the model. After an action
// the server saves the page snapshot to a file and answers with a link; the
// guest reads that file (only below the output directory) and puts the
// snapshot in place of the link, then removes the file, so the model needs no
// extra call to see the new page. The echo of the server's own script is dropped.
func playwrightTidy(root, text string) string {
	text = playwrightCodeSection.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasSuffix(m, "\n### ") {
			return "### "
		}
		return ""
	})
	dir, err := filepath.EvalSymlinks(playwrightOutputDir(root))
	if err != nil {
		return text
	}
	return playwrightSnapshotLink.ReplaceAllStringFunc(text, func(m string) string {
		link := playwrightSnapshotLink.FindStringSubmatch(m)[1]
		name := link
		if !filepath.IsAbs(name) {
			name = filepath.Join(root, name)
		}
		real, err := filepath.EvalSymlinks(name)
		if err != nil || filepath.Dir(real) != dir {
			return m
		}
		f, err := os.Open(real)
		if err != nil {
			return m
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, playwrightSnapshotMax))
		if err != nil {
			return m
		}
		_ = os.Remove(real)
		return "```yaml\n" + strings.TrimRight(string(body), "\n") + "\n```"
	})
}

var errPlaywrightMissing = errors.New("browser engine is not installed in this Bot computer image; use the classic browser engine or update the Bot computer image")

// playwrightEndpoint is the DevTools address of the shared Chrome.
func playwrightEndpoint(port int) string {
	u := url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	return u.String()
}
