package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Browser engine (decision 2026-10-29, default changed 2026-10-30). "classic"
// is the hand-built computer_browser tool. "playwright" (the default for a
// workspace that never set the item) offers the browser_* tools below, which
// relay Microsoft's @playwright/mcp (accessibility snapshot with element refs)
// running on the computer and attached to its Chrome over DevTools, so logins
// stay. Classic stays as the fallback: when the relay cannot start, DevTools
// is unreachable or a snapshot has no refs, the call (and, for a start
// failure, the next minutes of the session) goes to computer_browser. Both
// engines use the same effect declaration and the same guardAction review
// before a consequential action.
const (
	browserEngineClassic    = "classic"
	browserEnginePlaywright = "playwright"
)

func (s *Server) browserEnginePlaywright() bool {
	return s.autoValue(autoBrowserEngine) == browserEnginePlaywright
}

// computerBrowserEssentialsFor is the always-present browser recipe of the engine.
func computerBrowserEssentialsFor(playwright bool) string {
	if playwright {
		return computerBrowserEssentialsPlaywright
	}
	return computerBrowserEssentials
}

const computerBrowserEssentialsPlaywright = "Browser: read a page with browser_snapshot; each element has a ref. Act with browser_ tools by ref and element name. A ref is valid only until the page changes: snapshot again after a change. Open sites by URL with browser_navigate. At most 2 tabs. If the engine is unavailable, use computer_browser."

const computerBrowserGuidePlaywright = `The browser is the shared Chrome. browser_snapshot returns the page as an accessibility tree; every interactive element has a ref. Pass the ref and a short element name to browser_click, browser_type, browser_select_option, browser_fill_form and browser_hover. A ref belongs to one snapshot: after the page changes, take a new snapshot before the next action. The result of an action usually includes the new snapshot.
Declare effect on every action that changes something outside the page view. For a file chooser, click the element that opens it, then call browser_file_upload with paths inside /workspace; relative paths start in the Bot directory. Dialogs from the page wait for browser_handle_dialog. Open sites and searches directly by URL, reuse the current tab and keep at most 2 tabs open. Use computer_desktop only for windows outside the browser.
All Bots share one Chrome profile and graphical session. Files, cookies and login state persist, but a desktop restart opens a fresh page. If the page is blocked by a person, ask for a takeover instead of repeating the action.
` + computerResearchGuidance

// pwSpec describes one model-facing tool and how it maps to the engine's tool.
type pwSpec struct {
	Name        string
	Description string
	Props       map[string]any
	Required    []string
	Engine      string
	// Rename maps a model argument to the engine argument of the same meaning.
	Rename map[string]string
	// Observation tools only read or move the view; they are never reviewed.
	Observation bool
	// Review is the action kind shown to the reviewer; empty means never reviewed.
	Review string
	// TextArg is the argument that carries typed or entered text.
	TextArg string
	// MinEffect is the effect assumed when the model declares none.
	MinEffect string
}

var (
	pwRef     = map[string]any{"type": "string", "description": "Ref of the element in the latest browser_snapshot."}
	pwElement = map[string]any{"type": "string", "description": "Short name of the element as the snapshot shows it."}
)

