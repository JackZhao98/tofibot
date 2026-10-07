package extensions

import (
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"io"
	"net"
	"unicode/utf8"
)

const maxMCPSchemaHintBytes = 4 << 10

func validateMCPArguments(params map[string]any, args map[string]any) error {
	return tooloutcome.ValidateArguments(params, args)
}

// withMCPSchemaHint lets the model repair arguments in one step: an
// invalid_arguments outcome carries the current input schema, bounded.
func withMCPSchemaHint(err error, params map[string]any) error {
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "invalid_arguments" {
		return err
	}
	encoded, marshalErr := json.Marshal(params)
	if marshalErr != nil {
		return err
	}
	schema := string(encoded)
	if len(schema) > maxMCPSchemaHintBytes {
		const marker = " …[schema truncated]"
		// Cut on a rune boundary in linear time; schemas can be large.
		cut := maxMCPSchemaHintBytes - len(marker)
		for cut > 0 && !utf8.RuneStart(schema[cut]) {
			cut--
		}
		schema = schema[:cut] + marker
	}
	o.Message += " Current input schema: " + schema
	return o.Err()
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
