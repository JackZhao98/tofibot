package extensions

import (
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"io"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// A nested schema with command-dependent fields is several KiB; cutting it
// at 4 KiB dropped fields the model needed to repair its arguments.
const maxMCPSchemaHintBytes = 16 << 10

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
	schema, marshalErr := boundedMCPSchema(params)
	if marshalErr != nil {
		return err
	}
	o.Message += " Current input schema: " + schema
	return o.Err()
}

func boundedMCPSchema(params map[string]any) (string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", err
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
	return schema, nil
}

// mcpSchemaRevealOutcome refuses a first call to a tool whose schema the model
// has not been shown, and carries the schema so the next call can follow it.
func mcpSchemaRevealOutcome(t runtime.Tool) tooloutcome.Outcome {
	msg := "Nothing was executed: this tool's input schema had not been shown in this run. Call it again with arguments that follow the schema exactly, including command-specific required fields."
	if schema, err := boundedMCPSchema(t.Parameters); err == nil {
		msg += " Input schema: " + schema
	}
	return tooloutcome.New(tooloutcome.Validation, "schema_required", "not_executed", msg, "repair_arguments")
}

// withMCPReportedSchema lets the model repair a call the remote rejected after
// dispatch: the remote's own words stay at the end as untrusted details, and
// the current input schema goes before them.
func withMCPReportedSchema(err error, params map[string]any) error {
	o, ok := tooloutcome.FromError(err)
	if !ok || (o.Code != "mcp_reported_error" && o.Code != "mcp_result_unknown") {
		return err
	}
	const marker = " Untrusted tool-reported details:"
	at := strings.Index(o.Message, marker)
	if at < 0 {
		return err
	}
	schema, marshalErr := boundedMCPSchema(params)
	if marshalErr != nil {
		return err
	}
	o.Message = o.Message[:at] + " If the details below say the arguments were rejected, repair them against the current input schema: " + schema + o.Message[at:]
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
