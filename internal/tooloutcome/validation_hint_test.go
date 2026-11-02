package tooloutcome

import (
	"strings"
	"testing"
)

func TestMissingRequiredNamesItsValues(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":    map[string]any{"type": "string"},
			"outcome": map[string]any{"type": "string", "enum": []string{"done", "blocked"}},
			"count":   map[string]any{"type": "integer"},
		},
		"required": []string{"text", "outcome", "count"},
	}
	err := ValidateArguments(params, map[string]any{"text": "hi"})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	msg := err.Error()
	for _, want := range []string{"Add the missing", "outcome (one of done, blocked)", "count (integer)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "text (") {
		t.Fatalf("a present property is not missing: %s", msg)
	}
}
