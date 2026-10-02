package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func extensionToolFixture(t *testing.T) (*Server, Conversation, Run, Tool) {
	t.Helper()
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, MCPConfigPath: filepath.Join(root, "mcp.json"), SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.store.CreateBot("extension owner", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, r := teamTestRun(t, s, bot)
	tools := s.extensionManagementTools(c, r)
	if len(tools) != 1 || tools[0].Name != "manage_extensions" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	return s, c, r, tools[0]
}
func extensionToolCall(t *testing.T, tool Tool, args any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExtensionToolsMCPWritesAndMaskedReads(t *testing.T) {
	s, _, _, tool := extensionToolFixture(t)
	cursor := s.store.workspaceEventCursor()
	extensionToolCall(t, tool, map[string]any{"action": "mcp_create", "name": "local", "url": "https://example.invalid/mcp?token=fixture-query-secret#fixture-fragment-secret", "headers": map[string]string{"Authorization": "fixture-header-secret"}, "oauth": map[string]any{"client_id": "public-client", "auth_server_metadata_url": "https://example.invalid/metadata?key=fixture-metadata-secret"}})
	if events := configEventsAfter(t, s, cursor); len(events) != 1 {
		t.Fatalf("create events=%v", events)
	}
	cursor = s.store.workspaceEventCursor()
	result := extensionToolCall(t, tool, map[string]any{"action": "mcp_list"})
	for _, secret := range []string{"fixture-query-secret", "fixture-fragment-secret", "fixture-header-secret", "fixture-metadata-secret"} {
		if strings.Contains(result, secret) {
			t.Fatalf("secret in result: %s", result)
		}
	}
	if !strings.Contains(result, "••••••••") || !strings.Contains(result, "public-client") {
		t.Fatalf("masked/public data missing: %s", result)
	}
	result = extensionToolCall(t, tool, map[string]any{"action": "mcp_test", "name": "local", "test_connection": true})
	if !strings.Contains(result, `"user_action_required":true`) {
		t.Fatalf("OAuth guidance missing: %s", result)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatal("read/OAuth guidance mutated state")
	}
	extensionToolCall(t, tool, map[string]any{"action": "mcp_update", "name": "local", "url": "https://example.invalid/new"})
	if events := configEventsAfter(t, s, cursor); len(events) != 1 {
		t.Fatalf("update events=%v", events)
	}
	views, err := s.extensions.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].OAuth == nil || views[0].Transport != "streamable_http" || views[0].Headers["Authorization"] != "••••••••" {
		t.Fatalf("update did not retain omitted headers/OAuth: %#v", views)
	}
	cursor = s.store.workspaceEventCursor()
	extensionToolCall(t, tool, map[string]any{"action": "mcp_delete", "name": "local"})
	if events := configEventsAfter(t, s, cursor); len(events) != 1 {
		t.Fatalf("delete events=%v", events)
	}
	views, err = s.extensions.ListMCP()
	if err != nil || len(views) != 0 {
		t.Fatalf("delete failed: %#v %v", views, err)
	}
}

func TestExtensionToolsSkillLifecycleAndActivation(t *testing.T) {
	s, _, _, tool := extensionToolFixture(t)
	cursor := s.store.workspaceEventCursor()
	extensionToolCall(t, tool, map[string]any{"action": "skill_install", "name": "fixture", "files": map[string]string{"SKILL.md": "---\nname: fixture\ndescription: independent fixture\n---\nUse local inputs.", "references/note.txt": "local fixture"}})
	skills, _ := s.extensions.ListSkills()
	if len(skills) != 1 {
		t.Fatalf("skill not installed: %#v", skills)
	}
	extensionToolCall(t, tool, map[string]any{"action": "skill_list"})
	extensionToolCall(t, tool, map[string]any{"action": "skill_delete", "name": "fixture"})
	if events := configEventsAfter(t, s, cursor); len(events) != 2 {
		t.Fatalf("want one config event per mutation, got %d", len(events))
	}
	skills, _ = s.extensions.ListSkills()
	if len(skills) != 0 {
		t.Fatal("skill not deleted")
	}
}

func TestExtensionToolsRejectStaleOrCancelledCalls(t *testing.T) {
	s, c, r, tool := extensionToolFixture(t)
	raw := json.RawMessage(`{"action":"mcp_create","name":"late","url":"https://example.invalid/mcp"}`)
	cursor := s.store.workspaceEventCursor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, raw); err != context.Canceled {
		t.Fatalf("cancelled request: %v", err)
	}
	wrong := r
	wrong.BotID = "other"
	if _, err := s.extensionManagementTools(c, wrong)[0].Execute(context.Background(), raw); err == nil {
		t.Fatal("mismatched run identity accepted")
	}
	if _, err := s.store.SetRunStatus(r.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), raw); err == nil {
		t.Fatal("stale tool mutated settings")
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 0 {
		t.Fatal("stale/cancelled call created config")
	}
	if len(configEventsAfter(t, s, cursor)) != 0 {
		t.Fatal("stale/cancelled call emitted event")
	}
}

