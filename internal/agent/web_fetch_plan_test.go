package agent

import (
	"strings"
	"testing"
)

func TestWebFetchPlanBoundsTheRunAndSpeaksOnlyWhenClose(t *testing.T) {
	if uses, note := webFetchPlan(5, 0, 100); uses != 5 || note != "" {
		t.Fatalf("no run limit: %d %q", uses, note)
	}
	if uses, note := webFetchPlan(5, 20, 3); uses != 5 || note != "" {
		t.Fatalf("far from the limit: %d %q", uses, note)
	}
	if uses, note := webFetchPlan(5, 20, 17); uses != 3 || !strings.Contains(note, "3") {
		t.Fatalf("close: %d %q", uses, note)
	}
	if uses, note := webFetchPlan(5, 20, 19); uses != 1 || !strings.Contains(note, "1") {
		t.Fatalf("one left: %d %q", uses, note)
	}
	if uses, note := webFetchPlan(5, 20, 20); uses != 0 || note == "" {
		t.Fatalf("used up: %d %q", uses, note)
	}
}
