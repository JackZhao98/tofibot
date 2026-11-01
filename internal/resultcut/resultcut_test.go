package resultcut

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestJSONArrayStaysValidAndCountsDroppedItems(t *testing.T) {
	items := make([]map[string]any, 600)
	for i := range items {
		items[i] = map[string]any{"id": i, "text": strings.Repeat("a", 100)}
	}
	raw, _ := json.Marshal(items)
	if len(raw) < 60_000 {
		t.Fatalf("fixture too small: %d", len(raw))
	}
	out, ok := JSON(string(raw), 24_000)
	if !ok || len(out) > 24_000 {
		t.Fatalf("ok=%v len=%d", ok, len(out))
	}
	var got []any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	var marker string
	kept := 0
	for _, g := range got {
		if s, isStr := g.(string); isStr {
			marker = s
		} else {
			kept++
		}
	}
	var dropped int
	if _, err := fmt.Sscanf(marker, "[%d items omitted", &dropped); err != nil || dropped+kept != 600 || !strings.Contains(marker, "offset or cursor") {
		t.Fatalf("marker=%q dropped=%d kept=%d", marker, dropped, kept)
	}
	first, _ := got[0].(map[string]any)
	last, _ := got[len(got)-1].(map[string]any)
	if first["id"] != float64(0) || last["id"] != float64(599) {
		t.Fatalf("head or tail lost: %v %v", first, last)
	}
}

func TestJSONObjectCutsTheBulkyField(t *testing.T) {
	obj := map[string]any{"status": "ok", "total": 9, "rows": make([]any, 0)}
	rows := make([]any, 0)
	for i := 0; i < 1000; i++ {
		rows = append(rows, fmt.Sprintf("row-%d-%s", i, strings.Repeat("z", 60)))
	}
	obj["rows"] = rows
	raw, _ := json.Marshal(obj)
	out, ok := JSON(string(raw), 10_000)
	if !ok || len(out) > 10_000 {
		t.Fatalf("ok=%v len=%d", ok, len(out))
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil || back["status"] != "ok" || back["total"] != float64(9) {
		t.Fatalf("err=%v back=%v", err, back)
	}
}

func TestJSONOneHugeStringIsCutInTheMiddleWithACount(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"content": strings.Repeat("é", 40_000)})
	out, ok := JSON(string(raw), 24_000)
	if !ok || len(out) > 24_000 {
		t.Fatalf("ok=%v len=%d", ok, len(out))
	}
	var back map[string]string
	if err := json.Unmarshal([]byte(out), &back); err != nil || !strings.Contains(back["content"], "bytes omitted from the middle") {
		t.Fatalf("err=%v", err)
	}
}

func TestJSONRefusesPlainText(t *testing.T) {
	if _, ok := JSON(strings.Repeat("not json ", 5000), 1000); ok {
		t.Fatal("plain text accepted")
	}
}
