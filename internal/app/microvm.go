package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const microVMComputerID = "firecracker"

var microVMActions = map[string]bool{
	"shell.exec": true,
	"files.list": true, "files.read": true, "files.write": true, "files.export_chunk": true,
	"desktop.start": true, "desktop.stop": true, "desktop.capture": true,
	"desktop.click": true, "desktop.type": true, "desktop.key": true, "desktop.scroll": true,
	"browser.navigate": true, "browser.snapshot": true, "browser.action": true,
	"terminal.open": true, "terminal.list": true, "terminal.read": true,
	"terminal.write": true, "terminal.resize": true, "terminal.close": true,
}

func (s *Server) microVMConfigured() bool { return s.microVM != nil }

func (s *Server) microVMInfo(ctx context.Context) (computer.Info, error) {
	if s.microVM == nil {
		return computer.Info{}, errors.New("computer VM is not configured")
	}
	return s.microVM.Info(ctx)
}

const (
	microVMInfoTTL     = 30 * time.Second
	microVMInfoWait    = 300 * time.Millisecond
	microVMInfoTimeout = 5 * time.Second
)

// microVMInfoCache keeps run start off the VM control socket: a stale entry is
// served while one background refresh runs, and a cold cache waits briefly.
type microVMInfoCache struct {
	mu      sync.Mutex
	info    computer.Info
	err     error
	at      time.Time
	pending chan struct{}
}

func (s *Server) cachedMicroVMInfo(ctx context.Context) (computer.Info, error, bool) {
	if s.microVM == nil {
		return computer.Info{}, errors.New("computer VM is not configured"), true
	}
	c := &s.microVMInfoCache
	c.mu.Lock()
	if !c.at.IsZero() && time.Since(c.at) < microVMInfoTTL {
		info, err := c.info, c.err
		c.mu.Unlock()
		return info, err, true
	}
	wait := c.pending
	if wait == nil {
		wait = make(chan struct{})
		c.pending = wait
		go s.refreshMicroVMInfo(wait)
	}
	if !c.at.IsZero() {
		info, err := c.info, c.err
		c.mu.Unlock()
		return info, err, true
	}
	c.mu.Unlock()
	timer := time.NewTimer(microVMInfoWait)
	defer timer.Stop()
	select {
	case <-wait:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.info, c.err, true
	case <-timer.C:
	case <-ctx.Done():
	}
	return computer.Info{}, nil, false
}

func (s *Server) refreshMicroVMInfo(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), microVMInfoTimeout)
	defer cancel()
	info, err := s.microVM.Info(ctx)
	c := &s.microVMInfoCache
	c.mu.Lock()
	c.info, c.err, c.at, c.pending = info, err, time.Now(), nil
	c.mu.Unlock()
	close(done)
}

// microVMStatus is volatile, so it belongs in the trailing run context rather
// than the cache-stable system text.
func (s *Server) microVMStatus(ctx context.Context) string {
	if s.microVM == nil {
		return ""
	}
	info, err, known := s.cachedMicroVMInfo(ctx)
	if !known {
		return "VM status unknown"
	}
	state := strings.TrimSpace(info.State)
	if state == "" {
		state = "unknown"
	}
	status := state
	if phase := strings.TrimSpace(info.Phase); phase != "" {
		status += "/" + phase
	}
	if err != nil {
		status += "; status unavailable: " + err.Error()
	}
	if health := s.computerWatchdog.view(); health != nil && (health.State == computerRestarting || health.State == computerUnresponsive) {
		status += "; health " + health.State
	}
	if strings.TrimSpace(info.Error) != "" {
		status += "; manager reports: " + info.Error
	}
	return "VM status " + status
}

// microVMEnvironmentPrompt keeps the model aware of the real computer boundary
// without exposing the control socket or any credentials. The VM is one
// workspace-wide environment; Bot profiles and displays provide separation
// between Bots while /workspace/shared and other Bot directories remain
// intentionally addressable for collaboration.
func (s *Server) microVMEnvironmentPrompt(ctx context.Context, botID string) string {
	if s.microVM == nil {
		return ""
	}
	info, _, _ := s.cachedMicroVMInfo(ctx)
	root := strings.TrimRight(strings.TrimSpace(info.WorkspaceRoot), "/")
	if root == "" {
		root = "/workspace"
	}
	browser := strings.TrimSpace(info.Browser)
	if browser == "" {
		browser = "Google Chrome"
	}
	idle := ""
	if info.DesktopIdleSeconds != nil {
		idle = fmt.Sprintf(" Desktop idle timeout: %ds (0 disables).", *info.DesktopIdleSeconds)
	}
	return fmt.Sprintf("\nShared Linux VM, separate from the service host and Mac; never claim host/Mac access. cwd=%s/bots/%s, HOME=/workspace/home; /workspace/shared, files, tools, Chrome profile and display are shared. Browser: %s. System directories are read-only (no sudo); install software only in user space (skill: software). Use tools only when ready; otherwise report status. %s Stop the shared desktop only on user intent. Private keys use Secret Input; return public keys only.", root, botID, browser, computerBrowserEssentials) + idle
}

