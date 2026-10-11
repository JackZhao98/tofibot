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

// The resolution table of docs/agent-plan/mcp-tool-tiers.md: the provider's
// readOnlyHint is the authority for read_only; destructiveHint changes no
// tier; a duplicated listing is never read_only by annotation.
func TestMCPToolTierProviderAnnotationTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *mcpToolTierRecord
		want MCPToolTier
		src  string
	}{
		{"no record", nil, MCPTierWrite, MCPTierSourceDefault},
		{"no annotations", &mcpToolTierRecord{Digest: "d"}, MCPTierWrite, MCPTierSourceDefault},
		{"readOnlyHint true", &mcpToolTierRecord{Digest: "d", AnnotationsPresent: true, ReadOnlyHint: true}, MCPTierReadOnly, MCPTierSourceProvider},
		{"annotations present, readOnlyHint false", &mcpToolTierRecord{Digest: "d", AnnotationsPresent: true}, MCPTierWrite, MCPTierSourceDefault},
		{"readOnlyHint with destructiveHint true", &mcpToolTierRecord{Digest: "d", AnnotationsPresent: true, ReadOnlyHint: true, DestructiveHint: boolPtr(true)}, MCPTierReadOnly, MCPTierSourceProvider},
		{"destructiveHint true alone is write, not high_risk", &mcpToolTierRecord{Digest: "d", AnnotationsPresent: true, DestructiveHint: boolPtr(true)}, MCPTierWrite, MCPTierSourceDefault},
		{"duplicated listing with readOnlyHint", &mcpToolTierRecord{Digest: "d", AnnotationsPresent: true, ReadOnlyHint: true, Duplicated: true}, MCPTierWrite, MCPTierSourceDefault},
	} {
		if v := resolveMCPToolTier("x", tc.rec, false); v.Tier != tc.want || v.Source != tc.src {
			t.Errorf("%s: got %s/%s want %s/%s", tc.name, v.Tier, v.Source, tc.want, tc.src)
		}
	}
}

// Resolution precedence: fresh override > provider annotation > write; a
// stale override only tightens; everything binds to the tool digest.
func TestMCPToolTierResolutionAndOverrideStaleness(t *testing.T) {
	rec := &mcpToolTierRecord{Digest: "d1"}
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierWrite || v.Source != MCPTierSourceDefault || v.Changed {
		t.Fatalf("default: %+v", v)
	}
	if v := resolveMCPToolTier("x", nil, true); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceOwner {
		t.Fatalf("legacy trusted without record: %+v", v)
	}
	rec.AnnotationsPresent, rec.ReadOnlyHint = true, true
	provider := resolveMCPToolTier("x", rec, false)
	if provider.Tier != MCPTierReadOnly || provider.Source != MCPTierSourceProvider {
		t.Fatalf("provider: %+v", provider)
	}
	rec.Override, rec.OverrideDigest = string(MCPTierHighRisk), "d1"
	owner := resolveMCPToolTier("x", rec, false)
	if owner.Tier != MCPTierHighRisk || owner.Source != MCPTierSourceOwner || owner.Binding == provider.Binding {
		t.Fatalf("fresh override: %+v", owner)
	}
	// The tool changed: the tightening override is kept, a loosening one is not.
	rec.Digest = "d2"
	changed := resolveMCPToolTier("x", rec, false)
	if changed.Tier != MCPTierHighRisk || !changed.Changed || changed.Source != MCPTierSourceOwner || changed.Binding == owner.Binding {
		t.Fatalf("stale tightening override: %+v", changed)
	}
	rec.AnnotationsPresent, rec.ReadOnlyHint = false, false
	rec.Override = string(MCPTierReadOnly)
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierWrite || !v.Changed || v.Source != MCPTierSourceDefault || v.Override != string(MCPTierReadOnly) {
		t.Fatalf("stale loosening override must not apply: %+v", v)
	}
	// An owner write override over a provider read-only mark tightens.
	rec.AnnotationsPresent, rec.ReadOnlyHint = true, true
	rec.Override, rec.OverrideDigest = string(MCPTierWrite), "d2"
	if v := resolveMCPToolTier("x", rec, false); v.Tier != MCPTierWrite || v.Source != MCPTierSourceOwner {
		t.Fatalf("owner write over provider read-only: %+v", v)
	}
}

func tierDescriptor(name, description string, a MCPToolAnnotations) MCPToolDescriptor {
	d := MCPToolDescriptor{RemoteName: name, Description: description, InputSchema: map[string]any{"type": "object"}, Annotations: a}
	d.Digest = MCPToolDigest(d)
	return d
}

