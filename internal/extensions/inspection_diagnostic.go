package extensions

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/JackZhao98/tofibot/internal/googleapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Return only fixed messages/codes. Provider errors may contain credentialed
// URLs or tokens, so no substring or suggested activation URL is reflected.
func mcpInspectionDiagnostic(name, endpoint string, err error) Diagnostic {
	d := Diagnostic{Server: name, Message: "MCP server connection or discovery failed"}
	if errors.Is(err, errMCPLegacySSE) {
		d.Code, d.Message = "unsupported_transport", "This service uses legacy MCP SSE, which cannot support 2026-07-28. Upgrade the service and switch it to Streamable HTTP."
		return d
	}
	if isMCPProtocolFailure(err) {
		d.Code, d.Message = "unsupported_protocol", "This MCP service does not support the 2026-07-28 protocol that Tofi requires. Upgrade the service; Tofi does not fall back to an older protocol."
		return d
	}
	if !isGoogleWorkspaceMCPEndpoint(endpoint) {
		return d
	}
	if err == nil {
		return d
	}
	// "API not enabled" is read from Google's structured error (ErrorInfo reason SERVICE_DISABLED), never from wording.
	if failure, ok := googleapi.ParseFailure(err.Error()); ok {
		d.Code, d.Message = "api_disabled", "Google reports the API is not enabled. Enable the product API and its MCP service in the same project as the OAuth client, then refresh the tools."
		// The link is built here from the endpoint's own API and the validated project, never from the error body.
		host := ""
		if parsed, perr := url.Parse(endpoint); perr == nil {
			host = parsed.Hostname()
		}
		d.API = googleMCPProductAPI(host, failure.API)
		d.EnableURL = googleapi.EnableURL(d.API, failure.Project)
		return d
	}
	// Google documents no machine-readable reason for a non-agentic OAuth
	// client, so this classifies by host plus the HTTP status the transport
	// saw, never by message text.
	var httpErr *mcpHTTPStatusError
	authStatus := errors.As(err, &httpErr) && (httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden)
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "access_denied"):
		d.Code, d.Message = "access_denied", "Google denied access. If the app is in testing mode, check that this account is a test user; an account or organization restriction is also possible."
	case authStatus, strings.Contains(message, "permission_denied"), strings.Contains(message, "status 403"), strings.Contains(message, "status code: 403"):
		d.Code, d.Message = "permission_denied", "Google refused to list the tools. First confirm the Cloud project has joined the Google Workspace Developer Preview Program, then confirm the OAuth client has \"Use this client for an AI-powered agent\" checked (from 2026-11-11 the Workspace MCP rejects unmarked clients; the setting is fixed after 11-12, so a new client is needed); finally check the API and MCP enablement and the account's permissions. A 401 or 403 alone cannot tell which one it is."
	}
	return d
}

// googleMCPProductAPI picks the API to enable: the one the error names when it is a known API, else the product API
// of the Workspace MCP endpoint that was called.
func googleMCPProductAPI(host, named string) string {
	if googleapi.KnownAPI(named) {
		return named
	}
	for service, mcpAPI := range googleapi.ServiceMCPAPI {
		if mcpAPI == host {
			return googleapi.ServiceAPI[service]
		}
	}
	return ""
}

// isGoogleWorkspaceMCPEndpoint is true only for the exact https Workspace MCP
// hosts, never for look-alike hosts, other ports or credentialed URLs.
func isGoogleWorkspaceMCPEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "gmailmcp.googleapis.com", "drivemcp.googleapis.com", "docsmcp.googleapis.com", "sheetsmcp.googleapis.com", "slidesmcp.googleapis.com", "calendarmcp.googleapis.com":
		return true
	}
	return false
}

// mcpSessionHTTP is what a session's transport saw, kept beside the session so
// a failed tool call can be classified by endpoint plus HTTP status.
type mcpSessionHTTP struct {
	endpoint string
	failed   *atomic.Int32
}

func (m *Manager) resetHTTPStatus(cli *mcp.ClientSession) {
	if v, ok := m.mcpStatus.Load(cli); ok {
		v.(*mcpSessionHTTP).failed.Store(0)
	}
}

// googleAuthRejection returns a fixed, person-facing next step when a Google
// Workspace MCP host answered a tool call with HTTP 401 or 403. Google
// documents no structured reason for either cause, so the classification is
// host plus status; the hint says the status cannot tell the causes apart.
func (m *Manager) googleAuthRejection(cli *mcp.ClientSession) string {
	v, ok := m.mcpStatus.Load(cli)
	if !ok {
		return ""
	}
	s := v.(*mcpSessionHTTP)
	if status := s.failed.Load(); !isGoogleWorkspaceMCPEndpoint(s.endpoint) || (status != http.StatusUnauthorized && status != http.StatusForbidden) {
		return ""
	}
	return "Google Workspace MCP rejected the call with an authorization status. Check in this order: (1) the Google Cloud project is enrolled in the Google Workspace Developer Preview Program; (2) the OAuth client is marked \"Use this client for an AI-powered agent\" (required from 2026-11-11, permanent on an existing client from 2026-11-12, so a new client is needed); (3) the product API and its MCP service are enabled, the scopes are granted and the account is a test user. The status alone cannot tell which one applies."
}