// computerBrowserEssentials is the always-present browser recipe; computer_help
// keeps the longer procedure.
const computerBrowserEssentials = "Browser: if the desktop is stopped, run desktop.start once. Read pages with browser.read and open items with browser.click by their visible text; use browser.snapshot only for layout or coordinates (latest snapshot only). Open searches and sites directly by URL. Reuse the current tab; at most 3 tabs stay open."

const computerResearchGuidance = " For research, open primary pages (the article itself), not search results, and cite their URLs with dates."

func (s *Server) listComputers(ctx context.Context) ([]Computer, error) {
	items, err := s.store.listComputers(s.instance.ID)
	if err != nil {
		return nil, err
	}
	if s.microVM == nil {
		return items, nil
	}
	info, infoErr := s.microVMInfo(ctx)
	item := Computer{ID: microVMComputerID, Name: "Bot workspace", Platform: "linux", Kind: "firecracker", Capabilities: []string{"shell.exec", "files.list", "files.read", "files.write", "desktop.start", "desktop.stop", "desktop.capture", "desktop.click", "desktop.type", "desktop.key", "desktop.scroll", "browser.navigate", "browser.snapshot", "browser.action", "terminal.open", "terminal.list", "terminal.read", "terminal.write", "terminal.resize", "terminal.close"}}
	if infoErr == nil {
		item.Online = info.State == "ready"
		if info.WorkspaceRoot != "" {
			item.Name = "Bot workspace · " + info.WorkspaceRoot
		}
	} else {
		item.Online = false
	}
	items = append(items, item)
	return items, nil
}

func (s *Server) microVMAction(ctx context.Context, r Run, name string, args json.RawMessage) (string, error) {
	if s.microVM == nil {
		return "", tooloutcome.New(tooloutcome.Permanent, "computer_unavailable", "not_executed", "computer VM is not configured", "explain_blocker").Err()
	}
	if !microVMActions[name] {
		return "", tooloutcome.InvalidArguments(fmt.Sprintf("unsupported computer action %q", name))
	}
	// A restarting computer fails fast, before queueing for the desktop.
	if err := s.computerAdmission(); err != nil {
		return "", err
	}
	if !isGraphicAction(name) {
		if strings.HasPrefix(name, "terminal.") && name != "terminal.list" && name != "terminal.read" && !s.terminalAvailable(r.BotID, r.ID) {
			return "", tooloutcome.New(tooloutcome.Denied, "terminal_busy", "not_executed", "terminal_busy: user controls this Bot's terminal", "explain_blocker").Err()
		}
		if name == "terminal.open" || name == "terminal.write" {
			if err := s.store.registerRunTerminals(ctx, r, s.microVM.Socket()); err != nil {
				return "", err
			}
		}
		return s.microVMActionFromSource(ctx, r, name, args, "model")
	}
	release, err := s.acquireDesktop(ctx, r)
	if err != nil {
		return "", err
	}
	defer release()

	if actionNeedsObservation(name) && !s.desktopObservedFor(r) {
		return "", tooloutcome.InvalidArguments("needs_observation: shared desktop control changed; inspect desktop.capture or browser.snapshot before using coordinates or typing")
	}
	result, err := s.microVMActionOnLease(ctx, r, name, args)
	if err == nil {
		s.recordDesktopAction(r, name, args)
	}
	if err == nil && (name == "desktop.start" || name == "desktop.stop" || name == "browser.navigate" || name == "browser.action") {
		s.computerOwnerMu.Lock()
		delete(s.desktopObserved, r.ID)
		s.computerOwnerMu.Unlock()
	}
	if err == nil && (name == "desktop.capture" || name == "browser.snapshot") {
		s.markDesktopObserved(r)
	}
	return result, err
}

