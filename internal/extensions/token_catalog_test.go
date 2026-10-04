package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHeaderCredentialEditSemantics(t *testing.T) {
	for _, header := range []string{"Authorization", "Context7-API-Key", "X-Custom-Key"} {
		for _, test := range []struct {
			name    string
			headers map[string]string
			want    string
		}{
			{"omitted-preserves", nil, "synthetic-old"},
			{"masked-preserves", map[string]string{header: maskedSecret}, "synthetic-old"},
			{"empty-preserves", map[string]string{header: ""}, "synthetic-old"},
			{"explicit-replaces", map[string]string{header: "synthetic-new"}, "synthetic-new"},
			{"empty-map-removes", map[string]string{}, ""},
			{"deleted-key-removes", map[string]string{"X-Public": "fixture"}, ""},
		} {
			t.Run(header+"/"+test.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "mcp.json")
				manager := NewManager(Config{MCPConfigPath: path})
				if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://fixture.test/mcp", Headers: map[string]string{header: "synthetic-old"}}, false); err != nil {
					t.Fatal(err)
				}
				if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://fixture.test/mcp", Headers: test.headers}, true); err != nil {
					t.Fatal(err)
				}
				stored, err := loadServers(path)
				if err != nil || stored["fixture"].Headers[header] != test.want {
					t.Fatal("saved header semantics differ from expected")
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("MCP configuration is not private")
				}
				views, err := manager.ListMCP()
				if err != nil || len(views) != 1 || (test.want != "" && views[0].Headers[header] != maskedSecret) {
					t.Fatal("saved credential was not masked")
				}
			})
		}
	}
}

func TestMCPDestinationEditCannotRetainCredentials(t *testing.T) {
	for _, destination := range []string{"https://other.test/mcp", "http://fixture.test/mcp", "https://fixture.test/other", "https://fixture.test:8443/mcp", "https://fixture.test/mcp?tenant=other"} {
		for _, headers := range []map[string]string{nil, {"Authorization": maskedSecret}, {"Authorization": ""}} {
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			original := MCPServerConfig{URL: "https://fixture.test/mcp", Headers: map[string]string{"Authorization": "synthetic-secret"}}
			if err := manager.SaveMCP("fixture", original, false); err != nil {
				t.Fatal(err)
			}
			err := manager.SaveMCP("fixture", MCPServerConfig{URL: destination, Headers: headers}, true)
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("destination edit retained or exposed credential")
			}
			stored, err := loadServers(manager.cfg.MCPConfigPath)
			if err != nil || !reflect.DeepEqual(stored["fixture"].Headers, original.Headers) || stored["fixture"].URL != original.URL {
				t.Fatal("rejected edit changed saved configuration")
			}
		}
	}
	for _, headers := range []map[string]string{{}, {"Authorization": "synthetic-new"}} {
		manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
		if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://fixture.test/mcp", Headers: map[string]string{"Authorization": "synthetic-old"}}, false); err != nil {
			t.Fatal(err)
		}
		if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://other.test/mcp", Headers: headers}, true); err != nil {
			t.Fatal("explicit removal/replacement should allow destination edit")
		}
	}
}

func TestMCPDestinationEditCannotImplicitlyRetainOAuthClient(t *testing.T) {
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	original := MCPServerConfig{URL: "https://fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "fixture", ClientSecret: "synthetic-client-secret"}}
	if err := manager.SaveMCP("fixture", original, false); err != nil {
		t.Fatal(err)
	}
	for _, oauth := range []*OAuthConfig{nil, {ClientID: "fixture", ClientSecret: maskedSecret}} {
		if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://other.test/mcp", OAuth: oauth}, true); err == nil || strings.Contains(err.Error(), "synthetic-client-secret") {
			t.Fatal("destination edit implicitly retained/exposed OAuth client")
		}
	}
}

// Authentication contract checks use a disposable server and discovery only.
// No provider package, live credential, or remote tool is executed here.
func TestTokenCatalogAuthenticationDiscoveryOnly(t *testing.T) {
	for _, auth := range []struct{ name, header, value string }{
		{"none", "", ""},
		{"bearer", "Authorization", "Bearer synthetic-pat"},
		{"custom-header", "Context7-API-Key", "synthetic-api-key"},
	} {
		t.Run(auth.name, func(t *testing.T) {
			server := fixtureServer("catalog-fixture", "1")
			server.AddTool(fixtureTool("fixture_read"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				t.Error("discovery test executed a tool")
				return fixtureText("unexpected"), nil
			})
			handler := fixtureHTTPHandler(server)
			var mu sync.Mutex
			methods := map[string]int{}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if auth.header != "" && r.Header.Get(auth.header) != auth.value {
					t.Error("authentication header contract mismatch")
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if auth.header == "" && r.Header.Get("Authorization") != "" {
					t.Error("no-auth configuration sent a credential")
				}
				body, _ := io.ReadAll(r.Body)
				var rpc struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(body, &rpc)
				if rpc.Method != "server/discover" && rpc.Method != "tools/list" {
					t.Errorf("unexpected RPC method: %s", rpc.Method)
					http.Error(w, "discovery only", http.StatusBadRequest)
					return
				}
				mu.Lock()
				methods[rpc.Method]++
				mu.Unlock()
				r.Body = io.NopCloser(bytes.NewReader(body))
				handler.ServeHTTP(w, r)
			}))
			defer remote.Close()
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			headers := map[string]string{}
			if auth.header != "" {
				headers[auth.header] = auth.value
			}
			if err := manager.SaveMCP("fixture", MCPServerConfig{URL: remote.URL, Headers: headers}, false); err != nil {
				t.Fatal(err)
			}
			result := manager.InspectMCP(context.Background(), "fixture")
			if len(result.Diagnostics) != 0 || result.ToolCount != 1 || result.AuthRequired {
				t.Fatalf("discovery result: %+v", result)
			}
			mu.Lock()
			defer mu.Unlock()
			if methods["server/discover"] != 1 || methods["tools/list"] != 1 || len(methods) != 2 {
				t.Fatalf("unexpected discovery method counts: %v", methods)
			}
		})
	}
}
