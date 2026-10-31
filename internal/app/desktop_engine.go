package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Desktop engine (decision 2026-10-30). "classic" is the screenshot tool
// computer_desktop alone. "a11y" adds the desktop_ tools below, which relay
// open-computer-use (AT-SPI element tree with indexes) running on the computer
// against the desktop's one session bus. computer_desktop stays in both modes
// (screenshots, pixel clicks, start and stop). The browser is not covered:
// Chrome's tree belongs to the browser engine. Actions use the same effect
// declaration and the same guardAction review as computer_desktop and the
// browser tools.
const (
	desktopEngineClassic = "classic"
	desktopEngineA11y    = "a11y"
)

func (s *Server) desktopEngineA11y() bool {
	return s.autoValue(autoDesktopEngine) == desktopEngineA11y
}

// computerDesktopEssentialsFor is the always-present desktop recipe of the engine,
// with a leading space for the environment line; empty for the classic engine.
func computerDesktopEssentialsFor(a11y bool) string {
	if a11y {
		return " " + computerDesktopEssentialsA11y
	}
	return ""
}

const computerDesktopEssentialsA11y = "Desktop apps: read an app with desktop_get_app_state, which lists its elements with a ref, and act with the desktop_ tools by ref and app. A ref is valid only until the app changes: read the state again after an action that changes it. The element tree is preferred; when an app has no tree or the element is missing, use computer_desktop screenshots and coordinate clicks."

// computerDesktopHybridNote opens the description of computer_desktop while the desktop_ tools are offered.
const computerDesktopHybridNote = "The desktop_ tools are preferred for apps that publish an element tree. Use the screenshot and coordinate actions here as the fallback, for apps without a tree or elements the tree lacks."

const computerDesktopGuideA11y = `desktop_list_apps names the apps that publish an element tree on the shared desktop. desktop_get_app_state returns the tree of one app's window; every element has a ref. Pass the app name and the ref to desktop_click, desktop_set_value, desktop_scroll and desktop_secondary_action. Give a short element name for the review. A ref belongs to one reading of the app: after the app changes, read the state again before the next action. The result of an action usually includes the new state.
desktop_set_value writes a settable element directly; desktop_type_text and desktop_press_key send keys to the app. Declare effect on every action that changes something outside the view.
Apps without a tree (terminal windows, some custom-drawn windows) and the browser do not show up here; use computer_desktop for them and the browser tools for pages. Everything runs in the shared desktop that all Bots use.
`

// dsSpec describes one model-facing desktop tool and how it maps to the engine.
type dsSpec struct {
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
}

var (
	dsApp = map[string]any{"type": "string", "description": "Name of a running app as desktop_list_apps shows it."}
	dsRef = map[string]any{"type": "string", "description": "Ref of the element in the latest desktop_get_app_state of this app."}
	dsEl  = map[string]any{"type": "string", "description": "Short name of the element as the state shows it."}
)