// The store: observation records digests and the provider's annotations,
// a description change stales an override, overrides persist across a
// restart, legacy trusted names migrate once, and a tool the provider marks
// read only is read_only at once.
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
	if v := m.MCPToolTier("svc", "legacy_read"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceOwner {
		t.Fatalf("legacy before observation: %+v", v)
	}
	read := tierDescriptor("fetch", "Fetch a record", MCPToolAnnotations{Present: true, ReadOnly: true})
	write := tierDescriptor("update", "Update a record", MCPToolAnnotations{})
	legacy := tierDescriptor("legacy_read", "Legacy read", MCPToolAnnotations{})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{read, write, legacy}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if v := m.MCPToolTier("svc", "legacy_read"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceOwner || v.Override != string(MCPTierReadOnly) {
		t.Fatalf("legacy after observation: %+v", v)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceProvider || !v.ReadOnlyHint {
		t.Fatalf("provider-marked read: %+v", v)
	}
	if v := m.MCPToolTier("svc", "update"); v.Tier != MCPTierWrite || v.Source != MCPTierSourceDefault {
		t.Fatalf("unmarked tool: %+v", v)
	}
	// The provider drops its mark: the tool is reviewed again at once.
	unmarked := tierDescriptor("fetch", "Fetch a record", MCPToolAnnotations{Present: true})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{unmarked, write, legacy}); err != nil {
		t.Fatal(err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite {
		t.Fatalf("dropped mark must fall back to write: %+v", v)
	}
	// Owner override persists across a restart; a description change stales
	// it; a dropped tool disappears.
	if _, err := m.SetToolTierOverride("svc", "update", string(MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetToolTierOverride("svc", "fetch", string(MCPTierReadOnly)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetToolTierOverride("svc", "never_seen", string(MCPTierReadOnly)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("override of an unobserved tool = %v", err)
	}
	moved := tierDescriptor("fetch", "Fetch a record and email it", MCPToolAnnotations{Present: true})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{moved, write, legacy}); err != nil {
		t.Fatal(err)
	}
	again := NewManager(Config{MCPConfigPath: path})
	if v := again.MCPToolTier("svc", "update"); v.Tier != MCPTierHighRisk || v.Source != MCPTierSourceOwner {
		t.Fatalf("override after restart: %+v", v)
	}
	if v := again.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || !v.Changed || v.Override != string(MCPTierReadOnly) {
		t.Fatalf("changed state after restart: %+v", v)
	}
	list := again.ListToolTiers("svc")
	if len(list.Tools) != 3 || list.HighRisk != 1 || list.ReadOnly != 1 || list.Write != 1 {
		t.Fatalf("list = %+v", list)
	}
	if err := again.ObserveTools("svc", cfg, []MCPToolDescriptor{moved, legacy}); err != nil {
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

// A version 1 tier file (model classification and background queue) is read
// once and rewritten as version 2: owner overrides (including the migrated
// trusted list) survive, the auto/model/queue fields do not, and a tool the
// old pass had labelled read_only without a provider mark is write again.
func TestMCPToolTierFileMigrationDropsClassifierState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	raw, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"svc": cfg}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	legacyFile := `{"version":1,"servers":{"svc":{"migrated_trusted":true,"queue":{"state":"running","attempts":2,"last_error":"timeout"},"tools":{
	 "fetch":{"digest":"d-fetch","auto":"read_only","auto_digest":"d-fetch","model_tier":"read_only","reason":"model said so","classified_at":"2026-10-01T00:00:00Z"},
	 "get":{"digest":"d-get","annotations_present":true,"read_only_hint":true,"auto":"read_only","auto_digest":"d-get","model_tier":"read_only"},
	 "legacy_read":{"digest":"d-legacy","override":"read_only","override_digest":"d-legacy"},
	 "send":{"digest":"d-send","auto":"high_risk","auto_digest":"d-send","model_tier":"high_risk","override":"high_risk","override_digest":"d-send"},
	 "post":{"digest":"d-post","auto":"high_risk","auto_digest":"d-post","model_tier":"high_risk"}}}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp-tool-tiers.json"), []byte(legacyFile), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	for name, want := range map[string]struct {
		tier MCPToolTier
		src  string
	}{
		"fetch":       {MCPTierWrite, MCPTierSourceDefault},     // the model's read_only is gone
		"get":         {MCPTierReadOnly, MCPTierSourceProvider}, // the provider's mark stands
		"legacy_read": {MCPTierReadOnly, MCPTierSourceOwner},    // migrated trusted entry stays an owner override
		"send":        {MCPTierHighRisk, MCPTierSourceOwner},    // owner high_risk stays
		"post":        {MCPTierWrite, MCPTierSourceDefault},     // the model's high_risk is gone: reviewed, not forced
	} {
		if v := m.MCPToolTier("svc", name); v.Tier != want.tier || v.Source != want.src {
			t.Errorf("%s: %+v", name, v)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "mcp-tool-tiers.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{`"queue"`, `"auto"`, `"auto_digest"`, `"model_tier"`, `"reason"`, `"classified_at"`, `"version": 1`} {
		if strings.Contains(string(b), gone) {
			t.Fatalf("migrated file still carries %s: %s", gone, b)
		}
	}
	if !strings.Contains(string(b), `"version": 2`) || !strings.Contains(string(b), `"override": "high_risk"`) {
		t.Fatalf("migrated file: %s", b)
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
	// The provider marks fetch readOnlyHint: it is read_only from the first
	// discovery and executes with no gate at all, no model asked.
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := call(prepared); err != nil || remoteCalls.Load() != 1 {
		t.Fatalf("provider-marked read did not execute: %v calls=%d", err, remoteCalls.Load())
	}
	prepared.Close()
	if v := manager.MCPToolTier("fixture", "fetch"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceProvider || !v.ReadOnlyHint {
		t.Fatalf("observed through discovery: %+v", v)
	}
	// The owner tightens it to write: refused without a gate.
	if _, err := manager.SetToolTierOverride("fixture", "fetch", string(MCPTierWrite)); err != nil {
		t.Fatal(err)
	}
	if prepared, err = manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := call(prepared); err == nil || !strings.Contains(err.Error(), "approval gate") || remoteCalls.Load() != 1 {
		t.Fatalf("owner write override executed without a gate: %v calls=%d", err, remoteCalls.Load())
	}
	prepared.Close()
	if _, err := manager.SetToolTierOverride("fixture", "fetch", ""); err != nil {
		t.Fatal(err)
	}
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
	if v := manager.MCPToolTier("fixture", "fetch"); v.Source != MCPTierSourceDefault || v.Override != "" {
		t.Fatalf("policy survived an endpoint change: %+v", v)
	}
}

// Two remote tools whose run names sanitize to the same string (case,
// punctuation) are distinct tools with distinct tiers; a recorded step names
// only the run name, so the fence must treat the ambiguous name as an effect
// rather than pick one of them.
func TestMCPRunToolReadOnlyAmbiguousNameIsNeverObservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	raw, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"svc": cfg}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	read := tierDescriptor("get_issue", "Read one issue", MCPToolAnnotations{Present: true, ReadOnly: true})
	write := tierDescriptor("Get.Issue", "Assign and update one issue", MCPToolAnnotations{})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{read, write}); err != nil {
		t.Fatal(err)
	}
	if m.MCPToolTier("svc", "get_issue").Tier != MCPTierReadOnly || m.MCPToolTier("svc", "Get.Issue").Tier != MCPTierWrite {
		t.Fatal("tiers were not recorded per exact remote name")
	}
	// Map iteration order is randomized: a single lucky pass proves nothing.
	for i := 0; i < 64; i++ {
		if m.RunToolReadOnly("mcp_svc__get_issue") {
			t.Fatalf("ambiguous run name read as an observation (iteration %d)", i)
		}
	}
	// An unambiguous read-only name still maps.
	only := tierDescriptor("list_items", "List items", MCPToolAnnotations{Present: true, ReadOnly: true})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{only}); err != nil {
		t.Fatal(err)
	}
	if !m.RunToolReadOnly("mcp_svc__list_items") {
		t.Fatal("unambiguous read-only run name must map")
	}
}

// A remote name listed twice is one tools/call target with two run-facing
// schemas. No single annotation describes it: the provider's readOnlyHint
// does not make it read_only, and an owner override binds to both listings.
func TestMCPObserveToolsDuplicateListingIsNeverReadOnlyByAnnotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	raw, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"svc": cfg}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	first := tierDescriptor("fetch", "Fetch a record", MCPToolAnnotations{Present: true, ReadOnly: true})
	second := MCPToolDescriptor{RemoteName: "fetch", Description: "Fetch a record", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"recipients": map[string]any{"type": "array"}}}, Annotations: MCPToolAnnotations{Present: true, ReadOnly: true}}
	second.Digest = MCPToolDigest(second)
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{first, second}); err != nil {
		t.Fatal(err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || v.Source != MCPTierSourceDefault || !v.Duplicated {
		t.Fatalf("duplicated tool: %+v", v)
	}
	// The owner may still decide; the decision is bound to the whole set.
	if _, err := m.SetToolTierOverride("svc", "fetch", string(MCPTierReadOnly)); err != nil {
		t.Fatal(err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceOwner || v.Changed {
		t.Fatalf("override on duplicated tool: %+v", v)
	}
	changed := second
	changed.InputSchema = map[string]any{"type": "object", "properties": map[string]any{"amount": map[string]any{"type": "number"}}}
	changed.Digest = ""
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{first, changed}); err != nil {
		t.Fatal(err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierWrite || !v.Changed {
		t.Fatalf("a changed second listing must stale the override: %+v", v)
	}
	// Listed once again: the provider's mark applies.
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{first}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetToolTierOverride("svc", "fetch", ""); err != nil {
		t.Fatal(err)
	}
	if v := m.MCPToolTier("svc", "fetch"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceProvider || v.Duplicated {
		t.Fatalf("single listing: %+v", v)
	}
}
