package tooloutcome

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

func ValidateArguments(params map[string]any, args map[string]any) error {
	b, err := json.Marshal(params)
	var schema jsonschema.Schema
	if err == nil {
		err = json.Unmarshal(b, &schema)
	}
	if err != nil {
		return New(Validation, "invalid_schema", "not_executed", "Tool input schema is invalid; refresh it before calling.", "refresh_schema").Err()
	}
	// No external Loader: third-party schemas cannot initiate network reads.
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return New(Validation, "unresolved_schema", "not_executed", "Tool input schema could not be resolved locally; refresh it or use another capability.", "refresh_schema").Err()
	}
	if verr := resolved.Validate(args); verr != nil {
		// The validator names the field and the rule (missing, unexpected,
		// wrong type, not in enum). Without it the model can only guess.
		o := New(Validation, "invalid_arguments", "not_executed", "Arguments do not match the tool input schema. Problem: "+validationProblem(verr)+"."+missingHints(params, args)+" Repair the arguments and call again.", "repair_arguments")
		o.RepairLimit = 3
		return o.Err()
	}
	return nil
}

// validationProblem is the validator's own explanation, bounded, without the
// "validating root:" noise.
func validationProblem(err error) string {
	text := strings.TrimSpace(err.Error())
	text = strings.TrimPrefix(text, "validating root: ")
	const limit = 600
	if len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

// missingHints names what each missing required top-level property accepts, so
// a model that left one out can fill it in instead of resending the same call.
func missingHints(params map[string]any, args map[string]any) string {
	props, _ := params["properties"].(map[string]any)
	var hints []string
	for _, name := range requiredNames(params["required"]) {
		if _, ok := args[name]; ok {
			continue
		}
		prop, _ := props[name].(map[string]any)
		hint := name
		if values := enumValues(prop["enum"]); len(values) > 0 {
			hint += " (one of " + strings.Join(values, ", ") + ")"
		} else if kind, ok := prop["type"].(string); ok && kind != "" {
			hint += " (" + kind + ")"
		}
		hints = append(hints, hint)
	}
	if len(hints) == 0 {
		return ""
	}
	sort.Strings(hints)
	text := " Add the missing " + strings.Join(hints, "; ") + "."
	if len(text) > 400 {
		text = text[:400] + "…"
	}
	return text
}

func requiredNames(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

func enumValues(v any) []string {
	var out []string
	switch list := v.(type) {
	case []string:
		out = append(out, list...)
	case []any:
		for _, item := range list {
			out = append(out, fmt.Sprint(item))
		}
	}
	return out
}
