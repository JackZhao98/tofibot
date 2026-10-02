package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type searchPageResult struct {
	Tools []struct {
		Name   string `json:"name"`
		Server string `json:"server"`
	} `json:"tools"`
	SearchMethod string `json:"search_method"`
	NextTool     *int   `json:"next_tool_offset"`
	NextServer   *int   `json:"next_server_offset"`
}

func decodeSearchPage(t *testing.T, raw string) searchPageResult {
	t.Helper()
	var page searchPageResult
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode search page: %v", err)
	}
	return page
}

func TestLazyDiscoveryAIExpansionBridgesLexicalMiss(t *testing.T) {
	backend := fixtureServer("semantic", "1")
	backend.AddTool(fixtureTool("weather_lookup", fixtureDescription("Get current weather conditions by city")), fixtureNoop)
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	defer remote.Close()

	var expansions atomic.Int32
	manager := NewManager(Config{
		MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"),
		ExpandToolQuery: func(_ context.Context, query string) ([]string, error) {
			expansions.Add(1)
			if query != "今天旧金山天气怎么样" {
				t.Errorf("expander query=%q", query)
			}
			return []string{"current weather conditions"}, nil
		},
	})
	if err := manager.SaveMCP("semantic", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	out, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"semantic","query":"今天旧金山天气怎么样"}`))
	if err != nil {
		t.Fatal(err)
	}
	page := decodeSearchPage(t, out)
	if page.SearchMethod != "ai_expanded" || len(page.Tools) != 1 || page.Tools[0].Name != "mcp_semantic__weather_lookup" {
		t.Fatalf("expanded result=%+v output=%s", page, out)
	}
	if expansions.Load() != 1 {
		t.Fatalf("expander calls=%d, want 1", expansions.Load())
	}
}

func TestLazyDiscoveryAIExpansionSkipsExactLexicalHitAndDegradesOnError(t *testing.T) {
	backend := fixtureServer("lexical", "1")
	backend.AddTool(fixtureTool("weather_lookup", fixtureDescription("Get current weather conditions by city")), fixtureNoop)
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	defer remote.Close()

	var expansions atomic.Int32
	manager := NewManager(Config{
		MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"),
		ExpandToolQuery: func(_ context.Context, _ string) ([]string, error) {
			if expansions.Add(1) == 1 {
				return nil, errors.New("temporary AI failure")
			}
			return []string{"current weather conditions"}, nil
		},
	})
	if err := manager.SaveMCP("lexical", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	search := discoveryTool(t, p, "search_mcp_tools")

	lexical, err := search.Execute(context.Background(), json.RawMessage(`{"server":"lexical","query":"weather"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSearchPage(t, lexical); got.SearchMethod != "lexical" || len(got.Tools) != 1 {
		t.Fatalf("lexical hit=%s", lexical)
	}
	if expansions.Load() != 0 {
		t.Fatalf("lexical hit invoked expander %d times", expansions.Load())
	}

	miss, err := search.Execute(context.Background(), json.RawMessage(`{"server":"lexical","query":"clima de hoy"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSearchPage(t, miss); got.SearchMethod != "lexical" || len(got.Tools) != 0 {
		t.Fatalf("AI error did not degrade to lexical empty result: %s", miss)
	}
	if expansions.Load() != 1 {
		t.Fatalf("failed expansion calls=%d, want 1", expansions.Load())
	}
}

func TestLazyDiscoveryExpansionRespectsServerPaginationAndPermissions(t *testing.T) {
	backend := fixtureServer("pages", "1")
	backend.AddTool(fixtureTool("forecast", fixtureDescription("Get forecast weather conditions")), fixtureNoop)
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	defer remote.Close()
	var expansions atomic.Int32
	manager := NewManager(Config{
		MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"),
		ExpandToolQuery: func(context.Context, string) ([]string, error) {
			expansions.Add(1)
			return []string{"forecast weather conditions"}, nil
		},
	})
	for i := 0; i < 8; i++ {
		name := "server_" + string(rune('a'+i))
		if err := manager.SaveMCP(name, MCPServerConfig{URL: "http://127.0.0.1:1/mcp"}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.SaveMCP("server_z", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	search := discoveryTool(t, p, "search_mcp_tools")

	first, err := search.Execute(context.Background(), json.RawMessage(`{"query":"今天天气如何","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	page := decodeSearchPage(t, first)
	if page.NextServer == nil || *page.NextServer != 8 || len(page.Tools) != 0 {
		t.Fatalf("first server page bypassed its boundary: %s", first)
	}
	if expansions.Load() != 0 {
		t.Fatalf("AI ran without discovered candidates: %d", expansions.Load())
	}
	second, err := search.Execute(context.Background(), json.RawMessage(`{"query":"今天天气如何","server_offset":8,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	page = decodeSearchPage(t, second)
	if page.SearchMethod != "ai_expanded" || len(page.Tools) != 1 || page.Tools[0].Server != "server_z" || expansions.Load() != 1 {
		t.Fatalf("second server page=%s expansions=%d", second, expansions.Load())
	}

	// The expander can only rank already-authorized candidates. A disallowed
	// tool whose metadata matches the generated phrase must remain invisible.
	if err := manager.SaveMCP("guarded", MCPServerConfig{
		URL: remote.URL, ToolAllowlist: []string{"forecast"}, ToolDenylist: []string{"forecast"},
	}, false); err != nil {
		t.Fatal(err)
	}
	guarded, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer guarded.Close()
	guardedOut, err := discoveryTool(t, guarded, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"guarded","query":"clima de hoy"}`))
	if err != nil {
		t.Fatal(err)
	}
	guardedPage := decodeSearchPage(t, guardedOut)
	if len(guardedPage.Tools) != 0 || strings.Contains(guardedOut, "forecast") {
		t.Fatalf("expansion bypassed the server allowlist: %s", guardedOut)
	}
}

func TestLazyDiscoveryExpandedServerPaginationStableAndMemoized(t *testing.T) {
	backend := fixtureServer("stable", "1")
	for _, name := range []string{"weather_a", "weather_b", "weather_c"} {
		backend.AddTool(fixtureTool(name, fixtureDescription("Current weather conditions")), fixtureNoop)
	}
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	defer remote.Close()
	var expansions atomic.Int32
	manager := NewManager(Config{
		MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"),
		ExpandToolQuery: func(context.Context, string) ([]string, error) {
			expansions.Add(1)
			return []string{"current weather conditions"}, nil
		},
	})
	if err := manager.SaveMCP("stable", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	search := discoveryTool(t, p, "search_mcp_tools")
	first, err := search.Execute(context.Background(), json.RawMessage(`{"server":"stable","query":"今天天气如何","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	firstPage := decodeSearchPage(t, first)
	if firstPage.SearchMethod != "ai_expanded" || len(firstPage.Tools) != 1 || firstPage.NextTool == nil {
		t.Fatalf("first page=%s", first)
	}
	secondRaw, _ := json.Marshal(map[string]any{"server": "stable", "query": "今天天气如何", "tool_offset": *firstPage.NextTool, "limit": 1})
	second, err := search.Execute(context.Background(), secondRaw)
	if err != nil {
		t.Fatal(err)
	}
	secondPage := decodeSearchPage(t, second)
	if secondPage.SearchMethod != "ai_expanded" || len(secondPage.Tools) != 1 || secondPage.Tools[0].Name == firstPage.Tools[0].Name {
		t.Fatalf("second page=%s first=%s", second, first)
	}
	if expansions.Load() != 1 {
		t.Fatalf("pagination re-ran expander %d times", expansions.Load())
	}
}