func TestExtensionToolsValidationHasNoWritesOrSecretErrors(t *testing.T) {
	s, _, _, tool := extensionToolFixture(t)
	cursor := s.store.workspaceEventCursor()
	args := []string{
		`{"action":"mcp_create","name":"bad","url":"https://fixture-user:fixture-password@example.invalid"}`,
		`{"action":"mcp_create","name":"bad","url":"https://example.invalid","oauth":{"client_id":"public","client_secret":"fixture-secret"}}`,
		`{"action":"skill_install","name":"unsafe","files":{"SKILL.md":"---\nname: unsafe\ndescription: fixture\n---\nbody","../escape":"fixture-secret"}}`,
		`{"action":"skill_set_enabled","name":"missing"}`,
		`{"action":"mcp_test","name":"missing"}`,
		`{"action":"mcp_list","unexpected":"fixture-secret"}`,
		`{"action":"mcp_list"} {"fixture-secret":true}`,
	}
	for _, raw := range args {
		_, err := tool.Execute(context.Background(), json.RawMessage(raw))
		if err == nil {
			t.Fatalf("accepted invalid args %s", raw)
		}
		if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "fixture-password") {
			t.Fatalf("secret error: %v", err)
		}
	}
	if len(configEventsAfter(t, s, cursor)) != 0 {
		t.Fatal("invalid action emitted event")
	}
}

func TestExtensionToolsNetworkTestingIsExplicitAndCancelable(t *testing.T) {
	s, _, _, tool := extensionToolFixture(t)
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "fixture-provider-secret", http.StatusServiceUnavailable)
	}))
	defer endpoint.Close()
	extensionToolCall(t, tool, map[string]any{"action": "mcp_create", "name": "fixture", "url": endpoint.URL, "headers": map[string]string{"Authorization": "fixture-auth-secret"}})
	extensionToolCall(t, tool, map[string]any{"action": "mcp_list"})
	if requests.Load() != 0 {
		t.Fatal("save/list initiated a network connection")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"mcp_test","name":"fixture"}`)); err == nil {
		t.Fatal("implicit test accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("implicit test made a network connection")
	}
	cursor := s.store.workspaceEventCursor()
	result := extensionToolCall(t, tool, map[string]any{"action": "mcp_test", "name": "fixture", "test_connection": true})
	if requests.Load() == 0 || !strings.Contains(result, `"ok":false`) {
		t.Fatalf("explicit test missing: %s", result)
	}
	if strings.Contains(result, "fixture-provider-secret") || strings.Contains(result, "fixture-auth-secret") {
		t.Fatalf("test leaked provider/header value: %s", result)
	}
	if len(configEventsAfter(t, s, cursor)) != 0 {
		t.Fatal("network test emitted mutation event")
	}
	// Already-cancelled network tests never reach the provider.
	before := requests.Load()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"mcp_test","name":"fixture","test_connection":true}`)); err != context.Canceled {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("cancelled test reached provider")
	}
	// Seed a real secret via the existing manager, never via a tool read.
	if err := s.extensions.SaveMCP("oauth", extensions.MCPServerConfig{URL: endpoint.URL, OAuth: &extensions.OAuthConfig{ClientID: "public", ClientSecret: "fixture-oauth-secret"}}, false); err != nil {
		t.Fatal(err)
	}
	result = extensionToolCall(t, tool, map[string]any{"action": "mcp_list"})
	if strings.Contains(result, "fixture-oauth-secret") {
		t.Fatal("OAuth secret leaked")
	}
}

