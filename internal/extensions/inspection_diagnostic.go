package extensions

import (
	"errors"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/googleapi"
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
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return d
	}
	switch u.Hostname() {
	case "gmailmcp.googleapis.com", "drivemcp.googleapis.com", "docsmcp.googleapis.com", "sheetsmcp.googleapis.com", "slidesmcp.googleapis.com", "calendarmcp.googleapis.com":
	default:
		return d
	}
	if err == nil {
		return d
	}
	// "API not enabled" is read from Google's structured error (ErrorInfo reason SERVICE_DISABLED), never from wording.
	if failure, ok := googleapi.ParseFailure(err.Error()); ok {
		d.Code, d.Message = "api_disabled", "Google 返回 API 未启用。请在 OAuth Client 所属的同一个项目中启用产品 API 和 MCP 服务，再刷新工具。"
		// The link is built here from the endpoint's own API and the validated project, never from the error body.
		d.API = googleMCPProductAPI(u.Hostname(), failure.API)
		d.EnableURL = googleapi.EnableURL(d.API, failure.Project)
		return d
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "access_denied"):
		d.Code, d.Message = "access_denied", "Google 拒绝了访问。若应用处于测试模式，请检查当前账号是否在测试用户中；也可能是账号或组织限制。"
	case strings.Contains(message, "permission_denied"), strings.Contains(message, "status 403"), strings.Contains(message, "status code: 403"):
		d.Code, d.Message = "permission_denied", "Google 拒绝读取工具。请检查 API / MCP 启用状态、预览资格和账号权限；403 本身不能确定是哪一项。"
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
