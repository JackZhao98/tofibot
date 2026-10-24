package agent

import "testing"

// A refill must leave the step budget inside the assistant-turn cap, or the run
// dies at turn MaxAssistantTurnIndex+1 instead of ending cleanly.
func TestRefilledStepLimitStaysInsideTheTurnCap(t *testing.T) {
	for _, turn := range []int{0, 100, 200, 262, 290} {
		limit := refilledStepLimit(MaxStepsWithProgressReports, turn)
		if turn+limit > MaxAssistantTurnIndex-2 {
			t.Fatalf("turn %d: limit %d reaches turn %d, past the cap %d", turn, limit, turn+limit, MaxAssistantTurnIndex)
		}
	}
	if got := refilledStepLimit(MaxStepsWithProgressReports, 0); got != MaxStepsWithProgressReports {
		t.Fatalf("a fresh run lost budget: %d", got)
	}
}
