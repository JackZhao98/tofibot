package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestGateRefusalsAreNotCountedAsFailedCalls(t *testing.T) {
	s, c, r, _ := outcomeFixture(t, "")
	for i, code := range append([]string{"some_other_refusal"}, gateOutcomeCodes...) {
		o := tooloutcome.New(tooloutcome.Transient, code, "not_executed", "Synthetic.", "retry")
		for _, status := range []string{"queued", "running", "failed"} {
			ev := runtime.ToolEvent{CallID: "call-" + code, Name: "say", Arguments: "{}", Status: status}
			if status == "failed" {
				ev.Outcome = &o
				ev.Result = o.JSON()
			}
			if err := s.store.RecordToolEvent(c.ID, r.BotID, r.ID, ev); err != nil {
				t.Fatalf("%d %s: %v", i, code, err)
			}
		}
	}
	got, err := s.store.ToolActivitySummaries(c.ID, []string{r.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("summaries = %+v %v", got, err)
	}
	if got[0].FailedCount != 1 {
		t.Fatalf("failed = %d, want 1 (only the ungated refusal)", got[0].FailedCount)
	}
}

func TestRevisionMessageStatesTheRoundAndTheStopRule(t *testing.T) {
	criteria := rubricCriteria(answerRubric)
	gap := []criterionResult{{ID: "C2", Result: "gap", Note: "Leaves the date out."}}
	first := answerRevision(gap, criteria, 1, 2)
	last := answerRevision(gap, criteria, 2, 2)
	if !strings.Contains(first, "revision 1 of at most 2") || !strings.Contains(last, "revision 2 of at most 2") {
		t.Fatalf("rounds missing: %q / %q", first, last)
	}
	if !strings.Contains(last, "delivered as it stands") || strings.Contains(first, "delivered as it stands") {
		t.Fatalf("stop rule: %q / %q", first, last)
	}
	if !strings.Contains(first, "outcome blocked") {
		t.Fatalf("blocked exit missing: %q", first)
	}
}
