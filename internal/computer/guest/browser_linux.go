//go:build linux

package guest

// Browser control deliberately stays separate from the desktop lifecycle. The
// desktop owns Chrome/Xvfb processes; this file owns the target list and the
// small CDP surface needed to operate the visible page.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type browserTarget struct {
	Description          string `json:"description"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl"`
	FaviconURL           string `json:"faviconUrl"`
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type browserPage struct {
	TargetID             string `json:"target_id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Loading              bool   `json:"loading"`
	ReadyState           string `json:"ready_state"`
	Visible              bool   `json:"visible"`
	Focused              bool   `json:"focused"`
	WebSocketDebuggerURL string `json:"-"`
}

type browserPageState struct {
	Title           string `json:"title"`
	URL             string `json:"url"`
	VisibilityState string `json:"visibility_state"`
	HasFocus        bool   `json:"has_focus"`
	ReadyState      string `json:"ready_state"`
}

type browserPagesResult struct {
	Pages         []browserPage `json:"tabs"`
	Current       *browserPage  `json:"current"`
	CurrentSource string        `json:"current_source"`
}

// browserNavigate keeps the existing action.go contract. The default target
// is resolved from the currently focused/visible page, never from JSON order.
func (s *Service) browserNavigate(ctx context.Context, botID, raw string) (map[string]any, error) {
	value, err := s.browserControl(ctx, botID, browserCommand{Action: "navigate", URL: raw})
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("browser navigation returned an invalid result")
	}
	return result, nil
}

func (s *Service) browserSnapshot(ctx context.Context, botID string) (any, error) {
	return s.browserControl(ctx, botID, browserCommand{Action: "snapshot"})
}

func (s *Service) browserAction(ctx context.Context, botID, action, rawURL string) (any, error) {
	return s.browserControl(ctx, botID, browserCommand{Action: action, URL: rawURL})
}

// browserControl is used by the action dispatcher once it accepts target_id.
// Supported actions: navigate (current page by default), snapshot, new,
// switch and close. Explicit target operations are brought to the front and
// verified through CDP before a result is returned.
func (s *Service) browserControl(ctx context.Context, botID string, command browserCommand) (any, error) {
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	switch command.Action {
	case "snapshot":
		return s.browserSnapshotPages(ctx, botID, d, command.TargetID)
	case "navigate":
		return s.browserNavigatePage(ctx, botID, d, command.URL, command.TargetID)
	case "new":
		return s.browserNewPage(ctx, botID, d, command.URL)
	case "switch":
		return s.browserSwitchPage(ctx, d, command.TargetID)
	case "close":
		return s.browserClosePage(ctx, botID, d, command.TargetID)
	default:
		return nil, errors.New("browser action supports navigate, snapshot, new, switch and close")
	}
}

func (s *Service) browserSnapshotPages(ctx context.Context, botID string, d *desktop, targetID string) (map[string]any, error) {
	pages, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, err
	}
	current, source := chooseCurrentPage(pages)
	if targetID != "" {
		page, ok := findBrowserPage(pages, targetID)
		if !ok {
			return nil, fmt.Errorf("browser target %q was not found", targetID)
		}
		if current == nil || current.TargetID != page.TargetID || source != "focused" {
			return nil, fmt.Errorf("browser snapshot target_id %q is not the current focused page; use browser.action switch first", targetID)
		}
		source = "requested"
	}
	image, err := s.desktopCapture(ctx, botID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"tabs":           pages,
		"current":        current,
		"current_source": source,
	}
	for key, value := range image {
		result[key] = value
	}
	return result, nil
}

func (s *Service) browserNavigatePage(ctx context.Context, botID string, d *desktop, rawURL, targetID string) (map[string]any, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	pages, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, err
	}
	target, err := resolveBrowserTarget(pages, targetID)
	if err != nil {
		return nil, err
	}
	if err := s.bringBrowserPageToFront(ctx, d, target); err != nil {
		return nil, err
	}
	navigateResult, err := runCDPCommand(ctx, target.WebSocketDebuggerURL, d.remotePort, "Page.navigate", map[string]any{"url": u.String()})
	if err != nil {
		return nil, err
	}
	var nav struct {
		ErrorText  string `json:"errorText"`
		FrameID    string `json:"frameId"`
		LoaderID   string `json:"loaderId"`
		IsDownload bool   `json:"isDownload"`
	}
	if err := json.Unmarshal(navigateResult, &nav); err != nil {
		return nil, fmt.Errorf("decode Page.navigate result: %w", err)
	}
	if nav.ErrorText != "" {
		return nil, fmt.Errorf("browser navigation failed: %s", nav.ErrorText)
	}
	verified, verifyErr := s.waitForBrowserPage(ctx, d, target.TargetID, target.URL)
	if verifyErr != nil {
		return nil, verifyErr
	}
	// A complete old document is not proof that the new navigation committed.
	navigationConfirmed := !nav.IsDownload
	if nav.LoaderID != "" {
		navigationConfirmed = browserLoaderMatches(ctx, target.WebSocketDebuggerURL, d.remotePort, nav.LoaderID)
	}
	loaded := navigationConfirmed && !verified.Loading && verified.ReadyState == "complete"
	return map[string]any{
		"action":               "navigate",
		"navigation_confirmed": navigationConfirmed,
		"target_id":            verified.TargetID,
		"title":                verified.Title,
		"url":                  verified.URL,
		"loading":              verified.Loading,
		"ready_state":          verified.ReadyState,
		"frame_id":             nav.FrameID,
		"loader_id":            nav.LoaderID,
		"is_download":          nav.IsDownload,
		"verified":             loaded,
	}, nil
}

func (s *Service) browserNewPage(ctx context.Context, botID string, d *desktop, rawURL string) (map[string]any, error) {
	targetURL := "about:blank"
	if strings.TrimSpace(rawURL) != "" {
		u, err := validateURL(rawURL)
		if err != nil {
			return nil, err
		}
		targetURL = u.String()
	}
	s.makeRoomForNewTab(ctx, d)
	body, err := chromeHTTP(ctx, d, http.MethodPut, "/json/new?"+url.QueryEscape(targetURL))
	if err != nil {
		return nil, err
	}
	var target browserTarget
	if err := json.Unmarshal(body, &target); err != nil || target.ID == "" || target.WebSocketDebuggerURL == "" {
		return nil, errors.New("browser new tab returned an invalid target")
	}
	pageTarget := browserPage{TargetID: target.ID, URL: target.URL, WebSocketDebuggerURL: target.WebSocketDebuggerURL}
	if err := s.bringBrowserPageToFront(ctx, d, pageTarget); err != nil {
		return nil, err
	}
	page, err := s.waitForBrowserPage(ctx, d, target.ID, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": "new", "target_id": page.TargetID, "title": page.Title, "url": page.URL, "loading": page.Loading, "ready_state": page.ReadyState, "verified": !page.Loading && page.ReadyState == "complete"}, nil
}

func (s *Service) browserSwitchPage(ctx context.Context, d *desktop, targetID string) (map[string]any, error) {
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("browser switch requires target_id")
	}
	pages, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, err
	}
	target, ok := findBrowserPage(pages, targetID)
	if !ok {
		return nil, fmt.Errorf("browser target %q was not found", targetID)
	}
	if err := s.bringBrowserPageToFront(ctx, d, target); err != nil {
		return nil, err
	}
	page, err := s.waitForFocusedBrowserPage(ctx, d, target.TargetID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": "switch", "target_id": page.TargetID, "title": page.Title, "url": page.URL, "loading": page.Loading, "ready_state": page.ReadyState, "focused": page.Focused, "verified": page.Focused}, nil
}

func (s *Service) browserClosePage(ctx context.Context, botID string, d *desktop, targetID string) (map[string]any, error) {
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("browser close requires target_id")
	}
	pages, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, err
	}
	target, ok := findBrowserPage(pages, targetID)
	if !ok {
		return nil, fmt.Errorf("browser target %q was not found", targetID)
	}
	if err := s.bringBrowserPageToFront(ctx, d, target); err != nil {
		return nil, err
	}
	if _, err := chromeHTTP(ctx, d, http.MethodGet, "/json/close/"+url.PathEscape(target.TargetID)); err != nil {
		return nil, err
	}
	remaining, err := s.inspectBrowserPages(ctx, d)
	if err != nil {
		return nil, err
	}
	current, source := chooseCurrentPage(remaining)
	return map[string]any{"action": "close", "target_id": target.TargetID, "closed": true, "tabs": remaining, "current": current, "current_source": source}, nil
}

func (s *Service) bringBrowserPageToFront(ctx context.Context, d *desktop, target browserPage) error {
	return bringBrowserTargetToFront(ctx, d, target)
}

func bringBrowserTargetToFront(ctx context.Context, d *desktop, target browserPage) error {
	if _, err := chromeHTTP(ctx, d, http.MethodGet, "/json/activate/"+url.PathEscape(target.TargetID)); err != nil {
		return fmt.Errorf("activate browser target: %w", err)
	}
	_, err := runCDPCommand(ctx, target.WebSocketDebuggerURL, d.remotePort, "Page.bringToFront", map[string]any{})
	return err
}

func (s *Service) inspectBrowserPages(ctx context.Context, d *desktop) ([]browserPage, error) {
	body, err := chromeHTTP(ctx, d, http.MethodGet, "/json/list")
	if err != nil {
		return nil, err
	}
	var targets []browserTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("decode browser targets: %w", err)
	}
	pages := make([]browserPage, 0, len(targets))
	for _, target := range targets {
		if target.Type != "page" || target.ID == "" || target.WebSocketDebuggerURL == "" {
			continue
		}
		if err := validateDebuggerURL(target.WebSocketDebuggerURL, d.remotePort); err != nil {
			continue
		}
		state, err := cdpEvaluateBrowserState(ctx, target.WebSocketDebuggerURL, d.remotePort)
		if err != nil {
			// A page can disappear between /json/list and CDP evaluation. Skip
			// that stale target instead of reporting it as the current page.
			continue
		}
		pages = append(pages, browserPage{
			TargetID: target.ID, Title: state.Title, URL: state.URL,
			Loading: state.ReadyState != "complete", ReadyState: state.ReadyState,
			Visible: state.VisibilityState == "visible", Focused: state.HasFocus,
			WebSocketDebuggerURL: target.WebSocketDebuggerURL,
		})
	}
	return pages, nil
}

func (s *Service) waitForBrowserPage(ctx context.Context, d *desktop, targetID, previousURL string) (browserPage, error) {
	deadline := time.Now().Add(3 * time.Second)
	var last browserPage
	for time.Now().Before(deadline) {
		pages, err := s.inspectBrowserPages(ctx, d)
		if err != nil {
			return browserPage{}, err
		}
		page, ok := findBrowserPage(pages, targetID)
		if ok {
			last = page
			// Page.navigate may follow redirects or normalize the URL. The
			// protocol response already reports navigation errors; here only
			// wait for the target to settle, and return the actual final URL.
			// A complete ready state is the only verified content state.
			if page.URL != "" && page.ReadyState == "complete" && (previousURL == "" || page.URL != previousURL) {
				return page, nil
			}
		}
		select {
		case <-ctx.Done():
			return browserPage{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if last.TargetID != "" {
		// A slow page is still a real target. Return its observed state so the
		// caller can expose loading=true and verified=false without claiming
		// the page has finished loading.
		return last, nil
	}
	return browserPage{}, fmt.Errorf("browser target %q disappeared during navigation", targetID)
}

func (s *Service) waitForFocusedBrowserPage(ctx context.Context, d *desktop, targetID string) (browserPage, error) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pages, err := s.inspectBrowserPages(ctx, d)
		if err != nil {
			return browserPage{}, err
		}
		page, ok := findBrowserPage(pages, targetID)
		if ok && page.Focused && page.Visible {
			return page, nil
		}
		select {
		case <-ctx.Done():
			return browserPage{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return browserPage{}, fmt.Errorf("browser target %q was not verified as the focused visible page", targetID)
}

func chooseCurrentPage(pages []browserPage) (*browserPage, string) {
	for i := range pages {
		if pages[i].Focused && pages[i].Visible {
			return &pages[i], "focused"
		}
	}
	visible := make([]int, 0, len(pages))
	for i := range pages {
		if pages[i].Visible {
			visible = append(visible, i)
		}
	}
	if len(visible) == 1 {
		return &pages[visible[0]], "visible-single"
	}
	return nil, "none"
}

func findBrowserPage(pages []browserPage, targetID string) (browserPage, bool) {
	for _, page := range pages {
		if page.TargetID == targetID {
			return page, true
		}
	}
	return browserPage{}, false
}

func resolveBrowserTarget(pages []browserPage, targetID string) (browserPage, error) {
	if targetID != "" {
		page, ok := findBrowserPage(pages, targetID)
		if !ok {
			return browserPage{}, fmt.Errorf("browser target %q was not found", targetID)
		}
		return page, nil
	}
	page, source := chooseCurrentPage(pages)
	if page == nil {
		return browserPage{}, fmt.Errorf("no focused visible browser page; specify target_id (current_source=%s)", source)
	}
	return *page, nil
}

func (s *Service) desktopScroll(ctx context.Context, botID, direction string, units int) (map[string]any, error) {
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	direction, button, units, err := normalizeScroll(direction, units)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return nil, errors.New("desktop input dependency xdotool is unavailable")
	}
	cmd := exec.CommandContext(ctx, "xdotool", "mousemove", "--sync", "640", "400")
	cmd.Env = append(cleanEnv(s.root, filepath.Join(s.root, "bots", botID)), "DISPLAY="+d.display)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("desktop scroll: %w: %s", err, strings.TrimSpace(string(out)))
	}
	cmd = exec.CommandContext(ctx, "xdotool", "click", "--repeat", strconv.Itoa(units), button)
	cmd.Env = append(cleanEnv(s.root, filepath.Join(s.root, "bots", botID)), "DISPLAY="+d.display)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("desktop scroll: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return map[string]any{"ok": true, "direction": direction, "units": units}, nil
}

func normalizeScroll(direction string, units int) (string, string, int, error) {
	direction = strings.ToLower(strings.TrimSpace(direction))
	if units == 0 {
		units = 3
	}
	if units < 1 || units > 20 {
		return "", "", 0, errors.New("desktop.scroll units must be between 1 and 20")
	}
	button := map[string]string{"up": "4", "down": "5", "left": "6", "right": "7"}[direction]
	if button == "" {
		return "", "", 0, errors.New("desktop.scroll direction must be up, down, left or right")
	}
	return direction, button, units, nil
}

func validateDebuggerURL(raw string, port int) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "ws" || u.Hostname() != "127.0.0.1" || u.Port() != strconv.Itoa(port) || u.Path == "" {
		return errors.New("browser debugger endpoint is not local to this desktop")
	}
	return nil
}

func cdpEvaluateBrowserState(ctx context.Context, wsURL string, port int) (browserPageState, error) {
	raw, err := runCDPCommand(ctx, wsURL, port, "Runtime.evaluate", map[string]any{
		"expression":    "(() => ({title: document.title, url: location.href, visibility_state: document.visibilityState, has_focus: document.hasFocus(), ready_state: document.readyState}))()",
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return browserPageState{}, err
	}
	var evaluated struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &evaluated); err != nil {
		return browserPageState{}, fmt.Errorf("decode Runtime.evaluate result: %w", err)
	}
	if len(evaluated.ExceptionDetails) > 0 && string(evaluated.ExceptionDetails) != "null" {
		return browserPageState{}, fmt.Errorf("browser page inspection raised an exception: %s", string(evaluated.ExceptionDetails))
	}
	if len(evaluated.Result.Value) == 0 || string(evaluated.Result.Value) == "null" {
		return browserPageState{}, errors.New("browser page inspection returned no value")
	}
	var state browserPageState
	if err := json.Unmarshal(evaluated.Result.Value, &state); err != nil {
		return browserPageState{}, fmt.Errorf("decode browser page state: %w", err)
	}
	return state, nil
}

// The guest image already carries python3-websocket for the existing Chrome
// shutdown path. Reuse that short-lived helper for CDP instead of maintaining
// a second WebSocket framing implementation in Go. CommandContext bounds and
// cancels the helper with the caller's action context.
const cdpPythonScript = `
import json, sys, websocket
ws = websocket.create_connection(sys.argv[1], timeout=10, suppress_origin=True)
try:
    ws.send(json.dumps({"id": 1, "method": sys.argv[2], "params": json.load(sys.stdin)}, separators=(",", ":")))
    while True:
        message = json.loads(ws.recv())
        if message.get("id") != 1:
            continue
        if "error" in message:
            raise RuntimeError(json.dumps(message["error"], separators=(",", ":")))
        print(json.dumps(message.get("result", {}), separators=(",", ":")))
        break
finally:
    ws.close()
`

var runCDPCommand = cdpCommand

func cdpCommand(ctx context.Context, wsURL string, port int, method string, params map[string]any) (json.RawMessage, error) {
	if err := validateDebuggerURL(wsURL, port); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	// Params can contain a private form value. Keep them off process argv.
	cmd := exec.CommandContext(ctx, "python3", "-c", cdpPythonScript, wsURL, method)
	cmd.Stdin = strings.NewReader(string(encoded))
	cmd.Env = []string{"HOME=/tmp", "PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONNOUSERSITE=1"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("CDP %s: %w: %s", method, err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(string(output))
	if line == "" || !json.Valid([]byte(line)) {
		return nil, fmt.Errorf("CDP %s returned invalid JSON", method)
	}
	return json.RawMessage(line), nil
}

func browserLoaderMatches(ctx context.Context, endpoint string, port int, expected string) bool {
	raw, err := runCDPCommand(ctx, endpoint, port, "Page.getFrameTree", map[string]any{})
	if err != nil {
		return false
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	return json.Unmarshal(raw, &tree) == nil && tree.FrameTree.Frame.LoaderID == expected
}
