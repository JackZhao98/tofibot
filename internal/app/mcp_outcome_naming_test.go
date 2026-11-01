package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestMCPRefusalsNameConnectionToolAndLink(t *testing.T) {
	call := extensions.MCPCallApproval{Server: "my notes", Tool: "update_page"}
	for _, code := range []string{"mcp_needs_human_unattended", "mcp_high_risk_unattended", "mcp_review_context_missing", "approval_already_claimed"} {
		in := tooloutcome.New(tooloutcome.Denied, code, "not_executed", "base.", "explain_blocker").Err()
		out, ok := tooloutcome.FromError(nameMCPConnectionInOutcome(in, call))
		if !ok || out.Code != code || out.Server != "my notes" || out.Tool != "update_page" || out.Link != "/settings/connections/my%20notes" {
			t.Fatalf("%s: %+v", code, out)
		}
		if !strings.HasPrefix(out.Message, "base.") || !strings.Contains(out.Message, "my notes") || !strings.Contains(out.Message, "update_page") {
			t.Errorf("%s message: %s", code, out.Message)
		}
	}
	other := errors.New("plain")
	if nameMCPConnectionInOutcome(other, call) != other {
		t.Fatal("an unrelated error changed")
	}
	unrelated := tooloutcome.New(tooloutcome.Denied, "mcp_review_policy_denied", "not_executed", "x", "explain_blocker").Err()
	if nameMCPConnectionInOutcome(unrelated, call) != unrelated {
		t.Fatal("an unrelated code changed")
	}
}
