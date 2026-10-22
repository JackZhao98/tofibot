package extensions

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const requiredMCPProtocolVersion = "2026-07-28"

var errMCPProtocolIncompatible = errors.New("MCP protocol incompatible: Tofi requires 2026-07-28; upgrade this server to support server/discover")
var errMCPLegacySSE = errors.New("Legacy MCP SSE transport is incompatible with 2026-07-28; switch this server to Streamable HTTP")

func isMCPProtocolFailure(err error) bool {
	var rpc *jsonrpc.Error
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "server/discover") && (strings.Contains(message, "not found") || strings.Contains(message, "method not allowed") || strings.Contains(message, "upgrade required")) {
			return true
		}
	}
	return errors.Is(err, errMCPProtocolIncompatible) || errors.As(err, &rpc) && (rpc.Code == jsonrpc.CodeMethodNotFound || rpc.Code == mcp.CodeUnsupportedProtocolVersion)
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type mcpHeaderTransport struct {
	headers map[string]string
	base    http.RoundTripper
	// failed remembers the last non-2xx HTTP status so a failed connect can be
	// classified by status, never by the SDK's or the provider's message text.
	failed *atomic.Int32
}

// mcpHTTPStatusError carries the HTTP status of the failing response next to
// the SDK error. Only the number is kept; no provider text.
type mcpHTTPStatusError struct {
	Status int
	Err    error
}

func (e *mcpHTTPStatusError) Error() string { return e.Err.Error() }
func (e *mcpHTTPStatusError) Unwrap() error { return e.Err }

func (t mcpHeaderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	for k, v := range t.headers {
		// Protocol and session headers belong to the SDK and cannot be overridden
		// by saved user configuration, including an old protocol header.
		switch http.CanonicalHeaderKey(k) {
		case "Mcp-Protocol-Version", "Mcp-Session-Id":
			continue
		}
		// A fresh OAuth bearer from the official transport takes priority.
		if http.CanonicalHeaderKey(k) == "Authorization" && req.Header.Get(k) != "" {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp != nil && t.failed != nil && resp.StatusCode >= 400 {
		t.failed.Store(int32(resp.StatusCode))
	}
	return resp, err
}
