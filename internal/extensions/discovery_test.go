package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func discoveryTool(t *testing.T, p *Prepared, name string) runtime.Tool {
	t.Helper()
	for _, tool := range p.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing %s", name)
	return runtime.Tool{}
}

func decodeSearchResult(t *testing.T, raw string) (tools []struct {
	Name string `json:"name"`
}, truncated bool) {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode search result: %v", err)
	}
	return result.Tools, result.Truncated
}

func TestDiscoverableStartupDoesNotConnectOrIndexEverything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"slow":{"url":"http://127.0.0.1:1/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(Config{MCPConfigPath: path})
	start := time.Now()
	p, err := mgr.PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("startup performed remote work: %v", time.Since(start))
	}
	if len(p.Diagnostics) != 0 {
		t.Fatalf("startup diagnostics=%v", p.Diagnostics)
	}
	if !strings.Contains(p.Instructions, "slow") {
		t.Fatal("configured source missing from capability directory")
	}
	if _, err := discoveryTool(t, p, "list_mcp_servers").Execute(context.Background(), json.RawMessage(`{"offset":0}`)); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverableServerListPagination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	var b strings.Builder
	b.WriteString(`{"mcpServers":{`)
	for i := 0; i < 55; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"s%03d":{"url":"http://127.0.0.1:1/mcp"}`, i)
	}
	b.WriteString(`}}`)
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewManager(Config{MCPConfigPath: path}).PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, err := discoveryTool(t, p, "list_mcp_servers").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"next_offset":50`) || len(out) > maxDiscoveryBytes {
		t.Fatalf("pagination=%s", out)
	}
}

