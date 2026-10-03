package tooloutcome

import (
	"encoding/json"
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
	if resolved.Validate(args) != nil {
		o := New(Validation, "invalid_arguments", "not_executed", "Arguments do not match the tool input schema. Inspect required fields, types and additionalProperties and repair the arguments.", "repair_arguments")
		o.RepairLimit = 3
		return o.Err()
	}
	return nil
}
