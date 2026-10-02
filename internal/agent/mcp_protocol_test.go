package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentMCPRequiresLatestProtocol(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "current", version: latestMCPProtocolVersion},
		{name: "legacy", version: "2025-11-25", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"},
				&mcp.ServerOptions{SupportedProtocolVersions: []string{tc.version}})
			server.AddTool(&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
				})
			serverSession, err := server.Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()

			clientSession, err := connectStrictMCP(ctx, clientTransport)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), latestMCPProtocolVersion) {
					t.Fatalf("expected explicit latest protocol error, got session=%v err=%v", clientSession, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()
			if got := clientSession.InitializeResult().ProtocolVersion; got != latestMCPProtocolVersion {
				t.Fatalf("negotiated %s, want %s", got, latestMCPProtocolVersion)
			}
			tools, err := clientSession.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(convertTools(tools.Tools)); got != 1 {
				t.Fatalf("latest tools/list count=%d", got)
			}
			result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) != 1 {
				t.Fatalf("latest tools/call result=%v", result)
			}
			content, ok := result.Content[0].(*mcp.TextContent)
			if !ok || content.Text != "pong" {
				t.Fatalf("latest tools/call content=%v", result.Content[0])
			}
		})
	}
}
