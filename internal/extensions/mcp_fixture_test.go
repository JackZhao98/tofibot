package extensions

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These small builders keep schema fixtures readable while all wire behavior
// and tool handlers use the official SDK and the current protocol.
func fixtureServer(name, version string, pageSize ...int) *mcp.Server {
	options := &mcp.ServerOptions{SupportedProtocolVersions: []string{requiredMCPProtocolVersion}, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}}
	if len(pageSize) > 0 {
		options.PageSize = pageSize[0]
	}
	return mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, options)
}
func fixtureHTTPHandler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}
func fixtureHTTPServer(server *mcp.Server) *httptest.Server {
	return httptest.NewServer(fixtureHTTPHandler(server))
}
func fixtureSSEServer(server *mcp.Server) *httptest.Server {
	return httptest.NewServer(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil))
}

type fixtureToolOption func(*mcp.Tool)

func fixtureTool(name string, options ...fixtureToolOption) *mcp.Tool {
	tool := &mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
	for _, option := range options {
		option(tool)
	}
	return tool
}
func fixtureDescription(description string) fixtureToolOption {
	return func(tool *mcp.Tool) { tool.Description = description }
}
func fixtureString(name string, options ...func(map[string]any)) fixtureToolOption {
	return func(tool *mcp.Tool) {
		property := map[string]any{"type": "string"}
		for _, option := range options {
			option(property)
		}
		tool.InputSchema.(map[string]any)["properties"].(map[string]any)[name] = property
	}
}
func fixtureParameterDescription(description string) func(map[string]any) {
	return func(property map[string]any) { property["description"] = description }
}
func fixtureText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
func fixtureError(text string) *mcp.CallToolResult {
	result := fixtureText(text)
	result.IsError = true
	return result
}
func fixtureNoop(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return fixtureText(""), nil
}