func TestExtensionToolsUpdatePreservesPoliciesAndOAuthSecret(t *testing.T) {
	s, _, _, tool := extensionToolFixture(t)
	root := t.TempDir()
	configPath := filepath.Join(root, "mcp.json")
	s.extensions = extensions.NewManager(extensions.Config{MCPConfigPath: configPath, SkillsDir: filepath.Join(root, "skills")})
	if err := s.extensions.SaveMCP("existing", extensions.MCPServerConfig{URL: "https://example.invalid/mcp", Transport: "sse", ToolAllowlist: []string{"read"}, ToolDenylist: []string{"delete"}, OAuth: &extensions.OAuthConfig{ClientID: "public", ClientSecret: "fixture-preserved-secret"}}, false); err != nil {
		t.Fatal(err)
	}
	extensionToolCall(t, tool, map[string]any{"action": "mcp_update", "name": "existing", "url": "https://example.invalid/mcp", "oauth": map[string]any{"client_id": "public", "scopes": []string{"read"}}})
	views, err := s.extensions.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	v := views[0]
	if len(v.ToolAllowlist) != 1 || v.ToolAllowlist[0] != "read" || len(v.ToolDenylist) != 1 || v.ToolDenylist[0] != "delete" {
		t.Fatalf("omitted global policy changed: %#v", v)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fixture-preserved-secret") || strings.Contains(string(data), "••••••••") {
		t.Fatal("OAuth update replaced actual secret with mask")
	}
	extensionToolCall(t, tool, map[string]any{"action": "mcp_update", "name": "existing", "url": "https://example.invalid/mcp", "tool_allowlist": []string{}, "tool_denylist": []string{}})
	views, err = s.extensions.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(views[0].ToolAllowlist) != 0 || len(views[0].ToolDenylist) != 0 || len(views[0].BotAllowlists) != 0 {
		t.Fatal("explicit clear was ignored")
	}
	if views[0].Transport != "sse" {
		t.Fatalf("omitted update reset transport: %#v", views[0])
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"mcp_update","name":"existing","url":"https://example.invalid/mcp","transport":"invalid"}`)); err == nil {
		t.Fatal("invalid transport accepted")
	}
	extensionToolCall(t, tool, map[string]any{"action": "mcp_update", "name": "existing", "url": "https://example.invalid/mcp", "transport": "streamable_http"})
	views, err = s.extensions.ListMCP()
	if err != nil || views[0].Transport != "streamable_http" {
		t.Fatalf("explicit transport update failed: %#v %v", views, err)
	}
	extensionToolCall(t, tool, map[string]any{"action": "mcp_create", "name": "public-client", "url": "https://example.invalid/mcp", "oauth": map[string]any{"client_id": "public"}})
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Servers map[string]extensions.MCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Servers["public-client"].OAuth.ClientSecret != "" {
		t.Fatal("create persisted a fake OAuth secret")
	}
}

func TestExtensionToolsCancelInFlightNetworkTest(t *testing.T) {
	_, _, _, tool := extensionToolFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer endpoint.Close()
	defer close(release)
	extensionToolCall(t, tool, map[string]any{"action": "mcp_create", "name": "slow", "url": endpoint.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(ctx, json.RawMessage(`{"action":"mcp_test","name":"slow","test_connection":true}`))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("test never reached local provider")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("want canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("network test ignored cancellation")
	}
}