var browserEngineSpecs = []pwSpec{
	{Name: "browser_snapshot", Engine: "browser_snapshot", Observation: true,
		Description: "Read the current page of the shared Chrome as an accessibility tree. Every interactive element has a ref. Use the refs in the other browser_ tools. A ref is valid only until the page changes. Pass ref to read one part of the page, depth to limit nesting.",
		Props:       map[string]any{"ref": pwRef, "depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Maximum nesting depth of the tree."}},
		Rename:      map[string]string{"ref": "target"}},
	{Name: "browser_navigate", Engine: "browser_navigate", Observation: true,
		Description: "Open an http or https URL in the current tab of the shared Chrome and return the page snapshot.",
		Props:       map[string]any{"url": map[string]any{"type": "string", "description": "Address to open."}}, Required: []string{"url"}},
	{Name: "browser_navigate_back", Engine: "browser_navigate_back", Observation: true,
		Description: "Go back one page in the current tab and return the page snapshot.",
		Props:       map[string]any{}},
	{Name: "browser_click", Engine: "browser_click", Review: "click",
		Description: "Click an element of the page by ref. Returns the page snapshot after the click.",
		Props: map[string]any{"ref": pwRef, "element": pwElement, "double_click": map[string]any{"type": "boolean", "description": "Click twice."},
			"button": map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}, "description": "Mouse button. Default left."}, "effect": effectParam},
		Required: []string{"ref", "element"},
		Rename:   map[string]string{"ref": "target", "double_click": "doubleClick"}},
	{Name: "browser_type", Engine: "browser_type", Review: "type", TextArg: "text",
		Description: "Enter text into an editable element by ref. Replaces the content of the field. Set submit to press Enter afterwards.",
		Props: map[string]any{"ref": pwRef, "element": pwElement, "text": map[string]any{"type": "string", "description": "Text to enter."},
			"submit": map[string]any{"type": "boolean", "description": "Press Enter after the text."}, "slowly": map[string]any{"type": "boolean", "description": "Send one key at a time, for fields that react to each key."}, "effect": effectParam},
		Required: []string{"ref", "element", "text"},
		Rename:   map[string]string{"ref": "target"}},
	{Name: "browser_fill_form", Engine: "browser_fill_form", Review: "fill_form",
		Description: "Fill several form fields in one call. Each field has a ref, a name, a type and a value. A checkbox value is true or false. A combobox value is the text of the option.",
		Props: map[string]any{"fields": map[string]any{"type": "array", "minItems": 1, "maxItems": 30, "items": objectSchema(map[string]any{
			"ref": pwRef, "name": map[string]any{"type": "string", "description": "Name of the field."},
			"type":  map[string]any{"type": "string", "enum": []string{"textbox", "checkbox", "radio", "combobox", "slider"}},
			"value": map[string]any{"type": "string", "description": "Value to set."}}, []string{"ref", "name", "type", "value"})}, "effect": effectParam},
		Required: []string{"fields"}},
	{Name: "browser_select_option", Engine: "browser_select_option", Review: "select",
		Description: "Choose one or more options in a dropdown by ref.",
		Props:       map[string]any{"ref": pwRef, "element": pwElement, "values": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string"}, "description": "Options to choose."}, "effect": effectParam},
		Required:    []string{"ref", "element", "values"},
		Rename:      map[string]string{"ref": "target"}},
	{Name: "browser_press_key", Engine: "browser_press_key", Review: "key",
		Description: "Press one key on the page, by key name or a single character.",
		Props:       map[string]any{"key": map[string]any{"type": "string", "description": "Key name or character."}, "effect": effectParam},
		Required:    []string{"key"}},
	{Name: "browser_hover", Engine: "browser_hover", Observation: true,
		Description: "Move the pointer over an element by ref, for menus that open on hover.",
		Props:       map[string]any{"ref": pwRef, "element": pwElement}, Required: []string{"ref", "element"},
		Rename: map[string]string{"ref": "target"}},
	{Name: "browser_wait_for", Engine: "browser_wait_for", Observation: true,
		Description: "Wait until text appears on the page, until text is gone, or for a number of seconds. Returns the page snapshot.",
		Props: map[string]any{"seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 30, "description": "Time to wait."},
			"text": map[string]any{"type": "string", "description": "Text to wait for."}, "text_gone": map[string]any{"type": "string", "description": "Text to wait to disappear."}},
		Rename: map[string]string{"seconds": "time", "text_gone": "textGone"}},
	{Name: "browser_tabs", Engine: "browser_tabs", Observation: true,
		Description: "List, open, close or select tabs of the shared Chrome. At most 2 tabs stay open.",
		Props: map[string]any{"action": map[string]any{"type": "string", "enum": []string{"list", "new", "close", "select"}},
			"index": map[string]any{"type": "integer", "minimum": 0, "description": "Tab number for close and select. Close without it closes the current tab."},
			"url":   map[string]any{"type": "string", "description": "Address to open in a new tab."}},
		Required: []string{"action"}},
	{Name: "browser_file_upload", Engine: "browser_file_upload", Review: "upload", MinEffect: "send",
		Description: "Give files to the file chooser that the page opened. Click the element that opens the chooser first. Paths are on this Bot computer inside /workspace; a relative path starts in the Bot directory. Browser profile and key folders are refused.",
		Props:       map[string]any{"paths": map[string]any{"type": "array", "minItems": 1, "maxItems": browserUploadMaxFiles, "items": map[string]any{"type": "string"}, "description": "Files to attach."}, "effect": effectParam},
		Required:    []string{"paths"}},
	{Name: "browser_handle_dialog", Engine: "browser_handle_dialog", Review: "dialog",
		Description: "Accept or dismiss a dialog that the page opened. Give prompt_text when the dialog asks for text.",
		Props:       map[string]any{"accept": map[string]any{"type": "boolean"}, "prompt_text": map[string]any{"type": "string"}, "effect": effectParam},
		Required:    []string{"accept"}, TextArg: "prompt_text",
		Rename: map[string]string{"prompt_text": "promptText"}},
}

