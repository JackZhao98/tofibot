package extensions

import (
	"strings"
	"testing"
)

// The routing policy states the order of means: connected sources first, the computer as the fallback.
func TestDiscoveryPolicyStatesConnectedSourcesFirst(t *testing.T) {
	policy, _ := DiscoveryInstructionParts(discoveryInstructions(map[string]MCPServerConfig{"x": {}}, nil))
	if strings.Contains(policy, "browser is a fine default") {
		t.Fatal("the policy still makes the browser the default")
	}
	first := strings.Index(policy, "Connected tools come first")
	search := strings.Index(policy, "search_mcp_tools lists its tools")
	fallback := strings.Index(policy, "The Bot computer is the fallback")
	if first < 0 || search < first || fallback < search {
		t.Fatalf("order of means is not stated in order: %d %d %d\n%s", first, search, fallback, policy)
	}
	for _, want := range []string{"is not connected or authorized", "the person asks for it"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy lacks %q", want)
		}
	}
}
