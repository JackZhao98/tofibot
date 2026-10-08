package extensions

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestMCPReadOnlyTransientRetriesAreBoundedAndMutationIsNotReplayed(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "read", false: "write"}[readOnly], func(t *testing.T) {
			var effects, approvals atomic.Int32
			backend := fixtureServer("failure", "1")
			backend.AddTool(fixtureTool("action"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				effects.Add(1)
				return nil, &jsonrpc.Error{Code: -32603, Message: "synthetic lost response after success"}
			})
			remote := fixtureHTTPServer(backend)
			defer remote.Close()
			path := filepath.Join(t.TempDir(), "mcp.json")
			cfg := MCPServerConfig{URL: remote.URL}
			if readOnly {
				cfg.TrustedReadOnlyTools = []string{"action"}
			}
			data, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"fixture": cfg}})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			m := NewManager(Config{MCPConfigPath: path})
			prepared, err := m.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error { approvals.Add(1); return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			if _, err = discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"action"}`)); err != nil {
				t.Fatal(err)
			}
			_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__action","arguments":{}}`))
			o, ok := tooloutcome.FromError(err)
			if !ok {
				t.Fatalf("unclassified err=%v", err)
			}
			if readOnly {
				// Reads still pass the gate once (it owns review policy); retries never re-ask.
				if effects.Load() != 3 || approvals.Load() != 1 || o.Status != tooloutcome.Transient || o.RetryLimit != 2 {
					t.Fatalf("retry effects=%d approvals=%d outcome=%+v", effects.Load(), approvals.Load(), o)
				}
			} else if effects.Load() != 1 || approvals.Load() != 1 || o.Status != tooloutcome.Uncertain || o.NextAction != "verify_effect" {
				t.Fatalf("unsafe retry effects=%d outcome=%+v", effects.Load(), o)
			}
		})
	}
}

func TestMCPValidationPrecedesApprovalAndRemoteDispatch(t *testing.T) {
	params := map[string]any{"type": "object", "required": []string{"target"}, "properties": map[string]any{"target": map[string]any{"type": "string"}}, "additionalProperties": false}
	var effects, approvals atomic.Int32
	backend := fixtureServer("schema", "1")
	tool := fixtureTool("write")
	tool.InputSchema = params
	backend.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		effects.Add(1)
		return fixtureText("done"), nil
	})
	remote := fixtureHTTPServer(backend)
	defer remote.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	data, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"fixture": {URL: remote.URL}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{MCPConfigPath: path})
	prepared, err := m.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error { approvals.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err = discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"write"}`)); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{{}, {"target": 1}, {"target": "ok", "extra": true}} {
		raw, _ := json.Marshal(map[string]any{"name": "mcp_fixture__write", "arguments": args})
		_, err := discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), raw)
		o, ok := tooloutcome.FromError(err)
		if !ok || o.Status != tooloutcome.Validation || o.Certainty != "not_executed" {
			t.Fatalf("invalid args=%v outcome=%+v err=%v", args, o, err)
		}
	}
	if effects.Load() != 0 || approvals.Load() != 0 {
		t.Fatalf("validation caused effects=%d approvals=%d", effects.Load(), approvals.Load())
	}
	if _, err := discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__write","arguments":{"target":"ok"}}`)); err != nil {
		t.Fatal(err)
	}
	if effects.Load() != 1 || approvals.Load() != 1 {
		t.Fatalf("corrected call effects=%d approvals=%d", effects.Load(), approvals.Load())
	}
}
