package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func boolPtr(v bool) *bool { return &v }

// The merge table of docs/agent-plan/mcp-tool-tiers.md: annotations only tighten.
func TestMCPToolTierMergeTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model MCPToolTier
		a     MCPToolAnnotations
		want  MCPToolTier
	}{
		{"read_only with no annotations", MCPTierReadOnly, MCPToolAnnotations{}, MCPTierReadOnly},
		{"read_only with readOnlyHint", MCPTierReadOnly, MCPToolAnnotations{Present: true, ReadOnly: true}, MCPTierReadOnly},
		{"read_only but readOnlyHint false", MCPTierReadOnly, MCPToolAnnotations{Present: true, ReadOnly: false}, MCPTierWrite},
		{"read_only but destructiveHint true", MCPTierReadOnly, MCPToolAnnotations{Present: true, ReadOnly: true, Destructive: boolPtr(true)}, MCPTierWrite},
		{"read_only with destructiveHint false", MCPTierReadOnly, MCPToolAnnotations{Present: true, ReadOnly: true, Destructive: boolPtr(false)}, MCPTierReadOnly},
		{"write cannot be loosened by readOnlyHint", MCPTierWrite, MCPToolAnnotations{Present: true, ReadOnly: true}, MCPTierWrite},
		{"high_risk stays", MCPTierHighRisk, MCPToolAnnotations{Present: true, ReadOnly: true}, MCPTierHighRisk},
		{"invalid model tier", MCPToolTier("maybe"), MCPToolAnnotations{}, ""},
	} {
		if got := MergeMCPToolTier(tc.model, tc.a); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// Resolution precedence: fresh override > current auto > write; a stale
// override only tightens; everything binds to the tool digest.
func TestMCPToolTierResolutionAndOverrideStaleness(t *testing.T) {
	rec := &mcpToolTierRecord{Digest: "d1"}
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierWrite || v.Source != mcpTierSourceUnclassified || !v.Pending || v.Changed {
		t.Fatalf("unclassified: %+v", v)
	}
	if v := resolveMCPToolTier("x", nil, true); v.Tier != MCPTierReadOnly || v.Source != mcpTierSourceOwner {
		t.Fatalf("legacy trusted without record: %+v", v)
	}
	rec.Auto, rec.AutoDigest = string(MCPTierReadOnly), "d1"
	auto := resolveMCPToolTier("x", rec, false)
	if auto.Tier != MCPTierReadOnly || auto.Source != mcpTierSourceAuto || auto.Pending {
		t.Fatalf("auto: %+v", auto)
	}
	rec.Override, rec.OverrideDigest = string(MCPTierHighRisk), "d1"
	owner := resolveMCPToolTier("x", rec, false)
	if owner.Tier != MCPTierHighRisk || owner.Source != mcpTierSourceOwner || owner.Binding == auto.Binding {
		t.Fatalf("fresh override: %+v", owner)
	}
	// The tool changed: auto tier is stale (write, pending, changed); the
	// tightening override is kept, a loosening one is not.
	rec.Digest = "d2"
	changed := resolveMCPToolTier("x", rec, false)
	if changed.Tier != MCPTierHighRisk || !changed.Changed || !changed.Pending || changed.Source != mcpTierSourceOwner || changed.Binding == owner.Binding {
		t.Fatalf("stale tightening override: %+v", changed)
	}
	rec.Override = string(MCPTierReadOnly)
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierWrite || !v.Changed || v.Source != mcpTierSourceUnclassified || v.Override != string(MCPTierReadOnly) {
		t.Fatalf("stale loosening override must not apply: %+v", v)
	}
	rec.Auto, rec.AutoDigest = string(MCPTierHighRisk), "d2"
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierHighRisk || v.Source != mcpTierSourceAuto {
		t.Fatalf("stale loosening override under a stricter auto: %+v", v)
	}
}

func tierDescriptor(name, description string, a MCPToolAnnotations) MCPToolDescriptor {
	d := MCPToolDescriptor{RemoteName: name, Description: description, InputSchema: map[string]any{"type": "object"}, Annotations: a}
	d.Digest = MCPToolDigest(d)
	return d
}

// The store: observation records digests, classification is cached by
// digest, a description change resets the auto tier, overrides persist
// across a restart, and legacy trusted names migrate once.
func TestMCPToolTierStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp", TrustedReadOnlyTools: []string{"legacy_read"}}
	raw, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"svc": cfg}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	// Legacy trust is honoured before any observation and survives migration.
	if v := m.MCPToolTier("svc", "legacy_read"); v.Tier != MCPTierReadOnly || v.Source != mcpTierSourceOwner {
		t.Fatalf("legacy before observation: %+v", v)
	}
	read := tierDescriptor("fetch", "Fetch a record", MCPToolAnnotations{Present: true, ReadOnly: true})
	write := tierDescriptor("update", "Update a record", MCPToolAnnotations{})
	legacy := tierDescriptor("legacy_read", "Legacy read", MCPToolAnnotations{})
	pending, err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{read, write, legacy})
	if err != nil || pending != 3 {
		t.Fatalf("observe: pending=%d err=%v", pending, err)
	}
	if v := m.MCPToolTier("svc", "legacy_read"); v.Tier != MCPTierReadOnly || v.Source != mcpTierSourceOwner || v.Override != string(MCPTierReadOnly) {
		t.Fatalf("legacy after observation: %+v", v)
	}
	if got := m.PendingToolClassification("svc"); len(got) != 3 {
		t.Fatalf("pending list = %d", len(got))
	}
	// Unclassified tools are write (fail closed), never read_only.
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || !v.Pending {
		t.Fatalf("unclassified read: %+v", v)
	}
	n, err := m.RecordToolClassification("svc", []MCPToolClassification{
		{RemoteName: "fetch", Digest: read.Digest, Tier: MCPTierReadOnly, Reason: "reads"},
		{RemoteName: "update", Digest: write.Digest, Tier: MCPTierWrite},
		{RemoteName: "legacy_read", Digest: "stale-digest", Tier: MCPTierReadOnly}, // dropped: not the current digest
		{RemoteName: "ghost", Digest: "x", Tier: MCPTierReadOnly},                  // dropped: never observed
	})
	if err != nil || n != 2 {
		t.Fatalf("record: n=%d err=%v", n, err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierReadOnly || v.Source != mcpTierSourceAuto || v.Reason != "reads" {
		t.Fatalf("classified read: %+v", v)
	}
	if got := m.PendingToolClassification("svc"); len(got) != 1 || got[0].RemoteName != "legacy_read" {
		t.Fatalf("pending after classification = %+v", got)
	}
	// Re-observing unchanged tools classifies nothing again.
	if pending, err = m.ObserveTools("svc", cfg, []MCPToolDescriptor{read, write, legacy}); err != nil || pending != 1 {
		t.Fatalf("re-observe: pending=%d err=%v", pending, err)
	}
	// A description change resets the auto tier and marks the tool changed.
	moved := tierDescriptor("fetch", "Fetch a record and email it", MCPToolAnnotations{Present: true, ReadOnly: true})
	if pending, err = m.ObserveTools("svc", cfg, []MCPToolDescriptor{moved, write, legacy}); err != nil || pending != 2 {
		t.Fatalf("changed observe: pending=%d err=%v", pending, err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || !v.Changed || !v.Pending {
		t.Fatalf("changed read must fall back to write: %+v", v)
	}
	// Owner override persists across a restart; a dropped tool disappears.
	if _, err := m.SetToolTierOverride("svc", "update", string(MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetToolTierOverride("svc", "never_seen", string(MCPTierReadOnly)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("override of an unobserved tool = %v", err)
	}
	again := NewManager(Config{MCPConfigPath: path})
	if v := again.MCPToolTier("svc", "update"); v.Tier != MCPTierHighRisk || v.Source != mcpTierSourceOwner {
		t.Fatalf("override after restart: %+v", v)
	}
	if v := again.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || !v.Changed {
		t.Fatalf("changed state after restart: %+v", v)
	}
	list := again.ListToolTiers("svc")
	if len(list.Tools) != 3 || list.HighRisk != 1 || list.ReadOnly != 1 || list.Write != 1 || list.Pending != 2 {
		t.Fatalf("list = %+v", list)
	}
	if _, err := again.ObserveTools("svc", cfg, []MCPToolDescriptor{moved, legacy}); err != nil {
		t.Fatal(err)
	}
	if list := again.ListToolTiers("svc"); len(list.Tools) != 2 {
		t.Fatalf("dropped tool still listed: %+v", list)
	}
	if !again.RunToolReadOnly("mcp_svc__legacy_read") || again.RunToolReadOnly("mcp_svc__fetch") || again.RunToolReadOnly("mcp_other__fetch") {
		t.Fatal("run tool name mapping")
	}
	info, err := os.Stat(filepath.Join(dir, "mcp-tool-tiers.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("tier file mode: %v %v", info, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mcp-tool-tiers.json")); strings.Contains(string(b), "synthetic.invalid") {
		t.Fatal("tier file must not carry the endpoint")
	}
}

// The gate: a read_only tool executes without any gate; a write tool with no
// gate is refused; the proposal carries the tier binding, and a tier change
// between gate and dispatch makes the exact proposal stale.
func TestMCPCallGateHonoursToolTier(t *testing.T) {
	backend := fixtureServer("tiers", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("fetch", func(tool *mcp.Tool) { tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true} }), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureText("fetched"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://synthetic.invalid/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	call := func(p *Prepared) error {
		if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"fetch"}`)); err != nil {
			return err
		}
		_, err := discoveryTool(t, p, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__fetch","arguments":{}}`))
		return err
	}
	// Annotated read-only but unclassified: write tier, refused without a gate.
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := call(prepared); err == nil || !strings.Contains(err.Error(), "approval gate") || remoteCalls.Load() != 0 {
		t.Fatalf("unclassified hinted read executed without a gate: %v calls=%d", err, remoteCalls.Load())
	}
	prepared.Close()
	if v := manager.MCPToolTier("fixture", "fetch"); v.Tier != MCPTierWrite || !v.Pending || !v.ReadOnlyHint {
		t.Fatalf("observed through discovery: %+v", v)
	}
	// Owner read_only override: executes with no gate at all.
	if _, err := manager.SetToolTierOverride("fixture", "fetch", string(MCPTierReadOnly)); err != nil {
		t.Fatal(err)
	}
	if prepared, err = manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := call(prepared); err != nil || remoteCalls.Load() != 1 {
		t.Fatalf("read_only did not execute: %v calls=%d", err, remoteCalls.Load())
	}
	prepared.Close()
	// The gate sees the tier and binding; a change under the gate stales the proposal.
	var seen MCPCallApproval
	gate := func(_ context.Context, c MCPCallApproval) error {
		seen = c
		if _, err := manager.SetToolTierOverride("fixture", "fetch", string(MCPTierHighRisk)); err != nil {
			return err
		}
		return nil
	}
	if prepared, err = manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, gate); err != nil {
		t.Fatal(err)
	}
	err = call(prepared)
	prepared.Close()
	if seen.Tier != MCPTierReadOnly || seen.TierBinding == "" || err == nil || !strings.Contains(err.Error(), "configuration changed") || remoteCalls.Load() != 1 {
		t.Fatalf("tier change under the gate: seen=%+v err=%v calls=%d", seen, err, remoteCalls.Load())
	}
	if tier, current := manager.MCPCallTier(seen); current || tier != MCPTierWrite {
		t.Fatalf("stale binding must not be current: %v %v", tier, current)
	}
	seen.TierBinding = manager.MCPToolTier("fixture", "fetch").Binding
	if tier, current := manager.MCPCallTier(seen); !current || tier != MCPTierHighRisk {
		t.Fatalf("rebound proposal: %v %v", tier, current)
	}
	// A credential-target change forgets the policy for the new service.
	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://synthetic.invalid/other"}, true); err != nil {
		t.Fatal(err)
	}
	if v := manager.MCPToolTier("fixture", "fetch"); v.Source != mcpTierSourceUnclassified || v.Override != "" {
		t.Fatalf("policy survived an endpoint change: %+v", v)
	}
}
