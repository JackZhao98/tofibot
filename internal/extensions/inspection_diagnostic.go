package extensions

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Return only fixed messages/codes. Provider errors may contain credentialed
// URLs or tokens, so no substring or suggested activation URL is reflected.
func mcpInspectionDiagnostic(name, endpoint string, err error) Diagnostic {
	d := Diagnostic{Server: name, Message: "MCP server connection or discovery failed"}
	if errors.Is(err, errMCPLegacySSE) {
		d.Code, d.Message = "unsupported_transport", "该服务使用旧式 MCP SSE，无法支持 2026-07-28。请将服务升级并改为 Streamable HTTP。"
		return d
	}
	if isMCPProtocolFailure(err) {
		d.Code, d.Message = "unsupported_protocol", "该 MCP 服务不支持 Tofi 要求的 2026-07-28 协议。请升级服务；Tofi 不会降级到旧协议。"
		return d
	}
	if !isGoogleWorkspaceMCPEndpoint(endpoint) {
		return d
	}
	if err == nil {
		return d
	}
	// Google documents no machine-readable reason for a non-agentic OAuth
	// client, so this classifies by host plus the HTTP status the transport
	// saw, never by message text.
	var httpErr *mcpHTTPStatusError
	authStatus := errors.As(err, &httpErr) && (httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden)
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "service_disabled"), strings.Contains(message, "accessnotconfigured"), strings.Contains(message, "has not been used in project") && strings.Contains(message, "is disabled"):
		d.Code, d.Message = "api_disabled", "Google 返回 API 未启用。请在 OAuth Client 所属的同一个项目中启用产品 API 和 MCP 服务，再刷新工具。"
	case strings.Contains(message, "access_denied"):
		d.Code, d.Message = "access_denied", "Google 拒绝了访问。若应用处于测试模式，请检查当前账号是否在测试用户中；也可能是账号或组织限制。"
	case authStatus, strings.Contains(message, "permission_denied"), strings.Contains(message, "status 403"), strings.Contains(message, "status code: 403"):
		d.Code, d.Message = "permission_denied", "Google 拒绝读取工具。请先确认 Cloud 项目已加入 Google Workspace Developer Preview Program，再确认 OAuth Client 已勾选“Use this client for an AI-powered agent”（2026-11-11 起 Workspace MCP 拒绝未标记的 Client；该设置在 11-12 后固定，需新建 Client）；最后检查 API / MCP 启用状态和账号权限。401 / 403 本身不能确定是哪一项。"
	}
	return d
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
