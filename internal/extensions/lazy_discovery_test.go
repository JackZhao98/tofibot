package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestLazyDiscoveryTargetsCachesRetriesAndCloses(t *testing.T) {
	var unavailable atomic.Bool
	unavailable.Store(true)
	var aRequests, bRequests atomic.Int32
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { aRequests.Add(1); http.Error(w, "unavailable", 503) }))
	defer a.Close()
	backend := fixtureServer("b", "1")
	backend.AddTool(fixtureTool("echo", fixtureDescription("echo text")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("ok"), nil
	})
	handler := fixtureHTTPHandler(backend)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bRequests.Add(1)
		if unavailable.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer b.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	for name, endpoint := range map[string]string{"a": a.URL, "b": b.URL} {
		if err := manager.SaveMCP(name, MCPServerConfig{URL: endpoint}, false); err != nil {
			t.Fatal(err)
		}
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if aRequests.Load() != 0 || bRequests.Load() != 0 {
		t.Fatal("startup touched remote MCP")
	}
	search := discoveryTool(t, p, "search_mcp_tools")
	raw := json.RawMessage(`{"server":"b","query":"echo"}`)
	out, err := search.Execute(context.Background(), raw)
	if err != nil || !strings.Contains(out, "diagnostics") {
		t.Fatalf("failure not visible: %s %v", out, err)
	}
	unavailable.Store(false)
	out, err = search.Execute(context.Background(), raw)
	if err != nil || !strings.Contains(out, "mcp_b__echo") {
		t.Fatalf("failed server did not retry: %s %v", out, err)
	}
	calls := bRequests.Load()
	if _, err = search.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if bRequests.Load() != calls || aRequests.Load() != 0 {
		t.Fatal("targeted cached search contacted a server")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = search.Execute(context.Background(), raw); err == nil {
		t.Fatal("search allowed after Close")
	}
	if _, err = discoveryTool(t, p, "list_mcp_servers").Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("list allowed after Close")
	}
	if _, err = discoveryTool(t, p, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_b__echo","arguments":{}}`)); err == nil {
		t.Fatal("call allowed after Close")
	}
}

func TestCatalogReusesMetadataAcrossRunsButReconnects(t *testing.T) {
	backend := fixtureServer("catalog-cache", "1")
	backend.AddTool(fixtureTool("read_notes", fixtureDescription("Read notes")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("ok"), nil
	})
	handler := fixtureHTTPHandler(backend)
	var initializes, listings atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"server/discover"`)) {
			initializes.Add(1)
		}
		if bytes.Contains(body, []byte(`"method":"tools/list"`)) {
			listings.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("notes", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	searchRun := func() {
		p, err := manager.PrepareDiscoverableForBot(context.Background(), "any")
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		out, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"notes","query":"notes"}`))
		if err != nil || !strings.Contains(out, "mcp_notes__read_notes") {
			t.Fatalf("search: %s %v", out, err)
		}
	}
	searchRun()
	searchRun()
	if listings.Load() != 1 || initializes.Load() != 2 {
		t.Fatalf("list=%d initialize=%d", listings.Load(), initializes.Load())
	}
	manager.catalog.InvalidateServer("notes")
	searchRun()
	if listings.Load() != 2 || initializes.Load() != 3 {
		t.Fatalf("invalidation list=%d initialize=%d", listings.Load(), initializes.Load())
	}
}

