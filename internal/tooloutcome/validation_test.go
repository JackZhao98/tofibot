package tooloutcome

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInvalidArgumentsNamesTheProblem(t *testing.T) {
	params := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"url"}, "properties": map[string]any{"url": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "maximum": 100}}}
	for args, want := range map[string]string{
		`{}`:                      `missing properties: ["url"]`,
		`{"url":"x","query":"y"}`: `unexpected additional properties ["query"]`,
		`{"url":"x","limit":500}`: `/properties/limit`,
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(args), &m)
		o, ok := FromError(ValidateArguments(params, m))
		if !ok || o.Code != "invalid_arguments" || !strings.Contains(o.Message, "Problem: ") || !strings.Contains(o.Message, want) || strings.Contains(o.Message, "validating root") {
			t.Fatalf("%s: %+v", args, o)
		}
	}
}
