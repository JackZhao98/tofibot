package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRequiresLatestWithoutSendingLegacyHandshake(t *testing.T) {
	for _, mode := range []string{"method_not_found", "old_versions", "http_not_found", "old_sdk_server"} {
		t.Run(mode, func(t *testing.T) {
			var discovers, initializes atomic.Int32
			var handler http.Handler
			if mode == "old_sdk_server" {
				server := mcp.NewServer(&mcp.Implementation{Name: "old", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{"2025-11-25"}})
				handler = fixtureHTTPHandler(server)
			}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Meta map[string]any `json:"_meta"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("invalid request: %v", err)
					http.Error(w, "bad", 400)
					return
				}
				if request.Method == "initialize" {
					initializes.Add(1)
				}
				if request.Method != "server/discover" {
					t.Errorf("unexpected method %s", request.Method)
					http.Error(w, "legacy", 500)
					return
				}
				discovers.Add(1)
				if request.Params.Meta[mcp.MetaKeyProtocolVersion] != requiredMCPProtocolVersion {
					t.Errorf("protocol meta=%v", request.Params.Meta)
				}
				if mode == "old_sdk_server" {
					body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "method": request.Method, "params": map[string]any{"_meta": request.Params.Meta}})
					r.Body = io.NopCloser(bytes.NewReader(body))
					handler.ServeHTTP(w, r)
					return
				}
				if mode == "http_not_found" {
					http.Error(w, "old endpoint", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
				if mode == "method_not_found" {
					response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
				} else {
					response["result"] = map[string]any{"supportedVersions": []string{"2025-11-25"}, "capabilities": map[string]any{}}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer remote.Close()
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			if err := manager.SaveMCP("old", MCPServerConfig{URL: remote.URL}, false); err != nil {
				t.Fatal(err)
			}
			inspection := manager.InspectMCP(context.Background(), "old")
			if inspection.AuthRequired || len(inspection.Diagnostics) != 1 || inspection.Diagnostics[0].Code != "unsupported_protocol" || !strings.Contains(inspection.Diagnostics[0].Message, requiredMCPProtocolVersion) {
				t.Fatalf("inspection=%+v", inspection)
			}
			if discovers.Load() != 1 || initializes.Load() != 0 {
				t.Fatalf("discover=%d initialize=%d", discovers.Load(), initializes.Load())
			}
		})
	}
}

func TestMCPCurrentRequestsAndHeadersIgnoreOldConfiguredVersion(t *testing.T) {
	server := fixtureServer("latest", "1")
	server.AddTool(fixtureTool("echo"), func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if request.ProtocolVersion() != requiredMCPProtocolVersion {
			t.Errorf("call protocol=%s", request.ProtocolVersion())
		}
		return fixtureText("latest"), nil
	})
	handler := fixtureHTTPHandler(server)
	var discovers, lists, calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("MCP-Protocol-Version") == "2025-11-25" {
			t.Error("old config header overrode protocol")
		}
		if r.Header.Get("X-Fixture") != "retained" {
			t.Error("configured custom header lost")
		}
		if r.Method == http.MethodPost {
			var request struct {
				Method string `json:"method"`
				Params struct {
					Meta map[string]any `json:"_meta"`
				} `json:"params"`
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			switch request.Method {
			case "server/discover":
				discovers.Add(1)
			case "tools/list":
				lists.Add(1)
			case "tools/call":
				calls.Add(1)
			default:
				t.Errorf("unexpected method=%s", request.Method)
			}
			if request.Params.Meta[mcp.MetaKeyProtocolVersion] != requiredMCPProtocolVersion {
				t.Errorf("%s lacks current protocol meta: %v", request.Method, request.Params.Meta)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("latest", MCPServerConfig{URL: remote.URL, Headers: map[string]string{"MCP-Protocol-Version": "2025-11-25", "X-Fixture": "retained"}}, false); err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Diagnostics) != 0 || len(prepared.Tools) != 1 {
		t.Fatalf("prepared=%+v", prepared)
	}
	text, err := prepared.Tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || text != "latest" {
		t.Fatalf("call=%s err=%v", text, err)
	}
	if discovers.Load() != 1 || lists.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("discover=%d list=%d call=%d", discovers.Load(), lists.Load(), calls.Load())
	}
}

func TestMCPCurrentSubscriptionInvalidatesCatalogs(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "changing", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{requiredMCPProtocolVersion}, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	server.AddTool(fixtureTool("before"), fixtureNoop)
	remote := fixtureHTTPServer(server)
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	cfg := MCPServerConfig{URL: remote.URL}
	if err := manager.SaveMCP("changing", cfg, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := manager.openMCPClient(context.Background(), ctx, "changing", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools := []MCPCatalogTool{{RemoteName: "before", InputSchema: map[string]any{"type": "object"}}}
	version := manager.schemaVersion("changing", cfg)
	if err := manager.catalog.Put("changing", version, "catalog-v1", tools); err != nil {
		t.Fatal(err)
	}
	if err := manager.metadata.Put("changing", version, "metadata-v1", tools); err != nil {
		t.Fatal(err)
	}
	if err := manager.metadataDisk.Put("changing", metadataFingerprint("changing", cfg), []MCPMetadataTool{{RemoteName: "before"}}); err != nil {
		t.Fatal(err)
	}
	server.AddTool(fixtureTool("after"), fixtureNoop)
	timer := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	for {
		_, hot := manager.catalog.Get("changing", version, "catalog-v1")
		_, metadata := manager.metadata.Get("changing", version, "metadata-v1")
		_, disk := manager.metadataDisk.Get("changing", metadataFingerprint("changing", cfg))
		if !hot && !metadata && !disk {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("current subscription did not invalidate catalogs: hot=%v metadata=%v disk=%v", hot, metadata, disk)
		case <-timer.C:
		}
	}
}