func TestLazyDiscoveryCloseCancelsInitialization(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer remote.Close()
	defer close(release)
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), DiscoveryTimeout: time.Minute})
	if err := manager.SaveMCP("slow", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"query":"echo"}`))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("search did not start")
	}
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited for remote timeout")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled search succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("search did not stop")
	}
}

func TestLazyDiscoveryBrowseAfterKeywordMissRemainsBoundedAndScoped(t *testing.T) {
	var requests, otherRequests atomic.Int32
	backend := fixtureServer("synthetic-catalog", "1")
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("synthetic result"), nil
	}
	// The first schema cannot fit. Browsing must report that omission and still
	// advance to later pages without authorizing the omitted tool.
	backend.AddTool(fixtureTool("entry_00", fixtureString("value", fixtureParameterDescription(strings.Repeat("x", maxDiscoveryBytes*2)))), handler)
	for i := 1; i <= 12; i++ {
		backend.AddTool(fixtureTool(fmt.Sprintf("entry_%02d", i), fixtureDescription("Read synthetic inventory records")), handler)
	}
	remoteHandler := fixtureHTTPHandler(backend)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); remoteHandler.ServeHTTP(w, r) }))
	defer remote.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { otherRequests.Add(1); http.Error(w, "unrelated", 503) }))
	defer other.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	for name, url := range map[string]string{"catalog": remote.URL, "unrelated": other.URL} {
		if err := manager.SaveMCP(name, MCPServerConfig{URL: url}, false); err != nil {
			t.Fatal(err)
		}
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "synthetic-bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	listing, err := discoveryTool(t, p, "list_mcp_servers").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(listing, "no tool schemas have been inspected") {
		t.Fatalf("directory guidance: %v", err)
	}
	if requests.Load() != 0 || otherRequests.Load() != 0 {
		t.Fatal("directory initiated remote discovery")
	}
	search := discoveryTool(t, p, "search_mcp_tools")
	call := discoveryTool(t, p, "call_mcp_tool")
	miss, err := search.Execute(context.Background(), json.RawMessage(`{"server":"catalog","query":"不存在的关键词"}`))
	if err != nil || !strings.Contains(miss, `"tools":[]`) || !strings.Contains(miss, "query '*'") {
		t.Fatalf("keyword miss lacks browse path: %s %v", miss, err)
	}
	requestsAfterDiscovery := requests.Load()
	// Exact-name resolution reuses this run's discovery: a name the server
	// does not expose fails without another remote request.
	if _, err = call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_catalog__entry_99","arguments":{}}`)); err == nil || !strings.Contains(err.Error(), "search_mcp_tools") {
		t.Fatalf("absent tool resolved: %v", err)
	}
	offset := 0
	found := map[string]bool{}
	omitted := 0
	for page := 0; page < 10; page++ {
		raw, _ := json.Marshal(map[string]any{"server": "catalog", "query": "*", "tool_offset": offset, "limit": 3})
		out, err := search.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > maxDiscoveryBytes {
			t.Fatal("unbounded discovery response")
		}
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Next    *int `json:"next_tool_offset"`
			Omitted int  `json:"omitted_schema_count"`
		}
		if err = json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Tools) > 3 {
			t.Fatal("page limit exceeded")
		}
		omitted += result.Omitted
		for _, tool := range result.Tools {
			if found[tool.Name] {
				t.Fatal("duplicate across pages")
			}
			found[tool.Name] = true
		}
		if result.Next == nil {
			break
		}
		if *result.Next <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = *result.Next
	}
	if len(found) != 12 || omitted != 1 {
		t.Fatalf("schemas=%d omissions=%d", len(found), omitted)
	}
	if requests.Load() != requestsAfterDiscovery || otherRequests.Load() != 0 {
		t.Fatal("cached pagination contacted remote or unrelated server")
	}
	// An oversized schema omitted from search pages stays callable by its
	// exact name; the server's own schema still validates the arguments.
	if _, err = call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_catalog__entry_00","arguments":{"value":1}}`)); err == nil || !strings.Contains(err.Error(), "Current input schema") {
		t.Fatalf("omitted schema did not validate arguments: %v", err)
	}
	if _, err = call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_catalog__entry_12","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"query":"*"}`, `{"query":"*","server_offset":1}`, `{"query":"records","tool_offset":-1}`, `{"query":"*","server":"catalog","tool_offset":-1}`, `{"query":"*","server":"catalog","tool_offset":99}`} {
		if _, err = search.Execute(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid browse accepted: %s", raw)
		}
	}
}

func TestLazyDiscoverySchemaBudgetDefersRatherThanDrops(t *testing.T) {
	backend := fixtureServer("budget", "1")
	for _, name := range []string{"first", "second"} {
		backend.AddTool(fixtureTool(name, fixtureString("value", fixtureParameterDescription(strings.Repeat("m", 17<<10)))), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("synthetic"), nil
		})
	}
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("budget", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	search := discoveryTool(t, p, "search_mcp_tools")
	for offset := 0; offset < 2; offset++ {
		raw, _ := json.Marshal(map[string]any{"server": "budget", "query": "*", "limit": 10, "tool_offset": offset})
		output, err := search.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Next    *int `json:"next_tool_offset"`
			Omitted int  `json:"omitted_schema_count"`
		}
		if err = json.Unmarshal([]byte(output), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Tools) != 1 || page.Omitted != 0 || len(output) > maxDiscoveryBytes {
			t.Fatalf("bad bounded page: offset=%d tools=%d omitted=%d", offset, len(page.Tools), page.Omitted)
		}
		if offset == 0 && (page.Next == nil || *page.Next != 1 || page.Tools[0].Name != "mcp_budget__first") {
			t.Fatal("schema that fits alone was skipped")
		}
		if offset == 1 && (page.Next != nil || page.Tools[0].Name != "mcp_budget__second") {
			t.Fatal("deferred schema missing from next page")
		}
	}
}

func TestLazyDiscoveryCrossServerToolPages(t *testing.T) {
	for _, tc := range []struct {
		name, description string
		limit             int
	}{
		{name: "count", description: "inventory", limit: 3},
		{name: "bytes", description: strings.Repeat("x", 17<<10), limit: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			backend := fixtureServer("page-fixture", "1")
			for _, name := range []string{"inventory_a", "inventory_b"} {
				backend.AddTool(fixtureTool(name, fixtureString("value", fixtureParameterDescription(tc.description))), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return fixtureText("fixture"), nil
				})
			}
			handler := fixtureHTTPHandler(backend)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				handler.ServeHTTP(w, r)
			}))
			defer remote.Close()
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			for i := 0; i < 9; i++ {
				if err := manager.SaveMCP(fmt.Sprintf("catalog_%02d", i), MCPServerConfig{URL: remote.URL}, false); err != nil {
					t.Fatal(err)
				}
			}
			p, err := manager.PrepareDiscoverableForBot(context.Background(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			search, call := discoveryTool(t, p, "search_mcp_tools"), discoveryTool(t, p, "call_mcp_tool")
			seen := map[string]bool{}
			serverOffset, toolOffset := 0, 0
			var pageRequests int32
			for page := 0; page < 24; page++ {
				raw, _ := json.Marshal(map[string]any{"query": "inventory", "server_offset": serverOffset, "tool_offset": toolOffset, "limit": tc.limit})
				output, err := search.Execute(context.Background(), raw)
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
					NextTool   *int `json:"next_tool_offset"`
					NextServer *int `json:"next_server_offset"`
					Omitted    int  `json:"omitted_schema_count"`
				}
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					t.Fatal(err)
				}
				if len(output) > maxDiscoveryBytes || len(result.Tools) == 0 || len(result.Tools) > tc.limit || result.Omitted != 0 {
					t.Fatalf("bad page: tools=%d omitted=%d bytes=%d", len(result.Tools), result.Omitted, len(output))
				}
				if toolOffset == 0 {
					pageRequests = requests.Load()
				} else if requests.Load() != pageRequests {
					t.Fatal("tool continuation repeated remote discovery")
				}
				for _, tool := range result.Tools {
					if seen[tool.Name] {
						t.Fatalf("duplicate tool: %s", tool.Name)
					}
					seen[tool.Name] = true
				}
				if page == 0 {
					if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_catalog_07__inventory_z","arguments":{}}`)); err == nil {
						t.Fatal("absent tool became callable")
					}
				}
				if result.NextTool != nil {
					if *result.NextTool <= toolOffset {
						t.Fatal("tool continuation did not advance")
					}
					toolOffset = *result.NextTool
					continue
				}
				if result.NextServer == nil {
					break
				}
				if *result.NextServer != 8 || len(seen) != 16 {
					t.Fatalf("advanced servers before finishing tool pages: next=%d seen=%d", *result.NextServer, len(seen))
				}
				serverOffset, toolOffset = *result.NextServer, 0
			}
			if len(seen) != 18 {
				t.Fatalf("lost schemas across server/tool pages: got %d, want 18", len(seen))
			}
			if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_catalog_07__inventory_b","arguments":{}}`)); err != nil {
				t.Fatalf("continued schema not callable: %v", err)
			}
		})
	}
}

