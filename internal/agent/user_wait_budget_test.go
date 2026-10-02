package agent

import (
	"strings"
	"testing"
	"time"
)

func TestUserWaitAccountingPreservesRunCaps(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	cfg := AgentConfig{ToolsOnly: true, MaxRunDuration: 3 * time.Minute, UserWaitDuration: func() time.Duration { return 9 * time.Minute }}
	if exceeded, why := checkRunBudget(&cfg, 1, 0, start); exceeded {
		t.Fatalf("human wait consumed budget: %s", why)
	}
	cfg.MaxRunLLMCalls = 2
	if exceeded, why := checkRunBudget(&cfg, 2, 0, start); !exceeded || !strings.Contains(why, "LLM calls") {
		t.Fatalf("call cap bypassed: %v %s", exceeded, why)
	}
	cfg.MaxRunCost = 1
	if exceeded, why := checkRunBudget(&cfg, 1, 1, start); !exceeded || !strings.Contains(why, "cost") {
		t.Fatalf("cost cap bypassed: %v %s", exceeded, why)
	}
	cfg.MaxRunCost, cfg.MaxRunLLMCalls = 0, 0
	cfg.ToolsOnly = false
	if exceeded, _ := checkRunBudget(&cfg, 1, 0, start); !exceeded {
		t.Fatal("legacy concurrent tools excluded running time")
	}
	cfg.ToolsOnly = true
	cfg.UserWaitDuration = func() time.Duration { return -time.Hour }
	if exceeded, _ := checkRunBudget(&cfg, 1, 0, start); !exceeded {
		t.Fatal("negative wait bypassed cap")
	}
	cfg.UserWaitDuration = nil
	if exceeded, _ := checkRunBudget(&cfg, 1, 0, start); !exceeded {
		t.Fatal("ordinary execution cap bypassed")
	}
}