var desktopEngineSpecs = []dsSpec{
	{Name: "desktop_list_apps", Engine: "list_apps", Observation: true,
		Description: "List the apps on the shared desktop that publish an element tree.",
		Props:       map[string]any{}},
	{Name: "desktop_get_app_state", Engine: "get_app_state", Observation: true,
		Description: "Read the key window of a running app as an element tree. Every element has a ref. Use the refs in the other desktop_ tools. A ref is valid only until the app changes. Call this before acting on an app. Preferred over screenshots and coordinates; use computer_desktop only when the app has no tree or the element is missing.",
		Props: map[string]any{"app": dsApp,
			"max_tree_nodes": map[string]any{"type": "integer", "minimum": 1, "maximum": 5000, "description": "Maximum number of elements to list."},
			"max_tree_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 64, "description": "Maximum nesting depth of the tree."}},
		Required: []string{"app"}},
	{Name: "desktop_click", Engine: "click", Review: "click",
		Description: "Click an element of an app by ref. Returns the app state after the click.",
		Props: map[string]any{"app": dsApp, "ref": dsRef, "element": dsEl,
			"click_count":  map[string]any{"type": "integer", "minimum": 1, "maximum": 3, "description": "Number of clicks. Default 1."},
			"mouse_button": map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}, "description": "Mouse button. Default left."}, "effect": effectParam},
		Required: []string{"app", "ref", "element"},
		Rename:   map[string]string{"ref": "element_index"}},
	{Name: "desktop_set_value", Engine: "set_value", Review: "set_value",
		Description: "Set the value of a settable element by ref: a text field, a slider or a toggle. An empty value clears a text field.",
		Props:       map[string]any{"app": dsApp, "ref": dsRef, "element": dsEl, "value": map[string]any{"type": "string", "description": "Value to assign."}, "effect": effectParam},
		Required:    []string{"app", "ref", "element", "value"},
		Rename:      map[string]string{"ref": "element_index"}},
	{Name: "desktop_type_text", Engine: "type_text", Review: "type",
		Description: "Type literal text with the keyboard into the focused element of an app.",
		Props:       map[string]any{"app": dsApp, "text": map[string]any{"type": "string", "description": "Text to type."}, "effect": effectParam},
		Required:    []string{"app", "text"}},
	{Name: "desktop_press_key", Engine: "press_key", Review: "key",
		Description: "Press a key or key combination in an app, in xdotool key syntax.",
		Props:       map[string]any{"app": dsApp, "key": map[string]any{"type": "string", "description": "Key or combination."}, "effect": effectParam},
		Required:    []string{"app", "key"}},
	{Name: "desktop_scroll", Engine: "scroll", Observation: true,
		Description: "Scroll an element of an app by ref. Returns the app state afterwards.",
		Props: map[string]any{"app": dsApp, "ref": dsRef, "direction": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}},
			"pages": map[string]any{"type": "number", "minimum": 0.1, "maximum": 20, "description": "Number of pages. Default 1."}},
		Required: []string{"app", "ref", "direction"},
		Rename:   map[string]string{"ref": "element_index"}},
	{Name: "desktop_secondary_action", Engine: "perform_secondary_action", Review: "action",
		Description: "Run a named accessibility action that an element lists besides its default click, by ref.",
		Props:       map[string]any{"app": dsApp, "ref": dsRef, "element": dsEl, "action": map[string]any{"type": "string", "description": "Action name as the state lists it."}, "effect": effectParam},
		Required:    []string{"app", "ref", "element", "action"},
		Rename:      map[string]string{"ref": "element_index"}},
}

// desktopEngineToolNames lists the desktop_ tools, for name based lists.
func desktopEngineToolNames() []string {
	names := make([]string, 0, len(desktopEngineSpecs))
	for _, sp := range desktopEngineSpecs {
		names = append(names, sp.Name)
	}
	return names
}

// desktopEngineObservation reports a desktop_ tool that only reads or moves the view.
func desktopEngineObservation(name string) bool {
	for _, sp := range desktopEngineSpecs {
		if sp.Name == name {
			return sp.Observation
		}
	}
	return false
}

// dsStaleRef: the engine names an element or app it does not have; nothing was done then.
var dsStaleRef = regexp.MustCompile(`(?i)unknown element_index|appNotFound`)

// build turns the model's arguments into the engine call and the review.
func (sp dsSpec) build(raw json.RawMessage) (args map[string]any, effect, app, element, text string, err error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		return nil, "", "", "", "", tooloutcome.InvalidArguments(sp.Name + " needs a JSON object of arguments")
	}
	for _, key := range sp.Required {
		v, ok := in[key]
		// An empty value is how a field is cleared.
		if !ok || v == nil || (v == "" && key != "value") {
			return nil, "", "", "", "", tooloutcome.InvalidArguments(sp.Name + " needs " + key)
		}
	}
	args = map[string]any{}
	for key, value := range in {
		switch key {
		case "effect":
			effect, _ = value.(string)
			continue
		case "element":
			// The element name is for the review; the engine finds the element by ref.
			if _, known := sp.Props[key]; !known {
				return nil, "", "", "", "", tooloutcome.InvalidArguments(fmt.Sprintf("%s does not take %q", sp.Name, key))
			}
			element, _ = value.(string)
			element = strings.TrimSpace(element)
			continue
		}
		if _, known := sp.Props[key]; !known {
			return nil, "", "", "", "", tooloutcome.InvalidArguments(fmt.Sprintf("%s does not take %q", sp.Name, key))
		}
		if to, ok := sp.Rename[key]; ok {
			key = to
		}
		args[key] = value
	}
	app, _ = in["app"].(string)
	app = strings.TrimSpace(app)
	switch sp.Name {
	case "desktop_type_text":
		text, _ = in["text"].(string)
		element = "focused element"
	case "desktop_set_value":
		text, _ = in["value"].(string)
	case "desktop_press_key":
		text = "key " + fmt.Sprint(in["key"])
		element = "focused element"
	case "desktop_secondary_action":
		text = "action " + fmt.Sprint(in["action"])
	}
	return args, effect, app, element, text, nil
}

