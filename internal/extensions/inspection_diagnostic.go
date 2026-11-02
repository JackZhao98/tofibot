package extensions

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"

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
		d.Code, d.Message = "unsupported_protocol", "This MCP service offers no protocol version that Tofi supports (2024-11-05 through 2026-07-28). Upgrade the service."
		return d
	}
	if errors.Is(err, ErrBlockedAddress) {
		d.Code, d.Message = "endpoint_not_allowed", "This server may not connect to that address. Only a server Admin can use a private or local address, or the operator can allow it by setting TOFI_ALLOW_PRIVATE_ENDPOINTS=1. Metadata and link-local addresses are never allowed."
		return d
	}
	if isLANEndpoint(endpoint) {
		if code, message := lanDiagnostic(endpoint, err); code != "" {
			d.Code, d.Message = code, message
			return d
		}
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

// mcpSessionHTTP is the endpoint of a session, kept beside it so a failed tool
// call can be classified by endpoint plus the call's own HTTP status.
type mcpSessionHTTP struct {
	endpoint string
}

// mcpSessionEndpoint is the endpoint recorded for a live session, or "". Read it
// before a call: a rejected tools/call can make the SDK close the session, and the
// session's Wait goroutine then drops the entry before the failure is classified.
func (m *Manager) mcpSessionEndpoint(cli *mcp.ClientSession) string {
	if v, ok := m.mcpStatus.Load(cli); ok {
		return v.(*mcpSessionHTTP).endpoint
	}
	return ""
}

// googleAuthRejection returns a fixed, person-facing next step when a Google
// Workspace MCP host answered a tool call with HTTP 401 or 403. Google
// documents no structured reason for either cause, so the classification is
// host plus status; the hint says the status cannot tell the causes apart.
// endpoint is the one captured before the call (see mcpSessionEndpoint).
func googleAuthRejection(endpoint string, call *mcpCallStatus) string {
	if status := call.status.Load(); !isGoogleWorkspaceMCPEndpoint(endpoint) || (status != http.StatusUnauthorized && status != http.StatusForbidden) {
		return ""
	}
	return "Google Workspace MCP rejected the call with an authorization status. Check in this order: (1) the Google Cloud project is enrolled in the Google Workspace Developer Preview Program; (2) the OAuth client is marked \"Use this client for an AI-powered agent\" (required from 2026-11-11, permanent on an existing client from 2026-11-12, so a new client is needed); (3) the product API and its MCP service are enabled, the scopes are granted and the account is a test user. The status alone cannot tell which one applies."
}

// isLANEndpoint is true for an address that lives on the person's own network: a literal private, loopback,
// link-local or CGNAT address, a ".local" (mDNS) name, or a single-label host such as "homeassistant". Only these get
// the LAN reasons below; a public host keeps the one generic failure, so a public service's status text is never
// guessed at.
func isLANEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || BlockedAddr(ip)
	}
	return strings.HasSuffix(host, ".local") || !strings.Contains(host, ".")
}

func isMDNSHost(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && strings.HasSuffix(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), ".local")
}

// lanDiagnostic names why a LAN endpoint could not be used. The reason is read from the error's type (resolver error,
// connection errno, timeout) or from the HTTP status the transport saw, never from message text, and the sentence is
// fixed: nothing the device or the provider sent is reflected. Codes: lan_mdns_unresolved, lan_host_not_found,
// lan_connection_refused, lan_unreachable, lan_timeout, lan_unauthorized, lan_forbidden, lan_not_found.
func lanDiagnostic(endpoint string, err error) (string, string) {
	var httpErr *mcpHTTPStatusError
	if errors.As(err, &httpErr) {
		switch httpErr.Status {
		case http.StatusUnauthorized:
			return "lan_unauthorized", "The device refused the access token. Create a new long-lived access token in Home Assistant (your profile, Security) and enter it again."
		case http.StatusForbidden:
			return "lan_forbidden", "The device accepted the token but refused the request. In Home Assistant, the MCP Server may be limited to administrator accounts: use a token from an administrator."
		case http.StatusNotFound:
			return "lan_not_found", "The device answered, but there is no MCP endpoint at this path. In Home Assistant, add the Model Context Protocol Server integration, and check the address ends in /api/mcp."
		}
		return "", ""
	}
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		if isMDNSHost(endpoint) {
			return "lan_mdns_unresolved", "This server could not resolve a .local name. mDNS names often do not resolve inside the container Tofi runs in. Use the device's IP address instead, for example http://192.168.1.20:8123/api/mcp."
		}
		return "lan_host_not_found", "This server could not find that host name. Check the spelling, or use the device's IP address."
	case errors.Is(err, syscall.ECONNREFUSED):
		return "lan_connection_refused", "The device refused the connection. Check the address and port (Home Assistant listens on 8123) and that it is running."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "lan_unreachable", "Tofi's server has no route to that address. It must run on, or be able to reach, the same network as the device."
	case errors.Is(err, context.DeadlineExceeded), isNetTimeout(err):
		return "lan_timeout", "The device did not answer in time. Check the address, that the device is on, and that Tofi's server can reach your network."
	}
	return "", ""
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
