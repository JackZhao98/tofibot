package agent

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestSlowBrowserAndLostConnectionNeverFenceARepeat(t *testing.T) {
	obs := observationIdentity("lookup", `{}`)
	for _, code := range []string{tooloutcome.BrowserSlow, tooloutcome.ConnectionLost} {
		slow := tooloutcome.New(tooloutcome.Transient, code, "unknown", "no answer", "retry")
		var records []ToolRecoveryRecord
		for i := 0; i < 6; i++ {
			records = append(records, ToolRecoveryRecord{Call: provider.ToolCall{ID: "x", Name: "lookup", Arguments: `{}`}, Identity: &obs, Outcome: slow, Epoch: 0})
		}
		if got := toolRecoveryIdentityGuardAt(records, obs, 0); got != nil {
			t.Fatalf("%s fenced the repeat: %+v", code, got)
		}
	}
}