// browserEngineToolNames lists the browser_ tools, for name based lists.
func browserEngineToolNames() []string {
	names := make([]string, 0, len(browserEngineSpecs))
	for _, sp := range browserEngineSpecs {
		names = append(names, sp.Name)
	}
	return names
}

// browserEngineObservation reports a browser_ tool that only reads or moves the view.
func browserEngineObservation(name string) bool {
	for _, sp := range browserEngineSpecs {
		if sp.Name == name {
			return sp.Observation
		}
	}
	return false
}

// pwBuild turns the model's arguments into the engine tool call and the review.
func (sp pwSpec) build(r Run, raw json.RawMessage) (args map[string]any, effect string, element, text string, err error) {
	var in map[string]any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	if err := d.Decode(&in); err != nil || in == nil {
		return nil, "", "", "", tooloutcome.InvalidArguments(sp.Name + " needs a JSON object of arguments")
	}
	for _, key := range sp.Required {
		if v, ok := in[key]; !ok || v == nil || v == "" {
			return nil, "", "", "", tooloutcome.InvalidArguments(sp.Name + " needs " + key)
		}
	}
	args = map[string]any{}
	for key, value := range in {
		if key == "effect" {
			effect, _ = value.(string)
			continue
		}
		if _, known := sp.Props[key]; !known {
			return nil, "", "", "", tooloutcome.InvalidArguments(fmt.Sprintf("%s does not take %q", sp.Name, key))
		}
		if to, ok := sp.Rename[key]; ok {
			key = to
		}
		args[key] = value
	}
	if v, ok := in["element"].(string); ok {
		element = strings.TrimSpace(v)
		args["element"] = element
	}
	if sp.TextArg != "" {
		text, _ = in[sp.TextArg].(string)
	}
	switch sp.Name {
	case "browser_fill_form":
		fields, _ := in["fields"].([]any)
		var names []string
		for i, f := range fields {
			m, _ := f.(map[string]any)
			if m == nil {
				return nil, "", "", "", tooloutcome.InvalidArguments("each field must be an object")
			}
			if ref, ok := m["ref"]; ok {
				m["target"] = ref
				delete(m, "ref")
			}
			if n, _ := m["name"].(string); n != "" {
				names = append(names, n)
			}
			fields[i] = m
		}
		args["fields"] = fields
		element = strings.Join(names, ", ")
	case "browser_file_upload":
		paths, _ := in["paths"].([]any)
		abs := make([]any, 0, len(paths))
		var shown []string
		for _, p := range paths {
			name, _ := p.(string)
			if name == "" {
				return nil, "", "", "", tooloutcome.InvalidArguments("file paths must not be empty")
			}
			if !path.IsAbs(name) {
				name = path.Join(browserUploadBotDirs, r.BotID, name)
			}
			name = path.Clean(name)
			abs = append(abs, name)
			shown = append(shown, path.Base(name))
		}
		args["paths"] = abs
		element = "file chooser"
		text = strings.Join(shown, ", ")
	case "browser_press_key":
		text = "key " + fmt.Sprint(in["key"])
	}
	if sp.MinEffect != "" && !consequentialEffect(effect) {
		effect = sp.MinEffect
	}
	return args, effect, element, text, nil
}

// pwPage is the page the last result showed, for the reviewer.
type pwPage struct {
	mu         sync.Mutex
	url, title string
}

var (
	pwURLLine   = regexp.MustCompile(`(?m)^- Page URL: (.+)$`)
	pwTitleLine = regexp.MustCompile(`(?m)^- Page Title: (.*)$`)
	// The engine names a ref that no longer exists; nothing was done then.
	pwStaleRef = regexp.MustCompile(`(?i)ref .* not found|not found in the current page snapshot|capturing new snapshot`)
)