func lockComputerLease(ctx context.Context, lease *sync.Mutex) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lease.TryLock() {
			if err := ctx.Err(); err != nil {
				lease.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) microVMActionOnLease(ctx context.Context, r Run, name string, args json.RawMessage) (string, error) {
	return s.microVMActionFromSource(ctx, r, name, args, "model")
}

func (s *Server) microVMActionFromSource(ctx context.Context, r Run, name string, args json.RawMessage, source string) (string, error) {
	if s.microVM == nil {
		return "", errors.New("computer VM is not configured")
	}
	s.prepareGuestTimezoneForAction(ctx, name)
	if !microVMActions[name] {
		return "", fmt.Errorf("unsupported computer action %q", name)
	}
	if err := s.computerAdmission(); err != nil {
		return "", err
	}
	botName := ""
	if s.store != nil {
		if bot, err := s.store.GetBot(r.BotID); err == nil {
			botName = bot.Name
		}
	}
	action := computer.Action{BotID: r.BotID, BotName: botName, RunID: r.ID, Name: name, Args: args, Source: source}
	if identity, ok := tooloutcome.ExecutionIdentity(ctx); ok && name == "files.write" && identity.GuardVersion == 1 {
		action.WriteIdentity = &identity
	}
	limit := computerDispatchTimeoutFor(name, args)
	result, err := s.microVM.ActionWithin(ctx, action, limit)
	if err != nil {
		var deadline *computer.ActionDeadlineError
		if errors.As(err, &deadline) {
			return "", s.computerActionTimeoutError(ctx, name, deadline.Limit)
		}
		return "", err
	}
	return string(result.Result), nil
}

func (s *Server) claimComputerOwner(botID, runID string) bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	oauthHuman := strings.HasPrefix(runID, "human-control:") && strings.HasPrefix(s.computerOwners["oauth:"+botID], oauthOwnerPrefix)
	for otherBot, owner := range s.computerOwners {
		if oauthHuman && otherBot == "oauth:"+botID {
			continue
		}
		if owner != "" && (otherBot != botID || owner != runID) {
			log.Printf("[desktop] claim denied bot=%s run=%s owner=%s=%s", botID, runID, otherBot, owner)
			return false
		}
	}
	if !oauthHuman && len(s.desktopWaiters) > 0 && s.computerOwners[botID] != runID {
		log.Printf("[desktop] claim queued bot=%s run=%s waiters=%d", botID, runID, len(s.desktopWaiters))
		return false
	}

	if s.computerOwners == nil {
		s.computerOwners = map[string]string{}
	}
	s.computerOwners[botID] = runID
	s.desktopChangedLocked()
	return true
}

func (s *Server) releaseComputerOwner(botID, runID string) {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	delete(s.desktopQueueLeft, runID)
	if s.computerOwners[botID] == runID {
		delete(s.computerOwners, botID)
		delete(s.desktopObserved, runID)
		if s.desktopPointer != nil && s.desktopPointer.RunID == runID {
			s.desktopPointer = nil
		}
		s.desktopChangedLocked()
	}
}

func isReadOnlyMicroVMAction(name string) bool {
	return name == "desktop.capture" || name == "browser.snapshot" || name == "files.list" || name == "files.read" || name == "files.export_chunk"
}

