package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// update_plan runs after the other calls of its parallel batch, so a step it
// marks done counts the calls sent with it (internal/app/run_plans.go); the
// other calls still run at the same time.
func TestUpdatePlanRunsAfterItsBatch(t *testing.T) {
	var mu sync.Mutex
	var order []string
	running, peak := 0, 0
	calls := []provider.ToolCall{{ID: "1", Name: "update_plan"}, {ID: "2", Name: "web_fetch"}, {ID: "3", Name: "web_fetch"}, {ID: "4", Name: "update_plan"}}
	results := executeToolsParallel(calls, func(tc provider.ToolCall) (string, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		if tc.Name == "web_fetch" {
			time.Sleep(30 * time.Millisecond)
		}
		mu.Lock()
		running--
		order = append(order, tc.Name+tc.ID)
		mu.Unlock()
		return tc.ID, nil
	}, 5)
	if len(order) != 4 || order[2] != "update_plan1" || order[3] != "update_plan4" {
		t.Fatalf("order %v", order)
	}
	if peak < 2 {
		t.Fatalf("the other calls run together: peak %d", peak)
	}
	for i, r := range results {
		if r.CallID != calls[i].ID || r.Index != i {
			t.Fatalf("results keep the order sent: %+v", results)
		}
	}
}
