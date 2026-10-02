package agent

import (
	"fmt"
	"time"
)

// checkRunBudget returns (true, reason) if any per-run cap in cfg has been
// crossed. Zero-valued caps are treated as "no limit" and skipped. The reason
// string is short and suitable for feeding back to the model inside the
// wrap-up directive (e.g. "cost $0.22 ≥ $0.20 cap" or "15 LLM calls").
//
// Called once per iteration after RecordAPICall. It is deliberately a pure
// function of its inputs so it is trivial to unit-test without spinning up
// the whole agent loop.
func checkRunBudget(cfg *AgentConfig, llmCalls int, cost float64, runStart time.Time) (bool, string) {
	if cfg == nil {
		return false, ""
	}
	if cfg.MaxRunCost > 0 && cost >= cfg.MaxRunCost {
		return true, fmt.Sprintf("cost $%.2f ≥ $%.2f cap", cost, cfg.MaxRunCost)
	}
	if cfg.MaxRunLLMCalls > 0 && llmCalls >= cfg.MaxRunLLMCalls {
		return true, fmt.Sprintf("%d LLM calls ≥ %d cap", llmCalls, cfg.MaxRunLLMCalls)
	}
	if cfg.MaxRunDuration > 0 {
		elapsed := continuationActiveElapsed(cfg, runStart)
		if elapsed >= cfg.MaxRunDuration {
			return true, fmt.Sprintf("duration %s ≥ %s cap", elapsed.Round(time.Second), cfg.MaxRunDuration)
		}
	}
	return false, ""
}
