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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This exercises the metadata directory separately from schema retrieval:
// finding a late server must not require putting every input schema in context.
func TestGlobalCatalogSearchFindsLateAuthorizedToolsAndPages(t *testing.T) {
	var listRequests, initializeRequests atomic.Int32
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	servers := make([]*httptest.Server, 0, 12)
	defer func() {
		for _, remote := range servers {
			remote.Close()
		}
	}()
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("service_%02d", i)
		toolName := fmt.Sprintf("inventory_%02d", i)
		backend := fixtureServer(name, "1")
		backend.AddTool(fixtureTool(toolName, fixtureDescription("Search inventory records"), fixtureString("account", fixtureParameterDescription("Account identifier"))), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("fixture"), nil
		})
		// The final server also offers a denied candidate. Its presence must not
		// leak into global name/description search results.
		if i == 11 {
			backend.AddTool(fixtureTool("inventory_denied", fixtureDescription("Search inventory records")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return fixtureText("should not be callable"), nil
			})
		}
		handler := fixtureHTTPHandler(backend)
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"method":"server/discover"`)) {
				initializeRequests.Add(1)
			}
			if bytes.Contains(body, []byte(`"method":"tools/list"`)) {
				listRequests.Add(1)
			}
			handler.ServeHTTP(w, r)
		}))
		servers = append(servers, remote)
		cfg := MCPServerConfig{URL: remote.URL}
		if i == 11 {
			cfg.BotAllowlists = map[string][]string{"test": {"inventory_11", "inventory_denied"}}
			cfg.ToolDenylist = []string{"inventory_denied"}
		}
		if err := manager.SaveMCP(name, cfg, false); err != nil {
			t.Fatal(err)
		}
	}

	p, err := manager.PrepareDiscoverableForBot(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	searchCatalog := discoveryTool(t, p, "search_mcp_catalog")
	searchSchema := discoveryTool(t, p, "search_mcp_tools")

	// The metadata index must scan beyond the old eight-server window and
	// return only bounded names/descriptions/server identities.
	first, err := searchCatalog.Execute(context.Background(), json.RawMessage(`{"query":"inventory","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var firstPage struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Server      string `json:"server"`
		} `json:"tools"`
		Indexed  int  `json:"indexed_servers"`
		Total    int  `json:"total_servers"`
		Complete bool `json:"complete"`
		Next     *int `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(first), &firstPage); err != nil {
		t.Fatalf("decode catalog page: %v (%s)", err, first)
	}
	if len(firstPage.Tools) != 5 || firstPage.Indexed != 12 || firstPage.Total != 12 || !firstPage.Complete || firstPage.Next == nil || *firstPage.Next != 5 {
		t.Fatalf("unexpected global page metadata: %+v", firstPage)
	}
	if strings.Contains(first, "input_schema") || strings.Contains(first, "parameters") || strings.Contains(first, `"schema"`) {
		t.Fatalf("catalog response included tool schemas: %s", first)
	}
	if initializeRequests.Load() != 12 || listRequests.Load() != 12 {
		t.Fatalf("catalog did not inspect every configured server: initialize=%d list=%d", initializeRequests.Load(), listRequests.Load())
	}

	allNames := map[string]bool{}
	for _, tool := range firstPage.Tools {
		allNames[tool.Name] = true
	}
	for offset := *firstPage.Next; ; {
		raw, _ := json.Marshal(map[string]any{"query": "inventory", "offset": offset, "limit": 5})
		pageJSON, err := searchCatalog.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Next *int `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(pageJSON), &page); err != nil {
			t.Fatal(err)
		}
		for _, tool := range page.Tools {
			if allNames[tool.Name] {
				t.Fatalf("duplicate catalog result across pages: %s", tool.Name)
			}
			allNames[tool.Name] = true
		}
		if page.Next == nil {
			break
		}
		offset = *page.Next
	}
	if len(allNames) != 12 || allNames["mcp_service_11__inventory_denied"] {
		t.Fatalf("global catalog lost results or leaked denied tool: %v", allNames)
	}
	if initializeRequests.Load() != 12 || listRequests.Load() != 12 {
		t.Fatal("metadata pagination repeated remote indexing")
	}

	// Once the directory identifies a candidate, the existing targeted search
	// supplies its schema on demand, and that result then becomes callable.
	schemaJSON, err := searchSchema.Execute(context.Background(), json.RawMessage(`{"server":"service_11","query":"*"}`))
	if err != nil || !strings.Contains(schemaJSON, `"input_schema"`) || !strings.Contains(schemaJSON, "mcp_service_11__inventory_11") {
		t.Fatalf("targeted schema retrieval failed: %s %v", schemaJSON, err)
	}
	call := discoveryTool(t, p, "call_mcp_tool")
	if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_service_11__inventory_denied","arguments":{}}`)); err == nil {
		t.Fatal("denied candidate became callable")
	}
}

func TestGlobalCatalogPersistsAfterRestartAndRefreshesExplicitly(t *testing.T) {
	backend := fixtureServer("persistent", "1")
	backend.AddTool(fixtureTool("read_report", fixtureDescription("Read a report")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("ok"), nil
	})
	handler := fixtureHTTPHandler(backend)
	var lists atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"tools/list"`)) {
			lists.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	first := NewManager(Config{MCPConfigPath: path})
	if err := first.SaveMCP("reports", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	search := func(manager *Manager, raw string) string {
		p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		out, err := discoveryTool(t, p, "search_mcp_catalog").Execute(context.Background(), json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := search(first, `{"query":"report"}`); !strings.Contains(out, `"complete":true`) || !strings.Contains(out, "read_report") {
		t.Fatal(out)
	}
	if lists.Load() != 1 {
		t.Fatalf("first list=%d", lists.Load())
	}
	second := NewManager(Config{MCPConfigPath: path})
	if out := search(second, `{"query":"report"}`); !strings.Contains(out, `"complete":true`) || !strings.Contains(out, "read_report") {
		t.Fatal(out)
	}
	if lists.Load() != 1 {
		t.Fatalf("restart relisted tools: %d", lists.Load())
	}
	if out := search(second, `{"query":"report","refresh":true}`); !strings.Contains(out, `"complete":true`) {
		t.Fatal(out)
	}
	if lists.Load() != 2 {
		t.Fatalf("explicit refresh list=%d", lists.Load())
	}
	if err := second.SaveMCP("reports", MCPServerConfig{URL: remote.URL, ToolDenylist: []string{"read_report"}}, true); err != nil {
		t.Fatal(err)
	}
	third := NewManager(Config{MCPConfigPath: path})
	if out := search(third, `{"query":"report"}`); !strings.Contains(out, `"complete":true`) || strings.Contains(out, "read_report") {
		t.Fatal(out)
	}
	if lists.Load() != 3 {
		t.Fatalf("config change did not relist: %d", lists.Load())
	}
}
