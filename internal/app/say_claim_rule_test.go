package app

import (
	"strings"
	"testing"
)

// A claimed change needs the tool result that made it.
func TestSayToolRequiresASuccessfulCallBeforeClaimingAChange(t *testing.T) {
	if !strings.Contains(sayToolDescription, "returned success in this run") {
		t.Fatal("the say tool does not tie a claimed change to a successful tool call")
	}
}
