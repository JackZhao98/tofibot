package extensions

import (
	"errors"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"io"
	"net"
)

func validateMCPArguments(params map[string]any, args map[string]any) error {
	return tooloutcome.ValidateArguments(params, args)
}

func transientMCPError(err error) bool {
	var rpc *jsonrpc.Error
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &network) && network.Timeout()) || (errors.As(err, &rpc) && rpc.Code == -32603)
}

func uncertainMCPOutcome(attempts int) tooloutcome.Outcome {
	o := tooloutcome.New(tooloutcome.Uncertain, "mcp_result_unknown", "unknown", "MCP tool call failed or returned an error after dispatch. The action may already have happened. Verify the target state before proposing any retry; this approval cannot be reused.", "verify_effect")
	o.Attempts = attempts
	return o
}