// desktopEngineTools builds the desktop_ tools for one run. prepare runs before
// each call (attachment sync, as for the other computer tools).
func (s *Server) desktopEngineTools(r Run, prepare func(context.Context)) []Tool {
	var tools []Tool
	for _, sp := range desktopEngineSpecs {
		sp := sp
		props := map[string]any{}
		for k, v := range sp.Props {
			props[k] = v
		}
		identity := func(raw json.RawMessage) tooloutcome.Identity {
			args, _, _, _, _, err := sp.build(raw)
			if err != nil {
				return computerRecoveryIdentity(r.BotID, microVMComputerID, "desktop.mcp."+sp.Name, raw)
			}
			encoded, _ := json.Marshal(args)
			i := computerRecoveryIdentity(r.BotID, microVMComputerID, "desktop.mcp."+sp.Name, encoded)
			if sp.Observation {
				i.Risk = tooloutcome.Observation
			}
			return i
		}
		tools = append(tools, Tool{
			Name: sp.Name, Description: sp.Description, Parameters: objectSchema(props, sp.Required), Timeout: computerToolTimeout,
			Identity: identity,
			ResolveIdentity: func(_ context.Context, raw json.RawMessage) (tooloutcome.Identity, error) {
				if _, _, _, _, _, err := sp.build(raw); err != nil {
					return tooloutcome.Identity{}, err
				}
				return identity(raw), nil
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
				args, effect, app, element, text, err := sp.build(raw)
				if err != nil {
					return "", err
				}
				prepare(ctx)
				settle := func(error) {}
				if sp.Review != "" && consequentialEffect(effect) {
					label := element
					if label == "" {
						label = text
					}
					if settle, err = s.guardAction(ctx, r, actionReview{Kind: sp.Review, Effect: effect, Element: label, Text: text, Title: app}); err != nil {
						return "", err
					}
				}
				out, err := s.desktopEngineCall(ctx, r, sp.Engine, args)
				settle(err)
				if err != nil {
					return "", err
				}
				if sp.Name == "desktop_get_app_state" {
					s.markDesktopObserved(r)
				}
				return out, nil
			},
		})
	}
	return tools
}

// desktopEngineCall relays one engine tool call to the guest and unwraps the text.
func (s *Server) desktopEngineCall(ctx context.Context, r Run, tool string, args map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{"tool": tool, "arguments": args})
	if err != nil {
		return "", err
	}
	_ = s.renewComputerHold(ctx, r)
	out, err := s.microVMAction(ctx, r, "desktop.mcp", payload)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported action") {
			return "", tooloutcome.InvalidArguments("the desktop engine needs a newer computer image; this computer's guest does not support it yet. Nothing was done.")
		}
		return "", err
	}
	return desktopEngineResult(out)
}

// desktopEngineResult unwraps the guest answer {tool,text,is_error}.
func desktopEngineResult(out string) (string, error) {
	var res struct {
		Text    string `json:"text"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errors.New("the desktop engine returned an unreadable answer")
	}
	if res.IsError {
		if dsStaleRef.MatchString(res.Text) {
			return "", tooloutcome.InvalidArguments(strings.TrimSpace(res.Text) + " Read the app again with desktop_get_app_state and use the new refs. Nothing was done.")
		}
		return "", errors.New(strings.TrimSpace(res.Text))
	}
	if strings.TrimSpace(res.Text) == "" {
		return "Done.", nil
	}
	return res.Text, nil
}
