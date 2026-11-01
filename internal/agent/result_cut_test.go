package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTruncateToolResultKeepsJSONValidAndNamesTheCut(t *testing.T) {
	rows := make([]map[string]any, 800)
	for i := range rows {
		rows[i] = map[string]any{"id": i, "note": strings.Repeat("n", 90)}
	}
	raw, _ := json.Marshal(map[string]any{"rows": rows, "total": 800})
	out := truncateToolResultForModel(string(raw), maxToolsOnlyResultChars)
	if len(out) > maxToolsOnlyResultChars {
		t.Fatalf("len=%d", len(out))
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("cut result is not JSON: %v", err)
	}
	if !strings.Contains(out, "items omitted from the middle") || !strings.Contains(out, "offset or cursor") {
		t.Fatal("cut does not say what was removed or how to get it")
	}
}

func TestTruncateToolResultTextCutSaysHowToContinue(t *testing.T) {
	out := truncateToolResultForModel(strings.Repeat("word ", 20_000), maxToolsOnlyResultChars)
	if !strings.Contains(out, "omitted") || !strings.Contains(out, "offset or cursor") {
		t.Fatal(out[:200])
	}
}
