package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestReadinessOutcomeNamesConnectionStateAndFix(t *testing.T) {
	out := mcpReadinessOutcome("my notes", runtime.MethodAuthRequired)
	for _, want := range []string{"my notes", "auth_required", "request_connection", "/settings/connections/my%20notes", "scheduled run"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("auth_required message lacks %q: %s", want, out.Message)
		}
	}
	for _, state := range []runtime.MethodReadiness{runtime.MethodNotConfigured, runtime.MethodUnavailable, runtime.MethodUnknown} {
		o := mcpReadinessOutcome("srv", state)
		if !strings.Contains(o.Message, "srv") || !strings.Contains(o.Message, "/settings/connections/srv") || o.Code != "mcp_"+string(state) {
			t.Errorf("%s: %+v", state, o)
		}
	}
}

func TestListMCPServersShowsCachedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	cfg := `{"mcpServers":{"fresh":{"url":"http://127.0.0.1:1/mcp"},"signin":{"url":"http://127.0.0.1:1/mcp","oauth":{"client_id":"x"}},"broken":{"url":"http://127.0.0.1:1/mcp"},"plain":{"url":"http://127.0.0.1:1/mcp"}}}`
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	servers, err := loadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	m.markMCPReady("fresh", servers["fresh"])
	m.noteMCPReadiness("broken", runtime.MethodUnavailable)
	p, err := m.PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, err := discoveryTool(t, p, "list_mcp_servers").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Servers []struct{ Name, State, Link string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"fresh": "connected", "signin": "needs_sign_in", "broken": "error", "plain": "unchecked"}
	for _, s := range got.Servers {
		if s.State != want[s.Name] {
			t.Errorf("%s state=%q want %q", s.Name, s.State, want[s.Name])
		}
		if (s.State == "needs_sign_in" || s.State == "error") != (s.Link == "/settings/connections/"+s.Name) {
			t.Errorf("%s link=%q", s.Name, s.Link)
		}
	}
	if len(got.Servers) != 4 {
		t.Fatalf("servers=%s", out)
	}
}