func TestLazyDiscoveryContinuationStableAcrossServerRecovery(t *testing.T) {
	backend := fixtureServer("recovery-fixture", "1")
	for _, name := range []string{"inventory_a", "inventory_b"} {
		backend.AddTool(fixtureTool(name), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("fixture"), nil
		})
	}
	handler := fixtureHTTPHandler(backend)
	var recovered atomic.Bool
	var failedRequests atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedRequests.Add(1)
		if !recovered.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer failing.Close()
	working := httptest.NewServer(handler)
	defer working.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	for name, endpoint := range map[string]string{"a": failing.URL, "b": working.URL} {
		if err := manager.SaveMCP(name, MCPServerConfig{URL: endpoint}, false); err != nil {
			t.Fatal(err)
		}
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	search := discoveryTool(t, p, "search_mcp_tools")
	first, err := search.Execute(context.Background(), json.RawMessage(`{"query":"inventory","limit":1}`))
	if err != nil || !strings.Contains(first, `"next_tool_offset":1`) || !strings.Contains(first, "mcp_b__inventory_a") {
		t.Fatalf("first page: %s %v", first, err)
	}
	recovered.Store(true)
	// Even explicitly discovering the recovered server must not change the
	// ranking of a cross-server page already being consumed.
	if _, err := search.Execute(context.Background(), json.RawMessage(`{"server":"a","query":"inventory"}`)); err != nil {
		t.Fatal(err)
	}
	requests := failedRequests.Load()
	second, err := search.Execute(context.Background(), json.RawMessage(`{"query":"inventory","tool_offset":1,"limit":1}`))
	if err != nil || !strings.Contains(second, "mcp_b__inventory_b") || strings.Contains(second, `"next_tool_offset":`) || !strings.Contains(second, `"diagnostics":`) {
		t.Fatalf("continuation shifted after recovery: %s %v", second, err)
	}
	if failedRequests.Load() != requests {
		t.Fatal("continuation retried failed server")
	}
	if _, err := search.Execute(context.Background(), json.RawMessage(`{"query":"unstarted","tool_offset":1}`)); err == nil {
		t.Fatal("continuation without initial search accepted")
	}
}