func (p *pwPage) learn(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m := pwURLLine.FindStringSubmatch(text); m != nil {
		p.url = strings.TrimSpace(m[1])
	}
	if m := pwTitleLine.FindStringSubmatch(text); m != nil {
		p.title = strings.TrimSpace(m[1])
	}
}

func (p *pwPage) get() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url, p.title
}

// browserEngineTools builds the browser_ tools for one run. prepare runs before
// each call (attachment sync, as for the other computer tools).
func (s *Server) browserEngineTools(r Run, prepare func(context.Context), classic Tool) []Tool {
	page := &pwPage{}
	var noted atomic.Bool
	var tools []Tool
	for _, sp := range browserEngineSpecs {
		sp := sp
		props := map[string]any{}
		for k, v := range sp.Props {
			props[k] = v
		}
		identity := func(raw json.RawMessage) tooloutcome.Identity {
			args, _, _, _, err := sp.build(r, raw)
			if err != nil {
				return computerRecoveryIdentity(r.BotID, microVMComputerID, "browser.mcp."+sp.Name, raw)
			}
			encoded, _ := json.Marshal(args)
			i := computerRecoveryIdentity(r.BotID, microVMComputerID, "browser.mcp."+sp.Name, encoded)
			if sp.Observation {
				i.Risk = tooloutcome.Observation
			}
			return i
		}
		tools = append(tools, Tool{
			Name: sp.Name, Description: sp.Description, Parameters: objectSchema(props, sp.Required), Timeout: computerToolTimeout,
			Identity: identity,
			ResolveIdentity: func(_ context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
				if _, _, _, _, err := sp.build(r, raw); err != nil {
					return tooloutcome.Identity{}, err
				}
				return identity(raw), nil
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
				args, effect, element, text, err := sp.build(r, raw)
				if err != nil {
					return "", err
				}
				prepare(ctx)
				settle := func(error) {}
				if sp.Review != "" && consequentialEffect(effect) {
					url, title := page.get()
					label := element
					if label == "" {
						label = text
					}
					if settle, err = s.guardAction(ctx, r, actionReview{Kind: sp.Review, Effect: effect, Element: label, Text: text, URL: url, Title: title}); err != nil {
						return "", err
					}
				}
				if reason, down := s.browserFallbackReason(r.BotID); down {
					settle(nil)
					return s.browserClassicFallback(ctx, sp, args, classic, reason, &noted)
				}
				out, err := s.browserEngineCall(ctx, r, sp.Engine, args)
				settle(err)
				if err != nil {
					if browserEngineUnavailable(err) {
						reason := browserEngineReason(err)
						s.markBrowserFallback(r, reason)
						return s.browserClassicFallback(ctx, sp, args, classic, reason, &noted)
					}
					return "", err
				}
				if browserSnapshotWithoutRefs(sp.Name, out) {
					log.Printf("[browser] run %s: %s returned no refs; this call used the classic browser", r.ID, sp.Name)
					return s.browserClassicFallback(ctx, sp, args, classic, "the page snapshot had no element refs", &noted)
				}
				page.learn(out)
				if sp.Name == "browser_snapshot" || sp.Name == "browser_navigate" {
					s.markDesktopObserved(r)
				}
				return out, nil
			},
		})
	}
	return tools
}

// browserEngineCall relays one engine tool call to the guest and unwraps the text.
func (s *Server) browserEngineCall(ctx context.Context, r Run, tool string, args map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{"tool": tool, "arguments": args})
	if err != nil {
		return "", err
	}
	_ = s.renewComputerHold(ctx, r)
	out, err := s.microVMAction(ctx, r, "browser.mcp", payload)
	if err != nil && browserProcessGone(err) && s.restartDesktop(ctx, r) == nil {
		out, err = s.microVMAction(ctx, r, "browser.mcp", payload)
	}
	if err != nil {
		if strings.Contains(err.Error(), "unsupported action") {
			return "", tooloutcome.InvalidArguments("the browser engine needs a newer Bot computer image; this Bot computer's guest does not support it yet. Nothing was done.")
		}
		return "", err
	}
	return browserEngineResult(out)
}

