package guest

// Desktop accessibility engine (decision 2026-10-30): the guest runs
// open-computer-use (iFurySt, MIT; a Go binary that embeds a Python AT-SPI
// bridge) as a stdio MCP server, one process per Bot, started on the first
// call and kept for the element indexes it remembers. It talks to the
// desktop's single session bus (desktop_bus_linux.go). This file holds the
// parts with no OS dependency: the tool allowlist, the argument checks and the
// result shape. The process lives in ocu_linux.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	// ocuBinaryDefault is where the image build puts the pinned release.
	ocuBinaryDefault = "/usr/local/bin/open-computer-use"
	// ocuResultCap bounds one tool result; a longer tree is cut with a note.
	ocuResultCap = 200 * 1024

	ocuAppMax  = 200
	ocuTextMax = 20000
)

// ocuTools is the allowlist of server tools the guest relays and, for each,
// the argument names it passes on. Drag, coordinate clicks (x, y) and the
// click, key and window placement methods are left out: coordinates belong to
// the screenshot tools, and the methods only select macOS implementations or
// global pointer synthesis.
var ocuTools = map[string][]string{
	"list_apps":                {},
	"get_app_state":            {"app", "text_limit", "max_tree_nodes", "max_tree_depth"},
	"click":                    {"app", "element_index", "click_count", "mouse_button"},
	"perform_secondary_action": {"app", "element_index", "action"},
	"scroll":                   {"app", "element_index", "direction", "pages"},
	"set_value":                {"app", "element_index", "value"},
	"type_text":                {"app", "text"},
	"press_key":                {"app", "key"},
}

// ocuRequired lists the arguments a call must carry besides the allowlist.
var ocuRequired = map[string][]string{
	"get_app_state":            {"app"},
	"click":                    {"app", "element_index"},
	"perform_secondary_action": {"app", "element_index", "action"},
	"scroll":                   {"app", "element_index", "direction"},
	"set_value":                {"app", "element_index", "value"},
	"type_text":                {"app", "text"},
	"press_key":                {"app", "key"},
}

// ocuBinary is replaced only by tests.
var ocuBinary = ocuBinaryDefault

var errOCUMissing = errors.New("desktop accessibility engine is not installed in this Bot computer image; use the classic desktop engine or update the Bot computer image")

type ocuCallArgs struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// checkOCUCall validates one relayed call and returns the arguments to send.
func checkOCUCall(in ocuCallArgs) (map[string]any, error) {
	allowed, ok := ocuTools[in.Tool]
	if !ok {
		return nil, rejectedf("unsupported desktop tool %q", in.Tool)
	}
	args := map[string]any{}
	if len(in.Arguments) > 0 && string(in.Arguments) != "null" {
		if err := json.Unmarshal(in.Arguments, &args); err != nil {
			return nil, rejectedf("arguments must be a JSON object")
		}
	}
	for key := range args {
		if !slices.Contains(allowed, key) {
			return nil, rejectedf("%s does not take %q", in.Tool, key)
		}
	}
	// The engine takes the element index as a string; a model may send a number.
	if n, ok := args["element_index"].(float64); ok {
		if n < 0 || n != float64(int64(n)) {
			return nil, rejectedf("element_index must be a whole number from the latest get_app_state")
		}
		args["element_index"] = fmt.Sprintf("%d", int64(n))
	}
	for _, key := range ocuRequired[in.Tool] {
		// An empty value is how a field is cleared.
		if v, present := args[key]; !present || v == nil || (v == "" && key != "value") {
			return nil, rejectedf("%s needs %s", in.Tool, key)
		}
	}
	for key, value := range args {
		switch key {
		case "app", "element_index", "action", "direction", "key", "mouse_button", "text", "value":
			text, ok := value.(string)
			if !ok || strings.ContainsRune(text, 0) {
				return nil, rejectedf("%s must be text", key)
			}
			if key == "app" && strings.TrimSpace(text) == "" {
				return nil, rejectedf("%s needs app, the name of a running app", in.Tool)
			}
			limit := ocuTextMax
			if key == "app" {
				limit = ocuAppMax
			} else if key != "text" && key != "value" {
				limit = 200
			}
			if len(text) > limit {
				return nil, rejectedf("%s is longer than %d bytes", key, limit)
			}
		case "max_tree_nodes", "max_tree_depth", "click_count", "pages":
			max := map[string]float64{"max_tree_nodes": 5000, "max_tree_depth": 64, "click_count": 3, "pages": 20}[key]
			n, ok := value.(float64)
			if !ok || n <= 0 || n > max {
				return nil, rejectedf("%s must be a number above 0 and at most %.0f", key, max)
			}
		case "text_limit":
			switch t := value.(type) {
			case string:
				if t != "max" {
					return nil, rejectedf("text_limit must be a number or \"max\"")
				}
			case float64:
				if t <= 0 || t > 100000 {
					return nil, rejectedf("text_limit must be above 0 and at most 100000")
				}
			default:
				return nil, rejectedf("text_limit must be a number or \"max\"")
			}
		}
	}
	return args, nil
}

// ocuResult is what the host receives: the server's text (the app's element
// tree with indexes, or the action's read-back), whether the server called it
// an error, and how many images were left out.
//
// shape, when set, rewrites the text before the size cap (states added, a long
// tree pruned); see ocu_tree.go.
func ocuResult(tool string, raw json.RawMessage, shape func(string) string) (map[string]any, error) {
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("desktop engine returned an invalid result: %w", err)
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
	out := text.String()
	if shape != nil && tool != "list_apps" {
		out = shape(out)
	}
	truncated := false
	if len(out) > ocuResultCap {
		out = strings.ToValidUTF8(out[:ocuResultCap], "") + "\n[result cut: the tree is longer than the limit; lower max_tree_depth or max_tree_nodes]"
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