func (s *Server) botHasActiveRun(botID string) bool {
	var active int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND status IN ('queued','running')`, botID).Scan(&active); err != nil {
		return true
	}
	return active > 0
}

func (s *Server) computerLease(botID string) *sync.Mutex { return s.resourceLease("shared-desktop") }
func (s *Server) resourceLease(botID string) *sync.Mutex {
	s.computerLeaseMu.Lock()
	defer s.computerLeaseMu.Unlock()
	if s.computerLeases == nil {
		s.computerLeases = map[string]*sync.Mutex{}
	}
	lease := s.computerLeases[botID]
	if lease == nil {
		lease = &sync.Mutex{}
		s.computerLeases[botID] = lease
	}
	return lease
}

// microVMTools are intentionally separate from the paired-Mac queue. A VM is
// already bound to this service's one workspace and therefore never accepts a
// user-provided VM/socket identifier.
// effectParam is the model's own statement of a page action's effect.
var effectParam = map[string]any{"type": "string", "enum": actionEffects, "description": "For click/type/key: what this does outside reading. none = read, open, navigate, select, search; otherwise submit, purchase, send, delete, publish, account (sign-up, permissions, settings) or other_external. Actions with an effect may need the user's confirmation."}

func (s *Server) microVMTools(r Run) []Tool {
	if s.microVM == nil {
		return nil
	}
	decode := func(raw json.RawMessage, target any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		return d.Decode(target)
	}
	call := func(name, description string, schema map[string]any, parse func(json.RawMessage) (string, json.RawMessage, error)) Tool {
		return Tool{Name: name, Description: description, Parameters: schema, Timeout: computerToolTimeout, Identity: func(raw json.RawMessage) tooloutcome.Identity {
			action, args, err := parse(raw)
			if err != nil {
				var in struct {
					Action string `json:"action"`
				}
				_ = json.Unmarshal(raw, &in)
				action, args = in.Action, raw
				if action == "" {
					action = name
				}
			}
			return computerRecoveryIdentity(r.BotID, microVMComputerID, action, args)
		}, ResolveIdentity: func(ctx context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
			action, args, err := parse(raw)
			if err != nil {
				return tooloutcome.Identity{}, tooloutcome.InvalidArguments(err.Error())
			}
			return s.resolveComputerRecovery(ctx, r, microVMComputerID, action, args)
		}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			action, args, err := parse(raw)
			if err != nil {
				return "", err
			}
			// Scheduled work may run after idle cleanup has stopped Chrome.
			// Starting the shared desktop is idempotent, and makes the promised
			// browser fallback usable without relying on the model to infer a
			// separate recovery step from a failed snapshot.
			if name == "computer_browser" && (r.Kind == runKindSchedule || r.scheduleTask != nil) {
				if _, err := s.microVMAction(ctx, r, "desktop.start", json.RawMessage(`{}`)); err != nil {
					return "", fmt.Errorf("scheduled browser startup: %w", err)
				}
			}
			if action == "browser.click" {
				return s.browserClick(ctx, r, args)
			}
			if action == "browser.read" {
				out, err := s.browserRead(ctx, r, args)
				if err != nil && browserProcessGone(err) && s.restartDesktop(ctx, r) == nil {
					out, err = s.browserRead(ctx, r, args)
				}
				return out, err
			}
			var declared struct {
				Effect string `json:"effect"`
				Text   string `json:"text"`
				Key    string `json:"key"`
			}
			_ = json.Unmarshal(raw, &declared)
			if (action == "desktop.click" || action == "desktop.type" || action == "desktop.key") && consequentialEffect(declared.Effect) {
				var target actionTarget
				if action == "desktop.click" {
					var at struct{ X, Y float64 }
					var dims struct {
						W float64 `json:"screenshot_width"`
						H float64 `json:"screenshot_height"`
					}
					_ = json.Unmarshal(args, &at)
					_ = json.Unmarshal(args, &dims)
					if dims.W <= 0 || dims.H <= 0 {
						dims.W, dims.H = 1280, 800
					}
					target = s.locateAction(ctx, r, browserReadArgs{Probe: []float64{at.X, at.Y, dims.W, dims.H}})
				} else {
					target = s.locateAction(ctx, r, browserReadArgs{Probe: []float64{-1, -1, 1, 1}})
				}
				text := declared.Text
				if action == "desktop.key" {
					text = "key " + declared.Key
				}
				if err := s.guardAction(ctx, r, actionReview{Kind: strings.TrimPrefix(action, "desktop."), Effect: declared.Effect, Element: target.Label, Text: text, URL: target.URL, Title: target.Title}); err != nil {
					return "", err
				}
			}
			out, err := s.microVMAction(ctx, r, action, args)
			// Chrome can exit under a still-registered desktop; every later
			// browser call then fails the same way. Restart it once and retry.
			if err != nil && name == "computer_browser" && browserProcessGone(err) && s.restartDesktop(ctx, r) == nil {
				out, err = s.microVMAction(ctx, r, action, args)
			}
			return out, err
		}}
	}
	shell := call("computer_shell", "Run a bounded shell command with this Bot's default working directory inside the shared user VM. HOME and installed tools are shared across all Bots in this VM; installing or uninstalling a command affects all of them. Only default working directories differ and remain mutually accessible. Load a project's .env explicitly when needed.", objectSchema(map[string]any{"command": map[string]any{"type": "string", "description": "bash command, for example pwd"}, "timeout_sec": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Optional command timeout in seconds"}}, []string{"command"}), func(raw json.RawMessage) (string, json.RawMessage, error) {
		var in struct {
			Command    string `json:"command"`
			TimeoutSec int    `json:"timeout_sec,omitempty"`
		}
		if err := decode(raw, &in); err != nil || strings.TrimSpace(in.Command) == "" {
			return "", nil, errors.New("computer_shell requires command")
		}
		args, _ := json.Marshal(map[string]any{"command": in.Command, "timeout_sec": in.TimeoutSec})
		return "shell.exec", args, nil
	})
	files := call("computer_files", "List, read, or write files in the shared user VM. Relative paths start in the current Bot profile; absolute paths under /workspace may access shared files or another Bot profile when needed. Files are shared and other Bots may write concurrently. Read sha256 then send expected_sha256 on writes to avoid overwriting another Bot's changes. Keep writes scoped to the requested Bot or shared workspace.", objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"files.list", "files.read", "files.write"}, "description": "files.list, files.read, or files.write"}, "path": map[string]any{"type": "string", "description": "Relative to this Bot profile, or an absolute path under /workspace such as /workspace/shared/..."}, "content": map[string]any{"type": "string"}, "expected_sha256": map[string]any{"type": "string", "description": "Optional SHA-256 from files.read for conditional writes to shared files; reject if another writer changed the file"}, "append": map[string]any{"type": "boolean"}, "offset": map[string]any{"type": "integer", "minimum": 1}, "limit": map[string]any{"type": "integer", "minimum": 1}}, []string{"action"}), func(raw json.RawMessage) (string, json.RawMessage, error) {
		var in struct {
			Action         string  `json:"action"`
			Path           string  `json:"path,omitempty"`
			Content        string  `json:"content,omitempty"`
			Append         bool    `json:"append,omitempty"`
			ExpectedSHA256 *string `json:"expected_sha256,omitempty"`
			Offset         int     `json:"offset,omitempty"`
			Limit          int     `json:"limit,omitempty"`
		}
		if err := decode(raw, &in); err != nil || !strings.HasPrefix(in.Action, "files.") {
			return "", nil, errors.New("computer_files requires a files action")
		}
		var args any
		switch in.Action {
		case "files.list":
			args = struct {
				Path string `json:"path,omitempty"`
			}{Path: in.Path}
		case "files.read":
			args = struct {
				Path   string `json:"path,omitempty"`
				Offset int    `json:"offset,omitempty"`
				Limit  int    `json:"limit,omitempty"`
			}{Path: in.Path, Offset: in.Offset, Limit: in.Limit}
		case "files.write":
			args = struct {
				Path           string  `json:"path,omitempty"`
				Content        string  `json:"content,omitempty"`
				Append         bool    `json:"append,omitempty"`
				ExpectedSHA256 *string `json:"expected_sha256,omitempty"`
			}{Path: in.Path, Content: in.Content, Append: in.Append, ExpectedSHA256: in.ExpectedSHA256}
		default:
			return "", nil, errors.New("computer_files requires files.list, files.read, or files.write")
		}
		argsJSON, _ := json.Marshal(args)
		return in.Action, argsJSON, nil
	})
	desktop := call("computer_desktop", "Control the shared user VM's display and Chrome session. All Bots share this display/browser. Graphical actions wait for exclusive control; inspect after acquiring it or after the page changes. A successful browser.snapshot already includes a current screenshot and proves the desktop is running: use that screenshot directly, without an extra desktop.start or desktop.capture. Use desktop.start when desktop readiness is unknown/stopped; desktop.capture is an alternative observation for non-browser UI. Click/type/key/scroll act on the current visible screen. After an input sequence, inspect the result before claiming success.", objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"desktop.start", "desktop.stop", "desktop.capture", "desktop.snapshot", "desktop.click", "desktop.type", "desktop.key", "desktop.scroll", "start", "stop", "capture", "click", "type", "key", "scroll"}, "description": "desktop.start/stop/capture/click/type/key/scroll (desktop.snapshot = capture)"}, "x": map[string]any{"type": "integer", "minimum": 0, "description": "Screen pixel X from the latest capture"}, "y": map[string]any{"type": "integer", "minimum": 0, "description": "Screen pixel Y from the latest capture"}, "screenshot_width": map[string]any{"type": "integer", "minimum": 1, "maximum": 1280, "description": "Width of the screenshot used for x/y; provide together with screenshot_height"}, "screenshot_height": map[string]any{"type": "integer", "minimum": 1, "maximum": 800, "description": "Height of the screenshot used for x/y; provide together with screenshot_width"}, "direction": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}, "description": "Visible page scroll direction"}, "amount": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Mouse wheel steps, default 3"}, "text": map[string]any{"type": "string"}, "key": map[string]any{"type": "string", "description": "X11 key name, for example Return or Escape"}, "effect": effectParam, "modifiers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, []string{"action"}), func(raw json.RawMessage) (string, json.RawMessage, error) {
		var in struct {
			Action           string   `json:"action"`
			Direction        string   `json:"direction,omitempty"`
			Amount           int      `json:"amount,omitempty"`
			X                int      `json:"x,omitempty"`
			Y                int      `json:"y,omitempty"`
			ScreenshotWidth  int      `json:"screenshot_width,omitempty"`
			ScreenshotHeight int      `json:"screenshot_height,omitempty"`
			Text             string   `json:"text,omitempty"`
			Key              string   `json:"key,omitempty"`
			Modifiers        []string `json:"modifiers,omitempty"`
			Effect           string   `json:"effect,omitempty"`
		}
		if err := decode(raw, &in); err != nil {
			return "", nil, errors.New("computer_desktop requires a desktop action")
		}
		if !strings.HasPrefix(in.Action, "desktop.") {
			in.Action = "desktop." + in.Action // models drop the prefix
		}
		if in.Action == "desktop." {
			return "", nil, errors.New("computer_desktop requires a desktop action")
		}
		if in.Action == "desktop.snapshot" || in.Action == "desktop.screenshot" {
			// Models borrow browser.snapshot's verb; it means a screen capture.
			in.Action = "desktop.capture"
		}
		var args any
		switch in.Action {
		case "desktop.start", "desktop.stop", "desktop.capture":
			args = struct{}{}
		case "desktop.click":
			args = struct {
				X                int `json:"x,omitempty"`
				Y                int `json:"y,omitempty"`
				ScreenshotWidth  int `json:"screenshot_width,omitempty"`
				ScreenshotHeight int `json:"screenshot_height,omitempty"`
			}{X: in.X, Y: in.Y, ScreenshotWidth: in.ScreenshotWidth, ScreenshotHeight: in.ScreenshotHeight}
		case "desktop.scroll":
			args = struct {
				Direction string `json:"direction"`
				Amount    int    `json:"amount,omitempty"`
			}{in.Direction, in.Amount}
		case "desktop.type":
			args = struct {
				Text string `json:"text"`
			}{Text: in.Text}
		case "desktop.key":
			args = struct {
				Key       string   `json:"key"`
				Modifiers []string `json:"modifiers,omitempty"`
			}{Key: in.Key, Modifiers: in.Modifiers}
		default:
			return "", nil, errors.New("computer_desktop requires a supported desktop action")
		}
		argsJSON, _ := json.Marshal(args)
		return in.Action, argsJSON, nil
	})
	browser := call("computer_browser", "Use the shared Chrome. browser.read returns the focused page's text and links (use find to jump to a phrase); prefer it for reading pages, results, articles and mail. browser.click clicks an item by its visible text (a mail subject, button, link) and returns the new page, so no screenshot or coordinates are needed. browser.snapshot returns a screenshot for layout and click coordinates. browser.navigate opens a URL in the current tab (search engines and site searches by URL are fine). browser.action switches/opens/closes tabs. All Bots share this Chrome and its logged-in profile.", objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"browser.read", "browser.click", "browser.navigate", "browser.snapshot", "browser.action", "read", "click", "navigate", "snapshot"}, "description": "browser.read: page text+links (optional find, max_chars); browser.click: click the element showing click text, then returns the page like read; browser.navigate needs url; browser.snapshot: screenshot; browser.action uses target_action"}, "click": map[string]any{"type": "string", "description": "browser.click: visible text of the item to click (a mail subject, button or link label)"}, "effect": effectParam, "find": map[string]any{"type": "string", "description": "browser.read: return passages around this phrase"}, "max_chars": map[string]any{"type": "integer", "minimum": 1000, "maximum": 60000, "description": "browser.read: text budget, default 20000"}, "url": map[string]any{"type": "string", "description": "URL for browser.navigate"}, "target_action": map[string]any{"type": "string", "enum": []string{"navigate", "snapshot", "new", "switch", "close"}, "description": "Browser action; new explicitly opens a foreground tab; switch/close require target_id"}, "target_id": map[string]any{"type": "string", "description": "Exact target_id from browser.snapshot; omit to use the actual current foreground tab"}}, []string{"action"}), func(raw json.RawMessage) (string, json.RawMessage, error) {
		var in struct {
			Action       string `json:"action"`
			URL          string `json:"url,omitempty"`
			TargetAction string `json:"target_action,omitempty"`
			TargetID     string `json:"target_id,omitempty"`
			Find         string `json:"find,omitempty"`
			MaxChars     int    `json:"max_chars,omitempty"`
			Click        string `json:"click,omitempty"`
			Effect       string `json:"effect,omitempty"`
		}
		if err := decode(raw, &in); err != nil || in.Action == "" {
			return "", nil, errors.New("computer_browser requires a browser action")
		}
		if !strings.HasPrefix(in.Action, "browser.") {
			in.Action = "browser." + in.Action // models drop the prefix
		}
		var args any
		switch in.Action {
		case "browser.read":
			args = browserReadArgs{Find: in.Find, MaxChars: in.MaxChars}
		case "browser.click":
			args = browserReadArgs{Click: in.Click, Find: in.Find, MaxChars: in.MaxChars, Effect: in.Effect}
		case "browser.navigate":
			args = struct {
				URL      string `json:"url"`
				TargetID string `json:"target_id,omitempty"`
			}{URL: in.URL, TargetID: in.TargetID}
		case "browser.snapshot":
			args = struct {
				TargetID string `json:"target_id,omitempty"`
			}{in.TargetID}
		case "browser.action":
			args = struct {
				Action   string `json:"action"`
				URL      string `json:"url,omitempty"`
				TargetID string `json:"target_id,omitempty"`
			}{Action: in.TargetAction, URL: in.URL, TargetID: in.TargetID}
		default:
			return "", nil, errors.New("computer_browser requires a supported browser action")
		}
		argsJSON, _ := json.Marshal(args)
		return in.Action, argsJSON, nil
	})
	return []Tool{shell, files, desktop, browser, computerHelpTool(), s.terminalTool(r), s.sshKeyTool(r)}
}

// browserProcessGone reports a browser call that failed because Chrome's
// DevTools endpoint no longer answers, not because of the page or action.
func browserProcessGone(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "chrome is not running") ||
		strings.Contains(msg, "connection refused") && (strings.Contains(msg, "127.0.0.1") && strings.Contains(msg, "/json"))
}

// desktopRestartCooldown keeps a crash-looping Chrome from repeatedly wiping
// the shared desktop that other Bots are using.
const desktopRestartCooldown = 2 * time.Minute

// restartDesktop stops and starts the shared desktop once to bring Chrome back.
func (s *Server) restartDesktop(ctx context.Context, r Run) error {
	s.computerOwnerMu.Lock()
	recent := time.Since(s.desktopRestartedAt) < desktopRestartCooldown
	if !recent {
		s.desktopRestartedAt = time.Now()
	}
	s.computerOwnerMu.Unlock()
	if recent {
		return errors.New("Chrome restarted moments ago; not restarting again")
	}
	log.Printf("[computer] restarting shared desktop for run %s: Chrome DevTools unreachable", r.ID)
	if _, err := s.microVMAction(ctx, r, "desktop.stop", json.RawMessage(`{}`)); err != nil {
		return err
	}
	_, err := s.microVMAction(ctx, r, "desktop.start", json.RawMessage(`{}`))
	return err
}

// acquireDesktop waits for this run to own the shared desktop and holds its
// lease; the returned release unlocks the lease.
func (s *Server) acquireDesktop(ctx context.Context, r Run) (func(), error) {
	var lease *sync.Mutex
	for {
		if err := s.waitComputerOwner(ctx, r); err != nil {
			return nil, err
		}
		lease = s.computerLease(r.BotID)
		if err := lockComputerLease(ctx, lease); err != nil {
			s.releaseComputerOwner(r.BotID, r.ID)
			return nil, err
		}
		if !s.ownsComputer(r) {
			lease.Unlock()
			continue
		}
		break
	}
	if err := s.renewComputerHold(ctx, r); err != nil {
		lease.Unlock()
		s.releaseComputerOwner(r.BotID, r.ID)
		return nil, err
	}
	return lease.Unlock, nil
}
