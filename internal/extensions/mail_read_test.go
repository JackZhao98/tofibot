package extensions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JackZhao98/tofibot/internal/mailread"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMailReadMetadataOnlyAcceptedFromConfiguredPrivateBoundary(t *testing.T) {
	raw, err := os.ReadFile("../mailread/testdata/gog-v0400-search.json")
	if err != nil {
		t.Fatal(err)
	}
	var root any
	json.Unmarshal(raw, &root)
	identity := mailread.Identity{Version: 1, Provider: "gmail", Connection: "synthetic-gog", Mailbox: "reader@example.test"}
	for _, tc := range []struct {
		name, dispatch                                 string
		trusted, isError, unsupported, wrongConnection bool
	}{
		{name: "trusted-eager", dispatch: "eager", trusted: true},
		{name: "trusted-cached", dispatch: "cached", trusted: true},
		{name: "trusted-lazy", dispatch: "lazy", trusted: true},
		{name: "remote", dispatch: "eager"},
		{name: "reported-error", dispatch: "eager", trusted: true, isError: true},
		{name: "unsupported-schema", dispatch: "eager", trusted: true, unsupported: true},
		{name: "foreign-connection", dispatch: "eager", trusted: true, wrongConnection: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := fixtureServer("synthetic", "1")
			backend.AddTool(fixtureTool("gmail_search"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				content := root
				if tc.unsupported {
					content = map[string]any{"body": "Unstructured synthetic mail is not trusted"}
				}
				stamp := identity
				if tc.wrongConnection {
					stamp.Connection = "other-synthetic-gog"
				}
				return &mcp.CallToolResult{StructuredContent: content, IsError: tc.isError, Meta: mcp.Meta{mailread.MetaKey: stamp}}, nil
			})
			configuredEndpoint := "http://mail.fixture.test/mcp"
			handler := fixtureHTTPHandler(backend)
			transport := oauthDestinationTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != configuredEndpoint {
					t.Fatal("synthetic transport escaped endpoint")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w.Result(), nil
			})
			boundaryCalls := 0
			m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(url string) (http.RoundTripper, error) {
				if url != configuredEndpoint {
					t.Fatal("unconfigured endpoint")
				}
				return transport, nil
			}, TrustedMailEndpoint: func(endpoint, connection string) bool {
				boundaryCalls++
				if endpoint != configuredEndpoint {
					t.Fatal("observer lost configured dispatch endpoint", endpoint)
				}
				return tc.trusted && connection == identity.Connection
			}})
			defer m.CloseIdleConnections()
			cfg := MCPServerConfig{URL: configuredEndpoint}
			if err := m.SaveMCP("fixture", cfg, false); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			count := 0
			ctx = mailread.WithRecorder(ctx, func(_ context.Context, snapshot mailread.Snapshot, result string) (string, error) {
				count++
				if snapshot.Mailbox != identity.Mailbox || snapshot.Messages[0].From != "Sender <sender@example.test>" || snapshot.Messages[0].Subject != "Same synthetic subject" {
					t.Fatal(snapshot)
				}
				return result + "\nSynthetic source descriptor", nil
			})
			var out string
			var err error
			if tc.dispatch == "eager" {
				prepared, e := m.Prepare(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer prepared.Close()
				if len(prepared.Tools) != 1 || prepared.Tools[0].CheckReadiness == nil {
					t.Fatal("eager dispatch lost readiness")
				}
				out, err = prepared.Tools[0].Execute(ctx, json.RawMessage(`{}`))
			} else {
				cached := []CachedMCPTool(nil)
				if tc.dispatch == "cached" {
					cached = []CachedMCPTool{{Name: "mcp_fixture__gmail_search", Server: "fixture", RemoteName: "gmail_search", Parameters: map[string]any{"type": "object"}, SchemaVersion: m.schemaVersion("fixture", cfg)}}
				}
				approvals := 0
				prepared, e := m.PrepareDiscoverableForBotWithCallGate(ctx, "synthetic-bot", cached, func(context.Context, MCPCallApproval) error { approvals++; return nil })
				if e != nil {
					t.Fatal(e)
				}
				defer prepared.Close()
				if tc.dispatch == "lazy" {
					if _, e := discoveryTool(t, prepared, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"fixture","query":"gmail_search"}`)); e != nil {
						t.Fatal(e)
					}
				}
				out, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(ctx, json.RawMessage(`{"name":"mcp_fixture__gmail_search","arguments":{}}`))
				if approvals != 1 {
					t.Fatal("mail dispatch lost backend approval gate", approvals)
				}
			}
			if (err != nil) != tc.isError {
				t.Fatal("reported MCP error changed", err)
			}
			want := 0
			if tc.trusted && !tc.isError && !tc.unsupported && !tc.wrongConnection {
				want = 1
			}
			if count != want || want == 1 && len(out) == 0 {
				t.Fatal("untrusted/failed/unsupported output established mail provenance", count, want)
			}
			if tc.isError && boundaryCalls != 0 {
				t.Fatal("error result reached provenance observer")
			}
		})
	}
}