func TestDiscoverableMCPAuthorizationAndTransports(t *testing.T) {
	for _, transport := range []string{"streamable_http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			var calls atomic.Int32
			srv := fixtureServer("fixture", "1")
			for _, name := range []string{"echo", "unmatched", "denied", "otherbot"} {
				srv.AddTool(fixtureTool(name, fixtureDescription("fixture "+name), fixtureString("value")), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls.Add(1)
					return fixtureText(req.Params.Name), nil
				})
			}
			httpSrv := fixtureHTTPServer(srv)
			if transport == "sse" {
				httpSrv.Close()
				httpSrv = fixtureSSEServer(srv)
			}
			defer httpSrv.Close()
			url := httpSrv.URL
			if transport == "sse" {
				url += "/sse"
			}
			mgr := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			if err := mgr.SaveMCP("fixture", MCPServerConfig{URL: url, Transport: transport, ToolDenylist: []string{"denied"}, BotAllowlists: map[string][]string{"bot": {"echo", "unmatched", "denied"}}}, false); err != nil {
				t.Fatal(err)
			}
			p, err := mgr.PrepareDiscoverableForBot(context.Background(), "bot")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if len(p.Diagnostics) > 0 || len(p.Tools) != 7 { // list/catalog/schema/call + 3 skill tools
				t.Fatalf("diagnostics=%v tools=%v", p.Diagnostics, p.Tools)
			}
			search := discoveryTool(t, p, "search_mcp_tools")
			call := discoveryTool(t, p, "call_mcp_tool")
			if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__echo","arguments":{}}`)); err == nil {
				t.Fatal("unseen call allowed")
			}
			got, err := search.Execute(context.Background(), json.RawMessage(`{"query":"echo"}`))
			if transport == "sse" {
				if err != nil || !strings.Contains(got, "unsupported_transport") || strings.Contains(got, "input_schema") || calls.Load() != 0 {
					t.Fatalf("legacy search=%s err=%v calls=%d", got, err, calls.Load())
				}
				return
			}
			if err != nil || !strings.Contains(got, "input_schema") || strings.Contains(got, "unmatched") {
				t.Fatalf("search=%s err=%v", got, err)
			}
			got, err = call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__echo","arguments":{}}`))
			if err != nil || got != "echo" {
				t.Fatalf("call=%s err=%v", got, err)
			}
			for _, name := range []string{"denied", "otherbot", "unmatched"} {
				got, err = search.Execute(context.Background(), json.RawMessage(`{"query":"`+name+`"}`))
				if err != nil || (name != "denied" && !strings.Contains(got, "mcp_fixture__")) || (name == "denied" && strings.Contains(got, "mcp_fixture__")) {
					t.Fatalf("global search=%s err=%v", got, err)
				}
			}
			if _, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__unmatched","arguments":{}}`)); err != nil {
				t.Fatalf("global unmatched call failed: %v", err)
			}
			if calls.Load() != 2 {
				t.Fatalf("remote calls=%d", calls.Load())
			}
			other, err := mgr.PrepareDiscoverableForBot(context.Background(), "other")
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			got, err = discoveryTool(t, other, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"query":"echo"}`))
			if err != nil || !strings.Contains(got, "mcp_fixture__") {
				t.Fatalf("global access=%s %v", got, err)
			}
		})
	}
}

func TestDiscoverableReusesValidatedRecentSchemaWithoutListingTools(t *testing.T) {
	var listCalls, toolCalls atomic.Int32
	backend := fixtureServer("cache-fixture", "1")
	backend.AddTool(fixtureTool("echo", fixtureDescription("Echo fixture text")), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolCalls.Add(1)
		return fixtureText(req.Params.Name), nil
	})
	handler := fixtureHTTPHandler(backend)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if bytes.Contains(body, []byte(`"method":"tools/list"`)) {
			listCalls.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()

	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	first, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	search := discoveryTool(t, first, "search_mcp_tools")
	listing, err := search.Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"echo"}`))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Tools []CachedMCPTool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(listing), &record); err != nil || len(record.Tools) != 1 || record.Tools[0].SchemaVersion == "" {
		t.Fatalf("cached schema record=%+v err=%v", record, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	listed := listCalls.Load()
	if listed == 0 {
		t.Fatal("first discovery did not list tools")
	}

	second, err := manager.PrepareDiscoverableForBotWithCachedTools(context.Background(), "bot", record.Tools)
	if err != nil {
		t.Fatal(err)
	}
	call := discoveryTool(t, second, "call_mcp_tool")
	got, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__echo","arguments":{}}`))
	if err != nil || got != "echo" || toolCalls.Load() != 1 {
		t.Fatalf("cached call=%q err=%v calls=%d", got, err, toolCalls.Load())
	}
	if listCalls.Load() != listed {
		t.Fatalf("cached call repeated tools/list: before=%d after=%d", listed, listCalls.Load())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: remote.URL, ToolDenylist: []string{"echo"}}, true); err != nil {
		t.Fatal(err)
	}
	revoked, err := manager.PrepareDiscoverableForBotWithCachedTools(context.Background(), "bot", record.Tools)
	if err != nil {
		t.Fatal(err)
	}
	defer revoked.Close()
	_, err = discoveryTool(t, revoked, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__echo","arguments":{}}`))
	if err == nil || !strings.Contains(err.Error(), "recent MCP schema was not accepted") {
		t.Fatalf("revoked cached schema error=%v", err)
	}
	if listCalls.Load() != listed || toolCalls.Load() != 1 {
		t.Fatalf("revoked cache touched remote: lists=%d calls=%d", listCalls.Load(), toolCalls.Load())
	}
}

func TestDiscoverableSearchBoundsValidationAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tools []runtime.Tool
	for i := 0; i < 12; i++ {
		tools = append(tools, runtime.Tool{Name: strings.Repeat("x", i+1), Description: "fixture", Parameters: map[string]any{"description": strings.Repeat("s", 5000)}})
	}
	wrapped := discoverableMCPTools(ctx, tools)
	for _, raw := range []string{`{}`, `null`, `{"query":" "}`, `{"query":"x","limit":null}`, `{"query":"x","limit":0}`, `{"query":"x","limit":11}`, `{"query":"x","limit":1.5}`, `{"query":"x","unknown":true}`, `{"query":"x"} {}`} {
		if _, err := wrapped[0].Execute(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	out, err := wrapped[0].Execute(context.Background(), json.RawMessage(`{"query":"fixture","limit":10}`))
	if err != nil || len(out) > maxDiscoveryBytes || !strings.Contains(out, `"truncated":true`) {
		t.Fatalf("bounds len=%d err=%v", len(out), err)
	}
	if _, err := wrapped[1].Execute(context.Background(), json.RawMessage(`{"name":"xxxxxxxxxxxx","arguments":{}}`)); err == nil {
		t.Fatal("truncated result callable")
	}
	cancel()
	if _, err := wrapped[0].Execute(context.Background(), json.RawMessage(`{"query":"fixture"}`)); err == nil {
		t.Fatal("search ignored run cancellation")
	}
}

func TestDiscoverableSearchThousandToolsKeepsOutputBounded(t *testing.T) {
	tools := make([]runtime.Tool, 1000)
	for i := range tools {
		tools[i] = runtime.Tool{
			Name:        fmt.Sprintf("archive_record_%04d", i),
			Description: fmt.Sprintf("Read archived record number %d", i),
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"record": map[string]any{"type": "string"}}},
		}
	}
	search := discoverableMCPTools(context.Background(), tools)[0]
	got, err := search.Execute(context.Background(), json.RawMessage(`{"query":"archive record","limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	results, truncated := decodeSearchResult(t, got)
	if len(got) > maxDiscoveryBytes || len(results) != 10 || !truncated {
		t.Fatalf("1000-tool response len=%d result_count=%d truncated=%v", len(got), len(results), truncated)
	}
}

func TestDiscoverableSkillBodyLoadedOnlyOnRead(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(Config{SkillsDir: dir})
	manifest := "---\nname: alpha\ndescription: useful skill\n---\n"
	if err := mgr.InstallSkill("alpha", map[string][]byte{"SKILL.md": []byte(manifest + "ORIGINAL_PRIVATE_BODY"), "guide.txt": []byte("supporting")}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetSkillEnabled("bot", "alpha", true); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if strings.Contains(p.Instructions, "PRIVATE_BODY") || !strings.Contains(p.Instructions, "alpha") {
		t.Fatalf("instructions=%s", p.Instructions)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte(manifest+"FRESH_BODY"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := discoveryTool(t, p, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
	if err != nil || got != "FRESH_BODY" {
		t.Fatalf("lazy body=%q %v", got, err)
	}
	got, err = discoveryTool(t, p, "read_skill_file").Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"guide.txt"}`))
	if err != nil || got != "supporting" {
		t.Fatalf("support=%q %v", got, err)
	}
	other, err := mgr.PrepareDiscoverableForBot(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if !strings.Contains(other.Instructions, "alpha") {
		t.Fatal("global skill metadata missing")
	}
	if _, err := discoveryTool(t, other, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`)); err != nil {
		t.Fatalf("global skill read failed: %v", err)
	}
}

func TestDiscoverableInvokeCancellationAndRunIsolation(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	available := []runtime.Tool{{Name: "waiting", Description: "wait", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}}}
	tools := discoverableMCPTools(runCtx, available)
	if _, err := tools[0].Execute(context.Background(), json.RawMessage(`{"query":"wait"}`)); err != nil {
		t.Fatal(err)
	}
	other := discoverableMCPTools(context.Background(), available)
	if _, err := other[1].Execute(context.Background(), json.RawMessage(`{"name":"waiting","arguments":{}}`)); err == nil {
		t.Fatal("discovery leaked across runs")
	}
	done := make(chan error, 1)
	go func() {
		_, err := tools[1].Execute(context.Background(), json.RawMessage(`{"name":"waiting","arguments":{}}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("call never entered")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight call did not cancel")
	}
}
