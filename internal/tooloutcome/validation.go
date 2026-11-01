package tooloutcome

import (
	"encoding/json"
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
		o := New(Validation, "invalid_arguments", "not_executed", "Arguments do not match the tool input schema. Problem: "+validationProblem(verr)+". Repair the arguments and call again.", "repair_arguments")
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
