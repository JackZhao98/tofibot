package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The remote's reason stays whole and first even when the schema is large.
func TestRemoteErrorReasonIsWholeAndBeforeSchema(t *testing.T) {
	reason := "Rejected: " + strings.Repeat("r", 300) + " END"
	var rejected bool
	srv := fixtureServer("notion", "1")
	tool := fixtureTool("notion-update-page", fixtureDescription("Update a page"))
	tool.InputSchema = notionLikeSchema(600)
	srv.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rejected = true
		return fixtureError(reason), nil
	})
	f := newNotionFixtureWithServer(t, srv)
	p := f.prepare(t)
	ctx := context.Background()
	if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"notion","query":"update"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := discoveryTool(t, p, "call_mcp_tool").Execute(ctx, json.RawMessage(validReplace))
	o, ok := tooloutcome.FromError(err)
	if !ok || !rejected {
		t.Fatalf("no outcome: %v", err)
	}
	if !strings.Contains(o.Message, mcpReportedMarker+" "+mcpReportedOpen+reason+mcpReportedClose) || o.RemoteError != reason {
		t.Fatalf("reason not whole and first: %.200s", o.Message)
	}
	if bounded := tooloutcome.Bounded(&o); !strings.Contains(bounded.Message, "END") {
		t.Fatal("stored copy lost the reason")
	}
}

func TestProtocolRejectionIsNotExecutedAndKeepsCode(t *testing.T) {
	invalid := &jsonrpc.Error{Code: -32602, Message: "minItems: array length 1 is less than 2\x00"}
	o, ok := mcpProtocolRejection(fmt.Errorf("calling tool: %w", invalid), 1)
	if !ok || o.Status != tooloutcome.Validation || o.Code != "mcp_invalid_params" || o.Certainty != "not_executed" || o.NextAction != "repair_arguments" || o.RPCCode != -32602 {
		t.Fatalf("%+v ok=%v", o, ok)
	}
	if !strings.Contains(o.Message, "minItems: array length 1 is less than 2") || strings.Contains(o.Message, "may already have happened") || strings.Contains(o.Message, "\x00") {
		t.Fatalf("message=%q", o.Message)
	}
	if o, ok := mcpProtocolRejection(&jsonrpc.Error{Code: -32601, Message: "no such method"}, 1); !ok || o.Code != "mcp_request_rejected" || o.Certainty != "not_executed" || o.RPCCode != -32601 {
		t.Fatalf("%+v", o)
	}
	// An internal error is ambiguous and stays with the caller's own handling.
	if _, ok := mcpProtocolRejection(&jsonrpc.Error{Code: -32603, Message: "boom"}, 1); ok {
		t.Fatal("internal error treated as a rejection")
	}
	withSchema := withMCPReportedSchema(invalid2(), map[string]any{"type": "object", "properties": map[string]any{"data_source_urls": map[string]any{"type": "array"}}})
	got, _ := tooloutcome.FromError(withSchema)
	reasonAt, schemaAt := strings.Index(got.Message, "minItems"), strings.Index(got.Message, "data_source_urls")
	if reasonAt < 0 || schemaAt < reasonAt {
		t.Fatalf("schema must follow reason: %s", got.Message)
	}
}

func invalid2() error {
	o, _ := mcpProtocolRejection(&jsonrpc.Error{Code: -32602, Message: "minItems: array length 1 is less than 2"}, 1)
	return o.Err()
}
