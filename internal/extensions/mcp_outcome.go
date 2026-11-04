package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"io"
	"net"
	"strings"
	"unicode"
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

// boundedMCPSchema is the schema as the model should see it in an error hint:
// top level first, compacted in steps (see schemaWithin) until it fits, and
// only as a last resort cut at a byte limit, which then loses "$defs" detail,
// never the top-level fields.
func boundedMCPSchema(params map[string]any) (string, error) {
	_, encoded, _, ok := schemaWithin(params, maxMCPSchemaHintBytes)
	if encoded == nil {
		data, err := json.Marshal(params)
		if err != nil {
			return "", err
		}
		encoded = data
	}
	schema := string(encoded)
	if !ok && len(schema) > maxMCPSchemaHintBytes {
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
	o := tooloutcome.New(tooloutcome.Validation, "schema_required", "not_executed", msg, "repair_arguments")
	// The name rides in the outcome: a large first call's stored arguments are
	// cut, and a resume reads this name to know the schema was shown.
	o.Tool = t.Name
	return o
}

// mcpReportedMarker opens the remote's own words. The words sit between
// mcpReportedOpen and mcpReportedClose, so a reader can tell where the
// remote's text stops and TOFI's own text resumes.
const (
	mcpReportedMarker = "Untrusted tool-reported details:"
	mcpReportedOpen   = "«"
	mcpReportedClose  = "»"
	mcpReportedEnd    = "End of untrusted details."
)

// mcpReportedBlock returns the remote's text as a delimited, bounded block.
// sanitizeRemoteText already removed the delimiter runes from the text.
func mcpReportedBlock(text string) string {
	return mcpReportedMarker + " " + mcpReportedOpen + text + mcpReportedClose + " " + mcpReportedEnd
}

// maxMCPRemoteErrorRunes bounds the remote's error text in an outcome.
const maxMCPRemoteErrorRunes = 1500

// sanitizeRemoteText drops control and Unicode format characters (bidi
// overrides, zero-width marks) and the block delimiters, and bounds the text
// by runes.
func sanitizeRemoteText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) || r == '«' || r == '»' {
			return -1
		}
		return r
	}, strings.TrimSpace(text))
	if r := []rune(text); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return text
}

// withMCPReportedSchema lets the model repair a call the remote rejected: the
// remote's own words stay first, and the current input schema follows them.
func withMCPReportedSchema(err error, params map[string]any) error {
	o, ok := tooloutcome.FromError(err)
	if !ok {
		return err
	}
	switch o.Code {
	case "mcp_invalid_params":
	case "mcp_reported_error", "mcp_result_unknown":
		if !strings.Contains(o.Message, mcpReportedMarker) {
			return err
		}
	default:
		return err
	}
	schema, marshalErr := boundedMCPSchema(params)
	if marshalErr != nil {
		return err
	}
	o.Message += " If the details name rejected arguments, repair them against the current input schema: " + schema
	return o.Err()
}

// mcpProtocolRejection classifies a JSON-RPC error the server returned for the
// request itself (malformed request, unknown method, invalid params). The
// server refused to run it, so nothing happened, for write tools as well.
func mcpProtocolRejection(err error, attempts int) (tooloutcome.Outcome, bool) {
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) {
		return tooloutcome.Outcome{}, false
	}
	detail := sanitizeRemoteText(rpc.Message, 600)
	var o tooloutcome.Outcome
	switch rpc.Code {
	case -32602:
		o = tooloutcome.New(tooloutcome.Validation, "mcp_invalid_params", "not_executed", fmt.Sprintf("The server rejected the arguments (JSON-RPC code %d); nothing was executed. %s", rpc.Code, mcpReportedBlock(detail)), "repair_arguments")
		o.RepairLimit = 3
	case -32600, -32601, -32700:
		o = tooloutcome.New(tooloutcome.Permanent, "mcp_request_rejected", "not_executed", fmt.Sprintf("The server rejected the request (JSON-RPC code %d); nothing was executed. %s", rpc.Code, mcpReportedBlock(detail)), "explain_blocker")
	default:
		return tooloutcome.Outcome{}, false
	}
	o.RPCCode, o.RemoteError, o.Attempts = int(rpc.Code), detail, attempts
	return o, true
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