// browserEngineResult unwraps the guest answer {tool,text,is_error}.
func browserEngineResult(out string) (string, error) {
	var res struct {
		Text    string `json:"text"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errors.New("the browser engine returned an unreadable answer")
	}
	if res.IsError {
		if pwStaleRef.MatchString(res.Text) {
			return "", tooloutcome.InvalidArguments(strings.TrimSpace(res.Text))
		}
		return "", errors.New(strings.TrimSpace(res.Text))
	}
	if strings.TrimSpace(res.Text) == "" {
		return "Done.", nil
	}
	return res.Text, nil
}

// browserFallbackWindow is how long a Bot's browser_ tools go straight to the
// classic browser after the engine proved unavailable, so each call does not
// wait for the same failure.
const browserFallbackWindow = 10 * time.Minute

type browserFallbackMark struct {
	at     time.Time
	reason string
}

// browserEngineUnavailable reports a failure of the engine itself (relay
// cannot start, image too old, Chrome DevTools unreachable), not of the page
// or of the action.
func browserEngineUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if browserProcessGone(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, part := range []string{
		"needs a newer bot computer image", "needs a newer computer image", "browser engine is not installed", "browser engine needs node",
		"browser engine did not start", "browser engine is not running", "browser engine stopped",
		"browser engine did not answer", "browser debugging endpoint is unavailable",
	} {
		if strings.Contains(msg, part) {
			return true
		}
	}
	return false
}

// browserEngineReason is the short cause shown to the Bot.
func browserEngineReason(err error) string {
	msg := strings.TrimSpace(err.Error())
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 160 {
		msg = strings.ToValidUTF8(msg[:160], "")
	}
	return msg
}

func (s *Server) markBrowserFallback(r Run, reason string) {
	log.Printf("[browser] run %s: Playwright browser engine unavailable (%s); using the classic browser", r.ID, reason)
	s.browserFallbackAt.Store(r.BotID, browserFallbackMark{at: time.Now(), reason: reason})
}

// browserFallbackReason says whether this Bot's engine failed within the window.
func (s *Server) browserFallbackReason(botID string) (string, bool) {
	v, ok := s.browserFallbackAt.Load(botID)
	if !ok {
		return "", false
	}
	m := v.(browserFallbackMark)
	if time.Since(m.at) > browserFallbackWindow {
		s.browserFallbackAt.Delete(botID)
		return "", false
	}
	return m.reason, true
}

// browserSnapshotWithoutRefs: a page read that holds no element ref at all
// cannot be acted on with the browser_ tools.
func browserSnapshotWithoutRefs(tool, out string) bool {
	if tool != "browser_snapshot" && tool != "browser_navigate" {
		return false
	}
	return strings.TrimSpace(out) != "" && !strings.Contains(out, "[ref=")
}

// browserClassicFallback serves one browser_ call through computer_browser
// when the engine is unavailable. Reading and opening a page have a classic
// equal; every other call is refused unexecuted and points at computer_browser.
func (s *Server) browserClassicFallback(ctx context.Context, sp pwSpec, args map[string]any, classic Tool, reason string, noted *atomic.Bool) (string, error) {
	note := "Note: the Playwright browser engine is not usable (" + reason + "). Use computer_browser for the browser from now on: browser.read for page text, browser.click by visible text, browser.navigate for a URL."
	if classic.Execute == nil {
		return "", tooloutcome.InvalidArguments(note + " Nothing was done.")
	}
	run := func(in map[string]any) (string, error) {
		raw, _ := json.Marshal(in)
		return classic.Execute(ctx, raw)
	}
	var out string
	var err error
	switch sp.Name {
	case "browser_navigate":
		url, _ := args["url"].(string)
		if _, err = run(map[string]any{"action": "browser.navigate", "url": url}); err == nil {
			out, err = run(map[string]any{"action": "browser.read"})
		}
	case "browser_snapshot":
		out, err = run(map[string]any{"action": "browser.read"})
	default:
		return "", tooloutcome.InvalidArguments(note + " " + sp.Name + " was not run. Nothing was done.")
	}
	if err != nil {
		return "", err
	}
	if noted.CompareAndSwap(false, true) {
		out = note + "\n" + out
	}
	return out, nil
}
