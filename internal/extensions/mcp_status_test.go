package extensions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPStatusPersistsAndFollowsEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"a":{"url":"https://a.example/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return at }

	views, err := m.ListMCP()
	if err != nil || views[0].Status != nil {
		t.Fatalf("never-checked server must have no status: %v %+v", err, views)
	}
	m.recordStatus("a", "https://a.example/mcp", MCPInspection{ToolCount: 49})
	// A fresh Manager (a restart) reads the same state.
	views, _ = NewManager(Config{MCPConfigPath: path}).ListMCP()
	st := views[0].Status
	if st == nil || !st.OK || st.ToolCount != 49 || !st.CheckedAt.Equal(at) {
		t.Fatalf("status not persisted: %+v", st)
	}
	// A failure keeps the last tool count and names only a class.
	m.recordStatus("a", "https://a.example/mcp", MCPInspection{Diagnostics: []Diagnostic{{Server: "a", Message: "secret upstream detail"}}})
	views, _ = m.ListMCP()
	st = views[0].Status
	if st.OK || st.ErrorClass != "failed" || st.ToolCount != 49 {
		t.Fatalf("failure status wrong: %+v", st)
	}
	m.recordStatus("a", "https://a.example/mcp", MCPInspection{AuthRequired: true, Diagnostics: []Diagnostic{{Message: "x"}}})
	views, _ = m.ListMCP()
	if views[0].Status.ErrorClass != "auth" {
		t.Fatalf("auth class: %+v", views[0].Status)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".mcp-status.json"))
	if string(b) != "" && containsText(string(b), "secret upstream detail") {
		t.Fatal("upstream detail leaked into status file")
	}
	// Changing the endpoint drops the stale status.
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"a":{"url":"https://b.example/mcp"}}}`), 0600)
	views, _ = m.ListMCP()
	if views[0].Status != nil {
		t.Fatalf("status must not survive an endpoint change: %+v", views[0].Status)
	}
	// Deleting forgets it.
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"a":{"url":"https://a.example/mcp"}}}`), 0600)
	m.recordStatus("a", "https://a.example/mcp", MCPInspection{ToolCount: 3})
	if err := m.DeleteMCP("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.loadStatuses()["a"]; ok {
		t.Fatal("status survived delete")
	}
}

func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A tool call that comes back "API not enabled" (Google's structured SERVICE_DISABLED) is recorded on the connection,
// so the row can say so and offer the enable link; unstructured failures and other servers record nothing.
func TestToolCallFailureRecordsAPINotEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	const url = "https://gmailmcp.googleapis.com/mcp/v1"
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"gmail":{"url":"`+url+`"},"other":{"url":"https://other.example/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	m.recordStatus("gmail", url, MCPInspection{ToolCount: 23})
	body := `status 403: {"error":{"code":403,"status":"PERMISSION_DENIED","message":"PRIVATE","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"gmail.googleapis.com","consumer":"projects/622599621014","activationUrl":"https://evil.invalid/x"}}]}}`
	m.noteToolFailure("other", "https://other.example/mcp", errors.New(body))
	m.noteToolFailure("gmail", url, errors.New("SERVICE_DISABLED accessNotConfigured PRIVATE"))
	views, _ := m.ListMCP()
	for _, v := range views {
		if v.Name == "gmail" && (!v.Status.OK || v.Status.ErrorCode != "") {
			t.Fatalf("unstructured failure changed the status: %+v", v.Status)
		}
		if v.Name == "other" && v.Status != nil {
			t.Fatalf("a non-Google server recorded a Google failure: %+v", v.Status)
		}
	}
	m.noteToolFailure("gmail", url, errors.New(body))
	views, _ = m.ListMCP()
	var st *MCPStatus
	for _, v := range views {
		if v.Name == "gmail" {
			st = v.Status
		}
	}
	if st == nil || st.OK || st.ErrorCode != "api_disabled" || st.API != "gmail.googleapis.com" || st.ToolCount != 23 ||
		st.EnableURL != "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=622599621014" {
		t.Fatalf("not recorded: %+v", st)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".mcp-status.json"))
	if containsText(string(raw), "PRIVATE") || containsText(string(raw), "evil.invalid") {
		t.Fatal("provider text reached the status file")
	}
	// A later successful check clears it.
	m.recordStatus("gmail", url, MCPInspection{ToolCount: 23})
	views, _ = m.ListMCP()
	for _, v := range views {
		if v.Name == "gmail" && (!v.Status.OK || v.Status.ErrorCode != "") {
			t.Fatalf("not cleared by a good check: %+v", v.Status)
		}
	}
}
