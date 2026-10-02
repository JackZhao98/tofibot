package mcprunner

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ProtocolVersion = "2026-07-28"

var ErrIncompatibleProtocol = errors.New("MCP plugin is incompatible: Tofi requires protocol 2026-07-28 and does not support older versions")

// The SDK otherwise falls back from server/discover to the legacy initialize
// handshake. Reject that write before it reaches an installed plugin.
type latestOnlyTransport struct{ mcp.Transport }

func (t *latestOnlyTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &latestOnlyConnection{Connection: conn}, nil
}

type latestOnlyConnection struct{ mcp.Connection }

func (c *latestOnlyConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if request, ok := message.(*jsonrpc.Request); ok && request.Method == "initialize" {
		return ErrIncompatibleProtocol
	}
	return c.Connection.Write(ctx, message)
}
