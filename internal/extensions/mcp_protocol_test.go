package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// legacyMCPHandler is a hand-written JSON-RPC server for a pre-2026 MCP spec: it does not implement
// server/discover, answers initialize with initVersion, and lists one tool.
func legacyMCPHandler(t *testing.T, discoverMode, initVersion string, counts map[string]*atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "no stream", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("invalid request: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		if c := counts[request.Method]; c != nil {
			c.Add(1)
		}
		if request.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "server/discover":
			if discoverMode == "http_not_found" {
				http.Error(w, "old endpoint", http.StatusNotFound)
				return
			}
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": initVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "legacy", "version": "1"}}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "legacy_tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		_ = json.NewEncoder(w).Encode(response)
	})
}

// A server on an older supported spec (no server/discover) must connect through the SDK's native initialize
// fallback and list its tools.
func TestMCPLegacyInitializeFallbackConnects(t *testing.T) {
	for _, discoverMode := range []string{"method_not_found", "http_not_found"} {
		for _, version := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {
			t.Run(discoverMode+"/"+version, func(t *testing.T) {
				counts := map[string]*atomic.Int32{"server/discover": {}, "initialize": {}, "notifications/initialized": {}}
				remote := httptest.NewServer(legacyMCPHandler(t, discoverMode, version, counts))
				defer remote.Close()
				manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
				if err := manager.SaveMCP("legacy", MCPServerConfig{URL: remote.URL}, false); err != nil {
					t.Fatal(err)
				}
				prepared, err := manager.Prepare(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.Close()
				if len(prepared.Diagnostics) != 0 || len(prepared.Tools) != 1 {
					t.Fatalf("prepared diagnostics=%+v tools=%d", prepared.Diagnostics, len(prepared.Tools))
				}
				if counts["server/discover"].Load() == 0 || counts["initialize"].Load() == 0 || counts["notifications/initialized"].Load() == 0 {
					t.Fatalf("discover=%d initialize=%d initialized=%d", counts["server/discover"].Load(), counts["initialize"].Load(), counts["notifications/initialized"].Load())
				}
				inspection := manager.InspectMCP(context.Background(), "legacy")
				if inspection.AuthRequired || len(inspection.Diagnostics) != 0 {
					t.Fatalf("inspection=%+v", inspection)
				}
			})
		}
	}
}

// A server that only offers versions outside the SDK's supported set still fails with the protocol diagnostic.
func TestMCPUnsupportedProtocolStillDiagnosed(t *testing.T) {
	for _, mode := range []string{"bogus_initialize", "discover_unsupported_error"} {
		t.Run(mode, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.ID == nil {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
				switch {
				case mode == "discover_unsupported_error":
					response["error"] = map[string]any{"code": mcp.CodeUnsupportedProtocolVersion, "message": "unsupported", "data": map[string]any{"supported": []string{"1999-01-01"}}}
				case request.Method == "server/discover":
					response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
				default:
					response["result"] = map[string]any{"protocolVersion": "1999-01-01", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "bogus", "version": "1"}}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer remote.Close()
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			if err := manager.SaveMCP("bogus", MCPServerConfig{URL: remote.URL}, false); err != nil {
				t.Fatal(err)
			}
			inspection := manager.InspectMCP(context.Background(), "bogus")
			if inspection.AuthRequired || len(inspection.Diagnostics) != 1 || inspection.Diagnostics[0].Code != "unsupported_protocol" {
				t.Fatalf("inspection=%+v", inspection)
			}
		})
	}
}

// A 2026-07-28 server keeps using server/discover and never sees the legacy handshake.
func TestMCPCurrentServerUsesDiscoverOnly(t *testing.T) {
	server := fixtureServer("latest", "1")
	server.AddTool(fixtureTool("echo"), fixtureNoop)
	handler := fixtureHTTPHandler(server)
	var discovers, initializes atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var request struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(body, &request)
			r.Body = io.NopCloser(bytes.NewReader(body))
			switch request.Method {
			case "server/discover":
				discovers.Add(1)
			case "initialize":
				initializes.Add(1)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("latest", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Diagnostics) != 0 || len(prepared.Tools) != 1 || discovers.Load() == 0 || initializes.Load() != 0 {
		t.Fatalf("diagnostics=%+v tools=%d discover=%d initialize=%d", prepared.Diagnostics, len(prepared.Tools), discovers.Load(), initializes.Load())
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
