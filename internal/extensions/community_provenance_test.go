package extensions

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A package a person installed is community code. Its own readOnlyHint is shown
// but never obeyed: a tool it calls read only still goes to review, so a
// mislabelled write cannot run without a person. Curated and remote servers
// keep the provider-authority contract.
func TestCommunityServerNeverTrustsProviderReadOnlyHint(t *testing.T) {
	backend := fixtureServer("community", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("fetch", func(tool *mcp.Tool) { tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true} }), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureText("fetched"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	community := &MCPProvenance{Origin: MCPOriginCommunity, Kind: "npm", Package: "@example/community-server", Version: "1.2.3"}
	for name, provenance := range map[string]*MCPProvenance{"local_community": community, "curated": nil} {
		if err := manager.SaveMCP(name, MCPServerConfig{URL: "https://synthetic.invalid/" + name, Provenance: provenance}, false); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	call := func(server string) error {
		if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"`+server+`","query":"fetch"}`)); err != nil {
			return err
		}
		_, err := discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_`+server+`__fetch","arguments":{}}`))
		return err
	}
	if err := call("local_community"); err == nil || !strings.Contains(err.Error(), "approval gate") || remoteCalls.Load() != 0 {
		t.Fatalf("community tool marked read-only ran without a gate: %v calls=%d", err, remoteCalls.Load())
	}
	if v := manager.MCPToolTier("local_community", "fetch"); v.Tier != MCPTierWrite || v.Source != MCPTierSourceDefault || !v.ReadOnlyHint {
		t.Fatalf("the hint must be visible but not obeyed: %+v", v)
	}
	if err := call("curated"); err != nil || remoteCalls.Load() != 1 {
		t.Fatalf("a non-community server keeps provider authority: %v calls=%d", err, remoteCalls.Load())
	}
	// The owner's own decision still applies to a community tool.
	if _, err := manager.SetToolTierOverride("local_community", "fetch", string(MCPTierReadOnly)); err != nil {
		t.Fatal(err)
	}
	if v := manager.MCPToolTier("local_community", "fetch"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceOwner {
		t.Fatalf("owner override on a community tool: %+v", v)
	}
}

// Provenance is operator-set: settings and Bot edits cannot supply it, it is
// kept while the server still dials the same address, and a different package
// or version is a different server identity (standing allowances and stale
// approvals bind to the fingerprint).
func TestCommunityProvenanceIsServerSetAndPartOfIdentity(t *testing.T) {
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	first := &MCPProvenance{Origin: MCPOriginCommunity, Kind: "npm", Package: "@example/community-server", Version: "1.2.3"}
	url := "https://synthetic.invalid/mcp/local_x"
	if err := manager.SaveMCP("local_x", MCPServerConfig{URL: url, Provenance: first}, false); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.MCPConfigVersion("local_x")
	if err := manager.SaveMCP("local_x", MCPServerConfig{URL: url, ToolAllowlist: []string{"fetch"}}, true); err != nil {
		t.Fatal(err)
	}
	views, err := manager.ListMCP()
	if err != nil || len(views) != 1 || views[0].Provenance == nil || *views[0].Provenance != *first {
		t.Fatalf("an ordinary edit must keep provenance: %+v err=%v", views, err)
	}
	if err := manager.SaveMCP("local_x", MCPServerConfig{URL: url, ToolAllowlist: []string{"fetch"}, Provenance: &MCPProvenance{Origin: MCPOriginCommunity, Kind: "npm", Package: "@example/community-server", Version: "1.2.4"}}, true); err != nil {
		t.Fatal(err)
	}
	after, _ := manager.MCPConfigVersion("local_x")
	if before == after {
		t.Fatal("a different package version must change the server identity")
	}
	if err := manager.SaveMCP("local_x", MCPServerConfig{URL: "https://synthetic.invalid/elsewhere"}, true); err != nil {
		t.Fatal(err)
	}
	if views, _ = manager.ListMCP(); views[0].Provenance != nil {
		t.Fatalf("provenance must not follow a new address: %+v", views[0].Provenance)
	}
}
