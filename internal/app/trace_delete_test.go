package app

import (
	"strings"
	"testing"
)

// Deleting the pictures of a run that is still going keeps the numbering: the
// capture numbers the Bot holds must not name other pictures afterwards.
func TestDeletingTheTraceOfARunningRunDoesNotRestartCaptureNumbers(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 2)
	if n, err := s.store.deleteRunTrace(run.ID); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	rows := captures(t, s, run)
	if len(rows) != 2 || rows[0].AttachmentID != "" || !rows[0].Redacted || rows[0].Label != "" {
		t.Fatalf("%+v", rows)
	}
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	if !strings.Contains(out, `"capture_seq":3`) {
		t.Fatalf("the next capture must be number 3: %.200s", out)
	}
}
