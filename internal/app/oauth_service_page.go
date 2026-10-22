package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Provider artwork and configuration links come from known endpoints, never
// from callback parameters or provider-supplied error URLs.
func oauthServicePage(identity extensions.OAuthServiceIdentity) oauthCompletionPage {
	page := oauthCompletionPage{ServiceName: "外部服务", LogoURL: "/oauth/provider-generic.svg"}
	if identity.Name == "" {
		return page
	}
	page.ServiceName = identity.Name
	u, err := url.Parse(identity.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return page
	}
	if u.Hostname() == "mcp.notion.com" && strings.TrimSuffix(u.Path, "/") == "/mcp" {
		page.ServiceName, page.LogoURL, page.DocsURL = "Notion", "/oauth/provider-notion.svg", "https://www.notion.com/help/notion-mcp"
		return page
	}
	if strings.TrimSuffix(u.Path, "/") != "/mcp/v1" {
		return page
	}
	products := map[string]struct{ name, api, product string }{
		"gmailmcp.googleapis.com":    {"Google Gmail", "gmail.googleapis.com", "gmail/api"},
		"drivemcp.googleapis.com":    {"Google Drive", "drive.googleapis.com", "drive/api"},
		"docsmcp.googleapis.com":     {"Google Docs", "docs.googleapis.com", "docs/api"},
		"sheetsmcp.googleapis.com":   {"Google Sheets", "sheets.googleapis.com", "sheets/api"},
		"slidesmcp.googleapis.com":   {"Google Slides", "slides.googleapis.com", "slides/api"},
		"calendarmcp.googleapis.com": {"Google Calendar", "calendar-json.googleapis.com", "calendar/api"},
	}
	if product, ok := products[u.Hostname()]; ok {
		page.ServiceName, page.LogoURL = product.name, "/oauth/provider-google.svg"
		page.APIURL = "https://console.cloud.google.com/apis/enableflow;apiid=" + product.api
		page.MCPURL = "https://console.cloud.google.com/apis/enableflow;apiid=" + u.Hostname()
		page.DocsURL = "https://developers.google.com/workspace/" + product.product + "/guides/configure-mcp-server"
	}
	return page
}

// oauthVerifiedPage turns the tool listing taken right after a successful authorization into what the page says.
// Every sentence comes from fixed server text (inspection diagnostics carry no provider text), never from the
// callback. A working connection gets its result and nothing else; setup help appears only when the diagnostic names
// what is missing.
func oauthVerifiedPage(page *oauthCompletionPage, inspection extensions.MCPInspection) {
	page.State = "success"
	if len(inspection.Diagnostics) == 0 {
		if inspection.ToolCount == 0 {
			page.Message = "已连接，但这个服务暂时没有列出工具。"
		} else {
			page.Message = fmt.Sprintf("已连接，读取到 %d 个工具，可以使用了。", inspection.ToolCount)
		}
		return
	}
	diagnostic := inspection.Diagnostics[0]
	switch {
	case inspection.AuthRequired:
		page.Message = "授权已保存，但服务还不接受这次登录。请回到 Tofi 重新授权。"
	case diagnostic.Code == "api_disabled" || diagnostic.Code == "permission_denied":
		page.Setup = "enable"
		page.Message = "授权已保存，但工具还不能用。" + diagnostic.Message
	case diagnostic.Code == "access_denied":
		page.Setup = "audience"
		page.Message = "授权已保存，但工具还不能用。" + diagnostic.Message
	default:
		page.Message = "授权已保存，但暂时读取不到工具。" + diagnostic.Message
	}
}
