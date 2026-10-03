package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func TestLocalMCPAdapterAttachmentUsesAuthenticatedIdentity(t *testing.T) {
	identity := strings.Repeat("a", 64)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-private-token" {
			t.Error("identity request missing private auth")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path != "/v1/plugins" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"plugins": []mcprunner.Status{{ID: "notion-fixture", AdapterIdentity: identity}}})
	}))
	defer remote.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("synthetic-private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", remote.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", token)
	server, err := NewServer(Config{DataDir: dir, MCPConfigPath: filepath.Join(dir, "mcp.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.attachLocalMCP("notion-fixture"); err != nil {
		t.Fatal(err)
	}
	views, err := server.extensions.ListMCP()
	if err != nil || len(views) != 1 {
		t.Fatal("missing attached adapter")
	}
	firstURL := views[0].URL
	u, err := url.Parse(firstURL)
	if err != nil || u.Query().Get(mcprunner.AdapterIdentityQuery) != identity {
		t.Fatal("adapter identity missing from config URL")
	}
	if strings.Contains(views[0].Headers["Authorization"], "synthetic-private-token") {
		t.Fatal("private token exposed")
	}
	identity = strings.Repeat("b", 64)
	if err := server.attachLocalMCP("notion-fixture"); err != nil {
		t.Fatal(err)
	}
	views, err = server.extensions.ListMCP()
	if err != nil || views[0].URL == firstURL {
		t.Fatal("reattachment retained old adapter identity")
	}
	before, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity = "untrusted-format"
	if err := server.attachLocalMCP("notion-fixture"); err == nil {
		t.Fatal("malformed adapter identity accepted")
	}
	after, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("rejected identity edit mutated config")
	}
}