func TestLazyDiscoverySourceFailureLeavesAlternateCallable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  *mcp.CallToolResult
		wantErr bool
	}{
		{name: "tool_error", result: fixtureError("fixture source unavailable"), wantErr: true},
		{name: "empty_source", result: fixtureText("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var alternateCalls atomic.Int32
			backend := fixtureServer("fallback-fixture", "1")
			backend.AddTool(fixtureTool("primary", fixtureDescription("Read inventory records")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return tc.result, nil
			})
			backend.AddTool(fixtureTool("alternate", fixtureDescription("Inspect records in the available browser")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				alternateCalls.Add(1)
				return fixtureText("fixture inventory record"), nil
			})
			remote := httptest.NewServer(fixtureHTTPHandler(backend))
			defer remote.Close()
			manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			if err := manager.SaveMCP("fixture", MCPServerConfig{URL: remote.URL}, false); err != nil {
				t.Fatal(err)
			}
			p, err := manager.PrepareDiscoverableForBot(context.Background(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			search, call := discoveryTool(t, p, "search_mcp_tools"), discoveryTool(t, p, "call_mcp_tool")
			if _, err := search.Execute(context.Background(), json.RawMessage(`{"query":"primary"}`)); err != nil {
				t.Fatal(err)
			}
			output, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__primary","arguments":{}}`))
			if (err != nil) != tc.wantErr || (!tc.wantErr && output != "MCP returned no text content") {
				t.Fatalf("source evidence changed: %s %v", output, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "Untrusted tool-reported details: fixture source unavailable") {
				t.Fatalf("model-visible failure lost the bounded source reason: %v", err)
			}
			// A source failure never falls back on its own; the alternate runs
			// only when explicitly called by its exact name.
			if alternateCalls.Load() != 0 {
				t.Fatal("source failure invoked the alternate")
			}
			if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__alternate","arguments":{}}`)); err != nil || alternateCalls.Load() != 1 {
				t.Fatalf("explicit exact-name alternate failed: %v", err)
			}
			if _, err := search.Execute(context.Background(), json.RawMessage(`{"query":"browser"}`)); err != nil {
				t.Fatal(err)
			}
			output, err = call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__alternate","arguments":{}}`))
			if err != nil || output != "fixture inventory record" || alternateCalls.Load() != 2 {
				t.Fatalf("alternate unavailable after source failure: %s %v", output, err)
			}
		})
	}
}
