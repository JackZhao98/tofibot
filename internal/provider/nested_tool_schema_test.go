package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// nestedToolSchema has the shape of a hosted page-update tool: a nested data
// object whose command enum and conditional fields (new_str) must reach the
// model unchanged by every request converter.
func nestedToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{"data": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"page_id": map[string]any{"type": "string"},
				"command": map[string]any{"type": "string", "enum": []any{"replace_content", "update_properties"}},
				"new_str": map[string]any{"type": "string"},
			},
			"required": []any{"page_id", "command"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"command": map[string]any{"const": "replace_content"}}, "required": []any{"new_str"}},
				map[string]any{"properties": map[string]any{"command": map[string]any{"const": "update_properties"}}},
			},
		}},
		"required": []any{"data"},
	}
}

func wireJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConvertersKeepNestedToolSchema(t *testing.T) {
	want := wireJSON(t, nestedToolSchema())
	req := &ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Tools: []Tool{{Name: "update", Description: "d", Parameters: nestedToolSchema()}}}

	anthropic, _ := (&anthropicProvider{}).buildPayload(req)
	var got []any
	for _, tool := range anthropic["tools"].([]map[string]any) {
		if tool["name"] == "update" {
			got = append(got, wireJSON(t, tool["input_schema"]))
		}
	}
	responses := (&openaiResponses{}).buildPayload(req, false)
	for _, tool := range responses["tools"].([]map[string]interface{}) {
		got = append(got, wireJSON(t, tool["parameters"]))
	}
	completions := (&openaiChatCompletions{}).buildPayload(req, false)
	for _, tool := range completions["tools"].([]map[string]interface{}) {
		got = append(got, wireJSON(t, tool["function"].(map[string]interface{})["parameters"]))
	}
	if len(got) != 3 {
		t.Fatalf("converters produced %d schemas", len(got))
	}
	for i, schema := range got {
		if !reflect.DeepEqual(schema, want) {
			t.Fatalf("converter %d changed the schema: %v", i, schema)
		}
		if !strings.Contains(string(mustMarshal(t, schema)), `"new_str"`) {
			t.Fatalf("converter %d lost new_str", i)
		}
	}
	if s, _ := responses["tools"].([]map[string]interface{})[0]["strict"].(bool); s {
		t.Fatal("strict mode would reject oneOf; it must stay off for generic tools")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
