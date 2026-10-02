package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newAppMCPFixture serves only the current stateless MCP protocol. It is used
// by App acceptance tests so their synthetic servers exercise the same wire
// revision required from production connectors.
func newAppMCPFixture(t *testing.T, name string, tool *mcp.Tool, handler func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error)) *httptest.Server {
	t.Helper()
	backend := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"},
		&mcp.ServerOptions{SupportedProtocolVersions: []string{"2026-07-28"}})
	backend.AddTool(tool, handler)
	remote := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return backend },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(remote.Close)
	return remote
}

func newAppTextTool(name, description, property string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				property: map[string]any{"type": "string"},
			},
			"required": []string{property},
		},
	}
}
