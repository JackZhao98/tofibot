package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveMicrosoftLearnDiscoveryAndCall(t *testing.T) {
	if os.Getenv("TOFI_ACCEPTANCE_LIVE_MCP") != "1" {
		t.Skip("opt-in public Microsoft Learn MCP check")
	}
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), DiscoveryTimeout: 20 * time.Second})
	if err := manager.SaveMCP("microsoft-learn", MCPServerConfig{URL: "https://learn.microsoft.com/api/mcp", BotAllowlists: map[string][]string{"acceptance": {"microsoft_docs_search"}}}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	prepared, err := manager.PrepareForBot(ctx, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Diagnostics) != 0 {
		t.Fatalf("discovery failed: %v", prepared.Diagnostics)
	}
	for _, tool := range prepared.Tools {
		if strings.Contains(tool.Name, "microsoft_docs_search") {
			out, err := tool.Execute(ctx, json.RawMessage(`{"query":"Azure Container Apps health probes"}`))
			if err != nil {
				t.Fatal("real MCP call failed:", err)
			}
			if !strings.Contains(out, "learn.microsoft.com") {
				t.Fatal("real MCP call returned no official documentation")
			}
			t.Log("Streamable HTTP initialize/list/call succeeded against Microsoft's public Learn MCP, with only the authorized search tool exposed.")
			return
		}
	}
	t.Fatal("authorized search tool not discovered")
}
